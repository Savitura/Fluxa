package wallet_test

import (
	"context"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/wallet"
)

type mockRepo struct {
	count int
}

func (m *mockRepo) Create(ctx context.Context, w *domain.Wallet) error {
	m.count++
	return nil
}
func (m *mockRepo) GetByID(ctx context.Context, id string) (*domain.Wallet, error) {
	return &domain.Wallet{ID: id}, nil
}
func (m *mockRepo) GetByPublicKey(ctx context.Context, pubKey string) (*domain.Wallet, error) {
	return nil, nil
}
func (m *mockRepo) List(ctx context.Context, limit, offset int) ([]*domain.Wallet, error) {
	return nil, nil
}
func (m *mockRepo) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	return m.count, nil
}

type mockTenantRepo struct {
	tenant *domain.Tenant
}

func (m *mockTenantRepo) GetByID(ctx context.Context, id string) (*domain.Tenant, error) {
	return m.tenant, nil
}

type mockStellar struct{}

func (m *mockStellar) LoadAccount(pubKey string) (interface{}, error) { return nil, nil }

func TestIndividualWalletLimit(t *testing.T) {
	repo := &mockRepo{count: 5}
	tRepo := &mockTenantRepo{
		tenant: &domain.Tenant{
			ID:          "t-1",
			AccountType: domain.AccountTypeIndividual,
		},
	}
	masterKey := make([]byte, 32)
	svc := wallet.NewService(repo, nil, masterKey, tRepo)

	ctx := tenant.WithID(context.Background(), "t-1")
	_, err := svc.CreateWallet(ctx)
	if err != domain.ErrWalletLimitReached {
		t.Fatalf("expected ErrWalletLimitReached, got %v", err)
	}
}

func TestOrgWalletLimitAllowed(t *testing.T) {
	repo := &mockRepo{count: 5}
	tRepo := &mockTenantRepo{
		tenant: &domain.Tenant{
			ID:          "t-2",
			AccountType: domain.AccountTypeOrganization,
		},
	}
	masterKey := make([]byte, 32)
	svc := wallet.NewService(repo, nil, masterKey, tRepo)

	ctx := tenant.WithID(context.Background(), "t-2")
	w, err := svc.CreateWallet(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w == nil {
		t.Fatal("expected wallet struct, got nil")
	}
}

func TestOrgWalletLimitExceeded(t *testing.T) {
	repo := &mockRepo{count: 50}
	tRepo := &mockTenantRepo{
		tenant: &domain.Tenant{
			ID:          "t-3",
			AccountType: domain.AccountTypeOrganization,
		},
	}
	masterKey := make([]byte, 32)
	svc := wallet.NewService(repo, nil, masterKey, tRepo)

	ctx := tenant.WithID(context.Background(), "t-3")
	_, err := svc.CreateWallet(ctx)
	if err != domain.ErrWalletLimitReached {
		t.Fatalf("expected ErrWalletLimitReached at 50 for org, got %v", err)
	}
}
