package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

type Worker struct {
	repo        Repository
	transferSvc transfer.Service
}

func NewWorker(repo Repository, transferSvc transfer.Service) *Worker {
	return &Worker{repo: repo, transferSvc: transferSvc}
}

// HandleRunSchedules is registered against the periodic "schedule:run" task,
// which asynq's scheduler enqueues every minute (see cmd/worker/main.go). It
// fires every due, active schedule and advances next_run_at.
func (w *Worker) HandleRunSchedules(ctx context.Context, _ *asynq.Task) error {
	due, err := w.repo.ListDue(ctx, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("list due schedules: %w", err)
	}

	for _, sch := range due {
		w.runOne(ctx, sch)
	}
	return nil
}

func (w *Worker) runOne(ctx context.Context, sch *domain.Schedule) {
	runCtx := ctx
	if sch.TenantID != nil {
		runCtx = tenant.WithID(ctx, *sch.TenantID)
	}

	if _, err := w.transferSvc.InitiateTransfer(runCtx, sch.FromWallet, sch.ToWallet, sch.Asset, sch.Amount); err != nil {
		log.Error().Err(err).Str("schedule_id", sch.ID).Msg("scheduled transfer failed to initiate")
	}

	sch.NextRunAt = AddInterval(sch.NextRunAt, sch.Frequency)
	if sch.EndAt != nil && sch.NextRunAt.After(*sch.EndAt) {
		sch.Status = domain.ScheduleStatusCompleted
	}
	sch.UpdatedAt = time.Now().UTC()

	if err := w.repo.Update(ctx, sch); err != nil {
		log.Error().Err(err).Str("schedule_id", sch.ID).Msg("failed to advance schedule next_run_at")
	}
}
