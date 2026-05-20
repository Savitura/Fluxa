package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/fx"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/fluxa/fluxa/internal/wallet"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Server struct {
	router *chi.Mux
	http   *http.Server
}

func New(
	walletHandler *wallet.Handler,
	transferHandler *transfer.Handler,
	fxHandler *fx.Handler,
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
		r.Route("/wallets", walletHandler.Routes())
		r.Route("/transfers", transferHandler.Routes())
		r.Route("/transactions", transferHandler.TransactionRoutes())
		r.Route("/fx", fxHandler.Routes())
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
