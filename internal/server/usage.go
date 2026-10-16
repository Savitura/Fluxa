package server

import (
	"context"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/tenant"
)

type UsageQuerier interface {
	GetTenantUsage(ctx context.Context, tenantID string, mode domain.Mode) (*UsageResponse, error)
}

type UsageResponse struct {
	TotalRequests    int64 `json:"total_requests"`
	RequestsToday    int64 `json:"requests_today"`
	TransfersCount   int   `json:"transfers_count"`
	ConversionsCount int   `json:"conversions_count"`
	BatchesCount     int   `json:"batches_count"`
}

type UsageHandler struct {
	db postgres.DB
}

func NewUsageHandler(db postgres.DB) *UsageHandler {
	return &UsageHandler{db: db}
}

func (h *UsageHandler) GetUsage(w http.ResponseWriter, r *http.Request) {
	tenantID := tenant.IDFromContext(r.Context())
	mode, ok := tenant.ModeFromContext(r.Context())
	if tenantID == "" || !ok {
		api.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "tenant environment is required")
		return
	}

	ctx := r.Context()
	var transfersCount, conversionsCount, batchesCount int
	var requestsToday, totalRequests int64

	if h.db != nil {
		_ = h.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM transactions WHERE tenant_id = $1 AND mode = $2`,
			tenantID, string(mode),
		).Scan(&transfersCount)

		_ = h.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM conversions c JOIN wallets w ON c.wallet_id = w.id WHERE w.tenant_id = $1 AND w.mode = $2`,
			tenantID, string(mode),
		).Scan(&conversionsCount)

		_ = h.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM batch_transfers WHERE tenant_id = $1 AND mode = $2`,
			tenantID, string(mode),
		).Scan(&batchesCount)

		todayStart := time.Now().UTC().Truncate(24 * time.Hour)
		_ = h.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM tenant_audit_events WHERE tenant_id = $1 AND mode = $2 AND created_at >= $3`,
			tenantID, string(mode), todayStart,
		).Scan(&requestsToday)

		_ = h.db.QueryRow(ctx,
			`SELECT COUNT(*) FROM tenant_audit_events WHERE tenant_id = $1 AND mode = $2`,
			tenantID, string(mode),
		).Scan(&totalRequests)
	}

	if totalRequests == 0 {
		totalRequests = int64(transfersCount + conversionsCount + batchesCount)
	}
	if requestsToday == 0 && totalRequests > 0 {
		requestsToday = totalRequests
	}

	api.JSON(w, http.StatusOK, UsageResponse{
		TotalRequests:    totalRequests,
		RequestsToday:    requestsToday,
		TransfersCount:   transfersCount,
		ConversionsCount: conversionsCount,
		BatchesCount:     batchesCount,
	})
}
