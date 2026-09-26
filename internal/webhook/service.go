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
	"github.com/fluxa/fluxa/internal/tracing"
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

// ConfigRepository stores the single tenant-scoped webhook configuration and
// the delivery history produced against it. Every method is keyed by tenant ID
// so one tenant can never read or mutate another's configuration.
type ConfigRepository interface {
	GetConfig(ctx context.Context, tenantID string) (*domain.TenantWebhookConfig, error)
	UpsertConfig(ctx context.Context, config *domain.TenantWebhookConfig) error
	ListEnabledConfigs(ctx context.Context) ([]*domain.TenantWebhookConfig, error)
	CreateConfigDelivery(ctx context.Context, delivery *domain.TenantWebhookDelivery) error
	UpdateConfigDelivery(ctx context.Context, delivery *domain.TenantWebhookDelivery) error
	GetConfigDelivery(ctx context.Context, id, tenantID string) (*domain.TenantWebhookDelivery, error)
	ListConfigDeliveries(ctx context.Context, tenantID string, limit, offset int) ([]*domain.TenantWebhookDelivery, error)
	UpdateConfigLastDelivered(ctx context.Context, tenantID string, deliveredAt time.Time) error
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

// ConfigService is the tenant-scoped webhook configuration surface. It is a
// separate interface from Service so that deployments without tenant webhook
// storage (and existing test doubles) can keep satisfying Service alone; the
// HTTP handler and worker feature-detect it with a type assertion.
type ConfigService interface {
	// GetConfig returns the tenant's configuration. The stored secret is
	// never included in the returned value — use UpdateConfig's result to
	// learn a secret, and only at the moment it is created or rotated.
	GetConfig(ctx context.Context) (*domain.TenantWebhookConfig, error)
	// UpdateConfig applies a partial update and returns the resulting config.
	// Result.Secret is populated only when a secret was just generated, either
	// because this was the first save or because RotateSecret was requested.
	UpdateConfig(ctx context.Context, update domain.WebhookConfigUpdate) (*domain.WebhookConfigResult, error)
	// ListConfigDeliveries returns the tenant's recent delivery attempts.
	ListConfigDeliveries(ctx context.Context, limit, offset int) ([]*domain.TenantWebhookDelivery, error)
	// TestDelivery sends a synthetic event to the tenant's endpoint
	// immediately, bypassing the queue, and records the attempt.
	TestDelivery(ctx context.Context) (*domain.TenantWebhookDelivery, error)
	// DeliverConfig performs the queued delivery for a tenant config delivery.
	DeliverConfig(ctx context.Context, deliveryID, tenantID string) error
	// DispatchToTenants fans an event out to every enabled, unpaused tenant
	// configuration subscribed to it.
	DispatchToTenants(ctx context.Context, eventType domain.EventType, payload interface{}) error
}

type TenantGetter interface {
	GetByID(ctx context.Context, id string) (*domain.Tenant, error)
}

type service struct {
	repo       Repository
	configRepo ConfigRepository
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

// NewConfigService builds a webhook service that also serves the tenant-scoped
// webhook configuration. It is a constructor rather than a variadic on
// NewService so that the config repository is an explicit, required argument
// for callers that intend to expose the config endpoints.
func NewConfigService(repo Repository, configRepo ConfigRepository, q *queue.Client, tenantRepo ...TenantGetter) Service {
	s := &service{
		repo:       repo,
		configRepo: configRepo,
		queue:      q,
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
// then enqueues async delivery tasks. Tenant-wide configs are fanned out in the
// same pass, so a business event reaches both endpoint-scoped subscribers and
// the single per-tenant config without callers having to know which mechanism
// a given tenant uses.
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

	// Tenant config fan-out must not mask a successful endpoint dispatch, and a
	// deployment without a config repository (e.g. an older schema) is fine.
	if s.configRepo != nil {
		if err := s.DispatchToTenants(ctx, eventType, payload); err != nil {
			return fmt.Errorf("dispatch to tenant webhook configs: %w", err)
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

	now := time.Now().UTC()
	delivery.AttemptCount++
	delivery.LastAttempt = &now

	if err := s.validateWebhookURL(ctx, ep.URL); err != nil {
		delivery.Status = domain.DeliveryFailed
		_ = s.repo.UpdateDelivery(ctx, delivery)
		return fmt.Errorf("validate webhook destination: %w", err)
	}

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
