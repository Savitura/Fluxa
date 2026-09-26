package wallet_test

import (
	"context"
	"errors"
	"math/big"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/shopspring/decimal"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

// Both adapters must satisfy the same Service interface.
var (
	_ wallet.Service         = (*wallet.ContractWalletAdapter)(nil)
	_ wallet.ContractService = (*wallet.ContractWalletAdapter)(nil)
)

const (
	testContractID = "CAAQEAYEAUDAOCAJBIFQYDIOB4IBCEQTCQKRMFYYDENBWHA5DYPSBFLM"
	testOwner      = "GBRPYHIL2CI3FNQ4BXLFMNDLFJUNPU2HY3ZMFSHONUCEOASW7QC7OX2H"
	testAssetSAC   = "CDEMPRWFYTB4FQOAX67L3PF3XK43RN5WWW2LHMVRWCX25LNMVOVKTGRU"
)

// contractRepo is a self-contained repository stub so these tests do not
// depend on mocks defined in the package's other test files.
type contractRepo struct {
	wallets  map[string]*domain.Wallet
	balances map[string][]domain.BalanceRecord
}

func newContractRepo() *contractRepo {
	return &contractRepo{
		wallets:  make(map[string]*domain.Wallet),
		balances: make(map[string][]domain.BalanceRecord),
	}
}

func (m *contractRepo) Create(ctx context.Context, w *domain.Wallet) error {
	m.wallets[w.ID] = w
	return nil
}

func (m *contractRepo) GetByID(ctx context.Context, id string) (*domain.Wallet, error) {
	w, ok := m.wallets[id]
	if !ok {
		return nil, domain.ErrWalletNotFound
	}
	return w, nil
}

func (m *contractRepo) GetByPublicKey(ctx context.Context, pubKey string) (*domain.Wallet, error) {
	for _, w := range m.wallets {
		if w.PublicKey == pubKey {
			return w, nil
		}
	}
	return nil, domain.ErrWalletNotFound
}

func (m *contractRepo) List(ctx context.Context, limit, offset int) ([]*domain.Wallet, error) {
	return nil, nil
}

func (m *contractRepo) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	return len(m.wallets), nil
}

func (m *contractRepo) UpsertBalance(ctx context.Context, walletID, assetCode, issuer string, balance decimal.Decimal) error {
	m.balances[walletID] = append(m.balances[walletID], domain.BalanceRecord{
		WalletID: walletID, AssetCode: assetCode, Issuer: issuer, Balance: balance.String(),
	})
	return nil
}

func (m *contractRepo) GetBalances(ctx context.Context, walletID string) ([]domain.BalanceRecord, error) {
	return m.balances[walletID], nil
}

func (m *contractRepo) UpdateSyncCursor(ctx context.Context, walletID, cursor string) error {
	return nil
}

type stubSoroban struct {
	prepared   *txnbuild.Transaction
	submitted  *txnbuild.Transaction
	lastFn     string
	lastArgs   xdr.ScVec
	simulateFn func(fn string) (xdr.ScVal, error)
	prepareErr error
}

func (s *stubSoroban) NetworkPassphrase() string { return "Test SDF Network ; September 2015" }

func (s *stubSoroban) record(op *txnbuild.InvokeHostFunction) {
	if op.HostFunction.InvokeContract != nil {
		s.lastFn = string(op.HostFunction.InvokeContract.FunctionName)
		s.lastArgs = op.HostFunction.InvokeContract.Args
	}
}

func (s *stubSoroban) PrepareInvocation(ctx context.Context, source string, op *txnbuild.InvokeHostFunction) (*txnbuild.Transaction, error) {
	s.record(op)
	if s.prepareErr != nil {
		return nil, s.prepareErr
	}

	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &txnbuild.SimpleAccount{AccountID: source, Sequence: 1},
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewTimeout(300)},
	})
	if err != nil {
		return nil, err
	}
	s.prepared = tx
	return tx, nil
}

func (s *stubSoroban) SimulateInvocation(ctx context.Context, source string, op *txnbuild.InvokeHostFunction) (xdr.ScVal, error) {
	s.record(op)
	return s.simulateFn(s.lastFn)
}

func (s *stubSoroban) SubmitTransaction(ctx context.Context, tx *txnbuild.Transaction) (string, error) {
	s.submitted = tx
	return "contract_tx_hash_abc", nil
}

type stubDeployer struct {
	gotOwner  string
	gotParams wallet.ContractWalletParams
	err       error
}

func (d *stubDeployer) Deploy(ctx context.Context, owner string, params wallet.ContractWalletParams) (string, error) {
	d.gotOwner = owner
	d.gotParams = params
	if d.err != nil {
		return "", d.err
	}
	return testContractID, nil
}

type stubAssets struct{}

func (stubAssets) ContractAddress(assetCode, issuer string) (string, error) { return testAssetSAC, nil }

type stubSigner struct{ gotSecret string }

func (s *stubSigner) Sign(tx *txnbuild.Transaction, encryptedSecret string) (*txnbuild.Transaction, error) {
	s.gotSecret = encryptedSecret
	return tx, nil
}

func newContractAdapter(t *testing.T, repo *contractRepo, soroban *stubSoroban, deployer *stubDeployer) *wallet.ContractWalletAdapter {
	t.Helper()
	a := wallet.NewContractWalletAdapter(repo, soroban, deployer, stubAssets{}, wallet.ContractWalletParams{
		RecoveryThreshold:     2,
		SpendingLimit:         decimal.NewFromInt(1000),
		SpendingWindowSeconds: 86400,
	})
	a.WithSigner(&stubSigner{})
	return a
}

func TestContractCreateWalletStoresNoSecret(t *testing.T) {
	repo := newContractRepo()
	deployer := &stubDeployer{}
	adapter := newContractAdapter(t, repo, &stubSoroban{}, deployer)

	w, err := adapter.CreateWallet(context.Background(), testOwner)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if w.EncryptedSecret != "" {
		t.Fatal("contract wallet must not persist an encrypted secret")
	}
	if w.ContractID != testContractID {
		t.Fatalf("expected contract id %s, got %s", testContractID, w.ContractID)
	}
	if w.CustodyType != domain.CustodyContract {
		t.Fatalf("expected contract custody, got %s", w.CustodyType)
	}
	if w.PublicKey != testOwner {
		t.Fatalf("expected owner %s to be the wallet public key, got %s", testOwner, w.PublicKey)
	}
	if deployer.gotOwner != testOwner {
		t.Fatalf("expected deployer to receive owner %s, got %s", testOwner, deployer.gotOwner)
	}
}

func TestContractCreateWalletRequiresOwnerKey(t *testing.T) {
	adapter := newContractAdapter(t, newContractRepo(), &stubSoroban{}, &stubDeployer{})

	if _, err := adapter.CreateWallet(context.Background()); !errors.Is(err, domain.ErrOwnerKeyRequired) {
		t.Fatalf("expected ErrOwnerKeyRequired, got %v", err)
	}
}

func TestContractExecuteTransferInvokesExecutePayment(t *testing.T) {
	repo := newContractRepo()
	repo.wallets["w-c1"] = &domain.Wallet{
		ID:          "w-c1",
		PublicKey:   testOwner,
		ContractID:  testContractID,
		CustodyType: domain.CustodyContract,
	}

	soroban := &stubSoroban{}
	adapter := newContractAdapter(t, repo, soroban, &stubDeployer{})

	hash, err := adapter.ExecuteTransfer(
		context.Background(), "w-c1", testOwner, "USDC", "", decimal.NewFromFloat(12.5), "invoice-7",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hash != "contract_tx_hash_abc" {
		t.Fatalf("expected submitted tx hash, got %s", hash)
	}
	if soroban.lastFn != "execute_payment" {
		t.Fatalf("expected execute_payment invocation, got %s", soroban.lastFn)
	}
	if len(soroban.lastArgs) != 4 {
		t.Fatalf("expected 4 args (destination, asset, amount, memo), got %d", len(soroban.lastArgs))
	}

	// Amount must be converted to 7-decimal stroops: 12.5 -> 125000000.
	amount, err := stellar.DecodeI128(soroban.lastArgs[2])
	if err != nil {
		t.Fatalf("decode amount arg: %v", err)
	}
	if amount.Cmp(big.NewInt(125_000_000)) != 0 {
		t.Fatalf("expected 125000000 stroops, got %s", amount)
	}

	if soroban.submitted == nil {
		t.Fatal("expected the signed transaction to be submitted")
	}
}

func TestContractExecuteTransferRejectsCustodialWallet(t *testing.T) {
	repo := newContractRepo()
	repo.wallets["w-cust"] = &domain.Wallet{ID: "w-cust", PublicKey: testOwner, CustodyType: domain.CustodyCustodial}

	adapter := newContractAdapter(t, repo, &stubSoroban{}, &stubDeployer{})

	_, err := adapter.ExecuteTransfer(
		context.Background(), "w-cust", testOwner, "USDC", "", decimal.NewFromInt(1), "",
	)
	if !errors.Is(err, domain.ErrNotContractWallet) {
		t.Fatalf("expected ErrNotContractWallet, got %v", err)
	}
}

func TestContractGetSpendingStatusDecodesContractReturn(t *testing.T) {
	repo := newContractRepo()
	repo.wallets["w-c2"] = &domain.Wallet{ID: "w-c2", PublicKey: testOwner, ContractID: testContractID}

	soroban := &stubSoroban{
		simulateFn: func(fn string) (xdr.ScVal, error) {
			return scMap(map[string]xdr.ScVal{
				"limit":            stellar.I128ScVal(big.NewInt(1_000_0000000)),
				"spent_in_window":  stellar.I128ScVal(big.NewInt(250_0000000)),
				"remaining":        stellar.I128ScVal(big.NewInt(750_0000000)),
				"window_resets_at": stellar.U64ScVal(1893456000),
			}), nil
		},
	}

	adapter := newContractAdapter(t, repo, soroban, &stubDeployer{})

	status, err := adapter.GetSpendingStatus(context.Background(), "w-c2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if status.Limit != "1000" || status.SpentInWindow != "250" || status.Remaining != "750" {
		t.Fatalf("unexpected spending status: %+v", status)
	}
	if status.WindowResetsAt != 1893456000 {
		t.Fatalf("expected window reset 1893456000, got %d", status.WindowResetsAt)
	}
}

func TestContractAddTrustlineIsRejected(t *testing.T) {
	adapter := newContractAdapter(t, newContractRepo(), &stubSoroban{}, &stubDeployer{})

	if _, err := adapter.AddTrustline(context.Background(), "w-1", "USDC", "", ""); !errors.Is(err, domain.ErrTrustlineNotApplicable) {
		t.Fatalf("expected ErrTrustlineNotApplicable, got %v", err)
	}
}

func TestContractExecuteTransferRejectsSubPrecisionAmount(t *testing.T) {
	repo := newContractRepo()
	repo.wallets["w-c1"] = &domain.Wallet{
		ID:          "w-c1",
		PublicKey:   testOwner,
		ContractID:  testContractID,
		CustodyType: domain.CustodyContract,
	}

	adapter := newContractAdapter(t, repo, &stubSoroban{}, &stubDeployer{})

	// 8 decimal places — should be rejected.
	_, err := adapter.ExecuteTransfer(
		context.Background(), "w-c1", testOwner, "USDC", "", decimal.RequireFromString("1.12345678"), "",
	)
	if !errors.Is(err, domain.ErrSubPrecisionAmount) {
		t.Fatalf("expected ErrSubPrecisionAmount for 8 decimal places, got %v", err)
	}
}

func TestContractExecuteTransferRejectsZeroAfterTruncation(t *testing.T) {
	repo := newContractRepo()
	repo.wallets["w-c1"] = &domain.Wallet{
		ID:          "w-c1",
		PublicKey:   testOwner,
		ContractID:  testContractID,
		CustodyType: domain.CustodyContract,
	}

	adapter := newContractAdapter(t, repo, &stubSoroban{}, &stubDeployer{})

	// 0.00000001 has 8 decimal places — sub-precision, would truncate to 0.
	_, err := adapter.ExecuteTransfer(
		context.Background(), "w-c1", testOwner, "USDC", "", decimal.RequireFromString("0.00000001"), "",
	)
	if !errors.Is(err, domain.ErrSubPrecisionAmount) {
		t.Fatalf("expected ErrSubPrecisionAmount for sub-stroop amount, got %v", err)
	}
}

func TestContractExecuteTransferAcceptsMaxPrecision(t *testing.T) {
	repo := newContractRepo()
	repo.wallets["w-c1"] = &domain.Wallet{
		ID:          "w-c1",
		PublicKey:   testOwner,
		ContractID:  testContractID,
		CustodyType: domain.CustodyContract,
	}

	soroban := &stubSoroban{}
	adapter := newContractAdapter(t, repo, soroban, &stubDeployer{})

	// Exactly 7 decimal places — should be accepted.
	hash, err := adapter.ExecuteTransfer(
		context.Background(), "w-c1", testOwner, "USDC", "", decimal.RequireFromString("1.1234567"), "",
	)
	if err != nil {
		t.Fatalf("unexpected error for max precision amount: %v", err)
	}
	if hash != "contract_tx_hash_abc" {
		t.Fatalf("expected submitted tx hash, got %s", hash)
	}

	// Verify the stroops value: 1.1234567 * 10^7 = 11234567.
	amount, err := stellar.DecodeI128(soroban.lastArgs[2])
	if err != nil {
		t.Fatalf("decode amount arg: %v", err)
	}
	if amount.Cmp(big.NewInt(11_234_567)) != 0 {
		t.Fatalf("expected 11234567 stroops, got %s", amount)
	}
}

func TestContractExecuteTransferAcceptsLessThanMaxPrecision(t *testing.T) {
	repo := newContractRepo()
	repo.wallets["w-c1"] = &domain.Wallet{
		ID:          "w-c1",
		PublicKey:   testOwner,
		ContractID:  testContractID,
		CustodyType: domain.CustodyContract,
	}

	soroban := &stubSoroban{}
	adapter := newContractAdapter(t, repo, soroban, &stubDeployer{})

	// Fewer than 7 decimal places — should be accepted.
	hash, err := adapter.ExecuteTransfer(
		context.Background(), "w-c1", testOwner, "USDC", "", decimal.RequireFromString("12.5"), "",
	)
	if err != nil {
		t.Fatalf("unexpected error for 1-decimal amount: %v", err)
	}
	if hash != "contract_tx_hash_abc" {
		t.Fatalf("expected submitted tx hash, got %s", hash)
	}
}

func TestContractExecuteTransferRejectsLargeSubPrecisionAmount(t *testing.T) {
	repo := newContractRepo()
	repo.wallets["w-c1"] = &domain.Wallet{
		ID:          "w-c1",
		PublicKey:   testOwner,
		ContractID:  testContractID,
		CustodyType: domain.CustodyContract,
	}

	adapter := newContractAdapter(t, repo, &stubSoroban{}, &stubDeployer{})

	// Large amount with sub-precision — should be rejected.
	_, err := adapter.ExecuteTransfer(
		context.Background(), "w-c1", testOwner, "USDC", "", decimal.RequireFromString("1000.00000001"), "",
	)
	if !errors.Is(err, domain.ErrSubPrecisionAmount) {
		t.Fatalf("expected ErrSubPrecisionAmount for large sub-precision amount, got %v", err)
	}
}

// scMap builds the ScMap encoding Soroban uses for a contract struct return.
func scMap(fields map[string]xdr.ScVal) xdr.ScVal {
	keys := []string{"limit", "spent_in_window", "remaining", "window_resets_at"}
	entries := make(xdr.ScMap, 0, len(fields))
	for _, k := range keys {
		v, ok := fields[k]
		if !ok {
			continue
		}
		sym := xdr.ScSymbol(k)
		entries = append(entries, xdr.ScMapEntry{
			Key: xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym},
			Val: v,
		})
	}
	m := &entries
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &m}
}
