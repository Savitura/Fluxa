package settlement

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

type Worker struct {
	engine *Engine
}

func NewWorker(engine *Engine) *Worker {
	return &Worker{engine: engine}
}

func (w *Worker) HandleProcessTransfer(ctx context.Context, task *asynq.Task) error {
	var payload queue.ProcessTransferPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	log.Info().Str("tx_id", payload.TransactionID).Msg("processing transfer")

	tx, err := w.engine.txRepo.GetByID(ctx, payload.TransactionID)
	if err != nil {
		log.Error().Err(err).Str("tx_id", payload.TransactionID).Msg("failed to fetch transaction for settlement check")
		return err
	}

	if tx.Status == domain.StatusFailed || tx.Status == domain.StatusReconciliationFailed {
		log.Warn().Str("tx_id", payload.TransactionID).Str("status", string(tx.Status)).Msg("skipping already-failed transaction")
		_ = w.engine.txRepo.UpdateStatus(ctx, payload.TransactionID, domain.StatusFailed, tx.TxHash)
		return nil
	}

	if err := w.engine.SubmitTransfer(ctx, payload.TransactionID);
	err != nil {
		log.Error().Err(err).Str("tx_id", payload.TransactionID).Msg("transfer submission failed")
		_ = w.engine.txRepo.UpdateStatus(ctx, payload.TransactionID, domain.StatusFailed, "")
		return err
	}

	log.Info().Str("tx_id", payload.TransactionID).Msg("transfer confirmed")
	return nil
}
