package webhook

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/server"
)

// Handler handles webhook endpoints and verification.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
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
	});
}

func (h *Handler) ListEndpoints(w http.ResponseWriter, r *http.Request) {
	orgID := server.GetOrgID(r.Context())
	endpoints, err := h.service.ListEndpoints(r.Context(), orgID)
	dataMap := map[string]interface{}{}
	if err != nil {
		dataMap["endpoints"] = []interface{}{}
	} else {
		dataMap["endpoints"] = endpoints
	}
	api.RespondJSON(w, http.StatusOK, dataMap)
}

func (h *Handler) RegisterEndpoint(w http.ResponseWriter, r *http.Request) {
	orgID := server.GetOrgID(r.Context())
	var req struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ep, err := h.service.RegisterEndpoint(r.Context(), orgID, req.URL, req.Events)
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.RespondJSON(w, http.StatusCreated, ep)
}

func (h *Handler) DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.service.DeleteEndpoint(r.Context(), id); err != nil {
		api.RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListDeliveries(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	deliveries, err := h.service.ListDeliveries(r.Context(), id)
	dataMap := map[string]interface{}{}
	if err != nil {
		dataMap["deliveries"] = []interface{}{}
	} else {
		dataMap["deliveries"] = deliveries
	}
	api.RespondJSON(w, http.StatusOK, dataMap)
}

func (h *Handler) GetSigningSecret(w http.ResponseWriter, r *http.Request) {
	orgID := server.GetOrgID(r.Context())
	secret, err := h.service.GetSigningSecret(r.Context(), orgID)
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.RespondJSON(w, http.StatusOK, map[string]string{"signing_secret": secret})
}

func (h *Handler) RotateSigningSecret(w http.ResponseWriter, r *http.Request) {
	orgID := server.GetOrgID(r.Context())
	secret, err := h.service.RotateSigningSecret(r.Context(), orgID)
	if err != nil {
		api.RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	api.RespondJSON(w, http.StatusOK, map[string]string{"signing_secret": secret})
}

func (h *Handler) VerifySignature(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Secret    string `json:"secret"`
		Timestamp string `json:"timestamp"`
		Body      string `json:"body"`
		Signature string `json:"signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.RespondError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	result := Verify(req.Secret, req.Timestamp, req.Body, req.Signature)
	api.RespondJSON(w, http.StatusOK, result)
}

// Verify verifies a webhook delivery signature and timestamp freshness.
func Verify(secret, timestamp, body, signature string) VerifyResult {
	timestampSeconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return VerifyResult{Valid: false, Reason: "invalid_timestamp"}
	}

	now := time.Now().Unix()
	delta := now - timestampSeconds
	if delta < 0 {
		delta = -delta
	}
	if delta >= 300 {
		return VerifyResult{Valid: false, Reason: "stale_timestamp"}
	}

	signedPayload := timestamp + "." + body
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return VerifyResult{Valid: false, Reason: "signature_mismatch"}
	}

	return VerifyResult{Valid: true}
}

type VerifyResult struct {
	Valid  bool   `json:"valid"`
	Reason string `json:"reason,omitempty"`
}

func sign(secret, timestamp string, body []byte) string {
	signedPayload := timestamp + "." + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func VerifyRateLimit() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
