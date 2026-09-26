package webhook

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/hibiken/asynq"
)

type Worker struct {
	svc Service
}

func NewWorker(svc Service) *Worker {
	return &Worker{svc: svc}
}

func (w *Worker) HandleDeliver(ctx context.Context, t *asynq.Task) error {
	ctx = queue.ContextFromTask(ctx, t)
	ctx, span := tracing.StartConsumer(ctx, t.Type())
	defer span.End()

	var payload queue.WebhookDeliverPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal webhook deliver payload: %w", err)
	}
	if payload.Config {
		configSvc, ok := w.svc.(ConfigService)
		if !ok {
			return fmt.Errorf("tenant webhook config service is unavailable")
		}
		return configSvc.DeliverConfig(ctx, payload.DeliveryID, payload.TenantID)
	}
	return w.svc.Deliver(ctx, payload.DeliveryID)
}
