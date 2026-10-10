package wallet_balance_alert

import (
	"context"
	"errors"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
)

type mockRepo struct {
	alerts    map[string]*domain.WalletBalanceAlert
	events    map[string][]*domain.WalletBalanceAlertEvent
	createErr error
	getErr    error
}

func (m *mockRepo) Create(ctx context.Context, alert *domain.WalletBalanceAlert) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.alerts[alert.ID] = alert
	return nil
}

func (m *mockRepo) Get(ctx context.Context, id string) (*domain.WalletBalanceAlert, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	return m.alerts[id], nil
}

func (m *mockRepo) List(ctx context.Context, walletID string) ([]*domain.WalletBalanceAlert, error) {
	var result []*domain.WalletBalanceAlert
	for _, a := range m.alerts {
		if walletID == "" || a.WalletID == walletID {
			result = append(result, a)
		}
	}
	return result, nil
}

func (m *mockRepo) Update(ctx context.Context, alert *domain.WalletBalanceAlert) error {
	m.alerts[alert.ID] = alert
	return nil
}

func (m *mockRepo) Delete(ctx context.Context, id string) error {
	delete(m.alerts, id)
	return nil
}

func (m *mockRepo) CreateEvent(ctx context.Context, event *domain.WalletBalanceAlertEvent) error {
	m.events[event.AlertID] = append(m.events[event.AlertID], event)
	return nil
}

func (m *mockRepo) ListEvents(ctx context.Context, alertID string, limit int) ([]*domain.WalletBalanceAlertEvent, error) {
	return m.events[alertID], nil
}

func (m *mockRepo) GetActiveAlertsForWallet(ctx context.Context, walletID string) ([]*domain.WalletBalanceAlert, error) {
	var result []*domain.WalletBalanceAlert
	for _, a := range m.alerts {
		if a.WalletID == walletID && a.Status == domain.WalletBalanceAlertActive {
			result = append(result, a)
		}
	}
	return result, nil
}

type mockAudit struct{}

func (m *mockAudit) Record(ctx context.Context, event *domain.AuditEvent) error {
	return nil
}

func TestService_Create(t *testing.T) {
	repo := &mockRepo{alerts: make(map[string]*domain.WalletBalanceAlert), events: make(map[string][]*domain.WalletBalanceAlertEvent)}
	audit := &mockAudit{}
	svc := NewService(repo, audit)

	t.Run("valid alert creation", func(t *testing.T) {
		// Set tenant context
		ctx := context.Background()
		ctx = tenant.WithID(ctx, uuid.NewString())
		ctx = tenant.WithMode(ctx, domain.ModeLive)

		alert, err := svc.Create(ctx, "wallet-123", "USDC", "issuer-123", "100.00")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if alert == nil {
			t.Fatal("expected alert to be created")
		}
		if alert.AssetCode != "USDC" {
			t.Errorf("expected asset code USDC, got %s", alert.AssetCode)
		}
		if alert.Threshold != "100.00" {
			t.Errorf("expected threshold 100.00, got %s", alert.Threshold)
		}
		if alert.Status != domain.WalletBalanceAlertActive {
			t.Errorf("expected status active, got %s", alert.Status)
		}
	})

	t.Run("invalid asset code", func(t *testing.T) {
		ctx := context.Background()
		ctx = tenant.WithID(ctx, uuid.NewString())
		_, err := svc.Create(ctx, "wallet-123", "", "issuer-123", "100.00")
		if !errors.Is(err, ErrInvalidAsset) {
			t.Errorf("expected ErrInvalidAsset, got %v", err)
		}
	})

	t.Run("invalid threshold", func(t *testing.T) {
		ctx := context.Background()
		ctx = tenant.WithID(ctx, uuid.NewString())
		_, err := svc.Create(ctx, "wallet-123", "USDC", "issuer-123", "-10.00")
		if !errors.Is(err, ErrInvalidThreshold) {
			t.Errorf("expected ErrInvalidThreshold, got %v", err)
		}
	})

	t.Run("duplicate alert", func(t *testing.T) {
		ctx := context.Background()
		ctx = tenant.WithID(ctx, uuid.NewString())
		repo.createErr = domain.ErrDuplicateAlert
		_, err := svc.Create(ctx, "wallet-123", "USDC", "issuer-123", "100.00")
		if !errors.Is(err, domain.ErrDuplicateAlert) {
			t.Errorf("expected ErrDuplicateAlert, got %v", err)
		}
		repo.createErr = nil
	})
}

func TestService_Update(t *testing.T) {
	repo := &mockRepo{alerts: make(map[string]*domain.WalletBalanceAlert), events: make(map[string][]*domain.WalletBalanceAlertEvent)}
	audit := &mockAudit{}
	svc := NewService(repo, audit)

	t.Run("update threshold", func(t *testing.T) {
		ctx := context.Background()
		ctx = tenant.WithID(ctx, uuid.NewString())
		ctx = tenant.WithMode(ctx, domain.ModeLive)

		// Create an alert first
		alert, _ := svc.Create(ctx, "wallet-123", "USDC", "issuer-123", "100.00")

		// Update threshold
		updated, err := svc.Update(ctx, alert.ID, "200.00", "")
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if updated.Threshold != "200.00" {
			t.Errorf("expected threshold 200.00, got %s", updated.Threshold)
		}
	})

	t.Run("update status", func(t *testing.T) {
		ctx := context.Background()
		ctx = tenant.WithID(ctx, uuid.NewString())
		ctx = tenant.WithMode(ctx, domain.ModeLive)

		alert, _ := svc.Create(ctx, "wallet-456", "XLM", "", "50.00")

		updated, err := svc.Update(ctx, alert.ID, "", domain.WalletBalanceAlertInactive)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if updated.Status != domain.WalletBalanceAlertInactive {
			t.Errorf("expected status inactive, got %s", updated.Status)
		}
	})

	t.Run("invalid threshold", func(t *testing.T) {
		ctx := context.Background()
		ctx = tenant.WithID(ctx, uuid.NewString())
		ctx = tenant.WithMode(ctx, domain.ModeLive)

		alert, _ := svc.Create(ctx, "wallet-789", "EURC", "issuer-456", "100.00")

		_, err := svc.Update(ctx, alert.ID, "-5.00", "")
		if !errors.Is(err, ErrInvalidThreshold) {
			t.Errorf("expected ErrInvalidThreshold, got %v", err)
		}
	})
}
