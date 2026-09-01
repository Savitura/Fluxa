package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type WebhookRepository struct {
	db *pgxpool.Pool
}

func NewWebhookRepository(db *pgxpool.Pool) *WebhookRepository {
	return &WebhookRepository{db: db}
}

func (r *WebhookRepository) CreateEndpoint(ctx context.Context, ep *domain.WebhookEndpoint) error {
	query := `
		INSERT INTO webhook_endpoints (id, tenant_id, url, secret, events, active, success_count, failure_count, last_delivered_at, notified_failing, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`
	_, err := r.db.Exec(ctx, query, ep.ID, ep.TenantID, ep.URL, ep.Secret, ep.Events, ep.Active, ep.SuccessCount, ep.FailureCount, ep.LastDeliveredAt, ep.NotifiedFailing, ep.CreatedAt, ep.UpdatedAt)
	return err
}

func (r *WebhookRepository) GetEndpoint(ctx context.Context, id string) (*domain.WebhookEndpoint, error) {
	query := `SELECT id, tenant_id, url, secret, events, active, success_count, failure_count, last_delivered_at, notified_failing, created_at, updated_at FROM webhook_endpoints WHERE id = $1`
	var ep domain.WebhookEndpoint
	err := r.db.QueryRow(ctx, query, id).Scan(
		&ep.ID, &ep.TenantID, &ep.URL, &ep.Secret, &ep.Events, &ep.Active, &ep.SuccessCount, &ep.FailureCount, &ep.LastDeliveredAt, &ep.NotifiedFailing, &ep.CreatedAt, &ep.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("webhook endpoint not found")
	}
	return &ep, err
}

func (r *WebhookRepository) ListEndpoints(ctx context.Context, tenantID *string) ([]*domain.WebhookEndpoint, error) {
	var rows pgx.Rows
	var err error
	if tenantID != nil {
		query := `SELECT id, tenant_id, url, secret, events, active, success_count, failure_count, last_delivered_at, notified_failing, created_at, updated_at FROM webhook_endpoints WHERE tenant_id = $1 ORDER BY created_at DESC`
		rows, err = r.db.Query(ctx, query, *tenantID)
	} else {
		query := `SELECT id, tenant_id, url, secret, events, active, success_count, failure_count, last_delivered_at, notified_failing, created_at, updated_at FROM webhook_endpoints ORDER BY created_at DESC`
		rows, err = r.db.Query(ctx, query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var endpoints []*domain.WebhookEndpoint
	for rows.Next() {
		var ep domain.WebhookEndpoint
		if err := rows.Scan(&ep.ID, &ep.TenantID, &ep.URL, &ep.Secret, &ep.Events, &ep.Active, &ep.SuccessCount, &ep.FailureCount, &ep.LastDeliveredAt, &ep.NotifiedFailing, &ep.CreatedAt, &ep.UpdatedAt); err != nil {
			return nil, err
		}
		endpoints = append(endpoints, &ep)
	}
	return endpoints, nil
}

func (r *WebhookRepository) UpdateEndpoint(ctx context.Context, ep *domain.WebhookEndpoint) error {
	query := `
		UPDATE webhook_endpoints
		SET url = $2, secret = $3, events = $4, active = $5, success_count = $6, failure_count = $7, last_delivered_at = $8, notified_failing = $9, updated_at = $10
		WHERE id = $1
	`
	_, err := r.db.Exec(ctx, query, ep.ID, ep.URL, ep.Secret, ep.Events, ep.Active, ep.SuccessCount, ep.FailureCount, ep.LastDeliveredAt, ep.NotifiedFailing, ep.UpdatedAt)
	return err
}

func (r *WebhookRepository) DeleteEndpoint(ctx context.Context, id string) error {
	query := `DELETE FROM webhook_endpoints WHERE id = $1`
	_, err := r.db.Exec(ctx, query, id)
	return err
}

func (r *WebhookRepository) CreateSubscription(ctx context.Context, sub *domain.WebhookSubscription) error {
	tID := tenant.IDFromContext(ctx)
	if tID != "" {
		sub.TenantID = &tID
	}
	query := `
		INSERT INTO webhook_subscriptions (id, tenant_id, event_type, webhook_url, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.db.Exec(ctx, query, sub.ID, nullableUUID(sub.TenantID), sub.EventType, sub.WebhookURL, sub.CreatedAt)
	return err
}

func (r *WebhookRepository) DeleteSubscription(ctx context.Context, id string) error {
	tID := tenant.IDFromContext(ctx)
	query := `DELETE FROM webhook_subscriptions WHERE id = $1`
	args := []interface{}{id}
	if tID != "" {
		query += ` AND tenant_id = $2`
		args = append(args, tID)
	}
	_, err := r.db.Exec(ctx, query, args...)
	return err
}

func (r *WebhookRepository) ListSubscriptions(ctx context.Context, tenantID *string) ([]*domain.WebhookSubscription, error) {
	var rows pgx.Rows
	var err error
	if tenantID != nil {
		query := `SELECT id, tenant_id, event_type, webhook_url, created_at FROM webhook_subscriptions WHERE tenant_id = $1 ORDER BY created_at DESC`
		rows, err = r.db.Query(ctx, query, *tenantID)
	} else {
		query := `SELECT id, tenant_id, event_type, webhook_url, created_at FROM webhook_subscriptions ORDER BY created_at DESC`
		rows, err = r.db.Query(ctx, query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []*domain.WebhookSubscription
	for rows.Next() {
		var s domain.WebhookSubscription
		if err := rows.Scan(&s.ID, &s.TenantID, &s.EventType, &s.WebhookURL, &s.CreatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, &s)
	}
	return subs, nil
}

func (r *WebhookRepository) GetSubscriptionsForEvent(ctx context.Context, tenantID *string, eventType string) ([]*domain.WebhookSubscription, error) {
	var rows pgx.Rows
	var err error
	if tenantID != nil {
		query := `SELECT id, tenant_id, event_type, webhook_url, created_at FROM webhook_subscriptions WHERE tenant_id = $1 AND event_type = $2`
		rows, err = r.db.Query(ctx, query, *tenantID, eventType)
	} else {
		query := `SELECT id, tenant_id, event_type, webhook_url, created_at FROM webhook_subscriptions WHERE event_type = $1`
		rows, err = r.db.Query(ctx, query, eventType)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []*domain.WebhookSubscription
	for rows.Next() {
		var s domain.WebhookSubscription
		if err := rows.Scan(&s.ID, &s.TenantID, &s.EventType, &s.WebhookURL, &s.CreatedAt); err != nil {
			return nil, err
		}
		subs = append(subs, &s)
	}
	return subs, nil
}

func (r *WebhookRepository) CreateDelivery(ctx context.Context, d *domain.WebhookDelivery) error {
	query := `
		INSERT INTO webhook_deliveries (id, endpoint_id, tenant_id, event_type, method, payload, status, response_code, response_body, error_message, attempt_count, max_attempts, next_attempt_at, last_attempt, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
	`
	_, err := r.db.Exec(ctx, query, d.ID, d.EndpointID, d.TenantID, d.EventType, d.Method, d.Payload, d.Status, d.ResponseCode, d.ResponseBody, d.ErrorMessage, d.AttemptCount, d.MaxAttempts, d.NextAttemptAt, d.LastAttempt, d.CreatedAt, d.UpdatedAt)
	return err
}

func (r *WebhookRepository) GetDelivery(ctx context.Context, id string) (*domain.WebhookDelivery, error) {
	query := `SELECT id, endpoint_id, tenant_id, event_type, method, payload, status, response_code, response_body, error_message, attempt_count, max_attempts, next_attempt_at, last_attempt, created_at, updated_at FROM webhook_deliveries WHERE id = $1`
	var d domain.WebhookDelivery
	err := r.db.QueryRow(ctx, query, id).Scan(
		&d.ID, &d.EndpointID, &d.TenantID, &d.EventType, &d.Method, &d.Payload, &d.Status, &d.ResponseCode, &d.ResponseBody, &d.ErrorMessage, &d.AttemptCount, &d.MaxAttempts, &d.NextAttemptAt, &d.LastAttempt, &d.CreatedAt, &d.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("webhook delivery not found")
	}
	return &d, err
}

func (r *WebhookRepository) UpdateDelivery(ctx context.Context, d *domain.WebhookDelivery) error {
	query := `
		UPDATE webhook_deliveries
		SET status = $2, response_code = $3, response_body = $4, error_message = $5, attempt_count = $6, max_attempts = $7, next_attempt_at = $8, last_attempt = $9, updated_at = $10
		WHERE id = $1
	`
	_, err := r.db.Exec(ctx, query, d.ID, d.Status, d.ResponseCode, d.ResponseBody, d.ErrorMessage, d.AttemptCount, d.MaxAttempts, d.NextAttemptAt, d.LastAttempt, d.UpdatedAt)
	return err
}

func (r *WebhookRepository) ListDeliveries(ctx context.Context, endpointID string, limit, offset int) ([]*domain.WebhookDelivery, error) {
	query := `SELECT id, endpoint_id, tenant_id, event_type, method, payload, status, response_code, response_body, error_message, attempt_count, max_attempts, next_attempt_at, last_attempt, created_at, updated_at FROM webhook_deliveries WHERE endpoint_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := r.db.Query(ctx, query, endpointID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deliveries []*domain.WebhookDelivery
	for rows.Next() {
		var d domain.WebhookDelivery
		if err := rows.Scan(&d.ID, &d.EndpointID, &d.TenantID, &d.EventType, &d.Method, &d.Payload, &d.Status, &d.ResponseCode, &d.ResponseBody, &d.ErrorMessage, &d.AttemptCount, &d.MaxAttempts, &d.NextAttemptAt, &d.LastAttempt, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		deliveries = append(deliveries, &d)
	}
	return deliveries, nil
}

func (r *WebhookRepository) CreateDeadLetter(ctx context.Context, dl *domain.WebhookDeadLetter) error {
	query := `
		INSERT INTO webhook_dead_letters (id, endpoint_id, tenant_id, delivery_id, payload, error_message, attempt_count, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err := r.db.Exec(ctx, query, dl.ID, dl.EndpointID, dl.TenantID, dl.DeliveryID, dl.Payload, dl.ErrorMessage, dl.AttemptCount, dl.CreatedAt)
	return err
}

func (r *WebhookRepository) GetDeadLetter(ctx context.Context, id string) (*domain.WebhookDeadLetter, error) {
	query := `SELECT id, endpoint_id, tenant_id, delivery_id, payload, error_message, attempt_count, created_at FROM webhook_dead_letters WHERE id = $1`
	var dl domain.WebhookDeadLetter
	err := r.db.QueryRow(ctx, query, id).Scan(
		&dl.ID, &dl.EndpointID, &dl.TenantID, &dl.DeliveryID, &dl.Payload, &dl.ErrorMessage, &dl.AttemptCount, &dl.CreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errors.New("dead letter not found")
	}
	return &dl, err
}

func (r *WebhookRepository) ListDeadLetters(ctx context.Context, tenantID *string, limit, offset int) ([]*domain.WebhookDeadLetter, error) {
	var rows pgx.Rows
	var err error
	if tenantID != nil {
		query := `SELECT id, endpoint_id, tenant_id, delivery_id, payload, error_message, attempt_count, created_at FROM webhook_dead_letters WHERE tenant_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
		rows, err = r.db.Query(ctx, query, *tenantID, limit, offset)
	} else {
		query := `SELECT id, endpoint_id, tenant_id, delivery_id, payload, error_message, attempt_count, created_at FROM webhook_dead_letters ORDER BY created_at DESC LIMIT $1 OFFSET $2`
		rows, err = r.db.Query(ctx, query, limit, offset)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deadLetters []*domain.WebhookDeadLetter
	for rows.Next() {
		var dl domain.WebhookDeadLetter
		if err := rows.Scan(&dl.ID, &dl.EndpointID, &dl.TenantID, &dl.DeliveryID, &dl.Payload, &dl.ErrorMessage, &dl.AttemptCount, &dl.CreatedAt); err != nil {
			return nil, err
		}
		deadLetters = append(deadLetters, &dl)
	}
	return deadLetters, nil
}
