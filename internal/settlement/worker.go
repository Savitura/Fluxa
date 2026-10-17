package settlement

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/hibiken/asynq"
)

// BatchSyncer refreshes the aggregate counts of the batch a settled transfer
// belongs to, and emits the batch-level webhook when the batch reaches a
// terminal status. It is the hook that keeps per-transfer settlement and the
// parent batch summary in step.
type BatchSyncer interface {
	SyncBatch(ctx context.Context, batchID string) error
}

type Worker struct {
	engine  *Engine
	batches BatchSyncer
}

func NewWorker(engine *Engine) *Worker {
	return &Worker{engine: engine}
}

// WithBatchSyncer attaches the batch aggregate sync so every terminal
// transfer outcome updates its parent batch. Optional: without it the worker
// behaves as before and batches only refresh on read (GET self-heal).
func (w *Worker) WithBatchSyncer(s BatchSyncer) *Worker {
	w.batches = s
	return w
}

func (w *Worker) syncBatch(ctx context.Context, tx *domain.Transaction) {
	if w.batches == nil || tx.BatchID == nil || *tx.BatchID == "" {
		return
	}
	if err := w.batches.SyncBatch(ctx, *tx.BatchID); err != nil {
		tracing.Logger(ctx).Error().Err(err).
			Str("tx_id", tx.ID).
			Str("batch_id", *tx.BatchID).
			Msg("failed to sync batch aggregates")
	}
}

func (w *Worker) HandleProcessTransfer(ctx context.Context, task *asynq.Task) error {
	ctx = queue.ContextFromTask(ctx, task)
	ctx, span := tracing.StartConsumer(ctx, task.Type())
	defer span.End()

	var payload queue.ProcessTransferPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	// Log through the context so every entry carries the inherited trace_id.
	logger := tracing.Logger(ctx)

	logger.Info().Str("tx_id", payload.TransactionID).Msg("processing transfer")

	tx, err := w.engine.txRepo.GetByID(ctx, payload.TransactionID)
	if err != nil {
		logger.Error().Err(err).Str("tx_id", payload.TransactionID).Msg("failed to fetch transaction for settlement check")
		return err
	}
	if tx.TenantID != nil {
		ctx = tenant.WithID(ctx, *tx.TenantID)
	}
	ctx = tenant.WithMode(ctx, tx.Mode)

	if tx.Status == domain.StatusFailed || tx.Status == domain.StatusReconciliationFailed {
		logger.Warn().Str("tx_id", payload.TransactionID).Str("status", string(tx.Status)).Msg("skipping already-failed transaction")
		_ = w.engine.txRepo.UpdateStatus(ctx, payload.TransactionID, domain.StatusFailed, tx.TxHash)
		w.syncBatch(ctx, tx)
		return nil
	}

	if err := w.engine.SubmitTransfer(ctx, payload.TransactionID); err != nil {
		logger.Error().Err(err).Str("tx_id", payload.TransactionID).Msg("transfer submission failed")
		_ = w.engine.txRepo.UpdateStatus(ctx, payload.TransactionID, domain.StatusFailed, "")
		w.syncBatch(ctx, tx)
		return err
	}

	logger.Info().Str("tx_id", payload.TransactionID).Msg("transfer confirmed")
	w.syncBatch(ctx, tx)
	return nil
}
