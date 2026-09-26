package idempotency_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/server/idempotency"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
)

type mockRepo struct {
	mu      sync.Mutex
	records map[string]*idempotency.Record
}

func newMockRepo() *mockRepo {
	return &mockRepo{records: map[string]*idempotency.Record{}}
}

func (m *mockRepo) TryAcquire(ctx context.Context, orgID, key, requestHash string, expiresAt time.Time) (*idempotency.Record, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := orgID + ":" + key
	if rec, ok := m.records[k]; ok {
		// Handle 24-hour expiration simulation
		if !rec.ExpiresAt.IsZero() && time.Now().After(rec.ExpiresAt) {
			delete(m.records, k)
		} else {
			cp := *rec
			return &cp, true, nil
		}
	}
	m.records[k] = &idempotency.Record{
		OrgID:       orgID,
		Key:         key,
		RequestHash: requestHash,
		Status:      idempotency.StatusProcessing,
		ExpiresAt:   expiresAt,
	}
	return nil, false, nil
}

func (m *mockRepo) Complete(ctx context.Context, orgID, key string, responseStatus int, responseBody []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := orgID + ":" + key
	rec, ok := m.records[k]
	if !ok {
		return nil
	}
	rec.Status = idempotency.StatusComplete
	rec.ResponseStatus = responseStatus
	rec.ResponseBody = responseBody
	return nil
}

func newRequest(t *testing.T, key, body string) *http.Request {
	t.Helper()
	ctx := tenant.WithID(context.Background(), "org-1")
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers", bytes.NewBufferString(body)).WithContext(ctx)
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	return req
}

func newXRequest(t *testing.T, key, body string) *http.Request {
	t.Helper()
	ctx := tenant.WithID(context.Background(), "org-1")
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers", bytes.NewBufferString(body)).WithContext(ctx)
	if key != "" {
		req.Header.Set("X-Idempotency-Key", key)
	}
	return req
}

func decodeErrorCode(t *testing.T, body []byte) string {
	t.Helper()
	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode error response: %v (body=%s)", err, body)
	}
	return resp.Error.Code
}

func TestNewKey(t *testing.T) {
	repo := newMockRepo()
	mw := idempotency.Middleware(repo)

	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"tx-new","status":"pending"}`))
	}))

	rec := httptest.NewRecorder()
	key := uuid.New().String()
	h.ServeHTTP(rec, newRequest(t, key, `{"amount":"100"}`))

	if !called {
		t.Fatal("expected handler to be called for new key")
	}
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec.Code)
	}
}

func TestMissingKeyOptionalPasses(t *testing.T) {
	repo := newMockRepo()
	mw := idempotency.OptionalMiddleware(repo)

	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(t, "", `{"amount":"10"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for optional missing key, got %d", rec.Code)
	}
	if !called {
		t.Fatal("handler should run when idempotency key is optional and omitted")
	}
}

func TestMissingKeyRequiredReturns400(t *testing.T) {
	repo := newMockRepo()
	mw := idempotency.RequiredMiddleware(repo)

	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(t, "", `{"a":1}`))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "IDEMPOTENCY_KEY_REQUIRED" {
		t.Fatalf("expected IDEMPOTENCY_KEY_REQUIRED, got %s", code)
	}
	if called {
		t.Fatal("handler should not run when required key is missing")
	}
}

func TestXIdempotencyKeyHeader(t *testing.T) {
	repo := newMockRepo()
	mw := idempotency.Middleware(repo)

	callCount := 0
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"tx-x-key","status":"pending"}`))
	}))

	key := "custom-client-tx-" + uuid.New().String()
	body := `{"from_wallet_id":"a","to_wallet_id":"b","amount":"10"}`

	first := httptest.NewRecorder()
	h.ServeHTTP(first, newXRequest(t, key, body))

	second := httptest.NewRecorder()
	h.ServeHTTP(second, newXRequest(t, key, body))

	if callCount != 1 {
		t.Fatalf("expected handler to run once for duplicate X-Idempotency-Key, ran %d times", callCount)
	}
	if first.Code != second.Code || first.Body.String() != second.Body.String() {
		t.Fatalf("replayed responses differ: first=%s second=%s", first.Body.String(), second.Body.String())
	}
}

func TestSameKeySameBodyReplaysResponseByteForByte(t *testing.T) {
	repo := newMockRepo()
	mw := idempotency.Middleware(repo)

	callCount := 0
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"tx-1","status":"pending"}`))
	}))

	key := uuid.New().String()
	body := `{"from_wallet_id":"a","to_wallet_id":"b","asset":"XLM","amount":"10"}`

	first := httptest.NewRecorder()
	h.ServeHTTP(first, newRequest(t, key, body))

	second := httptest.NewRecorder()
	h.ServeHTTP(second, newRequest(t, key, body))

	if callCount != 1 {
		t.Fatalf("expected handler to run exactly once, ran %d times", callCount)
	}
	if first.Code != second.Code || first.Body.String() != second.Body.String() {
		t.Fatalf("responses differ: first=%d %q second=%d %q", first.Code, first.Body.String(), second.Code, second.Body.String())
	}
	if second.Code != http.StatusAccepted || second.Body.String() != `{"id":"tx-1","status":"pending"}` {
		t.Fatalf("unexpected replayed response: %d %s", second.Code, second.Body.String())
	}
}

func TestSameKeyDifferentBodyReturns409(t *testing.T) {
	repo := newMockRepo()
	mw := idempotency.Middleware(repo)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	key := uuid.New().String()
	first := httptest.NewRecorder()
	h.ServeHTTP(first, newRequest(t, key, `{"amount":"10"}`))

	second := httptest.NewRecorder()
	h.ServeHTTP(second, newRequest(t, key, `{"amount":"20"}`))

	// Issue #151: Return 409 Conflict if the same key is used with a different request body
	if second.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got %d", second.Code)
	}
	if code := decodeErrorCode(t, second.Body.Bytes()); code != "IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY" {
		t.Fatalf("expected IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY, got %s", code)
	}
}

func TestExpiredKeyAllowsNewExecution(t *testing.T) {
	repo := newMockRepo()
	mw := idempotency.Middleware(repo)

	callCount := 0
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"count":` + string(rune('0'+callCount)) + `}`))
	}))

	key := uuid.New().String()
	body := `{"amount":"50"}`

	// First execution
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, newRequest(t, key, body))
	if callCount != 1 {
		t.Fatalf("expected 1 execution, got %d", callCount)
	}

	// Manually age the record past 24 hours to simulate TTL expiration
	k := "org-1:" + idempotency.DeterministicKey(key)
	repo.mu.Lock()
	if rec, ok := repo.records[k]; ok {
		rec.ExpiresAt = time.Now().Add(-1 * time.Hour) // expired in the past
	}
	repo.mu.Unlock()

	// Second execution with expired key should run as a new key
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, newRequest(t, key, body))

	if callCount != 2 {
		t.Fatalf("expected handler to re-run after key expiration, got %d calls", callCount)
	}
	if rec2.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d", rec2.Code)
	}
}

func TestDeterministicKeySHA256(t *testing.T) {
	rawString := "order_transfer_ref_99999"
	k1 := idempotency.DeterministicKey(rawString)
	k2 := idempotency.DeterministicKey(rawString)

	if k1 != k2 {
		t.Fatalf("expected deterministic keys to be identical, got %q and %q", k1, k2)
	}

	// Must be a valid UUID
	parsed, err := uuid.Parse(k1)
	if err != nil {
		t.Fatalf("expected valid UUID from deterministic key, got error: %v", err)
	}
	if parsed.Version() != 4 {
		t.Fatalf("expected UUID v4 format, got version %d", parsed.Version())
	}
}

func TestConcurrentRequestInProgressReturns409(t *testing.T) {
	repo := newMockRepo()
	key := uuid.New().String()
	dk := idempotency.DeterministicKey(key)
	repo.records["org-1:"+dk] = &idempotency.Record{OrgID: "org-1", Key: dk, Status: idempotency.StatusProcessing}

	mw := idempotency.Middleware(repo)
	called := false
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(t, key, `{"amount":"10"}`))

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", rec.Code)
	}
	if code := decodeErrorCode(t, rec.Body.Bytes()); code != "REQUEST_IN_PROGRESS" {
		t.Fatalf("expected REQUEST_IN_PROGRESS, got %s", code)
	}
	if called {
		t.Fatal("handler should not run for a request that lost the race")
	}
}

func TestHandlerStillReceivesRequestBody(t *testing.T) {
	repo := newMockRepo()
	mw := idempotency.Middleware(repo)

	var received string
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(r.Body)
		received = buf.String()
		w.WriteHeader(http.StatusOK)
	}))

	body := `{"amount":"42"}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, newRequest(t, uuid.New().String(), body))

	if received != body {
		t.Fatalf("expected handler to read original body %q, got %q", body, received)
	}
}
