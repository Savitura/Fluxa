package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/anchor"
	"github.com/fluxa/fluxa/internal/apikey"
	"github.com/fluxa/fluxa/internal/auth"
	"github.com/fluxa/fluxa/internal/batch"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/fluxa/fluxa/internal/fx"
	"github.com/fluxa/fluxa/internal/org"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/fluxa/fluxa/internal/schedule"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/treasury"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/fluxa/fluxa/internal/webhook"
)

var authzJWTSecret = []byte("test-secret-authz")

// ---------------------------------------------------------------------------
// Mock membership validator
// ---------------------------------------------------------------------------

type mockMembershipValidator struct {
	// members maps "tenantID:userID" → *domain.OrgMember
	members map[string]*domain.OrgMember
}

func newMockMembershipValidator(members ...*domain.OrgMember) *mockMembershipValidator {
	m := &mockMembershipValidator{members: make(map[string]*domain.OrgMember)}
	for _, mem := range members {
		m.members[mem.TenantID+":"+mem.UserID] = mem
	}
	return m
}

func (m *mockMembershipValidator) GetMember(_ context.Context, tenantID, userID string) (*domain.OrgMember, error) {
	mem, ok := m.members[tenantID+":"+userID]
	if !ok {
		return nil, domain.ErrOrgMemberNotFound
	}
	return mem, nil
}

// nilValidator always returns not-found — useful for testing removal.
type nilValidator struct{}

func (nilValidator) GetMember(_ context.Context, _, _ string) (*domain.OrgMember, error) {
	return nil, domain.ErrOrgMemberNotFound
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newAuthzTestServerWithValidator(t *testing.T, validator MembershipValidator) *Server {
	t.Helper()

	treasuryHandler := treasury.NewHandler(nil).WithMutationGate(RequireRole(domain.RoleOwner, domain.RoleAdmin))

	return New(
		auth.NewHandler(nil),
		org.NewHandler(nil),
		wallet.NewHandler(nil),
		transfer.NewHandler(nil),
		fx.NewHandler(nil),
		fiat.NewHandler(nil),
		fiat.NewAnchorHandler(nil),
		anchor.NewHandler(nil),
		fees.NewHandler(nil),
		reconcile.NewHandler(nil),
		apikey.NewHandler(nil),
		nil,
		webhook.NewHandler(nil),
		batch.NewHandler(nil),
		schedule.NewHandler(nil),
		treasuryHandler,
		nil,
		authzJWTSecret,
		"0",
		nil,
		validator,
	)
}

func mustToken(t *testing.T, role string) string {
	t.Helper()
	tok, err := auth.GenerateToken("user-1", "tenant-1", role, "user@example.com", "access", authzJWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return tok
}

func mustTokenForUser(t *testing.T, userID, tenantID, role string) string {
	t.Helper()
	tok, err := auth.GenerateToken(userID, tenantID, role, "user@example.com", "access", authzJWTSecret, time.Hour)
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	return tok
}

func doRequestWithToken(t *testing.T, srv *Server, method, path, token string) int {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	srv.router.ServeHTTP(rec, req)
	return rec.Code
}

func doRequest(t *testing.T, srv *Server, method, path, role string) int {
	t.Helper()
	return doRequestWithToken(t, srv, method, path, mustToken(t, role))
}

// ---------------------------------------------------------------------------
// Existing tests (updated to pass validator)
// ---------------------------------------------------------------------------

func TestAdminRoutesRequireOwnerOrAdmin(t *testing.T) {
	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/admin/fees/collected"},
		{http.MethodGet, "/v1/admin/anchors"},
		{http.MethodPost, "/v1/admin/anchors"},
		{http.MethodGet, "/v1/admin/reconciliation/summary"},
		{http.MethodPost, "/v1/admin/reconciliation/run"},
		{http.MethodGet, "/v1/admin/treasury/balances"},
		{http.MethodPost, "/v1/admin/treasury/sweep"},
		{http.MethodPut, "/v1/admin/treasury/config"},
	}

	for _, rt := range routes {
		for _, role := range []string{domain.RoleViewer, domain.RoleDeveloper} {
			t.Run(rt.method+" "+rt.path+"/"+role, func(t *testing.T) {
				// Mint a token with the JWT role, but the validator maps
				// user-1/tenant-1 to "owner", so the middleware will override
				// the role. We need per-role validators for this test.
				roleValidator := newMockMembershipValidator(
					&domain.OrgMember{TenantID: "tenant-1", UserID: "user-1", Role: role},
				)
				srv := newAuthzTestServerWithValidator(t, roleValidator)
				code := doRequest(t, srv, rt.method, rt.path, role)
				if code != http.StatusForbidden {
					t.Fatalf("role %q on %s %s: expected 403, got %d", role, rt.method, rt.path, code)
				}
			})
		}

		for _, role := range []string{domain.RoleAdmin, domain.RoleOwner} {
			t.Run(rt.method+" "+rt.path+"/"+role, func(t *testing.T) {
				roleValidator := newMockMembershipValidator(
					&domain.OrgMember{TenantID: "tenant-1", UserID: "user-1", Role: role},
				)
				srv := newAuthzTestServerWithValidator(t, roleValidator)
				code := doRequest(t, srv, rt.method, rt.path, role)
				if code == http.StatusForbidden || code == http.StatusUnauthorized {
					t.Fatalf("role %q on %s %s: expected to pass authorization, got %d", role, rt.method, rt.path, code)
				}
			})
		}
	}
}

func TestOperationalRoutesAllowDeveloper(t *testing.T) {
	// Viewer gets 403 on RequireNotViewer
	viewerValidator := newMockMembershipValidator(
		&domain.OrgMember{TenantID: "tenant-1", UserID: "user-1", Role: domain.RoleViewer},
	)
	srv := newAuthzTestServerWithValidator(t, viewerValidator)
	code := doRequest(t, srv, http.MethodGet, "/v1/fx/rates", domain.RoleViewer)
	if code != http.StatusForbidden {
		t.Fatalf("viewer on mutating-capable group: expected 403, got %d", code)
	}

	// Developer, admin, owner pass
	for _, role := range []string{domain.RoleDeveloper, domain.RoleAdmin, domain.RoleOwner} {
		roleValidator := newMockMembershipValidator(
			&domain.OrgMember{TenantID: "tenant-1", UserID: "user-1", Role: role},
		)
		srv := newAuthzTestServerWithValidator(t, roleValidator)
		code := doRequest(t, srv, http.MethodGet, "/v1/fx/rates", role)
		if code == http.StatusForbidden || code == http.StatusUnauthorized {
			t.Fatalf("role %q on operational route: expected to pass authorization, got %d", role, code)
		}
	}
}

// ---------------------------------------------------------------------------
// New tests: membership revalidation
// ---------------------------------------------------------------------------

// TestRemovedMemberReturns403 verifies that a user whose membership has been
// deleted gets a 403 even with a valid JWT.
func TestRemovedMemberReturns403(t *testing.T) {
	srv := newAuthzTestServerWithValidator(t, &nilValidator{})

	code := doRequest(t, srv, http.MethodGet, "/v1/fx/rates", domain.RoleAdmin)
	if code != http.StatusForbidden {
		t.Fatalf("removed member: expected 403, got %d", code)
	}
}

// TestDemotedMemberUsesCurrentRole verifies that a user who was demoted from
// admin to developer via DB gets the downgraded role on the request, and is
// then rejected by RequireRole for admin-only routes.
func TestDemotedMemberUsesCurrentRole(t *testing.T) {
	// Token says "admin", but DB says "developer"
	demotedValidator := newMockMembershipValidator(
		&domain.OrgMember{TenantID: "tenant-1", UserID: "user-1", Role: domain.RoleDeveloper},
	)
	srv := newAuthzTestServerWithValidator(t, demotedValidator)

	code := doRequest(t, srv, http.MethodGet, "/v1/admin/fees/collected", domain.RoleAdmin)
	if code != http.StatusForbidden {
		t.Fatalf("demoted admin: expected 403, got %d", code)
	}
}

// TestPromotedMemberUsesCurrentRole verifies that a user promoted in the DB
// (token says "developer", DB says "admin") gains elevated access immediately.
func TestPromotedMemberUsesCurrentRole(t *testing.T) {
	// Token says "developer", but DB says "admin"
	promotedValidator := newMockMembershipValidator(
		&domain.OrgMember{TenantID: "tenant-1", UserID: "user-1", Role: domain.RoleAdmin},
	)
	srv := newAuthzTestServerWithValidator(t, promotedValidator)

	code := doRequest(t, srv, http.MethodGet, "/v1/admin/fees/collected", domain.RoleDeveloper)
	if code == http.StatusForbidden || code == http.StatusUnauthorized {
		t.Fatalf("promoted developer: expected to pass, got %d", code)
	}
}

// TestRevokedMembershipReturns403 verifies the tenant context is not supplied
// by stale claims when membership is revoked.
func TestRevokedMembershipReturns403(t *testing.T) {
	srv := newAuthzTestServerWithValidator(t, &nilValidator{})

	// Try an admin-only route — should be 403, not 404 or 200.
	code := doRequest(t, srv, http.MethodGet, "/v1/admin/anchors", domain.RoleOwner)
	if code != http.StatusForbidden {
		t.Fatalf("revoked membership on admin route: expected 403, got %d", code)
	}
}

// TestCrossTenantMembershipRejected verifies that a valid JWT for tenant-A
// is rejected when the validator has no membership for that user in tenant-A.
func TestCrossTenantMembershipRejected(t *testing.T) {
	// Validator only knows tenant-1, but token claims tenant-2
	validator := newMockMembershipValidator(
		&domain.OrgMember{TenantID: "tenant-1", UserID: "user-1", Role: domain.RoleAdmin},
	)
	srv := newAuthzTestServerWithValidator(t, validator)

	tok := mustTokenForUser(t, "user-1", "tenant-2", domain.RoleAdmin)
	code := doRequestWithToken(t, srv, http.MethodGet, "/v1/fx/rates", tok)
	if code != http.StatusForbidden {
		t.Fatalf("cross-tenant membership: expected 403, got %d", code)
	}
}

// TestRoleMismatchUsesDBRole verifies that even if the JWT claims owner,
// the middleware enforces the DB role. The request reaches a route that only
// allows owner/admin — if the DB says "viewer", it should be 403.
func TestRoleMismatchUsesDBRole(t *testing.T) {
	validator := newMockMembershipValidator(
		&domain.OrgMember{TenantID: "tenant-1", UserID: "user-1", Role: domain.RoleViewer},
	)
	srv := newAuthzTestServerWithValidator(t, validator)

	code := doRequest(t, srv, http.MethodGet, "/v1/admin/fees/collected", domain.RoleOwner)
	if code != http.StatusForbidden {
		t.Fatalf("role mismatch (JWT=owner, DB=viewer): expected 403, got %d", code)
	}
}
