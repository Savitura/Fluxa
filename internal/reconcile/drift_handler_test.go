package reconcile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

// newDriftTestRouter mounts the admin routes so the drift endpoint is exercised
// over real HTTP.
func newDriftTestRouter(svc *Service) http.Handler {
	r := chi.NewRouter()
	r.Route("/v1/admin", NewHandler(svc).AdminRoutes())
	return r
}

// newDriftTestService builds a reconciler with a single USDC wallet and the
// given drift threshold, reusing the fakes from drift_test.go.
func newDriftTestService(t *testing.T, threshold decimal.Decimal) (*Service, *driftRepo) {
	t.Helper()
	svc, repo, _, _ := newDriftService(t, "100", "100", threshold.String())
	return svc, repo
}

func decodeBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var decoded map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("non-JSON body %q: %v", rec.Body.String(), err)
	}
	return decoded
}

// TestDriftHandler_ReturnsCurrentSnapshots covers the new admin drift
// endpoint's happy path, including the count and the fields operators need.
func TestDriftHandler_ReturnsCurrentSnapshots(t *testing.T) {
	svc, repo := newDriftTestService(t, decimal.RequireFromString("1"))
	h := newDriftTestRouter(svc)

	// Seed the drift store the way a reconciliation pass would. GetDrift reads
	// the repository first and only falls back to the in-memory map.
	repo.WriteDriftSnapshot(context.Background(), &DriftSnapshot{
		ID:              "snap-1",
		TenantID:        "tenant-1",
		WalletID:        "wallet-1",
		WalletAddress:   "GADDRESS1",
		Asset:           "USDC",
		ExpectedBalance: decimal.RequireFromString("100"),
		ActualBalance:   decimal.RequireFromString("90"),
		DriftAmount:     decimal.RequireFromString("10"),
		Threshold:       decimal.RequireFromString("1"),
	})

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/reconciliation/drift", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeBody(t, rec)
	if count, _ := body["count"].(float64); count != 1 {
		t.Fatalf("count = %v, want 1, body=%v", body["count"], body)
	}
	drift, _ := body["drift"].([]interface{})
	if len(drift) != 1 {
		t.Fatalf("drift = %v, want 1 entry", body["drift"])
	}
	entry, _ := drift[0].(map[string]interface{})
	if entry["tenant_id"] != "tenant-1" {
		t.Fatalf("tenant_id = %v, want tenant-1", entry["tenant_id"])
	}
	if entry["wallet_address"] != "GADDRESS1" {
		t.Fatalf("wallet_address = %v, want GADDRESS1", entry["wallet_address"])
	}
	if entry["drift_amount"] != "10" {
		t.Fatalf("drift_amount = %v, want 10", entry["drift_amount"])
	}
	if entry["expected_balance"] != "100" {
		t.Fatalf("expected_balance = %v, want 100", entry["expected_balance"])
	}
	if entry["actual_balance"] != "90" {
		t.Fatalf("actual_balance = %v, want 90", entry["actual_balance"])
	}
}

// TestDriftHandler_EmptyWhenNoDrift is the healthy-state response.
func TestDriftHandler_EmptyWhenNoDrift(t *testing.T) {
	svc, _ := newDriftTestService(t, decimal.RequireFromString("1"))
	h := newDriftTestRouter(svc)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/reconciliation/drift", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decodeBody(t, rec)
	if count, _ := body["count"].(float64); count != 0 {
		t.Fatalf("count = %v, want 0", body["count"])
	}
	drift, ok := body["drift"].([]interface{})
	if !ok {
		t.Fatalf("drift should serialise as an array, got %T", body["drift"])
	}
	if len(drift) != 0 {
		t.Fatalf("drift = %v, want empty", drift)
	}
	if body["checked"] == nil {
		t.Fatal("response should include the time the check ran")
	}
}

// TestDriftHandler_ReturnsSnapshotCopy guards the concurrency contract: a
// caller mutating what it received must not corrupt the service's state.
func TestDriftHandler_ReturnsSnapshotCopy(t *testing.T) {
	svc, _ := newDriftTestService(t, decimal.RequireFromString("1"))

	repo := svc.driftRepo.(*driftRepo)
	repo.WriteDriftSnapshot(context.Background(), &DriftSnapshot{
		TenantID:    "tenant-1",
		Asset:       "XLM",
		DriftAmount: decimal.RequireFromString("5"),
	})

	snapshots, err := svc.GetDrift(context.Background())
	if err != nil {
		t.Fatalf("GetDrift() error: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}
	snapshots[0].DriftAmount = decimal.RequireFromString("999")
	snapshots[0].TenantID = "tampered"

	again, err := svc.GetDrift(context.Background())
	if err != nil {
		t.Fatalf("GetDrift() error: %v", err)
	}
	if again[0].TenantID != "tenant-1" {
		t.Fatalf("stored snapshot was mutated through a returned copy: %q", again[0].TenantID)
	}
	if !again[0].DriftAmount.Equal(decimal.RequireFromString("5")) {
		t.Fatalf("stored drift amount was mutated: %s", again[0].DriftAmount)
	}
}

// TestDriftHandler_RouteIsRegisteredUnderAdmin pins the path so a refactor
// cannot silently move the endpoint.
func TestDriftHandler_RouteIsRegisteredUnderAdmin(t *testing.T) {
	svc, _ := newDriftTestService(t, decimal.RequireFromString("1"))
	h := newDriftTestRouter(svc)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/admin/reconciliation/drift", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET drift = %d, want 200", rec.Code)
	}
	// A wrong method must not be treated as a match.
	badRec := httptest.NewRecorder()
	h.ServeHTTP(badRec, httptest.NewRequest(http.MethodPost, "/v1/admin/reconciliation/drift", nil))
	if badRec.Code == http.StatusOK {
		t.Fatal("POST /reconciliation/drift should not be routed")
	}
}
