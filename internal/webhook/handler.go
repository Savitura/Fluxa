package webhook

import (
	"encoding/json"
	"net/http"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	repo *postgres.WebhookRepository
}

func NewHandler(repo *postgres.WebhookRepository) *Handler {
	return &Handler{repo: repo}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/webhooks/endpoints", h.ListEndpoints)
	r.Post("/webhooks/endpoints", h.CreateEndpoint)
	r.Delete("/webhooks/endpoints/{id}", h.DeleteEndpoint)
	r.Get("/webhooks/subscriptions", h.ListSubscriptions)
	r.Post("/webhooks/subscriptions", h.CreateSubscription)
	r.Delete("/webhooks/subscriptions/{id}", h.DeleteSubscription)
}

func (h *Handler) ListEndpoints(w http.ResponseWriter, r *http.Request) {
	tID := tenant.IDFromContext(r.Context())
	var tIDPtr *string
	if tID != "" {
		tIDPtr = &tID
	}
	eps, err := h.repo.ListEndpoints(r.Context(), tIDPtr)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]interface{}{"endpoints": eps})
}

func (h *Handler) CreateEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	tID := tenant.IDFromContext(r.Context())
	var tIDPtr *string
	if tID != "" {
		tIDPtr = &tID
	}

	ep := &domain.WebhookEndpoint{
		ID:       uuid.New().String(),
		TenantID: tIDPtr,
		URL:      req.URL,
		Events:   req.Events,
		Active:   true,
	}

	if err := h.repo.CreateEndpoint(r.Context(), ep); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.WriteJSON(w, http.StatusCreated, ep)
}

func (h *Handler) DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.repo.DeleteEndpoint(r.Context(), id); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	tID := tenant.IDFromContext(r.Context())
	var tIDPtr *string
	if tID != "" {
		tIDPtr = &tID
	}
	subs, err := h.repo.ListSubscriptions(r.Context(), tIDPtr)
	if err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.WriteJSON(w, http.StatusOK, map[string]interface{}{"subscriptions": subs})
}

func (h *Handler) CreateSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EventType  string `json:"event_type"`
		WebhookURL string `json:"webhook_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.WriteError(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	sub := &domain.WebhookSubscription{
		ID:         uuid.New().String(),
		EventType:  req.EventType,
		WebhookURL: req.WebhookURL,
	}

	if err := h.repo.CreateSubscription(r.Context(), sub); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.WriteJSON(w, http.StatusCreated, sub)
}

func (h *Handler) DeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.repo.DeleteSubscription(r.Context(), id); err != nil {
		api.WriteError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
