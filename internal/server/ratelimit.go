package server

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fluxa/fluxa/internal/api"
	"github.com/fluxa/fluxa/internal/tenant"
	"golang.org/x/time/rate"
)

const maxIdleTime = 5 * time.Minute

type TrustedProxyConfig struct {
	CIDRs []netip.Prefix
}

type clientIPContextKey struct{}

type visitorEntry struct {
	rateLimiter *rate.Limiter
	lastSeen    time.Time
}

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitorEntry
	rate     rate.Limit
	burst    int
}

func newRateLimiter(rps float64, burst int) *rateLimiter {
	rl := &rateLimiter{
		visitors: make(map[string]*visitorEntry),
		rate:     rate.Limit(rps),
		burst:    burst,
	}
	go rl.cleanup()
	return rl
}

func (rl *rateLimiter) getLimiter(key string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.visitors[key]
	if !exists {
		entry = &visitorEntry{
			rateLimiter: rate.NewLimiter(rl.rate, rl.burst),
			lastSeen:    time.Now(),
		}
		rl.visitors[key] = entry
	} else {
		entry.lastSeen = time.Now()
	}
	return entry.rateLimiter
}

func (rl *rateLimiter) cleanup() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for key, entry := range rl.visitors {
			if now.Sub(entry.lastSeen) > maxIdleTime {
				delete(rl.visitors, key)
			}
		}
		rl.mu.Unlock()
	}
}

func remoteIP(remoteAddr string) netip.Addr {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		host = remoteAddr
	}
	ip, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return ip.Unmap()
}

func isTrustedProxy(ip netip.Addr, trustedProxies []netip.Prefix) bool {
	for _, prefix := range trustedProxies {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

func clientIP(r *http.Request, trustedProxies []netip.Prefix) netip.Addr {
	peer := remoteIP(r.RemoteAddr)
	if !peer.IsValid() || !isTrustedProxy(peer, trustedProxies) {
		return peer
	}

	current := peer
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		if !isTrustedProxy(current, trustedProxies) {
			break
		}
		forwardedIP, err := netip.ParseAddr(strings.TrimSpace(forwarded[i]))
		if err != nil {
			break
		}
		current = forwardedIP.Unmap()
	}
	return current
}

func clientIPMiddleware(trustedProxies []netip.Prefix) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r, trustedProxies)
			if ip.IsValid() {
				r = r.WithContext(context.WithValue(r.Context(), clientIPContextKey{}, ip))
			}
			next.ServeHTTP(w, r)
		})
	}
}

func extractClientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPContextKey{}).(netip.Addr); ok {
		return ip.String()
	}
	if ip := remoteIP(r.RemoteAddr); ip.IsValid() {
		return ip.String()
	}
	return r.RemoteAddr
}

func IPRateLimit(rps float64, burst int) func(http.Handler) http.Handler {
	visitors := newRateLimiter(rps, burst)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			limiter := visitors.getLimiter(extractClientIP(r))
			reservation := limiter.Reserve()
			delay := reservation.Delay()
			if !reservation.OK() || delay > 0 {
				reservation.Cancel()
				delaySeconds := int(delay.Seconds()) + 1
				if delaySeconds < 1 {
					delaySeconds = 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(delaySeconds))
				w.Header().Set("RateLimit-Limit", strconv.Itoa(burst))
				w.Header().Set("RateLimit-Remaining", "0")
				w.Header().Set("RateLimit-Reset", strconv.Itoa(delaySeconds))
				api.Error(w, http.StatusTooManyRequests, "RATE_LIMITED", "rate limit exceeded")
				return
			}

			remaining := int(limiter.Tokens())
			if remaining < 0 {
				remaining = 0
			}
			w.Header().Set("RateLimit-Limit", strconv.Itoa(burst))
			w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("RateLimit-Reset", "1")
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit returns a middleware that limits requests per-tenant (or per-IP for unauthenticated).
func RateLimit(rps float64, burst int) func(http.Handler) http.Handler {
	globalLimiter := newRateLimiter(rps, burst)
	tenantBurst := burst * 10
	tenantLimiters := newRateLimiter(rps*10, tenantBurst) // 10x for authenticated tenants

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check global rate first
			gLim := globalLimiter.getLimiter("global")
			gRes := gLim.Reserve()
			if !gRes.OK() || gRes.Delay() > 0 {
				gRes.Cancel()
				w.Header().Set("Retry-After", "1")
				w.Header().Set("RateLimit-Limit", strconv.Itoa(burst))
				w.Header().Set("RateLimit-Remaining", "0")
				w.Header().Set("RateLimit-Reset", "1")
				api.Error(w, http.StatusTooManyRequests, "RATE_LIMITED", "global rate limit exceeded")
				return
			}

			// Per-tenant or per-IP limiting
			tid := tenant.IDFromContext(r.Context())
			key := tid
			effectiveBurst := tenantBurst
			if key == "" {
				key = extractClientIP(r)
				effectiveBurst = burst
			}

			tLim := tenantLimiters.getLimiter(key)
			tRes := tLim.Reserve()
			if !tRes.OK() || tRes.Delay() > 0 {
				tRes.Cancel()
				delaySec := 1
				if tRes.Delay() > 0 {
					delaySec = int(tRes.Delay().Seconds()) + 1
				}
				w.Header().Set("Retry-After", strconv.Itoa(delaySec))
				w.Header().Set("RateLimit-Limit", strconv.Itoa(effectiveBurst))
				w.Header().Set("RateLimit-Remaining", "0")
				w.Header().Set("RateLimit-Reset", strconv.Itoa(delaySec))
				api.Error(w, http.StatusTooManyRequests, "RATE_LIMITED", "rate limit exceeded")
				return
			}

			remaining := int(tLim.Tokens())
			if remaining < 0 {
				remaining = 0
			}
			w.Header().Set("RateLimit-Limit", strconv.Itoa(effectiveBurst))
			w.Header().Set("RateLimit-Remaining", strconv.Itoa(remaining))
			w.Header().Set("RateLimit-Reset", "1")

			next.ServeHTTP(w, r)
		})
	}
}
