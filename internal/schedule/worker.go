package schedule

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

// runIDempotencyKey builds a stable idempotency key for
// InitiateTransferIdempotent from the schedule identity and occurrence time.
// The time is truncated to seconds to match the TIMESTAMPTZ precision stored
// in the database's UNIQUE constraint.
func runIDempotencyKey(scheduleID string, expectedAt time.Time) string {
	return fmt.Sprintf("sched:%s:%d", scheduleID, expectedAt.UTC().Truncate(time.Second).Unix())
}

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
func (w *Worker) HandleRunSchedules(ctx context.Context, task *asynq.Task) error {
	ctx = queue.ContextFromTask(ctx, task)
	ctx, span := tracing.StartConsumer(ctx, task.Type())
	defer span.End()

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
	if sch.NextRunAt.IsZero() {
		sch.NextRunAt = time.Now().UTC()
	}

	// Record the expected occurrence before claiming — this is the value that
	// the UNIQUE constraint and idempotency key are both keyed on.
	expectedAt := sch.NextRunAt.UTC().Truncate(time.Second)

	// -----------------------------------------------------------------------
	// Step 1: Claim the schedule row (CAS: active → processing).
	// Only the winner of this atomic UPDATE proceeds; a concurrent worker that
	// also picked up this row finds it in 'processing' and skips it.
	// -----------------------------------------------------------------------
	claimed, err := w.repo.Claim(ctx, sch.ID, sch.NextRunAt)
	if err != nil {
		log.Error().Err(err).Str("schedule_id", sch.ID).Msg("failed to claim schedule")
		return
	}
	if !claimed {
		// Another worker already claimed this occurrence.
		return
	}

	// Build a tenant-scoped context for all downstream calls.
	runCtx := ctx
	if sch.TenantID != nil {
		runCtx = tenant.WithID(ctx, *sch.TenantID)
	}

	// -----------------------------------------------------------------------
	// Step 2: Create (or retrieve) a durable run record for this occurrence.
	//
	// INSERT … ON CONFLICT DO NOTHING atomically ensures only one record is
	// written for (schedule_id, expected_run_at). If a previous attempt
	// crashed after inserting the run record, we retrieve it here and inspect
	// its current status to decide whether to continue.
	//
	// Terminal states (succeeded / failed / skipped / cancelled):
	//   Already handled — just advance the schedule.
	// Non-terminal states (pending / running):
	//   Proceed with payout initiation using the stable idempotency key;
	//   InitiateTransferIdempotent returns the existing transaction if one was
	//   already created with that key, preventing a duplicate payout.
	// -----------------------------------------------------------------------
	run, err := w.repo.ClaimRun(runCtx, sch.ID, sch.TenantID, expectedAt)
	if err != nil {
		log.Error().Err(err).
			Str("schedule_id", sch.ID).
			Time("expected_at", expectedAt).
			Msg("failed to claim run record")
		// Revert schedule so it retries on the next tick.
		w.revertSchedule(ctx, sch)
		return
	}

	// Skip occurrences that already reached a terminal state.
	switch run.Status {
	case domain.ScheduleRunStatusSucceeded,
		domain.ScheduleRunStatusFailed,
		domain.ScheduleRunStatusSkipped,
		domain.ScheduleRunStatusCancelled:
		w.advanceSchedule(ctx, sch)
		return
	}

	// -----------------------------------------------------------------------
	// Step 3: Mark the run as running so operators can detect stuck runs.
	// A run stuck in 'running' for an extended period indicates the worker
	// crashed between payout initiation and result recording; the idempotency
	// key allows safe recovery on the next retry.
	// -----------------------------------------------------------------------
	now := time.Now().UTC()
	run.Status = domain.ScheduleRunStatusRunning
	run.StartedAt = &now
	if updateErr := w.repo.UpdateRun(runCtx, run); updateErr != nil {
		log.Error().Err(updateErr).
			Str("schedule_id", sch.ID).
			Str("run_id", run.ID).
			Msg("failed to mark run as running; proceeding anyway")
		// Non-fatal: proceed; the run stays 'pending', which is safe.
	}

	// -----------------------------------------------------------------------
	// Step 4: Initiate the transfer using a stable idempotency key.
	//
	// The key is derived from (schedule_id, expected_run_at), so:
	//   - First attempt  → creates a new transaction.
	//   - Retry after crash (run was 'running') → returns the same transaction,
	//     preventing a duplicate payout.
	//
	// Known limitation: if the worker crashes during InitiateTransfer *before*
	// the transaction row is persisted to the database, there is no idempotency
	// record yet.  The next retry will create a new transaction — this is the
	// correct behaviour because the previous attempt cannot have succeeded.
	// -----------------------------------------------------------------------
	idempKey := runIDempotencyKey(sch.ID, expectedAt)
	tx, transferErr := w.transferSvc.InitiateTransferIdempotent(
		runCtx, sch.FromWallet, sch.ToWallet, sch.Asset, sch.Amount, idempKey,
	)

	// -----------------------------------------------------------------------
	// Step 5: Record the outcome on the run record.
	// -----------------------------------------------------------------------
	completedAt := time.Now().UTC()

	if transferErr != nil {
		errMsg := transferErr.Error()
		run.Status = domain.ScheduleRunStatusFailed
		run.Error = &errMsg
		run.CompletedAt = &completedAt
		if updateErr := w.repo.UpdateRun(runCtx, run); updateErr != nil {
			log.Error().Err(updateErr).
				Str("schedule_id", sch.ID).
				Str("run_id", run.ID).
				Msg("failed to record run failure")
		}

		log.Error().Err(transferErr).
			Str("schedule_id", sch.ID).
			Str("run_id", run.ID).
			Msg("scheduled transfer failed to initiate")

		// Mark the schedule as failed so it does not silently advance past this
		// occurrence without a visible record.
	now := time.Now().UTC()
	nextRun := AddInterval(sch.NextRunAt, sch.Frequency, sch.Timezone)
	isMissedCycle := !nextRun.After(now)

	if isMissedCycle && sch.MissedRunPolicy == domain.MissedRunPolicySkip {
		for !nextRun.After(now) {
			nextRun = AddInterval(nextRun, sch.Frequency, sch.Timezone)
		}
		sch.NextRunAt = nextRun
		sch.Status = domain.ScheduleStatusActive
		if sch.EndAt != nil && sch.NextRunAt.After(*sch.EndAt) {
			sch.Status = domain.ScheduleStatusCompleted
		}
		sch.UpdatedAt = time.Now().UTC()
		if updateErr := w.repo.Update(ctx, sch); updateErr != nil {
			log.Error().Err(updateErr).Str("schedule_id", sch.ID).Msg("failed to update skipped schedule")
		}
		return
	}

	if _, err := w.transferSvc.InitiateTransfer(runCtx, sch.FromWallet, sch.ToWallet, sch.Asset, sch.Amount); err != nil {
		log.Error().Err(err).Str("schedule_id", sch.ID).Msg("scheduled transfer failed to initiate")
		// Fail the schedule to avoid blind advancement and skipping occurrences
		sch.Status = domain.ScheduleStatusFailed
		sch.UpdatedAt = time.Now().UTC()
		if updateErr := w.repo.Update(ctx, sch); updateErr != nil {
			log.Error().Err(updateErr).
				Str("schedule_id", sch.ID).
				Msg("failed to update schedule to failed status")
		}
		return
	}

	run.Status = domain.ScheduleRunStatusSucceeded
	run.TransactionID = &tx.ID
	run.CompletedAt = &completedAt
	if updateErr := w.repo.UpdateRun(runCtx, run); updateErr != nil {
		log.Error().Err(updateErr).
			Str("schedule_id", sch.ID).
			Str("run_id", run.ID).
			Str("tx_id", tx.ID).
			Msg("failed to record run success; payout was initiated")
		// Non-fatal: the transfer is in flight. Advance the schedule so the
		// next occurrence is not blocked.
	}

	// -----------------------------------------------------------------------
	// Step 6: Advance the schedule to the next occurrence.
	// -----------------------------------------------------------------------
	w.advanceSchedule(ctx, sch)
}

// advanceSchedule computes the next occurrence and writes it to the DB.
// The schedule status is reset to active (it was 'processing') unless the
// schedule has passed its end date, in which case it becomes completed.
func (w *Worker) advanceSchedule(ctx context.Context, sch *domain.Schedule) {
	sch.NextRunAt = AddInterval(sch.NextRunAt, sch.Frequency)
	sch.Status = domain.ScheduleStatusActive
	if isMissedCycle {
		for !nextRun.After(now) {
			nextRun = AddInterval(nextRun, sch.Frequency, sch.Timezone)
		}
	}

	sch.NextRunAt = nextRun
	sch.Status = domain.ScheduleStatusActive // reset to active from processing
	if sch.EndAt != nil && sch.NextRunAt.After(*sch.EndAt) {
		sch.Status = domain.ScheduleStatusCompleted
	}
	sch.UpdatedAt = time.Now().UTC()
	if err := w.repo.Update(ctx, sch); err != nil {
		log.Error().Err(err).
			Str("schedule_id", sch.ID).
			Msg("failed to advance schedule next_run_at")
	}
}

// revertSchedule resets a schedule from 'processing' back to 'active' so it
// is retried on the next worker tick.  Called when a non-transfer error
// prevents normal execution.
func (w *Worker) revertSchedule(ctx context.Context, sch *domain.Schedule) {
	sch.Status = domain.ScheduleStatusActive
	sch.UpdatedAt = time.Now().UTC()
	if err := w.repo.Update(ctx, sch); err != nil {
		log.Error().Err(err).
			Str("schedule_id", sch.ID).
			Msg("failed to revert schedule to active status")
	}
}
