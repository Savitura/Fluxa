package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	repo Repository
}

func NewHandler(repo Repository) *Handler {
	return &Handler{repo: repo}
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Route("/v1/webhooks", func(r chi.Router) {
		r.Get("/", h.ListEndpoints)
		r.Post("/", h.RegisterEndpoint)
		r.Delete("/{id}", h.DeleteEndpoint)
		r.Get("/{id}/deliveries", h.ListDeliveries)
		r.Get("/secret", h.GetSigningSecret)
		r.Post("/secret/rotate", h.RotateSigningSecret)
		r.With(VerifyRateLimit()).Post("/verify", h.VerifySignature)

		r.Post("/subscriptions", h.CreateSubscription)
		r.Get("/subscriptions", h.ListSubscriptions)
		r.Delete("/subscriptions/{id}", h.DeleteSubscription)
	})
}

func (h *Handler) ListEndpoints(w http.ResponseWriter, r *http.Request) {
	tID := tenant.IDFromContext(r.Context())
	var tIDPtr *string
	if tID != "" {
		tIDPtr = &tID
	}
	eps, err := h.repo.ListEndpoints(r.Context(), tIDPtr)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"endpoints": eps})
}

func (h *Handler) RegisterEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
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
		api.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.JSON(w, http.StatusCreated, ep)
}

func (h *Handler) DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.repo.DeleteEndpoint(r.Context(), id); err != nil {
		api.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
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
	deliveries, err := h.repo.ListDeliveries(r.Context(), id, limit, 0)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"deliveries": deliveries})
}

func (h *Handler) GetSigningSecret(w http.ResponseWriter, r *http.Request) {
	api.Error(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "signing secret management not yet available")
}

func (h *Handler) RotateSigningSecret(w http.ResponseWriter, r *http.Request) {
	api.Error(w, http.StatusNotImplemented, "NOT_IMPLEMENTED", "signing secret rotation not yet available")
}

func (h *Handler) VerifySignature(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Secret    string `json:"secret"`
		Timestamp string `json:"timestamp"`
		Body      string `json:"body"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	result := Verify(req.Secret, req.Timestamp, req.Body, req.Signature)
	api.JSON(w, http.StatusOK, map[string]interface{}{"valid": result.Valid, "reason": result.Reason})
}

func (h *Handler) ListSubscriptions(w http.ResponseWriter, r *http.Request) {
	tID := tenant.IDFromContext(r.Context())
	var tIDPtr *string
	if tID != "" {
		tIDPtr = &tID
	}
	subs, err := h.repo.ListSubscriptions(r.Context(), tIDPtr)
	if err != nil {
		api.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"subscriptions": subs})
}

func (h *Handler) CreateSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		EventType  string `json:"event_type"`
		WebhookURL string `json:"webhook_url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	sub := &domain.WebhookSubscription{
		ID:         uuid.New().String(),
		EventType:  req.EventType,
		WebhookURL: req.WebhookURL,
	}

	if err := h.repo.CreateSubscription(r.Context(), sub); err != nil {
		api.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.JSON(w, http.StatusCreated, sub)
}

func (h *Handler) DeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.repo.DeleteSubscription(r.Context(), id); err != nil {
		api.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func sign(secret, timestamp string, body []byte) string {
	signedPayload := timestamp + "." + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
