package tenant

import (
	"encoding/json"
	"net/http"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Routes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Get("/", h.GetTenant)
		r.Patch("/", h.UpdateTenant)
	}
}

type updateTenantRequest struct {
	MaxTransfersPerMonth *int `json:"max_transfers_per_month,omitempty"`
	MaxTransfersPerDay   *int `json:"max_transfers_per_day,omitempty"`
	MaxWithdrawalsPerDay *int `json:"max_withdrawals_per_day,omitempty"`
	MaxWallets           *int `json:"max_wallets,omitempty"`
	MaxWebhooks          *int `json:"max_webhooks,omitempty"`
}

type tenantResponse struct {
	ID                   string `json:"id"`
	Name                 string `json:"name"`
	Email                string `json:"email"`
	AccountType          string `json:"account_type"`
	MaxWallets           int    `json:"max_wallets"`
	MaxTransfersPerMonth *int   `json:"max_transfers_per_month,omitempty"`
	MaxTransfersPerDay   *int   `json:"max_transfers_per_day,omitempty"`
	MaxWithdrawalsPerDay *int   `json:"max_withdrawals_per_day,omitempty"`
	MaxWebhooks          int    `json:"max_webhooks"`
	CreatedAt            string `json:"created_at"`
}

func toTenantResponse(t *domain.Tenant) tenantResponse {
	return tenantResponse{
		ID:                   t.ID,
		Name:                 t.Name,
		Email:                t.Email,
		AccountType:          string(t.AccountType),
		MaxWallets:           t.MaxWallets,
		MaxTransfersPerMonth: t.MaxTransfersPerMonth,
		MaxTransfersPerDay:   t.MaxTransfersPerDay,
		MaxWithdrawalsPerDay: t.MaxWithdrawalsPerDay,
		MaxWebhooks:          t.MaxWebhooks,
		CreatedAt:            t.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (h *Handler) GetTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value("tenant_id").(string)
	if tenantID == "" {
		api.BadRequest(w, "tenant_id not found in context")
		return
	}

	t, err := h.svc.GetByID(r.Context(), tenantID)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, toTenantResponse(t))
}

func (h *Handler) UpdateTenant(w http.ResponseWriter, r *http.Request) {
	tenantID := r.Context().Value("tenant_id").(string)
	if tenantID == "" {
		api.BadRequest(w, "tenant_id not found in context")
		return
	}

	var req updateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}

	t, err := h.svc.GetByID(r.Context(), tenantID)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	if req.MaxTransfersPerMonth != nil {
		t.MaxTransfersPerMonth = req.MaxTransfersPerMonth
	}
	if req.MaxTransfersPerDay != nil {
		t.MaxTransfersPerDay = req.MaxTransfersPerDay
	}
	if req.MaxWithdrawalsPerDay != nil {
		t.MaxWithdrawalsPerDay = req.MaxWithdrawalsPerDay
	}
	if req.MaxWallets != nil {
		t.MaxWallets = *req.MaxWallets
	}
	if req.MaxWebhooks != nil {
		t.MaxWebhooks = *req.MaxWebhooks
	}

	if err := h.svc.Update(r.Context(), t); err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, toTenantResponse(t))
}
