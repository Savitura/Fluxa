package webhook

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fluxa/fluxa/internal/queue"
	"github.com/hibiken/asynq"
)

type Worker struct {
	svc Service
}

func NewWorker(svc Service) *Worker {
	return &Worker{svc: svc}
}

func (w *Worker) HandleDeliver(ctx context.Context, t *asynq.Task) error {
	var p queue.WebhookDeliverPayload
	if err := json.Unmarshal(t.Payload(), &p); err != nil {
		return fmt.Errorf("unmarshal webhook deliver payload: %w", err)
	}
	return w.svc.Deliver(ctx, p.DeliveryID)
}
