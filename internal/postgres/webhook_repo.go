package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WebhookRepo struct {
	db *pgxpool.Pool
}

func NewWebhookRepo(db *pgxpool.Pool) *WebhookRepo {
	return &WebhookRepo{db: db}
}

func (r *WebhookRepo) Create(ctx context.Context, ep *domain.WebhookEndpoint) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO webhook_endpoints (id, tenant_id, url, secret, events, active, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		ep.ID, ep.TenantID, ep.URL, ep.Secret, ep.Events, ep.Active, ep.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert webhook endpoint: %w", err)
	}
	return nil
}

func (r *WebhookRepo) GetByID(ctx context.Context, id string) (*domain.WebhookEndpoint, error) {
	ep := &domain.WebhookEndpoint{}
	err := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, url, secret, events, active, created_at
		 FROM webhook_endpoints WHERE id = $1`,
		id,
	).Scan(&ep.ID, &ep.TenantID, &ep.URL, &ep.Secret, &ep.Events, &ep.Active, &ep.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWebhookNotFound
		}
		return nil, fmt.Errorf("get webhook endpoint: %w", err)
	}
	return ep, nil
}

func (r *WebhookRepo) List(ctx context.Context, tenantID *string) ([]*domain.WebhookEndpoint, error) {
	var rows pgx.Rows
	var err error
	if tenantID != nil {
		rows, err = r.db.Query(ctx,
			`SELECT id, tenant_id, url, secret, events, active, created_at
			 FROM webhook_endpoints WHERE tenant_id = $1 ORDER BY created_at DESC`,
			*tenantID,
		)
	} else {
		rows, err = r.db.Query(ctx,
			`SELECT id, tenant_id, url, secret, events, active, created_at
			 FROM webhook_endpoints ORDER BY created_at DESC`,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list webhook endpoints: %w", err)
	}
	defer rows.Close()

	var endpoints []*domain.WebhookEndpoint
	for rows.Next() {
		ep := &domain.WebhookEndpoint{}
		if err := rows.Scan(&ep.ID, &ep.TenantID, &ep.URL, &ep.Secret, &ep.Events, &ep.Active, &ep.CreatedAt); err != nil {
			return nil, err
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints, rows.Err()
}

func (r *WebhookRepo) Delete(ctx context.Context, id string, tenantID *string) error {
	var tag pgconn.CommandTag
	var err error
	if tenantID != nil {
		tag, err = r.db.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id = $1 AND tenant_id = $2`, id, *tenantID)
	} else {
		tag, err = r.db.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id = $1`, id)
	}
	if err != nil {
		return fmt.Errorf("delete webhook endpoint: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrWebhookNotFound
	}
	return nil
}

func (r *WebhookRepo) ListActiveByEvent(ctx context.Context, eventType string) ([]*domain.WebhookEndpoint, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, tenant_id, url, secret, events, active, created_at
		 FROM webhook_endpoints
		 WHERE active = TRUE AND (array_length(events, 1) IS NULL OR events = '{}' OR $1 = ANY(events))
		 ORDER BY created_at`,
		eventType,
	)
	if err != nil {
		return nil, fmt.Errorf("list active webhook endpoints: %w", err)
	}
	defer rows.Close()

	var endpoints []*domain.WebhookEndpoint
	for rows.Next() {
		ep := &domain.WebhookEndpoint{}
		if err := rows.Scan(&ep.ID, &ep.TenantID, &ep.URL, &ep.Secret, &ep.Events, &ep.Active, &ep.CreatedAt); err != nil {
			return nil, err
		}
		endpoints = append(endpoints, ep)
	}
	return endpoints, rows.Err()
}

func (r *WebhookRepo) CreateDelivery(ctx context.Context, d *domain.WebhookDelivery) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO webhook_deliveries (id, endpoint_id, event_type, payload, status, attempt_count, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		d.ID, d.EndpointID, string(d.EventType), d.Payload, string(d.Status), d.AttemptCount, d.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert webhook delivery: %w", err)
	}
	return nil
}

func (r *WebhookRepo) UpdateDelivery(ctx context.Context, d *domain.WebhookDelivery) error {
	_, err := r.db.Exec(ctx,
		`UPDATE webhook_deliveries
		 SET status = $1, response_code = $2, attempt_count = $3, last_attempt = $4
		 WHERE id = $5`,
		string(d.Status), d.ResponseCode, d.AttemptCount, d.LastAttempt, d.ID,
	)
	if err != nil {
		return fmt.Errorf("update webhook delivery: %w", err)
	}
	return nil
}

func (r *WebhookRepo) GetDeliveryByID(ctx context.Context, id string, tenantID *string) (*domain.WebhookDelivery, error) {
	d := &domain.WebhookDelivery{}
	var evType, status string
	var err error
	if tenantID != nil {
		err = r.db.QueryRow(ctx,
			`SELECT d.id, d.endpoint_id, d.event_type, d.payload, d.status, d.response_code, d.attempt_count, d.last_attempt, d.created_at
			 FROM webhook_deliveries d
			 JOIN webhook_endpoints e ON d.endpoint_id = e.id
			 WHERE d.id = $1 AND e.tenant_id = $2`,
			id, *tenantID,
		).Scan(&d.ID, &d.EndpointID, &evType, &d.Payload, &status,
			&d.ResponseCode, &d.AttemptCount, &d.LastAttempt, &d.CreatedAt)
	} else {
		err = r.db.QueryRow(ctx,
			`SELECT id, endpoint_id, event_type, payload, status, response_code, attempt_count, last_attempt, created_at
			 FROM webhook_deliveries WHERE id = $1`,
			id,
		).Scan(&d.ID, &d.EndpointID, &evType, &d.Payload, &status,
			&d.ResponseCode, &d.AttemptCount, &d.LastAttempt, &d.CreatedAt)
	}
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWebhookDeliveryNotFound
		}
		return nil, fmt.Errorf("get webhook delivery: %w", err)
	}
	d.EventType = domain.EventType(evType)
	d.Status = domain.DeliveryStatus(status)
	return d, nil
}

func (r *WebhookRepo) ListDeliveries(ctx context.Context, endpointID string, limit, offset int, tenantID *string) ([]*domain.WebhookDelivery, error) {
	var rows pgx.Rows
	var err error
	if tenantID != nil {
		rows, err = r.db.Query(ctx,
			`SELECT d.id, d.endpoint_id, d.event_type, d.payload, d.status, d.response_code, d.attempt_count, d.last_attempt, d.created_at
			 FROM webhook_deliveries d
			 JOIN webhook_endpoints e ON d.endpoint_id = e.id
			 WHERE d.endpoint_id = $1 AND e.tenant_id = $2
			 ORDER BY d.created_at DESC LIMIT $3 OFFSET $4`,
			endpointID, *tenantID, limit, offset,
		)
	} else {
		rows, err = r.db.Query(ctx,
			`SELECT id, endpoint_id, event_type, payload, status, response_code, attempt_count, last_attempt, created_at
			 FROM webhook_deliveries WHERE endpoint_id = $1
			 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
			endpointID, limit, offset,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list webhook deliveries: %w", err)
	}
	defer rows.Close()

	var deliveries []*domain.WebhookDelivery
	for rows.Next() {
		d := &domain.WebhookDelivery{}
		var evType, status string
		if err := rows.Scan(&d.ID, &d.EndpointID, &evType, &d.Payload, &status,
			&d.ResponseCode, &d.AttemptCount, &d.LastAttempt, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.EventType = domain.EventType(evType)
		d.Status = domain.DeliveryStatus(status)
		deliveries = append(deliveries, d)
	}
	return deliveries, rows.Err()
}

func (r *WebhookRepo) CountByTenant(ctx context.Context, tenantID string) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM webhook_endpoints WHERE tenant_id = $1 AND active = true`, tenantID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count webhooks by tenant: %w", err)
	}
	return count, nil
}

func (r *WebhookRepo) GetConfig(ctx context.Context, tenantID string) (*domain.TenantWebhookConfig, error) {
	config := &domain.TenantWebhookConfig{}
	err := r.db.QueryRow(ctx,
		`SELECT tenant_id, enabled, url, secret, signing_algorithm, events, paused, resume_at, last_delivered_at, created_at, updated_at
		 FROM tenant_webhook_configs WHERE tenant_id = $1`,
		tenantID,
	).Scan(
		&config.TenantID, &config.Enabled, &config.URL, &config.Secret, &config.SigningAlgorithm, &config.Events,
		&config.Paused, &config.ResumeAt, &config.LastDeliveredAt, &config.CreatedAt, &config.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrWebhookConfigNotFound
		}
		return nil, fmt.Errorf("get tenant webhook config: %w", err)
	}
	return config, nil
}

func (r *WebhookRepo) UpsertConfig(ctx context.Context, config *domain.TenantWebhookConfig) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO tenant_webhook_configs
		 (tenant_id, enabled, url, secret, signing_algorithm, events, paused, resume_at, last_delivered_at, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (tenant_id) DO UPDATE SET
		 enabled = EXCLUDED.enabled,
		 url = EXCLUDED.url,
		 secret = EXCLUDED.secret,
		 signing_algorithm = EXCLUDED.signing_algorithm,
		 events = EXCLUDED.events,
		 paused = EXCLUDED.paused,
		 resume_at = EXCLUDED.resume_at,
		 last_delivered_at = EXCLUDED.last_delivered_at,
		 updated_at = EXCLUDED.updated_at`,
		config.TenantID, config.Enabled, config.URL, config.Secret, config.SigningAlgorithm, config.Events,
		config.Paused, config.ResumeAt, config.LastDeliveredAt, config.CreatedAt, config.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("upsert tenant webhook config: %w", err)
	}
	return nil
}

func (r *WebhookRepo) ListEnabledConfigs(ctx context.Context) ([]*domain.TenantWebhookConfig, error) {
	rows, err := r.db.Query(ctx,
		`SELECT tenant_id, enabled, url, secret, signing_algorithm, events, paused, resume_at, last_delivered_at, created_at, updated_at
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
			&config.Paused, &config.ResumeAt, &config.LastDeliveredAt, &config.CreatedAt, &config.UpdatedAt,
		); err != nil {
			return nil, err
		}
		configs = append(configs, config)
	}
	return configs, rows.Err()
}

func (r *WebhookRepo) CreateConfigDelivery(ctx context.Context, delivery *domain.TenantWebhookDelivery) error {
	_, err := r.db.Exec(ctx,
		`INSERT INTO tenant_webhook_deliveries
		 (id, tenant_id, event_type, payload, status, response_code, attempt_count, last_attempt, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		delivery.ID, delivery.TenantID, string(delivery.EventType), delivery.Payload, string(delivery.Status),
		delivery.ResponseCode, delivery.AttemptCount, delivery.LastAttempt, delivery.CreatedAt, delivery.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert tenant webhook delivery: %w", err)
	}
	return nil
}

func (r *WebhookRepo) UpdateConfigDelivery(ctx context.Context, delivery *domain.TenantWebhookDelivery) error {
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

func (r *WebhookRepo) GetConfigDelivery(ctx context.Context, id, tenantID string) (*domain.TenantWebhookDelivery, error) {
	delivery := &domain.TenantWebhookDelivery{}
	var eventType, status string
	err := r.db.QueryRow(ctx,
		`SELECT id, tenant_id, event_type, payload, status, response_code, attempt_count, last_attempt, created_at, updated_at
		 FROM tenant_webhook_deliveries WHERE id = $1 AND tenant_id = $2`,
		id, tenantID,
	).Scan(
		&delivery.ID, &delivery.TenantID, &eventType, &delivery.Payload, &status, &delivery.ResponseCode,
		&delivery.AttemptCount, &delivery.LastAttempt, &delivery.CreatedAt, &delivery.UpdatedAt,
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

func (r *WebhookRepo) ListConfigDeliveries(ctx context.Context, tenantID string, limit, offset int) ([]*domain.TenantWebhookDelivery, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, tenant_id, event_type, payload, status, response_code, attempt_count, last_attempt, created_at, updated_at
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
			&delivery.AttemptCount, &delivery.LastAttempt, &delivery.CreatedAt, &delivery.UpdatedAt,
		); err != nil {
			return nil, err
		}
		delivery.EventType = domain.EventType(eventType)
		delivery.Status = domain.DeliveryStatus(status)
		deliveries = append(deliveries, delivery)
	}
	return deliveries, rows.Err()
}

func (r *WebhookRepo) UpdateConfigLastDelivered(ctx context.Context, tenantID string, deliveredAt time.Time) error {
	_, err := r.db.Exec(ctx,
		`UPDATE tenant_webhook_configs SET last_delivered_at = $1, updated_at = $1 WHERE tenant_id = $2`,
		deliveredAt, tenantID,
	)
	if err != nil {
		return fmt.Errorf("update tenant webhook last delivered: %w", err)
	}
	return nil
}
