package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
)

type Dispatcher struct {
	repo   *postgres.WebhookRepository
	client *http.Client
}

func NewDispatcher(repo *postgres.WebhookRepository) *Dispatcher {
	return &Dispatcher{
		repo: repo,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

func (d *Dispatcher) Dispatch(ctx context.Context, tenantID *string, eventType string, payloadObj interface{}) error {
	payloadBytes, err := json.Marshal(payloadObj)
	if err != nil {
		return fmt.Errorf("marshal webhook payload: %w", err)
	}
	payloadStr := string(payloadBytes)

	// Check if there are specific event type subscriptions for this tenant / globally
	subs, err := d.repo.GetSubscriptionsForEvent(ctx, tenantID, eventType)
	if err != nil {
		log.Ctx(ctx).Error().Err(err).Str("event_type", eventType).Msg("failed to get subscriptions for event")
	}

	var endpoints []*domain.WebhookEndpoint
	if len(subs) > 0 {
		// Deliver only to endpoints whose URL matches the subscription
		allEndpoints, err := d.repo.ListEndpoints(ctx, tenantID)
		if err == nil {
			subURLMap := make(map[string]bool)
			for _, sub := range subs {
				subURLMap[sub.WebhookURL] = true
			}
			for _, ep := range allEndpoints {
				if subURLMap[ep.URL] {
					endpoints = append(endpoints, ep)
				}
			}
		}
	} else {
		// Default behavior (no subscriptions for this event): deliver to all active endpoints for backward compatibility
		allEndpoints, err := d.repo.ListEndpoints(ctx, tenantID)
		if err != nil {
			return fmt.Errorf("list webhook endpoints: %w", err)
		}
		for _, ep := range allEndpoints {
			if !ep.Active {
				continue
			}
			// If endpoint has specific events configured, verify match
			if len(ep.Events) > 0 {
				matched := false
				for _, ev := range ep.Events {
					if ev == eventType {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
			}
			endpoints = append(endpoints, ep)
		}
	}

	for _, ep := range endpoints {
		if !ep.Active {
			continue
		}
		if err := d.sendToEndpoint(ctx, ep, eventType, payloadStr); err != nil {
			log.Ctx(ctx).Error().Err(err).Str("endpoint_id", ep.ID).Str("event_type", eventType).Msg("failed to deliver webhook")
		}
	}

	return nil
}

func (d *Dispatcher) sendToEndpoint(ctx context.Context, ep *domain.WebhookEndpoint, eventType, payload string) error {
	deliveryID := uuid.New().String()
	now := time.Now().UTC()

	delivery := &domain.WebhookDelivery{
		ID:           deliveryID,
		EndpointID:   ep.ID,
		TenantID:     ep.TenantID,
		EventType:    eventType,
		Payload:      payload,
		Status:       "pending",
		AttemptCount: 1,
		MaxAttempts:  3,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := d.repo.CreateDelivery(ctx, delivery); err != nil {
		return fmt.Errorf("create delivery record: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", ep.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Event", eventType)

	if ep.Secret != "" {
		mac := hmac.New(sha256.New, []byte(ep.Secret))
		mac.Write([]byte(payload))
		sig := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-Webhook-Signature", sig)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		delivery.Status = "failed"
		delivery.ErrorMessage = err.Error()
		nowTime := time.Now().UTC()
		delivery.LastAttempt = &nowTime
		delivery.UpdatedAt = nowTime
		_ = d.repo.UpdateDelivery(ctx, delivery)
		return err
	}
	defer resp.Body.Close()

	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyStr := string(bodyBytes)

	nowTime := time.Now().UTC()
	delivery.LastAttempt = &nowTime
	delivery.ResponseCode = resp.StatusCode
	delivery.ResponseBody = bodyStr
	delivery.UpdatedAt = nowTime

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		delivery.Status = "success"
		ep.SuccessCount++
		ep.LastDeliveredAt = &nowTime
	} else {
		delivery.Status = "failed"
		delivery.ErrorMessage = fmt.Sprintf("non-2xx status code: %d", resp.StatusCode)
		ep.FailureCount++
	}

	ep.UpdatedAt = nowTime
	_ = d.repo.UpdateEndpoint(ctx, ep)
	_ = d.repo.UpdateDelivery(ctx, delivery)

	return nil
}
