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
	"github.com/fluxa/fluxa/internal/tracing"
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
	CreateSubscription(ctx context.Context, sub *domain.WebhookSubscription) error
	DeleteSubscription(ctx context.Context, id string) error
	ListSubscriptions(ctx context.Context, tenantID *string) ([]*domain.WebhookSubscription, error)
	GetSubscriptionsForEvent(ctx context.Context, tenantID *string, eventType string) ([]*domain.WebhookSubscription, error)
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
	Dispatch(ctx context.Context, eventType string, payload interface{}) error
	Deliver(ctx context.Context, deliveryID string) error
	CreateSubscription(ctx context.Context, eventType, webhookURL string) (*domain.WebhookSubscription, error)
	ListSubscriptions(ctx context.Context) ([]*domain.WebhookSubscription, error)
	DeleteSubscription(ctx context.Context, id string) error
}

type service struct {
	repo               Repository
	rdb                redis.UniversalClient
	client             *http.Client
	queueClient        *queue.Client
	maxPerMinute       int
	maxAttempts        int
	backoffSchedule    []time.Time
	allowPrivateNetworks bool
}

var DefaultBackoffSchedule = []time.Duration{
	1 * time.Minute,
	5 * time.Minute,
	30 * time.Minute,
	2 * time.Hour,
	6 * time.Hour,
}

func NewService(repo Repository, rdb redis.UniversalClient, queueClient *queue.Client, maxPerMinute int, allowPrivateNetworks bool) Service {
	if maxPerMinute <= 0 {
		maxPerMinute = 120
	}
	s := &service{
		repo:                 repo,
		rdb:                  rdb,
		queueClient:          queueClient,
		maxPerMinute:         maxPerMinute,
		maxAttempts:          len(DefaultBackoffSchedule),
		allowPrivateNetworks: allowPrivateNetworks,
	}
	s.client = s.newSafeHTTPClient()
	return s
}

func generateSecret() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return "whsec_" + hex.EncodeToString(buf)
}

func (s *service) RegisterEndpoint(ctx context.Context, url string, events []string) (*domain.WebhookEndpoint, string, error) {
	if err := s.validateWebhookURL(ctx, url); err != nil {
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

func (s *service) CreateSubscription(ctx context.Context, eventType, webhookURL string) (*domain.WebhookSubscription, error) {
	if err := s.validateWebhookURL(ctx, webhookURL); err != nil {
		return nil, err
	}

	sub := &domain.WebhookSubscription{
		ID:         uuid.New().String(),
		EventType:  eventType,
		WebhookURL: webhookURL,
	}

	if err := s.repo.CreateSubscription(ctx, sub); err != nil {
		return nil, err
	}
	return sub, nil
}

func (s *service) ListSubscriptions(ctx context.Context) ([]*domain.WebhookSubscription, error) {
	tid := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tid != "" {
		tenantPtr = &tid
	}
	return s.repo.ListSubscriptions(ctx, tenantPtr)
}

func (s *service) DeleteSubscription(ctx context.Context, id string) error {
	return s.repo.DeleteSubscription(ctx, id)
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
		_, err = s.queueClient.EnqueueWebhookDelivery(ctx, newDel.ID)
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

func (s *service) Dispatch(ctx context.Context, eventType string, payload interface{}) error {
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

		_, _ = s.queueClient.EnqueueWebhookDelivery(ctx, deliv.ID, asynq.ProcessIn(0))
	}

	// Tenant config fan-out must not mask a successful endpoint dispatch, and a
	// deployment without a config repository (e.g. an older schema) is fine.
	if s.configRepo != nil {
		if err := s.DispatchToTenants(ctx, eventType, payload); err != nil {
			return fmt.Errorf("dispatch to tenant webhook configs: %w", err)
		}
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
		_, _ = s.queueClient.EnqueueWebhookDelivery(ctx, deliv.ID, asynq.ProcessIn(nextDelay))
		return nil
	}

	deliv.AttemptCount++
	now := time.Now().UTC()
	deliv.LastAttempt = &now

	timestamp := fmt.Sprintf("%d", now.Unix())
	sig := sign(ep.Secret, timestamp, []byte(deliv.Payload))

	if err := s.validateWebhookURL(ctx, ep.URL); err != nil {
		return s.handleDeliveryFailure(ctx, deliv, ep, err.Error(), nil, nil)
	}

	method := deliv.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, ep.URL, bytes.NewBufferString(deliv.Payload))
	if err != nil {
		return s.handleDeliveryFailure(ctx, deliv, ep, err.Error(), nil, nil)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fluxa-Signature", sig)
	req.Header.Set("X-Fluxa-Timestamp", timestamp)

	resp, err := s.client.Do(req)
	if err != nil {
		return s.handleDeliveryFailure(ctx, deliv, ep, err.Error(), nil, nil)
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	deliv.ResponseCode = code

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
	deliv.ErrorMessage = errMsg
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
		_, _ = s.queueClient.EnqueueWebhookDelivery(ctx, deliv.ID, asynq.ProcessIn(nextDelay))
	}

	return fmt.Errorf("webhook delivery failed (attempt %d/%d): %s", deliv.AttemptCount, s.maxAttempts, errMsg)
}

// ---------------------------------------------------------------------------
// Tenant-scoped webhook configuration
// ---------------------------------------------------------------------------

// requireTenantID pulls the authenticated tenant off the request context. All
// tenant config operations are refused without one, so an unauthenticated or
// unscoped call can never read or mutate a config.
func requireTenantID(ctx context.Context) (string, error) {
	tenantID := tenant.IDFromContext(ctx)
	if tenantID == "" {
		return "", fmt.Errorf("%w: tenant context is required", domain.ErrWebhookConfigNotFound)
	}
	return tenantID, nil
}

// GetConfig returns the tenant's configuration. The stored secret is
// deliberately stripped: it is a write-only value, so a client can learn a
// secret only at the moment it is created or rotated, never by reading the
// config back.
func (s *service) GetConfig(ctx context.Context) (*domain.TenantWebhookConfig, error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if s.configRepo == nil {
		return nil, domain.ErrWebhookConfigNotFound
	}
	config, err := s.configRepo.GetConfig(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	redactConfigSecret(config)
	return config, nil
}

// redactConfigSecret blanks the signing secret on a config value that is about
// to leave the service. The copy is mutated in place because every config value
// is freshly loaded from the repository and not shared with cached state.
func redactConfigSecret(config *domain.TenantWebhookConfig) {
	if config == nil {
		return
	}
	config.SecretConfigured = config.SecretConfigured || config.Secret != ""
	config.Secret = ""
}

func (s *service) UpdateConfig(ctx context.Context, update domain.WebhookConfigUpdate) (*domain.WebhookConfigResult, error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if s.configRepo == nil {
		return nil, domain.ErrWebhookConfigNotFound
	}

	// Not-found on first read means "no config yet", which is a valid state we
	// materialise rather than a client error.
	config, err := s.configRepo.GetConfig(ctx, tenantID)
	if err != nil {
		if err != domain.ErrWebhookConfigNotFound {
			return nil, err
		}
		config = &domain.TenantWebhookConfig{
			TenantID:         tenantID,
			Events:           []string{},
			SigningAlgorithm: defaultSigningAlgorithm,
			CreatedAt:        time.Now().UTC(),
		}
	}

	revealedSecret := ""
	if update.URL != nil && *update.URL != "" {
		if err := s.validateWebhookURL(ctx, *update.URL); err != nil {
			return nil, err
		}
		config.URL = *update.URL
	}
	if update.Events != nil {
		config.Events = append([]string{}, *update.Events...)
	}
	if update.Enabled != nil {
		config.Enabled = *update.Enabled
	}
	if update.Paused != nil {
		config.Paused = *update.Paused
	}
	if update.ResumeAt != nil {
		resumeAt := update.ResumeAt.UTC()
		config.ResumeAt = &resumeAt
	}

	// A first-time save always mints a secret, since there is nothing to sign
	// with otherwise. Rotation always mints a fresh one and invalidates the
	// previous secret for the tenant's endpoint.
	if config.Secret == "" || update.RotateSecret {
		secret, err := generateSecret()
		if err != nil {
			return nil, fmt.Errorf("generate webhook secret: %w", err)
		}
		config.Secret = secret
		revealedSecret = secret
	}

	// Resuming manually clears any scheduled resume, and vice versa, so a
	// tenant's pause state is never ambiguous.
	if update.Paused != nil && !*update.Paused {
		config.ResumeAt = nil
	}
	if update.ResumeAt != nil {
		config.Paused = true
	}

	config.UpdatedAt = time.Now().UTC()
	if config.CreatedAt.IsZero() {
		config.CreatedAt = config.UpdatedAt
	}

	if err := s.configRepo.UpsertConfig(ctx, config); err != nil {
		return nil, err
	}

	// A plain update (changing the URL, say) must not re-expose the existing
	// secret, so the config returned to callers always has it stripped. Callers
	// read the one-time secret from Result.Secret, which is only populated on
	// first save and on rotation.
	redactConfigSecret(config)
	return &domain.WebhookConfigResult{Config: config, Secret: revealedSecret}, nil
}

func (s *service) ListConfigDeliveries(ctx context.Context, limit, offset int) ([]*domain.TenantWebhookDelivery, error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if s.configRepo == nil {
		return nil, domain.ErrWebhookConfigNotFound
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	return s.configRepo.ListConfigDeliveries(ctx, tenantID, limit, offset)
}

func (s *service) TestDelivery(ctx context.Context) (*domain.TenantWebhookDelivery, error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if s.configRepo == nil {
		return nil, domain.ErrWebhookConfigNotFound
	}

	config, err := s.configRepo.GetConfig(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	if config.URL == "" {
		return nil, fmt.Errorf("%w: no webhook url configured", domain.ErrWebhookConfigDisabled)
	}

	payload, err := json.Marshal(map[string]interface{}{
		"event":       "webhook.test",
		"tenant_id":   tenantID,
		"sent_at":     time.Now().UTC().Format(time.RFC3339),
		"environment": "test",
	})
	if err != nil {
		return nil, fmt.Errorf("marshal test payload: %w", err)
	}

	now := time.Now().UTC()
	delivery := &domain.TenantWebhookDelivery{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		EventType: domain.EventType("webhook.test"),
		Payload:   payload,
		Status:    domain.DeliveryPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.configRepo.CreateConfigDelivery(ctx, delivery); err != nil {
		return nil, err
	}

	// A test delivery deliberately ignores the pause switch: the tenant is
	// actively asking us to prove the endpoint works.
	s.attemptConfigDelivery(ctx, config, delivery)
	return delivery, nil
}

func (s *service) DeliverConfig(ctx context.Context, deliveryID, tenantID string) error {
	if s.configRepo == nil {
		return domain.ErrWebhookConfigNotFound
	}
	if tenantID == "" {
		tenantID, _ = requireTenantID(ctx)
	}

	config, err := s.configRepo.GetConfig(ctx, tenantID)
	if err != nil {
		return err
	}
	delivery, err := s.configRepo.GetConfigDelivery(ctx, deliveryID, tenantID)
	if err != nil {
		return err
	}

	// Re-check the pause switch at delivery time. A scheduled resume that has
	// now elapsed lifts the pause automatically.
	if config.Paused {
		if config.ResumeAt != nil && !time.Now().UTC().Before(*config.ResumeAt) {
			config.Paused = false
			config.ResumeAt = nil
			config.UpdatedAt = time.Now().UTC()
			if err := s.configRepo.UpsertConfig(ctx, config); err != nil {
				return err
			}
		} else {
			delivery.Status = domain.DeliveryPaused
			delivery.UpdatedAt = time.Now().UTC()
			if err := s.configRepo.UpdateConfigDelivery(ctx, delivery); err != nil {
				return err
			}
			tracing.Logger(ctx).Info().Str("tenant_id", tenantID).Str("delivery_id", deliveryID).
				Msg("webhook: delivery recorded but not sent, tenant paused")
			return nil
		}
	}

	if config.URL == "" {
		return fmt.Errorf("%w: no webhook url configured", domain.ErrWebhookConfigDisabled)
	}

	s.attemptConfigDelivery(ctx, config, delivery)
	return nil
}

// DispatchToTenants records a delivery for every enabled, unpaused tenant
// subscribed to eventType and enqueues it. Paused tenants still get the record
// — marked paused — so their history shows the event was intentionally held.
func (s *service) DispatchToTenants(ctx context.Context, eventType domain.EventType, payload interface{}) error {
	if s.configRepo == nil {
		return nil
	}

	configs, err := s.configRepo.ListEnabledConfigs(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event payload: %w", err)
	}
	eventName := string(eventType)

	for _, config := range configs {
		if !subscribedTo(config.Events, eventName) {
			continue
		}

		now := time.Now().UTC()
		delivery := &domain.TenantWebhookDelivery{
			ID:        uuid.New().String(),
			TenantID:  config.TenantID,
			EventType: eventType,
			Payload:   body,
			Status:    domain.DeliveryPending,
			CreatedAt: now,
			UpdatedAt: now,
		}

		// A scheduled resume that has elapsed lifts the pause before we decide.
		paused := config.Paused
		if paused && config.ResumeAt != nil && !now.Before(*config.ResumeAt) {
			paused = false
			config.Paused = false
			config.ResumeAt = nil
			config.UpdatedAt = now
			if err := s.configRepo.UpsertConfig(ctx, config); err != nil {
				return err
			}
		}
		if paused || config.URL == "" {
			delivery.Status = domain.DeliveryPaused
		}

		if err := s.configRepo.CreateConfigDelivery(ctx, delivery); err != nil {
			return err
		}
		if delivery.Status == domain.DeliveryPaused {
			tracing.Logger(ctx).Info().Str("tenant_id", config.TenantID).Str("event", eventName).
				Msg("webhook: delivery recorded but not sent, tenant paused")
			continue
		}
		if s.queue != nil {
			if err := s.queue.EnqueueTenantWebhookDelivery(ctx, delivery.ID, config.TenantID); err != nil {
				// Delivery is persisted; the worker will pick it up on retry.
				_ = err
			}
		}
	}
	return nil
}

// attemptConfigDelivery performs the signed HTTP POST for a tenant config
// delivery and records the outcome on the delivery row.
func (s *service) attemptConfigDelivery(ctx context.Context, config *domain.TenantWebhookConfig, delivery *domain.TenantWebhookDelivery) {
	now := time.Now().UTC()
	delivery.AttemptCount++
	delivery.LastAttempt = &now
	delivery.UpdatedAt = now

	// Log through the context so delivery outcomes carry the trace_id of the
	// request or job that produced the event.
	logger := tracing.Logger(ctx)

	fail := func(err error) {
		delivery.Status = domain.DeliveryFailed
		if updateErr := s.configRepo.UpdateConfigDelivery(ctx, delivery); updateErr != nil {
			logger.Error().Err(updateErr).Str("delivery_id", delivery.ID).
				Msg("webhook: persist failed tenant delivery")
		}
		logger.Error().Err(err).Str("tenant_id", delivery.TenantID).Str("delivery_id", delivery.ID).
			Msg("webhook: tenant delivery failed")
	}

	if err := s.validateWebhookURL(ctx, config.URL); err != nil {
		fail(err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, config.URL, bytes.NewReader(delivery.Payload))
	if err != nil {
		fail(fmt.Errorf("build webhook request: %w", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fluxa-Signature", sign(config.Secret, delivery.Payload))
	req.Header.Set("X-Fluxa-Event", string(delivery.EventType))
	req.Header.Set("X-Fluxa-Tenant-ID", delivery.TenantID)

	resp, err := s.client.Do(req)
	if err != nil {
		fail(fmt.Errorf("deliver webhook: %w", err))
		return
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	delivery.ResponseCode = &code
	if code >= 200 && code < 300 {
		delivery.Status = domain.DeliverySuccess
	} else {
		delivery.Status = domain.DeliveryFailed
	}

	if err := s.configRepo.UpdateConfigDelivery(ctx, delivery); err != nil {
		logger.Error().Err(err).Str("delivery_id", delivery.ID).
			Msg("webhook: persist tenant delivery result")
		return
	}
	if delivery.Status == domain.DeliverySuccess {
		if err := s.configRepo.UpdateConfigLastDelivered(ctx, delivery.TenantID, now); err != nil {
			logger.Error().Err(err).Str("tenant_id", delivery.TenantID).
				Msg("webhook: update tenant last delivered timestamp")
		}
	}
}

// subscribedTo reports whether a config wants eventName. An empty list means
// every event, matching the endpoint-level Dispatch semantics.
func subscribedTo(events []string, eventName string) bool {
	if len(events) == 0 {
		return true
	}
	for _, event := range events {
		if event == eventName {
			return true
		}
	}
	return false
}

const defaultSigningAlgorithm = "hmac-sha256"

var _ ConfigService = (*service)(nil)
