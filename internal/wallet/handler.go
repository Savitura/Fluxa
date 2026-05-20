package wallet

import (
	"net/http"

	"github.com/fluxa/fluxa/internal/api"
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
		r.Post("/", h.createWallet)
		r.Get("/{id}/balances", h.getBalances)
	}
}

func (h *Handler) createWallet(w http.ResponseWriter, r *http.Request) {
	wallet, err := h.svc.CreateWallet(r.Context())
	if err != nil {
		api.InternalError(w, err)
		return
	}

	api.JSON(w, http.StatusCreated, map[string]interface{}{
		"id":         wallet.ID,
		"public_key": wallet.PublicKey,
		"created_at": wallet.CreatedAt,
	})
}

func (h *Handler) getBalances(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	balances, err := h.svc.GetBalances(r.Context(), id)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, map[string]interface{}{
		"wallet_id": id,
		"balances":  balances,
	})
}
