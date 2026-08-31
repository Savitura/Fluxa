package batch

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// idempotencyKeyHeader mirrors the header name used by
// internal/server/idempotency, duplicated here (rather than exported from
// that package) because it is only needed to decide whether this handler
// must supply a server-generated key before delegating to the shared
// middleware.
const idempotencyKeyHeader = "Idempotency-Key"

type Handler struct {
	svc              Service
	idem             func(http.Handler) http.Handler
	assetIsSupported func(code string) bool
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

// WithIdempotency attaches the idempotency-key middleware to the
// state-mutating route (POST /) only. Unlike the single-transfer endpoints
// (wallet, transfer, fx) that require the caller to supply the key, batch
// submissions treat it as optional: ensureIdempotencyKey generates one
// server-side when absent so the shared middleware always has a key to work
// with, while a request with no key still succeeds normally instead of
// being rejected.
func (h *Handler) WithIdempotency(mw func(http.Handler) http.Handler) *Handler {
	h.idem = mw
	return h
}

// WithAssetValidator sets the function used to check whether an asset code
// is supported. When set, the batch endpoint validates every item's asset
// before creating the batch and returns per-row validation errors.
func (h *Handler) WithAssetValidator(fn func(code string) bool) *Handler {
	h.assetIsSupported = fn
	return h
}

// Routes is mounted at /v1/transfers/batch.
func (h *Handler) Routes() func(r chi.Router) {
	return func(r chi.Router) {
		post := r.Post
		if h.idem != nil {
			post = r.With(ensureIdempotencyKey, h.idem).Post
		}
		post("/", h.createBatch)
		r.Get("/{batchId}", h.getBatch)
		r.Get("/{batchId}/export", h.exportBatch)
	}
}

// ensureIdempotencyKey generates a server-side UUID v4 idempotency key for
// batch requests that don't supply one, so the header stays optional for
// this endpoint: a caller that skips it still gets normal, non-deduplicated
// processing, while a caller that supplies a key still gets the duplicate
// detection implemented by the shared idempotency middleware.
func ensureIdempotencyKey(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(idempotencyKeyHeader) == "" {
			r.Header.Set(idempotencyKeyHeader, uuid.New().String())
		}
		next.ServeHTTP(w, r)
	})
}

type batchItemRequest struct {
	ToWalletID string `json:"to_wallet_id" validate:"required,uuid"`
	Asset      string `json:"asset"        validate:"required"`
	Amount     string `json:"amount"       validate:"required"`
	Reference  string `json:"reference"`
}

type createBatchRequest struct {
	FromWalletID string             `json:"from_wallet_id" validate:"required,uuid"`
	Transfers    []batchItemRequest `json:"transfers"       validate:"required,min=1,max=100,dive"`
}

type batchTransferResponse struct {
	ID        string `json:"id"`
	ToWallet  string `json:"to_wallet_id"`
	Asset     string `json:"asset"`
	Amount    string `json:"amount"`
	Reference string `json:"reference,omitempty"`
	Status    string `json:"status"`
	TxHash    string `json:"tx_hash,omitempty"`
}

// ValidationError describes a single invalid row in the batch request.
type ValidationError struct {
	Row    int    `json:"row"`
	Field  string `json:"field"`
	Value  string `json:"value"`
	Reason string `json:"reason"`
}

type batchResponse struct {
	ID               string                  `json:"id"`
	Status           string                  `json:"status"`
	TotalCount       int                     `json:"total_count"`
	SuccessCount     int                     `json:"success_count"`
	FailedCount      int                     `json:"failed_count"`
	HeldCount        int                     `json:"held_count"`
	CreatedAt        string                  `json:"created_at"`
	Transfers        []batchTransferResponse `json:"transfers,omitempty"`
	ValidationErrors []ValidationError       `json:"validation_errors,omitempty"`
}

func toBatchResponse(result *Result) batchResponse {
	resp := batchResponse{
		ID:         result.Batch.ID,
		Status:     string(result.Batch.Status),
		TotalCount: result.Batch.TotalCount,
		CreatedAt:  result.Batch.CreatedAt.Format(time.RFC3339),
	}

	resp.Transfers = make([]batchTransferResponse, len(result.Transactions))
	for i, tx := range result.Transactions {
		switch tx.Status {
		case domain.StatusConfirmed:
			resp.SuccessCount++
		case domain.StatusFailed:
			resp.FailedCount++
		case domain.StatusComplianceHold:
			resp.HeldCount++
		}
		resp.Transfers[i] = batchTransferResponse{
			ID:        tx.ID,
			ToWallet:  tx.ToWallet,
			Asset:     tx.Asset,
			Amount:    tx.Amount.StringFixed(7),
			Reference: tx.Reference,
			Status:    string(tx.Status),
			TxHash:    tx.TxHash,
		}
	}

	return resp
}

func (h *Handler) createBatch(w http.ResponseWriter, r *http.Request) {
	var req createBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}

	items := make([]Item, len(req.Transfers))
	var validationErrors []ValidationError
	for i, t := range req.Transfers {
		amount, err := decimal.NewFromString(t.Amount)
		if err != nil || amount.LessThanOrEqual(decimal.Zero) {
			validationErrors = append(validationErrors, ValidationError{
				Row:    i + 1,
				Field:  "amount",
				Value:  t.Amount,
				Reason: "amount must be a positive number",
			})
			continue
		}
		if h.assetIsSupported != nil && !h.assetIsSupported(t.Asset) {
			validationErrors = append(validationErrors, ValidationError{
				Row:    i + 1,
				Field:  "asset",
				Value:  t.Asset,
				Reason: "unsupported asset code",
			})
			continue
		}
		items[i] = Item{
			ToWalletID: t.ToWalletID,
			Asset:      t.Asset,
			Amount:     amount,
			Reference:  t.Reference,
		}
	}

	if len(validationErrors) > 0 {
		details := make([]api.ValidationErrorDetail, len(validationErrors))
		for i, ve := range validationErrors {
			details[i] = api.ValidationErrorDetail{
				Row:    ve.Row,
				Field:  ve.Field,
				Value:  ve.Value,
				Reason: ve.Reason,
			}
		}
		api.BadRequestWithValidationErrors(w, fmt.Sprintf("%d row(s) have validation errors", len(validationErrors)), details)
		return
	}

	result, err := h.svc.CreateBatch(r.Context(), req.FromWalletID, items)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusAccepted, toBatchResponse(result))
}

func (h *Handler) getBatch(w http.ResponseWriter, r *http.Request) {
	batchID := chi.URLParam(r, "batchId")
	result, err := h.svc.GetBatch(r.Context(), batchID)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, toBatchResponse(result))
}

func (h *Handler) exportBatch(w http.ResponseWriter, r *http.Request) {
	batchID := chi.URLParam(r, "batchId")
	csv, err := h.svc.ExportCSV(r.Context(), batchID)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", `attachment; filename="batch-`+batchID+`.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(csv))
}
