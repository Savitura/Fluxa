package webhook

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
)

func newTestService(t *testing.T) *service {
	t.Helper()
	repo := newFakeRepo()
	svc, ok := NewService(repo, nil, nil, 120, false).(*service)
	if !ok {
		t.Fatal("NewService did not return *service")
	}
	return svc
}

func TestRegister_RejectsDisallowedSchemes(t *testing.T) {
	svc := newTestService(t)

	schemes := []string{
		"file:///etc/passwd",
		"gopher://127.0.0.1:6379/_INFO",
		"ftp://example.com/hook",
		"javascript:alert(1)",
	}
	for _, raw := range schemes {
		if _, _, err := svc.RegisterEndpoint(context.Background(), raw, nil); !errors.Is(err, ErrUnsafeWebhookURL) {
			t.Errorf("RegisterEndpoint(%q) error = %v, want ErrUnsafeWebhookURL", raw, err)
		}
	}
}

func TestRegister_RejectsLoopbackAndPrivateDestinations(t *testing.T) {
	svc := newTestService(t)

	urls := []string{
		"http://127.0.0.1:8080/hook",
		"http://localhost/hook",
		"http://[::1]/hook",
		"http://169.254.169.254/latest/meta-data/", // cloud metadata
		"http://10.0.0.5/hook",                     // RFC1918
		"http://172.16.0.5/hook",                   // RFC1918
		"http://192.168.1.5/hook",                  // RFC1918
		"http://224.0.0.1/hook",                    // multicast
		"http://0.0.0.0/hook",                      // unspecified
	}
	for _, raw := range urls {
		if _, _, err := svc.RegisterEndpoint(context.Background(), raw, nil); !errors.Is(err, ErrUnsafeWebhookURL) {
			t.Errorf("RegisterEndpoint(%q) error = %v, want ErrUnsafeWebhookURL", raw, err)
		}
	}
}

func TestRegister_AllowsPublicHTTPSDestination(t *testing.T) {
	svc := newTestService(t)

	ep, _, err := svc.RegisterEndpoint(context.Background(), "https://example.com/hook", nil)
	if err != nil {
		t.Fatalf("RegisterEndpoint() error = %v, want nil", err)
	}
	if ep.URL != "https://example.com/hook" {
		t.Fatalf("URL = %q", ep.URL)
	}
}

// TestDeliver_RevalidatesDestinationAtSendTime ensures that even if a
// malicious or repointed URL somehow made it into storage, delivery itself
// still refuses to dial a private/loopback destination.
func TestDeliver_RevalidatesDestinationAtSendTime(t *testing.T) {
	svc := newTestService(t)

	ep := &domain.WebhookEndpoint{
		ID:     "ep-ssrf",
		URL:    "http://127.0.0.1:9/hook", // never actually dialed; blocked before that
		Secret: "secret",
		Active: true,
	}
	svc.repo.(*fakeRepo).endpoints[ep.ID] = ep

	delivery := &domain.WebhookDelivery{
		ID:         "dlv-ssrf",
		EndpointID: ep.ID,
		Payload:    []byte(`{}`),
	}
	svc.repo.(*fakeRepo).deliveries[delivery.ID] = delivery

	err := svc.Deliver(context.Background(), delivery.ID)
	if !errors.Is(err, ErrUnsafeWebhookURL) {
		t.Fatalf("Deliver() error = %v, want ErrUnsafeWebhookURL", err)
	}

	stored := svc.repo.(*fakeRepo).deliveries[delivery.ID]
	if stored.Status != domain.DeliveryFailed {
		t.Fatalf("delivery status = %s, want failed", stored.Status)
	}
}

// TestSafeClient_DoesNotFollowRedirectToInternalHost proves that even when a
// registered public URL later issues an HTTP redirect to an internal
// address, the client does not automatically follow it.
func TestSafeClient_DoesNotFollowRedirectToInternalHost(t *testing.T) {
	// Internal target that must never receive a request.
	var internalHit bool
	internal := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		internalHit = true
		w.WriteHeader(http.StatusOK)
	}))
	defer internal.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, internal.URL, http.StatusFound)
	}))
	defer redirector.Close()

	svc := newTestService(t)
	svc.allowPrivateNetworks = true // both test servers are on loopback

	resp, err := svc.client.Get(redirector.URL)
	if err != nil {
		t.Fatalf("client.Get() error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want %d (redirect must not be followed)", resp.StatusCode, http.StatusFound)
	}
	if internalHit {
		t.Fatal("redirect target was dialed automatically, expected it to be left to the caller")
	}
}

func TestResolveAndValidateHost_BlocksLinkLocalAndMulticast(t *testing.T) {
	svc := newTestService(t)

	blocked := []net.IP{
		net.ParseIP("169.254.169.254"),
		net.ParseIP("224.0.0.1"),
		net.ParseIP("ff02::1"),
		net.ParseIP("fc00::1"), // IPv6 ULA (private)
	}
	for _, ip := range blocked {
		if !svc.isBlockedIP(ip) {
			t.Errorf("isBlockedIP(%s) = false, want true", ip)
		}
	}
}

func TestValidateWebhookURL_RejectsMissingHost(t *testing.T) {
	svc := newTestService(t)
	err := svc.validateWebhookURL(context.Background(), "https:///no-host")
	if err == nil || !strings.Contains(err.Error(), "missing host") {
		t.Fatalf("validateWebhookURL() error = %v, want missing host error", err)
	}
}
