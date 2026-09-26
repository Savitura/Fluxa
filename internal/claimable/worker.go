package claimable

import (
	"context"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

type Worker struct {
	svc Service
}

func NewWorker(svc Service) *Worker {
	return &Worker{svc: svc}
}

// HandleExpiry is registered against the periodic "claimable:expire" task,
// which asynq's scheduler enqueues every five minutes (see cmd/worker/main.go).
// Each pass marks the unclaimed balances whose expiry has passed and, for the
// ones flagged revoke_on_expiry, claims them back to a custodied claimant so
// the funds do not stay stranded on the ledger.
func (w *Worker) HandleExpiry(ctx context.Context, _ *asynq.Task) error {
	report, err := w.svc.ProcessExpired(ctx)
	if err != nil {
		return err
	}

	log.Info().
		Int("scanned", report.Scanned).
		Int("expired", report.Expired).
		Int("revoked", report.Revoked).
		Msg("claimable: expiry sweep complete")
	return nil
}
