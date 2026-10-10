package idempotency_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/server/idempotency"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type testRepo struct {
	records map[string]idempotency.Record
	now     func() time.Time
}

func newTestRepo() *testRepo {
	return &testRepo{
		records: make(map[string]idempotency.Record),
		now:     func() time.Time { return time.Now().UTC() },
	}
}

func (r *testRepo) withClock(now func() time.Time) *testRepo {
	r.now = now
	return r
}

func (r *testRepo) Acquire(_ context.Context, _ string, _ domain.Mode, _, _ string, _, _, _ time.Time, _ bool) (idempotency.Acquisition, error) {
	return idempotency.Acquisition{}, nil
}

func (r *testRepo) Complete(_ context.Context, _, _ string, _ idempotency.Response, _ time.Time) error {
	return nil
}

func (r *testRepo) DeleteExpired(_ context.Context, _ int) (int64, error) {
	return 0, nil
}

func (r *testRepo) Lookup(_ context.Context, orgID string, mode domain.Mode, key string) (idempotency.LookupResult, error) {
	k := orgID + ":" + string(mode) + ":" + key
	rec, ok := r.records[k]
	if !ok {
		return idempotency.LookupResult{Found: false}, nil
	}
	if !rec.ExpiresAt.IsZero() && r.now().After(rec.ExpiresAt) {
		return idempotency.LookupResult{Found: false}, nil
	}
	return idempotency.LookupResult{Found: true, Record: rec}, nil
}

func setupTestRouter(repo *testRepo, now time.Time) *chi.Mux {
	repo.withClock(func() time.Time { return now })
	h := idempotency.NewHandler(repo).WithClock(func() time.Time { return now })
	r := chi.NewRouter()
	r.Route("/v1/idempotency", h.Routes())
	return r
}

func TestInspect_UnknownKey(t *testing.T) {
	repo := newTestRepo()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	router := setupTestRouter(repo, now)

	rawKey := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/v1/idempotency/"+rawKey, nil)
	req = req.WithContext(tenant.WithID(req.Context(), "org-1"))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp idempotency.InspectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "unknown" {
		t.Fatalf("expected status unknown, got %s", resp.Status)
	}
	if resp.Key != rawKey {
		t.Fatalf("expected key %s, got %s", rawKey, resp.Key)
	}
	if resp.Response != nil {
		t.Fatal("expected nil stored response for unknown key")
	}
}

func TestInspect_Processing(t *testing.T) {
	repo := newTestRepo()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	router := setupTestRouter(repo, now)

	rawKey := uuid.New().String()
	normKey := idempotency.DeterministicKey(rawKey)
	createdAt := now.Add(-5 * time.Second)
	leaseExpiresAt := now.Add(25 * time.Second)
	expiresAt := now.Add(24 * time.Hour)
	reqHash := idempotency.RequestHash(http.MethodPost, "/v1/transfers", []byte(`{"amount":"10"}`))

	repo.records["org-1:"+string(domain.ModeLive)+":"+normKey] = idempotency.Record{
		ID:             uuid.New().String(),
		OrgID:          "org-1",
		Mode:           domain.ModeLive,
		Key:            normKey,
		RequestHash:    reqHash,
		Status:         idempotency.StatusProcessing,
		CreatedAt:      createdAt,
		LeaseExpiresAt: leaseExpiresAt,
		ExpiresAt:      expiresAt,
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/idempotency/"+rawKey, nil)
	req = req.WithContext(tenant.WithID(req.Context(), "org-1"))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	// Verify Retry-After header
	retryAfterHdr := rec.Header().Get("Retry-After")
	if retryAfterHdr != "25" {
		t.Fatalf("expected Retry-After header 25, got %q", retryAfterHdr)
	}

	var resp idempotency.InspectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "processing" {
		t.Fatalf("expected status processing, got %s", resp.Status)
	}
	if resp.RetryAfterSeconds == nil || *resp.RetryAfterSeconds != 25 {
		t.Fatalf("expected retry_after_seconds 25, got %v", resp.RetryAfterSeconds)
	}
	if resp.RetryHint == "" {
		t.Fatal("expected non-empty retry_hint")
	}
	if resp.RequestHash != reqHash {
		t.Fatalf("expected request_hash %s, got %s", reqHash, resp.RequestHash)
	}
}

func TestInspect_Completed(t *testing.T) {
	repo := newTestRepo()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	router := setupTestRouter(repo, now)

	rawKey := uuid.New().String()
	normKey := idempotency.DeterministicKey(rawKey)
	createdAt := now.Add(-10 * time.Minute)
	expiresAt := now.Add(23 * time.Hour)
	reqHash := idempotency.RequestHash(http.MethodPost, "/v1/transfers", []byte(`{"amount":"100"}`))
	responseBody := `{"id":"tx_abc123","status":"completed","amount":"100"}`

	hdrs := make(http.Header)
	hdrs.Set("Content-Type", "application/json")
	hdrs.Set("X-Custom-Header", "test-val")

	repo.records["org-1:"+string(domain.ModeLive)+":"+normKey] = idempotency.Record{
		ID:              uuid.New().String(),
		OrgID:           "org-1",
		Mode:            domain.ModeLive,
		Key:             normKey,
		RequestHash:     reqHash,
		Status:          idempotency.StatusComplete,
		CreatedAt:       createdAt,
		ExpiresAt:       expiresAt,
		ResponseStatus:  http.StatusOK,
		ResponseHeaders: hdrs,
		ResponseBody:    []byte(responseBody),
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/idempotency/"+rawKey, nil)
	req = req.WithContext(tenant.WithID(req.Context(), "org-1"))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp idempotency.InspectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "completed" {
		t.Fatalf("expected status completed, got %s", resp.Status)
	}
	if resp.Response == nil {
		t.Fatal("expected stored response, got nil")
	}
	if resp.Response.Status != http.StatusOK {
		t.Fatalf("expected stored status 200, got %d", resp.Response.Status)
	}
	if string(resp.Response.Body) != responseBody {
		t.Fatalf("expected stored body %s, got %s", responseBody, string(resp.Response.Body))
	}
	if resp.Response.BodyTruncated {
		t.Fatal("expected body_truncated to be false")
	}
}

func TestInspect_Failed(t *testing.T) {
	repo := newTestRepo()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	router := setupTestRouter(repo, now)

	rawKey := uuid.New().String()
	normKey := idempotency.DeterministicKey(rawKey)
	createdAt := now.Add(-5 * time.Minute)
	expiresAt := now.Add(23 * time.Hour)
	reqHash := idempotency.RequestHash(http.MethodPost, "/v1/transfers", []byte(`{"amount":"-10"}`))
	errBody := `{"error":{"code":"INVALID_AMOUNT","message":"amount must be positive"}}`

	repo.records["org-1:"+string(domain.ModeLive)+":"+normKey] = idempotency.Record{
		ID:             uuid.New().String(),
		OrgID:          "org-1",
		Mode:           domain.ModeLive,
		Key:            normKey,
		RequestHash:    reqHash,
		Status:         idempotency.StatusComplete,
		CreatedAt:      createdAt,
		ExpiresAt:      expiresAt,
		ResponseStatus: http.StatusBadRequest,
		ResponseBody:   []byte(errBody),
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/idempotency/"+rawKey, nil)
	req = req.WithContext(tenant.WithID(req.Context(), "org-1"))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp idempotency.InspectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Status != "failed" {
		t.Fatalf("expected status failed, got %s", resp.Status)
	}
	if resp.Response == nil || resp.Response.Status != http.StatusBadRequest {
		t.Fatalf("expected response status 400, got %v", resp.Response)
	}
}

func TestInspect_CrossTenantIsolation(t *testing.T) {
	repo := newTestRepo()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	router := setupTestRouter(repo, now)

	rawKey := uuid.New().String()
	normKey := idempotency.DeterministicKey(rawKey)

	// Record belongs to org-1
	repo.records["org-1:"+string(domain.ModeLive)+":"+normKey] = idempotency.Record{
		ID:             uuid.New().String(),
		OrgID:          "org-1",
		Mode:           domain.ModeLive,
		Key:            normKey,
		RequestHash:    "some-hash",
		Status:         idempotency.StatusComplete,
		CreatedAt:      now,
		ExpiresAt:      now.Add(24 * time.Hour),
		ResponseStatus: http.StatusOK,
	}

	// Inspected by org-2
	req := httptest.NewRequest(http.MethodGet, "/v1/idempotency/"+rawKey, nil)
	req = req.WithContext(tenant.WithID(req.Context(), "org-2"))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp idempotency.InspectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Must behave as unknown to prevent existence oracle
	if resp.Status != "unknown" {
		t.Fatalf("expected cross-tenant lookup to return status unknown, got %s", resp.Status)
	}
	if resp.Response != nil {
		t.Fatal("expected nil response for other tenant")
	}
}

func TestInspect_BodyMismatch(t *testing.T) {
	repo := newTestRepo()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	router := setupTestRouter(repo, now)

	rawKey := uuid.New().String()
	normKey := idempotency.DeterministicKey(rawKey)
	originalHash := idempotency.RequestHash(http.MethodPost, "/v1/transfers", []byte(`{"amount":"50"}`))
	differentHash := idempotency.RequestHash(http.MethodPost, "/v1/transfers", []byte(`{"amount":"100"}`))

	repo.records["org-1:"+string(domain.ModeLive)+":"+normKey] = idempotency.Record{
		ID:             uuid.New().String(),
		OrgID:          "org-1",
		Mode:           domain.ModeLive,
		Key:            normKey,
		RequestHash:    originalHash,
		Status:         idempotency.StatusComplete,
		CreatedAt:      now,
		ExpiresAt:      now.Add(24 * time.Hour),
		ResponseStatus: http.StatusOK,
		ResponseBody:   []byte(`{"ok":true}`),
	}

	// Case 1: Client provides mismatched hash via query param -> 422 IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY
	reqMismatch := httptest.NewRequest(http.MethodGet, "/v1/idempotency/"+rawKey+"?request_hash="+differentHash, nil)
	reqMismatch = reqMismatch.WithContext(tenant.WithID(reqMismatch.Context(), "org-1"))
	recMismatch := httptest.NewRecorder()

	router.ServeHTTP(recMismatch, reqMismatch)

	if recMismatch.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected status 422 for body mismatch, got %d", recMismatch.Code)
	}

	var errResp struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recMismatch.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}
	if errResp.Error.Code != "IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY" {
		t.Fatalf("expected code IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY, got %s", errResp.Error.Code)
	}

	// Case 2: Client provides matching hash -> 200 OK
	reqMatch := httptest.NewRequest(http.MethodGet, "/v1/idempotency/"+rawKey+"?request_hash="+originalHash, nil)
	reqMatch = reqMatch.WithContext(tenant.WithID(reqMatch.Context(), "org-1"))
	recMatch := httptest.NewRecorder()

	router.ServeHTTP(recMatch, reqMatch)

	if recMatch.Code != http.StatusOK {
		t.Fatalf("expected status 200 for matching hash, got %d", recMatch.Code)
	}
}

func TestInspect_BodyTruncation(t *testing.T) {
	repo := newTestRepo()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	router := setupTestRouter(repo, now)

	rawKey := uuid.New().String()
	normKey := idempotency.DeterministicKey(rawKey)

	// Create a body larger than MaxInspectionResponseBodyBytes (64KB)
	largeBody := strings.Repeat("A", idempotency.MaxInspectionResponseBodyBytes+1024)

	repo.records["org-1:"+string(domain.ModeLive)+":"+normKey] = idempotency.Record{
		ID:             uuid.New().String(),
		OrgID:          "org-1",
		Mode:           domain.ModeLive,
		Key:            normKey,
		RequestHash:    "some-hash",
		Status:         idempotency.StatusComplete,
		CreatedAt:      now,
		ExpiresAt:      now.Add(24 * time.Hour),
		ResponseStatus: http.StatusOK,
		ResponseBody:   []byte(largeBody),
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/idempotency/"+rawKey, nil)
	req = req.WithContext(tenant.WithID(req.Context(), "org-1"))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}

	var resp idempotency.InspectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Response == nil {
		t.Fatal("expected non-nil response")
	}
	if !resp.Response.BodyTruncated {
		t.Fatal("expected body_truncated to be true")
	}
	if len(resp.Response.BodyText) != idempotency.MaxInspectionResponseBodyBytes {
		t.Fatalf("expected truncated length %d, got %d", idempotency.MaxInspectionResponseBodyBytes, len(resp.Response.BodyText))
	}
	if resp.Response.BodySizeBytes != len(largeBody) {
		t.Fatalf("expected original body size %d, got %d", len(largeBody), resp.Response.BodySizeBytes)
	}
}

func TestInspect_RawReplay(t *testing.T) {
	repo := newTestRepo()
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	router := setupTestRouter(repo, now)

	rawKey := uuid.New().String()
	normKey := idempotency.DeterministicKey(rawKey)
	expectedBody := `{"transfer_id":"tr_999","amount":"50.00"}`

	hdrs := make(http.Header)
	hdrs.Set("Content-Type", "application/json")
	hdrs.Set("X-Custom", "replay-test")

	repo.records["org-1:"+string(domain.ModeLive)+":"+normKey] = idempotency.Record{
		ID:              uuid.New().String(),
		OrgID:           "org-1",
		Mode:            domain.ModeLive,
		Key:             normKey,
		RequestHash:     "some-hash",
		Status:          idempotency.StatusComplete,
		CreatedAt:       now,
		ExpiresAt:       now.Add(24 * time.Hour),
		ResponseStatus:  http.StatusCreated,
		ResponseHeaders: hdrs,
		ResponseBody:    []byte(expectedBody),
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/idempotency/"+rawKey+"?raw=true", nil)
	req = req.WithContext(tenant.WithID(req.Context(), "org-1"))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected status 201 Created, got %d", rec.Code)
	}
	if rec.Header().Get("Idempotency-Replayed") != "true" {
		t.Fatalf("expected Idempotency-Replayed: true header, got %q", rec.Header().Get("Idempotency-Replayed"))
	}
	if rec.Body.String() != expectedBody {
		t.Fatalf("expected body %s, got %s", expectedBody, rec.Body.String())
	}
}
