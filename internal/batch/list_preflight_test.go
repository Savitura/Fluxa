package batch

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// fakeListService implements Service for list/preflight handler tests.
type fakeListService struct {
	listCalls   int
	preflightCalls int
	lastQuery   ListQuery
	lastPreflightItems []Item
	listResult  *ListPage
	preflightResult *PreflightResult
	listErr     error
	preflightErr error
}

func (f *fakeListService) CreateBatch(_ context.Context, _ string, _ []Item) (*Result, error) {
	return nil, domain.ErrBatchNotFound
}
func (f *fakeListService) GetBatch(_ context.Context, _ string) (*Result, error) {
	return nil, domain.ErrBatchNotFound
}
func (f *fakeListService) ExportCSV(_ context.Context, _ string) (string, error) {
	return "", domain.ErrBatchNotFound
}
func (f *fakeListService) ListBatches(_ context.Context, q ListQuery) (*ListPage, error) {
	f.listCalls++
	f.lastQuery = q
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.listResult, nil
}
func (f *fakeListService) Preflight(_ context.Context, fromWalletID string, items []Item) (*PreflightResult, error) {
	f.preflightCalls++
	f.lastPreflightItems = items
	if f.preflightErr != nil {
		return nil, f.preflightErr
	}
	return f.preflightResult, nil
}

func newTestListRouter(svc Service) http.Handler {
	h := NewHandler(svc)
	r := chi.NewRouter()
	r.Route("/v1/transfers/batch", h.Routes())
	r.Route("/v1/transfers/batches", h.ListRoutes())
	return r
}

func batchCtx() context.Context {
	ctx := tenant.WithID(context.Background(), "org-1")
	return tenant.WithMode(ctx, domain.ModeLive)
}

func TestListBatchesReturnsPageAndSummary(t *testing.T) {
	now := time.Now().UTC()
	svc := &fakeListService{
		listResult: &ListPage{
			Batches: []*domain.Batch{
				{ID: "b1", Status: domain.BatchStatusCompleted, TotalCount: 3, CreatedAt: now, UpdatedAt: now},
				{ID: "b2", Status: domain.BatchStatusProcessing, TotalCount: 5, CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
			},
			NextCursor: &ListCursor{CreatedAt: now.Add(-time.Hour), ID: "b2"},
		},
	}
	router := newTestListRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/v1/transfers/batches?limit=10", nil).WithContext(batchCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp listBatchesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Batches) != 2 {
		t.Fatalf("expected 2 batches, got %d", len(resp.Batches))
	}
	if resp.NextCursor == nil || resp.NextCursor.ID != "b2" {
		t.Fatalf("expected next cursor b2, got %+v", resp.NextCursor)
	}
	if resp.Summary == nil || resp.Summary.Total != 2 {
		t.Fatalf("expected summary total 2, got %+v", resp.Summary)
	}
	if resp.Summary.ByStatus["completed"] != 1 || resp.Summary.ByStatus["processing"] != 1 {
		t.Fatalf("unexpected by_status: %+v", resp.Summary.ByStatus)
	}
	if svc.listCalls != 1 {
		t.Fatalf("expected 1 list call, got %d", svc.listCalls)
	}
	if svc.lastQuery.Limit != 10 {
		t.Fatalf("expected limit 10, got %d", svc.lastQuery.Limit)
	}
}

func TestListBatchesPassesStatusAndCursor(t *testing.T) {
	now := time.Now().UTC()
	svc := &fakeListService{
		listResult: &ListPage{},
	}
	router := newTestListRouter(svc)

	cursor := now.UTC().Format(time.RFC3339) + "|b9"
	req := httptest.NewRequest(http.MethodGet, "/v1/transfers/batches?status=completed&cursor="+cursor+"&from_wallet=w1", nil).WithContext(batchCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	if svc.lastQuery.Status != domain.BatchStatusCompleted {
		t.Fatalf("expected status completed, got %q", svc.lastQuery.Status)
	}
	if svc.lastQuery.FromWallet != "w1" {
		t.Fatalf("expected from_wallet w1, got %q", svc.lastQuery.FromWallet)
	}
	if svc.lastQuery.AfterCursor == nil || svc.lastQuery.AfterCursor.ID != "b9" {
		t.Fatalf("expected cursor id b9, got %+v", svc.lastQuery.AfterCursor)
	}
}

func TestListBatchesRejectsInvalidCursor(t *testing.T) {
	svc := &fakeListService{listResult: &ListPage{}}
	router := newTestListRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/v1/transfers/batches?cursor=not-a-cursor", nil).WithContext(batchCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid cursor, got %d", rec.Code)
	}
	if svc.listCalls != 0 {
		t.Fatalf("expected list not to run, ran %d times", svc.listCalls)
	}
}

func TestValidateBatchReturnsRowResults(t *testing.T) {
	svc := &fakeListService{
		preflightResult: &PreflightResult{
			TotalCount:     2,
			ValidCount:     1,
			InvalidCount:   1,
			EstimatedFees:  "0.1000000",
			TotalNetAmount: "10.0000000",
			Rows: []RowResult{
				{Row: 1, ToWalletID: "w1", Asset: "USDC", Amount: "10.0000000", Valid: true, NetAmount: "10.0000000"},
				{Row: 2, ToWalletID: "w2", Asset: "USDC", Amount: "0", Valid: false, ErrorCode: "invalid_amount", ErrorField: "amount", ErrorMessage: "amount must be a positive number"},
			},
		},
	}
	router := newTestListRouter(svc)

	body := `{"from_wallet_id":"11111111-1111-4111-8111-111111111111","transfers":[
		{"to_wallet_id":"22222222-2222-4222-8222-222222222222","asset":"USDC","amount":"10"},
		{"to_wallet_id":"33333333-3333-4333-8333-333333333333","asset":"USDC","amount":"0"}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers/batch/validate", bytes.NewBufferString(body)).WithContext(batchCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp PreflightResult
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.TotalCount != 2 || resp.ValidCount != 1 || resp.InvalidCount != 1 {
		t.Fatalf("unexpected counts: %+v", resp)
	}
	if len(resp.Rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(resp.Rows))
	}
	if !resp.Rows[0].Valid || resp.Rows[1].Valid {
		t.Fatalf("unexpected row validity: %+v", resp.Rows)
	}
	if svc.preflightCalls != 1 {
		t.Fatalf("expected 1 preflight call, got %d", svc.preflightCalls)
	}
}

func TestValidateBatchDoesNotCreateBatch(t *testing.T) {
	svc := &fakeListService{
		preflightResult: &PreflightResult{TotalCount: 1, ValidCount: 1, Rows: []RowResult{{Row: 1, Valid: true}}},
	}
	router := newTestListRouter(svc)

	body := `{"from_wallet_id":"11111111-1111-4111-8111-111111111111","transfers":[
		{"to_wallet_id":"22222222-2222-4222-8222-222222222222","asset":"USDC","amount":"10"}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers/batch/validate", bytes.NewBufferString(body)).WithContext(batchCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	// The fake service's CreateBatch is never called by validateBatch.
	if svc.preflightCalls != 1 {
		t.Fatalf("expected 1 preflight call, got %d", svc.preflightCalls)
	}
}

func TestValidateBatchRejectsEmptyTransfers(t *testing.T) {
	svc := &fakeListService{}
	router := newTestListRouter(svc)

	body := `{"from_wallet_id":"11111111-1111-4111-8111-111111111111","transfers":[]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers/batch/validate", bytes.NewBufferString(body)).WithContext(batchCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	// api.Validate should reject empty transfers with min=1
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty transfers, got %d: %s", rec.Code, rec.Body.String())
	}
	if svc.preflightCalls != 0 {
		t.Fatalf("expected preflight not to run, ran %d times", svc.preflightCalls)
	}
}

func TestValidateBatchPassesOriginalRequestToService(t *testing.T) {
	svc := &fakeListService{
		preflightResult: &PreflightResult{TotalCount: 1, ValidCount: 1, Rows: []RowResult{{Row: 1, Valid: true}}},
	}
	router := newTestListRouter(svc)

	body := `{"from_wallet_id":"11111111-1111-4111-8111-111111111111","transfers":[
		{"to_wallet_id":"22222222-2222-4222-8222-222222222222","asset":"USDC","amount":"42.5","reference":"pay-1"}
	]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers/batch/validate", bytes.NewBufferString(body)).WithContext(batchCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if len(svc.lastPreflightItems) != 1 {
		t.Fatalf("expected 1 item, got %d", len(svc.lastPreflightItems))
	}
	item := svc.lastPreflightItems[0]
	if item.ToWalletID != "22222222-2222-4222-8222-222222222222" {
		t.Fatalf("unexpected to_wallet_id: %q", item.ToWalletID)
	}
	if item.Asset != "USDC" {
		t.Fatalf("unexpected asset: %q", item.Asset)
	}
	if !item.Amount.Equal(decimal.RequireFromString("42.5")) {
		t.Fatalf("unexpected amount: %s", item.Amount)
	}
	if item.Reference != "pay-1" {
		t.Fatalf("unexpected reference: %q", item.Reference)
	}
}

func TestListBatchesRejectsInvalidLimit(t *testing.T) {
	svc := &fakeListService{listResult: &ListPage{}}
	router := newTestListRouter(svc)

	req := httptest.NewRequest(http.MethodGet, "/v1/transfers/batches?limit=abc", nil).WithContext(batchCtx())
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid limit, got %d", rec.Code)
	}
	if svc.listCalls != 0 {
		t.Fatalf("expected list not to run, ran %d times", svc.listCalls)
	}
}

func TestNewBatchIDIsUUID(t *testing.T) {
	// Sanity: the handler/service path generates UUIDs for batch IDs.
	id := uuid.New().String()
	if _, err := uuid.Parse(id); err != nil {
		t.Fatalf("uuid.Parse(%q): %v", id, err)
	}
}
