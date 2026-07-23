package server

import (
	"net/http"
	"time"

	"strings"

	"github.com/fluxa/fluxa/internal/apikey"
	"github.com/fluxa/fluxa/internal/auth"
	"github.com/fluxa/fluxa/internal/domain"
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

func AuthMiddleware(repo *postgres.APIKeyRepo, jwtSecret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || len(authHeader) <= 7 || authHeader[:7] != "Bearer " {
				http.Error(w, "missing or invalid authorization header", http.StatusUnauthorized)
				return
			}

			rawToken := authHeader[7:]

			// Check if token is JWT (contains 2 dots)
			if strings.Count(rawToken, ".") == 2 {
				claims, err := auth.ParseToken(rawToken, jwtSecret)
				if err == nil && claims.TokenType == "access" {
					ctx := tenant.WithID(r.Context(), claims.TenantID)
					ctx = tenant.WithUser(ctx, claims.Sub, claims.Role)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
			}

			// Fallback to API Key auth
			hash := apikey.Hash(rawToken)
			key, err := repo.GetByHash(r.Context(), hash)
			if err != nil || key == nil {
				http.Error(w, "invalid api key or authentication token", http.StatusUnauthorized)
				return
			}
			if key.RevokedAt != nil {
				http.Error(w, "revoked api key", http.StatusUnauthorized)
				return
			}

			_ = repo.UpdateLastUsed(r.Context(), key.ID)

			ctx := tenant.WithID(r.Context(), key.TenantID)
			ctx = tenant.WithUser(ctx, "", domain.RoleAdmin)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func RequireRole(allowedRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			role := tenant.RoleFromContext(r.Context())
			if role == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}

			for _, allowed := range allowedRoles {
				if role == allowed {
					next.ServeHTTP(w, r)
					return
				}
			}

			http.Error(w, "insufficient permissions", http.StatusForbidden)
		})
	}
}

func RequireNotViewer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role := tenant.RoleFromContext(r.Context())
		if role == domain.RoleViewer {
			http.Error(w, "viewer role is read-only", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

