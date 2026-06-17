package reconcile

import (
	"context"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

type Worker struct {
	service *Service
}

func NewWorker(service *Service) *Worker {
	return &Worker{service: service}
}

func (w *Worker) HandleReconcile(ctx context.Context, _ *asynq.Task) error {
	log.Info().Msg("reconcile: scheduled run starting")
	if err := w.service.RunAll(ctx); err != nil {
		log.Error().Err(err).Msg("reconcile: scheduled run failed")
		return err
	}
	log.Info().Msg("reconcile: scheduled run complete")
	return nil
}
