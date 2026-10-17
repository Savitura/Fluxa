package batch

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/server/idempotency"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// fakeService implements Service, counting how many times CreateBatch
// actually runs so tests can tell a replayed idempotent response apart from
// a reprocessed one. createErr, when set, is returned instead of a result.
type fakeService struct {
	mu         sync.Mutex
	createHits int
	createErr  error
}

func (f *fakeService) CreateBatch(_ context.Context, fromWalletID string, items []Item) (*Result, error) {
	f.mu.Lock()
	f.createHits++
	err := f.createErr
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	return &Result{
		Batch: &domain.BatchTransfer{
			ID:         "batch-1",
			Status:     domain.BatchStatusPending,
			TotalCount: len(items),
			CreatedAt:  now,
			UpdatedAt:  now,
		},
	}, nil
}

func (f *fakeService) GetBatch(_ context.Context, id string) (*Result, error) {
	return nil, domain.ErrBatchNotFound
}

func (f *fakeService) ExportCSV(_ context.Context, id string) (string, error) {
	return "", domain.ErrBatchNotFound
}

func (f *fakeService) hits() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.createHits
}

// mockIdemRepo is a minimal in-memory idempotency.Repository, matching the
// semantics exercised in internal/server/idempotency's own middleware
// tests: the first TryAcquire for a (org, key) pair wins and starts
// "processing"; later ones see that record until Complete overwrites it.
type mockIdemRepo struct {
	mu      sync.Mutex
	records map[string]*idempotency.Record
}

func newMockIdemRepo() *mockIdemRepo {
	return &mockIdemRepo{records: map[string]*idempotency.Record{}}
}

func (m *mockIdemRepo) Acquire(_ context.Context, orgID string, mode domain.Mode, key, requestHash string, now, leaseExpiresAt, recordExpiresAt time.Time, _ bool) (idempotency.Acquisition, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := orgID + ":" + string(mode) + ":" + key
	if rec, ok := m.records[k]; ok {
		if rec.RequestHash != requestHash {
			return idempotency.Acquisition{State: idempotency.BodyMismatch, Record: *rec}, nil
		}
		if rec.Status == idempotency.StatusComplete {
			return idempotency.Acquisition{State: idempotency.Replay, Record: *rec}, nil
		}
		return idempotency.Acquisition{State: idempotency.InProgress, Record: *rec}, nil
	}
	rec := &idempotency.Record{ID: uuid.NewString(), OrgID: orgID, Mode: mode, Key: key, RequestHash: requestHash, Status: idempotency.StatusProcessing, LeaseToken: uuid.NewString(), LeaseExpiresAt: leaseExpiresAt, ExpiresAt: recordExpiresAt}
	m.records[k] = rec
	return idempotency.Acquisition{State: idempotency.Acquired, Record: *rec}, nil
}

func (m *mockIdemRepo) Complete(_ context.Context, recordID, leaseToken string, response idempotency.Response, recordExpiresAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range m.records {
		if rec.ID == recordID && rec.LeaseToken == leaseToken {
			rec.Status = idempotency.StatusComplete
			rec.ResponseStatus = response.Status
			rec.ResponseBody = response.Body
			rec.ResponseHeaders = response.Headers
			rec.ExpiresAt = recordExpiresAt
			rec.LeaseToken = ""
			rec.LeaseExpiresAt = time.Time{}
		}
	}
	return nil
}

// DeleteExpired removes records whose retention window has elapsed.
func (m *mockIdemRepo) DeleteExpired(_ context.Context, batchSize int) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var deleted int64
	for k, rec := range m.records {
		if !rec.ExpiresAt.IsZero() && time.Now().After(rec.ExpiresAt) {
			delete(m.records, k)
			deleted++
			if int(deleted) >= batchSize {
				break
			}
		}
	}
	return deleted, nil
}

func newBatchRouter(svc Service, repo idempotency.Repository) http.Handler {
	h := NewHandler(svc).WithIdempotency(idempotency.RequiredMiddleware(repo))
	r := chi.NewRouter()
	r.Route("/", h.Routes())
	return r
}

func newBatchRequest(t *testing.T, key string) *http.Request {
	t.Helper()
	body := `{"from_wallet_id":"11111111-1111-4111-8111-111111111111","transfers":[{"to_wallet_id":"22222222-2222-4222-8222-222222222222","asset":"USDC","amount":"10"}]}`
	ctx := tenant.WithID(context.Background(), "org-1")
	ctx = tenant.WithMode(ctx, domain.ModeLive)
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body)).WithContext(ctx)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return req
}

func TestCreateBatchWithoutIdempotencyKeyIsRejected(t *testing.T) {
	svc := &fakeService{}
	router := newBatchRouter(svc, newMockIdemRepo())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, newBatchRequest(t, ""))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	if svc.hits() != 0 {
		t.Fatalf("expected CreateBatch not to run, ran %d times", svc.hits())
	}
}

func TestCreateBatchDuplicateKeyReplaysCachedResponseWithoutReprocessing(t *testing.T) {
	svc := &fakeService{}
	router := newBatchRouter(svc, newMockIdemRepo())
	key := uuid.New().String()

	first := httptest.NewRecorder()
	router.ServeHTTP(first, newBatchRequest(t, key))
	second := httptest.NewRecorder()
	router.ServeHTTP(second, newBatchRequest(t, key))

	if svc.hits() != 1 {
		t.Fatalf("expected CreateBatch to run exactly once, ran %d times", svc.hits())
	}
	if first.Code != second.Code || first.Body.String() != second.Body.String() {
		t.Fatalf("expected identical replayed response, got %d %q vs %d %q",
			first.Code, first.Body.String(), second.Code, second.Body.String())
	}
}

// TestCreateBatchNormalizesClientSuppliedKey asserts the documented contract
// for the Idempotency-Key header: any stable client-supplied string is mapped
// to a deterministic UUID, so opaque reference ids (not just UUIDs) dedupe.
func TestCreateBatchNormalizesClientSuppliedKey(t *testing.T) {
	svc := &fakeService{}
	router := newBatchRouter(svc, newMockIdemRepo())

	first := httptest.NewRecorder()
	router.ServeHTTP(first, newBatchRequest(t, "not-a-uuid"))

	if first.Code != http.StatusAccepted {
		t.Fatalf("expected 202 for a client-supplied reference key, got %d: %s", first.Code, first.Body.String())
	}

	// The same raw key must replay rather than create a second batch.
	second := httptest.NewRecorder()
	router.ServeHTTP(second, newBatchRequest(t, "not-a-uuid"))

	if svc.hits() != 1 {
		t.Fatalf("expected CreateBatch to run exactly once, ran %d times", svc.hits())
	}
	if first.Body.String() != second.Body.String() {
		t.Fatalf("expected identical replayed response, got %q vs %q", first.Body.String(), second.Body.String())
	}
}

// The accepted response is the issue's contract: batch_id plus the transfer
// ids accepted for settlement.
func TestCreateBatchAcceptedResponseCarriesBatchAndTransferIDs(t *testing.T) {
	svc := &fakeService{}
	router := newBatchRouter(svc, newMockIdemRepo())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, newBatchRequest(t, uuid.New().String()))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d: %s", rec.Code, rec.Body.String())
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if body["batch_id"] != "batch-1" {
		t.Fatalf("batch_id = %v, want batch-1", body["batch_id"])
	}
	if _, ok := body["transfer_ids"].([]interface{}); !ok {
		t.Fatalf("transfer_ids = %v, want array", body["transfer_ids"])
	}
}

// The service's pre-validation pass surfaces as 400 with per-row details, the
// same shape as the syntactic checks.
func TestCreateBatchServiceValidationErrorsReturn400(t *testing.T) {
	svc := &fakeService{createErr: &BatchValidationError{Errors: []ValidationError{
		{Row: 1, Field: "to_wallet_id", Value: "wallet-x", Reason: "wallet not found"},
	}}}
	router := newBatchRouter(svc, newMockIdemRepo())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, newBatchRequest(t, uuid.New().String()))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Message          string                      `json:"message"`
		ValidationErrors []api.ValidationErrorDetail `json:"validation_errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(body.ValidationErrors) != 1 || body.ValidationErrors[0].Field != "to_wallet_id" || body.ValidationErrors[0].Row != 1 {
		t.Fatalf("validation_errors = %+v, want row 1 to_wallet_id", body.ValidationErrors)
	}
}
