package treasury_test

import (
	"context"
	"errors"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/treasury"
	"github.com/shopspring/decimal"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/base"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/txnbuild"
)

func nativeBalance(amount string) horizon.Balance {
	return horizon.Balance{Balance: amount, Asset: base.Asset{Type: "native"}}
}

func creditBalance(code, issuer, amount string) horizon.Balance {
	return horizon.Balance{Balance: amount, Asset: base.Asset{Type: "credit_alphanum4", Code: code, Issuer: issuer}}
}

// mockRepo is an in-memory treasury.Repository.
type mockRepo struct {
	configs    map[string]*treasury.Config
	publicKeys []string
	sweeps     []*treasury.SweepLog
}

func newMockRepo() *mockRepo {
	return &mockRepo{configs: map[string]*treasury.Config{}}
}

func (m *mockRepo) GetConfig(ctx context.Context, asset string) (*treasury.Config, error) {
	cfg, ok := m.configs[asset]
	if !ok {
		return nil, domain.ErrTreasuryConfigNotFound
	}
	cp := *cfg
	return &cp, nil
}

func (m *mockRepo) ListConfig(ctx context.Context) ([]*treasury.Config, error) {
	var out []*treasury.Config
	for _, cfg := range m.configs {
		cp := *cfg
		out = append(out, &cp)
	}
	return out, nil
}

func (m *mockRepo) UpdateConfig(ctx context.Context, cfg *treasury.Config) error {
	cp := *cfg
	m.configs[cfg.Asset] = &cp
	return nil
}

func (m *mockRepo) ListWalletPublicKeys(ctx context.Context) ([]string, error) {
	return m.publicKeys, nil
}

func (m *mockRepo) RecordSweep(ctx context.Context, entry *treasury.SweepLog) error {
	m.sweeps = append(m.sweeps, entry)
	return nil
}

func (m *mockRepo) ListSweeps(ctx context.Context, limit, offset int) ([]*treasury.SweepLog, error) {
	return m.sweeps, nil
}

// mockStellarClient is a minimal stellar.Client double.
type mockStellarClient struct {
	accounts map[string]horizon.Account
	offers   map[string][]horizon.Offer
}

func (m *mockStellarClient) LoadAccount(accountID string) (horizon.Account, error) {
	acct, ok := m.accounts[accountID]
	if !ok {
		return horizon.Account{}, errors.New("account not found")
	}
	return acct, nil
}
func (m *mockStellarClient) SubmitTransaction(tx *txnbuild.Transaction) (horizon.Transaction, error) {
	return horizon.Transaction{Hash: "fake-tx-hash"}, nil
}
func (m *mockStellarClient) FindPathsStrict(sourceAccount, destAsset, destIssuer, destAmount string) ([]horizon.Path, error) {
	return nil, nil
}
func (m *mockStellarClient) TransactionDetail(hash string) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}
func (m *mockStellarClient) OperationsForTransaction(hash string) ([]operations.Operation, error) {
	return nil, nil
}
func (m *mockStellarClient) PaymentsForAccount(_ string, _ string, _ int) ([]operations.Payment, error) {
	return nil, nil
}

func (m *mockStellarClient) Payments(accountID, cursor string, limit uint) ([]operations.Operation, error) {
	return nil, nil
}
func (m *mockStellarClient) StreamPayments(ctx context.Context, accountID, cursor string, handler func(operations.Operation) error) error {
	return nil
}
func (m *mockStellarClient) Offers(accountID string, limit uint) ([]horizon.Offer, error) {
	return m.offers[accountID], nil
}

const feeWallet = "GFEEWALLET"

func newFixture() (*mockRepo, *mockStellarClient) {
	repo := newMockRepo()
	repo.configs["XLM"] = &treasury.Config{Asset: "XLM"}
	repo.configs["USDC"] = &treasury.Config{Asset: "USDC"}
	repo.publicKeys = []string{"GWALLETA", "GWALLETB"}

	stClient := &mockStellarClient{
		accounts: map[string]horizon.Account{
			feeWallet: {ID: feeWallet, AccountID: feeWallet, Balances: []horizon.Balance{
				nativeBalance("4.0000000"),
			}},
			"GWALLETA": {Balances: []horizon.Balance{
				nativeBalance("10.0000000"),
				creditBalance("USDC", "GISSUER", "5.0000000"),
			}},
			"GWALLETB": {Balances: []horizon.Balance{
				nativeBalance("10.0000000"),
				creditBalance("USDC", "GISSUER", "5.0000000"),
				creditBalance("EURC", "GISSUER", "5.0000000"),
			}},
		},
		offers: map[string][]horizon.Offer{
			"GWALLETA": {{ID: 1}},
		},
	}
	return repo, stClient
}

// TestReserveRequirementArithmetic validates GetReserveBreakdown against
// manual arithmetic for a known fixed state: 2 wallets (1 trustline + 1
// offer, and 2 trustlines + 0 offers) => 3 trustlines, 1 offer total.
// total = wallets*(2*0.5) + trustlines*0.5 + offers*0.5
//
//	= 2*1.0 + 3*0.5 + 1*0.5 = 2.0 + 1.5 + 0.5 = 4.0
func TestReserveRequirementArithmetic(t *testing.T) {
	repo, stClient := newFixture()
	svc := treasury.NewService(repo, stClient, nil, nil, feeWallet, "testnet", "", "GUSDC", "GEURC")

	breakdown, err := svc.GetReserveBreakdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if breakdown.WalletCount != 2 {
		t.Errorf("expected 2 wallets, got %d", breakdown.WalletCount)
	}
	if breakdown.TrustlineCount != 3 {
		t.Errorf("expected 3 trustlines, got %d", breakdown.TrustlineCount)
	}
	if breakdown.OfferCount != 1 {
		t.Errorf("expected 1 offer, got %d", breakdown.OfferCount)
	}

	want := decimal.RequireFromString("4.0000000")
	if !breakdown.TotalXLMRequired.Equal(want) {
		t.Errorf("expected total reserve %s, got %s", want, breakdown.TotalXLMRequired)
	}

	wantSurplus := decimal.RequireFromString("4.0000000").Sub(want) // fee wallet balance (4.0) - total (4.0)
	if !breakdown.Surplus.Equal(wantSurplus) {
		t.Errorf("expected surplus %s, got %s", wantSurplus, breakdown.Surplus)
	}
}

// TestSweepableAmountFloorsAtZero checks that when the fee wallet's XLM
// balance exactly equals the reserve requirement (no operating buffer
// configured), sweepable_amount is 0, never negative.
func TestSweepableAmountFloorsAtZero(t *testing.T) {
	repo, stClient := newFixture()
	svc := treasury.NewService(repo, stClient, nil, nil, feeWallet, "testnet", "", "GUSDC", "GEURC")

	sweepable, err := svc.GetSweepableAmount(context.Background(), "XLM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sweepable.IsZero() {
		t.Errorf("expected sweepable amount 0, got %s", sweepable)
	}
}

// TestSweepableAmountBelowReserveReturnsZero pushes the fee wallet's balance
// below the reserve requirement and checks it still floors at zero rather
// than going negative.
func TestSweepableAmountBelowReserveReturnsZero(t *testing.T) {
	repo, stClient := newFixture()
	acct := stClient.accounts[feeWallet]
	acct.Balances = []horizon.Balance{nativeBalance("1.0000000")} // below the 4.0 XLM reserve
	stClient.accounts[feeWallet] = acct

	svc := treasury.NewService(repo, stClient, nil, nil, feeWallet, "testnet", "", "GUSDC", "GEURC")

	sweepable, err := svc.GetSweepableAmount(context.Background(), "XLM")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !sweepable.IsZero() {
		t.Errorf("expected sweepable amount 0, got %s", sweepable)
	}
}

func TestExecuteSweepOverSweepableReturnsInsufficientBalance(t *testing.T) {
	repo, stClient := newFixture()
	// USDC has no reserve requirement, min_operating_buffer is 0 by default,
	// so the whole USDC balance (5+5=10 across the two wallets isn't on the
	// fee wallet though — the fee wallet itself holds no USDC in this
	// fixture, so sweepable is 0 and any positive amount should be rejected.
	svc := treasury.NewService(repo, stClient, nil, nil, feeWallet, "testnet", "", "GUSDC", "GEURC")

	_, err := svc.ExecuteSweep(context.Background(), "USDC", decimal.NewFromInt(1), "GCOLDSTORAGE", treasury.TriggeredByManual)
	if !errors.Is(err, domain.ErrInsufficientSweepableBalance) {
		t.Fatalf("expected ErrInsufficientSweepableBalance, got %v", err)
	}
}

func TestExecuteSweepZeroAmountWritesAuditRecord(t *testing.T) {
	repo, stClient := newFixture()
	svc := treasury.NewService(repo, stClient, nil, nil, feeWallet, "testnet", "", "GUSDC", "GEURC")

	txHash, err := svc.ExecuteSweep(context.Background(), "XLM", decimal.Zero, "GCOLDSTORAGE", treasury.TriggeredByAuto)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if txHash != "" {
		t.Errorf("expected empty tx hash for a zero sweep, got %q", txHash)
	}
	if len(repo.sweeps) != 1 {
		t.Fatalf("expected exactly one sweep_log record, got %d", len(repo.sweeps))
	}
	if !repo.sweeps[0].Amount.IsZero() || repo.sweeps[0].TriggeredBy != treasury.TriggeredByAuto {
		t.Errorf("unexpected sweep record: %+v", repo.sweeps[0])
	}
}
