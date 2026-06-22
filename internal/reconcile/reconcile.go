package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/alerting"
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

type Service struct {
	repo             Repository
	walletRepo       WalletRepository
	stellar          stellar.Client
	alerting         *alerting.Client
	queue            *queue.Client
	webhookSvc       webhook.Service
	svcName          string
	balanceThreshold decimal.Decimal
}

func NewService(
	repo Repository,
	walletRepo WalletRepository,
	stellarClient stellar.Client,
	alertingClient *alerting.Client,
	q *queue.Client,
	webhookSvc webhook.Service,
	svcName string,
	balanceThreshold decimal.Decimal,
) *Service {
	return &Service{
		repo:             repo,
		walletRepo:       walletRepo,
		stellar:          stellarClient,
		alerting:         alertingClient,
		queue:            q,
		webhookSvc:       webhookSvc,
		svcName:          svcName,
		balanceThreshold: balanceThreshold,
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
			return true, false, fmt.Errorf("update tx %s to confirmed: %w", tx.ID, updateErr)
		}
		log.Info().Str("tx_id", tx.ID).Str("tx_hash", tx.TxHash).
			Msg("reconcile: corrected DB state pending→confirmed")
		s.dispatchWebhook(ctx, domain.EventTransferSettled, tx)
		return true, true, nil
	}

	// On-chain failed but DB still shows pending → correct to failed.
	if updateErr := s.repo.UpdateTxFailed(ctx, tx.ID); updateErr != nil {
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

			s.writeAudit(ctx, tx, "HTTP 404", false, false, AuditNotFound, "transaction not found on Horizon")
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

		s.writeAudit(ctx, tx, "unsuccessful", false, false, AuditNotFound,
			fmt.Sprintf("transaction successful=false on Horizon (result: %s)", horizonTx.ResultXdr))
		s.alerting.Critical(ctx, "Reconciliation Failed: Unsuccessful Transaction",
			fmt.Sprintf("Transaction %s (hash: %s) is marked confirmed in DB but Horizon reports it as unsuccessful.", tx.ID, hash))
		return nil
	}

	ops, err := s.stellar.OperationsForTransaction(hash)
	if err != nil {
		return fmt.Errorf("fetch operations for transaction: %w", err)
	}

	amountVerified, assetVerified, details := verifyOps(tx, ops)

	if !amountVerified || !assetVerified {
		log.Error().Str("tx_id", tx.ID).Str("tx_hash", hash).
			Bool("amount_verified", amountVerified).Bool("asset_verified", assetVerified).
			Msg("reconcile: amount/asset mismatch")
		if repoErr := s.repo.UpdateReconciliationStatus(ctx, tx.ID, domain.StatusReconciliationFailed); repoErr != nil {
			return fmt.Errorf("update status to reconciliation_failed: %w", repoErr)
		}

		s.writeAudit(ctx, tx, horizonStatus(&horizonTx), amountVerified, assetVerified, AuditMismatch, details)
		s.alerting.Critical(ctx, "Reconciliation Failed: Amount/Asset Mismatch",
			fmt.Sprintf("Transaction %s (hash: %s): %s", tx.ID, hash, details))
		return nil
	}

	s.writeAudit(ctx, tx, horizonStatus(&horizonTx), true, true, AuditOK, "all checks passed")
	if err := s.repo.UpdateReconciledAt(ctx, tx.ID); err != nil {
		log.Error().Err(err).Str("tx_id", tx.ID).Msg("reconcile: update reconciled_at")
	}

	log.Debug().Str("tx_id", tx.ID).Str("tx_hash", hash).Msg("reconcile: verified ok")
	return nil
}

func verifyOps(tx *domain.Transaction, ops []operations.Operation) (amountVerified, assetVerified bool, details string) {
	for _, op := range ops {
		opType := op.GetType()
		if opType != "payment" && opType != "path_payment_strict_send" && opType != "path_payment_strict_receive" {
			continue
		}

		var amount, assetType, assetCode string
		switch p := op.(type) {
		case operations.Payment:
			amount = p.Amount
			assetType = p.Asset.Type
			assetCode = p.Asset.Code
		case operations.PathPayment:
			amount = p.Amount
			assetType = p.Asset.Type
			assetCode = p.Asset.Code
		default:
			continue
		}

		if amount == "" {
			continue
		}

		horizonAmount, err := decimal.NewFromString(amount)
		if err != nil {
			continue
		}

		netAmount := tx.NetAmount()
		if horizonAmount.Equal(netAmount) || horizonAmount.Equal(tx.Amount) {
			amountVerified = true
		}

		expectedCode := tx.Asset
		matched := false
		if expectedCode == "XLM" && assetType == "native" {
			matched = true
		} else if expectedCode != "" && assetCode == expectedCode {
			matched = true
		}
		if matched {
			assetVerified = true
		}

		if amountVerified && assetVerified {
			return true, true, ""
		}
	}

	return amountVerified, assetVerified,
		fmt.Sprintf("DB: amount=%s asset=%s | Horizon ops: %d checked", tx.Amount, tx.Asset, len(ops))
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

	// Build asset → balance map from Horizon.
	horizonBalances := make(map[string]decimal.Decimal)
	for _, b := range acct.Balances {
		asset := assetCode(&b)
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
		dbAmt := dbBalances[asset]       // zero-value decimal if key absent
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

func assetCode(b *horizon.Balance) string {
	if b.Asset.Type == "native" {
		return "XLM"
	}
	return b.Asset.Code
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

func (s *Service) writeAudit(ctx context.Context, tx *domain.Transaction, horizonStatus string, amountOK, assetOK bool, outcome AuditOutcome, details string) {
	entry := &AuditLogEntry{
		ID:             uuid.New().String(),
		TxID:           tx.ID,
		StellarHash:    tx.TxHash,
		CheckedAt:      time.Now().UTC(),
		HorizonStatus:  horizonStatus,
		AmountVerified: amountOK,
		AssetVerified:  assetOK,
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
