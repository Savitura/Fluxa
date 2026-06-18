package webhook

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
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
		r.Post("/", h.register)
		r.Get("/", h.list)
		r.Delete("/{id}", h.delete)
		r.Get("/{id}/deliveries", h.listDeliveries)
	}
}

type registerRequest struct {
	URL    string   `json:"url"    validate:"required,url"`
	Events []string `json:"events"`
}

type endpointResponse struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	Secret    string   `json:"secret,omitempty"`
	Events    []string `json:"events"`
	Active    bool     `json:"active"`
	CreatedAt string   `json:"created_at"`
}

type deliveryResponse struct {
	ID           string  `json:"id"`
	EndpointID   string  `json:"endpoint_id"`
	EventType    string  `json:"event_type"`
	Status       string  `json:"status"`
	ResponseCode *int    `json:"response_code,omitempty"`
	AttemptCount int     `json:"attempt_count"`
	LastAttempt  *string `json:"last_attempt,omitempty"`
	CreatedAt    string  `json:"created_at"`
}

func toEndpointResponse(ep *domain.WebhookEndpoint, includeSecret bool) endpointResponse {
	r := endpointResponse{
		ID:        ep.ID,
		URL:       ep.URL,
		Events:    ep.Events,
		Active:    ep.Active,
		CreatedAt: ep.CreatedAt.Format(time.RFC3339),
	}
	if includeSecret {
		r.Secret = ep.Secret
	}
	return r
}

func toDeliveryResponse(d *domain.WebhookDelivery) deliveryResponse {
	r := deliveryResponse{
		ID:           d.ID,
		EndpointID:   d.EndpointID,
		EventType:    string(d.EventType),
		Status:       string(d.Status),
		ResponseCode: d.ResponseCode,
		AttemptCount: d.AttemptCount,
		CreatedAt:    d.CreatedAt.Format(time.RFC3339),
	}
	if d.LastAttempt != nil {
		s := d.LastAttempt.Format(time.RFC3339)
		r.LastAttempt = &s
	}
	return r
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	if err := api.Validate(req); err != nil {
		api.BadRequest(w, err.Error())
		return
	}

	ep, err := h.svc.Register(r.Context(), req.URL, req.Events)
	if err != nil {
		api.InternalError(w, err)
		return
	}

	api.JSON(w, http.StatusCreated, toEndpointResponse(ep, true))
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	endpoints, err := h.svc.List(r.Context())
	if err != nil {
		api.InternalError(w, err)
		return
	}

	resp := make([]endpointResponse, len(endpoints))
	for i, ep := range endpoints {
		resp[i] = toEndpointResponse(ep, false)
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"endpoints": resp})
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.Delete(r.Context(), id); err != nil {
		api.HandleDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listDeliveries(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	deliveries, err := h.svc.ListDeliveries(r.Context(), id, limit, offset)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	resp := make([]deliveryResponse, len(deliveries))
	for i, d := range deliveries {
		resp[i] = toDeliveryResponse(d)
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"deliveries": resp})
}
