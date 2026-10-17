package paymentlink

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog/log"
)

type Worker struct {
	svc     *Service
	timeout time.Duration
}

func NewWorker(svc *Service, checkoutTimeout time.Duration) *Worker {
	return &Worker{svc: svc, timeout: checkoutTimeout}
}

// HandleReleaseStale is registered against the periodic
// "payment_link:release_stale" task (see cmd/worker/main.go).
func (w *Worker) HandleReleaseStale(ctx context.Context, _ *asynq.Task) error {
	released, err := w.svc.ReleaseAbandoned(ctx, w.timeout)
	if err != nil {
		return err
	}
	log.Info().Int("released", released).Dur("checkout_timeout", w.timeout).
		Msg("paymentlink: abandoned checkout sweep complete")
	return nil
}
