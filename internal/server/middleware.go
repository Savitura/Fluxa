package server

import (
	"net/http"
	"time"

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
