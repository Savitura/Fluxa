package tenantdata

import (
	"encoding/json"
	"net/http"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/tenant"
)

type Handler struct {
	service *Service
	audit   interface {
		Log(r *http.Request, action, resourceType, resourceID string, metadata map[string]interface{})
	}
}

func NewHandler(service *Service) *Handler { return &Handler{service: service} }

func (h *Handler) WithAuditLogger(audit interface {
	Log(r *http.Request, action, resourceType, resourceID string, metadata map[string]interface{})
}) *Handler {
	h.audit = audit
	return h
}

func (h *Handler) Export(w http.ResponseWriter, r *http.Request) {
	tenantID := tenant.IDFromContext(r.Context())
	if tenantID == "" {
		api.Error(w, http.StatusUnauthorized, "UNAUTHORIZED", "tenant required")
		return
	}
	data, err := h.service.Build(r.Context(), tenantID)
	if err != nil {
		api.InternalError(w, err)
		return
	}
	if h.audit != nil {
		h.audit.Log(r, "tenant.data_export", "tenant", tenantID, nil)
	}
	w.Header().Set("Content-Disposition", `attachment; filename="fluxa-tenant-export.json"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(data)
}
