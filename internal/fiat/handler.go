package fiat

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"github.com/shopspring/decimal"
)

type Handler struct {
	svc  Service
	idem func(http.Handler) http.Handler
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

// WithIdempotency attaches the idempotency-key middleware to mutating routes.
func (h *Handler) WithIdempotency(mw func(http.Handler) http.Handler) *Handler {
	h.idem = mw
	return h
}

func (h *Handler) DepositRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/fiat", h.handleDeposit)
	}
}

func (h *Handler) WithdrawRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		post := r.Post
		if h.idem != nil {
			post = r.With(h.idem).Post
		}
		post("/", h.handleWithdrawal)
		post("/fiat", h.handleWithdrawal)
	}
}

func (h *Handler) WithdrawalRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		post := r.Post
		if h.idem != nil {
			post = r.With(h.idem).Post
		}
		post("/", h.handleWithdrawal)
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
		api.BadRequest(w, "wallet id is required")
		return
	}

	var req depositReq
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
		api.BadRequest(w, "invalid amount")
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
		log.Error().Err(err).Str("wallet_id", walletID).Msg("initiate deposit failed")
		api.InternalError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, resp)
}

type withdrawReq struct {
	WalletID      string `json:"wallet_id,omitempty"`
	Amount        string `json:"amount" validate:"required"`
	Currency      string `json:"currency" validate:"required"`
	AccountBank   string `json:"account_bank" validate:"required"`
	AccountNumber string `json:"account_number" validate:"required"`
}

func (h *Handler) handleWithdrawal(w http.ResponseWriter, r *http.Request) {
	var req withdrawReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}

	walletID := chi.URLParam(r, "id")
	if walletID == "" {
		walletID = req.WalletID
	}
	if walletID == "" {
		api.BadRequest(w, "wallet id is required")
		return
	}

	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}

	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		api.BadRequest(w, "invalid amount")
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
		log.Error().Err(err).Str("wallet_id", walletID).Msg("initiate withdrawal failed")
		api.InternalError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, resp)
}

func (h *Handler) handleWebhook(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	if provider == "" {
		api.BadRequest(w, "provider is required")
		return
	}

	payload, err := io.ReadAll(r.Body)
	if err != nil {
		api.BadRequest(w, "read payload error")
		return
	}

	// Flutterwave sends signature in "verif-hash" header
	signature := r.Header.Get("verif-hash")

	if err := h.svc.HandleWebhook(r.Context(), payload, signature); err != nil {
		log.Error().Err(err).Str("provider", provider).Msg("webhook handling failed")
		// Do not return 500 so provider won't keep retrying if it's a fatal validation error
		api.BadRequest(w, "webhook validation failed")
		return
	}

	api.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
