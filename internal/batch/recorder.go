package batch

import (
	"context"
	"errors"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/rs/zerolog"
)

// Dispatcher is the webhook service's dispatch entry point.
type Dispatcher interface {
	Dispatch(ctx context.Context, eventType domain.EventType, payload interface{}) error
}

// Recorder is attached to the paths that settle a batch's children
// (settlement, reconciliation). After a child reaches a terminal state it
// syncs the parent batch and, when the batch crosses into a terminal status,
// emits batch.completed or batch.partially_failed exactly once — the row lock
// inside SyncAggregates guarantees a single caller observes that crossing.
type Recorder struct {
	repo     Repository
	webhooks Dispatcher
}

func NewRecorder(repo Repository, webhooks Dispatcher) *Recorder {
	return &Recorder{repo: repo, webhooks: webhooks}
}

// SyncBatch refreshes the aggregates of batchID and dispatches the
// batch-level webhook on the terminal transition. It is a no-op for a
// transaction that is not part of a batch (empty batchID) or whose batch has
// been deleted, and it is safe to call concurrently from many workers.
func (r *Recorder) SyncBatch(ctx context.Context, batchID string) error {
	if r == nil || batchID == "" {
		return nil
	}
	b, transitioned, err := r.repo.SyncAggregates(ctx, batchID)
	if err != nil {
		if errors.Is(err, domain.ErrBatchNotFound) {
			// The batch row is gone (tenant teardown, ON DELETE SET NULL on
			// the children) — there is nothing left to summarise.
			return nil
		}
		return err
	}
	if !transitioned || r.webhooks == nil {
		return nil
	}

	// Every terminal batch fires exactly one event: a fully successful one as
	// batch.completed, a mixed outcome as batch.partially_failed. An
	// all-failed batch still completed processing, so it fires
	// batch.completed with status "failed" for subscribers to branch on.
	event := domain.EventType(domain.EventBatchCompleted)
	if b.Status == domain.BatchStatusPartial {
		event = domain.EventType(domain.EventBatchPartiallyFailed)
	}

	payload := map[string]interface{}{
		"batch_id":        b.ID,
		"event":           string(event),
		"status":          string(b.Status),
		"total_count":     b.TotalCount,
		"succeeded_count": b.SucceededCount,
		"failed_count":    b.FailedCount,
	}
	if b.SettledAt != nil {
		payload["settled_at"] = b.SettledAt.Format(time.RFC3339)
	}

	if err := r.webhooks.Dispatch(ctx, event, payload); err != nil {
		// The batch state is already committed, so this must not fail the
		// settlement task that triggered the sync — retrying it could not
		// re-emit the event anyway, because the terminal transition has been
		// consumed. Log loudly instead.
		zerolog.Ctx(ctx).Error().Err(err).
			Str("batch_id", b.ID).
			Str("event", string(event)).
			Msg("batch: webhook dispatch failed")
	}
	return nil
}
