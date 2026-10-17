package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
	"github.com/fluxa/fluxa/internal/server/idempotency"
)

type Handler struct {
	svc  Service
	idem func(http.Handler) http.Handler
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

// WithIdempotency attaches the idempotency-key middleware to the
// state-mutating route (POST /) only; reads (GET /{id}) are unaffected.
func (h *Handler) WithIdempotency(mw func(http.Handler) http.Handler) *Handler {
	h.idem = mw
	return h
}

func (h *Handler) Routes() func(r chi.Router) {
	return func(r chi.Router) {
		post := r.Post
		if h.idem != nil {
			post = r.With(h.idem).Post
		}
		post("/", h.initiateTransfer)
		r.Get("/", h.listTransfers)
		r.Get("/{id}", h.getTransaction)
		r.Post("/{id}/cancel", h.cancelTransfer)
	}
}

func (h *Handler) TransactionRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Get("/", h.listTransactions)
	}
}

type createTransferRequest struct {
	FromWalletID      string   `json:"from_wallet_id" validate:"required,uuid"`
	ToWalletID        string   `json:"to_wallet_id"   validate:"required,uuid"`
	Asset             string   `json:"asset"          validate:"required"`
	Amount            string   `json:"amount"         validate:"required"`
	Reference         string   `json:"reference,omitempty"`
	ExternalReference *string  `json:"external_reference,omitempty"`
	Tags              []string `json:"tags,omitempty"`
}

type transferResponse struct {
	ID                string   `json:"id"`
	TxHash            string   `json:"tx_hash,omitempty"`
	Type              string   `json:"type"`
	Status            string   `json:"status"`
	Mode              string   `json:"mode"`
	FromWallet        string   `json:"from_wallet_id"`
	ToWallet          string   `json:"to_wallet_id"`
	Asset             string   `json:"asset"`
	Amount            string   `json:"amount"`
	FeeAmount         string   `json:"fee_amount"`
	NetAmount         string   `json:"net_amount"`
	FeeBps            int      `json:"fee_bps"`
	Reference         string   `json:"reference,omitempty"`
	ExternalReference *string  `json:"external_reference,omitempty"`
	Tags              []string `json:"tags,omitempty"`
	FailureReason     string   `json:"failure_reason,omitempty"`
	FailureMessage    string   `json:"failure_message,omitempty"`
	CreatedAt         string   `json:"created_at"`
}

func toTransferResponse(tx *domain.Transaction) transferResponse {
	return transferResponse{
		ID:                tx.ID,
		TxHash:            tx.TxHash,
		Type:              string(tx.Type),
		Status:            string(tx.Status),
		Mode:              string(tx.Mode),
		FromWallet:        tx.FromWallet,
		ToWallet:          tx.ToWallet,
		Asset:             tx.Asset,
		Amount:            tx.Amount.StringFixed(7),
		FeeAmount:         tx.Fee.StringFixed(7),
		NetAmount:         tx.NetAmount().StringFixed(7),
		FeeBps:            tx.FeeBps,
		Reference:         tx.Reference,
		ExternalReference: tx.ExternalReference,
		Tags:              tx.Tags,
		FailureReason:     tx.FailureReason,
		FailureMessage:    tx.FailureMessage,
		CreatedAt:         tx.CreatedAt.Format(time.RFC3339),
	}
}

func (h *Handler) initiateTransfer(w http.ResponseWriter, r *http.Request) {
	idempotencyKey := idempotency.IdempotencyKeyFromContext(r.Context())
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



	extended, ok := h.svc.(interface {
		InitiateTransferExt(context.Context, TransferParams) (*domain.Transaction, error)
	})
	if !ok {
		api.InternalError(w, errors.New("extended transfers are unavailable"))
		return
	}
	tx, err := extended.InitiateTransferExt(r.Context(), TransferParams{
		FromID:            req.FromWalletID,
		ToID:              req.ToWalletID,
		Asset:             req.Asset,
		Amount:            amount,
		Reference:         req.Reference,
		ExternalReference: req.ExternalReference,
		Tags:              req.Tags,
		IdempotencyKey:    idempotencyKey,
	})
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusAccepted, toTransferResponse(tx))
}

func (h *Handler) cancelTransfer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	actor := api.ActorFromContext(r.Context())
		idempotencyKey := idempotency.IdempotencyKeyFromContext(r.Context())


		if idempotencyKey == "" {
		api.BadRequest(w, "idempotency key is required")
		return
	}

	canceller, ok := h.svc.(interface {
		CancelTransfer(context.Context, string, string, string) (*domain.Transaction, error)
	})
	if !ok {
		api.InternalError(w, errors.New("transfer cancellation is unavailable"))
		return
	}
	tx, err := canceller.CancelTransfer(r.Context(), id, actor, idempotencyKey)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, toTransferResponse(tx))
}

func (h *Handler) getTransaction(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	tx, err := h.svc.GetTransaction(r.Context(), id)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, toTransferResponse(tx))
}

func (h *Handler) listTransfers(w http.ResponseWriter, r *http.Request) {
	h.listFiltered(w, r)
}

func (h *Handler) listTransactions(w http.ResponseWriter, r *http.Request) {
	h.listFiltered(w, r)
}

func (h *Handler) listFiltered(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	walletID := q.Get("wallet_id")
	extRef := q.Get("external_reference")
	tag := q.Get("tag")

	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))

	filter := domain.TransactionFilter{
		WalletID:          walletID,
		ExternalReference: extRef,
		Tag:               tag,
		Limit:             limit,
		Offset:            offset,
	}

	filterable, ok := h.svc.(interface {
		ListTransactionsFiltered(context.Context, domain.TransactionFilter) ([]*domain.Transaction, error)
	})
	if !ok {
		api.InternalError(w, errors.New("filtered transaction listing is unavailable"))
		return
	}
	txs, err := filterable.ListTransactionsFiltered(r.Context(), filter)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	responses := make([]transferResponse, len(txs))
	for i, tx := range txs {
		responses[i] = toTransferResponse(tx)
	}

	api.JSON(w, http.StatusOK, map[string]interface{}{
		"transactions": responses,
	})
}
