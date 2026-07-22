package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/jackc/pgx/v5"
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

func (r *WebhookRepo) Delete(ctx context.Context, id string) error {
	tag, err := r.db.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id = $1`, id)
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

func (r *WebhookRepo) GetDeliveryByID(ctx context.Context, id string) (*domain.WebhookDelivery, error) {
	d := &domain.WebhookDelivery{}
	var evType, status string
	err := r.db.QueryRow(ctx,
		`SELECT id, endpoint_id, event_type, payload, status, response_code, attempt_count, last_attempt, created_at
		 FROM webhook_deliveries WHERE id = $1`,
		id,
	).Scan(&d.ID, &d.EndpointID, &evType, &d.Payload, &status,
		&d.ResponseCode, &d.AttemptCount, &d.LastAttempt, &d.CreatedAt)
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

func (r *WebhookRepo) ListDeliveries(ctx context.Context, endpointID string, limit, offset int) ([]*domain.WebhookDelivery, error) {
	rows, err := r.db.Query(ctx,
		`SELECT id, endpoint_id, event_type, payload, status, response_code, attempt_count, last_attempt, created_at
		 FROM webhook_deliveries WHERE endpoint_id = $1
		 ORDER BY created_at DESC LIMIT $2 OFFSET $3`,
		endpointID, limit, offset,
	)
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
	err := r.db.QueryRow(ctx, `SELECT COUNT(*) FROM webhooks WHERE tenant_id = $1 AND active = true`, tenantID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count webhooks by tenant: %w", err)
	}
	return count, nil
}

