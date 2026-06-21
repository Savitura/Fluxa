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

// HandleReconcile runs the full pending + confirmed reconciliation pass.
// Registered as a periodic Asynq task every 5 minutes.
func (w *Worker) HandleReconcile(ctx context.Context, _ *asynq.Task) error {
	log.Info().Msg("reconcile: scheduled run starting")
	if err := w.service.RunAll(ctx); err != nil {
		log.Error().Err(err).Msg("reconcile: scheduled run failed")
		return err
	}
	log.Info().Msg("reconcile: scheduled run complete")
	return nil
}

// HandleBalanceReconcile runs the daily balance reconciliation job.
// It compares DB balances against live Horizon account balances and flags
// discrepancies — never auto-corrects.
func (w *Worker) HandleBalanceReconcile(ctx context.Context, _ *asynq.Task) error {
	log.Info().Msg("reconcile: balance reconciliation starting")
	if err := w.service.RunBalanceReconciliation(ctx); err != nil {
		log.Error().Err(err).Msg("reconcile: balance reconciliation failed")
		return err
	}
	log.Info().Msg("reconcile: balance reconciliation complete")
	return nil
}
