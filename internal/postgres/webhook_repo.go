package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type WebhookRepository struct {
	db DB
}

// NewWebhookRepo is retained for worker code compiled against the older
// constructor name.
func NewWebhookRepo(db DB) *WebhookRepository {
	return NewWebhookRepository(db)
}

func NewWebhookRepository(db DB) *WebhookRepository {
	return &WebhookRepository{db: db}
}

func webhookMode(ctx context.Context) domain.Mode {
	return tenant.ModeOrDefault(ctx, domain.ModeLive)
}

func (r *WebhookRepository) CreateEndpoint(ctx context.Context, ep *domain.WebhookEndpoint) error {
	// An endpoint belongs to exactly one environment; test-mode deliveries must
	// never reach an endpoint registered by the live environment.
	if !ep.Mode.Valid() {
		ep.Mode = webhookMode(ctx)
	}
	query := `
		INSERT INTO webhook_endpoints (id, tenant_id, mode, url, secret, events, active, success_count, failure_count, last_delivered_at, notified_failing, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
	`
	db := TxFromContext(ctx, r.db)
	_, err := db.Exec(ctx, query, ep.ID, ep.TenantID, ep.Mode, ep.URL, ep.Secret, ep.Events, ep.Active, ep.SuccessCount, ep.FailureCount, ep.LastDeliveredAt, ep.NotifiedFailing, ep.CreatedAt, ep.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create webhook endpoint: %w", err)
	}
	return nil
}

func (r *WebhookRepository) GetEndpoint(ctx context.Context, id string) (*domain.WebhookEndpoint, error) {
	ep := &domain.WebhookEndpoint{}
	mode := webhookMode(ctx)
	query := `SELECT id, tenant_id, mode, url, secret, events, active, success_count, failure_count, last_delivered_at, notified_failing, created_at, updated_at
	          FROM webhook_endpoints WHERE id = $1 AND mode = $2`
	args := []interface{}{id, mode}
	if tenantID := tenant.IDFromContext(ctx); tenantID != "" {
		query += ` AND tenant_id = $3`
		args = append(args, tenantID)
	}
	err := r.db.QueryRow(ctx, query, args...).Scan(&ep.ID, &ep.TenantID, &ep.Mode, &ep.URL, &ep.Secret, &ep.Events, &ep.Active, &ep.SuccessCount, &ep.FailureCount, &ep.LastDeliveredAt, &ep.NotifiedFailing, &ep.CreatedAt, &ep.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrWebhookNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get webhook endpoint: %w", err)
	}
	return ep, nil
}

func (r *WebhookRepository) ListEndpoints(ctx context.Context, tenantID *string) ([]*domain.WebhookEndpoint, error) {
	mode := webhookMode(ctx)
	query := `SELECT id, tenant_id, mode, url, secret, events, active, success_count, failure_count, last_delivered_at, notified_failing, created_at, updated_at
	          FROM webhook_endpoints WHERE mode = $1`
	args := []interface{}{mode}
	if tenantID != nil && *tenantID != "" {
		query += ` AND tenant_id = $2`
		args = append(args, *tenantID)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list webhook endpoints: %w", err)
	}
	defer rows.Close()
	endpoints := make([]*domain.WebhookEndpoint, 0)
	for rows.Next() {
		ep := &domain.WebhookEndpoint{}
		if err := rows.Scan(&ep.ID, &ep.TenantID, &ep.Mode, &ep.URL, &ep.Secret, &ep.Events, &ep.Active, &ep.SuccessCount, &ep.FailureCount, &ep.LastDeliveredAt, &ep.NotifiedFailing, &ep.CreatedAt, &ep.UpdatedAt); err != nil {
			return nil, err
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints, rows.Err()
}

func (r *WebhookRepository) UpdateEndpoint(ctx context.Context, ep *domain.WebhookEndpoint) error {
	_, err := r.db.Exec(ctx,
		`UPDATE webhook_endpoints SET url=$2, secret=$3, events=$4, active=$5, success_count=$6, failure_count=$7,
		 last_delivered_at=$8, notified_failing=$9, updated_at=$10 WHERE id=$1 AND mode=$11`,
		ep.ID, ep.URL, ep.Secret, ep.Events, ep.Active, ep.SuccessCount, ep.FailureCount,
		ep.LastDeliveredAt, ep.NotifiedFailing, ep.UpdatedAt, webhookMode(ctx))
	if err != nil {
		return fmt.Errorf("update webhook endpoint: %w", err)
	}
	return nil
}

func (r *WebhookRepository) DeleteEndpoint(ctx context.Context, id string) error {
	query := `DELETE FROM webhook_endpoints WHERE id=$1 AND mode=$2`
	args := []interface{}{id, webhookMode(ctx)}
	if tenantID := tenant.IDFromContext(ctx); tenantID != "" {
		query += ` AND tenant_id=$3`
		args = append(args, tenantID)
	}
	result, err := r.db.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("delete webhook endpoint: %w", err)
	}
	if result.RowsAffected() == 0 {
		return domain.ErrWebhookNotFound
	}
	return nil
}

func (r *WebhookRepository) CreateSubscription(ctx context.Context, sub *domain.WebhookSubscription) error {
	if tID := tenant.IDFromContext(ctx); tID != "" {
		sub.TenantID = &tID
	}
	sub.Mode = webhookMode(ctx)
	_, err := r.db.Exec(ctx,
		`INSERT INTO webhook_subscriptions (id, tenant_id, mode, event_type, webhook_url, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		sub.ID, nullableUUID(sub.TenantID), sub.Mode, sub.EventType, sub.WebhookURL, sub.CreatedAt)
	if err != nil {
		return fmt.Errorf("create webhook subscription: %w", err)
	}
	return nil
}

func (r *WebhookRepository) DeleteSubscription(ctx context.Context, id string) error {
	query := `DELETE FROM webhook_subscriptions WHERE id=$1 AND mode=$2`
	args := []interface{}{id, webhookMode(ctx)}
	if tenantID := tenant.IDFromContext(ctx); tenantID != "" {
		query += ` AND tenant_id=$3`
		args = append(args, tenantID)
	}
	_, err := r.db.Exec(ctx, query, args...)
	return err
}

func (r *WebhookRepository) ListSubscriptions(ctx context.Context, tenantID *string) ([]*domain.WebhookSubscription, error) {
	query := `SELECT id, tenant_id, mode, event_type, webhook_url, created_at FROM webhook_subscriptions WHERE mode=$1`
	args := []interface{}{webhookMode(ctx)}
	if tenantID != nil && *tenantID != "" {
		query += ` AND tenant_id=$2`
		args = append(args, *tenantID)
	}
	query += ` ORDER BY created_at DESC`
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subs := make([]*domain.WebhookSubscription, 0)
	for rows.Next() {
		sub := &domain.WebhookSubscription{}
		if err := rows.Scan(&sub.ID, &sub.TenantID, &sub.Mode, &sub.EventType, &sub.WebhookURL, &sub.CreatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func (r *WebhookRepository) GetSubscriptionsForEvent(ctx context.Context, tenantID *string, eventType string) ([]*domain.WebhookSubscription, error) {
	query := `SELECT id, tenant_id, mode, event_type, webhook_url, created_at FROM webhook_subscriptions WHERE mode=$1 AND event_type=$2`
	args := []interface{}{webhookMode(ctx), eventType}
	if tenantID != nil && *tenantID != "" {
		query += ` AND tenant_id=$3`
		args = append(args, *tenantID)
	}
	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	subs := make([]*domain.WebhookSubscription, 0)
	for rows.Next() {
		sub := &domain.WebhookSubscription{}
		if err := rows.Scan(&sub.ID, &sub.TenantID, &sub.Mode, &sub.EventType, &sub.WebhookURL, &sub.CreatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

func (r *WebhookRepository) CreateDelivery(ctx context.Context, d *domain.WebhookDelivery) error {
	d.Mode = webhookMode(ctx)
	_, err := r.db.Exec(ctx,
		`INSERT INTO webhook_deliveries (id, endpoint_id, tenant_id, mode, event_type, method, payload, status, response_code, response_body, error_message, attempt_count, max_attempts, next_attempt_at, last_attempt, replay_of, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NULLIF($16, '')::uuid,$17,$18)`,
		d.ID, d.EndpointID, nullableUUID(d.TenantID), d.Mode, d.EventType, d.Method, d.Payload, d.Status,
		d.ResponseCode, d.ResponseBody, d.ErrorMessage, d.AttemptCount, d.MaxAttempts, d.NextAttemptAt, d.LastAttempt, d.ReplayOf, d.CreatedAt, d.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create webhook delivery: %w", err)
	}
	return nil
}

func (r *WebhookRepository) GetDelivery(ctx context.Context, id string) (*domain.WebhookDelivery, error) {
	d := &domain.WebhookDelivery{}
	err := r.db.QueryRow(ctx,
		`SELECT id, endpoint_id, tenant_id, mode, event_type, method, payload, status, response_code, response_body, error_message, attempt_count, max_attempts, next_attempt_at, last_attempt, COALESCE(replay_of::text, ''), created_at, updated_at
		 FROM webhook_deliveries WHERE id=$1 AND mode=$2`, id, webhookMode(ctx)).Scan(
		&d.ID, &d.EndpointID, &d.TenantID, &d.Mode, &d.EventType, &d.Method, &d.Payload, &d.Status, &d.ResponseCode, &d.ResponseBody, &d.ErrorMessage, &d.AttemptCount, &d.MaxAttempts, &d.NextAttemptAt, &d.LastAttempt, &d.ReplayOf, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrWebhookDeliveryNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get webhook delivery: %w", err)
	}
	return d, nil
}

func (r *WebhookRepository) UpdateDelivery(ctx context.Context, d *domain.WebhookDelivery) error {
	_, err := r.db.Exec(ctx,
		`UPDATE webhook_deliveries SET status=$2, response_code=$3, response_body=$4, error_message=$5, attempt_count=$6, max_attempts=$7, next_attempt_at=$8, last_attempt=$9, updated_at=$10 WHERE id=$1 AND mode=$11`,
		d.ID, d.Status, d.ResponseCode, d.ResponseBody, d.ErrorMessage, d.AttemptCount, d.MaxAttempts, d.NextAttemptAt, d.LastAttempt, d.UpdatedAt, webhookMode(ctx))
	return err
}

func (r *WebhookRepository) ListDeliveries(ctx context.Context, endpointID string, limit, offset int) ([]*domain.WebhookDelivery, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, endpoint_id, tenant_id, mode, event_type, method, payload, status, response_code, response_body, error_message, attempt_count, max_attempts, next_attempt_at, last_attempt, COALESCE(replay_of::text, ''), created_at, updated_at
		 FROM webhook_deliveries WHERE endpoint_id=$1 AND mode=$2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`, endpointID, webhookMode(ctx), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deliveries := make([]*domain.WebhookDelivery, 0)
	for rows.Next() {
		d := &domain.WebhookDelivery{}
		if err := rows.Scan(&d.ID, &d.EndpointID, &d.TenantID, &d.Mode, &d.EventType, &d.Method, &d.Payload, &d.Status, &d.ResponseCode, &d.ResponseBody, &d.ErrorMessage, &d.AttemptCount, &d.MaxAttempts, &d.NextAttemptAt, &d.LastAttempt, &d.ReplayOf, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		deliveries = append(deliveries, d)
	}
	return deliveries, rows.Err()
}

// deadLetterColumns is the projection shared by every dead-letter read so the
// scan order has exactly one definition.
const deadLetterColumns = `id, endpoint_id, tenant_id, mode, delivery_id, event_type, payload, error_message, attempt_count, status, replay_count, last_replayed_at, COALESCE(replay_token, ''), COALESCE(replay_delivery_id::text, ''), redacted_fields, retain_until, created_at`

func scanDeadLetter(row rowScanner, dl *domain.WebhookDeadLetter) error {
	var status string
	if err := row.Scan(
		&dl.ID, &dl.EndpointID, &dl.TenantID, &dl.Mode, &dl.DeliveryID, &dl.EventType, &dl.Payload,
		&dl.ErrorMessage, &dl.AttemptCount, &status, &dl.ReplayCount, &dl.LastReplayedAt, &dl.ReplayToken,
		&dl.ReplayDeliveryID, &dl.RedactedFields, &dl.RetainUntil, &dl.CreatedAt,
	); err != nil {
		return err
	}
	dl.Status = domain.DeadLetterStatus(status)
	return nil
}

func (r *WebhookRepository) CreateDeadLetter(ctx context.Context, dl *domain.WebhookDeadLetter) error {
	dl.Mode = webhookMode(ctx)
	if dl.Status == "" {
		dl.Status = domain.DeadLetterPending
	}
	if dl.RedactedFields == nil {
		dl.RedactedFields = []string{}
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO webhook_dead_letters (id, endpoint_id, tenant_id, mode, delivery_id, event_type, payload, error_message, attempt_count, status, replay_count, last_replayed_at, replay_token, replay_delivery_id, redacted_fields, retain_until, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,NULLIF($14, '')::uuid,$15,$16,$17)`,
		dl.ID, dl.EndpointID, nullableUUID(dl.TenantID), dl.Mode, dl.DeliveryID, dl.EventType, dl.Payload,
		dl.ErrorMessage, dl.AttemptCount, string(dl.Status), dl.ReplayCount, dl.LastReplayedAt,
		nullableStringPtr(&dl.ReplayToken), dl.ReplayDeliveryID, dl.RedactedFields, dl.RetainUntil, dl.CreatedAt)
	if err != nil {
		return fmt.Errorf("create webhook dead letter: %w", err)
	}
	return nil
}

func (r *WebhookRepository) GetDeadLetter(ctx context.Context, id string) (*domain.WebhookDeadLetter, error) {
	dl := &domain.WebhookDeadLetter{}
	err := scanDeadLetter(r.db.QueryRow(ctx,
		`SELECT `+deadLetterColumns+` FROM webhook_dead_letters WHERE id=$1 AND mode=$2`, id, webhookMode(ctx)), dl)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrDeadLetterNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get webhook dead letter: %w", err)
	}
	return dl, nil
}

func (r *WebhookRepository) GetDeadLetterForTenant(ctx context.Context, id, tenantID string) (*domain.WebhookDeadLetter, error) {
	dl := &domain.WebhookDeadLetter{}
	err := scanDeadLetter(r.db.QueryRow(ctx,
		`SELECT `+deadLetterColumns+` FROM webhook_dead_letters WHERE id=$1 AND tenant_id=$2 AND mode=$3`,
		id, tenantID, webhookMode(ctx)), dl)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrDeadLetterNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get tenant webhook dead letter: %w", err)
	}
	return dl, nil
}

func (r *WebhookRepository) ListDeadLetters(ctx context.Context, filter domain.DeadLetterFilter, limit, offset int) ([]*domain.WebhookDeadLetter, error) {
	query := `SELECT ` + deadLetterColumns + ` FROM webhook_dead_letters WHERE mode=$1`
	args := []interface{}{webhookMode(ctx)}
	add := func(column string, value interface{}) {
		args = append(args, value)
		query += fmt.Sprintf(" AND %s=$%d", column, len(args))
	}
	if filter.TenantID != "" {
		add("COALESCE(tenant_id::text, '')", filter.TenantID)
	}
	if filter.EndpointID != "" {
		add("endpoint_id", filter.EndpointID)
	}
	if filter.EventType != "" {
		add("event_type", filter.EventType)
	}
	if filter.Status != "" {
		add("status", string(filter.Status))
	}
	if filter.Since != nil {
		add("created_at >=", *filter.Since)
	}
	if filter.Until != nil {
		add("created_at <=", *filter.Until)
	}
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, limit, offset)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list webhook dead letters: %w", err)
	}
	defer rows.Close()
	deadLetters := make([]*domain.WebhookDeadLetter, 0)
	for rows.Next() {
		dl := &domain.WebhookDeadLetter{}
		if err := scanDeadLetter(rows, dl); err != nil {
			return nil, err
		}
		deadLetters = append(deadLetters, dl)
	}
	return deadLetters, rows.Err()
}

// ClaimReplay marks a dead letter replayed and inserts the delivery the replay
// produced. The dead-letter row is locked FOR UPDATE for the whole transaction,
// so two concurrent replays serialize: the second sees status='replayed' and
// returns the first request's delivery instead of creating a duplicate.
func (r *WebhookRepository) ClaimReplay(ctx context.Context, deadLetterID, tenantID string, replay *domain.WebhookDelivery, now time.Time) (*domain.WebhookDeadLetter, bool, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin webhook dead letter replay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	dl := &domain.WebhookDeadLetter{}
	err = scanDeadLetter(tx.QueryRow(ctx,
		`SELECT `+deadLetterColumns+` FROM webhook_dead_letters WHERE id=$1 AND tenant_id=$2 AND mode=$3 FOR UPDATE`,
		deadLetterID, tenantID, webhookMode(ctx)), dl)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, domain.ErrDeadLetterNotFound
	}
	if err != nil {
		return nil, false, fmt.Errorf("lock webhook dead letter: %w", err)
	}
	if dl.Status == domain.DeadLetterReplayed {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("commit webhook dead letter replay check: %w", err)
		}
		return dl, false, nil
	}
	if dl.Status == domain.DeadLetterDiscarded {
		return nil, false, fmt.Errorf("%w: dead letter was discarded", domain.ErrDeadLetterNotFound)
	}

	replay.Mode = dl.Mode
	if replay.ReplayOf == "" {
		replay.ReplayOf = dl.ID
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO webhook_deliveries (id, endpoint_id, tenant_id, mode, event_type, method, payload, status, response_code, response_body, error_message, attempt_count, max_attempts, next_attempt_at, last_attempt, replay_of, created_at, updated_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,NULLIF($16, '')::uuid,$17,$18)`,
		replay.ID, replay.EndpointID, nullableUUID(replay.TenantID), replay.Mode, replay.EventType, replay.Method,
		replay.Payload, replay.Status, replay.ResponseCode, replay.ResponseBody, replay.ErrorMessage,
		replay.AttemptCount, replay.MaxAttempts, replay.NextAttemptAt, replay.LastAttempt, replay.ReplayOf,
		replay.CreatedAt, replay.UpdatedAt); err != nil {
		return nil, false, fmt.Errorf("create replay delivery: %w", err)
	}

	if err := scanDeadLetter(tx.QueryRow(ctx,
		`UPDATE webhook_dead_letters SET status=$2, replay_count=replay_count+1, last_replayed_at=$3, replay_token=$4, replay_delivery_id=$5
		 WHERE id=$1 RETURNING `+deadLetterColumns,
		dl.ID, string(domain.DeadLetterReplayed), now, replay.ID, replay.ID), dl); err != nil {
		return nil, false, fmt.Errorf("mark webhook dead letter replayed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit webhook dead letter replay: %w", err)
	}
	return dl, true, nil
}

func (r *WebhookRepository) RecordDeliveryAttempt(ctx context.Context, attempt *domain.WebhookDeliveryAttempt) error {
	if !attempt.Mode.Valid() {
		attempt.Mode = webhookMode(ctx)
	}
	_, err := r.db.Exec(ctx,
		`INSERT INTO webhook_delivery_attempts (id, delivery_id, dead_letter_id, tenant_id, mode, kind, attempt_number, status, response_code, error_message, occurred_at)
		 VALUES ($1,$2,NULLIF($3, '')::uuid,$4,$5,$6,$7,$8,NULLIF($9, 0),$10,$11)`,
		attempt.ID, attempt.DeliveryID, attempt.DeadLetterID, nullableUUID(attempt.TenantID), attempt.Mode,
		string(attempt.Kind), attempt.AttemptNumber, attempt.Status, attempt.ResponseCode, attempt.ErrorMessage, attempt.OccurredAt)
	if err != nil {
		return fmt.Errorf("record webhook delivery attempt: %w", err)
	}
	return nil
}

func (r *WebhookRepository) ListDeliveryAttempts(ctx context.Context, deliveryID string) ([]*domain.WebhookDeliveryAttempt, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, delivery_id, COALESCE(dead_letter_id::text, ''), tenant_id, mode, kind, attempt_number, status, COALESCE(response_code, 0), COALESCE(error_message, ''), occurred_at
		 FROM webhook_delivery_attempts WHERE delivery_id=$1 AND mode=$2 ORDER BY attempt_number, occurred_at`,
		deliveryID, webhookMode(ctx))
	if err != nil {
		return nil, fmt.Errorf("list webhook delivery attempts: %w", err)
	}
	defer rows.Close()
	attempts := make([]*domain.WebhookDeliveryAttempt, 0)
	for rows.Next() {
		attempt := &domain.WebhookDeliveryAttempt{}
		var kind string
		if err := rows.Scan(&attempt.ID, &attempt.DeliveryID, &attempt.DeadLetterID, &attempt.TenantID, &attempt.Mode,
			&kind, &attempt.AttemptNumber, &attempt.Status, &attempt.ResponseCode, &attempt.ErrorMessage, &attempt.OccurredAt); err != nil {
			return nil, err
		}
		attempt.Kind = domain.AttemptKind(kind)
		attempts = append(attempts, attempt)
	}
	return attempts, rows.Err()
}

func (r *WebhookRepository) PruneDeadLetters(ctx context.Context, before time.Time) (int64, error) {
	tag, err := r.db.Exec(ctx, `DELETE FROM webhook_dead_letters WHERE mode=$1 AND created_at < $2`, webhookMode(ctx), before)
	if err != nil {
		return 0, fmt.Errorf("prune webhook dead letters: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *WebhookRepository) GetConfig(ctx context.Context, tenantID string) (*domain.TenantWebhookConfig, error) {
	config := &domain.TenantWebhookConfig{}
	err := r.db.QueryRow(ctx,
		`SELECT tenant_id, enabled, url, secret, signing_algorithm, events, paused, resume_at, last_delivered_at, created_at, updated_at, COALESCE(signing_key_id::text, '')
		 FROM tenant_webhook_configs WHERE tenant_id = $1`,
		tenantID,
	).Scan(
		&config.TenantID, &config.Enabled, &config.URL, &config.Secret, &config.SigningAlgorithm, &config.Events,
		&config.Paused, &config.ResumeAt, &config.LastDeliveredAt, &config.CreatedAt, &config.UpdatedAt, &config.SigningKeyID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWebhookConfigNotFound
		}
		return nil, fmt.Errorf("get tenant webhook config: %w", err)
	}
	return config, nil
}

func (r *WebhookRepository) UpsertConfig(ctx context.Context, config *domain.TenantWebhookConfig) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO tenant_webhook_configs
		 (tenant_id, enabled, url, secret, signing_algorithm, events, paused, resume_at, last_delivered_at, created_at, updated_at, signing_key_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NULLIF($12, '')::uuid)
		 ON CONFLICT (tenant_id) DO UPDATE SET
		 enabled = EXCLUDED.enabled,
		 url = EXCLUDED.url,
		 secret = EXCLUDED.secret,
		 signing_algorithm = EXCLUDED.signing_algorithm,
		 events = EXCLUDED.events,
		 paused = EXCLUDED.paused,
		 resume_at = EXCLUDED.resume_at,
		 last_delivered_at = EXCLUDED.last_delivered_at,
		 signing_key_id = EXCLUDED.signing_key_id,
		 updated_at = EXCLUDED.updated_at`,
		config.TenantID, config.Enabled, config.URL, config.Secret, config.SigningAlgorithm, config.Events,
		config.Paused, config.ResumeAt, config.LastDeliveredAt, config.CreatedAt, config.UpdatedAt, config.SigningKeyID,
	)
	if err != nil {
		return fmt.Errorf("upsert tenant webhook config: %w", err)
	}
	return nil
}

func (r *WebhookRepository) ListEnabledConfigs(ctx context.Context) ([]*domain.TenantWebhookConfig, error) {
	rows, err := r.db.Query(ctx,
		`SELECT tenant_id, enabled, url, secret, signing_algorithm, events, paused, resume_at, last_delivered_at, created_at, updated_at, COALESCE(signing_key_id::text, '')
		 FROM tenant_webhook_configs WHERE enabled = TRUE ORDER BY created_at`,
	)
	if err != nil {
		return nil, fmt.Errorf("list tenant webhook configs: %w", err)
	}
	defer rows.Close()

	var configs []*domain.TenantWebhookConfig
	for rows.Next() {
		config := &domain.TenantWebhookConfig{}
		if err := rows.Scan(
			&config.TenantID, &config.Enabled, &config.URL, &config.Secret, &config.SigningAlgorithm, &config.Events,
			&config.Paused, &config.ResumeAt, &config.LastDeliveredAt, &config.CreatedAt, &config.UpdatedAt, &config.SigningKeyID,
		); err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	return configs, rows.Err()
}

func (r *WebhookRepository) ListConfigs(ctx context.Context) ([]*domain.TenantWebhookConfig, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin tenant webhook secret migration scan: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx,
		`SELECT tenant_id, enabled, url, secret, signing_algorithm, events, paused, resume_at, last_delivered_at, created_at, updated_at, COALESCE(signing_key_id::text, '')
		 FROM tenant_webhook_configs ORDER BY tenant_id`)
	if err != nil {
		return nil, fmt.Errorf("list tenant webhook configs for secret migration: %w", err)
	}
	configs := make([]*domain.TenantWebhookConfig, 0)
	for rows.Next() {
		config := &domain.TenantWebhookConfig{}
		if err := rows.Scan(
			&config.TenantID, &config.Enabled, &config.URL, &config.Secret, &config.SigningAlgorithm, &config.Events,
			&config.Paused, &config.ResumeAt, &config.LastDeliveredAt, &config.CreatedAt, &config.UpdatedAt, &config.SigningKeyID,
		); err != nil {
			rows.Close()
			return nil, err
		}
		configs = append(configs, config)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit tenant webhook secret migration scan: %w", err)
	}
	return configs, nil
}

func (r *WebhookRepository) ListSigningSecrets(ctx context.Context, tenantID string) ([]*domain.WebhookSigningSecret, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin webhook signing secret inspection: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`UPDATE tenant_webhook_signing_secrets SET status = 'retired'
		 WHERE tenant_id = $1 AND status = 'overlapping' AND retired_at <= NOW()`, tenantID); err != nil {
		return nil, fmt.Errorf("expire webhook signing secrets: %w", err)
	}
	rows, err := tx.Query(ctx,
		`SELECT key_id, created_at, activated_at, retired_at, status
		 FROM tenant_webhook_signing_secrets WHERE tenant_id = $1 ORDER BY created_at DESC`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("list webhook signing secrets: %w", err)
	}
	defer rows.Close()
	secrets := make([]*domain.WebhookSigningSecret, 0)
	for rows.Next() {
		secret := &domain.WebhookSigningSecret{}
		if err := rows.Scan(&secret.KeyID, &secret.CreatedAt, &secret.ActivatedAt, &secret.RetiredAt, &secret.Status); err != nil {
			return nil, err
		}
		secrets = append(secrets, secret)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit webhook signing secret inspection: %w", err)
	}
	return secrets, nil
}

func (r *WebhookRepository) ImportLegacySigningSecret(ctx context.Context, tenantID, keyID, encryptedSecret string, now time.Time) (*domain.WebhookSigningSecret, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var lockedTenantID, activeKeyID string
	var storedSecret string
	if err := tx.QueryRow(ctx,
		`SELECT tenant_id::text, COALESCE(signing_key_id::text, ''), secret
		 FROM tenant_webhook_configs WHERE tenant_id = $1 FOR UPDATE`, tenantID).
		Scan(&lockedTenantID, &activeKeyID, &storedSecret); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWebhookConfigNotFound
		}
		return nil, fmt.Errorf("lock legacy webhook signing config: %w", err)
	}
	if activeKeyID != "" {
		metadata := &domain.WebhookSigningSecret{}
		if err := tx.QueryRow(ctx,
			`SELECT key_id, created_at, activated_at, retired_at, status
			 FROM tenant_webhook_signing_secrets WHERE key_id = $1`, activeKeyID).
			Scan(&metadata.KeyID, &metadata.CreatedAt, &metadata.ActivatedAt, &metadata.RetiredAt, &metadata.Status); err != nil {
			return nil, fmt.Errorf("read active webhook signing version: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit existing webhook signing version: %w", err)
		}
		return metadata, nil
	}
	if storedSecret == "" {
		if err := tx.Commit(ctx); err != nil {
			return nil, fmt.Errorf("commit empty webhook signing config: %w", err)
		}
		return nil, nil
	}
	metadata := &domain.WebhookSigningSecret{KeyID: keyID, CreatedAt: now, ActivatedAt: now, Status: "active"}
	if _, err := tx.Exec(ctx,
		`INSERT INTO tenant_webhook_signing_secrets (key_id, tenant_id, encrypted_secret, created_at, activated_at, status)
		 VALUES ($1, $2, $3, $4, $4, 'active')`, keyID, tenantID, encryptedSecret, now); err != nil {
		return nil, fmt.Errorf("import legacy webhook signing secret: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE tenant_webhook_configs SET secret = $1, signing_key_id = $2, updated_at = $3 WHERE tenant_id = $4`,
		encryptedSecret, keyID, now, tenantID); err != nil {
		return nil, fmt.Errorf("activate imported webhook signing secret: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE tenant_webhook_deliveries SET signing_key_id = $1
		 WHERE tenant_id = $2 AND signing_key_id IS NULL`, keyID, tenantID); err != nil {
		return nil, fmt.Errorf("pin legacy webhook deliveries: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit legacy webhook signing secret: %w", err)
	}
	return metadata, nil
}

func (r *WebhookRepository) RotateSigningSecret(ctx context.Context, tenantID, keyID, encryptedSecret, legacyKeyID, legacyEncryptedSecret string, overlap time.Duration, now time.Time) (*domain.WebhookSigningSecret, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		`INSERT INTO tenant_webhook_configs (tenant_id, secret, signing_algorithm)
		 VALUES ($1, '', 'hmac-sha256') ON CONFLICT (tenant_id) DO NOTHING`, tenantID); err != nil {
		return nil, fmt.Errorf("initialize webhook signing config: %w", err)
	}
	var lockedTenantID, activeKeyID string
	if err := tx.QueryRow(ctx,
		`SELECT tenant_id::text, COALESCE(signing_key_id::text, '') FROM tenant_webhook_configs WHERE tenant_id = $1 FOR UPDATE`, tenantID).Scan(&lockedTenantID, &activeKeyID); err != nil {
		return nil, fmt.Errorf("lock webhook signing config: %w", err)
	}
	if overlap > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE tenant_webhook_signing_secrets SET status = 'overlapping', retired_at = $2
			 WHERE tenant_id = $1 AND status = 'active'`, tenantID, now.Add(overlap)); err != nil {
			return nil, err
		}
	} else if _, err := tx.Exec(ctx,
		`UPDATE tenant_webhook_signing_secrets SET status = 'retired', retired_at = $2
		 WHERE tenant_id = $1 AND status IN ('active', 'overlapping')`, tenantID, now); err != nil {
		return nil, err
	}
	if activeKeyID == "" && legacyKeyID != "" && legacyEncryptedSecret != "" {
		legacyStatus := "retired"
		legacyRetiredAt := now
		if overlap > 0 {
			legacyStatus = "overlapping"
			legacyRetiredAt = now.Add(overlap)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO tenant_webhook_signing_secrets (key_id, tenant_id, encrypted_secret, created_at, activated_at, retired_at, status)
			 VALUES ($1, $2, $3, $4, $4, $5, $6)`,
			legacyKeyID, tenantID, legacyEncryptedSecret, now, legacyRetiredAt, legacyStatus); err != nil {
			return nil, fmt.Errorf("preserve legacy webhook signing secret: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`UPDATE tenant_webhook_deliveries SET signing_key_id = $1
			 WHERE tenant_id = $2 AND signing_key_id IS NULL`, legacyKeyID, tenantID); err != nil {
			return nil, fmt.Errorf("pin pending legacy webhook deliveries: %w", err)
		}
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO tenant_webhook_signing_secrets (key_id, tenant_id, encrypted_secret, created_at, activated_at, status)
		 VALUES ($1, $2, $3, $4, $4, 'active')`, keyID, tenantID, encryptedSecret, now); err != nil {
		return nil, fmt.Errorf("insert webhook signing secret: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE tenant_webhook_configs SET secret = $1, signing_key_id = $2, updated_at = $3 WHERE tenant_id = $4`,
		encryptedSecret, keyID, now, tenantID); err != nil {
		return nil, fmt.Errorf("activate webhook signing secret: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit webhook signing secret rotation: %w", err)
	}
	return &domain.WebhookSigningSecret{KeyID: keyID, CreatedAt: now, ActivatedAt: now, Status: "active"}, nil
}

func (r *WebhookRepository) GetSigningSecret(ctx context.Context, tenantID, keyID string) (string, error) {
	var encrypted string
	err := r.db.QueryRow(ctx,
		`SELECT encrypted_secret FROM tenant_webhook_signing_secrets WHERE tenant_id = $1 AND key_id = $2`,
		tenantID, keyID).Scan(&encrypted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", domain.ErrWebhookConfigNotFound
		}
		return "", fmt.Errorf("get webhook signing secret: %w", err)
	}
	return encrypted, nil
}

func (r *WebhookRepository) CreateConfigDelivery(ctx context.Context, delivery *domain.TenantWebhookDelivery) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO tenant_webhook_deliveries
		 (id, tenant_id, event_type, payload, status, response_code, attempt_count, last_attempt, created_at, updated_at, signing_key_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, '')::uuid)`,
		delivery.ID, delivery.TenantID, string(delivery.EventType), delivery.Payload, string(delivery.Status),
		delivery.ResponseCode, delivery.AttemptCount, delivery.LastAttempt, delivery.CreatedAt, delivery.UpdatedAt, delivery.SigningKeyID,
	)
	if err != nil {
		return fmt.Errorf("insert tenant webhook delivery: %w", err)
	}
	return nil
}

func (r *WebhookRepository) UpdateConfigDelivery(ctx context.Context, delivery *domain.TenantWebhookDelivery) error {
	_, err := r.db.Exec(ctx,
		`UPDATE tenant_webhook_deliveries
		 SET status = $1, response_code = $2, attempt_count = $3, last_attempt = $4, updated_at = $5
		 WHERE id = $6 AND tenant_id = $7`,
		string(delivery.Status), delivery.ResponseCode, delivery.AttemptCount, delivery.LastAttempt,
		delivery.UpdatedAt, delivery.ID, delivery.TenantID,
	)
	if err != nil {
		return fmt.Errorf("update tenant webhook delivery: %w", err)
	}
	return nil
}

func (r *WebhookRepository) GetConfigDelivery(ctx context.Context, id, tenantID string) (*domain.TenantWebhookDelivery, error) {
	delivery := &domain.TenantWebhookDelivery{}
	var eventType, status string
	err := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, event_type, payload, status, response_code, attempt_count, last_attempt, created_at, updated_at, COALESCE(signing_key_id::text, '')
		 FROM tenant_webhook_deliveries WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	).Scan(
		&delivery.ID, &delivery.TenantID, &eventType, &delivery.Payload, &status, &delivery.ResponseCode,
		&delivery.AttemptCount, &delivery.LastAttempt, &delivery.CreatedAt, &delivery.UpdatedAt, &delivery.SigningKeyID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWebhookDeliveryNotFound
		}
		return nil, fmt.Errorf("get tenant webhook delivery: %w", err)
	}
	delivery.EventType = domain.EventType(eventType)
	delivery.Status = domain.DeliveryStatus(status)
	return delivery, nil
}

func (r *WebhookRepository) ListConfigDeliveries(ctx context.Context, tenantID string, limit, offset int) ([]*domain.TenantWebhookDelivery, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, tenant_id, event_type, payload, status, response_code, attempt_count, last_attempt, created_at, updated_at, COALESCE(signing_key_id::text, '')
		 FROM tenant_webhook_deliveries WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		tenantID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("list tenant webhook deliveries: %w", err)
	}
	defer rows.Close()

	var deliveries []*domain.TenantWebhookDelivery
	for rows.Next() {
		delivery := &domain.TenantWebhookDelivery{}
		var eventType, status string
		if err := rows.Scan(
			&delivery.ID, &delivery.TenantID, &eventType, &delivery.Payload, &status, &delivery.ResponseCode,
			&delivery.AttemptCount, &delivery.LastAttempt, &delivery.CreatedAt, &delivery.UpdatedAt, &delivery.SigningKeyID,
		); err != nil {
			return nil, err
		}
		delivery.EventType = domain.EventType(eventType)
		delivery.Status = domain.DeliveryStatus(status)
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func (r *WebhookRepository) UpdateConfigLastDelivered(ctx context.Context, tenantID string, deliveredAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE tenant_webhook_configs SET last_delivered_at = $1, updated_at = $1 WHERE tenant_id = $2`,
		deliveredAt, tenantID,
	)
	if err != nil {
		return fmt.Errorf("update tenant webhook last delivered: %w", err)
	}
	return nil
}
