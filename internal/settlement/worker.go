package settlement

import (
	"context"
	"encoding/json"
	"fmt"

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

	if err := w.engine.SubmitTransfer(ctx, payload.TransactionID); err != nil {
		log.Error().Err(err).Str("tx_id", payload.TransactionID).Msg("transfer submission failed")
		return err
	}

	log.Info().Str("tx_id", payload.TransactionID).Msg("transfer confirmed")
	return nil
}
