package compliance

import (
	"context"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

// Worker refreshes the OFAC SDN list. It is registered against the periodic
// "compliance:sanctions_refresh" task, which asynq's scheduler enqueues once
// a day on the low-priority queue.
type Worker struct {
	repo     Repository
	source   SDNSource
	set      *SanctionsSet
	webhooks Dispatcher
}

func NewWorker(repo Repository, source SDNSource, set *SanctionsSet, webhooks Dispatcher) *Worker {
	return &Worker{repo: repo, source: source, set: set, webhooks: webhooks}
}

// HandleRefreshSanctions downloads, parses and persists the SDN list, then
// reloads this process's in-memory set.
//
// A failed refresh leaves the previous list in place: screening against a
// slightly stale list is far better than screening against an empty one,
// which would clear every transfer.
func (w *Worker) HandleRefreshSanctions(ctx context.Context, _ *asynq.Task) error {
	started := time.Now().UTC()

	entities, err := w.refresh(ctx)
	finished := time.Now().UTC()

	update := &domain.SanctionsUpdate{
		ID:         uuid.New().String(),
		Source:     SourceOFAC,
		StartedAt:  started,
		FinishedAt: finished,
		DurationMS: finished.Sub(started).Milliseconds(),
	}

	if err != nil {
		update.Status = domain.SanctionsUpdateFailed
		update.Error = err.Error()
		if recErr := w.repo.RecordSanctionsUpdate(ctx, update); recErr != nil {
			log.Error().Err(recErr).Msg("compliance: failed to record sanctions update audit row")
		}

		log.Error().Err(err).
			Int64("duration_ms", update.DurationMS).
			Msg("compliance: sanctions refresh failed")

		if w.webhooks != nil {
			if dErr := w.webhooks.Dispatch(ctx, domain.EventSanctionsRefreshFailed, map[string]interface{}{
				"source":      SourceOFAC,
				"error":       err.Error(),
				"duration_ms": update.DurationMS,
				"failed_at":   finished.Format(time.RFC3339),
			}); dErr != nil {
				log.Error().Err(dErr).Msg("compliance: sanctions refresh_failed webhook dispatch failed")
			}
		}

		return err
	}

	update.Status = domain.SanctionsUpdateSuccess
	update.EntityCount = entities
	if err := w.repo.RecordSanctionsUpdate(ctx, update); err != nil {
		log.Error().Err(err).Msg("compliance: failed to record sanctions update audit row")
	}

	log.Info().
		Int("entity_count", entities).
		Int64("duration_ms", update.DurationMS).
		Msg("compliance: sanctions refresh complete")

	return nil
}

func (w *Worker) refresh(ctx context.Context) (int, error) {
	body, err := w.source.Fetch(ctx)
	if err != nil {
		// SDNSource already names the operation; wrapping again here would
		// produce "fetch sdn list: fetch sdn list: ...".
		return 0, err
	}
	defer body.Close()

	entities, err := ParseSDN(body)
	if err != nil {
		return 0, fmt.Errorf("parse sdn list: %w", err)
	}
	if len(entities) == 0 {
		// An empty parse is far more likely to be a malformed download than a
		// genuinely empty SDN list, and applying it would clear every screen.
		return 0, fmt.Errorf("sdn list parsed to zero entities; refusing to replace the existing list")
	}

	refreshedAt := time.Now().UTC()
	written, err := w.repo.ReplaceSanctionsEntities(ctx, entities, refreshedAt)
	if err != nil {
		return 0, fmt.Errorf("persist sdn entities: %w", err)
	}

	// Refresh this process's own set immediately; other processes pick the
	// change up from the table on their reload ticker.
	if w.set != nil {
		w.set.Replace(entities, refreshedAt)
	}

	return written, nil
}
