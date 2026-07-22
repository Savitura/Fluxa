package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/apikey"
	"github.com/fluxa/fluxa/internal/batch"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/fluxa/fluxa/internal/fx"
	"github.com/fluxa/fluxa/internal/auth"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/org"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/fluxa/fluxa/internal/schedule"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/fluxa/fluxa/internal/webhook"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	router *chi.Mux
	http   *http.Server
}

func New(
	authHandler *auth.Handler,
	orgHandler *org.Handler,
	walletHandler *wallet.Handler,
	transferHandler *transfer.Handler,
	fxHandler *fx.Handler,
	fiatHandler *fiat.Handler,
	feeHandler *fees.Handler,
	reconcileHandler *reconcile.Handler,
	apikeyHandler *apikey.Handler,
	apiKeyRepo *postgres.APIKeyRepo,
	webhookHandler *webhook.Handler,
	batchHandler *batch.Handler,
	scheduleHandler *schedule.Handler,
	jwtSecret []byte,
	port string,
) *Server {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(requestID)
	r.Use(logger)
	r.Use(recoverer)

	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	r.Route("/v1", func(r chi.Router) {
		// Unauthenticated public endpoints
		r.Route("/auth", authHandler.Routes())
		r.Post("/org/invites/accept", orgHandler.AcceptInvite)

		// Authenticated endpoints
		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware(apiKeyRepo, jwtSecret))

			// API Keys (Owner & Admin only for creation & revocation)
			r.Route("/keys", func(r chi.Router) {
				r.With(RequireRole(domain.RoleOwner, domain.RoleAdmin)).Post("/", apikeyHandler.Create)
				r.Get("/", apikeyHandler.List)
				r.With(RequireRole(domain.RoleOwner, domain.RoleAdmin)).Delete("/{id}", apikeyHandler.Revoke)
			})

			// Org Member Management (Owner & Admin for invite, role update, remove)
			r.Route("/org", func(r chi.Router) {
				r.With(RequireRole(domain.RoleOwner, domain.RoleAdmin)).Post("/members/invite", orgHandler.InviteMember)
				r.Get("/members", orgHandler.ListMembers)
				r.With(RequireRole(domain.RoleOwner, domain.RoleAdmin)).Patch("/members/{userId}", orgHandler.UpdateRole)
				r.With(RequireRole(domain.RoleOwner, domain.RoleAdmin)).Delete("/members/{userId}", orgHandler.RemoveMember)
			})

			// Webhooks (Owner & Admin for management, viewer/dev read)
			r.Route("/webhooks", func(r chi.Router) {
				r.With(RequireRole(domain.RoleOwner, domain.RoleAdmin)).Post("/", webhookHandler.Register)
				r.Get("/", webhookHandler.List)
				r.With(RequireRole(domain.RoleOwner, domain.RoleAdmin)).Delete("/{id}", webhookHandler.Delete)
				r.Get("/{id}/deliveries", webhookHandler.ListDeliveries)
			})

			// Operational routes (Require not viewer for mutating calls)
			r.Group(func(r chi.Router) {
				r.Use(RequireNotViewer)
				r.Route("/wallets", walletHandler.Routes())
				r.Route("/wallets/{id}/deposit", fiatHandler.DepositRoutes())
				r.Route("/wallets/{id}/withdraw", fiatHandler.WithdrawRoutes())
				r.Route("/webhooks/fiat", fiatHandler.WebhookRoutes())
				r.Route("/transfers", transferHandler.Routes())
				r.Route("/transfers/batch", batchHandler.Routes())
				r.Route("/transactions", transferHandler.TransactionRoutes())
				r.Route("/schedules", scheduleHandler.Routes())
				r.Route("/fx", fxHandler.Routes())
				r.Route("/fees", feeHandler.Routes())
				r.Route("/admin/fees", feeHandler.AdminRoutes())
				r.Route("/admin", reconcileHandler.AdminRoutes())
			})
		})
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%s", port),
		Handler:      r,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	return &Server{router: r, http: srv}
}

func (s *Server) Start() error {
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
