package fiat

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) DepositRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/fiat", h.handleDeposit)
	}
}

func (h *Handler) WithdrawRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/fiat", h.handleWithdrawal)
	}
}

func (h *Handler) WebhookRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/{provider}", h.handleWebhook)
	}
}

type depositReq struct {
	Amount   string `json:"amount" validate:"required"`
	Currency string `json:"currency" validate:"required"`
	Email    string `json:"email" validate:"required,email"`
	Name     string `json:"name" validate:"required"`
}

func (h *Handler) handleDeposit(w http.ResponseWriter, r *http.Request) {
	walletID := chi.URLParam(r, "id")
	if walletID == "" {
		api.Error(w, http.StatusBadRequest, "wallet id is required")
		return
	}

	var req depositReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := api.Validate(req); err != nil {
		api.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		api.Error(w, http.StatusBadRequest, "invalid amount")
		return
	}

	dr := DepositRequest{
		WalletID:      walletID,
		Reference:     "DEP-" + uuid.New().String()[:8],
		FiatAmount:    amount,
		FiatCurrency:  req.Currency,
		CustomerEmail: req.Email,
		CustomerName:  req.Name,
	}

	resp, err := h.svc.InitiateDeposit(r.Context(), dr)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	api.JSON(w, http.StatusOK, resp)
}

type withdrawReq struct {
	Amount        string `json:"amount" validate:"required"`
	Currency      string `json:"currency" validate:"required"`
	AccountBank   string `json:"account_bank" validate:"required"`
	AccountNumber string `json:"account_number" validate:"required"`
}

func (h *Handler) handleWithdrawal(w http.ResponseWriter, r *http.Request) {
	walletID := chi.URLParam(r, "id")
	if walletID == "" {
		api.Error(w, http.StatusBadRequest, "wallet id is required")
		return
	}

	var req withdrawReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.Error(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := api.Validate(req); err != nil {
		api.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		api.Error(w, http.StatusBadRequest, "invalid amount")
		return
	}

	wr := WithdrawRequest{
		WalletID:      walletID,
		Reference:     "WIT-" + uuid.New().String()[:8],
		FiatAmount:    amount,
		FiatCurrency:  req.Currency,
		AccountBank:   req.AccountBank,
		AccountNumber: req.AccountNumber,
	}

	resp, err := h.svc.InitiateWithdrawal(r.Context(), wr)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, err.Error())
		return
	}

	api.JSON(w, http.StatusOK, resp)
}

func (h *Handler) handleWebhook(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	if provider == "" {
		api.Error(w, http.StatusBadRequest, "provider is required")
		return
	}

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		api.Error(w, http.StatusBadRequest, "read payload error")
		return
	}

	// Flutterwave sends signature in "verif-hash" header
	signature := r.Header.Get("verif-hash")

	if err := h.svc.HandleWebhook(r.Context(), payload, signature); err != nil {
		// Do not return 500 so provider won't keep retrying if it's a fatal validation error
		// Log the error though.
		api.Error(w, http.StatusBadRequest, err.Error())
		return
	}

	api.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
