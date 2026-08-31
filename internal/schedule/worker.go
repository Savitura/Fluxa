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
	// Legacy schedules created before the recurrence system existed may carry a
	// zero (nil) `next_run_at`. We cannot claim or advance from a zero time, so
	// recalculate it from the frequency definition, making the entry due now.
	// Without this the worker would dereference/advance a nil time and panic.
	if sch.NextRunAt.IsZero() {
		sch.NextRunAt = time.Now().UTC()
	}

	// Atomically claim the schedule
	claimed, err := w.repo.Claim(ctx, sch.ID, sch.NextRunAt)
	if err != nil {
		log.Error().Err(err).Str("schedule_id", sch.ID).Msg("failed to claim schedule")
		return
	}
	if !claimed {
		// Another worker claimed it or it is no longer due/active
		return
	}

	runCtx := ctx
	if sch.TenantID != nil {
		runCtx = tenant.WithID(ctx, *sch.TenantID)
	}

	if _, err := w.transferSvc.InitiateTransfer(runCtx, sch.FromWallet, sch.ToWallet, sch.Asset, sch.Amount); err != nil {
		log.Error().Err(err).Str("schedule_id", sch.ID).Msg("scheduled transfer failed to initiate")
		// Fail the schedule to avoid blind advancement and skipping occurrences
		sch.Status = domain.ScheduleStatusFailed
		sch.UpdatedAt = time.Now().UTC()
		if updateErr := w.repo.Update(ctx, sch); updateErr != nil {
			log.Error().Err(updateErr).Str("schedule_id", sch.ID).Msg("failed to update schedule to failed status")
		}
		return
	}

	sch.NextRunAt = AddInterval(sch.NextRunAt, sch.Frequency)
	sch.Status = domain.ScheduleStatusActive // reset to active from processing
	if sch.EndAt != nil && sch.NextRunAt.After(*sch.EndAt) {
		sch.Status = domain.ScheduleStatusCompleted
	}
	sch.UpdatedAt = time.Now().UTC()

	if err := w.repo.Update(ctx, sch); err != nil {
		log.Error().Err(err).Str("schedule_id", sch.ID).Msg("failed to advance schedule next_run_at")
	}
}
