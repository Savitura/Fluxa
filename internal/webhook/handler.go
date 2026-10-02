package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	svc   Service
	audit interface {
		Record(context.Context, *domain.AuditEvent) error
	}
	idempotencyMW func(http.Handler) http.Handler
}

func NewHandler(svc Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) WithAuditLogger(audit interface {
	Record(context.Context, *domain.AuditEvent) error
}) *Handler {
	h.audit = audit
	return h
}

func (h *Handler) WithIdempotency(mw func(http.Handler) http.Handler) *Handler {
	h.idempotencyMW = mw
	return h
}

func (h *Handler) IdempotencyMiddleware() func(http.Handler) http.Handler {
	if h.idempotencyMW != nil {
		return h.idempotencyMW
	}
	return func(next http.Handler) http.Handler { return next }
}

func (h *Handler) RegisterRoutes(r chi.Router) {
	r.Get("/events", h.ListEventCatalog)
	r.Get("/", h.ListEndpoints)
	r.Post("/", h.RegisterEndpoint)
	r.Delete("/{id}", h.DeleteEndpoint)
	r.Get("/{id}/deliveries", h.ListDeliveries)
	r.Get("/dead-letters", h.ListDeadLetters)
	r.Get("/dead-letters/{id}", h.GetDeadLetter)
	r.With(h.IdempotencyMiddleware()).Post("/dead-letters/{id}/replay", h.ReplayDeadLetter)
	r.Get("/secret", h.GetSigningSecret)
	r.With(h.IdempotencyMiddleware()).Post("/secret/rotate", h.RotateSigningSecret)
	r.With(VerifyRateLimit()).Post("/verify", h.VerifySignature)

	r.Post("/subscriptions", h.CreateSubscription)
	r.Get("/subscriptions", h.ListSubscriptions)
	r.Delete("/subscriptions/{id}", h.DeleteSubscription)

	r.Get("/config", h.GetConfig)
	r.Put("/config", h.UpdateConfig)
	r.Get("/config/deliveries", h.ListConfigDeliveries)
	r.Post("/config/test", h.TestConfigDelivery)
}

// TriggerTestEvent is the sandbox-only escape hatch for #7: a developer holding
// an sk_test_ API key can drive a real webhook event through the normal
// dispatch path without moving real funds. The route is gated by
// RequireTestMode, and the environment is re-checked here so the handler stays
// safe if it is ever mounted directly.
func (h *Handler) TriggerTestEvent(w http.ResponseWriter, r *http.Request) {
	mode, ok := tenant.ModeFromContext(r.Context())
	if !ok || mode != domain.ModeTest {
		api.Error(w, http.StatusForbidden, "TEST_MODE_REQUIRED", "this endpoint requires an sk_test_ API key")
		return
	}

	var req struct {
		Event string                 `json:"event"`
		Data  map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}

	if !domain.IsSupportedEventType(req.Event) {
		api.BadRequest(w, "unsupported event type")
		return
	}

	if err := h.svc.Dispatch(r.Context(), domain.EventType(req.Event), req.Data); err != nil {
		api.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to dispatch test event")
		return
	}

	api.JSON(w, http.StatusAccepted, map[string]interface{}{
		"event":  req.Event,
		"mode":   mode,
		"status": "queued",
	})
}

func (h *Handler) ListEndpoints(w http.ResponseWriter, r *http.Request) {
	eps, err := h.svc.ListEndpoints(r.Context())
	if err != nil {
		api.InternalError(w, err)
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

	ep, secret, err := h.svc.RegisterEndpoint(r.Context(), req.URL, req.Events)
	if err != nil {
		if errors.Is(err, ErrUnsafeWebhookURL) || errors.Is(err, ErrUnsupportedEventType) {
			api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		api.InternalError(w, err)
		return
	}
	api.JSON(w, http.StatusCreated, map[string]interface{}{
		"id":                ep.ID,
		"tenant_id":         ep.TenantID,
		"url":               ep.URL,
		"secret":            secret,
		"events":            ep.Events,
		"active":            ep.Active,
		"success_count":     ep.SuccessCount,
		"failure_count":     ep.FailureCount,
		"last_delivered_at": ep.LastDeliveredAt,
		"notified_failing":  ep.NotifiedFailing,
		"created_at":        ep.CreatedAt,
		"updated_at":        ep.UpdatedAt,
	})
}

func (h *Handler) DeleteEndpoint(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.DeleteEndpoint(r.Context(), id); err != nil {
		api.InternalError(w, err)
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
		api.InternalError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"deliveries": deliveries})
}

// deadLetterFilterFromQuery translates the list endpoint's query parameters
// into a domain filter. An unparseable date is a client error, not a silently
// ignored filter.
func deadLetterFilterFromQuery(r *http.Request) (domain.DeadLetterFilter, error) {
	query := r.URL.Query()
	filter := domain.DeadLetterFilter{
		EndpointID: query.Get("endpoint_id"),
		EventType:  query.Get("event"),
		Status:     domain.DeadLetterStatus(query.Get("status")),
	}
	if raw := query.Get("event_type"); raw != "" {
		filter.EventType = raw
	}
	if raw := query.Get("since"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return filter, fmt.Errorf("since must be RFC3339")
		}
		filter.Since = &parsed
	}
	if raw := query.Get("until"); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return filter, fmt.Errorf("until must be RFC3339")
		}
		filter.Until = &parsed
	}
	switch filter.Status {
	case "", domain.DeadLetterPending, domain.DeadLetterReplayed, domain.DeadLetterDiscarded:
	default:
		return filter, fmt.Errorf("status must be one of pending, replayed, discarded")
	}
	return filter, nil
}

func (h *Handler) ListDeadLetters(w http.ResponseWriter, r *http.Request) {
	if tenant.IDFromContext(r.Context()) == "" {
		api.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "tenant required")
		return
	}
	filter, err := deadLetterFilterFromQuery(r)
	if err != nil {
		api.BadRequest(w, err.Error())
		return
	}
	limit, offset := 50, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			limit = parsed
		}
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	deadLetters, err := h.svc.ListDeadLetters(r.Context(), filter, limit, offset)
	if err != nil {
		api.InternalError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"dead_letters": deadLetters})
}

func (h *Handler) GetDeadLetter(w http.ResponseWriter, r *http.Request) {
	if tenant.IDFromContext(r.Context()) == "" {
		api.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "tenant required")
		return
	}
	dl, err := h.svc.GetDeadLetter(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeDeadLetterError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, dl)
}

func (h *Handler) ReplayDeadLetter(w http.ResponseWriter, r *http.Request) {
	if tenant.IDFromContext(r.Context()) == "" {
		api.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "tenant required")
		return
	}
	deadLetterID := chi.URLParam(r, "id")
	dl, err := h.svc.ReplayDeadLetter(r.Context(), deadLetterID)
	if err != nil {
		writeDeadLetterError(w, err)
		return
	}
	if h.audit != nil {
		if auditErr := h.audit.Record(r.Context(), &domain.AuditEvent{
			Action:       "webhook.dead_letter.replayed",
			ResourceType: "webhook_dead_letter",
			ResourceID:   dl.ID,
			Metadata: map[string]interface{}{
				"delivery_id":  dl.ReplayDeliveryID,
				"endpoint_id":  dl.EndpointID,
				"event_type":   dl.EventType,
				"replay_count": dl.ReplayCount,
			},
		}); auditErr != nil {
			tracing.Logger(r.Context()).Error().Msg("webhook dead letter replayed but audit record failed")
		}
	}
	api.JSON(w, http.StatusAccepted, dl)
}

func writeDeadLetterError(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrDeadLetterNotFound) {
		api.Error(w, http.StatusNotFound, "DEAD_LETTER_NOT_FOUND", "webhook dead letter not found")
		return
	}
	if errors.Is(err, domain.ErrDeadLetterEndpointUnusable) {
		api.Error(w, http.StatusConflict, "DEAD_LETTER_ENDPOINT_UNAVAILABLE", "the original webhook endpoint is not available for replay")
		return
	}
	if errors.Is(err, domain.ErrForbidden) {
		api.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "tenant required")
		return
	}
	api.InternalError(w, err)
}

func (h *Handler) GetSigningSecret(w http.ResponseWriter, r *http.Request) {
	if tenant.IDFromContext(r.Context()) == "" {
		api.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "tenant required")
		return
	}
	cs, ok := h.svc.(ConfigService)
	if !ok {
		api.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook secret service unavailable")
		return
	}
	secrets, err := cs.ListSigningSecrets(r.Context())
	if err != nil {
		writeSecretServiceError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"secrets": secrets})
}

func (h *Handler) RotateSigningSecret(w http.ResponseWriter, r *http.Request) {
	if tenant.IDFromContext(r.Context()) == "" {
		api.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "tenant required")
		return
	}
	cs, ok := h.svc.(ConfigService)
	if !ok {
		api.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook secret service unavailable")
		return
	}
	var req struct {
		OverlapWindowSeconds *int `json:"overlap_window_seconds,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}
	seconds := 300
	if req.OverlapWindowSeconds != nil {
		seconds = *req.OverlapWindowSeconds
	}
	if seconds < 0 || seconds > int((24*time.Hour)/time.Second) {
		api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "overlap_window_seconds must be between 0 and 86400")
		return
	}
	metadata, secret, err := cs.RotateSigningSecret(r.Context(), time.Duration(seconds)*time.Second)
	if err != nil {
		writeSecretServiceError(w, err)
		return
	}
	if h.audit != nil {
		if auditErr := h.audit.Record(r.Context(), &domain.AuditEvent{
			Action:       "webhook.signing_secret.rotated",
			ResourceType: "webhook_signing_secret",
			ResourceID:   metadata.KeyID,
			Metadata: map[string]interface{}{
				"key_id":                 metadata.KeyID,
				"overlap_window_seconds": seconds,
			},
		}); auditErr != nil {
			tracing.Logger(r.Context()).Error().Msg("webhook signing secret rotated but audit record failed")
		}
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{
		"key_id":                 metadata.KeyID,
		"secret":                 secret,
		"created_at":             metadata.CreatedAt,
		"activated_at":           metadata.ActivatedAt,
		"retired_at":             metadata.RetiredAt,
		"status":                 metadata.Status,
		"overlap_window_seconds": seconds,
	})
}

func writeSecretServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, domain.ErrWebhookConfigNotFound) {
		api.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook signing secret unavailable")
		return
	}
	if strings.Contains(err.Error(), "overlap window") {
		api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid overlap window")
		return
	}
	api.Error(w, http.StatusInternalServerError, "INTERNAL_ERROR", "webhook signing secret operation failed")
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
		api.InternalError(w, err)
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
		if errors.Is(err, ErrUnsafeWebhookURL) || errors.Is(err, ErrUnsupportedEventType) {
			api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		api.InternalError(w, err)
		return
	}
	api.JSON(w, http.StatusCreated, sub)
}

func (h *Handler) DeleteSubscription(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.DeleteSubscription(r.Context(), id); err != nil {
		api.InternalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	cs, ok := h.svc.(ConfigService)
	if !ok {
		api.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook config service unavailable")
		return
	}
	tenantID := tenant.IDFromContext(r.Context())
	if tenantID == "" {
		api.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "tenant required")
		return
	}
	cfg, err := cs.GetConfig(r.Context())
	if err != nil {
		if errors.Is(err, domain.ErrWebhookConfigNotFound) {
			api.Error(w, http.StatusNotFound, "CONFIG_NOT_FOUND", "webhook config not found")
			return
		}
		api.InternalError(w, err)
		return
	}

	resp := map[string]interface{}{
		"tenant_id":         cfg.TenantID,
		"enabled":           cfg.Enabled,
		"url":               cfg.URL,
		"events":            cfg.Events,
		"paused":            cfg.Paused,
		"resume_at":         cfg.ResumeAt,
		"last_delivered_at": cfg.LastDeliveredAt,
		"created_at":        cfg.CreatedAt,
		"updated_at":        cfg.UpdatedAt,
		"secret_configured": cfg.SecretConfigured,
	}
	api.JSON(w, http.StatusOK, resp)
}

func (h *Handler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	cs, ok := h.svc.(ConfigService)
	if !ok {
		api.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook config service unavailable")
		return
	}
	tenantID := tenant.IDFromContext(r.Context())
	if tenantID == "" {
		api.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "tenant required")
		return
	}

	var req domain.WebhookConfigUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", "invalid request body")
		return
	}

	res, err := cs.UpdateConfig(r.Context(), req)
	if err != nil {
		if errors.Is(err, domain.ErrWebhookConfigNotFound) {
			api.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook config unavailable")
			return
		}
		if errors.Is(err, ErrUnsafeWebhookURL) || errors.Is(err, ErrUnsupportedEventType) {
			api.Error(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error())
			return
		}
		api.InternalError(w, err)
		return
	}

	resp := map[string]interface{}{
		"tenant_id":         res.Config.TenantID,
		"enabled":           res.Config.Enabled,
		"url":               res.Config.URL,
		"events":            res.Config.Events,
		"paused":            res.Config.Paused,
		"resume_at":         res.Config.ResumeAt,
		"last_delivered_at": res.Config.LastDeliveredAt,
		"created_at":        res.Config.CreatedAt,
		"updated_at":        res.Config.UpdatedAt,
		"secret_configured": res.Config.SecretConfigured,
	}
	if res.Secret != "" {
		resp["secret"] = res.Secret
	}
	api.JSON(w, http.StatusOK, resp)
}

func (h *Handler) ListConfigDeliveries(w http.ResponseWriter, r *http.Request) {
	cs, ok := h.svc.(ConfigService)
	if !ok {
		api.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook config service unavailable")
		return
	}
	limitStr := r.URL.Query().Get("limit")
	limit := 50
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 {
			limit = l
		}
	}
	offsetStr := r.URL.Query().Get("offset")
	offset := 0
	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}
	deliveries, err := cs.ListConfigDeliveries(r.Context(), limit, offset)
	if err != nil {
		if errors.Is(err, domain.ErrWebhookConfigNotFound) {
			api.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook config unavailable")
			return
		}
		api.InternalError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"deliveries": deliveries})
}

func (h *Handler) TestConfigDelivery(w http.ResponseWriter, r *http.Request) {
	cs, ok := h.svc.(ConfigService)
	if !ok {
		api.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook config service unavailable")
		return
	}
	delivery, err := cs.TestDelivery(r.Context())
	if err != nil {
		if errors.Is(err, domain.ErrWebhookConfigNotFound) {
			api.Error(w, http.StatusNotFound, "NOT_FOUND", "webhook config unavailable")
			return
		}
		api.InternalError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, delivery)
}

func sign(secret, timestamp string, body []byte) string {
	signedPayload := timestamp + "." + string(body)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(signedPayload))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// signBody signs a payload with the tenant webhook secret. The timestamped
// variant above is the developer-facing endpoint signature contract; tenant
// config deliveries authenticate the body directly.
func signBody(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}
