package server_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
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
	"github.com/fluxa/fluxa/internal/paymentlink"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/fluxa/fluxa/internal/schedule"
	"github.com/fluxa/fluxa/internal/server"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/treasury"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/go-chi/chi/v5"
)

type dummyAuthService struct{}

func (d *dummyAuthService) Register(ctx context.Context, req auth.RegisterRequest) (*auth.AuthResponse, error) {
	return &auth.AuthResponse{User: &domain.User{ID: "u1", Email: req.Email}}, nil
}

func (d *dummyAuthService) Login(ctx context.Context, email, password string) (*auth.AuthResponse, error) {
	return &auth.AuthResponse{User: &domain.User{ID: "u1", Email: email}}, nil
}

func (d *dummyAuthService) RefreshToken(ctx context.Context, token string) (*auth.AuthResponse, error) {
	return &auth.AuthResponse{}, nil
}

type dummyOrgService struct{}

func (d *dummyOrgService) InviteMember(ctx context.Context, email, role string) (*domain.OrgInvite, error) {
	return &domain.OrgInvite{}, nil
}

func (d *dummyOrgService) AcceptInvite(ctx context.Context, req org.AcceptInviteRequest) (*auth.AuthResponse, error) {
	return &auth.AuthResponse{}, nil
}

func (d *dummyOrgService) ListMembers(ctx context.Context) ([]*domain.OrgMember, error) {
	return nil, nil
}

func (d *dummyOrgService) UpdateRole(ctx context.Context, memberID, role string) error {
	return nil
}

func (d *dummyOrgService) RemoveMember(ctx context.Context, memberID string) error {
	return nil
}

func setupTestServerWithRateLimit(cfg server.AuthRateLimitConfig, options ...interface{}) *chi.Mux {
	authH := auth.NewHandler(&dummyAuthService{})
	orgH := org.NewHandler(&dummyOrgService{})

	treasuryH := treasury.NewHandler(nil)

	srv := server.New(
		authH,
		orgH,
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
		treasuryH,
		nil, // claimableHandler
		nil, // statusHandler
		nil, // complianceHandler
		nil, // auditHandler
		nil, // usageHandler
		[]byte("test-jwt-secret-key-32-bytes-long!"),
		"0",
		nil,
		nil,
		nil,
		append([]interface{}{cfg}, options...)...,
	)
	return srv.Router()
}

type emptyPaymentLinkRepository struct{}

func (emptyPaymentLinkRepository) Create(context.Context, *paymentlink.Link) error { return nil }
func (emptyPaymentLinkRepository) GetByIdempotencyKey(context.Context, string) (*paymentlink.Link, error) {
	return nil, paymentlink.ErrNotFound
}
func (emptyPaymentLinkRepository) List(context.Context) ([]*paymentlink.Link, error) { return nil, nil }
func (emptyPaymentLinkRepository) Get(context.Context, string) (*paymentlink.Link, error) {
	return nil, paymentlink.ErrNotFound
}
func (emptyPaymentLinkRepository) GetPublic(context.Context, string) (*paymentlink.Link, error) {
	return nil, paymentlink.ErrNotFound
}
func (emptyPaymentLinkRepository) Claim(context.Context, string) (*paymentlink.Link, error) {
	return nil, paymentlink.ErrNotAvailable
}
func (emptyPaymentLinkRepository) Release(context.Context, string) error { return nil }
func (emptyPaymentLinkRepository) Cancel(context.Context, string) error  { return nil }
func (emptyPaymentLinkRepository) ReleaseStale(context.Context, time.Time, int) (int, error) {
	return 0, nil
}

func TestAuthRateLimiter_PerIPLimiting(t *testing.T) {
	// Configure low burst for testing: IP burst 3, Account burst 10
	cfg := server.AuthRateLimitConfig{
		IPRPS:        1,
		IPBurst:      3,
		AccountRPS:   10,
		AccountBurst: 10,
	}
	router := setupTestServerWithRateLimit(cfg)

	// Test on /v1/auth/login
	for i := 1; i <= 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"email":"user`+strconv.Itoa(i)+`@example.com","password":"password123"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.1:1234"
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d expected status 200, got %d", i, rec.Code)
		}
	}

	// 4th request from same IP exceeds IP burst
	req4 := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"email":"user4@example.com","password":"password123"}`))
	req4.Header.Set("Content-Type", "application/json")
	req4.RemoteAddr = "192.0.2.1:1234"
	rec4 := httptest.NewRecorder()

	router.ServeHTTP(rec4, req4)
	if rec4.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests, got %d: %s", rec4.Code, rec4.Body.String())
	}

	retryAfter := rec4.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected Retry-After header on 429 response, got none")
	}
	retrySec, err := strconv.Atoi(retryAfter)
	if err != nil || retrySec <= 0 {
		t.Fatalf("invalid Retry-After header: %q", retryAfter)
	}

	if !strings.Contains(rec4.Body.String(), "RATE_LIMITED") {
		t.Fatalf("expected RATE_LIMITED error code in body: %s", rec4.Body.String())
	}

	// A different IP should still succeed
	reqOtherIP := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"email":"user5@example.com","password":"password123"}`))
	reqOtherIP.Header.Set("Content-Type", "application/json")
	reqOtherIP.RemoteAddr = "192.0.2.2:1234"
	recOtherIP := httptest.NewRecorder()

	router.ServeHTTP(recOtherIP, reqOtherIP)
	if recOtherIP.Code != http.StatusOK {
		t.Fatalf("expected status 200 from different IP, got %d", recOtherIP.Code)
	}
}

func TestAuthRateLimiter_IgnoresSpoofedForwardedFor(t *testing.T) {
	router := setupTestServerWithRateLimit(server.AuthRateLimitConfig{IPRPS: 1, IPBurst: 1, AccountRPS: 10, AccountBurst: 10})
	for i, forwardedIP := range []string{"198.51.100.1", "198.51.100.2"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"email":"user`+strconv.Itoa(i)+`@example.com","password":"password123"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", forwardedIP)
		req.RemoteAddr = "192.0.2.10:1234"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if i == 0 && rec.Code != http.StatusOK {
			t.Fatalf("first request status = %d, want 200", rec.Code)
		}
		if i == 1 && rec.Code != http.StatusTooManyRequests {
			t.Fatalf("spoofed X-Forwarded-For changed the rate-limit key: status = %d, want 429", rec.Code)
		}
	}
}

func TestAuthRateLimiter_UsesForwardedForFromTrustedProxy(t *testing.T) {
	trustedProxy := server.TrustedProxyConfig{CIDRs: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}}
	router := setupTestServerWithRateLimit(server.AuthRateLimitConfig{IPRPS: 1, IPBurst: 1, AccountRPS: 10, AccountBurst: 10}, trustedProxy)
	for i, forwardedIP := range []string{"198.51.100.1", "198.51.100.2"} {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"email":"proxy`+strconv.Itoa(i)+`@example.com","password":"password123"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", forwardedIP)
		req.RemoteAddr = "192.0.2.10:1234"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request from forwarded IP %s status = %d, want 200", forwardedIP, rec.Code)
		}
	}
}

func TestAuthRefreshIsRateLimitedAndMetricsAreNotPublic(t *testing.T) {
	router := setupTestServerWithRateLimit(server.AuthRateLimitConfig{IPRPS: 1, IPBurst: 1, AccountRPS: 10, AccountBurst: 10})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/refresh", bytes.NewBufferString(`{"refresh_token":"token"}`))
		req.RemoteAddr = "192.0.2.20:1234"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		want := http.StatusOK
		if i == 1 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("refresh request %d status = %d, want %d", i+1, rec.Code, want)
		}
	}

	metricsReq := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	metricsRec := httptest.NewRecorder()
	router.ServeHTTP(metricsRec, metricsReq)
	if metricsRec.Code != http.StatusNotFound {
		t.Fatalf("GET /metrics status = %d, want 404", metricsRec.Code)
	}
}

func TestPublicPaymentLinkCheckoutHasTighterRateLimit(t *testing.T) {
	handler := paymentlink.NewHandler(paymentlink.NewService(emptyPaymentLinkRepository{}, nil))
	router := setupTestServerWithRateLimit(server.DefaultAuthRateLimitConfig(), handler)

	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/public/payment-links/token/checkout", bytes.NewBufferString(`{"email":"buyer@example.com","name":"Buyer"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.30:1234"
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		want := http.StatusConflict
		if i == 3 {
			want = http.StatusTooManyRequests
		}
		if rec.Code != want {
			t.Fatalf("checkout request %d status = %d, want %d", i+1, rec.Code, want)
		}
	}

	otherIPReq := httptest.NewRequest(http.MethodPost, "/v1/public/payment-links/token/checkout", bytes.NewBufferString(`{"email":"buyer@example.com","name":"Buyer"}`))
	otherIPReq.Header.Set("Content-Type", "application/json")
	otherIPReq.RemoteAddr = "192.0.2.31:1234"
	otherIPRec := httptest.NewRecorder()
	router.ServeHTTP(otherIPRec, otherIPReq)
	if otherIPRec.Code != http.StatusConflict {
		t.Fatalf("checkout from a different IP status = %d, want 409 (not rate limited)", otherIPRec.Code)
	}

	for i := 0; i < 21; i++ {
		readReq := httptest.NewRequest(http.MethodGet, "/v1/public/payment-links/token", nil)
		readReq.RemoteAddr = "192.0.2.30:1234"
		readRec := httptest.NewRecorder()
		router.ServeHTTP(readRec, readReq)
		want := http.StatusNotFound
		if i == 20 {
			want = http.StatusTooManyRequests
		}
		if readRec.Code != want {
			t.Fatalf("public link read request %d status = %d, want %d", i+1, readRec.Code, want)
		}
	}
}

func TestAuthRateLimiter_PerAccountLimiting(t *testing.T) {
	// Configure high IP burst, low account burst: IP burst 10, Account burst 2
	cfg := server.AuthRateLimitConfig{
		IPRPS:        10,
		IPBurst:      10,
		AccountRPS:   1,
		AccountBurst: 2,
	}
	router := setupTestServerWithRateLimit(cfg)

	targetEmail := "victim@example.com"

	// 2 requests for victim@example.com from DIFFERENT IPs
	for i := 1; i <= 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"email":"`+targetEmail+`","password":"password123"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2." + strconv.Itoa(i) + ":1234"
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("request %d expected status 200, got %d", i, rec.Code)
		}
	}

	// 3rd request for same account from yet another IP should trigger per-account rate limit
	req3 := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"email":"`+targetEmail+`","password":"password123"}`))
	req3.Header.Set("Content-Type", "application/json")
	req3.RemoteAddr = "192.0.2.99:1234"
	rec3 := httptest.NewRecorder()

	router.ServeHTTP(rec3, req3)
	if rec3.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests on targeted account, got %d: %s", rec3.Code, rec3.Body.String())
	}

	retryAfter := rec3.Header().Get("Retry-After")
	if retryAfter == "" {
		t.Fatal("expected Retry-After header on 429 response, got none")
	}

	// Different account should succeed
	reqOtherAcct := httptest.NewRequest(http.MethodPost, "/v1/auth/login", bytes.NewBufferString(`{"email":"other@example.com","password":"password123"}`))
	reqOtherAcct.Header.Set("Content-Type", "application/json")
	reqOtherAcct.RemoteAddr = "192.0.2.100:1234"
	recOtherAcct := httptest.NewRecorder()

	router.ServeHTTP(recOtherAcct, reqOtherAcct)
	if recOtherAcct.Code != http.StatusOK {
		t.Fatalf("expected status 200 for different account, got %d", recOtherAcct.Code)
	}
}

func TestAuthRateLimiter_RegisterAndInviteAcceptEndpoints(t *testing.T) {
	cfg := server.AuthRateLimitConfig{
		IPRPS:        1,
		IPBurst:      2,
		AccountRPS:   1,
		AccountBurst: 2,
	}
	router := setupTestServerWithRateLimit(cfg)

	t.Run("POST /v1/auth/register is rate limited", func(t *testing.T) {
		for i := 1; i <= 2; i++ {
			req := httptest.NewRequest(http.MethodPost, "/v1/auth/register", bytes.NewBufferString(`{"name":"User","email":"reg`+strconv.Itoa(i)+`@example.com","password":"password123"}`))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "10.0.0.1:1234"
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusCreated {
				t.Fatalf("request %d expected status 201, got %d", i, rec.Code)
			}
		}

		req3 := httptest.NewRequest(http.MethodPost, "/v1/auth/register", bytes.NewBufferString(`{"name":"User","email":"reg3@example.com","password":"password123"}`))
		req3.Header.Set("Content-Type", "application/json")
		req3.RemoteAddr = "10.0.0.1:1234"
		rec3 := httptest.NewRecorder()
		router.ServeHTTP(rec3, req3)
		if rec3.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 for register, got %d: %s", rec3.Code, rec3.Body.String())
		}
		if rec3.Header().Get("Retry-After") == "" {
			t.Fatal("expected Retry-After header for register 429")
		}
	})

	t.Run("POST /v1/org/invites/accept is rate limited", func(t *testing.T) {
		for i := 1; i <= 2; i++ {
			req := httptest.NewRequest(http.MethodPost, "/v1/org/invites/accept", bytes.NewBufferString(`{"token":"token-`+strconv.Itoa(i)+`"}`))
			req.Header.Set("Content-Type", "application/json")
			req.RemoteAddr = "10.0.0.2:1234"
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("request %d expected status 200, got %d", i, rec.Code)
			}
		}

		req3 := httptest.NewRequest(http.MethodPost, "/v1/org/invites/accept", bytes.NewBufferString(`{"token":"token-3"}`))
		req3.Header.Set("Content-Type", "application/json")
		req3.RemoteAddr = "10.0.0.2:1234"
		rec3 := httptest.NewRecorder()
		router.ServeHTTP(rec3, req3)
		if rec3.Code != http.StatusTooManyRequests {
			t.Fatalf("expected 429 for invites/accept, got %d: %s", rec3.Code, rec3.Body.String())
		}
		if rec3.Header().Get("Retry-After") == "" {
			t.Fatal("expected Retry-After header for invites/accept 429")
		}
	})
}
