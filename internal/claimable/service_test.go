package claimable_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/claimable"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/shopspring/decimal"
	"github.com/stellar/go/keypair"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/operations"
	"github.com/stellar/go/txnbuild"
	"github.com/stellar/go/xdr"
)

// ---------------------------------------------------------------- repository

type mockRepo struct {
	balances map[string]*domain.ClaimableBalance
	order    []string
}

func newMockRepo() *mockRepo {
	return &mockRepo{balances: map[string]*domain.ClaimableBalance{}}
}

func (m *mockRepo) Create(ctx context.Context, b *domain.ClaimableBalance) error {
	if tID := tenant.IDFromContext(ctx); tID != "" {
		b.TenantID = &tID
	}
	cp := *b
	m.balances[b.ID] = &cp
	m.order = append(m.order, b.ID)
	return nil
}

func (m *mockRepo) GetByID(ctx context.Context, id string) (*domain.ClaimableBalance, error) {
	b, ok := m.visible(ctx, id)
	if !ok {
		return nil, domain.ErrClaimableBalanceNotFound
	}
	cp := *b
	return &cp, nil
}

func (m *mockRepo) visible(ctx context.Context, id string) (*domain.ClaimableBalance, bool) {
	b, ok := m.balances[id]
	if !ok {
		return nil, false
	}
	if tID := tenant.IDFromContext(ctx); tID != "" {
		if b.TenantID == nil || *b.TenantID != tID {
			return nil, false
		}
	}
	return b, true
}

func (m *mockRepo) List(ctx context.Context, f claimable.Filter) ([]*domain.ClaimableBalance, error) {
	var out []*domain.ClaimableBalance
	for _, id := range m.order {
		b, ok := m.visible(ctx, id)
		if !ok {
			continue
		}
		if f.Status != "" && b.Status != f.Status {
			continue
		}
		if f.Asset != "" && b.Asset != f.Asset {
			continue
		}
		if f.Claimant != "" && !hasClaimant(b, f.Claimant) {
			continue
		}
		if f.ExpiresBefore != nil && (b.ExpiresAt == nil || !b.ExpiresAt.Before(*f.ExpiresBefore)) {
			continue
		}
		cp := *b
		out = append(out, &cp)
	}

	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return nil, nil
		}
		out = out[f.Offset:]
	}
	if f.Limit > 0 && f.Limit < len(out) {
		out = out[:f.Limit]
	}
	return out, nil
}

func hasClaimant(b *domain.ClaimableBalance, account string) bool {
	for _, c := range b.Claimants {
		if c.Account == account {
			return true
		}
	}
	return false
}

func (m *mockRepo) mark(ctx context.Context, id string, status domain.ClaimableBalanceStatus, claimedBy string) error {
	b, ok := m.visible(ctx, id)
	if !ok {
		return domain.ErrClaimableBalanceNotFound
	}
	if b.Status != domain.ClaimableBalanceStatusPending {
		return domain.ErrClaimableBalanceNotPending
	}
	b.Status = status
	b.ClaimedBy = claimedBy
	now := time.Now().UTC()
	b.ClaimedAt = &now
	return nil
}

func (m *mockRepo) MarkClaimed(ctx context.Context, id, claimedBy string, _ time.Time) error {
	return m.mark(ctx, id, domain.ClaimableBalanceStatusClaimed, claimedBy)
}

func (m *mockRepo) MarkExpired(ctx context.Context, id string, _ time.Time) error {
	return m.mark(ctx, id, domain.ClaimableBalanceStatusExpired, "")
}

func (m *mockRepo) MarkRevoked(ctx context.Context, id string, _ time.Time) error {
	return m.mark(ctx, id, domain.ClaimableBalanceStatusRevoked, "")
}

func (m *mockRepo) ListExpiredPending(_ context.Context, now time.Time) ([]*domain.ClaimableBalance, error) {
	var out []*domain.ClaimableBalance
	for _, id := range m.order {
		b := m.balances[id]
		if b.Status != domain.ClaimableBalanceStatusPending || b.ExpiresAt == nil || !b.ExpiresAt.Before(now) {
			continue
		}
		cp := *b
		out = append(out, &cp)
	}
	return out, nil
}

// -------------------------------------------------------------- stellar mocks

type mockStellar struct {
	accounts  map[string]horizon.Account
	submitted []*txnbuild.Transaction
	submitErr error
}

func (m *mockStellar) LoadAccount(accountID string) (horizon.Account, error) {
	acct, ok := m.accounts[accountID]
	if !ok {
		return horizon.Account{}, fmt.Errorf("account %s not found", accountID)
	}
	return acct, nil
}

func (m *mockStellar) SubmitTransaction(tx *txnbuild.Transaction) (horizon.Transaction, error) {
	if m.submitErr != nil {
		return horizon.Transaction{}, m.submitErr
	}
	m.submitted = append(m.submitted, tx)
	return horizon.Transaction{Hash: fmt.Sprintf("tx-hash-%d", len(m.submitted))}, nil
}

func (m *mockStellar) FindPathsStrict(_, _, _, _ string) ([]horizon.Path, error) { return nil, nil }
func (m *mockStellar) TransactionDetail(string) (horizon.Transaction, error) {
	return horizon.Transaction{}, nil
}
func (m *mockStellar) OperationsForTransaction(string) ([]operations.Operation, error) {
	return nil, nil
}
func (m *mockStellar) PaymentsForAccount(_, _ string, _ int) ([]operations.Payment, error) {
	return nil, nil
}
func (m *mockStellar) Payments(_, _ string, _ uint) ([]operations.Operation, error) { return nil, nil }
func (m *mockStellar) StreamPayments(context.Context, string, string, func(operations.Operation) error) error {
	return nil
}
func (m *mockStellar) Offers(string, uint) ([]horizon.Offer, error) { return nil, nil }

type mockClaimables struct {
	byID map[string]horizon.ClaimableBalance
}

func (m *mockClaimables) ClaimableBalance(id string) (horizon.ClaimableBalance, error) {
	cb, ok := m.byID[id]
	if !ok {
		return horizon.ClaimableBalance{}, fmt.Errorf("%w: %s", stellar.ErrClaimableBalanceNotFound, id)
	}
	return cb, nil
}

func (m *mockClaimables) ClaimableBalancesForAccount(string, uint) ([]horizon.ClaimableBalance, error) {
	return nil, nil
}

type mockSigner struct {
	secrets []string
}

func (m *mockSigner) Sign(tx *txnbuild.Transaction, encryptedSecret string) (*txnbuild.Transaction, error) {
	m.secrets = append(m.secrets, encryptedSecret)
	return tx, nil
}

type mockResolver struct {
	wallets map[string]*claimable.SourceWallet
}

func (m *mockResolver) GetByID(_ context.Context, walletID string) (*claimable.SourceWallet, error) {
	if w, ok := m.wallets["id:"+walletID]; ok {
		return w, nil
	}
	return nil, domain.ErrWalletNotFound
}

func (m *mockResolver) GetByPublicKey(_ context.Context, publicKey string) (*claimable.SourceWallet, error) {
	if w, ok := m.wallets["pk:"+publicKey]; ok {
		return w, nil
	}
	return nil, domain.ErrWalletNotFound
}

type mockWebhooks struct {
	events []string
}

func (m *mockWebhooks) Dispatch(_ context.Context, _ *string, eventType string, _ interface{}) error {
	m.events = append(m.events, eventType)
	return nil
}

// ------------------------------------------------------------------- fixture

type fixture struct {
	repo       *mockRepo
	stellar    *mockStellar
	claimables *mockClaimables
	signer     *mockSigner
	resolver   *mockResolver
	webhooks   *mockWebhooks
	svc        claimable.Service

	source   *keypair.Full
	sponsor  *keypair.Full
	claimant *keypair.Full
}

func newFixture(t *testing.T) *fixture {
	t.Helper()

	source := keypair.MustRandom()
	sponsor := keypair.MustRandom()
	claimant := keypair.MustRandom()

	accounts := map[string]horizon.Account{}
	for i, kp := range []*keypair.Full{source, sponsor, claimant} {
		accounts[kp.Address()] = horizon.Account{
			ID:        kp.Address(),
			AccountID: kp.Address(),
			Sequence:  int64((i + 1) * 100),
		}
	}

	resolver := &mockResolver{wallets: map[string]*claimable.SourceWallet{}}
	add := func(id string, kp *keypair.Full) {
		w := &claimable.SourceWallet{
			ID:              id,
			PublicKey:       kp.Address(),
			EncryptedSecret: hex.EncodeToString([]byte(kp.Seed())),
		}
		resolver.wallets["id:"+id] = w
		resolver.wallets["pk:"+kp.Address()] = w
	}
	add("src-wallet", source)
	add("sponsor-wallet", sponsor)
	add("claimant-wallet", claimant)

	repo := newMockRepo()
	st := &mockStellar{accounts: accounts}
	claimables := &mockClaimables{byID: map[string]horizon.ClaimableBalance{}}
	signer := &mockSigner{}
	webhooks := &mockWebhooks{}

	svc := claimable.NewService(
		repo, st, claimables, signer, resolver, webhooks,
		"src-wallet",
		map[string]string{"USDC": "GUSDCISSUER"},
	)

	return &fixture{
		repo: repo, stellar: st, claimables: claimables,
		signer: signer, resolver: resolver, webhooks: webhooks, svc: svc,
		source: source, sponsor: sponsor, claimant: claimant,
	}
}

// validBalanceID builds a syntactically valid Stellar claimable balance ID from
// a readable label: the 4-byte V0 type discriminant followed by a 32-byte hash.
// Claim operations validate the format, so seeded test balances have to be
// well-formed even though they never reach a network.
func validBalanceID(label string) string {
	sum := sha256.Sum256([]byte(label))
	return hex.EncodeToString(append([]byte{0, 0, 0, 0}, sum[:]...))
}

// balanceID returns the ID seed stored this label under.
func (f *fixture) balanceID(label string) string { return validBalanceID(label) }

// seed inserts a pending balance directly, so claim and expiry tests exercise
// one path at a time rather than going back through creation. It returns the
// Stellar-shaped balance ID and also registers the readable label as an alias,
// so assertions can name a balance while the service still sees a real ID.
func (f *fixture) seed(label string, expiresAt *time.Time, revokeOnExpiry bool, claimants ...domain.Claimant) string {
	id := validBalanceID(label)
	balance := &domain.ClaimableBalance{
		ID:             id,
		Asset:          "XLM",
		Amount:         decimal.RequireFromString("10"),
		Claimants:      claimants,
		Status:         domain.ClaimableBalanceStatusPending,
		RevokeOnExpiry: revokeOnExpiry,
		CreatedAt:      time.Now().UTC().Add(-time.Hour),
		ExpiresAt:      expiresAt,
	}
	f.repo.balances[id] = balance
	f.repo.balances[label] = balance
	f.repo.order = append(f.repo.order, id)
	return id
}

func past() *time.Time {
	t := time.Now().UTC().Add(-time.Minute)
	return &t
}

func future() *time.Time {
	t := time.Now().UTC().Add(time.Hour)
	return &t
}

// --------------------------------------------------------------------- create

func TestCreateBuildsSingleOperationAndStoresBalance(t *testing.T) {
	f := newFixture(t)
	deadline := time.Now().UTC().Add(48 * time.Hour).Unix()

	result, err := f.svc.Create(context.Background(), claimable.CreateInput{
		Asset:  "XLM",
		Amount: decimal.RequireFromString("25"),
		Claimants: []domain.Claimant{{
			Account:   f.claimant.Address(),
			Predicate: &domain.ClaimPredicate{Type: domain.PredicateBeforeAbsoluteTime, Timestamp: deadline},
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(f.stellar.submitted) != 1 {
		t.Fatalf("expected exactly one submission, got %d", len(f.stellar.submitted))
	}
	ops := f.stellar.submitted[0].Operations()
	if len(ops) != 1 {
		t.Fatalf("an unsponsored creation must be a single operation, got %d", len(ops))
	}

	create, ok := ops[0].(*txnbuild.CreateClaimableBalance)
	if !ok {
		t.Fatalf("expected a CreateClaimableBalance, got %T", ops[0])
	}
	if create.Amount != "25.0000000" {
		t.Errorf("expected amount 25.0000000, got %q", create.Amount)
	}
	if len(create.Destinations) != 1 || create.Destinations[0].Destination != f.claimant.Address() {
		t.Fatalf("unexpected claimants: %+v", create.Destinations)
	}
	if create.Destinations[0].Predicate.Type != xdr.ClaimPredicateTypeClaimPredicateBeforeAbsoluteTime {
		t.Errorf("expected the before_absolute_time predicate, got %v", create.Destinations[0].Predicate.Type)
	}

	if result.Sponsored {
		t.Error("a creation without a sponsor must not be reported as sponsored")
	}
	if !result.ReserveRequired.Equal(decimal.RequireFromString("0.5")) {
		t.Errorf("expected a 0.5 XLM reserve, got %s", result.ReserveRequired)
	}
	if result.ExpiresAt == nil || result.ExpiresAt.Unix() != deadline {
		t.Errorf("expected the expiry to be derived from the predicate, got %v", result.ExpiresAt)
	}

	stored, ok := f.repo.balances[result.BalanceID]
	if !ok {
		t.Fatalf("expected the balance %s to be stored", result.BalanceID)
	}
	if stored.Status != domain.ClaimableBalanceStatusPending {
		t.Errorf("expected pending, got %s", stored.Status)
	}
	if stored.Asset != "XLM" || !stored.Amount.Equal(decimal.RequireFromString("25")) {
		t.Errorf("unexpected stored balance: %+v", stored)
	}
	if stored.ExpiresAt == nil || stored.ExpiresAt.Unix() != deadline {
		t.Errorf("expected the derived expiry to be persisted, got %v", stored.ExpiresAt)
	}

	if len(f.webhooks.events) != 1 || f.webhooks.events[0] != domain.EventClaimableBalanceCreated {
		t.Errorf("expected a claimable_balance.created webhook, got %v", f.webhooks.events)
	}
}

func TestCreateSponsoredWrapsWithSponsoringOperations(t *testing.T) {
	f := newFixture(t)

	result, err := f.svc.Create(context.Background(), claimable.CreateInput{
		Asset:          "XLM",
		Amount:         decimal.RequireFromString("5"),
		SponsorAccount: f.sponsor.Address(),
		Claimants: []domain.Claimant{{
			Account:   f.claimant.Address(),
			Predicate: &domain.ClaimPredicate{Type: domain.PredicateUnconditional},
		}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.Sponsored {
		t.Fatal("expected the creation to be reported as sponsored")
	}

	if len(f.stellar.submitted) != 1 {
		t.Fatalf("expected exactly one submission, got %d", len(f.stellar.submitted))
	}
	tx := f.stellar.submitted[0]
	ops := tx.Operations()

	// Begin sponsoring must open the transaction and end sponsoring must close
	// it, with the sponsored creation wrapped in between. Stellar rejects any
	// other ordering, so the positions here are not cosmetic.
	if len(ops) != 3 {
		t.Fatalf("expected 3 operations (begin, create, end), got %d", len(ops))
	}

	begin, ok := ops[0].(*txnbuild.BeginSponsoringFutureReserves)
	if !ok {
		t.Fatalf("expected the transaction to open with BeginSponsoringFutureReserves, got %T", ops[0])
	}
	if begin.SponsoredID != f.source.Address() {
		t.Errorf("expected the creator to be the sponsored account, got %q", begin.SponsoredID)
	}
	if begin.SourceAccount != f.sponsor.Address() {
		t.Errorf("expected the sponsor to source the begin operation, got %q", begin.SourceAccount)
	}

	create, ok := ops[1].(*txnbuild.CreateClaimableBalance)
	if !ok {
		t.Fatalf("expected the create operation in the middle, got %T", ops[1])
	}
	if create.SourceAccount != "" {
		t.Errorf("expected the create operation to inherit the creator as its source, got %q", create.SourceAccount)
	}

	end, ok := ops[2].(*txnbuild.EndSponsoringFutureReserves)
	if !ok {
		t.Fatalf("expected the transaction to close with EndSponsoringFutureReserves, got %T", ops[2])
	}
	if end.SourceAccount != f.sponsor.Address() {
		t.Errorf("expected the sponsor to source the end operation, got %q", end.SourceAccount)
	}

	// Both parties have to sign: the creator funds the balance and the sponsor
	// commits its reserve.
	if len(f.signer.secrets) != 2 {
		t.Fatalf("expected the creator and the sponsor to sign, got %d signature(s)", len(f.signer.secrets))
	}

	// The balance ID must be derived from the *create* operation's position, not
	// from the wrapping begin operation — off by one here would store an ID that
	// never matches the on-chain balance.
	want, err := claimable.BalanceID(f.source.Address(), tx.SequenceNumber(), 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.BalanceID != want {
		t.Errorf("expected balance id %s (operation index 1), got %s", want, result.BalanceID)
	}

	stored := f.repo.balances[result.BalanceID]
	if stored == nil {
		t.Fatalf("expected the sponsored balance %s to be stored", result.BalanceID)
	}
	if stored.Sponsor != f.sponsor.Address() {
		t.Errorf("expected the sponsor to be recorded, got %q", stored.Sponsor)
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	t.Run("no claimants", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.svc.Create(context.Background(), claimable.CreateInput{
			Asset:  "XLM",
			Amount: decimal.RequireFromString("1"),
		})
		if !errors.Is(err, domain.ErrNoClaimants) {
			t.Fatalf("expected ErrNoClaimants, got %v", err)
		}
	})

	t.Run("non-positive amount", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.svc.Create(context.Background(), claimable.CreateInput{
			Asset:     "XLM",
			Amount:    decimal.Zero,
			Claimants: []domain.Claimant{{Account: f.claimant.Address()}},
		})
		if !errors.Is(err, domain.ErrInvalidAmount) {
			t.Fatalf("expected ErrInvalidAmount, got %v", err)
		}
	})

	t.Run("unsupported asset", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.svc.Create(context.Background(), claimable.CreateInput{
			Asset:     "SHIB",
			Amount:    decimal.RequireFromString("1"),
			Claimants: []domain.Claimant{{Account: f.claimant.Address()}},
		})
		if !errors.Is(err, domain.ErrInvalidAsset) {
			t.Fatalf("expected ErrInvalidAsset, got %v", err)
		}
	})

	t.Run("sponsor not custodied", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.svc.Create(context.Background(), claimable.CreateInput{
			Asset:          "XLM",
			Amount:         decimal.RequireFromString("1"),
			SponsorAccount: keypair.MustRandom().Address(),
			Claimants:      []domain.Claimant{{Account: f.claimant.Address()}},
		})
		if !errors.Is(err, domain.ErrSponsorNotCustodied) {
			t.Fatalf("expected ErrSponsorNotCustodied, got %v", err)
		}
		if len(f.stellar.submitted) != 0 {
			t.Error("an unusable sponsor must be rejected before submission")
		}
	})

	t.Run("invalid predicate", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.svc.Create(context.Background(), claimable.CreateInput{
			Asset:  "XLM",
			Amount: decimal.RequireFromString("1"),
			Claimants: []domain.Claimant{{
				Account:   f.claimant.Address(),
				Predicate: &domain.ClaimPredicate{Type: domain.PredicateNot},
			}},
		})
		if !errors.Is(err, domain.ErrInvalidPredicate) {
			t.Fatalf("expected ErrInvalidPredicate, got %v", err)
		}
	})
}

// ---------------------------------------------------------------------- claim

func TestClaimRefusesUnsatisfiablePredicateWithoutSubmitting(t *testing.T) {
	f := newFixture(t)
	// A balance whose only claimant's window has already closed.
	f.seed("balance-past", future(), false, domain.Claimant{
		Account:   f.claimant.Address(),
		Predicate: &domain.ClaimPredicate{Type: domain.PredicateBeforeAbsoluteTime, Timestamp: time.Now().UTC().Add(-time.Hour).Unix()},
	})

	_, err := f.svc.Claim(context.Background(), f.balanceID("balance-past"), f.claimant.Address())
	if !errors.Is(err, domain.ErrPredicateNotSatisfiable) {
		t.Fatalf("expected ErrPredicateNotSatisfiable, got %v", err)
	}

	// The whole point of the pre-flight check: no Horizon round trip happens.
	if len(f.stellar.submitted) != 0 {
		t.Fatalf("expected no submission attempt, got %d", len(f.stellar.submitted))
	}

	stored := f.repo.balances["balance-past"]
	if stored.Status != domain.ClaimableBalanceStatusPending {
		t.Errorf("expected the balance to stay pending, got %s", stored.Status)
	}
}

func TestClaimSubmitsAndRecordsClaimant(t *testing.T) {
	f := newFixture(t)
	f.seed("balance-open", future(), false, domain.Claimant{
		Account:   f.claimant.Address(),
		Predicate: &domain.ClaimPredicate{Type: domain.PredicateUnconditional},
	})

	result, err := f.svc.Claim(context.Background(), f.balanceID("balance-open"), f.claimant.Address())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ClaimedBy != f.claimant.Address() {
		t.Errorf("expected the claimant to be recorded, got %q", result.ClaimedBy)
	}
	if result.TxHash == "" {
		t.Error("expected a transaction hash")
	}

	if len(f.stellar.submitted) != 1 {
		t.Fatalf("expected one submission, got %d", len(f.stellar.submitted))
	}
	ops := f.stellar.submitted[0].Operations()
	if len(ops) != 1 {
		t.Fatalf("expected a single claim operation, got %d", len(ops))
	}
	claim, ok := ops[0].(*txnbuild.ClaimClaimableBalance)
	if !ok {
		t.Fatalf("expected a ClaimClaimableBalance, got %T", ops[0])
	}
	if claim.BalanceID != f.balanceID("balance-open") {
		t.Errorf("expected the balance id to be claimed, got %q", claim.BalanceID)
	}

	stored := f.repo.balances["balance-open"]
	if stored.Status != domain.ClaimableBalanceStatusClaimed {
		t.Errorf("expected claimed, got %s", stored.Status)
	}
	if stored.ClaimedBy != f.claimant.Address() || stored.ClaimedAt == nil {
		t.Errorf("expected the claim to be recorded: %+v", stored)
	}

	if len(f.webhooks.events) != 1 || f.webhooks.events[0] != domain.EventClaimableBalanceClaimed {
		t.Errorf("expected a claimable_balance.claimed webhook, got %v", f.webhooks.events)
	}
}

func TestClaimFallsBackToSoleCustodiedClaimant(t *testing.T) {
	f := newFixture(t)
	f.seed("balance-solo", future(), false,
		domain.Claimant{Account: keypair.MustRandom().Address()},
		domain.Claimant{Account: f.claimant.Address()},
	)

	// No claimant named, but exactly one of the two is a Fluxa wallet, so it is
	// unambiguous.
	result, err := f.svc.Claim(context.Background(), f.balanceID("balance-solo"), "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ClaimedBy != f.claimant.Address() {
		t.Errorf("expected the custodied claimant, got %q", result.ClaimedBy)
	}
}

func TestClaimRejectsUnusableRequests(t *testing.T) {
	t.Run("already claimed", func(t *testing.T) {
		f := newFixture(t)
		f.seed("balance-done", future(), false, domain.Claimant{Account: f.claimant.Address()})
		f.repo.balances["balance-done"].Status = domain.ClaimableBalanceStatusClaimed

		_, err := f.svc.Claim(context.Background(), f.balanceID("balance-done"), f.claimant.Address())
		if !errors.Is(err, domain.ErrClaimableBalanceNotPending) {
			t.Fatalf("expected ErrClaimableBalanceNotPending, got %v", err)
		}
	})

	t.Run("unknown balance", func(t *testing.T) {
		f := newFixture(t)
		_, err := f.svc.Claim(context.Background(), "nope", f.claimant.Address())
		if !errors.Is(err, domain.ErrClaimableBalanceNotFound) {
			t.Fatalf("expected ErrClaimableBalanceNotFound, got %v", err)
		}
	})

	t.Run("account is not a claimant", func(t *testing.T) {
		f := newFixture(t)
		f.seed("balance-other", future(), false, domain.Claimant{Account: keypair.MustRandom().Address()})

		_, err := f.svc.Claim(context.Background(), f.balanceID("balance-other"), f.claimant.Address())
		if !errors.Is(err, domain.ErrClaimantNotFound) {
			t.Fatalf("expected ErrClaimantNotFound, got %v", err)
		}
	})

	t.Run("claimant not custodied", func(t *testing.T) {
		f := newFixture(t)
		external := keypair.MustRandom().Address()
		f.seed("balance-external", future(), false, domain.Claimant{Account: external})

		_, err := f.svc.Claim(context.Background(), f.balanceID("balance-external"), external)
		if !errors.Is(err, domain.ErrClaimantNotCustodied) {
			t.Fatalf("expected ErrClaimantNotCustodied, got %v", err)
		}
		if len(f.stellar.submitted) != 0 {
			t.Error("expected no submission for an unmanageable claimant")
		}
	})

	t.Run("balance past its expiry", func(t *testing.T) {
		f := newFixture(t)
		f.seed("balance-expired", past(), false, domain.Claimant{Account: f.claimant.Address()})

		_, err := f.svc.Claim(context.Background(), f.balanceID("balance-expired"), f.claimant.Address())
		if !errors.Is(err, domain.ErrClaimableBalanceNotPending) {
			t.Fatalf("expected ErrClaimableBalanceNotPending, got %v", err)
		}
	})
}

// ---------------------------------------------------------------------- list

func TestListForwardsFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	pending := domain.Claimant{Account: f.claimant.Address()}
	f.seed("a-pending", future(), false, pending)
	f.seed("b-pending", future(), false, pending)
	f.seed("c-claimed", future(), false, pending)
	f.repo.balances["c-claimed"].Status = domain.ClaimableBalanceStatusClaimed

	got, err := f.svc.List(ctx, claimable.Filter{Status: domain.ClaimableBalanceStatusPending})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected only the 2 pending balances, got %d", len(got))
	}
	for _, b := range got {
		if b.Status != domain.ClaimableBalanceStatusPending {
			t.Errorf("status=pending leaked a %s balance", b.Status)
		}
	}

	byClaimant, err := f.svc.List(ctx, claimable.Filter{Claimant: f.claimant.Address()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(byClaimant) != 3 {
		t.Errorf("expected all 3 balances for the claimant, got %d", len(byClaimant))
	}

	limited, err := f.svc.List(ctx, claimable.Filter{Limit: 1})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("expected the limit to apply, got %d rows", len(limited))
	}
}

func TestListDefaultLimit(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 25; i++ {
		f.seed(fmt.Sprintf("balance-%02d", i), future(), false, domain.Claimant{Account: "GX"})
	}

	got, err := f.svc.List(context.Background(), claimable.Filter{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) > 20 {
		t.Errorf("expected the default limit of 20, got %d", len(got))
	}
}

// ----------------------------------------------------------------------- get

func TestGetDecoratesWithLiveStatus(t *testing.T) {
	f := newFixture(t)
	id := f.seed("balance-live", future(), false, domain.Claimant{Account: f.claimant.Address()})

	live, err := f.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if live.Balance.ID != id {
		t.Errorf("expected the stored balance %s, got %q", id, live.Balance.ID)
	}
	if !live.Claimable {
		t.Error("expected the unconditional claimant to be claimable now")
	}
	if live.OnChain != nil {
		t.Error("expected no on-chain record when Horizon does not have the balance")
	}

	// With the balance reported by Horizon, the response carries both views.
	stored := f.repo.balances["balance-live"]
	horizonRecord := horizon.ClaimableBalance{
		BalanceID: stored.ID,
		Asset:     stored.Asset,
		Amount:    stored.Amount.StringFixed(7),
	}
	f.claimables.byID[stored.ID] = horizonRecord

	live, err = f.svc.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if live.OnChain == nil || live.OnChain.BalanceID != stored.ID {
		t.Fatalf("expected the Horizon view, got %+v", live.OnChain)
	}
}

// -------------------------------------------------------------------- expiry

func TestProcessExpiredMarksUnclaimedBalanceExpired(t *testing.T) {
	f := newFixture(t)
	f.seed("balance-old", past(), false, domain.Claimant{
		Account:   f.claimant.Address(),
		Predicate: &domain.ClaimPredicate{Type: domain.PredicateBeforeAbsoluteTime, Timestamp: time.Now().UTC().Add(-time.Minute).Unix()},
	})
	f.seed("balance-live", future(), false, domain.Claimant{Account: f.claimant.Address()})

	report, err := f.svc.ProcessExpired(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if report.Scanned != 1 || report.Expired != 1 || report.Revoked != 0 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if got := f.repo.balances["balance-old"].Status; got != domain.ClaimableBalanceStatusExpired {
		t.Errorf("expected the expired balance to be marked expired, got %s", got)
	}
	if got := f.repo.balances["balance-live"].Status; got != domain.ClaimableBalanceStatusPending {
		t.Errorf("expected the live balance to be untouched, got %s", got)
	}
	if len(f.webhooks.events) != 1 || f.webhooks.events[0] != domain.EventClaimableBalanceExpired {
		t.Errorf("expected a claimable_balance.expired webhook, got %v", f.webhooks.events)
	}
}

func TestProcessExpiredRevokesBackToOrg(t *testing.T) {
	f := newFixture(t)
	// The org holds the only claimant whose predicate opens up once the
	// balance has expired — the standard revoke-on-expiry arrangement.
	f.seed("balance-revocable", past(), true,
		domain.Claimant{
			Account:   f.source.Address(),
			Predicate: &domain.ClaimPredicate{Type: domain.PredicateAfterAbsoluteTime, Timestamp: time.Now().UTC().Add(-time.Minute).Unix()},
		},
		domain.Claimant{
			Account:   keypair.MustRandom().Address(),
			Predicate: &domain.ClaimPredicate{Type: domain.PredicateBeforeAbsoluteTime, Timestamp: time.Now().UTC().Add(-time.Minute).Unix()},
		},
	)

	report, err := f.svc.ProcessExpired(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Revoked != 1 || report.Expired != 0 {
		t.Fatalf("expected one revocation, got %+v", report)
	}

	if got := f.repo.balances["balance-revocable"].Status; got != domain.ClaimableBalanceStatusRevoked {
		t.Errorf("expected the balance to be revoked, got %s", got)
	}
	if len(f.stellar.submitted) != 1 {
		t.Fatalf("expected one claim submission, got %d", len(f.stellar.submitted))
	}
	// The claim has to come from the org's own account.
	if src := f.stellar.submitted[0].SourceAccount().AccountID; src != f.source.Address() {
		t.Errorf("expected the org wallet to claim it back, got %q", src)
	}
	if len(f.webhooks.events) != 1 || f.webhooks.events[0] != domain.EventClaimableBalanceRevoked {
		t.Errorf("expected a claimable_balance.revoked webhook, got %v", f.webhooks.events)
	}
}

func TestProcessExpiredFallsBackToExpiredWhenRevokeImpossible(t *testing.T) {
	f := newFixture(t)
	// revoke_on_expiry is set, but the only claimant is not a Fluxa wallet, so
	// there is nobody to claim it back with.
	f.seed("balance-stranded", past(), true,
		domain.Claimant{Account: keypair.MustRandom().Address()},
	)

	report, err := f.svc.ProcessExpired(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if report.Revoked != 0 || report.Expired != 1 {
		t.Fatalf("expected the balance to fall back to expired, got %+v", report)
	}
	if len(f.stellar.submitted) != 0 {
		t.Error("expected no submission when no claimant can revoke")
	}
	if got := f.repo.balances["balance-stranded"].Status; got != domain.ClaimableBalanceStatusExpired {
		t.Errorf("expected expired, got %s", got)
	}
}

func TestProcessExpiredIsIdempotent(t *testing.T) {
	f := newFixture(t)
	f.seed("balance-old", past(), false, domain.Claimant{Account: f.claimant.Address()})

	first, err := f.svc.ProcessExpired(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := f.svc.ProcessExpired(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if first.Expired != 1 {
		t.Errorf("expected the first pass to expire one balance, got %+v", first)
	}
	if second.Scanned != 0 {
		t.Errorf("expected the second pass to find nothing, got %+v", second)
	}
	if len(f.webhooks.events) != 1 {
		t.Errorf("expected exactly one webhook across both passes, got %v", f.webhooks.events)
	}
}
