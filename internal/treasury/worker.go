package treasury

import (
	"context"

	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/hibiken/asynq"
	"github.com/shopspring/decimal"
)

type Worker struct {
	svc Service
}

func NewWorker(svc Service) *Worker {
	return &Worker{svc: svc}
}

// HandleSweep is registered against the periodic "treasury:sweep" task,
// which asynq's scheduler enqueues once a day (see cmd/worker/main.go). For
// every asset with auto-sweep enabled it sweeps whatever is above the
// configured threshold to cold storage; ExecuteSweep always writes a
// sweep_log row, even a zero-amount one, so every run leaves an audit trail.
// Assets with auto_sweep_enabled = false are skipped entirely — flipping
// that flag off halts the sweeper for that asset on the very next run.
func (w *Worker) HandleSweep(ctx context.Context, task *asynq.Task) error {
	ctx = queue.ContextFromTask(ctx, task)
	ctx, span := tracing.StartConsumer(ctx, task.Type())
	defer span.End()

	configs, err := w.svc.GetConfig(ctx)
	if err != nil {
		return err
	}

	// Log through the context so every entry carries the inherited trace_id.
	logger := tracing.Logger(ctx)

	for _, cfg := range configs {
		if !cfg.AutoSweepEnabled {
			continue
		}

		sweepable, err := w.svc.GetSweepableAmount(ctx, cfg.Asset)
		if err != nil {
			logger.Error().Err(err).Str("asset", cfg.Asset).Msg("treasury sweep: failed to compute sweepable amount")
			continue
		}

		amount := decimal.Zero
		if sweepable.GreaterThan(cfg.SweepThreshold) {
			amount = sweepable
		}

		if _, err := w.svc.ExecuteSweep(ctx, cfg.Asset, amount, cfg.ColdStorageAddress, TriggeredByAuto); err != nil {
			logger.Error().Err(err).Str("asset", cfg.Asset).Msg("treasury sweep failed")
		}
	}

	return nil
}
