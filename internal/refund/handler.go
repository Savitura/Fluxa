package refund

import (
	"encoding/json"
	"errors"
	"net/http"

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
	idem  func(http.Handler) http.Handler
	audit AuditLogger
}

func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

func (h *Handler) WithIdempotency(mw func(http.Handler) http.Handler) *Handler {
	h.idem = mw
	return h
}

func (h *Handler) WithAuditLogger(audit AuditLogger) *Handler {
	h.audit = audit
	return h
}

func (h *Handler) Routes(read, write func(http.Handler) http.Handler) func(chi.Router) {
	return func(r chi.Router) {
		if h.idem != nil {
			r.With(write, h.idem).Post("/", h.create)
		} else {
			r.With(write).Post("/", h.create)
		}
		r.With(read).Get("/{id}", h.get)
		r.With(read).Get("/", h.list)
	}
}

type createRequest struct {
	OriginalTransactionID string `json:"original_transaction_id"`
	Amount                string `json:"amount"`
	Reason                string `json:"reason,omitempty"`
}

type response struct {
	ID                    string `json:"id"`
	OriginalTransactionID string `json:"original_transaction_id"`
	TransactionID         string `json:"transaction_id,omitempty"`
	Amount                string `json:"amount"`
	Reason                string `json:"reason,omitempty"`
	Status                string `json:"status"`
}

func toResponse(record *Record) response {
	return response{
		ID: record.ID, OriginalTransactionID: record.OriginalTransactionID,
		TransactionID: record.RefundTransactionID, Amount: record.Amount.StringFixed(7),
		Reason: record.Reason, Status: record.Status,
	}
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	// The middleware resolves Idempotency-Key (with its X-Idempotency-Key alias)
	// and puts the value on the context, so handlers never read the headers.
	key := idempotency.IdempotencyKeyFromContext(r.Context())
	var req createRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || !amount.IsPositive() || amount.Exponent() < -7 {
		api.BadRequest(w, "amount must be a positive number")
		return
	}

	if len(key) > 255 || len(req.Reason) > 500 {
		api.BadRequest(w, "idempotency key or reason exceeds its maximum length")
		return
	}
	record, err := h.svc.Create(r.Context(), req.OriginalTransactionID, amount, req.Reason, key)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidAmount) {
			api.BadRequest(w, err.Error())
			return
		}
		writeError(w, err)
		return
	}
	api.JSON(w, http.StatusAccepted, toResponse(record))
	if h.audit != nil {
		h.audit.Log(r, "refund.created", "refund", record.ID, map[string]interface{}{
			"original_transaction_id": record.OriginalTransactionID,
			"amount":                  record.Amount.String(),
		})
	}
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	record, err := h.svc.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, toResponse(record))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	originalID := r.URL.Query().Get("original_transaction_id")
	if originalID == "" {
		api.BadRequest(w, "original_transaction_id is required")
		return
	}
	records, err := h.svc.ListByOriginal(r.Context(), originalID)
	if err != nil {
		writeError(w, err)
		return
	}
	result := make([]response, 0, len(records))
	for _, record := range records {
		result = append(result, toResponse(record))
	}
	api.JSON(w, http.StatusOK, map[string]any{"refunds": result})
}

func writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		api.NotFound(w, "transaction or refund not found")
	case errors.Is(err, ErrNotRefundable), errors.Is(err, ErrAmountExceeded), errors.Is(err, ErrIdempotencyConflict):
		api.Error(w, http.StatusConflict, "REFUND_CONFLICT", err.Error())
	default:
		api.HandleDomainError(w, err)
	}
}
