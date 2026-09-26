package indexer

import (
	"context"

	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/hibiken/asynq"
)

type Worker struct {
	indexer *Indexer
}

func NewWorker(indexer *Indexer) *Worker {
	return &Worker{indexer: indexer}
}

func (w *Worker) HandleSyncLedger(ctx context.Context, task *asynq.Task) error {
	ctx = queue.ContextFromTask(ctx, task)
	ctx, span := tracing.StartConsumer(ctx, task.Type())
	defer span.End()

	// Log through the context so every entry carries the inherited trace_id.
	logger := tracing.Logger(ctx)

	logger.Info().Msg("running ledger sync")
	if err := w.indexer.SyncAll(ctx, 100, 0); err != nil {
		logger.Error().Err(err).Msg("ledger sync failed")
		return err
	}
	return nil
}
