package settlement

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	stellarnet "github.com/stellar/go/network"
	"github.com/stellar/go/txnbuild"
)

type Engine struct {
	txRepo       transfer.Repository
	walletRepo   wallet.Repository
	feeSvc       fees.Service
	stellar      stellar.Client
	signer       stellar.Signer
	network      string
	assetIssuers map[string]string
	feeWallet    string
}

func NewEngine(
	txRepo transfer.Repository,
	walletRepo wallet.Repository,
	feeSvc fees.Service,
	stellarClient stellar.Client,
	signer stellar.Signer,
	network string,
	assetIssuers map[string]string,
	feeWallet string,
) *Engine {
	return &Engine{
		txRepo:       txRepo,
		walletRepo:   walletRepo,
		feeSvc:       feeSvc,
		stellar:      stellarClient,
		signer:       signer,
		network:      network,
		assetIssuers: assetIssuers,
		feeWallet:    feeWallet,
	}
}

func (e *Engine) SubmitTransfer(ctx context.Context, txID string) error {
	tx, err := e.txRepo.GetByID(ctx, txID)
	if err != nil {
		return fmt.Errorf("load transaction: %w", err)
	}

	// Atomically claim the transaction before doing any work. This is what
	// makes concurrent workers (duplicate queue delivery, overlapping
	// retries) safe: only one caller can ever win this pending -> submitted
	// transition, so only one ever builds/signs/submits a Stellar
	// transaction for a given id. A transaction that's already submitted
	// (whether actively in-flight or awaiting reconciliation with a hash
	// on record) or in a terminal state is rejected here rather than
	// resubmitted — an in-flight/ambiguous submission is only ever
	// resolved by hash lookup (see tryResolveAmbiguous and the periodic
	// reconciliation sweep), never by blindly trying again.
	if err := e.txRepo.ClaimForSubmission(ctx, txID); err != nil {
		if errors.Is(err, domain.ErrConcurrentUpdate) {
			log.Info().Str("tx_id", txID).Str("status", string(tx.Status)).
				Msg("settlement: transaction already claimed or not eligible for submission")
			return nil
		}
		return fmt.Errorf("claim transaction for submission: %w", err)
	}

	srcWallet, err := e.walletRepo.GetByID(ctx, tx.FromWallet)
	if err != nil {
		return fmt.Errorf("load source wallet: %w", err)
	}

	srcAccount, err := e.stellar.LoadAccount(srcWallet.PublicKey)
	if err != nil {
		return fmt.Errorf("load stellar account: %w", err)
	}

	dstWallet, err := e.walletRepo.GetByID(ctx, tx.ToWallet)
	if err != nil {
		return fmt.Errorf("load destination wallet: %w", err)
	}

	txAsset, err := e.buildAsset(tx.Asset)
	if err != nil {
		return err
	}
	netAmount := tx.NetAmount()

	ops := []txnbuild.Operation{
		&txnbuild.Payment{
			Destination: dstWallet.PublicKey,
			Asset:       txAsset,
			Amount:      netAmount.StringFixed(7),
		},
	}

	if tx.Fee.GreaterThan(decimal.Zero) {
		if e.feeWallet == "" {
			return fmt.Errorf("PLATFORM_FEE_WALLET_PUBLIC_KEY is required to collect fees")
		}
		ops = append(ops, &txnbuild.Payment{
			Destination: e.feeWallet,
			Asset:       txAsset,
			Amount:      tx.Fee.StringFixed(7),
		})
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
		return fmt.Errorf("build transaction: %w", err)
	}

	encryptedSecret, err := hex.DecodeString(srcWallet.EncryptedSecret)
	if err != nil {
		return fmt.Errorf("decode encrypted secret: %w", err)
	}

	stellarTx, err = e.signer.Sign(stellarTx, string(encryptedSecret))
	if err != nil {
		return fmt.Errorf("sign transaction: %w", err)
	}

	txHash, err := stellarTx.HashHex(e.networkPassphrase())
	if err != nil {
		return fmt.Errorf("compute transaction hash: %w", err)
	}

	// Persist the hash before submitting to the network. If this process
	// crashes right after, or the submission call itself is ambiguous
	// (timeout, 5xx — we genuinely don't know whether Horizon applied it),
	// the hash is already on record so it can be looked up and resolved
	// later instead of guessed at.
	if err := e.txRepo.UpdateStatus(ctx, txID, domain.StatusSubmitted, txHash); err != nil {
		log.Error().Err(err).Str("tx_id", txID).Str("tx_hash", txHash).
			Msg("settlement: failed to record tx hash before submission")
	}

	outcome := e.submitWithRetry(ctx, stellarTx)

	switch {
	case outcome.confirmed:
		e.finalizeConfirmed(ctx, tx, txID, txHash, srcWallet, dstWallet)
		return nil

	case outcome.ambiguous:
		// The network result is unknown — do not mark this failed. Try one
		// immediate lookup of this exact hash on Horizon; if that's also
		// inconclusive, leave the transaction in `submitted` (hash already
		// recorded above) for the periodic reconciliation sweep, which
		// retries this same lookup until the outcome is known.
		if e.tryResolveAmbiguous(ctx, tx, txID, txHash, srcWallet, dstWallet) {
			return nil
		}
		log.Warn().Str("tx_id", txID).Str("tx_hash", txHash).Err(outcome.err).
			Msg("settlement: ambiguous submission outcome, leaving for reconciliation")
		return fmt.Errorf("submit to stellar: ambiguous outcome, awaiting reconciliation: %w", outcome.err)

	default:
		// Horizon definitively rejected the transaction (a non-retryable
		// error) — it was not applied, so it's safe to mark failed now.
		if err := e.txRepo.UpdateStatus(ctx, txID, domain.StatusFailed, txHash); err != nil {
			log.Error().Err(err).Str("tx_id", txID).Msg("settlement: failed to update failed status")
		}
		return fmt.Errorf("submit to stellar: %w", outcome.err)
	}
}

// finalizeConfirmed marks a transaction confirmed, refreshes cached wallet
// balances, and records any platform fee collected. Shared by the direct
// submission-success path and by tryResolveAmbiguous once a hash lookup
// confirms an initially-ambiguous submission did land on-chain.
func (e *Engine) finalizeConfirmed(ctx context.Context, tx *domain.Transaction, txID, txHash string, srcWallet, dstWallet *domain.Wallet) {
	if err := e.txRepo.UpdateStatus(ctx, txID, domain.StatusConfirmed, txHash); err != nil {
		log.Error().Err(err).Str("tx_id", txID).Str("tx_hash", txHash).Msg("failed to update confirmed status")
	}

	// Update cached balances for source and destination wallets for the transferred asset
	e.syncWalletBalances(ctx, srcWallet)
	e.syncWalletBalances(ctx, dstWallet)

	if tx.Fee.GreaterThan(decimal.Zero) {
		collection := &domain.FeeCollection{
			ID:            uuid.New().String(),
			TransactionID: txID,
			TenantID:      tx.TenantID,
			FeeAmount:     tx.Fee,
			Asset:         tx.Asset,
			FeeBps:        tx.FeeBps,
			CollectedAt:   time.Now().UTC(),
		}
		if err := e.feeSvc.RecordCollection(ctx, collection); err != nil {
			log.Error().Err(err).Str("tx_id", txID).Msg("failed to record fee collection")
		}
	}
}

// tryResolveAmbiguous looks the transaction's hash up on Horizon once, right
// after an ambiguous submission attempt, so a genuinely successful (or
// genuinely rejected) submission doesn't have to wait for the next
// reconciliation pass to be corrected. Returns false if Horizon doesn't know
// about the hash yet (or the lookup itself fails) — the transaction stays in
// `submitted` and is left for the periodic reconciliation sweep, which
// performs this exact same lookup on a schedule until it resolves.
func (e *Engine) tryResolveAmbiguous(ctx context.Context, tx *domain.Transaction, txID, txHash string, srcWallet, dstWallet *domain.Wallet) bool {
	horizonTx, err := e.stellar.TransactionDetail(txHash)
	if err != nil {
		return false
	}

	if horizonTx.Successful {
		log.Info().Str("tx_id", txID).Str("tx_hash", txHash).
			Msg("settlement: resolved ambiguous submission as confirmed via Horizon")
		e.finalizeConfirmed(ctx, tx, txID, txHash, srcWallet, dstWallet)
		return true
	}

	log.Info().Str("tx_id", txID).Str("tx_hash", txHash).
		Msg("settlement: resolved ambiguous submission as failed via Horizon")
	if err := e.txRepo.UpdateStatus(ctx, txID, domain.StatusFailed, txHash); err != nil {
		log.Error().Err(err).Str("tx_id", txID).Msg("settlement: failed to update failed status")
	}
	return true
}

func (e *Engine) buildAsset(code string) (txnbuild.Asset, error) {
	if code == "XLM" {
		return txnbuild.NativeAsset{}, nil
	}
	issuer, ok := e.assetIssuers[code]
	if !ok || issuer == "" {
		return nil, fmt.Errorf("%w: %s", domain.ErrInvalidAsset, code)
	}
	return txnbuild.CreditAsset{Code: code, Issuer: issuer}, nil
}

func (e *Engine) networkPassphrase() string {
	if e.network == "mainnet" || e.network == "public" {
		return stellarnet.PublicNetworkPassphrase
	}
	return stellarnet.TestNetworkPassphrase
}

// submitOutcome classifies the result of a submission attempt (after
// exhausting retries) into exactly one of three buckets: confirmed
// (Horizon accepted it), a definite non-retryable rejection (safe to mark
// failed immediately), or ambiguous (a retryable/network-class error on
// every attempt — Horizon's actual verdict, if any, is unknown).
type submitOutcome struct {
	hash      string
	confirmed bool
	ambiguous bool
	err       error
}

// submitWithRetry resubmits the same signed transaction envelope on
// transient errors. Reusing the identical envelope (same source account,
// same sequence number, same signature) across attempts is what makes the
// retries idempotent: Horizon either returns the original result for a
// transaction it already applied, or rejects the resubmission outright
// (e.g. the sequence number was already consumed) — neither path can ever
// produce a second on-chain payment.
func (e *Engine) submitWithRetry(ctx context.Context, tx *txnbuild.Transaction) submitOutcome {
	var lastErr error
	ambiguous := true
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return submitOutcome{ambiguous: true, err: ctx.Err()}
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}

		resp, err := e.stellar.SubmitTransaction(tx)
		if err == nil {
			return submitOutcome{hash: resp.Hash, confirmed: true}
		}

		lastErr = err
		if !isRetryable(err) {
			ambiguous = false
			break
		}
	}
	return submitOutcome{ambiguous: ambiguous, err: lastErr}
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "429") || strings.Contains(errStr, "503") || strings.Contains(errStr, "timeout")
}

func (e *Engine) syncWalletBalances(ctx context.Context, w *domain.Wallet) {
	if w == nil {
		return
	}
	if acct, err := e.stellar.LoadAccount(w.PublicKey); err == nil {
		for _, b := range acct.Balances {
			code := b.Code
			if code == "" {
				code = "XLM"
			}
			amt, err := decimal.NewFromString(b.Balance)
			if err == nil {
				_ = e.walletRepo.UpsertBalance(ctx, w.ID, code, b.Issuer, amt)
			}
		}
	}
}
