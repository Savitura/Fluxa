package compliance

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

// AdminRoutes is mounted at /v1/admin/compliance.
//
// It is deliberately not mounted at bare /admin: reconcile.Handler already
// owns that pattern, and chi panics when two sub-routers share one.
func (h *Handler) AdminRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Get("/reviews", h.listReviews)
		r.Get("/reviews/{id}", h.getReview)
		r.Post("/reviews/{id}/approve", h.approveReview)
		r.Post("/reviews/{id}/reject", h.rejectReview)
		r.Get("/sanctions-status", h.sanctionsStatus)
	}
}

type reviewResponse struct {
	ID            string   `json:"id"`
	TransactionID string   `json:"transaction_id"`
	Status        string   `json:"status"`
	RiskScore     int      `json:"risk_score"`
	RulesFired    []string `json:"rules_fired"`
	Reason        string   `json:"reason,omitempty"`
	ReviewedBy    string   `json:"reviewed_by,omitempty"`
	ReviewNotes   string   `json:"review_notes,omitempty"`
	ReviewedAt    string   `json:"reviewed_at,omitempty"`
	CreatedAt     string   `json:"created_at"`
}

func toReviewResponse(r *domain.ComplianceReview) reviewResponse {
	resp := reviewResponse{
		ID:            r.ID,
		TransactionID: r.TransactionID,
		Status:        string(r.Status),
		RiskScore:     r.RiskScore,
		RulesFired:    r.RulesFired,
		Reason:        r.Reason,
		ReviewNotes:   r.ReviewNotes,
		CreatedAt:     r.CreatedAt.Format(time.RFC3339),
	}
	if resp.RulesFired == nil {
		resp.RulesFired = []string{}
	}
	if r.ReviewedBy != nil {
		resp.ReviewedBy = *r.ReviewedBy
	}
	if r.ReviewedAt != nil {
		resp.ReviewedAt = r.ReviewedAt.Format(time.RFC3339)
	}
	return resp
}

func (h *Handler) listReviews(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))

	reviews, err := h.svc.ListReviews(r.Context(), r.URL.Query().Get("status"), limit, offset)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	resp := make([]reviewResponse, len(reviews))
	for i, rv := range reviews {
		resp[i] = toReviewResponse(rv)
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{"reviews": resp})
}

func (h *Handler) getReview(w http.ResponseWriter, r *http.Request) {
	review, err := h.svc.GetReview(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, toReviewResponse(review))
}

type decisionRequest struct {
	Notes string `json:"notes"`
}

// decodeNotes tolerates an empty body: approving with no note is the common
// case and should not require sending "{}".
func decodeNotes(r *http.Request) (string, bool) {
	var req decisionRequest
	if r.Body == nil {
		return "", true
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err.Error() != "EOF" {
		return "", false
	}
	return req.Notes, true
}

func (h *Handler) approveReview(w http.ResponseWriter, r *http.Request) {
	notes, ok := decodeNotes(r)
	if !ok {
		api.BadRequest(w, "invalid request body")
		return
	}
	review, err := h.svc.ApproveReview(r.Context(), chi.URLParam(r, "id"), notes)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, toReviewResponse(review))
}

func (h *Handler) rejectReview(w http.ResponseWriter, r *http.Request) {
	notes, ok := decodeNotes(r)
	if !ok {
		api.BadRequest(w, "invalid request body")
		return
	}
	review, err := h.svc.RejectReview(r.Context(), chi.URLParam(r, "id"), notes)
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}
	api.JSON(w, http.StatusOK, toReviewResponse(review))
}

func (h *Handler) sanctionsStatus(w http.ResponseWriter, r *http.Request) {
	status, err := h.svc.SanctionsStatus(r.Context())
	if err != nil {
		api.HandleDomainError(w, err)
		return
	}

	resp := map[string]interface{}{
		"loaded":        status.Loaded,
		"entity_count":  status.EntityCount,
		"address_count": status.AddressCount,
		"name_count":    status.NameCount,
	}
	if !status.UpdatedAt.IsZero() {
		resp["updated_at"] = status.UpdatedAt.Format(time.RFC3339)
	}
	if status.LastUpdate != nil {
		last := map[string]interface{}{
			"status":       string(status.LastUpdate.Status),
			"entity_count": status.LastUpdate.EntityCount,
			"duration_ms":  status.LastUpdate.DurationMS,
			"finished_at":  status.LastUpdate.FinishedAt.Format(time.RFC3339),
		}
		if status.LastUpdate.Error != "" {
			last["error"] = status.LastUpdate.Error
		}
		resp["last_refresh"] = last
	}
	api.JSON(w, http.StatusOK, resp)
}
