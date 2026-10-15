// Package idempotency implements request deduplication for state-mutating
// endpoints via the X-Idempotency-Key and Idempotency-Key headers, following
// the pattern used by modern payment APIs: a client-supplied key scopes a
// request so that a retry (e.g. after a network timeout) replays the original
// result instead of re-executing the handler and creating duplicate
// transactions.
package idempotency

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	fluxacrypto "github.com/fluxa/fluxa/internal/crypto"
	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

const (
	headerKey  = "Idempotency-Key"
	xHeaderKey = "X-Idempotency-Key"

	// DefaultTTL is the default idempotency key lifetime. It matches the value
	// documented in docs/idempotency.md and the IDEMPOTENCY_TTL_HOURS default.
	DefaultTTL = 24 * time.Hour

	defaultLeaseTTL          = 30 * time.Second
	defaultCompletionTimeout = 5 * time.Second
	defaultMaxResponseBody   = 1 << 20
	minimumRetryAfterSeconds = 1
)

// Options controls middleware behavior for idempotency enforcement.
type Options struct {
	// Required specifies whether the request must supply an idempotency key.
	// If false, requests without an idempotency key proceed normally without
	// deduplication.
	Required bool
	// TTL overrides the lifetime applied to new idempotency records.
	// A zero value defaults to DefaultTTL.
	TTL time.Duration
	// LeaseTTL bounds how long a single in-flight request may hold a key before
	// another request may recover it.
	LeaseTTL time.Duration
	// CompletionTimeout bounds how long the middleware waits for the durable
	// response to be persisted after the handler returns.
	CompletionTimeout time.Duration
	// MaxResponseBody caps the buffered response size.
	MaxResponseBody int64
	// AllowLeaseRecovery must only be enabled for operations whose handler is
	// safe to execute again after a crash (currently transfers reconcile their
	// durable transaction before creating or enqueueing anything). Without it,
	// an expired lease is reported as still in progress.
	AllowLeaseRecovery bool
	// Now is injectable for tests.
	Now                   func() time.Time
	ResponseEncryptionKey []byte
}

func (o Options) withDefaults() Options {
	if o.TTL <= 0 {
		o.TTL = DefaultTTL
	}
	if o.LeaseTTL <= 0 {
		o.LeaseTTL = defaultLeaseTTL
	}
	if o.CompletionTimeout <= 0 {
		o.CompletionTimeout = defaultCompletionTimeout
	}
	if o.MaxResponseBody <= 0 {
		o.MaxResponseBody = defaultMaxResponseBody
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	return o
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

// MiddlewareWithOptions returns middleware configured with the specified
// options. The downstream response is buffered and only released to the client
// after the idempotency record has been made durable, so a retry can never
// observe a client-visible success that was not recorded.
func MiddlewareWithOptions(repo Repository, opts Options) func(http.Handler) http.Handler {
	opts = opts.withDefaults()
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, err := GetIdempotencyKey(r)
			if err != nil {
				api.Error(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_CONFLICT", "conflicting Idempotency-Key and X-Idempotency-Key headers")
				return
			}
			if key == "" {
				if opts.Required {
					api.Error(w, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required for this endpoint")
					return
				}
				next.ServeHTTP(w, r)
				return
			}

			key = DeterministicKey(key)
			ctx := withIdempotencyKey(r.Context(), key)

			body, err := io.ReadAll(r.Body)
			if err != nil {
				api.BadRequest(w, "failed to read request body")
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(body))

			hash := requestHash(r.Method, r.URL.Path, body)
			orgID := tenant.IDFromContext(ctx)
			mode := tenant.ModeOrDefault(ctx, domain.ModeLive)
			now := opts.Now().UTC()

			acquisition, err := repo.Acquire(
				ctx,
				orgID,
				mode,
				key,
				hash,
				now,
				now.Add(opts.LeaseTTL),
				now.Add(opts.TTL),
				opts.AllowLeaseRecovery,
			)
			if err != nil {
				api.InternalError(w, err)
				return
			}

			switch acquisition.State {
			case Replay:
				responseBody := acquisition.Record.ResponseBody
				if len(opts.ResponseEncryptionKey) > 0 {
					responseBody, err = fluxacrypto.Decrypt(responseBody, opts.ResponseEncryptionKey)
					if err != nil {
						api.Error(w, http.StatusInternalServerError, "IDEMPOTENCY_RESPONSE_UNAVAILABLE", "the stored operation response could not be recovered")
						return
					}
				}
				writeResponse(w, acquisition.Record.ResponseStatus, acquisition.Record.ResponseHeaders, responseBody, true)
				return
			case BodyMismatch:
				// Issue #151: the same key with a different body is a conflict.
				api.Error(w, http.StatusConflict, "IDEMPOTENCY_KEY_REUSED_WITH_DIFFERENT_BODY", "this idempotency key was previously used with a different request body")
				return
			case InProgress:
				writeInProgress(w, acquisition.Record.LeaseExpiresAt, now)
				return
			case LeaseExpired:
				if !opts.AllowLeaseRecovery {
					writeInProgress(w, acquisition.Record.LeaseExpiresAt, now)
					return
				}
			case Acquired:
			default:
				api.InternalError(w, fmt.Errorf("unknown idempotency acquisition state %d", acquisition.State))
				return
			}

			recorder := newRecorder(int(opts.MaxResponseBody))
			next.ServeHTTP(recorder, r.WithContext(withRecordID(ctx, acquisition.Record.ID)))
			if recorder.err != nil {
				zerolog.Ctx(ctx).Error().Err(recorder.err).Str("idempotency_key", key).Msg("failed to buffer idempotent response")
				api.Error(w, http.StatusInternalServerError, "IDEMPOTENCY_RESPONSE_TOO_LARGE", "the response could not be buffered for idempotent replay")
				return
			}

			response := recorder.response()
			response.Headers = replayableHeader(response.Headers)
			recordedResponse := response
			if len(opts.ResponseEncryptionKey) > 0 {
				recordedResponse.Body, err = fluxacrypto.Encrypt(response.Body, opts.ResponseEncryptionKey)
				if err != nil {
					api.Error(w, http.StatusInternalServerError, "IDEMPOTENCY_RESPONSE_ENCRYPTION_FAILED", "the operation response could not be stored securely")
					return
				}
			}
			completeCtx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), opts.CompletionTimeout)
			err = repo.Complete(completeCtx, acquisition.Record.ID, acquisition.Record.LeaseToken, recordedResponse, opts.Now().UTC().Add(opts.TTL))
			cancel()
			if err != nil {
				zerolog.Ctx(ctx).Error().Err(err).Str("idempotency_key", key).Msg("failed to persist idempotent response")
				api.Error(w, http.StatusInternalServerError, "IDEMPOTENCY_RECORD_PERSISTENCE_FAILED", "the operation completed but its idempotency record could not be persisted; retry with the same key")
				return
			}

			writeResponse(w, response.Status, response.Headers, response.Body, false)
		})
	}
}

func writeInProgress(w http.ResponseWriter, leaseExpiresAt, now time.Time) {
	retryAfter := minimumRetryAfterSeconds
	if !leaseExpiresAt.IsZero() {
		seconds := int(math.Ceil(leaseExpiresAt.Sub(now).Seconds()))
		if seconds > retryAfter {
			retryAfter = seconds
		}
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	api.Error(w, http.StatusConflict, "REQUEST_IN_PROGRESS", "a request with this idempotency key is already being processed")
}

func writeResponse(w http.ResponseWriter, status int, headers http.Header, body []byte, replay bool) {
	if status == 0 {
		status = http.StatusOK
	}
	copyReplayableHeader(w.Header(), headers)
	if replay {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.WriteHeader(status)
	if len(body) > 0 {
		_, _ = w.Write(body)
	}
}

func replayableHeader(header http.Header) http.Header {
	filtered := make(http.Header)
	copyReplayableHeader(filtered, header)
	return filtered
}

func copyReplayableHeader(dst, src http.Header) {
	for name, values := range src {
		if !isReplayableHeader(name) {
			continue
		}
		for _, value := range values {
			dst.Add(name, value)
		}
	}
}

func isReplayableHeader(name string) bool {
	canonical := http.CanonicalHeaderKey(name)
	switch canonical {
	case "Connection",
		"Content-Length",
		"Date",
		"Idempotency-Replayed",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Te",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
		"X-Request-Id":
		return false
	default:
		return true
	}
}

// ErrConflictingKeys is returned when both idempotency headers carry different
// values: which one wins would otherwise depend on the endpoint.
var ErrConflictingKeys = errors.New("conflicting Idempotency-Key and X-Idempotency-Key headers")

// GetIdempotencyKey resolves the idempotency key for a request.
//
// Idempotency-Key is the canonical header; X-Idempotency-Key is a documented
// alias kept for older clients. Handlers must not read either header directly —
// they read the key the middleware put on the request context with
// IdempotencyKeyFromContext. Sending both with different values is a client
// error, not a precedence question.
func GetIdempotencyKey(r *http.Request) (string, error) {
	canonical := strings.TrimSpace(r.Header.Get(headerKey))
	alias := strings.TrimSpace(r.Header.Get(xHeaderKey))

	switch {
	case canonical == "" && alias == "":
		return "", nil
	case canonical != "" && alias != "" && canonical != alias:
		return "", ErrConflictingKeys
	case canonical != "":
		return canonical, nil
	default:
		return alias, nil
	}
}

// ExtractKey returns the idempotency key from the request headers, preferring
// the canonical Idempotency-Key. It ignores the conflict case (returning the
// canonical value when both are present); use GetIdempotencyKey when a
// conflicting pair must be rejected.
func ExtractKey(r *http.Request) string {
	key, err := GetIdempotencyKey(r)
	if err != nil {
		if canonical := strings.TrimSpace(r.Header.Get(headerKey)); canonical != "" {
			return canonical
		}
	}
	return key
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

// RequestHash computes the canonical SHA-256 fingerprint for a request: SHA-256(method + "\x00" + path + "\x00" + body).
func RequestHash(method, path string, body []byte) string {
	h := sha256.New()
	_, _ = h.Write([]byte(method))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(path))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write(body)
	return hex.EncodeToString(h.Sum(nil))
}

func requestHash(method, path string, body []byte) string {
	return RequestHash(method, path, body)
}

type recordIDContextKey struct{}

func withRecordID(ctx context.Context, recordID string) context.Context {
	return context.WithValue(ctx, recordIDContextKey{}, recordID)
}

// WithRecordID attaches a durable idempotency record ID to the context. It is
// used by request paths that recover a transfer outside the HTTP middleware
// (for example, an operator re-running a request whose process died) so the
// transfer service can fence the recovery to the exact generation.
func WithRecordID(ctx context.Context, recordID string) context.Context {
	return withRecordID(ctx, recordID)
}

// RecordIDFromContext returns the durable idempotency record ID for the
// current request. Transfer persistence uses it to enforce one transaction per
// processing generation without coupling the transfer service to HTTP state.
func RecordIDFromContext(ctx context.Context) string {
	recordID, _ := ctx.Value(recordIDContextKey{}).(string)
	return recordID
}

type idempotencyKeyContextKey struct{}

func withIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyContextKey{}, key)
}

// IdempotencyKeyFromContext returns the idempotency key for the current request.
func IdempotencyKeyFromContext(ctx context.Context) string {
	k, _ := ctx.Value(idempotencyKeyContextKey{}).(string)
	return k
}
