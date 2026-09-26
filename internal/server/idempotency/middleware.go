// Package idempotency implements request deduplication for state-mutating
// endpoints via the X-Idempotency-Key and Idempotency-Key headers, following the
// pattern used by modern payment APIs: a client-supplied key scopes a request
// so that a retry (e.g. after a network timeout) replays the original result
// instead of re-executing the handler and creating duplicate transactions.
package idempotency

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
)

const (
	headerKey  = "Idempotency-Key"
	xHeaderKey = "X-Idempotency-Key"
	ttl        = 24 * time.Hour
)

// Options controls middleware behavior for idempotency enforcement.
type Options struct {
	// Required specifies whether the request must supply an idempotency key.
	// If false, requests without an idempotency key proceed normally without deduplication.
	Required bool
}

// Middleware returns middleware with optional idempotency-key semantics.
func Middleware(repo Repository) func(http.Handler) http.Handler {
	return MiddlewareWithOptions(repo, Options{Required: false})
}

// OptionalMiddleware returns middleware where the idempotency key is optional.
func OptionalMiddleware(repo Repository) func(http.Handler) http.Handler {
	return MiddlewareWithOptions(repo, Options{Required: false})
}

// RequiredMiddleware returns middleware where the idempotency key is required.
func RequiredMiddleware(repo Repository) func(http.Handler) http.Handler {
	return MiddlewareWithOptions(repo, Options{Required: true})
}

// MiddlewareWithOptions returns middleware configured with the specified options.
func MiddlewareWithOptions(repo Repository, opts Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rawKey := ExtractKey(r)
			if rawKey == "" {
				if opts.Required {
					api.Error(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required for this endpoint")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			key := DeterministicKey(rawKey)

			body, err := io.ReadAll(r.Body)
			if err != nil {
				api.BadRequest(w, "failed to read request body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			hash := requestHash(r.Method, r.URL.Path, body)
			orgID := tenant.IDFromContext(r.Context())

			rec, existed, err := repo.TryAcquire(r.Context(), orgID, key, hash, time.Now().UTC().Add(ttl))
			if err != nil {
				api.InternalError(w, err)
				return
			}

			if existed {
				switch {
				case rec.Status == StatusProcessing:
					api.Error(w, http.StatusConflict, "REQUEST_IN_PROGRESS", "a request with this idempotency key is already being processed")
				case rec.RequestHash != hash:
					// Issue #151: Return 409 Conflict if the same key is used with a different request body
					api.Error(w, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY", "this idempotency key was previously used with a different request body")
				default:
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(rec.ResponseStatus)
					_, _ = w.Write(rec.ResponseBody)
				}
				return
			}

			rec2 := newRecorder(w)
			next.ServeHTTP(rec2, r)
			_ = repo.Complete(r.Context(), orgID, key, rec2.status, rec2.body)
		})
	}
}

// ExtractKey reads the idempotency key from X-Idempotency-Key or Idempotency-Key header.
func ExtractKey(r *http.Request) string {
	if k := r.Header.Get(xHeaderKey); k != "" {
		return k
	}
	if k := r.Header.Get(headerKey); k != "" {
		return k
	}
	return ""
}

// DeterministicKey returns a valid UUID string derived deterministically from raw.
// If raw is already a valid UUID v4, it is normalized and returned directly.
// Otherwise, it generates a deterministic RFC 4122 v4 UUID using SHA-256 of raw.
func DeterministicKey(raw string) string {
	if parsed, err := uuid.Parse(raw); err == nil && parsed.Version() == 4 {
		return parsed.String()
	}

	sum := sha256.Sum256([]byte(raw))
	u, err := uuid.FromBytes(sum[:16])
	if err != nil {
		return uuid.NewSHA1(uuid.NameSpaceOID, []byte(raw)).String()
	}
	u[6] = (u[6] & 0x0f) | 0x40 // Version 4
	u[8] = (u[8] & 0x3f) | 0x80 // RFC 4122 variant
	return u.String()
}

func requestHash(method, path string, body []byte) string {
	h := sha256.New()
	h.Write([]byte(method))
	h.Write([]byte(path))
	h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}
