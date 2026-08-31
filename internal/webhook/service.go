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
	"strconv"
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
	Delete(ctx context.Context, id string, tenantID *string) error
	ListActiveByEvent(ctx context.Context, eventType string) ([]*domain.WebhookEndpoint, error)
	CreateDelivery(ctx context.Context, d *domain.WebhookDelivery) error
	UpdateDelivery(ctx context.Context, d *domain.WebhookDelivery) error
	GetDeliveryByID(ctx context.Context, id string, tenantID *string) (*domain.WebhookDelivery, error)
	ListDeliveries(ctx context.Context, endpointID string, limit, offset int, tenantID *string) ([]*domain.WebhookDelivery, error)
	CountByTenant(ctx context.Context, tenantID string) (int, error)
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

type TenantGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Tenant, error)
}

type service struct {
	repo       Repository
	queue      *queue.Client
	client     *http.Client
	tenantRepo TenantGetter

	// allowPrivateNetworks disables SSRF destination checks. It only exists
	// so tests can target httptest servers on loopback addresses; it must
	// never be set outside of tests and NewService never sets it.
	allowPrivateNetworks bool
}

func NewService(repo Repository, q *queue.Client, tenantRepo ...TenantGetter) Service {
	s := &service{
		repo:  repo,
		queue: q,
	}
	s.client = s.newSafeHTTPClient()
	if len(tenantRepo) > 0 {
		s.tenantRepo = tenantRepo[0]
	}
	return s
}

func (s *service) Register(ctx context.Context, url string, events []string) (*domain.WebhookEndpoint, error) {
	if err := s.validateWebhookURL(ctx, url); err != nil {
		return nil, err
	}

	tenantID := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tenantID != "" {
		tenantPtr = &tenantID
		if s.tenantRepo != nil {
			t, err := s.tenantRepo.GetByID(ctx, tenantID)
			if err == nil && t != nil {
				limit := t.GetWebhookLimit()
				if limit > 0 {
					count, err := s.repo.CountByTenant(ctx, tenantID)
					if err == nil && count >= limit {
						return nil, domain.ErrWebhookLimitReached
					}
				}
			}
		}
	}

	secret, err := generateSecret()
	if err != nil {
		return nil, fmt.Errorf("generate webhook secret: %w", err)
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
	tenantID := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tenantID != "" {
		tenantPtr = &tenantID
	}
	return s.repo.Delete(ctx, id, tenantPtr)
}

func (s *service) ListDeliveries(ctx context.Context, endpointID string, limit, offset int) ([]*domain.WebhookDelivery, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	tenantID := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tenantID != "" {
		tenantPtr = &tenantID
	}
	return s.repo.ListDeliveries(ctx, endpointID, limit, offset, tenantPtr)
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
	delivery, ep, err := s.loadDelivery(ctx, deliveryID)
	if err != nil {
		return err
	}

	maxAttempts := 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}

		now := time.Now().UTC()
		delivery.AttemptCount++
		delivery.LastAttempt = &now

		if err := s.validateWebhookURL(ctx, ep.URL); err != nil {
			delivery.Status = domain.DeliveryFailed
			_ = s.repo.UpdateDelivery(ctx, delivery)
			return fmt.Errorf("validate webhook destination: %w", err)
		}

		timestamp := strconv.FormatInt(now.Unix(), 10)
		sig := sign(ep.Secret, timestamp, delivery.Payload)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewReader(delivery.Payload))
		if err != nil {
			delivery.Status = domain.DeliveryFailed
			_ = s.repo.UpdateDelivery(ctx, delivery)
			return fmt.Errorf("build webhook request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Fluxa-Signature", sig)
		req.Header.Set("X-Fluxa-Timestamp", timestamp)
		req.Header.Set("X-Fluxa-Event", string(delivery.EventType))

		resp, reqErr := s.client.Do(req)

		// Determine outcome for this attempt
		if reqErr != nil {
			// Network error, timeout, etc. -> Retryable
			delivery.Status = domain.DeliveryFailed
			// Do not log or fail immediately, just continue to next retry
		} else {
			code := resp.StatusCode
			delivery.ResponseCode = &code
			resp.Body.Close()

			if code >= 200 && code < 300 {
				delivery.Status = domain.DeliverySuccess
				break // Success, exit retry loop
			}
			
			// If not a retryable error (e.g. 400, 401, 403, 404), mark as failed and stop retrying
			// We retry on 5xx and 429
			if code < 500 && code != http.StatusTooManyRequests {
				delivery.Status = domain.DeliveryFailed
				break
			}
			
			delivery.Status = domain.DeliveryFailed // Will be retried
		}
	}

	if err := s.repo.UpdateDelivery(ctx, delivery); err != nil {
		return fmt.Errorf("update delivery record: %w", err)
	}
	return nil
}

func (s *service) loadDelivery(ctx context.Context, deliveryID string) (*domain.WebhookDelivery, *domain.WebhookEndpoint, error) {
	tenantID := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tenantID != "" {
		tenantPtr = &tenantID
	}
	delivery, err := s.repo.GetDeliveryByID(ctx, deliveryID, tenantPtr)
	if err != nil {
		return nil, nil, fmt.Errorf("load delivery: %w", err)
	}
	ep, err := s.repo.GetByID(ctx, delivery.EndpointID)
	if err != nil {
		return nil, nil, fmt.Errorf("load endpoint: %w", err)
	}
	return delivery, ep, nil
}

// sign computes the delivery signature over `timestamp + "." + payload`
// (not the payload alone) so verifiers can reject stale/replayed
// deliveries by checking the timestamp before trusting the signature —
// see docs/webhook-verification for the verification algorithm this
// must match exactly.
func sign(secret, timestamp string, payload []byte) string {
	signedPayload := append([]byte(timestamp+"."), payload...)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(signedPayload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func generateSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
