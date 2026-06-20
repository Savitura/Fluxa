package server

import (
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/apikey"
	"github.com/fluxa/fluxa/internal/postgres"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if id == "" {
			id = uuid.New().String()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(
			log.Logger.With().Str("request_id", id).Logger().WithContext(r.Context()),
		))
	})
}

func logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		next.ServeHTTP(ww, r)

		zerolog.Ctx(r.Context()).Info().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Int("status", ww.Status()).
			Dur("latency", time.Since(start)).
			Msg("request")
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rv := recover(); rv != nil {
				zerolog.Ctx(r.Context()).Error().Interface("panic", rv).Msg("panic recovered")
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func AuthMiddleware(repo *postgres.APIKeyRepo) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || len(authHeader) <= 7 || authHeader[:7] != "Bearer " {
				http.Error(w, "missing or invalid authorization header", http.StatusUnauthorized)
				return
			}

			rawKey := authHeader[7:]
			hash := apikey.Hash(rawKey)

			key, err := repo.GetByHash(r.Context(), hash)
			if err != nil || key == nil {
				http.Error(w, "invalid api key", http.StatusUnauthorized)
				return
			}
			if key.RevokedAt != nil {
				http.Error(w, "revoked api key", http.StatusUnauthorized)
				return
			}

			// Update last_used_at
			_ = repo.UpdateLastUsed(r.Context(), key.ID)

			// Attach tenant
			r = r.WithContext(tenant.WithID(r.Context(), key.TenantID))
			next.ServeHTTP(w, r)
		})
	}
}
