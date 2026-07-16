package batch

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

// Routes is mounted at /v1/transfers/batch.
func (h *Handler) Routes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/", h.createBatch)
		r.Get("/{batchId}", h.getBatch)
		r.Get("/{batchId}/export", h.exportBatch)
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

type batchResponse struct {
	ID           string                  `json:"id"`
	Status       string                  `json:"status"`
	TotalCount   int                     `json:"total_count"`
	SuccessCount int                     `json:"success_count"`
	FailedCount  int                     `json:"failed_count"`
	CreatedAt    string                  `json:"created_at"`
	Transfers    []batchTransferResponse `json:"transfers,omitempty"`
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
	for i, t := range req.Transfers {
		amount, err := decimal.NewFromString(t.Amount)
		if err != nil || amount.LessThanOrEqual(decimal.Zero) {
			api.BadRequest(w, "amount must be a positive number")
			return
		}
		items[i] = Item{
			ToWalletID: t.ToWalletID,
			Asset:      t.Asset,
			Amount:     amount,
			Reference:  t.Reference,
		}
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
