package fx

import (
	"encoding/json"
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
		r.Post("/quote", h.getQuote)
		r.Post("/convert", h.convert)
	}
}

type quoteRequest struct {
	SourceAsset string `json:"source_asset" validate:"required"`
	DestAsset   string `json:"dest_asset"   validate:"required"`
	Amount      string `json:"amount"       validate:"required"`
}

type convertRequest struct {
	WalletID    string `json:"wallet_id"    validate:"required,uuid"`
	SourceAsset string `json:"source_asset" validate:"required"`
	DestAsset   string `json:"dest_asset"   validate:"required"`
	Amount      string `json:"amount"       validate:"required"`
}

func (h *Handler) getQuote(w http.ResponseWriter, r *http.Request) {
	var req quoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}

	quote, err := h.svc.GetQuote(r.Context(), req.SourceAsset, req.DestAsset, req.Amount)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, quote)
}

func (h *Handler) convert(w http.ResponseWriter, r *http.Request) {
	var req convertRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}

	quote, err := h.svc.GetQuote(r.Context(), req.SourceAsset, req.DestAsset, req.Amount)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	conv, err := h.svc.ExecuteConversion(r.Context(), req.WalletID, quote)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, conv)
}
