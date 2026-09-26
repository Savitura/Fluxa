package schedule

// handler_run_test.go — HTTP-level tests for GET /v1/schedules/{id}/runs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Fake service for handler tests
// ---------------------------------------------------------------------------

type fakeRunService struct {
	schedules map[string]*domain.Schedule
	runs      map[string][]*domain.ScheduleRun // keyed by scheduleID
}

func newFakeRunService() *fakeRunService {
	return &fakeRunService{
		schedules: make(map[string]*domain.Schedule),
		runs:      make(map[string][]*domain.ScheduleRun),
	}
}

func (f *fakeRunService) Create(_ context.Context, in CreateInput) (*domain.Schedule, error) {
	return nil, nil
}

func (f *fakeRunService) List(_ context.Context) ([]*domain.Schedule, error) {
	return nil, nil
}

func (f *fakeRunService) Update(_ context.Context, id string, in UpdateInput) (*domain.Schedule, error) {
	return nil, nil
}

func (f *fakeRunService) Cancel(_ context.Context, id string) error {
	return nil
}

func (f *fakeRunService) ListRuns(ctx context.Context, scheduleID string, limit, offset int) ([]*domain.ScheduleRun, error) {
	tID := tenant.IDFromContext(ctx)
	sch, ok := f.schedules[scheduleID]
	if !ok {
		return nil, domain.ErrScheduleNotFound
	}
	// Tenant isolation: if context has a tenant and schedule belongs to another, 404.
	if tID != "" && (sch.TenantID == nil || *sch.TenantID != tID) {
		return nil, domain.ErrScheduleNotFound
	}

	all := f.runs[scheduleID]
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset >= len(all) {
		return []*domain.ScheduleRun{}, nil
	}
	end := offset + limit
	if end > len(all) {
		end = len(all)
	}
	return all[offset:end], nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func routerForHandler(h *Handler) *chi.Mux {
	r := chi.NewRouter()
	r.Route("/v1/schedules", h.Routes())
	return r
}

func makeRun(scheduleID, runID string, expectedAt time.Time, status domain.ScheduleRunStatus) *domain.ScheduleRun {
	now := time.Now().UTC()
	return &domain.ScheduleRun{
		ID:            runID,
		ScheduleID:    scheduleID,
		ExpectedRunAt: expectedAt,
		Status:        status,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
}

func tenantCtx(ctx context.Context, tenantID string) context.Context {
	return tenant.WithID(ctx, tenantID)
}

// ---------------------------------------------------------------------------
// Test: Successful listing of runs
// ---------------------------------------------------------------------------

func TestHandlerListRuns_Success(t *testing.T) {
	svc := newFakeRunService()
	tid := "tenant-1"
	svc.schedules["sched-1"] = &domain.Schedule{
		ID: "sched-1", TenantID: &tid,
		FromWallet: "f", ToWallet: "t", Asset: "XLM",
		Amount:    decimal.NewFromInt(10),
		Frequency: domain.FrequencyWeekly,
		Status:    domain.ScheduleStatusActive,
		NextRunAt: time.Now().UTC(),
	}
	dueAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	svc.runs["sched-1"] = []*domain.ScheduleRun{
		makeRun("sched-1", "run-1", dueAt, domain.ScheduleRunStatusSucceeded),
	}

	h := NewHandler(svc)
	router := routerForHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/schedules/sched-1/runs", nil)
	req = req.WithContext(tenantCtx(req.Context(), tid))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", w.Code, w.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	runs, ok := resp["runs"].([]interface{})
	if !ok {
		t.Fatalf("expected 'runs' array in response, got %T", resp["runs"])
	}
	if len(runs) != 1 {
		t.Fatalf("got %d runs, want 1", len(runs))
	}
	run := runs[0].(map[string]interface{})
	if run["status"] != "succeeded" {
		t.Errorf("run status = %q, want succeeded", run["status"])
	}
	if run["id"] != "run-1" {
		t.Errorf("run id = %q, want run-1", run["id"])
	}
}

// ---------------------------------------------------------------------------
// Test: Empty run history
// ---------------------------------------------------------------------------

func TestHandlerListRuns_EmptyHistory(t *testing.T) {
	svc := newFakeRunService()
	tid := "tenant-2"
	svc.schedules["sched-2"] = &domain.Schedule{
		ID: "sched-2", TenantID: &tid,
		FromWallet: "f", ToWallet: "t", Asset: "XLM",
		Amount:    decimal.NewFromInt(5),
		Frequency: domain.FrequencyDaily,
		Status:    domain.ScheduleStatusActive,
		NextRunAt: time.Now().UTC(),
	}

	h := NewHandler(svc)
	router := routerForHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/schedules/sched-2/runs", nil)
	req = req.WithContext(tenantCtx(req.Context(), tid))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	runs := resp["runs"].([]interface{})
	if len(runs) != 0 {
		t.Errorf("got %d runs, want 0 for empty history", len(runs))
	}
}

// ---------------------------------------------------------------------------
// Test: Tenant isolation — different tenant cannot access schedule
// ---------------------------------------------------------------------------

func TestHandlerListRuns_DifferentTenant_Returns404(t *testing.T) {
	svc := newFakeRunService()
	ownerTenant := "tenant-owner"
	svc.schedules["sched-3"] = &domain.Schedule{
		ID: "sched-3", TenantID: &ownerTenant,
		FromWallet: "f", ToWallet: "t", Asset: "XLM",
		Amount:    decimal.NewFromInt(5),
		Frequency: domain.FrequencyDaily,
		Status:    domain.ScheduleStatusActive,
		NextRunAt: time.Now().UTC(),
	}

	h := NewHandler(svc)
	router := routerForHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/schedules/sched-3/runs", nil)
	// Different tenant in context.
	req = req.WithContext(tenantCtx(req.Context(), "tenant-attacker"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Must be 404, not 403 — we must not reveal whether the schedule exists.
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for cross-tenant access", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Test: Schedule not found
// ---------------------------------------------------------------------------

func TestHandlerListRuns_ScheduleNotFound_Returns404(t *testing.T) {
	svc := newFakeRunService()
	h := NewHandler(svc)
	router := routerForHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/schedules/nonexistent/runs", nil)
	req = req.WithContext(tenantCtx(req.Context(), "tenant-x"))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for unknown schedule", w.Code)
	}
}

// ---------------------------------------------------------------------------
// Test: Pagination — default limit applied
// ---------------------------------------------------------------------------

func TestHandlerListRuns_Pagination_DefaultLimit(t *testing.T) {
	svc := newFakeRunService()
	tid := "tenant-pag"
	svc.schedules["sched-pag"] = &domain.Schedule{
		ID: "sched-pag", TenantID: &tid,
		FromWallet: "f", ToWallet: "t", Asset: "XLM",
		Amount:    decimal.NewFromInt(1),
		Frequency: domain.FrequencyDaily,
		Status:    domain.ScheduleStatusActive,
		NextRunAt: time.Now().UTC(),
	}

	// Create 30 run records.
	var runs []*domain.ScheduleRun
	base := time.Now().UTC().Add(-30 * 24 * time.Hour).Truncate(time.Second)
	for i := 0; i < 30; i++ {
		runs = append(runs, makeRun("sched-pag", fmt.Sprintf("run-%d", i), base.Add(time.Duration(i)*24*time.Hour), domain.ScheduleRunStatusSucceeded))
	}
	svc.runs["sched-pag"] = runs

	h := NewHandler(svc)
	router := routerForHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/schedules/sched-pag/runs", nil)
	req = req.WithContext(tenantCtx(req.Context(), tid))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	runsSlice := resp["runs"].([]interface{})
	// Default limit is 20, so only 20 of 30 should be returned.
	if len(runsSlice) != 20 {
		t.Errorf("got %d runs, want 20 (default limit)", len(runsSlice))
	}
}

// ---------------------------------------------------------------------------
// Test: Pagination — explicit limit and offset
// ---------------------------------------------------------------------------

func TestHandlerListRuns_Pagination_ExplicitLimitOffset(t *testing.T) {
	svc := newFakeRunService()
	tid := "tenant-pag2"
	svc.schedules["sched-pag2"] = &domain.Schedule{
		ID: "sched-pag2", TenantID: &tid,
		FromWallet: "f", ToWallet: "t", Asset: "XLM",
		Amount:    decimal.NewFromInt(1),
		Frequency: domain.FrequencyDaily,
		Status:    domain.ScheduleStatusActive,
		NextRunAt: time.Now().UTC(),
	}

	var runs []*domain.ScheduleRun
	base := time.Now().UTC().Add(-10 * 24 * time.Hour).Truncate(time.Second)
	for i := 0; i < 10; i++ {
		runs = append(runs, makeRun("sched-pag2", fmt.Sprintf("run-%d", i), base.Add(time.Duration(i)*24*time.Hour), domain.ScheduleRunStatusSucceeded))
	}
	svc.runs["sched-pag2"] = runs

	h := NewHandler(svc)
	router := routerForHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/schedules/sched-pag2/runs?limit=3&offset=5", nil)
	req = req.WithContext(tenantCtx(req.Context(), tid))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	runsSlice := resp["runs"].([]interface{})
	if len(runsSlice) != 3 {
		t.Errorf("got %d runs, want 3 (limit=3, offset=5 of 10)", len(runsSlice))
	}
}

// ---------------------------------------------------------------------------
// Test: Maximum page size enforced (limit > 100 → capped at 20)
// ---------------------------------------------------------------------------

func TestHandlerListRuns_MaxPageSizeEnforced(t *testing.T) {
	svc := newFakeRunService()
	tid := "tenant-max"
	svc.schedules["sched-max"] = &domain.Schedule{
		ID: "sched-max", TenantID: &tid,
		FromWallet: "f", ToWallet: "t", Asset: "XLM",
		Amount:    decimal.NewFromInt(1),
		Frequency: domain.FrequencyDaily,
		Status:    domain.ScheduleStatusActive,
		NextRunAt: time.Now().UTC(),
	}

	var runs []*domain.ScheduleRun
	base := time.Now().UTC().Add(-120 * 24 * time.Hour).Truncate(time.Second)
	for i := 0; i < 120; i++ {
		runs = append(runs, makeRun("sched-max", fmt.Sprintf("run-%d", i), base.Add(time.Duration(i)*24*time.Hour), domain.ScheduleRunStatusSucceeded))
	}
	svc.runs["sched-max"] = runs

	h := NewHandler(svc)
	router := routerForHandler(h)

	// Request limit=200 — server must cap at 20 (default when > 100).
	req := httptest.NewRequest(http.MethodGet, "/v1/schedules/sched-max/runs?limit=200", nil)
	req = req.WithContext(tenantCtx(req.Context(), tid))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	runsSlice := resp["runs"].([]interface{})
	if len(runsSlice) > 100 {
		t.Errorf("got %d runs; server must cap at most 100", len(runsSlice))
	}
}

// ---------------------------------------------------------------------------
// Test: Failed run — error field present, no transaction_id
// ---------------------------------------------------------------------------

func TestHandlerListRuns_FailedRun_ErrorFieldExposed(t *testing.T) {
	svc := newFakeRunService()
	tid := "tenant-fail"
	svc.schedules["sched-fail"] = &domain.Schedule{
		ID: "sched-fail", TenantID: &tid,
		FromWallet: "f", ToWallet: "t", Asset: "XLM",
		Amount:    decimal.NewFromInt(1),
		Frequency: domain.FrequencyDaily,
		Status:    domain.ScheduleStatusFailed,
		NextRunAt: time.Now().UTC(),
	}

	dueAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	errMsg := "insufficient balance"
	run := makeRun("sched-fail", "run-fail-1", dueAt, domain.ScheduleRunStatusFailed)
	run.Error = &errMsg

	svc.runs["sched-fail"] = []*domain.ScheduleRun{run}

	h := NewHandler(svc)
	router := routerForHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/schedules/sched-fail/runs", nil)
	req = req.WithContext(tenantCtx(req.Context(), tid))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	runs := resp["runs"].([]interface{})
	if len(runs) != 1 {
		t.Fatalf("want 1 run, got %d", len(runs))
	}
	runMap := runs[0].(map[string]interface{})
	if runMap["error"] == nil {
		t.Error("error field must be present for a failed run")
	}
	if runMap["transaction_id"] != nil {
		t.Error("transaction_id must be absent for a failed run")
	}
}

// ---------------------------------------------------------------------------
// Test: Succeeded run — transaction_id present, no error field
// ---------------------------------------------------------------------------

func TestHandlerListRuns_SucceededRun_TransactionIDPresent(t *testing.T) {
	svc := newFakeRunService()
	tid := "tenant-succ"
	svc.schedules["sched-succ"] = &domain.Schedule{
		ID: "sched-succ", TenantID: &tid,
		FromWallet: "f", ToWallet: "t", Asset: "XLM",
		Amount:    decimal.NewFromInt(1),
		Frequency: domain.FrequencyDaily,
		Status:    domain.ScheduleStatusActive,
		NextRunAt: time.Now().UTC(),
	}

	dueAt := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	txID := "tx-abc123"
	run := makeRun("sched-succ", "run-succ-1", dueAt, domain.ScheduleRunStatusSucceeded)
	run.TransactionID = &txID

	svc.runs["sched-succ"] = []*domain.ScheduleRun{run}

	h := NewHandler(svc)
	router := routerForHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/schedules/sched-succ/runs", nil)
	req = req.WithContext(tenantCtx(req.Context(), tid))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	runs := resp["runs"].([]interface{})
	runMap := runs[0].(map[string]interface{})
	if runMap["transaction_id"] != txID {
		t.Errorf("transaction_id = %v, want %q", runMap["transaction_id"], txID)
	}
	if runMap["error"] != nil {
		t.Errorf("error field must be absent for a succeeded run, got %v", runMap["error"])
	}
}

// ---------------------------------------------------------------------------
// Test: In-progress (running) run — status exposed as 'running'
// ---------------------------------------------------------------------------

func TestHandlerListRuns_RunningRun_StatusIsRunning(t *testing.T) {
	svc := newFakeRunService()
	tid := "tenant-running"
	svc.schedules["sched-running"] = &domain.Schedule{
		ID: "sched-running", TenantID: &tid,
		FromWallet: "f", ToWallet: "t", Asset: "XLM",
		Amount:    decimal.NewFromInt(1),
		Frequency: domain.FrequencyDaily,
		Status:    domain.ScheduleStatusProcessing,
		NextRunAt: time.Now().UTC(),
	}

	dueAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	run := makeRun("sched-running", "run-running-1", dueAt, domain.ScheduleRunStatusRunning)
	svc.runs["sched-running"] = []*domain.ScheduleRun{run}

	h := NewHandler(svc)
	router := routerForHandler(h)

	req := httptest.NewRequest(http.MethodGet, "/v1/schedules/sched-running/runs", nil)
	req = req.WithContext(tenantCtx(req.Context(), tid))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp map[string]interface{}
	_ = json.NewDecoder(w.Body).Decode(&resp)
	runs := resp["runs"].([]interface{})
	runMap := runs[0].(map[string]interface{})
	if runMap["status"] != "running" {
		t.Errorf("status = %q, want 'running' for an in-progress run", runMap["status"])
	}
}
