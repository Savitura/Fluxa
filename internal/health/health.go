package health

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

const (
	postgresTimeout = 500 * time.Millisecond
	redisTimeout    = 200 * time.Millisecond
	horizonTimeout  = time.Second
	workerTimeout   = 200 * time.Millisecond
	cacheTTL        = 5 * time.Second
)

type Status string

const (
	Healthy   Status = "healthy"
	Degraded  Status = "degraded"
	Unhealthy Status = "unhealthy"
)

type ComponentHealth struct {
	Status      Status      `json:"status"`
	LatencyMS   int64       `json:"latencyMs"`
	LastChecked time.Time   `json:"lastChecked"`
	Details     interface{} `json:"details,omitempty"`
}

type Probe func(context.Context) (interface{}, error)

type Service struct {
	probes map[string]Probe
	mu     sync.Mutex
	cached response
}

type response struct {
	statusCode int
	body       map[string]interface{}
	expiresAt  time.Time
}

func New(probes map[string]Probe) *Service { return &Service{probes: probes} }

func (s *Service) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if time.Now().Before(s.cached.expiresAt) {
			write(w, s.cached.statusCode, s.cached.body)
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()

		components := make(map[string]ComponentHealth, len(s.probes))
		for name, probe := range s.probes {
			timeout := componentTimeout(name)
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			start := time.Now()
			details, err := probe(ctx)
			cancel()
			component := ComponentHealth{Status: Healthy, LatencyMS: time.Since(start).Milliseconds(), LastChecked: time.Now().UTC(), Details: details}
			if err != nil {
				component.Status = Unhealthy
				component.Details = map[string]string{"error": err.Error()}
			}
			components[name] = component
		}

		status := Healthy
		failed := 0
		for _, component := range components {
			if component.Status == Unhealthy {
				failed++
			}
		}
		if failed > 0 {
			status = Degraded
		}
		if failed == len(components) && failed > 1 {
			status = Unhealthy
		}
		body := map[string]interface{}{"status": status, "components": components}
		code := http.StatusOK
		if status == Unhealthy {
			code = http.StatusServiceUnavailable
		}
		s.mu.Lock()
		s.cached = response{statusCode: code, body: body, expiresAt: time.Now().Add(cacheTTL)}
		s.mu.Unlock()
		write(w, code, body)
	}
}

func (s *Service) ReadyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		for _, name := range []string{"postgres", "redis"} {
			probe, ok := s.probes[name]
			if !ok {
				http.Error(w, "missing readiness probe", http.StatusServiceUnavailable)
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), componentTimeout(name))
			_, err := probe(ctx)
			cancel()
			if err != nil {
				http.Error(w, "not ready", http.StatusServiceUnavailable)
				return
			}
		}
		write(w, http.StatusOK, map[string]string{"status": string(Healthy)})
	}
}

func LiveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		write(w, http.StatusOK, map[string]string{"status": string(Healthy)})
	}
}

func HTTPProbe(url string) Probe {
	return func(ctx context.Context) (interface{}, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("create request: %w", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
		return nil, nil
	}
}

func HorizonProbe(url string) Probe {
	return func(ctx context.Context) (interface{}, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/fee_stats", nil)
		if err != nil {
			return nil, err
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return nil, fmt.Errorf("unexpected status %d", resp.StatusCode)
		}
		var payload struct {
			LastLedgerBaseFee int64 `json:"last_ledger_base_fee"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			return nil, err
		}
		if payload.LastLedgerBaseFee <= 0 {
			return nil, fmt.Errorf("last_ledger_base_fee is not positive")
		}
		return map[string]int64{"last_ledger_base_fee": payload.LastLedgerBaseFee}, nil
	}
}

func componentTimeout(name string) time.Duration {
	switch name {
	case "postgres":
		return postgresTimeout
	case "redis":
		return redisTimeout
	case "horizon":
		return horizonTimeout
	default:
		return workerTimeout
	}
}

func write(w http.ResponseWriter, code int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}
