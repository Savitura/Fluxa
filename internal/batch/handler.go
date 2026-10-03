package batch

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

type Handler struct {
	svc              Service
	idem             func(http.Handler) http.Handler
	assetIsSupported func(code string) bool
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

// WithIdempotency attaches the idempotency-key middleware to the
// state-mutating route (POST /) only.
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

// Routes is mounted at /v1/transfers/batch (create/get/export) and
// /v1/transfers/batches (list).
func (h *Handler) Routes() func(r chi.Router) {
	return func(r chi.Router) {
		post := r.Post
		if h.idem != nil {
			post = r.With(h.idem).Post
		}
		post("/", h.createBatch)
		post("/validate", h.validateBatch)
		r.Get("/{batchId}", h.getBatch)
		r.Get("/{batchId}/export", h.exportBatch)
	}
}

// ListRoutes is mounted at /v1/transfers/batches for history listing.
func (h *Handler) ListRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Get("/", h.listBatches)
	}
}

type batchItemRequest struct {
	ToWalletID string `json:"to_wallet_id" validate:"required,uuid"`
	Asset      string `json:"asset"        validate:"required"`
	Amount     string `json:"amount"       validate:"required"`
	Reference  string `json:"reference"`
}

type createBatchRequest struct {
	FromWalletID string             `json:"from_wallet_id" validate:"required,uuid"`
	Transfers    []batchItemRequest `json:"transfers"       validate:"required,min=1,dive"`
}

type batchTransferResponse struct {
	ID             string `json:"id"`
	ToWallet       string `json:"to_wallet_id"`
	Asset          string `json:"asset"`
	Amount         string `json:"amount"`
	Reference      string `json:"reference,omitempty"`
	Status         string `json:"status"`
	TxHash         string `json:"tx_hash,omitempty"`
	FailureReason  string `json:"failure_reason,omitempty"`
	FailureMessage string `json:"failure_message,omitempty"`
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

// listBatchResponse is one batch summary row in a history listing.
type listBatchResponse struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	TotalCount  int    `json:"total_count"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// listBatchesResponse is the GET /v1/transfers/batches envelope.
type listBatchesResponse struct {
	Batches    []listBatchResponse `json:"batches"`
	NextCursor *listCursorResponse `json:"next_cursor,omitempty"`
	Summary    *batchSummary       `json:"summary,omitempty"`
}

type listCursorResponse struct {
	CreatedAt string `json:"created_at"`
	ID        string `json:"id"`
}

type batchSummary struct {
	Total       int            `json:"total"`
	ByStatus    map[string]int `json:"by_status"`
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
			ID:             tx.ID,
			ToWallet:       tx.ToWallet,
			Asset:          tx.Asset,
			Amount:         tx.Amount.StringFixed(7),
			Reference:      tx.Reference,
			Status:         string(tx.Status),
			TxHash:         tx.TxHash,
			FailureReason:  tx.FailureReason,
			FailureMessage: tx.FailureMessage,
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

// validateBatch is the no-write preflight endpoint (POST /validate). It
// accepts the same request shape as createBatch and returns per-row results
// without persisting a batch or submitting transfers.
func (h *Handler) validateBatch(w http.ResponseWriter, r *http.Request) {
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
	for i, t := range req.Transfers {
		amount, err := decimal.NewFromString(t.Amount)
		if err != nil || amount.LessThanOrEqual(decimal.Zero) {
			// Let the service produce the row-level error; pass zero amount
			// so validateItems records invalid_amount with the original text.
			amount = decimal.Zero
			_ = t
		}
		if h.assetIsSupported != nil && !h.assetIsSupported(t.Asset) {
			// Mark unsupported asset by clearing Asset so validateItems
			// records the error; keep the original in Reference for context.
			_ = t
		}
		items[i] = Item{
			ToWalletID: t.ToWalletID,
			Asset:      t.Asset,
			Amount:     amount,
			Reference:  t.Reference,
		}
		// Re-check asset support here so the row result carries the reason.
		if h.assetIsSupported != nil && !h.assetIsSupported(t.Asset) {
			items[i].Asset = ""
			// Stash the original asset in Reference's prefix so the response
			// still shows what was requested.
			if items[i].Reference != "" {
				items[i].Reference = t.Asset + ":" + items[i].Reference
			} else {
				items[i].Reference = t.Asset
			}
		}
	}

	result, err := h.svc.Preflight(r.Context(), req.FromWalletID, items)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	// Overwrite Asset/Amount/Reference on rows with the original request
	// values so the response mirrors what the client sent.
	for i, t := range req.Transfers {
		if i < len(result.Rows) {
			result.Rows[i].Asset = t.Asset
			result.Rows[i].Amount = t.Amount
			result.Rows[i].Reference = t.Reference
		}
	}

	api.JSON(w, http.StatusOK, result)
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

// listBatches handles GET /v1/transfers/batches. Query parameters:
//
//	status      – filter by batch status
//	from_wallet – filter by source wallet (via linked transactions)
//	limit       – page size (default 20, max 100)
//	cursor      – opaque keyset from a previous page's next_cursor
func (h *Handler) listBatches(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit := 0
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			api.BadRequest(w, "limit must be a non-negative integer")
			return
		}
		limit = n
	}

	var afterCursor *ListCursor
	if c := q.Get("cursor"); c != "" {
		// Cursor format: RFC3339 timestamp + "|" + batch id
		parts := splitCursor(c)
		if parts == nil {
			api.BadRequest(w, "invalid cursor")
			return
		}
		ts, err := time.Parse(time.RFC3339, parts[0])
		if err != nil {
			api.BadRequest(w, "invalid cursor timestamp")
			return
		}
		afterCursor = &ListCursor{CreatedAt: ts, ID: parts[1]}
	}

	query := ListQuery{
		Status:      domain.BatchStatus(q.Get("status")),
		FromWallet:  q.Get("from_wallet"),
		AfterCursor: afterCursor,
		Limit:       limit,
	}

	page, err := h.svc.ListBatches(r.Context(), query)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	resp := listBatchesResponse{
		Batches: make([]listBatchResponse, len(page.Batches)),
		Summary: &batchSummary{
			ByStatus: map[string]int{},
		},
	}
	for i, b := range page.Batches {
		resp.Batches[i] = listBatchResponse{
			ID:         b.ID,
			Status:     string(b.Status),
			TotalCount: b.TotalCount,
			CreatedAt:  b.CreatedAt.Format(time.RFC3339),
			UpdatedAt:  b.UpdatedAt.Format(time.RFC3339),
		}
		resp.Summary.Total++
		resp.Summary.ByStatus[string(b.Status)]++
	}
	if page.NextCursor != nil {
		resp.NextCursor = &listCursorResponse{
			CreatedAt: page.NextCursor.CreatedAt.Format(time.RFC3339),
			ID:        page.NextCursor.ID,
		}
	}

	api.JSON(w, http.StatusOK, resp)
}

// splitCursor splits an opaque "timestamp|id" cursor. Returns nil when the
// cursor does not have exactly two parts.
func splitCursor(c string) []string {
	for i := 0; i < len(c); i++ {
		if c[i] == '|' {
			if i == 0 || i == len(c)-1 {
				return nil
			}
			return []string{c[:i], c[i+1:]}
		}
	}
	return nil
}
