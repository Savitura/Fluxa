package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
)

// Repository defines storage operations for webhooks.
type Repository interface {
	Create(ctx context.Context, ep *domain.WebhookEndpoint) error
	GetByID(ctx context.Context, id string) (*domain.WebhookEndpoint, error)
	List(ctx context.Context, tenantID *string) ([]*domain.WebhookEndpoint, error)
	Delete(ctx context.Context, id string) error
	ListActiveByEvent(ctx context.Context, eventType string) ([]*domain.WebhookEndpoint, error)
	CreateDelivery(ctx context.Context, d *domain.WebhookDelivery) error
	UpdateDelivery(ctx context.Context, d *domain.WebhookDelivery) error
	GetDeliveryByID(ctx context.Context, id string) (*domain.WebhookDelivery, error)
	ListDeliveries(ctx context.Context, endpointID string, limit, offset int) ([]*domain.WebhookDelivery, error)
}

// Service exposes webhook management and dispatch operations.
type Service interface {
	Register(ctx context.Context, url string, events []string) (*domain.WebhookEndpoint, error)
	List(ctx context.Context) ([]*domain.WebhookEndpoint, error)
	Delete(ctx context.Context, id string) error
	ListDeliveries(ctx context.Context, endpointID string, limit, offset int) ([]*domain.WebhookDelivery, error)
	Dispatch(ctx context.Context, eventType domain.EventType, payload interface{}) error
	Deliver(ctx context.Context, deliveryID string) error
}

type service struct {
	repo   Repository
	queue  *queue.Client
	client *http.Client
}

func NewService(repo Repository, q *queue.Client) Service {
	return &service{
		repo:   repo,
		queue:  q,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *service) Register(ctx context.Context, url string, events []string) (*domain.WebhookEndpoint, error) {
	secret, err := generateSecret()
	if err != nil {
		return nil, fmt.Errorf("generate webhook secret: %w", err)
	}

	tenantID := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tenantID != "" {
		tenantPtr = &tenantID
	}

	if events == nil {
		events = []string{}
	}

	ep := &domain.WebhookEndpoint{
		ID:        uuid.New().String(),
		TenantID:  tenantPtr,
		URL:       url,
		Secret:    secret,
		Events:    events,
		Active:    true,
		CreatedAt: time.Now().UTC(),
	}

	if err := s.repo.Create(ctx, ep); err != nil {
		return nil, fmt.Errorf("persist webhook endpoint: %w", err)
	}
	return ep, nil
}

func (s *service) List(ctx context.Context) ([]*domain.WebhookEndpoint, error) {
	tenantID := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tenantID != "" {
		tenantPtr = &tenantID
	}
	return s.repo.List(ctx, tenantPtr)
}

func (s *service) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

func (s *service) ListDeliveries(ctx context.Context, endpointID string, limit, offset int) ([]*domain.WebhookDelivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.repo.ListDeliveries(ctx, endpointID, limit, offset)
}

// Dispatch creates delivery records for all active endpoints subscribed to eventType,
// then enqueues async delivery tasks.
func (s *service) Dispatch(ctx context.Context, eventType domain.EventType, payload interface{}) error {
	endpoints, err := s.repo.ListActiveByEvent(ctx, string(eventType))
	if err != nil {
		return fmt.Errorf("list endpoints for event %s: %w", eventType, err)
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}

	for _, ep := range endpoints {
		delivery := &domain.WebhookDelivery{
			ID:           uuid.New().String(),
			EndpointID:   ep.ID,
			EventType:    eventType,
			Payload:      body,
			Status:       domain.DeliveryPending,
			AttemptCount: 0,
			CreatedAt:    time.Now().UTC(),
		}
		if err := s.repo.CreateDelivery(ctx, delivery); err != nil {
			return fmt.Errorf("create delivery record: %w", err)
		}
		if s.queue != nil {
			if err := s.queue.EnqueueWebhookDelivery(ctx, delivery.ID); err != nil {
				// Delivery is persisted; worker will handle it on next run.
				_ = err
			}
		}
	}
	return nil
}

// Deliver performs the actual HTTP POST for a delivery record.
func (s *service) Deliver(ctx context.Context, deliveryID string) error {
	// We need to look up the delivery — fetch via a dedicated repo method or scan deliveries.
	// For simplicity, use a helper that finds delivery by ID.
	delivery, ep, err := s.loadDelivery(ctx, deliveryID)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	delivery.AttemptCount++
	delivery.LastAttempt = &now

	sig := sign(ep.Secret, delivery.Payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(delivery.Payload))
	if err != nil {
		delivery.Status = domain.DeliveryFailed
		_ = s.repo.UpdateDelivery(ctx, delivery)
		return fmt.Errorf("build webhook request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fluxa-Signature", sig)
	req.Header.Set("X-Fluxa-Event", string(delivery.EventType))

	resp, err := s.client.Do(req)
	if err != nil {
		delivery.Status = domain.DeliveryFailed
		_ = s.repo.UpdateDelivery(ctx, delivery)
		return fmt.Errorf("deliver webhook: %w", err)
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	delivery.ResponseCode = &code
	if code >= 200 && code < 300 {
		delivery.Status = domain.DeliverySuccess
	} else {
		delivery.Status = domain.DeliveryFailed
	}

	if err := s.repo.UpdateDelivery(ctx, delivery); err != nil {
		return fmt.Errorf("update delivery record: %w", err)
	}
	return nil
}

func (s *service) loadDelivery(ctx context.Context, deliveryID string) (*domain.WebhookDelivery, *domain.WebhookEndpoint, error) {
	delivery, err := s.repo.GetDeliveryByID(ctx, deliveryID)
	if err != nil {
		return nil, nil, fmt.Errorf("load delivery: %w", err)
	}
	ep, err := s.repo.GetByID(ctx, delivery.EndpointID)
	if err != nil {
		return nil, nil, fmt.Errorf("load endpoint: %w", err)
	}
	return delivery, ep, nil
}

func sign(secret string, payload []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func generateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
