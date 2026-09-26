package wallet_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

type fakeContractService struct{}

func (f *fakeContractService) CreateWallet(ctx context.Context, ownerPublicKey ...string) (*domain.Wallet, error) {
	return &domain.Wallet{ID: "w-1"}, nil
}
func (f *fakeContractService) GetWalletForHandler(ctx context.Context, walletID string) (*domain.Wallet, error) {
	return &domain.Wallet{ID: walletID}, nil
}
func (f *fakeContractService) GetBalances(ctx context.Context, walletID string, includeFX ...string) ([]wallet.Balance, error) {
	return nil, nil
}
func (f *fakeContractService) AddTrustline(ctx context.Context, walletID, assetCode, issuer, limit string) (string, error) {
	return "tx", nil
}
func (f *fakeContractService) ExecuteTransfer(ctx context.Context, walletID, destination, assetCode, issuer string, amount decimal.Decimal, memo string) (string, error) {
	return "tx", nil
}
func (f *fakeContractService) WithSigner(signer stellar.Signer) wallet.Service          { return f }
func (f *fakeContractService) WithFXService(fxSvc wallet.FXRateGetter) wallet.Service   { return f }
func (f *fakeContractService) WithIssuers(usdcIssuer, eurcIssuer string) wallet.Service { return f }
func (f *fakeContractService) GetContractState(ctx context.Context, walletID string) (*wallet.ContractState, error) {
	return &wallet.ContractState{}, nil
}
func (f *fakeContractService) GetSpendingStatus(ctx context.Context, walletID string) (*wallet.SpendingStatus, error) {
	return &wallet.SpendingStatus{}, nil
}
func (f *fakeContractService) AddGuardian(ctx context.Context, walletID, guardian string) (string, error) {
	return "tx-guardian", nil
}
func (f *fakeContractService) RemoveGuardian(ctx context.Context, walletID, guardian string) (string, error) {
	return "tx-guardian", nil
}
func (f *fakeContractService) SetTimeLock(ctx context.Context, walletID string, untilTimestamp uint64) (string, error) {
	return "tx-timelock", nil
}

// requireRole mirrors internal/server.RequireRole for test purposes; it
// cannot be imported directly since internal/server imports internal/wallet.
func requireRole(allowed ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := tenant.RoleFromContext(r.Context())
			for _, a := range allowed {
				if role == a {
					next.ServeHTTP(w, r)
					return
				}
			}
			http.Error(w, "insufficient permissions", http.StatusForbidden)
		})
	}
}

func newGuardianTestRouter(t *testing.T) chi.Router {
	t.Helper()
	h := wallet.NewHandler(&fakeContractService{}).
		WithContractService(&fakeContractService{}).
		WithGuardianGate(requireRole(domain.RoleOwner, domain.RoleAdmin))

	r := chi.NewRouter()
	r.Route("/wallets", h.Routes())
	return r
}

func doGuardianRequest(t *testing.T, r chi.Router, method, path, role string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	ctx := tenant.WithUser(req.Context(), "user-1", role)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code
}

func TestGuardianAndTimeLockRoutesRequireOwnerOrAdmin(t *testing.T) {
	r := newGuardianTestRouter(t)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/wallets/w-1/guardians"},
		{http.MethodDelete, "/wallets/w-1/guardians/GADDR"},
		{http.MethodPost, "/wallets/w-1/time-lock"},
	}

	for _, rt := range routes {
		for _, role := range []string{domain.RoleViewer, domain.RoleDeveloper} {
			code := doGuardianRequest(t, r, rt.method, rt.path, role)
			if code != http.StatusForbidden {
				t.Fatalf("role %q on %s %s: expected 403, got %d", role, rt.method, rt.path, code)
			}
		}

		for _, role := range []string{domain.RoleAdmin, domain.RoleOwner} {
			code := doGuardianRequest(t, r, rt.method, rt.path, role)
			if code == http.StatusForbidden {
				t.Fatalf("role %q on %s %s: expected to pass authorization, got %d", role, rt.method, rt.path, code)
			}
		}
	}
}

func TestNonGuardianWalletRoutesRemainOpenToDeveloper(t *testing.T) {
	r := newGuardianTestRouter(t)

	code := doGuardianRequest(t, r, http.MethodGet, "/wallets/w-1/contract-state", domain.RoleDeveloper)
	if code == http.StatusForbidden {
		t.Fatalf("developer role on non-guardian route: expected to pass, got %d", code)
	}
}
