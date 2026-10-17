package transferapproval

import (
	"context"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

type Worker struct {
	svc *Service
}

func NewWorker(svc *Service) *Worker {
	return &Worker{svc: svc}
}

// HandleExpiry is registered against the periodic "transfer_approval:expire"
// task (see cmd/worker/main.go). Expiry used to happen only when someone listed
// approvals or voted on the request, so an unattended request could sit in
// approval_pending forever.
func (w *Worker) HandleExpiry(ctx context.Context, _ *asynq.Task) error {
	report, err := w.svc.ExpireDue(ctx)
	if err != nil {
		return err
	}
	log.Info().
		Int("expired", report.Expired).
		Int("transactions_failed", report.TransactionsFailed).
		Msg("transferapproval: expiry sweep complete")
	return nil
}
