package tenantdata

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type Queryer interface {
	QueryRow(context.Context, string, ...interface{}) pgx.Row
}

type Export struct {
	TenantID    string                       `json:"tenant_id"`
	GeneratedAt time.Time                    `json:"generated_at"`
	Data        map[string][]json.RawMessage `json:"data"`
}

type Service struct{ db Queryer }

func NewService(db Queryer) *Service { return &Service{db: db} }

// Build returns an allowlisted, tenant-scoped snapshot. Sensitive credential
// columns are excluded in SQL, rather than loaded and scrubbed after the fact.
func (s *Service) Build(ctx context.Context, tenantID string) (*Export, error) {
	if tenantID == "" {
		return nil, fmt.Errorf("tenant id is required")
	}
	queries := []struct {
		name string
		sql  string
	}{
		{"wallets", `SELECT COALESCE(jsonb_agg(to_jsonb(w) - 'encrypted_secret' ORDER BY w.created_at, w.id), '[]'::jsonb) FROM wallets w WHERE w.tenant_id = $1`},
		{"transactions", `SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY t.created_at, t.id), '[]'::jsonb) FROM transactions t WHERE t.tenant_id = $1`},
		{"balances", `SELECT COALESCE(jsonb_agg(to_jsonb(b) ORDER BY b.wallet_id, b.asset_code, b.issuer), '[]'::jsonb) FROM balances b JOIN wallets w ON w.id = b.wallet_id WHERE w.tenant_id = $1`},
		{"api_keys", `SELECT COALESCE(jsonb_agg(to_jsonb(k) - 'key_hash' ORDER BY k.created_at, k.id), '[]'::jsonb) FROM api_keys k WHERE k.tenant_id = $1`},
		{"batches", `SELECT COALESCE(jsonb_agg(to_jsonb(b) ORDER BY b.created_at, b.id), '[]'::jsonb) FROM batch_transfers b WHERE b.tenant_id = $1`},
		{"schedules", `SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY s.created_at, s.id), '[]'::jsonb) FROM schedules s WHERE s.tenant_id = $1`},
		{"beneficiaries", `SELECT COALESCE(jsonb_agg(to_jsonb(b) ORDER BY b.created_at, b.id), '[]'::jsonb) FROM beneficiaries b WHERE b.tenant_id = $1`},
		{"webhook_endpoints", `SELECT COALESCE(jsonb_agg(to_jsonb(e) - 'secret' ORDER BY e.created_at, e.id), '[]'::jsonb) FROM webhook_endpoints e WHERE e.tenant_id = $1`},
		{"webhook_subscriptions", `SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY s.created_at, s.id), '[]'::jsonb) FROM webhook_subscriptions s WHERE s.tenant_id = $1`},
		{"webhook_deliveries", `SELECT COALESCE(jsonb_agg(to_jsonb(d) - 'payload' - 'response_body' - 'error_message' ORDER BY d.created_at, d.id), '[]'::jsonb) FROM webhook_deliveries d JOIN webhook_endpoints e ON e.id = d.endpoint_id WHERE e.tenant_id = $1`},
		{"audit_events", `SELECT COALESCE(jsonb_agg(to_jsonb(a) ORDER BY a.created_at, a.id), '[]'::jsonb) FROM tenant_audit_events a WHERE a.tenant_id = $1`},
	}
	result := &Export{TenantID: tenantID, GeneratedAt: time.Now().UTC(), Data: make(map[string][]json.RawMessage, len(queries))}
	for _, query := range queries {
		var raw []byte
		if err := s.db.QueryRow(ctx, query.sql, tenantID).Scan(&raw); err != nil {
			return nil, fmt.Errorf("export %s: %w", query.name, err)
		}
		var rows []json.RawMessage
		if err := json.Unmarshal(raw, &rows); err != nil {
			return nil, fmt.Errorf("decode %s export: %w", query.name, err)
		}
		if rows == nil {
			rows = []json.RawMessage{}
		}
		result.Data[query.name] = rows
	}
	return result, nil
}
