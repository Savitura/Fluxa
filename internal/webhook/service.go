package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

type Repository interface {
	CreateEndpoint(ctx context.Context, ep *domain.WebhookEndpoint) error
	GetEndpoint(ctx context.Context, id string) (*domain.WebhookEndpoint, error)
	ListEndpoints(ctx context.Context, tenantID *string) ([]*domain.WebhookEndpoint, error)
	UpdateEndpoint(ctx context.Context, ep *domain.WebhookEndpoint) error
	DeleteEndpoint(ctx context.Context, id string) error
	CreateDelivery(ctx context.Context, d *domain.WebhookDelivery) error
	GetDelivery(ctx context.Context, id string) (*domain.WebhookDelivery, error)
	UpdateDelivery(ctx context.Context, d *domain.WebhookDelivery) error
	ListDeliveries(ctx context.Context, endpointID string, limit, offset int) ([]*domain.WebhookDelivery, error)
	CreateDeadLetter(ctx context.Context, dl *domain.WebhookDeadLetter) error
	GetDeadLetter(ctx context.Context, id string) (*domain.WebhookDeadLetter, error)
	ListDeadLetters(ctx context.Context, tenantID *string, limit, offset int) ([]*domain.WebhookDeadLetter, error)
}

type Service interface {
	RegisterEndpoint(ctx context.Context, url string, events []string) (*domain.WebhookEndpoint, string, error)
	ListEndpoints(ctx context.Context) ([]*domain.WebhookEndpoint, error)
	DeleteEndpoint(ctx context.Context, id string) error
	ListDeliveries(ctx context.Context, endpointID string, limit int) ([]*domain.WebhookDelivery, error)
	ListDeadLetters(ctx context.Context, limit int) ([]*domain.WebhookDeadLetter, error)
	ReplayDeadLetter(ctx context.Context, deadLetterID string) error
	GetEndpointHealth(ctx context.Context, endpointID string) (*domain.WebhookHealth, error)
	TriggerEvent(ctx context.Context, eventType string, payload interface{}) error
	Deliver(ctx context.Context, deliveryID string) error
}

type service struct {
	repo               Repository
	rdb                *redis.Client
	client             *http.Client
	queueClient        *queue.Client
	maxPerMinute       int
	maxAttempts        int
	backoffSchedule    []time.Time
}

var DefaultBackoffSchedule = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
}

func NewService(repo Repository, rdb *redis.Client, queueClient *queue.Client, maxPerMinute int) Service {
	if maxPerMinute <= 0 {
		maxPerMinute = 120
	}
	return &service{
		repo:        repo,
		rdb:         rdb,
		client:      &http.Client{Timeout: 10 * time.Second},
		queueClient: queueClient,
		maxPerMinute: maxPerMinute,
		maxAttempts:  len(DefaultBackoffSchedule),
	}
}

func generateSecret() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return "whsec_" + hex.EncodeToString(buf)
}

func (s *service) RegisterEndpoint(ctx context.Context, url string, events []string) (*domain.WebhookEndpoint, string, error) {
	if err := ValidateWebhookURL(url); err != nil {
		return nil, "", err
	}

	tid := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tid != "" {
		tenantPtr = &tid
	}

	if len(events) == 0 {
		events = []string{"transfer.initiated", "transfer.settled", "transfer.failed", "wallet.funded", "conversion.completed"}
	}

	secret := generateSecret()
	ep := &domain.WebhookEndpoint{
		ID:              uuid.New().String(),
		TenantID:        tenantPtr,
		URL:             url,
		Secret:          secret,
		Events:          events,
		Active:          true,
		SuccessCount:    0,
		FailureCount:    0,
		NotifiedFailing: false,
		CreatedAt:       time.Now().UTC(),
		UpdatedAt:       time.Now().UTC(),
	}

	if err := s.repo.CreateEndpoint(ctx, ep); err != nil {
		return nil, "", err
	}
	return ep, secret, nil
}

func (s *service) ListEndpoints(ctx context.Context) ([]*domain.WebhookEndpoint, error) {
	tid := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tid != "" {
		tenantPtr = &tid
	}
	return s.repo.ListEndpoints(ctx, tenantPtr)
}

func (s *service) DeleteEndpoint(ctx context.Context, id string) error {
	return s.repo.DeleteEndpoint(ctx, id)
}

func (s *service) ListDeliveries(ctx context.Context, endpointID string, limit int) ([]*domain.WebhookDelivery, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.repo.ListDeliveries(ctx, endpointID, limit, 0)
}

func (s *service) ListDeadLetters(ctx context.Context, limit int) ([]*domain.WebhookDeadLetter, error) {
	if limit <= 0 {
		limit = 50
	}
	tid := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tid != "" {
		tenantPtr = &tid
	}
	return s.repo.ListDeadLetters(ctx, tenantPtr, limit, 0)
}

func (s *service) ReplayDeadLetter(ctx context.Context, deadLetterID string) error {
	dl, err := s.repo.GetDeadLetter(ctx, deadLetterID)
	if err != nil {
		return err
	}

	// Create a new delivery record and enqueue it immediately
	newDel := &domain.WebhookDelivery{
		ID:           uuid.New().String(),
		EndpointID:   dl.EndpointID,
		TenantID:     dl.TenantID,
		EventType:    "replay",
		Payload:      dl.Payload,
		Status:       "pending",
		AttemptCount: 0,
		MaxAttempts:  s.maxAttempts,
		CreatedAt:    time.Now().UTC(),
		UpdatedAt:    time.Now().UTC(),
	}
	if err := s.repo.CreateDelivery(ctx, newDel);
	err != nil {
		return err
	}

	if s.queueClient != nil {
		_, err = s.queueClient.EnqueueWebhookDeliver(ctx, newDel.ID, asynq.ProcessIn(0))
		return err
	}
	return nil
}

func (s *service) GetEndpointHealth(ctx context.Context, endpointID string) (*domain.WebhookHealth, error) {
	ep, err := s.repo.GetEndpoint(ctx, endpointID)
	if err != nil {
		return nil, err
	}
	return &domain.WebhookHealth{
		EndpointID:      ep.ID,
		URL:             ep.URL,
		SuccessCount:    ep.SuccessCount,
		FailureCount:    ep.FailureCount,
		LastDeliveredAt: ep.LastDeliveredAt,
		Failing:         ep.FailureCount > 0 && ep.SuccessCount == 0 || (ep.FailureCount > ep.SuccessCount*2),
	}, nil
}

func (s *service) TriggerEvent(ctx context.Context, eventType string, payload interface{}) error {
	bytesPayload, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	tid := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tid != "" {
		tenantPtr = &tid
	}

	endpoints, err := s.repo.ListEndpoints(ctx, tenantPtr)
	if err != nil {
		return err
	}

	for _, ep := range endpoints {
		if !ep.Active {
			continue
		}
		matched := false
		for _, ev := range ep.Events {
			if ev == eventType || ev == "*" {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}

		deliv := &domain.WebhookDelivery{
			ID:           uuid.New().String(),
			EndpointID:   ep.ID,
			TenantID:     ep.TenantID,
			EventType:    eventType,
			Payload:      string(bytesPayload),
			Status:       "pending",
			AttemptCount: 0,
			MaxAttempts:  s.maxAttempts,
			CreatedAt:    time.Now().UTC(),
			UpdatedAt:    time.Now().UTC(),
		}

		if err := s.repo.CreateDelivery(ctx, deliv); err != nil {
			log.Error().Err(err).Str("endpoint_id", ep.ID).Msg("failed to create delivery record")
			continue
		}

		_, _ = s.queueClient.EnqueueWebhookDeliver(ctx, deliv.ID, asynq.ProcessIn(0))
	}
	return nil
}

func (s *service) checkRateLimit(ctx context.Context, url string) (bool, error) {
	if s.rdb == nil {
		return true, nil
	}
	windowKey := fmt.Sprintf("webhook:ratelimit:%s:%d", url, time.Now().Unix()/60)
	pipe := s.rdb.TxPipeline()
	incr := pipe.Incr(ctx, windowKey)
	pipe.Expire(ctx, windowKey, 70*time.Second)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return false, err
	}
	count := incr.Val()
	return count <= int64(s.maxPerMinute), nil
}

func (s *service) Deliver(ctx context.Context, deliveryID string) error {
	deliv, err := s.repo.GetDelivery(ctx, deliveryID)
	if err != nil {
		return err
	}

	ep, err := s.repo.GetEndpoint(ctx, deliv.EndpointID)
	if err != nil {
		return err
	}

	allowed, err := s.checkRateLimit(ctx, ep.URL)
	if err != nil {
		log.Error().Err(err).Msg("failed to check rate limit in redis, proceeding")
	} else if !allowed {
		// Rate limited: re-queue with backoff
		deliv.AttemptCount++
		deliv.UpdatedAt = time.Now().UTC()
		nextDelay := 1 * time.Minute
		if deliv.AttemptCount <= len(DefaultBackoffSchedule) {
			nextDelay = DefaultBackoffSchedule[deliv.AttemptCount-1]
		}
		nextAttempt := time.Now().UTC().Add(nextDelay)
		deliv.NextAttemptAt = &nextAttempt
		deliv.Status = "pending"
		_ = s.repo.UpdateDelivery(ctx, deliv)
		_, _ = s.queueClient.EnqueueWebhookDeliver(ctx, deliv.ID, asynq.ProcessIn(nextDelay))
		return nil
	}

	deliv.AttemptCount++
	now := time.Now().UTC()
	deliv.LastAttempt = &now

	hash := hmac.New(sha256.New, []byte(ep.Secret))
	hash.Write([]byte(deliv.Payload))
	sig := hex.EncodeToString(hash.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.URL, bytes.NewBufferString(deliv.Payload))
	if err != nil {
		return s.handleDeliveryFailure(ctx, deliv, ep, err.Error(), nil, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fluxa-Signature", "sha256="+sig)
	req.Header.Set("X-Fluxa-Timestamp", fmt.Sprintf("%d", now.Unix()))

	resp, err := s.client.Do(req)
	if err != nil {
		return s.handleDeliveryFailure(ctx, deliv, ep, err.Error(), nil, nil)
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	deliv.ResponseCode = &code

	if code >= 200 && code < 300 {
		deliv.Status = "success"
		deliv.UpdatedAt = time.Now().UTC()
		_ = s.repo.UpdateDelivery(ctx, deliv)

		ep.SuccessCount++
		ep.LastDeliveredAt = &now
		if ep.SuccessCount > 0 {
			ep.NotifiedFailing = false // reset on recovery
		}
		ep.UpdatedAt = time.Now().UTC()
		_ = s.repo.UpdateEndpoint(ctx, ep)
		return nil
	}

	return s.handleDeliveryFailure(ctx, deliv, ep, fmt.Sprintf("status code %d", code), &code, nil)
}

func (s *service) handleDeliveryFailure(ctx context.Context, deliv *domain.WebhookDelivery, ep *domain.WebhookEndpoint, errMsg string, code *int, body *string) error {
	deliv.Status = "failed"
	clientErr := errMsg
	deliv.ErrorMessage = &clientErr
	if code != nil {
		deliv.ResponseCode = code
	}
	if body != nil {
		deliv.ResponseBody = body
	}
	deliv.UpdatedAt = time.Now().UTC()

	ep.FailureCount++
	ep.UpdatedAt = time.Now().UTC()

	// Check notification trigger
	if !ep.NotifiedFailing && ep.FailureCount >= 3 {
		ep.NotifiedFailing = true
		log.Warn().Str(
			"endpoint_id", ep.ID,
		).Str(
			"url", ep.URL,
		).Msg("TENANT NOTIFICATION: Deliveries to webhook endpoint are failing consistently.")
	}

	_ = s.repo.UpdateEndpoint(ctx, ep)

	if deliv.AttemptCount >= s.maxAttempts {
		deliv.Status = "dead_lettered"
		_ = s.repo.UpdateDelivery(ctx, deliv)

		dl := &domain.WebhookDeadLetter{
			ID:           uuid.New().String(),
			EndpointID:   ep.ID,
			TenantID:     ep.TenantID,
			DeliveryID:   deliv.ID,
			Payload:      deliv.Payload,
			ErrorMessage: errMsg,
			AttemptCount: deliv.AttemptCount,
			CreatedAt:    time.Now().UTC(),
		}
		_ = s.repo.CreateDeadLetter(ctx, dl)
		return fmt.Errorf("webhook delivery reached max attempts (%d) and was sent to dead letter queue: %s", s.maxAttempts, errMsg)
	}

	nextDelay := 1 * time.Minute
	if deliv.AttemptCount <= len(DefaultBackoffSchedule) {
		nextDelay = DefaultBackoffSchedule[deliv.AttemptCount-1]
	}
	nextAttempt := time.Now().UTC().Add(nextDelay)
	deliv.NextAttemptAt = &nextAttempt
	_ = s.repo.UpdateDelivery(ctx, deliv)

	if s.queueClient != nil {
		_, _ = s.queueClient.EnqueueWebhookDeliver(ctx, deliv.ID, asynq.ProcessIn(nextDelay))
	}

	return fmt.Errorf("webhook delivery failed (attempt %d/%d): %s", deliv.AttemptCount, s.maxAttempts, errMsg)
}
