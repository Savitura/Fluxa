package paymentlink

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/server/idempotency"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

type AuditLogger interface {
	Log(*http.Request, string, string, string, map[string]interface{})
}

type Handler struct {
	svc   *Service
	audit AuditLogger
	idem  func(http.Handler) http.Handler
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) WithAuditLogger(audit AuditLogger) *Handler {
	h.audit = audit
	return h
}

func (h *Handler) WithIdempotency(mw func(http.Handler) http.Handler) *Handler {
	h.idem = mw
	return h
}

func (h *Handler) Routes(read, write func(http.Handler) http.Handler) func(chi.Router) {
	return func(r chi.Router) {
		if h.idem != nil {
			r.With(write, h.idem).Post("/", h.create)
		} else {
			r.With(write).Post("/", h.create)
		}
		r.With(read).Get("/", h.list)
		r.With(read).Get("/{id}", h.get)
		r.With(write).Delete("/{id}", h.cancel)
	}
}

func (h *Handler) PublicRoutes(readLimit, checkoutLimit func(http.Handler) http.Handler) func(chi.Router) {
	return func(r chi.Router) {
		r.With(readLimit).Get("/{token}", h.getPublic)
		r.With(checkoutLimit).Post("/{token}/checkout", h.checkout)
	}
}

type createRequest struct {
	WalletID  string `json:"wallet_id"`
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	ExpiresAt string `json:"expires_at"`
}

type checkoutRequest struct {
	Email string `json:"email" validate:"required,email"`
	Name  string `json:"name" validate:"required"`
}

type linkResponse struct {
	ID          string `json:"id,omitempty"`
	Token       string `json:"token,omitempty"`
	WalletID    string `json:"wallet_id,omitempty"`
	Amount      string `json:"amount"`
	Currency    string `json:"currency"`
	Status      string `json:"status"`
	CheckoutURL string `json:"checkout_url,omitempty"`
	ExpiresAt   string `json:"expires_at"`
	CreatedAt   string `json:"created_at,omitempty"`
}

func toLinkResponse(link *Link, public bool) linkResponse {
	resp := linkResponse{
		ID:        link.ID,
		Token:     link.Token,
		WalletID:  link.WalletID,
		Amount:    link.Amount.StringFixed(4),
		Currency:  link.Currency,
		Status:    link.Status,
		ExpiresAt: link.ExpiresAt.Format(time.RFC3339),
		CreatedAt: link.CreatedAt.Format(time.RFC3339),
	}
	if public {
		resp.ID = ""
		resp.Token = ""
		resp.WalletID = ""
		resp.CreatedAt = ""
	} else {
		resp.CheckoutURL = "/pay/" + link.Token
	}
	return resp
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		api.BadRequest(w, "amount must be a positive number")
		return
	}
	expiresAt, err := time.Parse(time.RFC3339, req.ExpiresAt)
	if err != nil {
		api.BadRequest(w, "expires_at must be an RFC3339 timestamp")
		return
	}
	link, err := h.svc.Create(r.Context(), CreateParams{
		WalletID: req.WalletID, Amount: amount, Currency: req.Currency, ExpiresAt: expiresAt,
		IdempotencyKey: idempotencyKey(r),
	})
	if err != nil {
		if errors.Is(err, ErrInvalidRequest) || errors.Is(err, domain.ErrInvalidAmount) {
			api.BadRequest(w, err.Error())
		} else if errors.Is(err, ErrIdempotencyConflict) {
			api.Error(w, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY", err.Error())
		} else if errors.Is(err, domain.ErrWalletNotFound) {
			api.NotFound(w, "wallet not found for this tenant and environment")
		} else if errors.Is(err, domain.ErrUnsupportedFiatCurrency) {
			api.HandleDomainError(w, err)
		} else {
			api.InternalError(w, err)
		}
		return
	}
	api.JSON(w, http.StatusCreated, toLinkResponse(link, false))
	if h.audit != nil {
		h.audit.Log(r, "payment_link.created", "payment_link", link.ID, map[string]interface{}{
			"amount": link.Amount.String(), "currency": link.Currency, "expires_at": link.ExpiresAt,
		})
	}
}

func idempotencyKey(r *http.Request) string {
	return idempotency.IdempotencyKeyFromContext(r.Context())
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	links, err := h.svc.List(r.Context())
	if err != nil {
		api.InternalError(w, err)
		return
	}
	responses := make([]linkResponse, 0, len(links))
	for _, link := range links {
		responses = append(responses, toLinkResponse(link, false))
	}
	api.JSON(w, http.StatusOK, map[string]any{"payment_links": responses})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	link, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, toLinkResponse(link, false))
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	if err := h.svc.Cancel(r.Context(), chi.URLParam(r, "id")); err != nil {
		writeError(w, err)
		return
	}
	if h.audit != nil {
		h.audit.Log(r, "payment_link.cancelled", "payment_link", chi.URLParam(r, "id"), nil)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getPublic(w http.ResponseWriter, r *http.Request) {
	link, err := h.svc.GetPublic(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		writeError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, toLinkResponse(link, true))
}

func (h *Handler) checkout(w http.ResponseWriter, r *http.Request) {
	var req checkoutRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}
	resp, err := h.svc.Checkout(r.Context(), chi.URLParam(r, "token"), req.Email, req.Name)
	if err != nil {
		if errors.Is(err, ErrNotAvailable) {
			api.Error(w, http.StatusConflict, "PAYMENT_LINK_UNAVAILABLE", err.Error())
			return
		}
		if errors.Is(err, domain.ErrUnsupportedFiatCurrency) {
			api.HandleDomainError(w, err)
			return
		}
		api.InternalError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, resp)
}

func writeError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		api.NotFound(w, "payment link not found")
		return
	}
	if errors.Is(err, ErrNotAvailable) {
		api.Error(w, http.StatusConflict, "PAYMENT_LINK_UNAVAILABLE", err.Error())
		return
	}
	api.InternalError(w, err)
}
