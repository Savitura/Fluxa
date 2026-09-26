package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	healthCheckTimeout    = 2 * time.Second
	healthStatusCacheTTL  = 30 * time.Second
	healthRefreshInterval = healthStatusCacheTTL
)

type DependencyCheck func(context.Context) error

func HorizonDependencyCheck(baseURL string) DependencyCheck {
	return func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/fee_stats", nil)
		if err != nil {
			return err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
		var payload struct {
			LastLedgerBaseFee int64 `json:"last_ledger_base_fee"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return err
		}
		if payload.LastLedgerBaseFee <= 0 {
			return fmt.Errorf("last_ledger_base_fee is not positive")
		}
		return nil
	}
}

func HTTPDependencyCheck(url string) DependencyCheck {
	return func(ctx context.Context) error {
		if url == "" {
			return errors.New("dependency URL is not configured")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
		return nil
	}
}

func StellarCoreDependencyCheck(url string) DependencyCheck {
	return func(ctx context.Context) error {
		if url == "" {
			return errors.New("stellar core URL is not configured")
		}
		body := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"getInfo","params":[]}`)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
		return nil
	}
}

type DependencyHealth struct {
	Status         string `json:"status"`
	ResponseTimeMS int64  `json:"response_time_ms"`
	Error          string `json:"error,omitempty"`
}

type HealthResponse struct {
	Status    string                      `json:"status"`
	Checks    map[string]DependencyHealth `json:"checks,omitempty"`
	CheckedAt time.Time                   `json:"checked_at"`
}

type dependencyHealth = DependencyHealth
type healthResponse = HealthResponse

type cachedHealthResponse struct {
	statusCode int
	body       HealthResponse
	expiresAt  time.Time
}

type HealthMonitor struct {
	checks   map[string]DependencyCheck
	interval time.Duration

	mu          sync.RWMutex
	cached      cachedHealthResponse
	initialized bool

	refreshMu sync.Mutex
	startOnce sync.Once
}

func NewHealthMonitor(checks map[string]DependencyCheck, intervals ...time.Duration) *HealthMonitor {
	interval := healthRefreshInterval
	if len(intervals) > 0 && intervals[0] > 0 {
		interval = intervals[0]
	}
	copied := make(map[string]DependencyCheck, len(checks))
	for name, check := range checks {
		copied[name] = check
	}
	return &HealthMonitor{checks: copied, interval: interval}
}

func (m *HealthMonitor) Start(ctx context.Context) {
	m.startOnce.Do(func() {
		m.Refresh(ctx)
		go func() {
			ticker := time.NewTicker(m.interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					m.Refresh(ctx)
				}
			}
		}()
	})
}

func (m *HealthMonitor) Refresh(ctx context.Context) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()

	type result struct {
		name   string
		health DependencyHealth
	}

	results := make(chan result, len(m.checks))
	for name, check := range m.checks {
		go func(name string, check DependencyCheck) {
			start := time.Now()
			checkCtx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
			defer cancel()
			if check == nil {
				results <- result{name: name, health: DependencyHealth{
					Status:         "unavailable",
					ResponseTimeMS: time.Since(start).Milliseconds(),
					Error:          "check is not configured",
				}}
				return
			}
			if err := check(checkCtx); err != nil {
				results <- result{name: name, health: DependencyHealth{
					Status:         "unavailable",
					ResponseTimeMS: time.Since(start).Milliseconds(),
					Error:          err.Error(),
				}}
				return
			}
			results <- result{name: name, health: DependencyHealth{
				Status:         "connected",
				ResponseTimeMS: time.Since(start).Milliseconds(),
			}}
		}(name, check)
	}

	status := "ok"
	response := HealthResponse{
		Status:    status,
		Checks:    make(map[string]DependencyHealth, len(m.checks)),
		CheckedAt: time.Now().UTC(),
	}
	for range m.checks {
		result := <-results
		if result.health.Status != "connected" {
			status = "degraded"
		}
		response.Checks[result.name] = result.health
	}
	response.Status = status
	statusCode := http.StatusOK
	if status == "degraded" {
		statusCode = http.StatusServiceUnavailable
	}

	m.mu.Lock()
	m.cached = cachedHealthResponse{
		statusCode: statusCode,
		body:       response,
		expiresAt:  time.Now().Add(m.interval),
	}
	m.initialized = true
	m.mu.Unlock()
}

func (m *HealthMonitor) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		cached := m.cached
		initialized := m.initialized
		m.mu.RUnlock()
		if !initialized || !time.Now().Before(cached.expiresAt) {
			m.Refresh(r.Context())
			m.mu.RLock()
			cached = m.cached
			m.mu.RUnlock()
		}
		writeHealthResponse(w, cached.statusCode, cached.body)
	}
}

func HealthHandler(checks map[string]DependencyCheck) http.HandlerFunc {
	return NewHealthMonitor(checks).Handler()
}

func writeHealthResponse(w http.ResponseWriter, status int, resp HealthResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}
