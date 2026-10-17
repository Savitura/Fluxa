package server

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"golang.org/x/time/rate"
)

// AuthRateLimitConfig holds the rate limiting parameters for sensitive
// unauthenticated public endpoints (/v1/auth/register, /v1/auth/login, /v1/org/invites/accept).
type AuthRateLimitConfig struct {
	IPRPS        float64
	IPBurst      int
	AccountRPS   float64
	AccountBurst int
}

// DefaultAuthRateLimitConfig returns the default rate limit parameters.
func DefaultAuthRateLimitConfig() AuthRateLimitConfig {
	return AuthRateLimitConfig{
		IPRPS:        5,
		IPBurst:      10,
		AccountRPS:   1,
		AccountBurst: 5,
	}
}

// AuthRateLimiter enforces dual-layer rate limiting: per-IP and per-account.
//
// Security note:
// The per-IP limiter relies on r.RemoteAddr, which in many production setups
// (reverse proxies, load balancers, CDN edges) or environments where RemoteAddr
// includes ephemeral port numbers is known to be imperfect or broken (tracked separately
// in issue backlog). Therefore, the per-IP limiter cannot be assumed sufficient on its own.
// Dual-layer limiting (coupling per-IP with per-account limiting based on email or token)
// ensures that credential stuffing or brute-forcing a specific target account is throttled
// even when distributed across multiple IP addresses.
type AuthRateLimiter struct {
	mu              sync.Mutex
	ipVisitors      map[string]*rate.Limiter
	accountVisitors map[string]*rate.Limiter
	ipRPS           rate.Limit
	ipBurst         int
	accountRPS      rate.Limit
	accountBurst    int
}

// NewAuthRateLimiter creates an AuthRateLimiter and launches a periodic cleanup worker.
func NewAuthRateLimiter(cfg AuthRateLimitConfig) *AuthRateLimiter {
	if cfg.IPRPS <= 0 {
		cfg.IPRPS = 5
	}
	if cfg.IPBurst <= 0 {
		cfg.IPBurst = 10
	}
	if cfg.AccountRPS <= 0 {
		cfg.AccountRPS = 1
	}
	if cfg.AccountBurst <= 0 {
		cfg.AccountBurst = 5
	}

	l := &AuthRateLimiter{
		ipVisitors:      make(map[string]*rate.Limiter),
		accountVisitors: make(map[string]*rate.Limiter),
		ipRPS:           rate.Limit(cfg.IPRPS),
		ipBurst:         cfg.IPBurst,
		accountRPS:      rate.Limit(cfg.AccountRPS),
		accountBurst:    cfg.AccountBurst,
	}
	go l.cleanup()
	return l
}

func (l *AuthRateLimiter) allow(visitors map[string]*rate.Limiter, rps rate.Limit, burst int, key string) (bool, int) {
	l.mu.Lock()
	defer l.mu.Unlock()

	limiter, ok := visitors[key]
	if !ok {
		limiter = rate.NewLimiter(rps, burst)
		visitors[key] = limiter
	}

	if limiter.Allow() {
		return true, 0
	}

	// Calculate Retry-After duration in seconds
	tokens := limiter.TokensAt(time.Now())
	needed := 1.0 - tokens
	retryAfter := 1
	if rps > 0 {
		retryAfter = int(math.Ceil(needed / float64(rps)))
	}
	if retryAfter < 1 {
		retryAfter = 1
	}
	return false, retryAfter
}

func (l *AuthRateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.mu.Lock()
		now := time.Now()
		for key, lim := range l.ipVisitors {
			if lim.TokensAt(now) >= float64(l.ipBurst) {
				delete(l.ipVisitors, key)
			}
		}
		for key, lim := range l.accountVisitors {
			if lim.TokensAt(now) >= float64(l.accountBurst) {
				delete(l.accountVisitors, key)
			}
		}
		l.mu.Unlock()
	}
}

// AccountExtractor extracts an account/identity key from a request body.
type AccountExtractor func(body []byte) string

// ExtractEmail extracts the email field from JSON request bodies (/register, /login).
func ExtractEmail(body []byte) string {
	var req struct {
		Email string `json:"email"`
	}
	_ = json.Unmarshal(body, &req)
	return strings.ToLower(strings.TrimSpace(req.Email))
}

// ExtractInviteToken extracts the invite token from JSON request bodies (/org/invites/accept).
func ExtractInviteToken(body []byte) string {
	var req struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(body, &req)
	return strings.TrimSpace(req.Token)
}

// Limit returns a middleware that enforces rate limiting per IP and per account.
func (l *AuthRateLimiter) Limit(extractor AccountExtractor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// 1. Enforce Per-IP Limit
			ipKey := extractClientIP(r)
			allowed, retryAfter := l.allow(l.ipVisitors, l.ipRPS, l.ipBurst, ipKey)
			if !allowed {
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				api.Error(w, http.StatusTooManyRequests, "RATE_LIMITED", "rate limit exceeded, retry later")
				return
			}

			// 2. Enforce Per-Account Limit (if extractor provided and account found)
			if extractor != nil && r.Body != nil {
				bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 64*1024))
				if err == nil {
					r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
					accountKey := extractor(bodyBytes)
					if accountKey != "" {
						acctAllowed, acctRetryAfter := l.allow(l.accountVisitors, l.accountRPS, l.accountBurst, accountKey)
						if !acctAllowed {
							w.Header().Set("Retry-After", strconv.Itoa(acctRetryAfter))
							api.Error(w, http.StatusTooManyRequests, "RATE_LIMITED", "rate limit exceeded, retry later")
							return
						}
					}
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
