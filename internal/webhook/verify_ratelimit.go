package webhook

import (
	"net/http"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// verifyRateLimiter enforces a pure per-IP request budget with no shared
// global cap — internal/server.RateLimit couples a global limiter and a
// per-key limiter together (by design, for authenticated routes sharing one
// backend), which doesn't fit "60 requests per minute per IP" for this
// public, unauthenticated endpoint: two different IPs must not be able to
// exhaust each other's budget.
type verifyRateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*rate.Limiter
	rps      rate.Limit
	burst    int
}

func newVerifyRateLimiter(rps float64, burst int) *verifyRateLimiter {
	rl := &verifyRateLimiter{
		visitors: make(map[string]*rate.Limiter),
		rps:      rate.Limit(rps),
		burst:    burst,
	}
	go rl.cleanup()
	return rl
}

func (rl *verifyRateLimiter) allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	limiter, ok := rl.visitors[key]
	if !ok {
		limiter = rate.NewLimiter(rl.rps, rl.burst)
		rl.visitors[key] = limiter
	}
	return limiter.Allow()
}

func (rl *verifyRateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		for key, limiter := range rl.visitors {
			if limiter.TokensAt(time.Now()) >= float64(rl.burst) {
				delete(rl.visitors, key)
			}
		}
		rl.mu.Unlock()
	}
}

// VerifyRateLimit returns middleware enforcing 60 requests/minute per
// client IP on the wrapped handler.
func VerifyRateLimit() func(http.Handler) http.Handler {
	limiter := newVerifyRateLimiter(1, 60) // 1 token/sec refill, burst 60 == 60/min
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !limiter.allow(r.RemoteAddr) {
				http.Error(w, `{"error":{"code":"RATE_LIMITED","message":"rate limit exceeded, retry later"}}`, http.StatusTooManyRequests)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
