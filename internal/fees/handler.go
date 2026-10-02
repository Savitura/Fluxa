package fees

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
	"github.com/shopspring/decimal"
)

type Handler struct {
	svc Service
	// estimator is feature-detected so deployments and test doubles that only
	// implement Service keep working; preflight is unavailable rather than
	// panicking when the concrete service does not provide it.
	estimator Estimator
}

func NewHandler(svc Service) *Handler {
	h := &Handler{svc: svc}
	if estimator, ok := svc.(Estimator); ok {
		h.estimator = estimator
	}
	return h
}

func (h *Handler) Routes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Get("/", h.getSchedule)
		r.Post("/preview", h.previewFee)
		r.Post("/estimate", h.estimateFee)
	}
}

func (h *Handler) AdminRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Get("/collected", h.listCollected)
		r.Put("/schedule", h.putSchedule)
	}
}

type feeScheduleResponse struct {
	TransferFeeBps   int    `json:"transfer_fee_bps"`
	ConversionFeeBps int    `json:"conversion_fee_bps"`
	MinFeeAmount     string `json:"min_fee_amount"`
	MaxFeeAmount     string `json:"max_fee_amount,omitempty"`
	Asset            string `json:"asset"`
}

func (h *Handler) getSchedule(w http.ResponseWriter, r *http.Request) {
	tenantID := tenant.IDFromContext(r.Context())

	schedule, err := h.svc.GetSchedule(r.Context(), tenantID)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	resp := feeScheduleResponse{
		TransferFeeBps:   schedule.TransferFeeBps,
		ConversionFeeBps: schedule.ConversionFeeBps,
		MinFeeAmount:     schedule.MinFeeAmount.StringFixed(7),
		Asset:            schedule.Asset,
	}
	if schedule.MaxFeeAmount != nil {
		resp.MaxFeeAmount = schedule.MaxFeeAmount.StringFixed(7)
	}

	api.JSON(w, http.StatusOK, resp)
}

type setScheduleReq struct {
	TransferFeeBps   int    `json:"transfer_fee_bps" validate:"min=0"`
	ConversionFeeBps int    `json:"conversion_fee_bps" validate:"min=0"`
	MinFeeAmount     string `json:"min_fee_amount"`
	MaxFeeAmount     string `json:"max_fee_amount,omitempty"`
	Asset            string `json:"asset"`
	TenantID         string `json:"tenant_id,omitempty"`
}

func (h *Handler) putSchedule(w http.ResponseWriter, r *http.Request) {
	var req setScheduleReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}

	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}

	var minFee decimal.Decimal
	if req.MinFeeAmount != "" {
		m, err := decimal.NewFromString(req.MinFeeAmount)
		if err != nil || m.LessThan(decimal.Zero) {
			api.BadRequest(w, "min_fee_amount must be a positive number")
			return
		}
		minFee = m
	}

	var maxFee *decimal.Decimal
	if req.MaxFeeAmount != "" {
		m, err := decimal.NewFromString(req.MaxFeeAmount)
		if err != nil || m.LessThan(decimal.Zero) {
			api.BadRequest(w, "max_fee_amount must be a positive number")
			return
		}
		maxFee = &m
	}

	asset := req.Asset
	if asset == "" {
		asset = "*"
	}

	var tenantID *string
	if req.TenantID != "" {
		tenantID = &req.TenantID
	}

	schedule := &domain.FeeSchedule{
		TenantID:         tenantID,
		Asset:            asset,
		TransferFeeBps:   req.TransferFeeBps,
		ConversionFeeBps: req.ConversionFeeBps,
		MinFeeAmount:     minFee,
		MaxFeeAmount:     maxFee,
	}

	if err := h.svc.SetSchedule(r.Context(), schedule); err != nil {
		api.InternalError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type previewReq struct {
	Type   string `json:"type" validate:"required,oneof=transfer conversion"`
	Asset  string `json:"asset" validate:"required"`
	Amount string `json:"amount" validate:"required"`
}

type previewResp struct {
	GrossAmount string `json:"gross_amount"`
	FeeAmount   string `json:"fee_amount"`
	NetAmount   string `json:"net_amount"`
	FeeBps      int    `json:"fee_bps"`
}

func (h *Handler) previewFee(w http.ResponseWriter, r *http.Request) {
	var req previewReq
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

	tenantID := tenant.IDFromContext(r.Context())

	var fee *TransferFee
	if req.Type == "transfer" {
		fee, err = h.svc.CalculateTransferFee(r.Context(), tenantID, req.Asset, amount)
	} else {
		fee, err = h.svc.CalculateConversionFee(r.Context(), tenantID, req.Asset, amount)
	}

	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, previewResp{
		GrossAmount: amount.StringFixed(7),
		FeeAmount:   fee.FeeAmount.StringFixed(7),
		NetAmount:   fee.NetAmount.StringFixed(7),
		FeeBps:      fee.FeeBps,
	})
}

type estimateReq struct {
	Type         string `json:"type" validate:"required,oneof=transfer batch"`
	Asset        string `json:"asset" validate:"required"`
	Amount       string `json:"amount" validate:"required"`
	Destinations int    `json:"destinations"`
}

// estimateFee is the preflight endpoint: it estimates the platform and network
// fees for a transfer or a batch without moving any funds.
func (h *Handler) estimateFee(w http.ResponseWriter, r *http.Request) {
	if h.estimator == nil {
		api.Error(w, http.StatusNotFound, "NOT_FOUND", "fee estimation unavailable")
		return
	}

	var req estimateReq
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

	tenantID := tenant.IDFromContext(r.Context())
	estimate, err := h.estimator.Estimate(r.Context(), tenantID, EstimateRequest{
		Type:         req.Type,
		Asset:        req.Asset,
		Amount:       amount,
		Destinations: req.Destinations,
	})
	if err != nil {
		if errors.Is(err, ErrInvalidEstimateRequest) {
			api.BadRequest(w, err.Error())
			return
		}
		api.HandleDomainError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, estimate)
}

func (h *Handler) listCollected(w http.ResponseWriter, r *http.Request) {
	var start, end *time.Time
	if s := r.URL.Query().Get("start_date"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			api.BadRequest(w, "start_date must be RFC3339 format")
			return
		}
		start = &t
	}
	if e := r.URL.Query().Get("end_date"); e != "" {
		t, err := time.Parse(time.RFC3339, e)
		if err != nil {
			api.BadRequest(w, "end_date must be RFC3339 format")
			return
		}
		end = &t
	}

	var tenantID *string
	if tid := r.URL.Query().Get("tenant_id"); tid != "" {
		tenantID = &tid
	}

	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		fmt.Sscanf(l, "%d", &limit)
	}

	offset := 0
	if o := r.URL.Query().Get("offset"); o != "" {
		fmt.Sscanf(o, "%d", &offset)
	}

	collections, err := h.svc.ListCollected(r.Context(), start, end, tenantID, limit, offset)
	if err != nil {
		api.InternalError(w, err)
		return
	}

	type feeCollectionResp struct {
		ID            string `json:"id"`
		TransactionID string `json:"transaction_id"`
		TenantID      string `json:"tenant_id,omitempty"`
		Asset         string `json:"asset"`
		FeeAmount     string `json:"fee_amount"`
		FeeBps        int    `json:"fee_bps"`
		CollectedAt   string `json:"collected_at"`
	}

	var data []feeCollectionResp
	for _, c := range collections {
		resp := feeCollectionResp{
			ID:            c.ID,
			TransactionID: c.TransactionID,
			Asset:         c.Asset,
			FeeAmount:     c.FeeAmount.StringFixed(7),
			FeeBps:        c.FeeBps,
			CollectedAt:   c.CollectedAt.Format(time.RFC3339),
		}
		if c.TenantID != nil {
			resp.TenantID = *c.TenantID
		}
		data = append(data, resp)
	}
	if data == nil {
		data = []feeCollectionResp{}
	}

	api.JSON(w, http.StatusOK, map[string]interface{}{
		"data": data,
	})
}
