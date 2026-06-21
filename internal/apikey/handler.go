package apikey

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type Handler struct {
	repo *postgres.APIKeyRepo
}

func NewHandler(repo *postgres.APIKeyRepo) *Handler {
	return &Handler{repo: repo}
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Post("/", h.Create)
	r.Get("/", h.List)
	r.Delete("/{id}", h.Revoke)
	return r
}

func (h *Handler) Create(w http.ResponseWriter, r *http.Request) {
	tenantID := tenant.IDFromContext(r.Context())
	if tenantID == "" {
		http.Error(w, "tenant not found in context", http.StatusUnauthorized)
		return
	}

	var req struct {
		Label *string `json:"label"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	raw, prefix, err := Generate()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	key := &domain.APIKey{
		ID:        uuid.New().String(),
		TenantID:  tenantID,
		KeyHash:   Hash(raw),
		Prefix:    prefix,
		Label:     req.Label,
		CreatedAt: time.Now(),
	}

	if err := h.repo.Create(r.Context(), key); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"id":         key.ID,
		"key":        raw, // raw key exactly once
		"prefix":     key.Prefix,
		"label":      key.Label,
		"created_at": key.CreatedAt,
	})
}

func (h *Handler) List(w http.ResponseWriter, r *http.Request) {
	tenantID := tenant.IDFromContext(r.Context())
	if tenantID == "" {
		http.Error(w, "tenant not found in context", http.StatusUnauthorized)
		return
	}

	keys, err := h.repo.ListByTenant(r.Context(), tenantID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	res := make([]map[string]interface{}, 0, len(keys))
	for _, k := range keys {
		res = append(res, map[string]interface{}{
			"id":           k.ID,
			"prefix":       k.Prefix,
			"label":        k.Label,
			"last_used_at": k.LastUsedAt,
			"revoked_at":   k.RevokedAt,
			"created_at":   k.CreatedAt,
		})
	}
	json.NewEncoder(w).Encode(res)
}

func (h *Handler) Revoke(w http.ResponseWriter, r *http.Request) {
	tenantID := tenant.IDFromContext(r.Context())
	id := chi.URLParam(r, "id")

	if err := h.repo.Revoke(r.Context(), id, tenantID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
