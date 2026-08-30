package anchor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

const testStellarToml = `
SIGNING_KEY="GDMQVRE6HRTFRKMSCUWTKTVFHXKHMR33XVIAWZLLAA6HYPRWXWH5PZFF"
NETWORK_PASSPHRASE="Test SDF Network ; September 2015"
WEB_AUTH_ENDPOINT="https://testanchor.example.com/auth"
TRANSFER_SERVER="https://testanchor.example.com/sep6"
TRANSFER_SERVER_SEP0024="https://testanchor.example.com/sep24"
KYC_SERVER="https://testanchor.example.com/kyc"

[[CURRENCIES]]
code="USDC"
issuer="GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
status="live"
`

// fakeRepo is an in-memory anchor.Repository used to test the registry
// without a database.
type fakeRepo struct {
	mu      sync.Mutex
	anchors []*domain.Anchor
}

func (f *fakeRepo) CreateAnchor(ctx context.Context, a *domain.Anchor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.anchors = append(f.anchors, a)
	return nil
}

func (f *fakeRepo) ListAnchors(ctx context.Context) ([]*domain.Anchor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*domain.Anchor, len(f.anchors))
	copy(out, f.anchors)
	return out, nil
}

func (f *fakeRepo) GetAnchorByID(ctx context.Context, id string) (*domain.Anchor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.anchors {
		if a.ID == id {
			return a, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) GetAnchorByHomeDomain(ctx context.Context, homeDomain string) (*domain.Anchor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, a := range f.anchors {
		if a.HomeDomain == homeDomain {
			return a, nil
		}
	}
	return nil, nil
}

func (f *fakeRepo) CreateTransaction(ctx context.Context, t *domain.AnchorTransaction) error {
	return nil
}
func (f *fakeRepo) GetTransactionByID(ctx context.Context, id string, tenantID *string) (*domain.AnchorTransaction, error) {
	return nil, nil
}
func (f *fakeRepo) UpdateTransactionStatus(ctx context.Context, id, status string, completedAt *time.Time, tenantID *string) error {
	return nil
}

func TestRegistry_Register_ParsesStellarTomlFromHomeDomainAlone(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/stellar.toml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(testStellarToml))
	})
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	homeDomain := srv.Listener.Addr().String() // acts as the anchor's home domain for this test

	repo := &fakeRepo{}
	registry := NewRegistry(repo, srv.Client())

	a, err := registry.Register(context.Background(), homeDomain)
	if err != nil {
		t.Fatalf("Register returned unexpected error: %v", err)
	}
	if a.HomeDomain != homeDomain {
		t.Fatalf("expected home domain %q, got %q", homeDomain, a.HomeDomain)
	}
	if a.TransferServer != "https://testanchor.example.com/sep6" {
		t.Fatalf("unexpected transfer server: %q", a.TransferServer)
	}
	if len(a.SupportedAssets) != 1 || a.SupportedAssets[0] != "USDC" {
		t.Fatalf("expected supported assets [USDC], got %v", a.SupportedAssets)
	}

	resolved, err := registry.ResolveForAsset("USDC")
	if err != nil {
		t.Fatalf("ResolveForAsset returned unexpected error: %v", err)
	}
	if resolved.ID != a.ID {
		t.Fatalf("expected resolved anchor to be the one just registered")
	}

	// Registration should also have persisted to the repository.
	if len(repo.anchors) != 1 {
		t.Fatalf("expected 1 persisted anchor, got %d", len(repo.anchors))
	}
}

func TestRegistry_Load_PopulatesFromRepository(t *testing.T) {
	repo := &fakeRepo{anchors: []*domain.Anchor{
		{ID: "a1", HomeDomain: "anchor-a.example.com", SupportedAssets: []string{"USDC"}},
		{ID: "a2", HomeDomain: "anchor-b.example.com", SupportedAssets: []string{"NGNT"}},
	}}
	registry := NewRegistry(repo, nil)

	if err := registry.Load(context.Background()); err != nil {
		t.Fatalf("Load returned unexpected error: %v", err)
	}

	if len(registry.List()) != 2 {
		t.Fatalf("expected 2 anchors loaded, got %d", len(registry.List()))
	}
	if _, err := registry.ResolveForAsset("NGNT"); err != nil {
		t.Fatalf("expected NGNT to resolve after Load: %v", err)
	}
	if _, err := registry.ResolveForAsset("EURC"); err == nil {
		t.Fatalf("expected resolving an unregistered asset to fail")
	}
}
