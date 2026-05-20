package transfer

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Routes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/", h.initiateTransfer)
		r.Get("/{id}", h.getTransaction)
	}
}

func (h *Handler) TransactionRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Get("/", h.listTransactions)
	}
}

type createTransferRequest struct {
	FromWalletID string `json:"from_wallet_id" validate:"required,uuid"`
	ToWalletID   string `json:"to_wallet_id"   validate:"required,uuid"`
	Asset        string `json:"asset"          validate:"required"`
	Amount       string `json:"amount"         validate:"required"`
}

func (h *Handler) initiateTransfer(w http.ResponseWriter, r *http.Request) {
	var req createTransferRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		api.BadRequest(w, "amount must be a positive number")
		return
	}

	tx, err := h.svc.InitiateTransfer(r.Context(), req.FromWalletID, req.ToWalletID, req.Asset, amount)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusAccepted, tx)
}

func (h *Handler) getTransaction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tx, err := h.svc.GetTransaction(r.Context(), id)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, tx)
}

func (h *Handler) listTransactions(w http.ResponseWriter, r *http.Request) {
	walletID := r.URL.Query().Get("wallet_id")
	if walletID == "" {
		api.BadRequest(w, "wallet_id query param is required")
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	txs, err := h.svc.ListTransactions(r.Context(), walletID, limit, offset)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, map[string]interface{}{
		"transactions": txs,
	})
}
