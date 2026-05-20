package indexer

import (
	"context"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

type Worker struct {
	indexer *Indexer
}

func NewWorker(indexer *Indexer) *Worker {
	return &Worker{indexer: indexer}
}

func (w *Worker) HandleSyncLedger(ctx context.Context, task *asynq.Task) error {
	log.Info().Msg("running ledger sync")
	if err := w.indexer.SyncAll(ctx, 100, 0); err != nil {
		log.Error().Err(err).Msg("ledger sync failed")
		return err
	}
	return nil
}
