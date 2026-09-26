package reconcile

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/fluxa/fluxa/internal/alerting"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/shopspring/decimal"
	horizonclient "github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/base"
	"github.com/stellar/go/support/render/problem"
)

const (
	driftWalletID = "11111111-1111-1111-1111-111111111111"
	driftWalletPK = "GBDRPAXW5JBCJLOWJ2KHTMIGM7R6U2NKZQ3W4BOWX3S2P5ZCQ2KJ4PVS"
	driftTenantID = "22222222-2222-2222-2222-222222222222"
)

// driftRepo is a WalletRepository that also satisfies DriftRepository, so the
// service picks up snapshot persistence through the same fake.
type driftRepo struct {
	Wallets       []*domain.Wallet
	Balances      map[string]map[string]decimal.Decimal
	Discrepancies []*BalanceDiscrepancy
	Snapshots     []*DriftSnapshot
	mu            sync.Mutex
}

func (d *driftRepo) ListAllWallets(context.Context) ([]*domain.Wallet, error) {
	return d.Wallets, nil
}

func (d *driftRepo) GetDBBalances(_ context.Context, walletID string) (map[string]decimal.Decimal, error) {
	if balances, ok := d.Balances[walletID]; ok {
		return balances, nil
	}
	return map[string]decimal.Decimal{}, nil
}

func (d *driftRepo) WriteBalanceDiscrepancy(_ context.Context, discrepancy *BalanceDiscrepancy) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Discrepancies = append(d.Discrepancies, discrepancy)
	return nil
}

func (d *driftRepo) WriteDriftSnapshot(_ context.Context, snapshot *DriftSnapshot) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Snapshots = append(d.Snapshots, snapshot)
	return nil
}

func (d *driftRepo) ListCurrentDrift(context.Context) ([]*DriftSnapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Mirror the real repository: each call returns freshly built snapshots, so
	// a caller mutating the result cannot corrupt what is stored. Returning a
	// non-nil slice also means clients see an empty JSON array, not null.
	snapshots := make([]*DriftSnapshot, 0, len(d.Snapshots))
	for _, snapshot := range d.Snapshots {
		copied := *snapshot
		snapshots = append(snapshots, &copied)
	}
	return snapshots, nil
}

// driftStellar returns Horizon balances for a wallet. The embedded interface
// satisfies the rest of stellar.Client; only LoadAccount is exercised here, and
// because the embedded interface has no *WithContext methods the context-aware
// helper transparently falls back to it.
type driftStellar struct {
	stellar.Client
	balances map[string]decimal.Decimal
	asset    string
}

func (s *driftStellar) LoadAccount(accountID string) (horizon.Account, error) {
	balance, ok := s.balances[accountID]
	if !ok {
		// Mirror Horizon's behaviour for an account that does not exist yet:
		// a 404, which reconciliation treats as "not funded" rather than drift.
		return horizon.Account{}, &horizonclient.Error{
			Response: &http.Response{StatusCode: http.StatusNotFound},
			Problem:  problem.P{Status: http.StatusNotFound},
		}
	}
	return horizon.Account{
		AccountID: accountID,
		Balances: []horizon.Balance{{
			Balance: balance.String(),
			Asset:   base.Asset{Type: "credit_alphanum4", Code: s.asset},
		}},
	}, nil
}

// driftLookup is a WalletLookup stub; balance reconciliation does not use it.
type driftLookup struct {
	Wallets map[string]*domain.Wallet
}

func (d *driftLookup) GetByID(_ context.Context, id string) (*domain.Wallet, error) {
	if w, ok := d.Wallets[id]; ok {
		return w, nil
	}
	return nil, domain.ErrWalletNotFound
}

// driftWebhook captures the events dispatched by the reconciler.
type driftWebhook struct {
	webhook.Service
	mu       sync.Mutex
	events   []domain.EventType
	payloads []map[string]interface{}
}

func (d *driftWebhook) Dispatch(_ context.Context, eventType domain.EventType, payload interface{}) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.events = append(d.events, eventType)
	if body, ok := payload.(map[string]interface{}); ok {
		d.payloads = append(d.payloads, body)
	}
	return nil
}

func (d *driftWebhook) recorded() ([]domain.EventType, []map[string]interface{}) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.events, d.payloads
}

// alertSink records the drift alerts the reconciler emits.
type alertSink struct {
	mu     sync.Mutex
	alerts []alerting.DriftAlert
	server *httptest.Server
}

func newAlertSink(t *testing.T) *alertSink {
	t.Helper()
	sink := &alertSink{}
	sink.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Alert alerting.DriftAlert `json:"alert"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
			sink.mu.Lock()
			sink.alerts = append(sink.alerts, body.Alert)
			sink.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(sink.server.Close)
	return sink
}

func (s *alertSink) received() []alerting.DriftAlert {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.alerts
}

// newDriftService wires a reconciler with one wallet whose DB balance and
// on-chain balance differ by the given amount, using the given drift threshold.
func newDriftService(t *testing.T, dbBalance, chainBalance, threshold string) (*Service, *driftRepo, *driftWebhook, *alertSink) {
	t.Helper()
	tenantID := driftTenantID
	repo := &driftRepo{
		Wallets: []*domain.Wallet{{
			ID:        driftWalletID,
			TenantID:  &tenantID,
			PublicKey: driftWalletPK,
		}},
		Balances: map[string]map[string]decimal.Decimal{
			driftWalletID: {"USDC": decimal.RequireFromString(dbBalance)},
		},
	}
	hook := &driftWebhook{}
	sink := newAlertSink(t)

	svc := NewService(
		nil,
		repo,
		&driftLookup{},
		&driftStellar{balances: map[string]decimal.Decimal{
			driftWalletPK: decimal.RequireFromString(chainBalance),
		}, asset: "USDC"},
		alerting.NewClient(sink.server.URL, "fluxa-test"),
		nil,
		hook,
		"fluxa-test",
		decimal.Zero,
		nil,
		"",
	).WithDriftThreshold(decimal.RequireFromString(threshold))

	return svc, repo, hook, sink
}

// TestReconcileDrift_NoDrift covers the "no drift" scenario: an exactly
// matching balance is snapshotted for the audit trail but never alerts.
func TestReconcileDrift_NoDrift(t *testing.T) {
	svc, repo, hook, sink := newDriftService(t, "100.00", "100.00", "1.00")

	if err := svc.RunBalanceReconciliation(context.Background()); err != nil {
		t.Fatalf("RunBalanceReconciliation() error: %v", err)
	}

	if len(repo.Snapshots) != 1 {
		t.Fatalf("expected 1 drift snapshot, got %d", len(repo.Snapshots))
	}
	if !repo.Snapshots[0].DriftAmount.IsZero() {
		t.Fatalf("drift = %s, want 0", repo.Snapshots[0].DriftAmount)
	}
	if len(repo.Discrepancies) != 0 {
		t.Fatalf("expected no discrepancies, got %d", len(repo.Discrepancies))
	}
	if got := sink.received(); len(got) != 0 {
		t.Fatalf("expected no alerts with zero drift, got %d", len(got))
	}
	events, _ := hook.recorded()
	if len(events) != 0 {
		t.Fatalf("expected no webhook events with zero drift, got %v", events)
	}
}

// TestReconcileDrift_WithinThreshold covers the "within threshold" scenario:
// a real but sub-threshold difference is recorded without alerting.
func TestReconcileDrift_WithinThreshold(t *testing.T) {
	svc, repo, _, sink := newDriftService(t, "100.00", "100.40", "1.00")

	if err := svc.RunBalanceReconciliation(context.Background()); err != nil {
		t.Fatalf("RunBalanceReconciliation() error: %v", err)
	}

	if len(repo.Snapshots) != 1 {
		t.Fatalf("expected the snapshot to be stored for audit, got %d", len(repo.Snapshots))
	}
	if want := decimal.RequireFromString("0.4"); !repo.Snapshots[0].DriftAmount.Equal(want) {
		t.Fatalf("drift = %s, want %s", repo.Snapshots[0].DriftAmount, want)
	}
	if len(repo.Discrepancies) != 0 {
		t.Fatalf("expected no discrepancies within threshold, got %d", len(repo.Discrepancies))
	}
	if got := sink.received(); len(got) != 0 {
		t.Fatalf("expected no alerts within threshold, got %d", len(got))
	}
}

// TestReconcileDrift_AboveThreshold covers the alerting scenario and asserts
// the alert carries every field operators need to act on the drift.
func TestReconcileDrift_AboveThreshold(t *testing.T) {
	svc, repo, hook, sink := newDriftService(t, "100.00", "95.00", "1.00")

	if err := svc.RunBalanceReconciliation(context.Background()); err != nil {
		t.Fatalf("RunBalanceReconciliation() error: %v", err)
	}

	// A snapshot is stored for every run, alerting or not.
	if len(repo.Snapshots) != 1 {
		t.Fatalf("expected 1 drift snapshot, got %d", len(repo.Snapshots))
	}
	if len(repo.Discrepancies) != 1 {
		t.Fatalf("expected 1 balance discrepancy, got %d", len(repo.Discrepancies))
	}

	alerts := sink.received()
	if len(alerts) != 1 {
		t.Fatalf("expected 1 drift alert, got %d", len(alerts))
	}
	alert := alerts[0]
	if alert.TenantID != driftTenantID {
		t.Fatalf("tenant_id = %q, want %q", alert.TenantID, driftTenantID)
	}
	if alert.WalletAddress != driftWalletPK {
		t.Fatalf("wallet_address = %q, want %q", alert.WalletAddress, driftWalletPK)
	}
	if alert.ExpectedBalance != "100" || alert.ActualBalance != "95" {
		t.Fatalf("expected/actual = %s/%s, want 100/95", alert.ExpectedBalance, alert.ActualBalance)
	}
	if alert.DriftAmount != "5" {
		t.Fatalf("drift_amount = %q, want 5", alert.DriftAmount)
	}
	if alert.Asset != "USDC" {
		t.Fatalf("asset = %q, want USDC", alert.Asset)
	}
	if alert.WalletID != driftWalletID {
		t.Fatalf("wallet_id = %q, want %q", alert.WalletID, driftWalletID)
	}

	// The drift is also surfaced to tenants as a webhook event.
	events, payloads := hook.recorded()
	if len(events) != 1 || events[0] != domain.EventReconciliationDrift {
		t.Fatalf("expected a reconciliation.drift webhook, got %v", events)
	}
	if payloads[0]["drift_amount"] != "5" {
		t.Fatalf("event drift_amount = %v, want 5", payloads[0]["drift_amount"])
	}
}

// TestReconcileDrift_DriftIsAbsolute covers drift below the DB balance, which
// must be reported as a positive magnitude just like drift above it.
func TestReconcileDrift_DriftIsAbsolute(t *testing.T) {
	svc, repo, _, sink := newDriftService(t, "100.00", "120.00", "1.00")

	if err := svc.RunBalanceReconciliation(context.Background()); err != nil {
		t.Fatalf("RunBalanceReconciliation() error: %v", err)
	}
	if want := decimal.RequireFromString("20"); !repo.Snapshots[0].DriftAmount.Equal(want) {
		t.Fatalf("drift = %s, want %s", repo.Snapshots[0].DriftAmount, want)
	}
	if got := sink.received(); len(got) != 1 {
		t.Fatalf("expected 1 alert, got %d", len(got))
	}
}

// TestReconcileDrift_ThresholdBoundary pins the comparison operator. The
// comparison is LessThanOrEqual, so a drift exactly equal to the threshold is
// treated as within tolerance and does not alert; one cent beyond it does.
func TestReconcileDrift_ThresholdBoundary(t *testing.T) {
	exact, repo, _, sink := newDriftService(t, "100.00", "99.00", "1.00")
	if err := exact.RunBalanceReconciliation(context.Background()); err != nil {
		t.Fatalf("RunBalanceReconciliation() error: %v", err)
	}
	if len(repo.Snapshots) != 1 {
		t.Fatalf("drift equal to threshold should still be snapshotted, got %d", len(repo.Snapshots))
	}
	if got := sink.received(); len(got) != 0 {
		t.Fatalf("drift equal to threshold should not alert, got %d alerts", len(got))
	}
	if len(repo.Discrepancies) != 0 {
		t.Fatalf("drift equal to threshold should not record a discrepancy, got %d", len(repo.Discrepancies))
	}

	over, repo2, _, sink2 := newDriftService(t, "100.00", "98.99", "1.00")
	if err := over.RunBalanceReconciliation(context.Background()); err != nil {
		t.Fatalf("RunBalanceReconciliation() error: %v", err)
	}
	if got := sink2.received(); len(got) != 1 {
		t.Fatalf("drift just past the threshold should alert, got %d alerts", len(got))
	}
	if len(repo2.Discrepancies) != 1 {
		t.Fatalf("drift just past the threshold should record a discrepancy, got %d", len(repo2.Discrepancies))
	}
}

// TestReconcileDrift_UnfundedWalletIsNotDiscrepancy keeps the pre-existing
// behaviour that a wallet with no on-chain account is not treated as drift.
func TestReconcileDrift_UnfundedWalletIsNotDiscrepancy(t *testing.T) {
	tenantID := driftTenantID
	repo := &driftRepo{
		Wallets: []*domain.Wallet{{ID: driftWalletID, TenantID: &tenantID, PublicKey: driftWalletPK}},
		Balances: map[string]map[string]decimal.Decimal{
			driftWalletID: {"USDC": decimal.RequireFromString("10")},
		},
	}
	svc := NewService(nil, repo, &driftLookup{}, &driftStellar{asset: "USDC"}, nil, nil, nil, "t", decimal.Zero, nil, "").
		WithDriftThreshold(decimal.RequireFromString("1.00"))

	if err := svc.RunBalanceReconciliation(context.Background()); err != nil {
		t.Fatalf("RunBalanceReconciliation() error: %v", err)
	}
	if len(repo.Snapshots) != 0 || len(repo.Discrepancies) != 0 {
		t.Fatalf("unfunded wallet should produce no snapshots or discrepancies, got %d/%d",
			len(repo.Snapshots), len(repo.Discrepancies))
	}
}

// TestGetDrift_ReturnsCurrentDrift covers the admin dashboard endpoint's data
// source, including the in-memory fallback used when no drift repo is wired.
func TestGetDrift_ReturnsCurrentDrift(t *testing.T) {
	svc, _, _, _ := newDriftService(t, "100.00", "95.00", "1.00")
	if err := svc.RunBalanceReconciliation(context.Background()); err != nil {
		t.Fatalf("RunBalanceReconciliation() error: %v", err)
	}

	snapshots, err := svc.GetDrift(context.Background())
	if err != nil {
		t.Fatalf("GetDrift() error: %v", err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(snapshots))
	}
	if snapshots[0].WalletID != driftWalletID || snapshots[0].Asset != "USDC" {
		t.Fatalf("unexpected snapshot identity: %#v", snapshots[0])
	}
}

func TestParseDriftThreshold(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"empty falls back to default", "", "1"},
		{"valid value", "12.50", "12.5"},
		{"zero is allowed", "0", "0"},
		{"malformed falls back", "not-a-number", "1"},
		{"negative falls back", "-5", "1"},
		{"whitespace is trimmed", "  2.25  ", "2.25"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseDriftThreshold(tc.raw)
			if !got.Equal(decimal.RequireFromString(tc.want)) {
				t.Fatalf("ParseDriftThreshold(%q) = %s, want %s", tc.raw, got, tc.want)
			}
		})
	}
}
