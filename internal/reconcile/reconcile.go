package reconcile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/alerting"
	"github.com/fluxa/fluxa/internal/assets"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	horizonclient "github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/protocols/horizon/operations"
)

const (
	reconcileInterval     = 1 * time.Hour
	pendingCheckThreshold = 2 * time.Minute
	stuckThreshold        = 10 * time.Minute
	maxRequeues           = 3
)

type AuditOutcome string

const (
	AuditOK       AuditOutcome = "ok"
	AuditMismatch AuditOutcome = "mismatch"
	AuditNotFound AuditOutcome = "not_found"
)

type AuditLogEntry struct {
	ID             string
	TxID           string
	StellarHash    string
	CheckedAt      time.Time
	HorizonStatus  string
	AmountVerified bool
	AssetVerified  bool
	FeeVerified    bool
	Outcome        AuditOutcome
	Details        string
}

type DailySummaryRow struct {
	Date          string `json:"date"`
	OKCount       int    `json:"ok"`
	MismatchCount int    `json:"mismatch"`
	NotFoundCount int    `json:"not_found"`
}

// ReconciliationRun records the outcome of a single reconciliation pass.
type ReconciliationRun struct {
	ID                 string
	StartedAt          time.Time
	CompletedAt        time.Time
	TxsChecked         int
	DiscrepanciesFound int
	CorrectionsMade    int
}

// BalanceDiscrepancy records a wallet whose DB balance diverges from Horizon.
type BalanceDiscrepancy struct {
	ID             string
	WalletID       string
	DBBalance      decimal.Decimal
	HorizonBalance decimal.Decimal
	Asset          string
	DetectedAt     time.Time
	ResolvedAt     *time.Time
}

// Repository is implemented by postgres.TransactionRepo and covers confirmed-tx
// auditing, pending-tx reconciliation, and run record writes.
type Repository interface {
	GetConfirmedTxesForReconciliation(ctx context.Context, since time.Duration) ([]*domain.Transaction, error)
	GetStuckPendingTxes(ctx context.Context, olderThan time.Duration) ([]*domain.Transaction, error)
	// ResetStuckSubmittedToPending recovers a transaction claimed
	// (status=submitted) by a worker that crashed before recording a
	// tx_hash, so nothing may have reached the network. Gated on age so an
	// in-flight worker still within its normal processing window is never
	// touched. No-op (via domain.ErrConcurrentUpdate) for a pending
	// transaction, which needs no reset before being re-enqueued.
	ResetStuckSubmittedToPending(ctx context.Context, id string, olderThan time.Duration) error
	GetPendingTxesForReconciliation(ctx context.Context, olderThan time.Duration) ([]*domain.Transaction, error)
	UpdateReconciliationStatus(ctx context.Context, id string, status domain.TransactionStatus) error
	UpdateTxConfirmed(ctx context.Context, id, txHash string) error
	UpdateTxFailed(ctx context.Context, id string) error
	IncrementRequeueCount(ctx context.Context, id string) (int, error)
	UpdateReconciledAt(ctx context.Context, id string) error
	WriteAuditLog(ctx context.Context, entry *AuditLogEntry) error
	GetDailyReconciliationSummary(ctx context.Context, days int) ([]DailySummaryRow, error)
	GetPendingStuckCount(ctx context.Context, olderThan time.Duration) (int, error)
	WriteReconciliationRun(ctx context.Context, run *ReconciliationRun) error
}

// WalletRepository is implemented by postgres.ReconcileRepo and covers balance
// comparison and discrepancy persistence.
type WalletRepository interface {
	ListAllWallets(ctx context.Context) ([]*domain.Wallet, error)
	GetDBBalances(ctx context.Context, walletID string) (map[string]decimal.Decimal, error)
	WriteBalanceDiscrepancy(ctx context.Context, d *BalanceDiscrepancy) error
}

// WalletLookup resolves a wallet ID to its Stellar public key. Implemented by
// postgres.WalletRepo. Used so reconciliation can require that a Horizon
// payment operation's exact source/destination accounts match the wallets
// the transaction actually references, instead of accepting any account
// moving a matching amount/asset (#97).
type WalletLookup interface {
	GetByID(ctx context.Context, id string) (*domain.Wallet, error)
}

type Service struct {
	repo              Repository
	walletRepo        WalletRepository
	walletLookup      WalletLookup
	stellar           stellar.Client
	alerting          *alerting.Client
	queue             *queue.Client
	webhookSvc        webhook.Service
	svcName           string
	balanceThreshold  decimal.Decimal
	assetRegistry     *assets.Registry
	platformFeeWallet string
}

func NewService(
	repo Repository,
	walletRepo WalletRepository,
	walletLookup WalletLookup,
	stellarClient stellar.Client,
	alertingClient *alerting.Client,
	q *queue.Client,
	webhookSvc webhook.Service,
	svcName string,
	balanceThreshold decimal.Decimal,
	assetRegistry *assets.Registry,
	platformFeeWallet string,
) *Service {
	return &Service{
		repo:              repo,
		walletRepo:        walletRepo,
		walletLookup:      walletLookup,
		stellar:           stellarClient,
		alerting:          alertingClient,
		queue:             q,
		webhookSvc:        webhookSvc,
		svcName:           svcName,
		balanceThreshold:  balanceThreshold,
		assetRegistry:     assetRegistry,
		platformFeeWallet: platformFeeWallet,
	}
}

// RunAll is called by the Asynq periodic task every 5 minutes. It runs the
// pending-tx reconciliation pass, the confirmed-tx audit pass, and the stuck-tx
// recovery pass, then writes a reconciliation_runs record regardless of errors.
func (s *Service) RunAll(ctx context.Context) error {
	startedAt := time.Now().UTC()

	txsChecked, discrepanciesFound, correctionsMade, pendingErr := s.RunPendingReconciliation(ctx)
	if pendingErr != nil {
		log.Error().Err(pendingErr).Msg("reconcile: pending reconciliation pass failed")
	}

	if err := s.Reconcile(ctx); err != nil {
		log.Error().Err(err).Msg("reconcile: confirmed reconciliation pass failed")
	}

	if err := s.RecoverPending(ctx); err != nil {
		log.Error().Err(err).Msg("reconcile: pending recovery pass failed")
	}

	run := &ReconciliationRun{
		ID:                 uuid.New().String(),
		StartedAt:          startedAt,
		CompletedAt:        time.Now().UTC(),
		TxsChecked:         txsChecked,
		DiscrepanciesFound: discrepanciesFound,
		CorrectionsMade:    correctionsMade,
	}
	if err := s.repo.WriteReconciliationRun(ctx, run); err != nil {
		log.Error().Err(err).Msg("reconcile: write reconciliation run record")
	}

	return nil
}

// RunPendingReconciliation checks all pending transactions that have a stored
// Stellar tx hash and corrects DB state to match Horizon. It uses row-level
// locking (SELECT FOR UPDATE SKIP LOCKED) in the repository layer so concurrent
// reconciler instances process disjoint sets of rows without blocking each other.
func (s *Service) RunPendingReconciliation(ctx context.Context) (txsChecked, discrepanciesFound, correctionsMade int, err error) {
	txes, err := s.repo.GetPendingTxesForReconciliation(ctx, pendingCheckThreshold)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("fetch pending txes for reconciliation: %w", err)
	}

	txsChecked = len(txes)
	log.Info().Int("count", txsChecked).Msg("reconcile: checking pending transactions against Horizon")

	for _, tx := range txes {
		discrepancy, correction, checkErr := s.checkPendingTransaction(ctx, tx)
		if checkErr != nil {
			log.Error().Err(checkErr).Str("tx_id", tx.ID).Msg("reconcile: pending tx check failed")
		}
		if discrepancy {
			discrepanciesFound++
		}
		if correction {
			correctionsMade++
		}
	}

	return txsChecked, discrepanciesFound, correctionsMade, nil
}

// checkPendingTransaction queries Horizon for a single pending transaction and
// corrects the DB state. Returns (discrepancy, correction, err).
func (s *Service) checkPendingTransaction(ctx context.Context, tx *domain.Transaction) (discrepancy, correction bool, err error) {
	if tx.TxHash == "" {
		// No hash means the worker never submitted it. If it has been stuck long
		// enough, RecoverPending will re-enqueue it; nothing to do here.
		if time.Since(tx.CreatedAt) > stuckThreshold {
			log.Warn().Str("tx_id", tx.ID).
				Msg("reconcile: pending tx has no hash and exceeds stuck threshold — flagging for manual review")
			s.alerting.Warning(ctx, "Unsubmitted Transaction Detected",
				fmt.Sprintf("Transaction %s has been pending with no Stellar hash for %s. RecoverPending will re-enqueue.", tx.ID, time.Since(tx.CreatedAt).Round(time.Second)))
			return true, false, nil
		}
		return false, false, nil
	}

	horizonTx, fetchErr := s.stellar.TransactionDetail(tx.TxHash)
	if fetchErr != nil {
		hErr, ok := fetchErr.(*horizonclient.Error)
		if ok && hErr.Problem.Status == 404 {
			// Hash exists in DB but Horizon doesn't know about it.
			if time.Since(tx.CreatedAt) > stuckThreshold {
				log.Warn().Str("tx_id", tx.ID).Str("tx_hash", tx.TxHash).
					Msg("reconcile: pending tx not found on Horizon after threshold — flagging for manual review")
				s.alerting.Warning(ctx, "Transaction Not Found on Horizon",
					fmt.Sprintf("Transaction %s (hash: %s) is pending in DB but not found on Horizon after %s. RecoverPending will re-enqueue.", tx.ID, tx.TxHash, time.Since(tx.CreatedAt).Round(time.Second)))
				return true, false, nil
			}
			return false, false, nil
		}
		return false, false, fmt.Errorf("fetch transaction detail for %s: %w", tx.TxHash, fetchErr)
	}

	if horizonTx.Successful {
		// On-chain confirmed but DB still shows pending → correct to confirmed.
		if updateErr := s.repo.UpdateTxConfirmed(ctx, tx.ID, tx.TxHash); updateErr != nil {
			if errors.Is(updateErr, domain.ErrConcurrentUpdate) {
				log.Warn().Str("tx_id", tx.ID).
					Msg("reconcile: concurrent update already handled pending→confirmed")
				return true, false, nil
			}
			return true, false, fmt.Errorf("update tx %s to confirmed: %w", tx.ID, updateErr)
		}
		log.Info().Str("tx_id", tx.ID).Str("tx_hash", tx.TxHash).
			Msg("reconcile: corrected DB state pending→confirmed")
		s.dispatchWebhook(ctx, domain.EventTransferSettled, tx)
		return true, true, nil
	}

	// On-chain failed but DB still shows pending → correct to failed.
	if updateErr := s.repo.UpdateTxFailed(ctx, tx.ID); updateErr != nil {
		if errors.Is(updateErr, domain.ErrConcurrentUpdate) {
			log.Warn().Str("tx_id", tx.ID).
				Msg("reconcile: concurrent update already handled pending→failed")
			return true, false, nil
		}
		return true, false, fmt.Errorf("update tx %s to failed: %w", tx.ID, updateErr)
	}
	log.Info().Str("tx_id", tx.ID).Str("tx_hash", tx.TxHash).
		Str("result_xdr", horizonTx.ResultXdr).Msg("reconcile: corrected DB state pending→failed")
	s.dispatchWebhook(ctx, domain.EventTransferFailed, tx)
	return true, true, nil
}

func (s *Service) dispatchWebhook(ctx context.Context, event domain.EventType, tx *domain.Transaction) {
	if s.webhookSvc == nil {
		return
	}
	payload := map[string]interface{}{
		"transaction_id": tx.ID,
		"event":          string(event),
		"tx_hash":        tx.TxHash,
		"amount":         tx.Amount.String(),
		"asset":          tx.Asset,
	}
	if err := s.webhookSvc.Dispatch(ctx, event, payload); err != nil {
		log.Error().Err(err).Str("tx_id", tx.ID).Str("event", string(event)).Msg("reconcile: dispatch webhook")
	}
}

// Reconcile verifies confirmed transactions against Horizon and flags
// discrepancies in the ledger audit log.
func (s *Service) Reconcile(ctx context.Context) error {
	txes, err := s.repo.GetConfirmedTxesForReconciliation(ctx, reconcileInterval)
	if err != nil {
		return fmt.Errorf("fetch txes for reconciliation: %w", err)
	}

	log.Info().Int("count", len(txes)).Msg("reconcile: checking confirmed transactions")

	for _, tx := range txes {
		if err := s.checkTransaction(ctx, tx); err != nil {
			log.Error().Err(err).Str("tx_id", tx.ID).Str("tx_hash", tx.TxHash).Msg("reconcile: check failed")
		}
	}

	return nil
}

func (s *Service) checkTransaction(ctx context.Context, tx *domain.Transaction) error {
	hash := tx.TxHash

	horizonTx, err := s.stellar.TransactionDetail(hash)
	if err != nil {
		hErr, ok := err.(*horizonclient.Error)
		if ok && hErr.Problem.Status == 404 {
			log.Error().Str("tx_id", tx.ID).Str("tx_hash", hash).Msg("reconcile: confirmed tx not found on horizon")
			if repoErr := s.repo.UpdateReconciliationStatus(ctx, tx.ID, domain.StatusReconciliationFailed); repoErr != nil {
				return fmt.Errorf("update status to reconciliation_failed: %w", repoErr)
			}

			s.writeAudit(ctx, tx, "HTTP 404", false, false, false, AuditNotFound, "transaction not found on Horizon")
			s.alerting.Critical(ctx, "Reconciliation Failed: Missing Transaction",
				fmt.Sprintf("Transaction %s (hash: %s) is marked confirmed in DB but returned 404 on Horizon. Possible ledger loss or fork.", tx.ID, hash))
			return nil
		}
		return fmt.Errorf("fetch transaction detail: %w", err)
	}

	if !horizonTx.Successful {
		log.Error().Str("tx_id", tx.ID).Str("tx_hash", hash).Msg("reconcile: confirmed tx marked as failed on horizon")
		if repoErr := s.repo.UpdateReconciliationStatus(ctx, tx.ID, domain.StatusReconciliationFailed); repoErr != nil {
			return fmt.Errorf("update status to reconciliation_failed: %w", repoErr)
		}

		s.writeAudit(ctx, tx, "unsuccessful", false, false, false, AuditNotFound,
			fmt.Sprintf("transaction successful=false on Horizon (result: %s)", horizonTx.ResultXdr))
		s.alerting.Critical(ctx, "Reconciliation Failed: Unsuccessful Transaction",
			fmt.Sprintf("Transaction %s (hash: %s) is marked confirmed in DB but Horizon reports it as unsuccessful.", tx.ID, hash))
		return nil
	}

	ops, err := s.stellar.OperationsForTransaction(hash)
	if err != nil {
		return fmt.Errorf("fetch operations for transaction: %w", err)
	}

	expected, err := s.buildExpectedPayment(ctx, tx)
	if err != nil {
		return fmt.Errorf("resolve expected payment for tx %s: %w", tx.ID, err)
	}

	amountVerified, assetVerified, feeVerified, details := verifyOps(ops, expected)

	if !amountVerified || !assetVerified || !feeVerified {
		log.Error().Str("tx_id", tx.ID).Str("tx_hash", hash).
			Bool("amount_verified", amountVerified).Bool("asset_verified", assetVerified).
			Bool("fee_verified", feeVerified).
			Msg("reconcile: payment mismatch")
		if repoErr := s.repo.UpdateReconciliationStatus(ctx, tx.ID, domain.StatusReconciliationFailed); repoErr != nil {
			return fmt.Errorf("update status to reconciliation_failed: %w", repoErr)
		}

		s.writeAudit(ctx, tx, horizonStatus(&horizonTx), amountVerified, assetVerified, feeVerified, AuditMismatch, details)
		s.alerting.Critical(ctx, "Reconciliation Failed: Payment Mismatch",
			fmt.Sprintf("Transaction %s (hash: %s): %s", tx.ID, hash, details))
		return nil
	}

	s.writeAudit(ctx, tx, horizonStatus(&horizonTx), true, true, true, AuditOK, "all checks passed")
	if err := s.repo.UpdateReconciledAt(ctx, tx.ID); err != nil {
		log.Error().Err(err).Str("tx_id", tx.ID).Msg("reconcile: update reconciled_at")
	}

	log.Debug().Str("tx_id", tx.ID).Str("tx_hash", hash).Msg("reconcile: verified ok")
	return nil
}

// expectedPayment describes exactly what the platform expects a reconciled
// transaction's on-chain operations to contain: the precise source and
// destination accounts (not just "some account"), the asset's code AND
// issuer/native identity (not just a matching code, which a look-alike asset
// could also have), and the exact net amount — plus, when the transaction
// carries a platform fee, the exact fee leg paid to the platform fee wallet.
type expectedPayment struct {
	// FromPublicKey is "" when the transaction has no internal source wallet
	// (e.g. a deposit discovered by the indexer from an external account),
	// in which case the source account is intentionally not constrained.
	FromPublicKey      string
	ToPublicKey        string
	AssetCode          string
	AssetIssuer        string // "" for native XLM
	NetAmount          decimal.Decimal
	Fee                decimal.Decimal
	FeeWalletPublicKey string
}

// buildExpectedPayment resolves a transaction's wallet IDs and asset code
// into the concrete Stellar accounts and asset identity reconciliation must
// match against Horizon, using the same asset registry and platform fee
// wallet the settlement engine uses to build the original payment.
func (s *Service) buildExpectedPayment(ctx context.Context, tx *domain.Transaction) (expectedPayment, error) {
	exp := expectedPayment{
		NetAmount:          tx.NetAmount(),
		Fee:                tx.Fee,
		FeeWalletPublicKey: s.platformFeeWallet,
	}

	if tx.FromWallet != "" {
		fromWallet, err := s.walletLookup.GetByID(ctx, tx.FromWallet)
		if err != nil {
			return expectedPayment{}, fmt.Errorf("resolve source wallet %s: %w", tx.FromWallet, err)
		}
		exp.FromPublicKey = fromWallet.PublicKey
	}

	if tx.ToWallet != "" {
		toWallet, err := s.walletLookup.GetByID(ctx, tx.ToWallet)
		if err != nil {
			return expectedPayment{}, fmt.Errorf("resolve destination wallet %s: %w", tx.ToWallet, err)
		}
		exp.ToPublicKey = toWallet.PublicKey
	}

	if tx.Asset == "XLM" {
		exp.AssetCode = "XLM"
	} else if asset, ok := s.assetRegistry.Get(tx.Asset); ok {
		exp.AssetCode = asset.Code
		exp.AssetIssuer = asset.Issuer
	} else {
		return expectedPayment{}, fmt.Errorf("asset %q is not in the platform asset registry", tx.Asset)
	}

	return exp, nil
}

// candidatePayment is the subset of a Horizon payment/path-payment operation
// relevant to reconciliation matching, extracted independently of which
// concrete Horizon SDK operation type carried it.
type candidatePayment struct {
	From        string
	To          string
	Amount      decimal.Decimal
	AssetType   string
	AssetCode   string
	AssetIssuer string
}

// assetIdentityMatches reports whether the candidate's asset is the exact
// asset the platform expects: native XLM must match on type alone (native
// assets carry no code/issuer), while a credit asset must match on code AND
// issuer — a look-alike token sharing a code (e.g. an unofficial "USDC")
// with a different issuer must not be accepted.
func (c candidatePayment) assetIdentityMatches(expected expectedPayment) bool {
	if expected.AssetCode == "XLM" {
		return c.AssetType == "native"
	}
	return c.AssetType != "native" && c.AssetCode == expected.AssetCode && c.AssetIssuer == expected.AssetIssuer
}

// matchesMainLeg reports whether this single candidate operation satisfies
// every expected property of the transaction's primary payment leg at once:
// exact source (when one is expected), exact destination, exact asset
// identity, and the exact net amount. Matching amount and asset independently
// on different operations — the root cause of #97 — is deliberately not
// possible here since every check runs against the same candidate.
func (c candidatePayment) matchesMainLeg(expected expectedPayment) bool {
	if expected.FromPublicKey != "" && c.From != expected.FromPublicKey {
		return false
	}
	if expected.ToPublicKey != "" && c.To != expected.ToPublicKey {
		return false
	}
	if !c.assetIdentityMatches(expected) {
		return false
	}
	return c.Amount.Equal(expected.NetAmount)
}

// matchesFeeLeg reports whether this candidate operation is the distinct fee
// payment the settlement engine submits alongside the main payment whenever
// tx.Fee > 0: the exact fee amount, in the same asset, paid to the platform
// fee wallet specifically (not merely to "an" account).
func (c candidatePayment) matchesFeeLeg(expected expectedPayment) bool {
	if expected.FeeWalletPublicKey == "" || c.To != expected.FeeWalletPublicKey {
		return false
	}
	if !c.assetIdentityMatches(expected) {
		return false
	}
	return c.Amount.Equal(expected.Fee)
}

// extractCandidatePayment pulls the fields relevant to matching out of a
// Horizon operation, or reports ok=false for anything that isn't a
// payment/path-payment operation with a parseable amount.
func extractCandidatePayment(op operations.Operation) (candidate candidatePayment, ok bool) {
	opType := op.GetType()
	if opType != "payment" && opType != "path_payment_strict_send" && opType != "path_payment_strict_receive" {
		return candidatePayment{}, false
	}

	var from, to, amountStr, assetType, assetCode, assetIssuer string
	switch p := op.(type) {
	case operations.Payment:
		from, to, amountStr = p.From, p.To, p.Amount
		assetType, assetCode, assetIssuer = p.Asset.Type, p.Asset.Code, p.Asset.Issuer
	case operations.PathPayment:
		from, to, amountStr = p.From, p.To, p.Amount
		assetType, assetCode, assetIssuer = p.Asset.Type, p.Asset.Code, p.Asset.Issuer
	default:
		return candidatePayment{}, false
	}

	if amountStr == "" {
		return candidatePayment{}, false
	}
	amount, err := decimal.NewFromString(amountStr)
	if err != nil {
		return candidatePayment{}, false
	}

	return candidatePayment{
		From:        from,
		To:          to,
		Amount:      amount,
		AssetType:   assetType,
		AssetCode:   assetCode,
		AssetIssuer: assetIssuer,
	}, true
}

// verifyOps checks the transaction's Horizon operations against exactly what
// the platform expects. amountVerified/assetVerified are only ever set
// together, by a single candidate operation that matches source,
// destination, asset identity, and amount simultaneously — an unrelated
// operation that merely happens to share the amount or the asset code is
// rejected. When the transaction carries a platform fee, feeVerified
// additionally requires a distinct operation paying that exact fee to the
// platform fee wallet.
func verifyOps(ops []operations.Operation, expected expectedPayment) (amountVerified, assetVerified, feeVerified bool, details string) {
	feeVerified = !expected.Fee.GreaterThan(decimal.Zero)

	for _, op := range ops {
		candidate, ok := extractCandidatePayment(op)
		if !ok {
			continue
		}

		if !amountVerified && candidate.matchesMainLeg(expected) {
			amountVerified = true
			assetVerified = true
		}
		if !feeVerified && candidate.matchesFeeLeg(expected) {
			feeVerified = true
		}
		if amountVerified && feeVerified {
			return true, true, true, ""
		}
	}

	return amountVerified, assetVerified, feeVerified, fmt.Sprintf(
		"expected from=%q to=%q asset=%s issuer=%q net_amount=%s fee=%s fee_wallet=%q | Horizon ops: %d checked",
		expected.FromPublicKey, expected.ToPublicKey, expected.AssetCode, expected.AssetIssuer,
		expected.NetAmount, expected.Fee, expected.FeeWalletPublicKey, len(ops))
}

// RecoverPending re-enqueues stuck pending transactions (regardless of whether
// they have a Stellar hash) up to maxRequeues times before marking them failed.
func (s *Service) RecoverPending(ctx context.Context) error {
	txes, err := s.repo.GetStuckPendingTxes(ctx, stuckThreshold)
	if err != nil {
		return fmt.Errorf("fetch stuck pending txes: %w", err)
	}

	log.Info().Int("count", len(txes)).Msg("reconcile: recovering stuck pending transactions")

	for _, tx := range txes {
		// Defence in depth, and checked first so no later branch can act on a
		// held transfer. GetStuckPendingTxes selects pending rows and
		// submitted-without-hash rows, so a compliance_hold row should never
		// appear here — but such a transfer is waiting on a human, not stuck,
		// and re-enqueuing one would release a payment compliance
		// deliberately stopped. Re-asserting the invariant here keeps it
		// testable and means a future widening of that query cannot quietly
		// become a compliance bypass.
		if tx.Status == domain.StatusComplianceHold {
			log.Warn().Str("tx_id", tx.ID).
				Msg("reconcile: skipping transaction held for compliance review")
			continue
		}

		if tx.Status == domain.StatusSubmitted {
			// This row was claimed by a worker that crashed before it could
			// record a tx_hash — nothing may have reached the network. Reset
			// it to pending so ClaimForSubmission (deliberately strict:
			// pending-only) will accept a fresh attempt.
			if err := s.repo.ResetStuckSubmittedToPending(ctx, tx.ID, stuckThreshold); err != nil {
				if errors.Is(err, domain.ErrConcurrentUpdate) {
					log.Info().Str("tx_id", tx.ID).
						Msg("reconcile: stuck submitted tx no longer eligible for reset (already progressed)")
				} else {
					log.Error().Err(err).Str("tx_id", tx.ID).Msg("reconcile: reset stuck submitted tx to pending")
				}
				continue
			}
		}

		newCount, err := s.repo.IncrementRequeueCount(ctx, tx.ID)
		if err != nil {
			log.Error().Err(err).Str("tx_id", tx.ID).Msg("reconcile: increment requeue count")
			continue
		}

		if newCount > maxRequeues {
			log.Warn().Str("tx_id", tx.ID).Int("requeue_count", newCount).Msg("reconcile: max requeues reached, marking failed")
			if repoErr := s.repo.UpdateReconciliationStatus(ctx, tx.ID, domain.StatusFailed); repoErr != nil {
				log.Error().Err(repoErr).Str("tx_id", tx.ID).Msg("reconcile: mark as failed")
			}
			s.alerting.Critical(ctx, "Transaction Failed: Max Requeues",
				fmt.Sprintf("Transaction %s has been re-enqueued %d times without success. Marked as failed.", tx.ID, newCount))
			continue
		}

		if err := s.queue.EnqueueTransfer(ctx, tx.ID); err != nil {
			log.Error().Err(err).Str("tx_id", tx.ID).Msg("reconcile: re-enqueue transfer failed")
			continue
		}

		log.Info().Str("tx_id", tx.ID).Int("requeue_count", newCount).Msg("reconcile: re-enqueued pending transaction")
	}

	return nil
}

// RunBalanceReconciliation is a daily job that compares each wallet's DB balances
// against live Horizon account balances. Discrepancies are flagged in the
// balance_discrepancies table and alerted — never auto-corrected.
func (s *Service) RunBalanceReconciliation(ctx context.Context) error {
	wallets, err := s.walletRepo.ListAllWallets(ctx)
	if err != nil {
		return fmt.Errorf("list wallets for balance reconciliation: %w", err)
	}

	log.Info().Int("wallet_count", len(wallets)).Msg("reconcile: balance reconciliation starting")

	for _, w := range wallets {
		if err := s.checkWalletBalance(ctx, w); err != nil {
			log.Error().Err(err).Str("wallet_id", w.ID).Str("public_key", w.PublicKey).
				Msg("reconcile: wallet balance check failed")
		}
	}

	log.Info().Msg("reconcile: balance reconciliation complete")
	return nil
}

func (s *Service) checkWalletBalance(ctx context.Context, w *domain.Wallet) error {
	acct, err := s.stellar.LoadAccount(w.PublicKey)
	if err != nil {
		hErr, ok := err.(*horizonclient.Error)
		if ok && hErr.Problem.Status == 404 {
			// Wallet not yet funded on Stellar — not a discrepancy.
			return nil
		}
		return fmt.Errorf("load Horizon account %s: %w", w.PublicKey, err)
	}

	dbBalances, err := s.walletRepo.GetDBBalances(ctx, w.ID)
	if err != nil {
		return fmt.Errorf("get DB balances for wallet %s: %w", w.ID, err)
	}

	// Build asset → balance map from Horizon, keyed by canonical identity
	// (e.g. "XLM" for native, "USDC:GXXXX" for credit assets).
	horizonBalances := make(map[string]decimal.Decimal)
	for _, b := range acct.Balances {
		asset := horizonAssetIdentity(&b)
		amt, _ := decimal.NewFromString(b.Balance)
		horizonBalances[asset] = horizonBalances[asset].Add(amt)
	}

	// Union of all assets mentioned in either side.
	assets := make(map[string]struct{})
	for k := range dbBalances {
		assets[k] = struct{}{}
	}
	for k := range horizonBalances {
		assets[k] = struct{}{}
	}

	for asset := range assets {
		dbAmt := dbBalances[asset] // zero-value decimal if key absent
		horizonAmt := horizonBalances[asset]
		diff := dbAmt.Sub(horizonAmt).Abs()
		if diff.LessThanOrEqual(s.balanceThreshold) {
			continue
		}

		d := &BalanceDiscrepancy{
			ID:             uuid.New().String(),
			WalletID:       w.ID,
			DBBalance:      dbAmt,
			HorizonBalance: horizonAmt,
			Asset:          asset,
			DetectedAt:     time.Now().UTC(),
		}
		if writeErr := s.walletRepo.WriteBalanceDiscrepancy(ctx, d); writeErr != nil {
			log.Error().Err(writeErr).Str("wallet_id", w.ID).Str("asset", asset).
				Msg("reconcile: write balance discrepancy")
		}

		log.Warn().Str("wallet_id", w.ID).Str("public_key", w.PublicKey).Str("asset", asset).
			Str("db_balance", dbAmt.String()).Str("horizon_balance", horizonAmt.String()).
			Str("diff", diff.String()).Msg("reconcile: balance discrepancy detected")

		s.alerting.Warning(ctx, "Balance Discrepancy Detected",
			fmt.Sprintf("Wallet %s (key: %s): asset=%s DB=%s Horizon=%s diff=%s",
				w.ID, w.PublicKey, asset, dbAmt.String(), horizonAmt.String(), diff.String()))
	}

	return nil
}

// horizonAssetIdentity returns a canonical string that uniquely identifies a
// Horizon balance asset: "XLM" for native, or "CODE:ISSUER" for credit assets.
// This ensures two issuers sharing the same asset code are compared independently.
func horizonAssetIdentity(b *horizon.Balance) string {
	if b.Asset.Type == "native" {
		return "XLM"
	}
	return b.Asset.Code + ":" + b.Asset.Issuer
}

func (s *Service) GetSummary(ctx context.Context, days int) (*SummaryResponse, error) {
	rows, err := s.repo.GetDailyReconciliationSummary(ctx, days)
	if err != nil {
		return nil, fmt.Errorf("get summary: %w", err)
	}

	stuckCount, err := s.repo.GetPendingStuckCount(ctx, stuckThreshold)
	if err != nil {
		return nil, fmt.Errorf("get stuck count: %w", err)
	}

	var totalOK, totalMismatch, totalNotFound int
	for _, r := range rows {
		totalOK += r.OKCount
		totalMismatch += r.MismatchCount
		totalNotFound += r.NotFoundCount
	}

	return &SummaryResponse{
		Days:          rows,
		TotalOK:       totalOK,
		TotalMismatch: totalMismatch,
		TotalNotFound: totalNotFound,
		PendingStuck:  stuckCount,
	}, nil
}

type SummaryResponse struct {
	Days          []DailySummaryRow `json:"days"`
	TotalOK       int               `json:"total_ok"`
	TotalMismatch int               `json:"total_mismatch"`
	TotalNotFound int               `json:"total_not_found"`
	PendingStuck  int               `json:"pending_stuck"`
}

func (s *Service) writeAudit(ctx context.Context, tx *domain.Transaction, horizonStatus string, amountOK, assetOK, feeOK bool, outcome AuditOutcome, details string) {
	entry := &AuditLogEntry{
		ID:             uuid.New().String(),
		TxID:           tx.ID,
		StellarHash:    tx.TxHash,
		CheckedAt:      time.Now().UTC(),
		HorizonStatus:  horizonStatus,
		AmountVerified: amountOK,
		AssetVerified:  assetOK,
		FeeVerified:    feeOK,
		Outcome:        outcome,
		Details:        details,
	}
	if err := s.repo.WriteAuditLog(ctx, entry); err != nil {
		log.Error().Err(err).Str("tx_id", tx.ID).Msg("reconcile: write audit log")
	}
}

func horizonStatus(tx *horizon.Transaction) string {
	if tx == nil {
		return ""
	}
	if tx.Successful {
		return fmt.Sprintf("successful (ledger %d)", tx.Ledger)
	}
	return fmt.Sprintf("unsuccessful (ledger %d)", tx.Ledger)
}
