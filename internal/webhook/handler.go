package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	svc Service
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
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
	eps, err := h.svc.ListEndpoints(r.Context())
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

	ep, _, err := h.svc.RegisterEndpoint(r.Context(), req.URL, req.Events)
	if err != nil {
		if errors.Is(err, ErrUnsafeWebhookURL) {
			api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		api.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.JSON(w, http.StatusCreated, ep)
}

func (h *Handler) DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.DeleteEndpoint(r.Context(), id); err != nil {
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
	deliveries, err := h.svc.ListDeliveries(r.Context(), id, limit)
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
	subs, err := h.svc.ListSubscriptions(r.Context())
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

	sub, err := h.svc.CreateSubscription(r.Context(), req.EventType, req.WebhookURL)
	if err != nil {
		if errors.Is(err, ErrUnsafeWebhookURL) {
			api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		api.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	api.JSON(w, http.StatusCreated, sub)
}

func (h *Handler) DeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.DeleteSubscription(r.Context(), id); err != nil {
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
