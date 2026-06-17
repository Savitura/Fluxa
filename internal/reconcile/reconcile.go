package reconcile

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/alerting"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
	horizonclient "github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/protocols/horizon"
)

const (
	reconcileInterval = 1 * time.Hour
	stuckThreshold    = 10 * time.Minute
	maxRequeues       = 3
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

type Repository interface {
	GetConfirmedTxesForReconciliation(ctx context.Context, since time.Duration) ([]*domain.Transaction, error)
	GetStuckPendingTxes(ctx context.Context, olderThan time.Duration) ([]*domain.Transaction, error)
	UpdateReconciliationStatus(ctx context.Context, id string, status domain.TransactionStatus) error
	IncrementRequeueCount(ctx context.Context, id string) (int, error)
	UpdateReconciledAt(ctx context.Context, id string) error
	WriteAuditLog(ctx context.Context, entry *AuditLogEntry) error
	GetDailyReconciliationSummary(ctx context.Context, days int) ([]DailySummaryRow, error)
	GetPendingStuckCount(ctx context.Context, olderThan time.Duration) (int, error)
}

type Service struct {
	repo     Repository
	stellar  stellar.Client
	alerting *alerting.Client
	queue    *queue.Client
	svcName  string
}

func NewService(repo Repository, stellarClient stellar.Client, alertingClient *alerting.Client, q *queue.Client, svcName string) *Service {
	return &Service{
		repo:     repo,
		stellar:  stellarClient,
		alerting: alertingClient,
		queue:    q,
		svcName:  svcName,
	}
}

func (s *Service) RunAll(ctx context.Context) error {
	if err := s.Reconcile(ctx); err != nil {
		log.Error().Err(err).Msg("reconcile: reconciliation pass failed")
	}
	if err := s.RecoverPending(ctx); err != nil {
		log.Error().Err(err).Msg("reconcile: pending recovery pass failed")
	}
	return nil
}

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
			fmt.Sprintf("transaction successful=false on Horizon (result: %s)", horizonTx.ResultXDR))
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

func verifyOps(tx *domain.Transaction, ops []horizon.Operation) (amountVerified, assetVerified bool, details string) {
	for _, op := range ops {
		if op.Type != "payment" && op.Type != "path_payment_strict_send" && op.Type != "path_payment_strict_receive" {
			continue
		}

		if op.Amount == "" {
			continue
		}

		horizonAmount, err := decimal.NewFromString(op.Amount)
		if err != nil {
			continue
		}

		netAmount := tx.NetAmount()

		if horizonAmount.Equal(netAmount) || horizonAmount.Equal(tx.Amount) {
			amountVerified = true
		}

		expectedCode := tx.Asset
		matched := false
		if expectedCode == "XLM" && op.AssetType == "native" {
			matched = true
		} else if expectedCode != "" && op.AssetCode == expectedCode {
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
		Days:             rows,
		TotalOK:          totalOK,
		TotalMismatch:    totalMismatch,
		TotalNotFound:    totalNotFound,
		PendingStuck:     stuckCount,
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
