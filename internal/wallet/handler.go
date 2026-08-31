package wallet

import (
	"net/http"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/wallets/{id}/balances", h.GetBalances)
}

func (h *Handler) GetBalances(w http.ResponseWriter, r *http.Request) {
	tid := tenant.IDFromContext(r.Context())
	walletID := chi.URLParam(r, "id")

	balances, err := h.svc.GetWalletBalances(r.Context(), tid, walletID)
	if err != nil {
		api.NotFound(w, "wallet not found")
		return
	}

	api.JSON(w, http.StatusOK, balances)
}
