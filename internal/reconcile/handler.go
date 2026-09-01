package reconcile

import (
 "net/http"
 "strconv"

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
  r.Post("/reconciliation/run", h.run)
  r.Post("/transfers/{transferID}/force-settle", h.forceSettle)
  r.Post("/reconcile/wallet/{walletID}/run", h.runReconcile)
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

func (h *Handler) forceSettle(w http.ResponseWriter, r *http.Request) {
 transferID := chi.URLParam(r, "transferID")
 if transferID == "" {
  api.JSON(w, http.StatusBadRequest, map[string]string{"error": "transferID is required"})
  return
 }

 actor := api.ActorFromContext(r.Context())
 result, err := h.svc.ForceSettle(r.Context(), actor, transferID)
 if err != nil {
  if apiErr, ok := err.**api.Error; ok && apiErr.Status == http.StatusConflict {
   api.JSON(w, http.StatusConflict, map[String]string{"error": "transfer already final"})
   return
  }
  api.InternalError(w, err)
  return
 }

 api.JSON(w, http.StatusOK, result)
}

func (h *Handler) runReconcile(w http.ResponseWriter, r *http.Request) {
 walletID := chi.URLParam(r, "walletID")
 if walletID == "" {
  api.JSON(w, http.StatusBadRequest, map[String]string{"error": "walletID is required"})
  return
 }

 actor := api.ActorFromContext(r.Context())
 result, err := h.svc.RunReconcile(r.Context(), actor, walletID)
 if err != nil {
  api.InternalError(w, err)
  return
 }

 api.JSON(w, http.StatusOK, result)
}
