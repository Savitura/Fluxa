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
	svc       Service
	configSvc ConfigService
}

func NewHandler(svc Service) *Handler {
	handler := &Handler{svc: svc}
	if configSvc, ok := svc.(ConfigService); ok {
		handler.configSvc = configSvc
	}
	return handler
}

func (h *Handler) Routes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Post("/", h.Register)
		r.Get("/", h.List)
		r.Get("/config", h.GetConfig)
		r.Put("/config", h.UpdateConfig)
		r.Get("/config/deliveries", h.ListConfigDeliveries)
		r.Post("/config/test", h.TestConfigDelivery)
		r.Delete("/{id}", h.Delete)
		r.Get("/{id}/deliveries", h.ListDeliveries)
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

type configUpdateRequest struct {
	Enabled      *bool      `json:"enabled"`
	URL          *string    `json:"url"`
	Events       *[]string  `json:"events"`
	Paused       *bool      `json:"paused"`
	ResumeAt     *time.Time `json:"resume_at"`
	RotateSecret bool       `json:"rotate_secret"`
}

type configResponse struct {
	TenantID         string   `json:"tenant_id"`
	Enabled          bool     `json:"enabled"`
	URL              string   `json:"url"`
	SigningAlgorithm string   `json:"signing_algorithm"`
	Events           []string `json:"events"`
	Paused           bool     `json:"paused"`
	ResumeAt         *string  `json:"resume_at,omitempty"`
	LastDeliveredAt  *string  `json:"last_delivered_at,omitempty"`
	CreatedAt        string   `json:"created_at"`
	UpdatedAt        string   `json:"updated_at"`
	// Secret is only populated on the response that creates or rotates the
	// config. It is never returned by a read, so a secret can be retrieved
	// exactly once.
	Secret string `json:"secret,omitempty"`
	// SecretConfigured lets a client confirm a secret exists (for example after
	// rotating it) without the value ever being sent again.
	SecretConfigured bool `json:"secret_configured"`
}

type configDeliveryResponse struct {
	ID           string  `json:"id"`
	TenantID     string  `json:"tenant_id"`
	EventType    string  `json:"event_type"`
	Status       string  `json:"status"`
	ResponseCode *int    `json:"response_code,omitempty"`
	AttemptCount int     `json:"attempt_count"`
	LastAttempt  *string `json:"last_attempt,omitempty"`
	CreatedAt    string  `json:"created_at"`
	UpdatedAt    string  `json:"updated_at"`
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

func toConfigResponse(config *domain.TenantWebhookConfig, secret string) configResponse {
	response := configResponse{
		TenantID:         config.TenantID,
		Enabled:          config.Enabled,
		URL:              config.URL,
		Secret:           secret,
		SecretConfigured: config.SecretConfigured || secret != "",
		SigningAlgorithm: config.SigningAlgorithm,
		Events:           config.Events,
		Paused:           config.Paused,
		CreatedAt:        config.CreatedAt.Format(time.RFC3339),
		UpdatedAt:        config.UpdatedAt.Format(time.RFC3339),
	}
	if config.ResumeAt != nil {
		value := config.ResumeAt.Format(time.RFC3339)
		response.ResumeAt = &value
	}
	if config.LastDeliveredAt != nil {
		value := config.LastDeliveredAt.Format(time.RFC3339)
		response.LastDeliveredAt = &value
	}
	return response
}

func toConfigDeliveryResponse(delivery *domain.TenantWebhookDelivery) configDeliveryResponse {
	response := configDeliveryResponse{
		ID:           delivery.ID,
		TenantID:     delivery.TenantID,
		EventType:    string(delivery.EventType),
		Status:       string(delivery.Status),
		ResponseCode: delivery.ResponseCode,
		AttemptCount: delivery.AttemptCount,
		CreatedAt:    delivery.CreatedAt.Format(time.RFC3339),
		UpdatedAt:    delivery.UpdatedAt.Format(time.RFC3339),
	}
	if delivery.LastAttempt != nil {
		value := delivery.LastAttempt.Format(time.RFC3339)
		response.LastAttempt = &value
	}
	return response
}

func (h *Handler) GetConfig(w http.ResponseWriter, r *http.Request) {
	if h.configSvc == nil {
		api.NotFound(w, "webhook config is not available")
		return
	}
	config, err := h.configSvc.GetConfig(r.Context())
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, toConfigResponse(config, ""))
}

func (h *Handler) UpdateConfig(w http.ResponseWriter, r *http.Request) {
	if h.configSvc == nil {
		api.NotFound(w, "webhook config is not available")
		return
	}
	var request configUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		api.BadRequest(w, "invalid request body")
		return
	}
	result, err := h.configSvc.UpdateConfig(r.Context(), domain.WebhookConfigUpdate{
		Enabled:      request.Enabled,
		URL:          request.URL,
		Events:       request.Events,
		Paused:       request.Paused,
		ResumeAt:     request.ResumeAt,
		RotateSecret: request.RotateSecret,
	})
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, toConfigResponse(result.Config, result.Secret))
}

func (h *Handler) ListConfigDeliveries(w http.ResponseWriter, r *http.Request) {
	if h.configSvc == nil {
		api.NotFound(w, "webhook config is not available")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	deliveries, err := h.configSvc.ListConfigDeliveries(r.Context(), limit, offset)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}
	response := make([]configDeliveryResponse, len(deliveries))
	for i, delivery := range deliveries {
		response[i] = toConfigDeliveryResponse(delivery)
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"deliveries": response})
}

func (h *Handler) TestConfigDelivery(w http.ResponseWriter, r *http.Request) {
	if h.configSvc == nil {
		api.NotFound(w, "webhook config is not available")
		return
	}
	delivery, err := h.configSvc.TestDelivery(r.Context())
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, toConfigDeliveryResponse(delivery))
}

func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
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

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.svc.Delete(r.Context(), id); err != nil {
		api.HandleDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) ListDeliveries(w http.ResponseWriter, r *http.Request) {
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
