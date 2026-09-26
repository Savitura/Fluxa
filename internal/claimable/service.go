package claimable

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/txnbuild"
)

// EntryReserve is Stellar's per-subentry minimum balance requirement. A
// claimable balance is one subentry, so creating one locks 0.5 XLM — paid by
// the creator, or by the sponsor when the creation is sponsored.
var EntryReserve = decimal.RequireFromString("0.5")

// SourceWallet is a Fluxa-custodied Stellar account usable as the funding or
// sponsor account for a claimable balance.
type SourceWallet struct {
	ID string
	// PublicKey is the Stellar account ID (G...).
	PublicKey string
	// EncryptedSecret is the hex-encoded AES-GCM envelope of the account seed,
	// exactly as stored on domain.Wallet.
	EncryptedSecret string
}

// WalletResolver looks up Fluxa-custodied wallets. It is declared here, and
// exchanges only plain values, so this package does not depend on
// internal/wallet.
type WalletResolver interface {
	GetByID(ctx context.Context, walletID string) (*SourceWallet, error)
	// GetByPublicKey resolves a Fluxa-custodied wallet from its Stellar account
	// ID, which is how a claimant or sponsor named by public key is turned into
	// something Fluxa can sign for.
	GetByPublicKey(ctx context.Context, publicKey string) (*SourceWallet, error)
}

// WebhookDispatcher is the narrow view of internal/webhook this service needs.
// Declared here so the claimable package stays independent of the webhook
// package; webhook.Dispatcher satisfies it.
type WebhookDispatcher interface {
	Dispatch(ctx context.Context, eventType string, payload interface{}) error
}

// CreateInput describes a new claimable balance.
type CreateInput struct {
	Asset     string
	Amount    decimal.Decimal
	Claimants []domain.Claimant

	// SourceWalletID funds the balance. When empty the service falls back to
	// its configured default source wallet.
	SourceWalletID string

	// SponsorAccount is an optional Stellar account that pays the entry reserve.
	// It must be a wallet Fluxa custodies, because sponsoring requires the
	// sponsor's signature. When set, the creation is wrapped in
	// Begin/EndSponsoringFutureReserves.
	SponsorAccount string

	// RevokeOnExpiry claims an unclaimed balance back to a satisfiable custodied
	// claimant once it expires, instead of leaving it stranded.
	RevokeOnExpiry bool

	// ExpiresAt overrides the expiry derived from the claimants' absolute
	// deadlines.
	ExpiresAt *time.Time
}

// CreateResult is what the API returns from POST /v1/claimable-balances.
type CreateResult struct {
	BalanceID string
	// ReserveRequired is the XLM the entry locks up.
	ReserveRequired decimal.Decimal
	// Sponsored reports whether a sponsor paid that reserve.
	Sponsored bool
	TxHash    string
	ExpiresAt *time.Time
}

// LiveStatus is a stored balance plus its current state on Stellar.
type LiveStatus struct {
	Balance *domain.ClaimableBalance
	// OnChain is the Horizon view of the balance, nil when Horizon has no such
	// balance (already consumed, or unknown to this network).
	OnChain *horizon.ClaimableBalance
	// Claimable reports whether a claimant could claim right now.
	Claimable bool
}

// ClaimResult is the outcome of a successful claim.
type ClaimResult struct {
	BalanceID string
	ClaimedBy string
	Amount    decimal.Decimal
	TxHash    string
}

// ExpiryReport summarises one expiry-tracker pass.
type ExpiryReport struct {
	Scanned int
	Expired int
	Revoked int
}

// Service is Fluxa's claimable balance management layer.
type Service interface {
	Create(ctx context.Context, in CreateInput) (*CreateResult, error)
	List(ctx context.Context, f Filter) ([]*domain.ClaimableBalance, error)
	Get(ctx context.Context, id string) (*LiveStatus, error)
	Claim(ctx context.Context, id, claimantAccount string) (*ClaimResult, error)
	// ProcessExpired is the background expiry tracker's entry point.
	ProcessExpired(ctx context.Context) (*ExpiryReport, error)
}

type service struct {
	repo         Repository
	stellar      stellar.Client
	claimables   stellar.ClaimableBalanceClient
	signer       stellar.Signer
	wallets      WalletResolver
	webhooks     WebhookDispatcher
	assetIssuers map[string]string
	// defaultSourceWalletID funds a creation whose request did not name a
	// source wallet.
	defaultSourceWalletID string
}

func NewService(
	repo Repository,
	stellarClient stellar.Client,
	claimableClient stellar.ClaimableBalanceClient,
	signer stellar.Signer,
	wallets WalletResolver,
	webhooks WebhookDispatcher,
	defaultSourceWalletID string,
	assetIssuers map[string]string,
) Service {
	return &service{
		repo:                  repo,
		stellar:               stellarClient,
		claimables:            claimableClient,
		signer:                signer,
		wallets:               wallets,
		webhooks:              webhooks,
		defaultSourceWalletID: defaultSourceWalletID,
		assetIssuers:          assetIssuers,
	}
}

func (s *service) Create(ctx context.Context, in CreateInput) (*CreateResult, error) {
	if len(in.Claimants) == 0 {
		return nil, domain.ErrNoClaimants
	}
	if !in.Amount.GreaterThan(decimal.Zero) {
		return nil, domain.ErrInvalidAmount
	}

	srcWallet, err := s.resolveSourceWallet(ctx, in.SourceWalletID)
	if err != nil {
		return nil, err
	}

	// The sponsor has to be signable, so a sponsor account that Fluxa does not
	// custody is rejected up front rather than at signature time.
	var sponsorWallet *SourceWallet
	if in.SponsorAccount != "" {
		sponsorWallet, err = s.wallets.GetByPublicKey(ctx, in.SponsorAccount)
		if err != nil || sponsorWallet == nil {
			return nil, fmt.Errorf("%w: %s", domain.ErrSponsorNotCustodied, in.SponsorAccount)
		}
	}

	asset, err := s.buildAsset(in.Asset)
	if err != nil {
		return nil, err
	}

	destinations := make([]txnbuild.Claimant, 0, len(in.Claimants))
	for _, c := range in.Claimants {
		predicate, err := PredicateToXDR(c.Predicate)
		if err != nil {
			return nil, err
		}
		destinations = append(destinations, txnbuild.Claimant{Destination: c.Account, Predicate: predicate})
	}

	createOp := &txnbuild.CreateClaimableBalance{
		Amount:       in.Amount.StringFixed(7),
		Asset:        asset,
		Destinations: destinations,
	}

	ops := []txnbuild.Operation{createOp}
	createOpIndex := uint32(0)
	if sponsorWallet != nil {
		// Begin sponsoring must open the transaction and end sponsoring must
		// close it, with the sponsored work in between — so the pair wraps the
		// create operation rather than sitting beside it. The creator is the
		// sponsored account: it is the one whose balance would otherwise lock
		// the entry reserve.
		ops = []txnbuild.Operation{
			&txnbuild.BeginSponsoringFutureReserves{
				SponsoredID:   srcWallet.PublicKey,
				SourceAccount: sponsorWallet.PublicKey,
			},
			createOp,
			&txnbuild.EndSponsoringFutureReserves{SourceAccount: sponsorWallet.PublicKey},
		}
		createOpIndex = 1
	}

	srcAccount, err := s.stellar.LoadAccount(srcWallet.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("load source wallet account: %w", err)
	}

	stellarTx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &srcAccount,
		IncrementSequenceNum: true,
		Operations:           ops,
		BaseFee:              txnbuild.MinBaseFee * int64(len(ops)),
		Preconditions: txnbuild.Preconditions{
			TimeBounds: txnbuild.NewTimeout(30),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("build create claimable balance transaction: %w", err)
	}

	// Both the creator and the sponsor must sign a sponsored creation; an
	// unsponsored one is signed by the creator alone.
	signers := []*SourceWallet{srcWallet}
	if sponsorWallet != nil {
		signers = append(signers, sponsorWallet)
	}
	stellarTx, err = s.sign(stellarTx, signers...)
	if err != nil {
		return nil, err
	}

	balanceID, err := BalanceID(stellarTx.SourceAccount().AccountID, stellarTx.SequenceNumber(), createOpIndex)
	if err != nil {
		return nil, err
	}

	resp, err := s.stellar.SubmitTransaction(stellarTx)
	if err != nil {
		return nil, fmt.Errorf("submit create claimable balance: %w", err)
	}

	now := time.Now().UTC()
	expiresAt := in.ExpiresAt
	if expiresAt == nil {
		expiresAt = EarliestDeadline(in.Claimants)
	}

	record := &domain.ClaimableBalance{
		ID:             balanceID,
		TenantID:       tenantPtr(ctx),
		Asset:          in.Asset,
		Amount:         in.Amount,
		Claimants:      in.Claimants,
		Sponsor:        in.SponsorAccount,
		Status:         domain.ClaimableBalanceStatusPending,
		RevokeOnExpiry: in.RevokeOnExpiry,
		CreatedAt:      now,
		ExpiresAt:      expiresAt,
	}

	if err := s.repo.Create(ctx, record); err != nil {
		// The balance exists on chain; the ID is logged so the row can be
		// reconstructed rather than the funds being orphaned silently.
		log.Error().Err(err).Str("balance_id", balanceID).Str("tx_hash", resp.Hash).
			Msg("claimable: created on chain but failed to persist")
		return nil, fmt.Errorf("persist claimable balance %s: %w", balanceID, err)
	}

	s.dispatch(ctx, domain.EventClaimableBalanceCreated, map[string]interface{}{
		"balance_id": balanceID,
		"asset":      in.Asset,
		"amount":     in.Amount.StringFixed(7),
		"sponsor":    in.SponsorAccount,
		"tx_hash":    resp.Hash,
		"expires_at": formatTime(expiresAt),
	})

	return &CreateResult{
		BalanceID:       balanceID,
		ReserveRequired: EntryReserve,
		Sponsored:       sponsorWallet != nil,
		TxHash:          resp.Hash,
		ExpiresAt:       expiresAt,
	}, nil
}

func (s *service) List(ctx context.Context, f Filter) ([]*domain.ClaimableBalance, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 20
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return s.repo.List(ctx, f)
}

func (s *service) Get(ctx context.Context, id string) (*LiveStatus, error) {
	balance, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	live := &LiveStatus{Balance: balance}

	if s.claimables != nil {
		onChain, err := s.claimables.ClaimableBalance(id)
		switch {
		case err == nil:
			live.OnChain = &onChain
		case isNotFound(err):
			// Consumed or not on this network — the local row still stands.
		default:
			log.Warn().Err(err).Str("balance_id", id).
				Msg("claimable: could not read live status from Horizon")
		}
	}

	now := time.Now().UTC()
	if balance.Status == domain.ClaimableBalanceStatusPending && !expiredAt(balance, now) {
		live.Claimable = AnyClaimantSatisfiable(balance.Claimants, now, balance.CreatedAt)
	}

	return live, nil
}

func (s *service) Claim(ctx context.Context, id, claimantAccount string) (*ClaimResult, error) {
	balance, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if balance.Status != domain.ClaimableBalanceStatusPending {
		return nil, domain.ErrClaimableBalanceNotPending
	}

	claimant, err := s.resolveClaimant(ctx, balance, claimantAccount)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()

	// Pre-flight the predicate locally and refuse before touching Horizon: a
	// claim that cannot succeed would still burn a round trip, and on some
	// networks a fee.
	if !Satisfiable(claimant.Predicate, now, balance.CreatedAt) {
		return nil, domain.ErrPredicateNotSatisfiable
	}
	if expiredAt(balance, now) {
		return nil, domain.ErrClaimableBalanceNotPending
	}

	wallet, err := s.wallets.GetByPublicKey(ctx, claimant.Account)
	if err != nil || wallet == nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrClaimantNotCustodied, claimant.Account)
	}

	txHash, err := s.submitClaim(ctx, balance.ID, wallet)
	if err != nil {
		return nil, err
	}

	if err := s.repo.MarkClaimed(ctx, balance.ID, claimant.Account, now); err != nil {
		log.Error().Err(err).Str("balance_id", balance.ID).Str("tx_hash", txHash).
			Msg("claimable: claim landed on chain but failed to persist")
		return nil, fmt.Errorf("record claim for %s: %w", balance.ID, err)
	}

	s.dispatch(ctx, domain.EventClaimableBalanceClaimed, map[string]interface{}{
		"balance_id": balance.ID,
		"asset":      balance.Asset,
		"amount":     balance.Amount.StringFixed(7),
		"claimed_by": claimant.Account,
		"tx_hash":    txHash,
	})

	return &ClaimResult{
		BalanceID: balance.ID,
		ClaimedBy: claimant.Account,
		Amount:    balance.Amount,
		TxHash:    txHash,
	}, nil
}

// ProcessExpired implements the every-5-minutes tracker: mark pending balances
// past their expiry, and claim the ones flagged revoke_on_expiry back to a
// custodied claimant so the funds are not stranded on the ledger.
func (s *service) ProcessExpired(ctx context.Context) (*ExpiryReport, error) {
	now := time.Now().UTC()

	due, err := s.repo.ListExpiredPending(ctx, now)
	if err != nil {
		return nil, fmt.Errorf("list expired claimable balances: %w", err)
	}

	report := &ExpiryReport{Scanned: len(due)}

	for _, balance := range due {
		if balance.RevokeOnExpiry {
			if revoked, err := s.revoke(ctx, balance, now); revoked {
				report.Revoked++
				continue
			} else if err != nil {
				log.Error().Err(err).Str("balance_id", balance.ID).
					Msg("claimable: revoke on expiry failed")
			}
			// Revocation could not be performed (no satisfiable custodied
			// claimant, or Horizon refused). Fall through and mark the balance
			// expired: leaving it pending would make the tracker retry forever.
		}

		if err := s.repo.MarkExpired(ctx, balance.ID, now); err != nil {
			log.Error().Err(err).Str("balance_id", balance.ID).
				Msg("claimable: failed to mark balance expired")
			continue
		}
		report.Expired++

		s.dispatch(ctx, domain.EventClaimableBalanceExpired, map[string]interface{}{
			"balance_id": balance.ID,
			"asset":      balance.Asset,
			"amount":     balance.Amount.StringFixed(7),
			"expired_at": now.Format(time.RFC3339),
		})
	}

	return report, nil
}

// revoke claims an expired balance back on behalf of the first claimant that
// is both currently satisfiable and custodied by Fluxa. Report whether the
// balance was revoked, plus any error worth logging.
func (s *service) revoke(ctx context.Context, balance *domain.ClaimableBalance, now time.Time) (bool, error) {
	for i := range balance.Claimants {
		c := balance.Claimants[i]
		if !Satisfiable(c.Predicate, now, balance.CreatedAt) {
			continue
		}
		wallet, err := s.wallets.GetByPublicKey(ctx, c.Account)
		if err != nil || wallet == nil {
			continue
		}

		txHash, err := s.submitClaim(ctx, balance.ID, wallet)
		if err != nil {
			return false, err
		}
		if err := s.repo.MarkRevoked(ctx, balance.ID, now); err != nil {
			return false, fmt.Errorf("record revocation: %w", err)
		}

		s.dispatch(ctx, domain.EventClaimableBalanceRevoked, map[string]interface{}{
			"balance_id": balance.ID,
			"asset":      balance.Asset,
			"amount":     balance.Amount.StringFixed(7),
			"revoked_by": c.Account,
			"tx_hash":    txHash,
		})
		return true, nil
	}
	return false, nil
}

// submitClaim builds, signs and submits a ClaimClaimableBalance from wallet.
func (s *service) submitClaim(ctx context.Context, balanceID string, wallet *SourceWallet) (string, error) {
	account, err := s.stellar.LoadAccount(wallet.PublicKey)
	if err != nil {
		return "", fmt.Errorf("load claimant account: %w", err)
	}

	stellarTx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &account,
		IncrementSequenceNum: true,
		Operations: []txnbuild.Operation{
			&txnbuild.ClaimClaimableBalance{BalanceID: balanceID},
		},
		BaseFee: txnbuild.MinBaseFee,
		Preconditions: txnbuild.Preconditions{
			TimeBounds: txnbuild.NewTimeout(30),
		},
	})
	if err != nil {
		return "", fmt.Errorf("build claim transaction: %w", err)
	}

	stellarTx, err = s.sign(stellarTx, wallet)
	if err != nil {
		return "", err
	}

	resp, err := s.stellar.SubmitTransaction(stellarTx)
	if err != nil {
		return "", fmt.Errorf("submit claim transaction: %w", err)
	}
	return resp.Hash, nil
}

func (s *service) resolveSourceWallet(ctx context.Context, walletID string) (*SourceWallet, error) {
	if walletID == "" {
		walletID = s.defaultSourceWalletID
	}
	if walletID == "" {
		return nil, domain.ErrSourceWalletRequired
	}
	wallet, err := s.wallets.GetByID(ctx, walletID)
	if err != nil || wallet == nil {
		return nil, fmt.Errorf("%w: %s", domain.ErrWalletNotFound, walletID)
	}
	return wallet, nil
}

// resolveClaimant picks the claimant to claim for. An explicit account must be
// one of the balance's claimants; when omitted and the balance has exactly one
// custodied claimant, that one is used.
func (s *service) resolveClaimant(ctx context.Context, balance *domain.ClaimableBalance, account string) (domain.Claimant, error) {
	if account != "" {
		for i := range balance.Claimants {
			if balance.Claimants[i].Account == account {
				return balance.Claimants[i], nil
			}
		}
		return domain.Claimant{}, domain.ErrClaimantNotFound
	}

	var custodied []domain.Claimant
	for i := range balance.Claimants {
		if _, err := s.wallets.GetByPublicKey(ctx, balance.Claimants[i].Account); err == nil {
			custodied = append(custodied, balance.Claimants[i])
		}
	}
	if len(custodied) == 1 {
		return custodied[0], nil
	}
	return domain.Claimant{}, domain.ErrClaimantNotFound
}

// sign applies each wallet's signature in turn. txnbuild accumulates signatures,
// so a sponsored creation carries both the creator's and the sponsor's.
func (s *service) sign(tx *txnbuild.Transaction, wallets ...*SourceWallet) (*txnbuild.Transaction, error) {
	for _, w := range wallets {
		secret, err := hex.DecodeString(w.EncryptedSecret)
		if err != nil {
			return nil, fmt.Errorf("decode wallet secret: %w", err)
		}
		tx, err = s.signer.Sign(tx, string(secret))
		if err != nil {
			return nil, fmt.Errorf("sign transaction: %w", err)
		}
	}
	return tx, nil
}

func (s *service) buildAsset(code string) (txnbuild.Asset, error) {
	if code == "XLM" {
		return txnbuild.NativeAsset{}, nil
	}
	issuer := s.assetIssuers[code]
	if issuer == "" {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidAsset, code)
	}
	return txnbuild.CreditAsset{Code: code, Issuer: issuer}, nil
}

func (s *service) dispatch(ctx context.Context, eventType string, payload interface{}) {
	if s.webhooks == nil {
		return
	}
	if err := s.webhooks.Dispatch(ctx, eventType, payload); err != nil {
		log.Error().Err(err).Str("event_type", eventType).Msg("claimable: webhook dispatch failed")
	}
}

func tenantPtr(ctx context.Context) *string {
	tid := tenant.IDFromContext(ctx)
	if tid == "" {
		return nil
	}
	return &tid
}

func expiredAt(b *domain.ClaimableBalance, now time.Time) bool {
	return b.ExpiresAt != nil && !now.Before(*b.ExpiresAt)
}

func formatTime(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.Format(time.RFC3339)
}

func isNotFound(err error) bool {
	return err != nil && errors.Is(err, stellar.ErrClaimableBalanceNotFound)
}
