package webhook

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

// Dispatcher enqueues webhook deliveries asynchronously, keeping the delivery
// off the caller's request path and pre-validating each destination through the
// service's SSRF guard before a delivery is queued.
type Dispatcher struct {
	svc         Service
	queueClient *queue.Client
}

// NewDispatcher creates a new Webhook Dispatcher.
func NewDispatcher(svc Service, queueClient *queue.Client) *Dispatcher {
	return &Dispatcher{svc: svc, queueClient: queueClient}
}

// Dispatch records one delivery per active, subscribed endpoint for the event
// and queues it. A destination that fails URL validation is skipped with a log
// line rather than failing the whole event: one bad endpoint must not stop
// deliveries to the tenant's other endpoints.
func (d *Dispatcher) Dispatch(ctx context.Context, eventType string, payload interface{}) error {
	if d.svc == nil {
		return nil
	}
	svc, ok := d.svc.(*service)
	if !ok {
		return d.svc.Dispatch(ctx, domain.EventType(eventType), payload)
	}

	tid := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tid != "" {
		tenantPtr = &tid
	}
	endpoints, err := svc.repo.ListEndpoints(ctx, tenantPtr)
	if err != nil {
		return fmt.Errorf("list webhook endpoints for event %q: %w", eventType, err)
	}
	if len(endpoints) == 0 {
		return nil
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}

	now := time.Now().UTC()
	for _, ep := range endpoints {
		if !ep.Active || !endpointSubscribedTo(ep, eventType) {
			continue
		}
		if err := svc.validateWebhookURL(ctx, ep.URL); err != nil {
			log.Error().Err(err).Str("endpoint_url", ep.URL).Msg("webhook: skipping delivery to unsafe URL")
			continue
		}

		delivery := &domain.WebhookDelivery{
			ID:           uuid.New().String(),
			EndpointID:   ep.ID,
			TenantID:     ep.TenantID,
			EventType:    eventType,
			Payload:      string(payloadBytes),
			Status:       "pending",
			AttemptCount: 0,
			MaxAttempts:  svc.maxAttempts,
			CreatedAt:    now,
			UpdatedAt:    now,
		}
		if err := svc.repo.CreateDelivery(ctx, delivery); err != nil {
			log.Error().Err(err).Str("delivery_id", delivery.ID).Msg("webhook: failed to create delivery record")
			continue
		}
		if d.queueClient == nil {
			continue
		}
		if _, err := d.queueClient.EnqueueWebhookDelivery(ctx, delivery.ID); err != nil {
			log.Error().Err(err).Str("delivery_id", delivery.ID).Msg("webhook: failed to enqueue delivery")
		}
	}
	return nil
}

// endpointSubscribedTo reports whether an endpoint wants the event. An empty
// subscription list means every event, matching the service's dispatch
// semantics.
func endpointSubscribedTo(ep *domain.WebhookEndpoint, eventType string) bool {
	if len(ep.Events) == 0 {
		return true
	}
	for _, event := range ep.Events {
		if event == eventType || event == "*" {
			return true
		}
	}
	return false
}
