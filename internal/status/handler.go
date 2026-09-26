package status

import (
	"encoding/json"
	"net/http"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/status", h.GetStatus)
	r.Get("/status/incidents", h.ListIncidents)
}

func (h *Handler) RegisterAdminRoutes(r chi.Router) {
	r.Post("/admin/incidents", h.CreateIncident)
	r.Patch("/admin/incidents/{id}", h.UpdateIncident)
}

func (h *Handler) GetStatus(w http.ResponseWriter, r *http.Request) {
	res, err := h.service.GetStatus(r.Context())
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, res)
}

func (h *Handler) ListIncidents(w http.ResponseWriter, r *http.Request) {
	incidents, err := h.service.ListIncidents(r.Context())
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]any{"incidents": incidents})
}

func (h *Handler) CreateIncident(w http.ResponseWriter, r *http.Request) {
	var req domain.CreateIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, r, domain.NewValidationError("invalid request body"))
		return
	}
	if err := api.Validate.Struct(req); err != nil {
		api.WriteError(w, r, domain.NewValidationError(err.Error()))
		return
	}

	inc, err := h.service.CreateIncident(r.Context(), req)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusCreated, inc)
}

func (h *Handler) UpdateIncident(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req domain.UpdateIncidentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, r, domain.NewValidationError("invalid request body"))
		return
	}
	if err := api.Validate.Struct(req); err != nil {
		api.WriteError(w, r, domain.NewValidationError(err.Error()))
		return
	}

	inc, err := h.service.UpdateIncident(r.Context(), id, req)
	if err != nil {
		api.WriteError(w, r, err)
		return
	}
	api.WriteJSON(w, http.StatusOK, inc)
}
