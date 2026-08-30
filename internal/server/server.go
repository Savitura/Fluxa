package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/anchor"
	"github.com/fluxa/fluxa/internal/apikey"
	"github.com/fluxa/fluxa/internal/auth"
	"github.com/fluxa/fluxa/internal/batch"
	"github.com/fluxa/fluxa/internal/compliance"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/fees"
	"github.com/fluxa/fluxa/internal/fiat"
	"github.com/fluxa/fluxa/internal/fx"
	fluxahealth "github.com/fluxa/fluxa/internal/health"
	"github.com/fluxa/fluxa/internal/org"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/reconcile"
	"github.com/fluxa/fluxa/internal/schedule"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/treasury"
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
	anchorFiatHandler *fiat.AnchorHandler,
	anchorHandler *anchor.Handler,
	feeHandler *fees.Handler,
	reconcileHandler *reconcile.Handler,
	apikeyHandler *apikey.Handler,
	apiKeyRepo *postgres.APIKeyRepo,
	webhookHandler *webhook.Handler,
	batchHandler *batch.Handler,
	scheduleHandler *schedule.Handler,
	treasuryHandler *treasury.Handler,
	complianceHandler *compliance.Handler,
	jwtSecret []byte,
	port string,
	healthChecks map[string]DependencyCheck,
	membershipValidator MembershipValidator,
) *Server {
	r := chi.NewRouter()

	r.Use(middleware.RealIP)
	r.Use(requestID)
	r.Use(logger)
	r.Use(recoverer)
	r.Use(CORS)
	r.Use(MaxBodySize(1 << 20))
	r.Use(MetricsMiddleware)

	componentProbes := make(map[string]fluxahealth.Probe, len(healthChecks))
	for name, check := range healthChecks {
		probe := check
		componentProbes[name] = func(ctx context.Context) (interface{}, error) { return nil, probe(ctx) }
	}
	healthService := fluxahealth.New(componentProbes)
	r.Get("/health", healthService.Handler())
	r.Get("/health/ready", healthService.ReadyHandler())
	r.Get("/health/live", fluxahealth.LiveHandler())
	r.Get("/metrics", MetricsHandler)

	r.Route("/v1", func(r chi.Router) {
		// Unauthenticated public endpoints
		r.Route("/auth", authHandler.Routes())
		r.Post("/org/invites/accept", orgHandler.AcceptInvite)
		// Registered as a direct path (not r.Route("/webhooks", ...)) because
		// the authenticated group below already mounts a "/webhooks"
		// sub-router for Register/List/Delete/deliveries; chi doesn't support
		// mounting two independent sub-routers at the same pattern.
		r.With(webhook.VerifyRateLimit()).Post("/webhooks/verify", webhookHandler.Verify)

		// Authenticated endpoints
		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware(apiKeyRepo, jwtSecret, membershipValidator))
			r.Use(RateLimit(100, 200))

			r.Get("/usage", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"request_count":   0,
					"transfer_volume": "0",
					"rate_limit":      100,
					"period":          "current",
					"note":            "derived on client — backend usage aggregation not yet implemented",
				})
			})

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
				r.Route("/fiat", anchorFiatHandler.Routes())
				r.Route("/transfers", transferHandler.Routes())
				r.Route("/transfers/batch", batchHandler.Routes())
				r.Route("/transactions", transferHandler.TransactionRoutes())
				r.Route("/schedules", scheduleHandler.Routes())
				r.Route("/fx", fxHandler.Routes())
				r.Route("/fees", feeHandler.Routes())
			})

			// Administrative routes (Owner & Admin only)
			r.Group(func(r chi.Router) {
				r.Use(RequireRole(domain.RoleOwner, domain.RoleAdmin))
				r.Route("/admin/fees", feeHandler.AdminRoutes())
				r.Route("/admin/anchors", anchorHandler.AdminRoutes())
				r.Route("/admin", reconcileHandler.AdminRoutes())
				r.Route("/admin/treasury", treasuryHandler.AdminRoutes())
				// Mounted at /admin/compliance, not /admin: reconcileHandler
				// already owns the bare /admin pattern above, and chi panics
				// when two sub-routers share one.
				if complianceHandler != nil {
					r.Route("/admin/compliance", complianceHandler.AdminRoutes())
				}
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
