package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Dispatcher handles synchronous dispatching converted to asynchronous queueing
// with SSRF-protected HTTP clients.
type Dispatcher struct {
	svc         Service
	queueClient *queue.Client
	client      *http.Client
}

// NewDispatcher creates a new Webhook Dispatcher.
func NewDispatcher(svc Service, queueClient *queue.Client, client *http.Client) *Dispatcher {
	return &Dispatcher{
		svc:         svc,
		queueClient: queueClient,
		client:      client,
	}
}

// Dispatch enqueues a webhook delivery asynchronously, removing it from the caller's request path.
func (d *Dispatcher) Dispatch(ctx context.Context, eventType string, payload interface{}) error {
	// Find subscriptions matching this event type
	subs, err := d.svc.(*service).repo.GetSubscriptionsForEvent(ctx, nil, eventType)
	if err != nil {
		return fmt.Errorf("get subscriptions for event %q: %w", eventType, err)
	}
	if len(subs) == 0 {
		return nil
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	now := time.Now().UTC()
	for _, sub := range subs {
		// Pre-validate the URL via SSRF guard before queueing
		if err := d.svc.(*service).validateWebhookURL(ctx, sub.WebhookURL); err != nil {
			log.Error().Err(err).Str("webhook_url", sub.WebhookURL).Msg("webhook: skipping delivery to unsafe URL")
			continue
		}

		delivery := &domain.WebhookDelivery{
			ID:           uuid.New().String(),
			EndpointID:   sub.ID,
			EventType:    eventType,
			Payload:      string(payloadBytes),
			Status:       string(domain.DeliveryPending),
			AttemptCount: 0,
			MaxAttempts:  5,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		if err := d.svc.(*service).repo.CreateDelivery(ctx, delivery); err != nil {
			log.Error().Err(err).Str("delivery_id", delivery.ID).Msg("webhook: failed to create delivery record")
			continue
		}

		if _, err := d.queueClient.EnqueueWebhookDelivery(ctx, delivery.ID); err != nil {
			log.Error().Err(err).Str(
				"delivery_id", delivery.ID,
			).Msg("webhook: failed to enqueue delivery")
		}
	}

	return nil
}
