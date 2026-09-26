package schedule

import (
	"encoding/json"
	"net/http"
	"strconv"
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

// Routes is mounted at /v1/schedules.
func (h *Handler) Routes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/", h.create)
		r.Get("/", h.list)
		r.Patch("/{id}", h.update)
		r.Delete("/{id}", h.cancel)
		r.Get("/{id}/runs", h.listRuns)
	}
}

type createScheduleRequest struct {
	FromWalletID string `json:"from_wallet_id" validate:"required,uuid"`
	ToWalletID   string `json:"to_wallet_id"   validate:"required,uuid"`
	Asset        string `json:"asset"          validate:"required"`
	Amount       string `json:"amount"         validate:"required"`
	Frequency       string `json:"frequency"      validate:"required,oneof=daily weekly monthly"`
	Timezone        string `json:"timezone"       validate:"required"`
	MissedRunPolicy string `json:"missed_run_policy" validate:"required,oneof=skip run_once"`
	StartDate       string `json:"start_date"     validate:"required"`
	EndDate         string `json:"end_date"`
}

type updateScheduleRequest struct {
	Status    string `json:"status"    validate:"omitempty,oneof=active paused"`
	Amount          string `json:"amount"`
	Frequency       string `json:"frequency"         validate:"omitempty,oneof=daily weekly monthly"`
	Timezone        string `json:"timezone"`
	MissedRunPolicy string `json:"missed_run_policy" validate:"omitempty,oneof=skip run_once"`
	EndDate         string `json:"end_date"`
}

type scheduleResponse struct {
	ID           string `json:"id"`
	FromWalletID string `json:"from_wallet_id"`
	ToWalletID   string `json:"to_wallet_id"`
	Asset        string `json:"asset"`
	Amount          string `json:"amount"`
	Frequency       string `json:"frequency"`
	Timezone        string `json:"timezone"`
	MissedRunPolicy string `json:"missed_run_policy"`
	NextRunAt       string `json:"next_run_at"`
	EndAt           string `json:"end_at,omitempty"`
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
}

func toScheduleResponse(s *domain.Schedule) scheduleResponse {
	resp := scheduleResponse{
		ID:           s.ID,
		FromWalletID: s.FromWallet,
		ToWalletID:   s.ToWallet,
		Asset:        s.Asset,
		Amount:          s.Amount.StringFixed(7),
		Frequency:       string(s.Frequency),
		Timezone:        s.Timezone,
		MissedRunPolicy: string(s.MissedRunPolicy),
		NextRunAt:       s.NextRunAt.Format(time.RFC3339),
		Status:          string(s.Status),
		CreatedAt:       s.CreatedAt.Format(time.RFC3339),
	}
	if s.EndAt != nil {
		resp.EndAt = s.EndAt.Format(time.RFC3339)
	}
	return resp
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var req createScheduleRequest
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

	startAt, err := time.Parse(time.RFC3339, req.StartDate)
	if err != nil {
		api.BadRequest(w, "start_date must be an RFC3339 timestamp")
		return
	}

	_, err = time.LoadLocation(req.Timezone)
	if err != nil {
		api.BadRequest(w, "invalid timezone")
		return
	}

	var endAt *time.Time
	if req.EndDate != "" {
		parsed, err := time.Parse(time.RFC3339, req.EndDate)
		if err != nil {
			api.BadRequest(w, "end_date must be an RFC3339 timestamp")
			return
		}
		endAt = &parsed
	}

	sch, err := h.svc.Create(r.Context(), CreateInput{
		FromWalletID: req.FromWalletID,
		ToWalletID:   req.ToWalletID,
		Asset:        req.Asset,
		Amount:          amount,
		Frequency:       domain.ScheduleFrequency(req.Frequency),
		Timezone:        req.Timezone,
		MissedRunPolicy: domain.MissedRunPolicy(req.MissedRunPolicy),
		StartAt:         startAt,
		EndAt:           endAt,
	})
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusCreated, toScheduleResponse(sch))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	schedules, err := h.svc.List(r.Context())
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	responses := make([]scheduleResponse, len(schedules))
	for i, s := range schedules {
		responses[i] = toScheduleResponse(s)
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"schedules": responses})
}

func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req updateScheduleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}

	in := UpdateInput{}
	if req.Status != "" {
		status := domain.ScheduleStatus(req.Status)
		in.Status = &status
	}
	if req.Amount != "" {
		amount, err := decimal.NewFromString(req.Amount)
		if err != nil || amount.LessThanOrEqual(decimal.Zero) {
			api.BadRequest(w, "amount must be a positive number")
			return
		}
		in.Amount = &amount
	}
	if req.Frequency != "" {
		freq := domain.ScheduleFrequency(req.Frequency)
		in.Frequency = &freq
	}
	if req.EndDate != "" {
		parsed, err := time.Parse(time.RFC3339, req.EndDate)
		if err != nil {
			api.BadRequest(w, "end_date must be an RFC3339 timestamp")
			return
		}
		in.EndAt = &parsed
	}
	if req.Timezone != "" {
		_, err := time.LoadLocation(req.Timezone)
		if err != nil {
			api.BadRequest(w, "invalid timezone")
			return
		}
		in.Timezone = &req.Timezone
	}
	if req.MissedRunPolicy != "" {
		policy := domain.MissedRunPolicy(req.MissedRunPolicy)
		in.MissedRunPolicy = &policy
	}

	sch, err := h.svc.Update(r.Context(), id, in)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, toScheduleResponse(sch))
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.Cancel(r.Context(), id); err != nil {
		api.HandleDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// scheduleRunResponse is the JSON representation of a single ScheduleRun.
// Error details are included only when the run failed: they carry a
// user-safe message and never expose internal stack traces or secrets.
type scheduleRunResponse struct {
	ID            string  `json:"id"`
	ScheduleID    string  `json:"schedule_id"`
	ExpectedRunAt string  `json:"expected_run_at"`
	Status        string  `json:"status"`
	TransactionID *string `json:"transaction_id,omitempty"`
	Error         *string `json:"error,omitempty"`
	StartedAt     *string `json:"started_at,omitempty"`
	CompletedAt   *string `json:"completed_at,omitempty"`
	CreatedAt     string  `json:"created_at"`
}

func toScheduleRunResponse(r *domain.ScheduleRun) scheduleRunResponse {
	resp := scheduleRunResponse{
		ID:            r.ID,
		ScheduleID:    r.ScheduleID,
		ExpectedRunAt: r.ExpectedRunAt.Format(time.RFC3339),
		Status:        string(r.Status),
		TransactionID: r.TransactionID,
		CreatedAt:     r.CreatedAt.Format(time.RFC3339),
	}
	// Only surface the error field on failed runs to avoid leaking
	// internal messaging to callers who don't need it.
	if r.Status == domain.ScheduleRunStatusFailed && r.Error != nil {
		resp.Error = r.Error
	}
	if r.StartedAt != nil {
		s := r.StartedAt.Format(time.RFC3339)
		resp.StartedAt = &s
	}
	if r.CompletedAt != nil {
		c := r.CompletedAt.Format(time.RFC3339)
		resp.CompletedAt = &c
	}
	return resp
}

// listRuns handles GET /v1/schedules/{id}/runs.
// The schedule ID in the URL is verified against the caller's tenant before
// any run records are fetched; an ID belonging to another tenant returns 404.
func (h *Handler) listRuns(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	runs, err := h.svc.ListRuns(r.Context(), id, limit, offset)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	responses := make([]scheduleRunResponse, len(runs))
	for i, run := range runs {
		responses[i] = toScheduleRunResponse(run)
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"runs": responses})
}
