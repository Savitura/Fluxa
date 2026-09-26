package webhook

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
)

// newConfigTestRouter mounts the real Routes() under a chi router and injects
// the tenant for each request, so the tests exercise the actual HTTP surface:
// status codes, JSON shape, and route wiring.
func newConfigTestRouter(t *testing.T, svc Service, tenantID string) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/webhooks", NewHandler(svc).Routes())
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if tenantID != "" {
			req = req.WithContext(tenant.WithID(req.Context(), tenantID))
		}
		r.ServeHTTP(w, req)
	})
}

func doJSON(t *testing.T, h http.Handler, method, path, body string) (int, map[string]interface{}) {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var decoded map[string]interface{}
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s returned non-JSON body %q: %v", method, path, rec.Body.String(), err)
		}
	}
	return rec.Code, decoded
}

// TestConfigHandler_GetConfigDoesNotLeakSecret is the HTTP-level guarantee for
// "secret is shown only once": the create response carries it, every later read
// does not.
func TestConfigHandler_GetConfigDoesNotLeakSecret(t *testing.T) {
	repo := newMockConfigRepo()
	svc := newConfigTestService(t, repo)
	h := newConfigTestRouter(t, svc, "tenant-1")

	// Create: the secret is revealed.
	code, created := doJSON(t, h, http.MethodPut, "/webhooks/config",
		`{"enabled":true,"url":"http://127.0.0.1:1/hook","events":["transfer.settled"]}`)
	if code != http.StatusOK {
		t.Fatalf("create status = %d, want 200", code)
	}
	secret, _ := created["secret"].(string)
	if secret == "" {
		t.Fatalf("create response must reveal the secret once, body=%v", created)
	}
	if configured, _ := created["secret_configured"].(bool); !configured {
		t.Fatalf("create response should report secret_configured, body=%v", created)
	}

	// Read: no secret.
	code, read := doJSON(t, h, http.MethodGet, "/webhooks/config", "")
	if code != http.StatusOK {
		t.Fatalf("get status = %d, want 200", code)
	}
	if got, ok := read["secret"]; ok {
		t.Fatalf("GET must not return a secret field, got %v", got)
	}
	if configured, _ := read["secret_configured"].(bool); !configured {
		t.Fatalf("GET should report secret_configured, body=%v", read)
	}

	// Plain update: still no secret.
	code, updated := doJSON(t, h, http.MethodPut, "/webhooks/config",
		`{"url":"http://127.0.0.1:1/hook2"}`)
	if code != http.StatusOK {
		t.Fatalf("update status = %d, want 200", code)
	}
	if got, ok := updated["secret"]; ok {
		t.Fatalf("a non-rotating update must not reveal the secret, got %v", got)
	}

	// Rotation: revealed again, and different from the original.
	code, rotated := doJSON(t, h, http.MethodPut, "/webhooks/config", `{"rotate_secret":true}`)
	if code != http.StatusOK {
		t.Fatalf("rotate status = %d, want 200", code)
	}
	newSecret, _ := rotated["secret"].(string)
	if newSecret == "" {
		t.Fatalf("rotation must reveal the new secret, body=%v", rotated)
	}
	if newSecret == secret {
		t.Fatal("rotated secret must differ from the original")
	}
}

// TestConfigHandler_GetConfigNotFoundBeforeCreation covers the empty state.
func TestConfigHandler_GetConfigNotFoundBeforeCreation(t *testing.T) {
	repo := newMockConfigRepo()
	svc := newConfigTestService(t, repo)
	h := newConfigTestRouter(t, svc, "tenant-1")

	code, body := doJSON(t, h, http.MethodGet, "/webhooks/config", "")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%v", code, body)
	}
}

// TestConfigHandler_RequiresTenantContext ensures the endpoints refuse to serve
// a request that carries no tenant.
func TestConfigHandler_RequiresTenantContext(t *testing.T) {
	repo := newMockConfigRepo()
	svc := newConfigTestService(t, repo)
	h := newConfigTestRouter(t, svc, "")

	if code, _ := doJSON(t, h, http.MethodGet, "/webhooks/config", ""); code == http.StatusOK {
		t.Fatal("GET /webhooks/config without a tenant must not succeed")
	}
	if code, _ := doJSON(t, h, http.MethodPut, "/webhooks/config", `{"enabled":true}`); code == http.StatusOK {
		t.Fatal("PUT /webhooks/config without a tenant must not succeed")
	}
}

// TestConfigHandler_IsolatedPerTenant proves one tenant can neither read nor
// overwrite another's config through the HTTP API.
func TestConfigHandler_IsolatedPerTenant(t *testing.T) {
	repo := newMockConfigRepo()
	svc := newConfigTestService(t, repo)

	tenantA := newConfigTestRouter(t, svc, "tenant-a")
	tenantB := newConfigTestRouter(t, svc, "tenant-b")

	if code, _ := doJSON(t, tenantA, http.MethodPut, "/webhooks/config",
		`{"enabled":true,"url":"http://127.0.0.1:1/a"}`); code != http.StatusOK {
		t.Fatalf("tenant-a create status = %d, want 200", code)
	}

	// tenant-b has no config of its own.
	if code, _ := doJSON(t, tenantB, http.MethodGet, "/webhooks/config", ""); code != http.StatusNotFound {
		t.Fatal("tenant-b must not see tenant-a's config")
	}

	// tenant-b's own create must not disturb tenant-a.
	if code, _ := doJSON(t, tenantB, http.MethodPut, "/webhooks/config",
		`{"enabled":true,"url":"http://127.0.0.1:1/b"}`); code != http.StatusOK {
		t.Fatal("tenant-b create should succeed independently")
	}
	code, a := doJSON(t, tenantA, http.MethodGet, "/webhooks/config", "")
	if code != http.StatusOK {
		t.Fatalf("tenant-a read status = %d, want 200", code)
	}
	if a["url"] != "http://127.0.0.1:1/a" {
		t.Fatalf("tenant-a url = %v, want its own", a["url"])
	}
}

// TestConfigHandler_RejectsInvalidBody and URL cover request validation.
func TestConfigHandler_RejectsInvalidBody(t *testing.T) {
	repo := newMockConfigRepo()
	svc := newConfigTestService(t, repo)
	h := newConfigTestRouter(t, svc, "tenant-1")

	if code, _ := doJSON(t, h, http.MethodPut, "/webhooks/config", `{"enabled":`); code != http.StatusBadRequest {
		t.Fatal("malformed JSON should be a 400")
	}
}

func TestConfigHandler_RejectsSSRFURL(t *testing.T) {
	repo := newMockConfigRepo()
	svc := NewConfigService(newMockRepo(), repo, nil).(*service)
	// allowPrivateNetworks deliberately left false so SSRF validation applies.
	h := newConfigTestRouter(t, svc, "tenant-1")

	code, body := doJSON(t, h, http.MethodPut, "/webhooks/config",
		`{"enabled":true,"url":"http://169.254.169.254/latest/meta-data/"}`)
	if code == http.StatusOK {
		t.Fatalf("a link-local metadata URL must be rejected, body=%v", body)
	}
}

// TestConfigHandler_TestDeliverySendsSignedPing covers the test endpoint
// end-to-end, including the signature header and tenant header.
func TestConfigHandler_TestDeliverySendsSignedPing(t *testing.T) {
	var gotSig, gotEvent, gotTenant string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Fluxa-Signature")
		gotEvent = r.Header.Get("X-Fluxa-Event")
		gotTenant = r.Header.Get("X-Fluxa-Tenant-ID")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	repo := newMockConfigRepo()
	svc := newConfigTestService(t, repo)
	h := newConfigTestRouter(t, svc, "tenant-1")

	if code, _ := doJSON(t, h, http.MethodPut, "/webhooks/config",
		`{"enabled":true,"url":"`+ts.URL+`"}`); code != http.StatusOK {
		t.Fatal("config create should succeed")
	}

	code, body := doJSON(t, h, http.MethodPost, "/webhooks/config/test", "")
	if code != http.StatusOK {
		t.Fatalf("test delivery status = %d, want 200, body=%v", code, body)
	}
	if gotSig == "" {
		t.Fatal("test delivery must be signed")
	}
	if gotTenant != "tenant-1" {
		t.Fatalf("X-Fluxa-Tenant-ID = %q, want tenant-1", gotTenant)
	}
	if gotEvent == "" {
		t.Fatal("test delivery should carry an event header")
	}
	if status, _ := body["status"].(string); status != string(domain.DeliverySuccess) {
		t.Fatalf("status = %v, want success, body=%v", status, body)
	}
}

// TestConfigHandler_TestDeliveryRequiresURL rejects a ping with nothing
// configured to ping.
func TestConfigHandler_TestDeliveryRequiresURL(t *testing.T) {
	repo := newMockConfigRepo()
	svc := newConfigTestService(t, repo)
	h := newConfigTestRouter(t, svc, "tenant-1")

	if code, _ := doJSON(t, h, http.MethodPost, "/webhooks/config/test", ""); code == http.StatusOK {
		t.Fatal("test delivery without a configured URL must fail")
	}
}

// TestConfigHandler_ListDeliveries covers the delivery-history endpoint and its
// tenant scoping.
func TestConfigHandler_ListDeliveries(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	repo := newMockConfigRepo()
	svc := newConfigTestService(t, repo)

	tenantA := newConfigTestRouter(t, svc, "tenant-a")
	tenantB := newConfigTestRouter(t, svc, "tenant-b")

	if code, _ := doJSON(t, tenantA, http.MethodPut, "/webhooks/config",
		`{"enabled":true,"url":"`+ts.URL+`"}`); code != http.StatusOK {
		t.Fatal("tenant-a create should succeed")
	}
	if code, _ := doJSON(t, tenantB, http.MethodPut, "/webhooks/config",
		`{"enabled":true,"url":"`+ts.URL+`"}`); code != http.StatusOK {
		t.Fatal("tenant-b create should succeed")
	}
	if code, _ := doJSON(t, tenantA, http.MethodPost, "/webhooks/config/test", ""); code != http.StatusOK {
		t.Fatal("tenant-a test delivery should succeed")
	}

	code, body := doJSON(t, tenantA, http.MethodGet, "/webhooks/config/deliveries", "")
	if code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", code)
	}
	list, _ := body["deliveries"].([]interface{})
	if len(list) != 1 {
		t.Fatalf("tenant-a deliveries = %d, want 1, body=%v", len(list), body)
	}
	first, _ := list[0].(map[string]interface{})
	if first["tenant_id"] != "tenant-a" {
		t.Fatalf("tenant_id = %v, want tenant-a", first["tenant_id"])
	}

	// tenant-b has none of its own.
	code, body = doJSON(t, tenantB, http.MethodGet, "/webhooks/config/deliveries", "")
	if code != http.StatusOK {
		t.Fatalf("tenant-b list status = %d, want 200", code)
	}
	list, _ = body["deliveries"].([]interface{})
	if len(list) != 0 {
		t.Fatalf("tenant-b must not see tenant-a's deliveries, got %d", len(list))
	}
}

// TestConfigHandler_UpdatePauseAndResume covers the pause/resume fields through
// the API, including clearing a scheduled resume by setting paused=false.
func TestConfigHandler_UpdatePauseAndResume(t *testing.T) {
	repo := newMockConfigRepo()
	svc := newConfigTestService(t, repo)
	h := newConfigTestRouter(t, svc, "tenant-1")

	if code, _ := doJSON(t, h, http.MethodPut, "/webhooks/config",
		`{"enabled":true,"url":"http://127.0.0.1:1/hook"}`); code != http.StatusOK {
		t.Fatal("create should succeed")
	}

	code, body := doJSON(t, h, http.MethodPut, "/webhooks/config",
		`{"paused":true,"resume_at":"2099-01-01T00:00:00Z"}`)
	if code != http.StatusOK {
		t.Fatalf("pause status = %d, want 200", code)
	}
	if paused, _ := body["paused"].(bool); !paused {
		t.Fatalf("paused = %v, want true, body=%v", body["paused"], body)
	}
	if body["resume_at"] == nil {
		t.Fatalf("resume_at should be echoed, body=%v", body)
	}

	// Explicitly unpausing clears the scheduled resume.
	code, body = doJSON(t, h, http.MethodPut, "/webhooks/config", `{"paused":false}`)
	if code != http.StatusOK {
		t.Fatalf("unpause status = %d, want 200", code)
	}
	if paused, _ := body["paused"].(bool); paused {
		t.Fatal("paused should be false")
	}
	if body["resume_at"] != nil {
		t.Fatalf("resume_at should be cleared by an explicit unpause, body=%v", body)
	}
}

// TestConfigHandler_UpdateEvents covers subscribing to and clearing events.
func TestConfigHandler_UpdateEvents(t *testing.T) {
	repo := newMockConfigRepo()
	svc := newConfigTestService(t, repo)
	h := newConfigTestRouter(t, svc, "tenant-1")

	code, body := doJSON(t, h, http.MethodPut, "/webhooks/config",
		`{"enabled":true,"url":"http://127.0.0.1:1/hook","events":["transfer.settled","reconciliation.drift"]}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	events, _ := body["events"].([]interface{})
	if len(events) != 2 {
		t.Fatalf("events = %v, want 2 entries", body["events"])
	}

	// An explicit empty list clears the subscription.
	code, body = doJSON(t, h, http.MethodPut, "/webhooks/config", `{"events":[]}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	events, _ = body["events"].([]interface{})
	if len(events) != 0 {
		t.Fatalf("events = %v, want cleared", body["events"])
	}
}

// TestConfigHandler_UnavailableWithoutConfigRepo documents the graceful
// degradation when the tenant-config tables are not present.
func TestConfigHandler_UnavailableWithoutConfigRepo(t *testing.T) {
	// A plain endpoint service has no config repository behind it.
	svc := NewService(newMockRepo(), nil)
	h := newConfigTestRouter(t, svc, "tenant-1")

	for _, tc := range []struct{ method, path string }{
		{http.MethodGet, "/webhooks/config"},
		{http.MethodPut, "/webhooks/config"},
		{http.MethodGet, "/webhooks/config/deliveries"},
		{http.MethodPost, "/webhooks/config/test"},
	} {
		if code, _ := doJSON(t, h, tc.method, tc.path, `{}`); code != http.StatusNotFound {
			t.Fatalf("%s %s = %d, want 404 when config is unavailable", tc.method, tc.path, code)
		}
	}
}
