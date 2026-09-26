package reconcile

import (
	"net/http"
	"strconv"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	svc *Service
}

func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) AdminRoutes() func(r chi.Router) {
	return func(r chi.Router) {
		r.Get("/reconciliation/summary", h.summary)
		r.Get("/reconciliation/drift", h.drift)
		r.Post("/reconciliation/run", h.run)
	}
}

func (h *Handler) summary(w http.ResponseWriter, r *http.Request) {
	daysStr := r.URL.Query().Get("days")
	days := 7
	if daysStr != "" {
		if d, err := strconv.Atoi(daysStr); err == nil && d > 0 && d <= 90 {
			days = d
		}
	}

	summary, err := h.svc.GetSummary(r.Context(), days)
	if err != nil {
		api.InternalError(w, err)
		return
	}

	api.JSON(w, http.StatusOK, summary)
}

func (h *Handler) drift(w http.ResponseWriter, r *http.Request) {
	snapshots, err := h.svc.GetDrift(r.Context())
	if err != nil {
		api.InternalError(w, err)
		return
	}
	// Always emit a JSON array, never null, so clients can iterate the result
	// without a nil check when there is no drift.
	if snapshots == nil {
		snapshots = []*DriftSnapshot{}
	}
	api.JSON(w, http.StatusOK, map[string]interface{}{
		"drift":   snapshots,
		"count":   len(snapshots),
		"checked": time.Now().UTC(),
	})
}

func (h *Handler) run(w http.ResponseWriter, r *http.Request) {
	summary, err := h.svc.GetSummary(r.Context(), 7)
	if err != nil {
		api.InternalError(w, err)
		return
	}
	api.JSON(w, http.StatusAccepted, map[string]interface{}{
		"status":  "triggered",
		"summary": summary,
	})
}
