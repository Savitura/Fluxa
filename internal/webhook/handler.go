package webhook

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Post("/webhooks", h.RegisterEndpoint)
	r.Get("/webhooks", h.ListEndpoints)
	r.Delete("/webhooks/{id}", h.DeleteEndpoint)
	r.Get("/webhooks/{id}/deliveries", h.ListDeliveries)
	r.Get("/webhooks/{id}/health", h.GetEndpointHealth)
	r.Get("/webhooks/dead-letters", h.ListDeadLetters)
	r.Post("/webhooks/dead-letters/{id}/replay", h.ReplayDeadLetter)
}

type RegisterRequest struct {
	URL    string   `json:"url" validate:"required,url"`
	Events []string `json:"events"`
}

func (h *Handler) RegisterEndpoint(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	ep, secret, err := h.svc.RegisterEndpoint(r.Context(), req.URL, req.Events)
	if err != nil {
		api.WriteError(w, http.StatusBadRequest, "REGISTRATION_FAILED", err.Error())
		return
	}
	api.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"id":         ep.ID,
		"url":        ep.URL,
		"secret":     secret,
		"events":     ep.Events,
		"active":     ep.Active,
		"created_at": ep.CreatedAt,
	})
}

func (h *Handler) ListEndpoints(w http.ResponseWriter, r *http.Request) {
	endpoints, err := h.svc.ListEndpoints(r.Context())
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"endpoints": endpoints,
	})
}

func (h *Handler) DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.DeleteEndpoint(r.Context(), id); err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "webhook endpoint not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListDeliveries(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	deliveries, err := h.svc.ListDeliveries(r.Context(), id, limit)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"deliveries": deliveries,
	})
}

func (h *Handler) GetEndpointHealth(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	h, err := h.svc.GetEndpointHealth(r.Context(), id)
	if err != nil {
		api.WriteError(w, http.StatusNotFound, "NOT_FOUND", "webhook endpoint not found")
		return
	}
	api.WriteJSON(w, http.StatusOK, h)
}

func (h *Handler) ListDeadLetters(w http.ResponseWriter, r *http.Request) {
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	dls, err := h.svc.ListDeadLetters(r.Context(), limit)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"dead_letters": dls,
	})
}

func (h *Handler) ReplayDeadLetter(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.ReplayDeadLetter(r.Context(), id); err != nil {
		api.WriteError(w, http.StatusBadRequest, "REPLAY_FAILED", err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
