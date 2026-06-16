package fees

import (
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/tenant"
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
		r.Get("/", h.getSchedule)
	}
}

func (h *Handler) AdminRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Get("/collected", h.listCollected)
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

	summary, err := h.svc.ListCollectedSummary(r.Context(), start, end)
	if err != nil {
		api.InternalError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, map[string]interface{}{
		"summary": summary,
	})
}
