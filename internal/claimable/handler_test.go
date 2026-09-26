package claimable_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/claimable"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/stellar/go/keypair"
)

// newRouter mounts the claimable routes the way the API does, so the tests
// exercise the real chi routing (path params included) rather than calling the
// handlers directly.
func newRouter(t *testing.T, f *fixture) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/v1/claimable-balances", claimable.NewHandler(f.svc).Routes())
	return r
}

func do(t *testing.T, router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestCreateRouteReturnsBalanceIDAndReserve(t *testing.T) {
	f := newFixture(t)
	router := newRouter(t, f)

	body := `{
		"asset": "XLM",
		"amount": "12.5",
		"claimants": [{
			"account": "` + f.claimant.Address() + `",
			"predicate": {"type": "before_absolute_time", "timestamp": ` +
		strconv.FormatInt(time.Now().Add(24*time.Hour).Unix(), 10) + `}
		}]
	}`

	rec := do(t, router, http.MethodPost, "/v1/claimable-balances", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	balanceID, _ := resp["balance_id"].(string)
	if balanceID == "" {
		t.Fatalf("expected a balance id, got %+v", resp)
	}
	if resp["reserve_required"] != "0.5000000" {
		t.Errorf("expected reserve_required 0.5000000, got %v", resp["reserve_required"])
	}
	if resp["sponsored"] != false {
		t.Errorf("expected sponsored false, got %v", resp["sponsored"])
	}
	if _, ok := f.repo.balances[balanceID]; !ok {
		t.Error("expected the created balance to be persisted")
	}
}

func TestCreateRouteReturnsSponsoredFlag(t *testing.T) {
	f := newFixture(t)
	body := `{
		"asset": "XLM",
		"amount": "3",
		"sponsor_account": "` + f.sponsor.Address() + `",
		"claimants": [{"account": "` + f.claimant.Address() + `"}]
	}`

	rec := do(t, newRouter(t, f), http.MethodPost, "/v1/claimable-balances", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["sponsored"] != true {
		t.Errorf("expected sponsored true, got %v", resp["sponsored"])
	}
}

func TestCreateRouteRejectsEmptyClaimants(t *testing.T) {
	f := newFixture(t)
	rec := do(t, newRouter(t, f), http.MethodPost, "/v1/claimable-balances",
		`{"asset":"XLM","amount":"1","claimants":[]}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateRouteRejectsNonPositiveAmount(t *testing.T) {
	f := newFixture(t)
	body := `{"asset":"XLM","amount":"0","claimants":[{"account":"` + keypair.MustRandom().Address() + `"}]}`
	rec := do(t, newRouter(t, f), http.MethodPost, "/v1/claimable-balances", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestCreateRouteRejectsUncustodiedSponsor(t *testing.T) {
	f := newFixture(t)
	body := `{
		"asset": "XLM",
		"amount": "3",
		"sponsor_account": "` + keypair.MustRandom().Address() + `",
		"claimants": [{"account": "` + f.claimant.Address() + `"}]
	}`

	rec := do(t, newRouter(t, f), http.MethodPost, "/v1/claimable-balances", body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListRouteFiltersToPending(t *testing.T) {
	f := newFixture(t)

	claimants := []domain.Claimant{{Account: f.claimant.Address()}}
	f.seed("balance-pending", future(), false, claimants...)
	f.seed("balance-claimed", future(), false, claimants...)
	f.repo.balances["balance-claimed"].Status = domain.ClaimableBalanceStatusClaimed

	rec := do(t, newRouter(t, f), http.MethodGet, "/v1/claimable-balances?status=pending&limit=50", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Balances []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"claimable_balances"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Balances) != 1 {
		t.Fatalf("status=pending must return only pending balances, got %+v", resp.Balances)
	}
	if resp.Balances[0].Status != string(domain.ClaimableBalanceStatusPending) {
		t.Errorf("status=pending returned a %s balance", resp.Balances[0].Status)
	}
}

func TestListRouteRejectsBadExpiryFilter(t *testing.T) {
	f := newFixture(t)
	rec := do(t, newRouter(t, f), http.MethodGet, "/v1/claimable-balances?expires_before=yesterday", "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}

func TestGetRouteReturnsNotFound(t *testing.T) {
	f := newFixture(t)
	rec := do(t, newRouter(t, f), http.MethodGet, "/v1/claimable-balances/does-not-exist", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetRouteReturnsLiveStatus(t *testing.T) {
	f := newFixture(t)

	// The label aliases a well-formed Stellar balance ID, so the route can be
	// addressed by a readable name.
	id := f.seed("balance-get", future(), false, domain.Claimant{Account: f.claimant.Address()})

	rec := do(t, newRouter(t, f), http.MethodGet, "/v1/claimable-balances/"+id, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		ID        string `json:"id"`
		Claimable bool   `json:"claimable"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.ID != id {
		t.Errorf("expected %s, got %q", id, resp.ID)
	}
	if !resp.Claimable {
		t.Error("expected the unconditional claimant to be claimable")
	}
}

func TestClaimRouteRejectsUnsatisfiablePredicateWithHonestCode(t *testing.T) {
	f := newFixture(t)
	id := f.seed("balance-closed", future(), false, domain.Claimant{
		Account: f.claimant.Address(),
		Predicate: &domain.ClaimPredicate{
			Type:      domain.PredicateBeforeAbsoluteTime,
			Timestamp: time.Now().UTC().Add(-time.Minute).Unix(),
		},
	})

	body := `{"claimant_account":"` + f.claimant.Address() + `"}`
	rec := do(t, newRouter(t, f), http.MethodPost, "/v1/claimable-balances/"+id+"/claim", body)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Error.Code != "PREDICATE_NOT_SATISFIABLE" {
		t.Errorf("expected PREDICATE_NOT_SATISFIABLE, got %q", resp.Error.Code)
	}
	if len(f.stellar.submitted) != 0 {
		t.Error("expected no Horizon submission for an unsatisfiable predicate")
	}
}

func TestClaimRouteConflictsWhenAlreadyClaimed(t *testing.T) {
	f := newFixture(t)
	id := f.seed("balance-done", future(), false, domain.Claimant{Account: f.claimant.Address()})
	f.repo.balances["balance-done"].Status = domain.ClaimableBalanceStatusClaimed

	rec := do(t, newRouter(t, f), http.MethodPost, "/v1/claimable-balances/"+id+"/claim", "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestClaimRouteClaimsWithoutABody(t *testing.T) {
	f := newFixture(t)
	id := f.seed("balance-one", future(), false, domain.Claimant{Account: f.claimant.Address()})

	rec := do(t, newRouter(t, f), http.MethodPost, "/v1/claimable-balances/"+id+"/claim", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
