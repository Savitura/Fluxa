package webhook

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	fluxacrypto "github.com/fluxa/fluxa/internal/crypto"
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
	GetDeadLetterForTenant(ctx context.Context, id, tenantID string) (*domain.WebhookDeadLetter, error)
	ListDeadLetters(ctx context.Context, filter domain.DeadLetterFilter, limit, offset int) ([]*domain.WebhookDeadLetter, error)
	// ClaimReplay atomically marks a dead letter replayed and inserts the
	// delivery the replay produced, under a row lock. It returns created=false
	// when another request already replayed the dead letter; the returned dead
	// letter then carries the winning replay_delivery_id so the caller can
	// return the same result without sending a second delivery.
	ClaimReplay(ctx context.Context, deadLetterID, tenantID string, replay *domain.WebhookDelivery, now time.Time) (*domain.WebhookDeadLetter, bool, error)
	RecordDeliveryAttempt(ctx context.Context, attempt *domain.WebhookDeliveryAttempt) error
	ListDeliveryAttempts(ctx context.Context, deliveryID string) ([]*domain.WebhookDeliveryAttempt, error)
	PruneDeadLetters(ctx context.Context, before time.Time) (int64, error)
}

type Service interface {
	RegisterEndpoint(ctx context.Context, url string, events []string) (*domain.WebhookEndpoint, string, error)
	ListEndpoints(ctx context.Context) ([]*domain.WebhookEndpoint, error)
	DeleteEndpoint(ctx context.Context, id string) error
	ListDeliveries(ctx context.Context, endpointID string, limit int) ([]*domain.WebhookDelivery, error)
	ListDeadLetters(ctx context.Context, filter domain.DeadLetterFilter, limit, offset int) ([]*domain.WebhookDeadLetter, error)
	GetDeadLetter(ctx context.Context, id string) (*domain.WebhookDeadLetter, error)
	ReplayDeadLetter(ctx context.Context, deadLetterID string) (*domain.WebhookDeadLetter, error)
	PruneDeadLetters(ctx context.Context, retention time.Duration) (int64, error)
	GetEndpointHealth(ctx context.Context, endpointID string) (*domain.WebhookHealth, error)
	Dispatch(ctx context.Context, eventType domain.EventType, payload interface{}) error
	Deliver(ctx context.Context, deliveryID string) error
	CreateSubscription(ctx context.Context, eventType, webhookURL string) (*domain.WebhookSubscription, error)
	ListSubscriptions(ctx context.Context) ([]*domain.WebhookSubscription, error)
	DeleteSubscription(ctx context.Context, id string) error
}

// ConfigRepository stores the single tenant-scoped webhook configuration and
// the delivery history produced against it. Every method is keyed by tenant ID
// so one tenant can never read or mutate another tenant's configuration.
type ConfigRepository interface {
	GetConfig(ctx context.Context, tenantID string) (*domain.TenantWebhookConfig, error)
	UpsertConfig(ctx context.Context, config *domain.TenantWebhookConfig) error
	ListConfigs(ctx context.Context) ([]*domain.TenantWebhookConfig, error)
	ListEnabledConfigs(ctx context.Context) ([]*domain.TenantWebhookConfig, error)
	ListSigningSecrets(ctx context.Context, tenantID string) ([]*domain.WebhookSigningSecret, error)
	ImportLegacySigningSecret(ctx context.Context, tenantID, keyID, encryptedSecret string, now time.Time) (*domain.WebhookSigningSecret, error)
	RotateSigningSecret(ctx context.Context, tenantID, keyID, encryptedSecret, legacyKeyID, legacyEncryptedSecret string, overlap time.Duration, now time.Time) (*domain.WebhookSigningSecret, error)
	GetSigningSecret(ctx context.Context, tenantID, keyID string) (string, error)
	CreateConfigDelivery(ctx context.Context, delivery *domain.TenantWebhookDelivery) error
	UpdateConfigDelivery(ctx context.Context, delivery *domain.TenantWebhookDelivery) error
	GetConfigDelivery(ctx context.Context, id, tenantID string) (*domain.TenantWebhookDelivery, error)
	ListConfigDeliveries(ctx context.Context, tenantID string, limit, offset int) ([]*domain.TenantWebhookDelivery, error)
	UpdateConfigLastDelivered(ctx context.Context, tenantID string, deliveredAt time.Time) error
}

// ConfigService is the tenant-scoped webhook configuration surface. It is a
// separate interface from Service so that deployments without tenant webhook
// storage (and existing test doubles) can keep satisfying Service alone; the
// HTTP handler and worker feature-detect it with a type assertion.
type ConfigService interface {
	GetConfig(ctx context.Context) (*domain.TenantWebhookConfig, error)
	UpdateConfig(ctx context.Context, update domain.WebhookConfigUpdate) (*domain.WebhookConfigResult, error)
	MigrateLegacySigningSecrets(ctx context.Context) error
	ListSigningSecrets(ctx context.Context) ([]*domain.WebhookSigningSecret, error)
	RotateSigningSecret(ctx context.Context, overlap time.Duration) (*domain.WebhookSigningSecret, string, error)
	ListConfigDeliveries(ctx context.Context, limit, offset int) ([]*domain.TenantWebhookDelivery, error)
	TestDelivery(ctx context.Context) (*domain.TenantWebhookDelivery, error)
	DeliverConfig(ctx context.Context, deliveryID, tenantID string) error
	DispatchToTenants(ctx context.Context, eventType domain.EventType, payload interface{}) error
}

type service struct {
	repo                 Repository
	configRepo           ConfigRepository
	rdb                  redis.UniversalClient
	client               *http.Client
	queueClient          *queue.Client
	maxPerMinute         int
	maxAttempts          int
	allowPrivateNetworks bool
	encryptionKey        []byte
}

func defaultBackoffSchedule() []time.Duration {
	return []time.Duration{
		1 * time.Minute,
		5 * time.Minute,
		30 * time.Minute,
		2 * time.Hour,
		6 * time.Hour,
	}
}

func backoffFor(attempt int) time.Duration {
	schedule := defaultBackoffSchedule()
	if attempt <= 0 || attempt > len(schedule) {
		return 1 * time.Minute
	}
	return schedule[attempt-1]
}

func NewService(repo Repository, rdb redis.UniversalClient, queueClient *queue.Client, maxPerMinute int, allowPrivateNetworks bool, encryptionKey ...[]byte) Service {
	if maxPerMinute <= 0 {
		maxPerMinute = 120
	}
	s := &service{
		repo:                 repo,
		rdb:                  rdb,
		queueClient:          queueClient,
		maxPerMinute:         maxPerMinute,
		maxAttempts:          len(defaultBackoffSchedule()),
		allowPrivateNetworks: allowPrivateNetworks,
		configRepo:           configRepository(repo),
		encryptionKey:        serviceEncryptionKey(encryptionKey),
	}
	s.client = s.newSafeHTTPClient()
	return s
}

// NewConfigService builds a webhook service that also serves the tenant-scoped
// webhook configuration API. The config repository is an explicit, required
// argument so a caller exposing the config endpoints cannot forget to wire it;
// NewService remains available for deployments without tenant webhook storage.
func NewConfigService(repo Repository, configRepo ConfigRepository, q *queue.Client, encryptionKey ...[]byte) Service {
	s := &service{
		repo:          repo,
		configRepo:    configRepo,
		queueClient:   q,
		maxPerMinute:  120,
		maxAttempts:   len(defaultBackoffSchedule()),
		encryptionKey: serviceEncryptionKey(encryptionKey),
	}
	s.client = s.newSafeHTTPClient()
	return s
}

func configRepository(repo Repository) ConfigRepository {
	configRepo, _ := repo.(ConfigRepository)
	return configRepo
}

func serviceEncryptionKey(keys [][]byte) []byte {
	if len(keys) > 0 {
		if len(keys[0]) != 32 {
			panic("webhook encryption key must be 32 bytes")
		}
		return append([]byte(nil), keys[0]...)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("unable to initialize webhook encryption key")
	}
	return key
}

func generateSecret() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate webhook signing secret")
	}
	return "whsec_" + hex.EncodeToString(buf), nil
}

func (s *service) RegisterEndpoint(ctx context.Context, url string, events []string) (*domain.WebhookEndpoint, string, error) {
	for _, eventType := range events {
		if eventType != "*" && !domain.IsSupportedEventType(eventType) {
			return nil, "", fmt.Errorf("%w: %q", ErrUnsupportedEventType, eventType)
		}
	}
	if err := s.validateWebhookURL(ctx, url); err != nil {
		return nil, "", err
	}

	tid := tenant.IDFromContext(ctx)
	var tenantPtr *string
	if tid != "" {
		tenantPtr = &tid
	}

	if len(events) == 0 {
		events = append([]string(nil), domain.SupportedEventTypes...)
	}

	secret, err := generateSecret()
	if err != nil {
		return nil, "", err
	}
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

	stored := *ep
	stored.Secret, err = s.encryptSecret(secret)
	if err != nil {
		return nil, "", err
	}
	if err := s.repo.CreateEndpoint(ctx, &stored); err != nil {
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
	endpoints, err := s.repo.ListEndpoints(ctx, tenantPtr)
	if err != nil {
		return nil, err
	}
	for index, endpoint := range endpoints {
		copy := *endpoint
		if copy.Secret != "" && !strings.HasPrefix(copy.Secret, "v1::") {
			if err := s.persistEndpoint(ctx, &copy); err != nil {
				return nil, err
			}
		}
		copy.Secret = ""
		endpoints[index] = &copy
	}
	return endpoints, nil
}

func (s *service) DeleteEndpoint(ctx context.Context, id string) error {
	return s.repo.DeleteEndpoint(ctx, id)
}

func (s *service) CreateSubscription(ctx context.Context, eventType, webhookURL string) (*domain.WebhookSubscription, error) {
	if !domain.IsSupportedEventType(eventType) {
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedEventType, eventType)
	}
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

// defaultDeadLetterRetention bounds how long an exhausted delivery is kept
// before the retention sweep removes it. Thirty days mirrors the audit window
// the rest of the platform uses.
const defaultDeadLetterRetention = 30 * 24 * time.Hour

// maxDeadLetterPageSize caps a single listing so a tenant with a large backlog
// cannot force an unbounded response.
const maxDeadLetterPageSize = 200

func (s *service) ListDeadLetters(ctx context.Context, filter domain.DeadLetterFilter, limit, offset int) ([]*domain.WebhookDeadLetter, error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > maxDeadLetterPageSize {
		limit = maxDeadLetterPageSize
	}
	if offset < 0 {
		offset = 0
	}
	filter.TenantID = tenantID

	// Best-effort retention: the sweep keeps the table from growing without
	// bound without requiring a dedicated worker, and a failure here must not
	// hide the listing the operator asked for.
	if _, err := s.PruneDeadLetters(ctx, defaultDeadLetterRetention); err != nil {
		log.Error().Err(err).Msg("webhook: dead letter retention sweep failed")
	}

	deadLetters, err := s.repo.ListDeadLetters(ctx, filter, limit, offset)
	if err != nil {
		return nil, err
	}
	for _, dl := range deadLetters {
		redactDeadLetter(dl)
	}
	return deadLetters, nil
}

func (s *service) GetDeadLetter(ctx context.Context, id string) (*domain.WebhookDeadLetter, error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	dl, err := s.repo.GetDeadLetterForTenant(ctx, id, tenantID)
	if err != nil {
		return nil, err
	}
	attempts, err := s.repo.ListDeliveryAttempts(ctx, dl.DeliveryID)
	if err != nil {
		return nil, err
	}
	dl.Attempts = attempts
	redactDeadLetter(dl)
	return dl, nil
}

// redactDeadLetter strips sensitive payload fields from the value about to
// leave the service. The stored payload keeps the original body so a replay is
// faithful; only what an operator can read is redacted.
func redactDeadLetter(dl *domain.WebhookDeadLetter) {
	if dl == nil || dl.Payload == "" {
		return
	}
	payload, fields := domain.RedactWebhookPayload(dl.Payload)
	dl.Payload = payload
	if len(fields) > 0 {
		dl.RedactedFields = fields
	}
}

// ReplayDeadLetter re-sends an exhausted delivery to its original endpoint with
// the original event identity and a brand-new delivery identity. The delivery
// is queued through the normal delivery path, so it is signed with the current
// signing secret and the standard timestamped signature.
func (s *service) ReplayDeadLetter(ctx context.Context, deadLetterID string) (*domain.WebhookDeadLetter, error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	dl, err := s.repo.GetDeadLetterForTenant(ctx, deadLetterID, tenantID)
	if err != nil {
		return nil, err
	}
	if dl.Status == domain.DeadLetterDiscarded {
		return nil, fmt.Errorf("%w: dead letter was discarded", domain.ErrDeadLetterNotFound)
	}

	// The replay must target the original endpoint, and only while it is still
	// active. Replaying to a disabled endpoint would silently drop the event;
	// replaying to a different endpoint would change its destination.
	ep, err := s.repo.GetEndpoint(ctx, dl.EndpointID)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrDeadLetterEndpointUnusable, err)
	}
	if !ep.Active {
		return nil, fmt.Errorf("%w: endpoint is disabled", domain.ErrDeadLetterEndpointUnusable)
	}
	if ep.TenantID != nil && *ep.TenantID != "" && *ep.TenantID != tenantID {
		return nil, domain.ErrDeadLetterEndpointUnusable
	}

	now := time.Now().UTC()
	replay := &domain.WebhookDelivery{
		ID:           uuid.New().String(),
		EndpointID:   ep.ID,
		TenantID:     dl.TenantID,
		EventType:    dl.EventType,
		Payload:      dl.Payload,
		Status:       "pending",
		AttemptCount: 0,
		MaxAttempts:  s.maxAttempts,
		ReplayOf:     dl.ID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	claimed, created, err := s.repo.ClaimReplay(ctx, dl.ID, tenantID, replay, now)
	if err != nil {
		return nil, err
	}
	if !created {
		// Idempotent replay: an earlier (or concurrent) request already produced
		// the delivery, so return that result instead of sending a second one.
		redactDeadLetter(claimed)
		return claimed, nil
	}

	// Record the operator action in the attempt timeline before the queued
	// delivery's own attempts are appended, so the history reads
	// replay → delivery attempt 1 → …
	if err := s.repo.RecordDeliveryAttempt(ctx, &domain.WebhookDeliveryAttempt{
		ID:            uuid.New().String(),
		DeliveryID:    replay.ID,
		DeadLetterID:  dl.ID,
		TenantID:      dl.TenantID,
		Mode:          dl.Mode,
		Kind:          domain.AttemptKindReplay,
		AttemptNumber: 0,
		Status:        "queued",
		OccurredAt:    now,
	}); err != nil {
		log.Error().Err(err).Str("delivery_id", replay.ID).Msg("webhook: failed to record replay attempt")
	}

	if s.queueClient != nil {
		if _, err := s.queueClient.EnqueueWebhookDelivery(ctx, replay.ID); err != nil {
			return nil, err
		}
	}

	redactDeadLetter(claimed)
	return claimed, nil
}

// PruneDeadLetters drops dead letters older than retention. A non-positive
// retention falls back to the default so a misconfigured caller cannot wipe the
// whole queue.
func (s *service) PruneDeadLetters(ctx context.Context, retention time.Duration) (int64, error) {
	if retention <= 0 {
		retention = defaultDeadLetterRetention
	}
	return s.repo.PruneDeadLetters(ctx, time.Now().UTC().Add(-retention))
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

func (s *service) Dispatch(ctx context.Context, eventType domain.EventType, payload interface{}) error {
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
			if ev == string(eventType) || ev == "*" {
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
			EventType:    string(eventType),
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
	storedSecret := ep.Secret
	ep.Secret, err = s.decryptSecret(ep.Secret)
	if err != nil {
		return err
	}
	if ep.Secret != "" && !strings.HasPrefix(storedSecret, "v1::") {
		if err := s.persistEndpoint(ctx, ep); err != nil {
			return err
		}
	}

	allowed, err := s.checkRateLimit(ctx, ep.URL)
	if err != nil {
		log.Error().Err(err).Msg("failed to check rate limit in redis, proceeding")
	} else if !allowed {
		// Rate limited: re-queue with backoff
		deliv.AttemptCount++
		deliv.UpdatedAt = time.Now().UTC()
		nextDelay := backoffFor(deliv.AttemptCount)
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
		return s.handleDeliveryFailure(ctx, deliv, ep, err.Error(), nil, nil, err)
	}

	method := deliv.Method
	if method == "" {
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, ep.URL, bytes.NewBufferString(deliv.Payload))
	if err != nil {
		return s.handleDeliveryFailure(ctx, deliv, ep, err.Error(), nil, nil, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Fluxa-Signature", sig)
	req.Header.Set("X-Fluxa-Timestamp", timestamp)

	resp, err := s.client.Do(req)
	if err != nil {
		return s.handleDeliveryFailure(ctx, deliv, ep, err.Error(), nil, nil, err)
	}
	defer resp.Body.Close()

	code := resp.StatusCode
	deliv.ResponseCode = code

	if code >= 200 && code < 300 {
		deliv.Status = "success"
		deliv.UpdatedAt = time.Now().UTC()
		s.recordAttempt(ctx, deliv, statusSuccess, code, "")
		_ = s.repo.UpdateDelivery(ctx, deliv)

		ep.SuccessCount++
		ep.LastDeliveredAt = &now
		if ep.SuccessCount > 0 {
			ep.NotifiedFailing = false // reset on recovery
		}
		ep.UpdatedAt = time.Now().UTC()
		_ = s.persistEndpoint(ctx, ep)
		return nil
	}

	return s.handleDeliveryFailure(ctx, deliv, ep, fmt.Sprintf("status code %d", code), &code, nil, nil)
}

// handleDeliveryFailure records a failed attempt and returns an error that
// still wraps cause, so callers can match sentinels such as
// ErrUnsafeWebhookURL instead of only seeing a formatted message.
func (s *service) handleDeliveryFailure(ctx context.Context, deliv *domain.WebhookDelivery, ep *domain.WebhookEndpoint, errMsg string, code *int, body *string, cause error) error {
	deliv.Status = "failed"
	deliv.ErrorMessage = errMsg
	if code != nil {
		deliv.ResponseCode = *code
	}
	if body != nil {
		deliv.ResponseBody = *body
	}
	deliv.UpdatedAt = time.Now().UTC()

	attemptCode := 0
	if code != nil {
		attemptCode = *code
	}
	s.recordAttempt(ctx, deliv, statusFailed, attemptCode, errMsg)

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

	_ = s.persistEndpoint(ctx, ep)

	if deliv.AttemptCount >= s.maxAttempts {
		deliv.Status = "dead_lettered"
		_ = s.repo.UpdateDelivery(ctx, deliv)

		retainUntil := time.Now().UTC().Add(defaultDeadLetterRetention)
		dl := &domain.WebhookDeadLetter{
			ID:           uuid.New().String(),
			EndpointID:   ep.ID,
			TenantID:     ep.TenantID,
			DeliveryID:   deliv.ID,
			EventType:    deliv.EventType,
			Payload:      deliv.Payload,
			ErrorMessage: errMsg,
			AttemptCount: deliv.AttemptCount,
			Status:       domain.DeadLetterPending,
			RetainUntil:  &retainUntil,
			CreatedAt:    time.Now().UTC(),
		}
		_ = s.repo.CreateDeadLetter(ctx, dl)
		if cause != nil {
			return fmt.Errorf("webhook delivery reached max attempts (%d) and was sent to dead letter queue: %w", s.maxAttempts, cause)
		}
		return fmt.Errorf("webhook delivery reached max attempts (%d) and was sent to dead letter queue: %s", s.maxAttempts, errMsg)
	}

	nextDelay := backoffFor(deliv.AttemptCount)
	nextAttempt := time.Now().UTC().Add(nextDelay)
	deliv.NextAttemptAt = &nextAttempt
	_ = s.repo.UpdateDelivery(ctx, deliv)

	if s.queueClient != nil {
		_, _ = s.queueClient.EnqueueWebhookDelivery(ctx, deliv.ID, asynq.ProcessIn(nextDelay))
	}

	if cause != nil {
		return fmt.Errorf("webhook delivery failed (attempt %d/%d): %w", deliv.AttemptCount, s.maxAttempts, cause)
	}
	return fmt.Errorf("webhook delivery failed (attempt %d/%d): %s", deliv.AttemptCount, s.maxAttempts, errMsg)
}

const (
	statusSuccess = "success"
	statusFailed  = "failed"
)

// attemptKindFor reports how a delivery came to be: an operator replay or the
// worker's own retries. The delivery, not the individual attempt, carries the
// distinction because every attempt of a replay is operator-initiated.
func attemptKindFor(deliv *domain.WebhookDelivery) domain.AttemptKind {
	if deliv.ReplayOf != "" {
		return domain.AttemptKindReplay
	}
	return domain.AttemptKindAutomatic
}

// recordAttempt appends one delivery attempt to the durable history. A failure
// to record must not fail the delivery itself — the attempt history is
// observability, not the delivery's state machine.
func (s *service) recordAttempt(ctx context.Context, deliv *domain.WebhookDelivery, status string, code int, errMsg string) {
	attempt := &domain.WebhookDeliveryAttempt{
		ID:            uuid.New().String(),
		DeliveryID:    deliv.ID,
		TenantID:      deliv.TenantID,
		Mode:          deliv.Mode,
		Kind:          attemptKindFor(deliv),
		AttemptNumber: deliv.AttemptCount,
		Status:        status,
		ResponseCode:  code,
		ErrorMessage:  errMsg,
		OccurredAt:    time.Now().UTC(),
	}
	if deliv.ReplayOf != "" {
		attempt.DeadLetterID = deliv.ReplayOf
	}
	if err := s.repo.RecordDeliveryAttempt(ctx, attempt); err != nil {
		log.Error().Err(err).Str("delivery_id", deliv.ID).Msg("webhook: failed to record delivery attempt")
	}
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
	storedSecret := config.Secret
	config.Secret, err = s.decryptSecret(config.Secret)
	if err != nil {
		return nil, err
	}
	if config.Secret != "" && !strings.HasPrefix(storedSecret, "v1::") {
		if err := s.persistConfig(ctx, config); err != nil {
			return nil, err
		}
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

func (s *service) persistConfig(ctx context.Context, config *domain.TenantWebhookConfig) error {
	stored := *config
	encrypted, err := s.encryptSecret(config.Secret)
	if err != nil {
		return err
	}
	stored.Secret = encrypted
	return s.configRepo.UpsertConfig(ctx, &stored)
}

func (s *service) persistEndpoint(ctx context.Context, endpoint *domain.WebhookEndpoint) error {
	stored := *endpoint
	if stored.Secret != "" && !strings.HasPrefix(stored.Secret, "v1::") {
		encrypted, err := s.encryptSecret(stored.Secret)
		if err != nil {
			return err
		}
		stored.Secret = encrypted
	}
	return s.repo.UpdateEndpoint(ctx, &stored)
}

func (s *service) MigrateLegacySigningSecrets(ctx context.Context) error {
	if s.configRepo == nil {
		return domain.ErrWebhookConfigNotFound
	}
	configs, err := s.configRepo.ListConfigs(ctx)
	if err != nil {
		return err
	}
	for _, config := range configs {
		storedSecret := config.Secret
		config.Secret, err = s.decryptSecret(config.Secret)
		if err != nil {
			return err
		}
		if config.Secret != "" && !strings.HasPrefix(storedSecret, "v1::") {
			if err := s.persistConfig(ctx, config); err != nil {
				return err
			}
		}
		if err := s.ensureSigningSecretVersion(ctx, config); err != nil {
			return err
		}
	}
	if s.repo == nil {
		return nil
	}
	for _, mode := range []domain.Mode{domain.ModeLive, domain.ModeTest} {
		modeCtx := tenant.WithMode(ctx, mode)
		endpoints, err := s.repo.ListEndpoints(modeCtx, nil)
		if err != nil {
			return err
		}
		for _, endpoint := range endpoints {
			if endpoint.Secret == "" || strings.HasPrefix(endpoint.Secret, "v1::") {
				continue
			}
			if err := s.persistEndpoint(modeCtx, endpoint); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *service) encryptSecret(secret string) (string, error) {
	ciphertext, err := fluxacrypto.Encrypt([]byte(secret), s.encryptionKey)
	if err != nil {
		return "", fmt.Errorf("encrypt webhook signing secret")
	}
	return string(ciphertext), nil
}

func (s *service) decryptSecret(stored string) (string, error) {
	if !strings.HasPrefix(stored, "v1::") {
		return stored, nil
	}
	plaintext, err := fluxacrypto.Decrypt([]byte(stored), s.encryptionKey)
	if err != nil {
		return "", fmt.Errorf("decrypt webhook signing secret")
	}
	return string(plaintext), nil
}

func (s *service) ListSigningSecrets(ctx context.Context) ([]*domain.WebhookSigningSecret, error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, err
	}
	if s.configRepo == nil {
		return nil, domain.ErrWebhookConfigNotFound
	}
	config, err := s.configRepo.GetConfig(ctx, tenantID)
	if errors.Is(err, domain.ErrWebhookConfigNotFound) {
		return []*domain.WebhookSigningSecret{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.ensureSigningSecretVersion(ctx, config); err != nil {
		return nil, err
	}
	return s.configRepo.ListSigningSecrets(ctx, tenantID)
}

func (s *service) ensureSigningSecretVersion(ctx context.Context, config *domain.TenantWebhookConfig) error {
	if config.SigningKeyID != "" || config.Secret == "" {
		return nil
	}
	secret, err := s.decryptSecret(config.Secret)
	if err != nil {
		return err
	}
	encrypted, err := s.encryptSecret(secret)
	if err != nil {
		return err
	}
	metadata, err := s.configRepo.ImportLegacySigningSecret(ctx, config.TenantID, uuid.NewString(), encrypted, time.Now().UTC())
	if err != nil {
		return err
	}
	if metadata == nil {
		return nil
	}
	config.Secret = secret
	config.SigningKeyID = metadata.KeyID
	return nil
}

func (s *service) RotateSigningSecret(ctx context.Context, overlap time.Duration) (*domain.WebhookSigningSecret, string, error) {
	tenantID, err := requireTenantID(ctx)
	if err != nil {
		return nil, "", err
	}
	if s.configRepo == nil {
		return nil, "", domain.ErrWebhookConfigNotFound
	}
	if overlap < 0 || overlap > 24*time.Hour {
		return nil, "", fmt.Errorf("overlap window must be between 0 and 24 hours")
	}
	secret, err := generateSecret()
	if err != nil {
		return nil, "", err
	}
	encrypted, err := s.encryptSecret(secret)
	if err != nil {
		return nil, "", err
	}
	legacyKeyID := ""
	legacySecret := ""
	current, currentErr := s.configRepo.GetConfig(ctx, tenantID)
	if currentErr == nil && current.SigningKeyID == "" && current.Secret != "" {
		plaintext, decryptErr := s.decryptSecret(current.Secret)
		if decryptErr != nil {
			return nil, "", decryptErr
		}
		legacySecret, err = s.encryptSecret(plaintext)
		if err != nil {
			return nil, "", err
		}
		legacyKeyID = uuid.NewString()
	} else if currentErr != nil && !errors.Is(currentErr, domain.ErrWebhookConfigNotFound) {
		return nil, "", currentErr
	}
	metadata, err := s.configRepo.RotateSigningSecret(ctx, tenantID, uuid.NewString(), encrypted, legacyKeyID, legacySecret, overlap, time.Now().UTC())
	if err != nil {
		return nil, "", err
	}
	return metadata, secret, nil
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
	} else {
		config.Secret, err = s.decryptSecret(config.Secret)
		if err != nil {
			return nil, err
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
		for _, eventType := range *update.Events {
			if !domain.IsSupportedEventType(eventType) {
				return nil, fmt.Errorf("%w: %q", ErrUnsupportedEventType, eventType)
			}
		}
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
		metadata, secret, rotateErr := s.RotateSigningSecret(ctx, 0)
		if rotateErr != nil {
			return nil, rotateErr
		}
		config.Secret = secret
		config.SigningKeyID = metadata.KeyID
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

	if err := s.persistConfig(ctx, config); err != nil {
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
	config.Secret, err = s.decryptSecret(config.Secret)
	if err != nil {
		return nil, err
	}
	if err := s.ensureSigningSecretVersion(ctx, config); err != nil {
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
		ID:           uuid.New().String(),
		TenantID:     tenantID,
		SigningKeyID: config.SigningKeyID,
		EventType:    domain.EventType("webhook.test"),
		Payload:      payload,
		Status:       domain.DeliveryPending,
		CreatedAt:    now,
		UpdatedAt:    now,
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
	config.Secret, err = s.decryptSecret(config.Secret)
	if err != nil {
		return err
	}
	delivery, err := s.configRepo.GetConfigDelivery(ctx, deliveryID, tenantID)
	if err != nil {
		return err
	}
	deliverySecret := config.Secret
	if delivery.SigningKeyID != "" {
		storedSecret, secretErr := s.configRepo.GetSigningSecret(ctx, tenantID, delivery.SigningKeyID)
		if secretErr != nil {
			return secretErr
		}
		deliverySecret, secretErr = s.decryptSecret(storedSecret)
		if secretErr != nil {
			return secretErr
		}
	}

	// Re-check the pause switch at delivery time. A scheduled resume that has
	// now elapsed lifts the pause automatically.
	if config.Paused {
		if config.ResumeAt != nil && !time.Now().UTC().Before(*config.ResumeAt) {
			config.Paused = false
			config.ResumeAt = nil
			config.UpdatedAt = time.Now().UTC()
			if err := s.persistConfig(ctx, config); err != nil {
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

	deliveryConfig := *config
	deliveryConfig.Secret = deliverySecret
	s.attemptConfigDelivery(ctx, &deliveryConfig, delivery)
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
		storedSecret := config.Secret
		config.Secret, err = s.decryptSecret(config.Secret)
		if err != nil {
			return err
		}
		if config.Secret != "" && !strings.HasPrefix(storedSecret, "v1::") {
			if err := s.persistConfig(ctx, config); err != nil {
				return err
			}
		}
		if err := s.ensureSigningSecretVersion(ctx, config); err != nil {
			return err
		}
		if !subscribedTo(config.Events, eventName) {
			continue
		}

		now := time.Now().UTC()
		delivery := &domain.TenantWebhookDelivery{
			ID:           uuid.New().String(),
			TenantID:     config.TenantID,
			SigningKeyID: config.SigningKeyID,
			EventType:    eventType,
			Payload:      body,
			Status:       domain.DeliveryPending,
			CreatedAt:    now,
			UpdatedAt:    now,
		}

		// A scheduled resume that has elapsed lifts the pause before we decide.
		paused := config.Paused
		if paused && config.ResumeAt != nil && !now.Before(*config.ResumeAt) {
			paused = false
			config.Paused = false
			config.ResumeAt = nil
			config.UpdatedAt = now
			if err := s.persistConfig(ctx, config); err != nil {
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
		if s.queueClient != nil {
			if err := s.queueClient.EnqueueTenantWebhookDelivery(ctx, delivery.ID, config.TenantID); err != nil {
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
	req.Header.Set("X-Fluxa-Signature", signBody(config.Secret, delivery.Payload))
	req.Header.Set("X-Fluxa-Timestamp", fmt.Sprintf("%d", now.Unix()))
	if delivery.SigningKeyID != "" {
		req.Header.Set("X-Fluxa-Key-ID", delivery.SigningKeyID)
	}
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
