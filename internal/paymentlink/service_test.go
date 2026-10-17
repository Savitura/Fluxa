package paymentlink

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/shopspring/decimal"
)

type linkRepoFake struct {
	link     *Link
	claimed  *Link
	released bool

	staleCutoffs []time.Time
	staleResults []int
}

func (f *linkRepoFake) Create(_ context.Context, link *Link) error { f.link = link; return nil }
func (f *linkRepoFake) GetByIdempotencyKey(context.Context, string) (*Link, error) {
	return nil, ErrNotFound
}
func (f *linkRepoFake) List(context.Context) ([]*Link, error)            { return []*Link{f.link}, nil }
func (f *linkRepoFake) Get(context.Context, string) (*Link, error)       { return f.link, nil }
func (f *linkRepoFake) GetPublic(context.Context, string) (*Link, error) { return f.link, nil }
func (f *linkRepoFake) Claim(context.Context, string) (*Link, error)     { return f.claimed, nil }
func (f *linkRepoFake) Release(context.Context, string) error            { f.released = true; return nil }
func (f *linkRepoFake) Cancel(context.Context, string) error             { return nil }
func (f *linkRepoFake) ReleaseStale(_ context.Context, claimedBefore time.Time, _ int) (int, error) {
	f.staleCutoffs = append(f.staleCutoffs, claimedBefore)
	if len(f.staleResults) == 0 {
		return 0, nil
	}
	n := f.staleResults[0]
	f.staleResults = f.staleResults[1:]
	return n, nil
}

type depositorFake struct {
	ctx context.Context
	req fiat.DepositRequest
	err error
}

func (f *depositorFake) InitiateDeposit(ctx context.Context, req fiat.DepositRequest) (*fiat.DepositResponse, error) {
	f.ctx, f.req = ctx, req
	return &fiat.DepositResponse{PaymentLink: "https://pay.example/checkout", Reference: req.Reference}, f.err
}

func TestCreateRequiresTenantModeAndBoundedExpiry(t *testing.T) {
	repo := &linkRepoFake{}
	svc := NewService(repo, &depositorFake{})
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	params := CreateParams{WalletID: "wallet", Amount: decimal.NewFromInt(10), Currency: "ngn", ExpiresAt: now.Add(time.Hour), IdempotencyKey: "550e8400-e29b-41d4-a716-446655440000"}
	if _, err := svc.Create(context.Background(), params); !errors.Is(err, ErrTenantMissing) {
		t.Fatalf("expected tenant context error, got %v", err)
	}
	ctx := tenant.WithID(context.Background(), "tenant-1")
	if _, err := svc.Create(ctx, params); err == nil {
		t.Fatal("expected missing environment mode error")
	}
	ctx = tenant.WithMode(ctx, domain.ModeTest)
	params.ExpiresAt = now.Add(91 * 24 * time.Hour)
	if _, err := svc.Create(ctx, params); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected expiry validation error, got %v", err)
	}
	params.ExpiresAt = now.Add(time.Hour)
	link, err := svc.Create(ctx, params)
	if err != nil {
		t.Fatalf("create link: %v", err)
	}
	if link.Currency != "NGN" || link.Mode != domain.ModeTest || link.TenantID != "tenant-1" {
		t.Fatalf("unexpected link scope or currency: %+v", link)
	}
}

func TestCheckoutUsesStoredConstraintsAndRestoresScope(t *testing.T) {
	repo := &linkRepoFake{claimed: &Link{
		ID: "link-1", TenantID: "tenant-1", WalletID: "wallet-1", Mode: domain.ModeTest,
		Amount: decimal.NewFromInt(2500), Currency: "NGN",
	}}
	depositor := &depositorFake{}
	svc := NewService(repo, depositor)
	resp, err := svc.Checkout(context.Background(), "token", "buyer@example.com", "Buyer")
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if resp.PaymentLink == "" || depositor.req.FiatAmount.String() != "2500" || depositor.req.FiatCurrency != "NGN" {
		t.Fatalf("checkout did not use stored amount/currency: %+v %+v", resp, depositor.req)
	}
	if tenant.IDFromContext(depositor.ctx) != "tenant-1" || tenant.ModeOrDefault(depositor.ctx, domain.ModeLive) != domain.ModeTest {
		t.Fatal("checkout did not restore the payment link tenant/environment")
	}
}

func TestFailedCheckoutReleasesLink(t *testing.T) {
	repo := &linkRepoFake{claimed: &Link{ID: "link-1", TenantID: "tenant-1", WalletID: "wallet-1", Mode: domain.ModeLive}}
	depositor := &depositorFake{err: errors.New("provider unavailable")}
	svc := NewService(repo, depositor)
	if _, err := svc.Checkout(context.Background(), "token", "buyer@example.com", "Buyer"); err == nil {
		t.Fatal("expected provider error")
	}
	if !repo.released {
		t.Fatal("expected failed checkout to attempt link release")
	}
}

// #390: links stuck in processing after an abandoned checkout are released
// once they have been claimed for longer than the checkout timeout.
func TestReleaseAbandonedUsesTimeoutCutoffAndDrainsBatches(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	repo := &linkRepoFake{staleResults: []int{releaseBatchSize, 2}}
	svc := NewService(repo, nil)
	svc.now = func() time.Time { return now }

	released, err := svc.ReleaseAbandoned(context.Background(), 15*time.Minute)
	if err != nil {
		t.Fatalf("ReleaseAbandoned: %v", err)
	}
	if released != releaseBatchSize+2 {
		t.Fatalf("released = %d, want %d", released, releaseBatchSize+2)
	}
	if len(repo.staleCutoffs) != 2 {
		t.Fatalf("ReleaseStale called %d times, want 2", len(repo.staleCutoffs))
	}
	for _, cutoff := range repo.staleCutoffs {
		if !cutoff.Equal(now.Add(-15 * time.Minute)) {
			t.Fatalf("cutoff = %v, want %v", cutoff, now.Add(-15*time.Minute))
		}
	}
}

func TestReleaseAbandonedDefaultsTimeout(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	repo := &linkRepoFake{}
	svc := NewService(repo, nil)
	svc.now = func() time.Time { return now }

	if _, err := svc.ReleaseAbandoned(context.Background(), 0); err != nil {
		t.Fatalf("ReleaseAbandoned: %v", err)
	}
	if !repo.staleCutoffs[0].Equal(now.Add(-DefaultCheckoutTimeout)) {
		t.Fatalf("cutoff = %v, want default timeout cutoff", repo.staleCutoffs[0])
	}
}
