package reconcile

import (
	"context"

	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/hibiken/asynq"
)

type Worker struct {
	service *Service
}

func NewWorker(service *Service) *Worker {
	return &Worker{service: service}
}

// HandleReconcile runs the full pending + confirmed reconciliation pass.
// Registered as a periodic Asynq task every 5 minutes.
func (w *Worker) HandleReconcile(ctx context.Context, task *asynq.Task) error {
	ctx = queue.ContextFromTask(ctx, task)
	ctx, span := tracing.StartConsumer(ctx, task.Type())
	defer span.End()

	// Log through the context so every entry carries the inherited trace_id.
	logger := tracing.Logger(ctx)

	logger.Info().Msg("reconcile: scheduled run starting")
	if err := w.service.RunAll(ctx); err != nil {
		logger.Error().Err(err).Msg("reconcile: scheduled run failed")
		return err
	}
	logger.Info().Msg("reconcile: scheduled run complete")
	return nil
}

// HandleBalanceReconcile runs the daily balance reconciliation job.
// It compares DB balances against live Horizon account balances and flags
// discrepancies — never auto-corrects.
func (w *Worker) HandleBalanceReconcile(ctx context.Context, task *asynq.Task) error {
	ctx = queue.ContextFromTask(ctx, task)
	ctx, span := tracing.StartConsumer(ctx, task.Type())
	defer span.End()

	// Log through the context so every entry carries the inherited trace_id.
	logger := tracing.Logger(ctx)

	logger.Info().Msg("reconcile: balance reconciliation starting")
	if err := w.service.RunBalanceReconciliation(ctx); err != nil {
		logger.Error().Err(err).Msg("reconcile: balance reconciliation failed")
		return err
	}
	logger.Info().Msg("reconcile: balance reconciliation complete")
	return nil
}
