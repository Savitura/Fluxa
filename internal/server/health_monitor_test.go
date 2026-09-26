package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func decodeHealth(t *testing.T, handler http.HandlerFunc) (int, HealthResponse) {
	t.Helper()
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	var body HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return rec.Code, body
}

func TestHealth_AllHealthyReturnsOKWithLatency(t *testing.T) {
	monitor := NewHealthMonitor(map[string]DependencyCheck{
		"database":     func(context.Context) error { return nil },
		"redis":        func(context.Context) error { return nil },
		"stellar":      func(context.Context) error { return nil },
		"stellar_core": func(context.Context) error { return nil },
	})

	code, body := decodeHealth(t, monitor.Handler())
	if code != http.StatusOK {
		t.Fatalf("status = %d, want %d", code, http.StatusOK)
	}
	if body.Status != "ok" {
		t.Fatalf("status = %q, want ok", body.Status)
	}
	if len(body.Checks) != 4 {
		t.Fatalf("expected 4 dependency checks, got %d", len(body.Checks))
	}
	for name, check := range body.Checks {
		if check.Status != "connected" {
			t.Fatalf("%s status = %q, want connected", name, check.Status)
		}
		// Latency must be reported, in milliseconds, for every dependency.
		if check.ResponseTimeMS < 0 {
			t.Fatalf("%s reported negative latency %d", name, check.ResponseTimeMS)
		}
		if check.Error != "" {
			t.Fatalf("%s reported an error while healthy: %s", name, check.Error)
		}
	}
	if body.CheckedAt.IsZero() {
		t.Fatal("expected checked_at to be populated")
	}
}

func TestHealth_OneDependencyDownReturns503(t *testing.T) {
	monitor := NewHealthMonitor(map[string]DependencyCheck{
		"database": func(context.Context) error { return nil },
		"redis":    func(context.Context) error { return nil },
		"stellar":  func(context.Context) error { return errors.New("dial tcp: connection refused") },
	})

	code, body := decodeHealth(t, monitor.Handler())
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", code, http.StatusServiceUnavailable)
	}
	if body.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", body.Status)
	}
	if body.Checks["stellar"].Status != "unavailable" {
		t.Fatalf("stellar = %#v, want unavailable", body.Checks["stellar"])
	}
	if body.Checks["stellar"].Error != "dial tcp: connection refused" {
		t.Fatalf("expected the underlying error to be surfaced, got %q", body.Checks["stellar"].Error)
	}
	// Healthy dependencies must still be reported as connected.
	if body.Checks["database"].Status != "connected" || body.Checks["redis"].Status != "connected" {
		t.Fatalf("healthy dependencies were marked down: %#v", body.Checks)
	}
}

func TestHealth_AllDownReturns503(t *testing.T) {
	monitor := NewHealthMonitor(map[string]DependencyCheck{
		"database": func(context.Context) error { return errors.New("db down") },
		"redis":    func(context.Context) error { return errors.New("redis down") },
		"stellar":  func(context.Context) error { return errors.New("horizon down") },
	})

	code, body := decodeHealth(t, monitor.Handler())
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", code, http.StatusServiceUnavailable)
	}
	if body.Status != "degraded" {
		t.Fatalf("status = %q, want degraded", body.Status)
	}
	if len(body.Checks) != 3 {
		t.Fatalf("expected all 3 dependencies reported, got %d", len(body.Checks))
	}
	for name, check := range body.Checks {
		if check.Status != "unavailable" {
			t.Fatalf("%s = %#v, want unavailable", name, check)
		}
	}
}

// TestHealth_MonitorCachesAndRefreshesInBackground covers the caching
// requirement: once warmed, requests are served from memory and only the
// background ticker re-runs the dependency checks.
func TestHealth_MonitorCachesAndRefreshesInBackground(t *testing.T) {
	var calls atomic.Int64
	monitor := NewHealthMonitor(map[string]DependencyCheck{
		"database": func(context.Context) error {
			calls.Add(1)
			return nil
		},
	}, 20*time.Millisecond)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitor.Start(ctx)

	if got := calls.Load(); got != 1 {
		t.Fatalf("Start should perform an initial refresh, got %d calls", got)
	}

	// Many requests inside the refresh window must not re-run checks.
	for i := 0; i < 20; i++ {
		if code, _ := decodeHealth(t, monitor.Handler()); code != http.StatusOK {
			t.Fatalf("request %d status = %d, want 200", i, code)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("cached requests re-ran checks: %d calls", got)
	}

	// The background loop must eventually refresh on its own.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() > 1 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("background refresh never re-ran the dependency check")
}

// TestHealth_HandlerIsNonBlockingOnHealthyDependencies asserts the endpoint
// adds no per-request dependency latency: repeated calls stay fast because the
// answer is served from cache.
func TestHealth_HandlerIsNonBlockingOnHealthyDependencies(t *testing.T) {
	monitor := NewHealthMonitor(map[string]DependencyCheck{
		"database": func(context.Context) error { time.Sleep(5 * time.Millisecond); return nil },
	})
	monitor.Start(context.Background())

	start := time.Now()
	for i := 0; i < 50; i++ {
		decodeHealth(t, monitor.Handler())
	}
	elapsed := time.Since(start)
	// Uncached would be 50*5ms = 250ms; cached should be far below that.
	if elapsed > 100*time.Millisecond {
		t.Fatalf("50 cached health requests took %s, expected far less", elapsed)
	}
}

func TestHealth_MonitorRunsChecksConcurrently(t *testing.T) {
	// Each check blocks until all three have started. If Refresh ran them
	// serially this would deadlock, so a timeout here means the fan-out broke.
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	check := func(context.Context) error {
		started <- struct{}{}
		<-release
		return nil
	}

	monitor := NewHealthMonitor(map[string]DependencyCheck{
		"a": check, "b": check, "c": check,
	})

	done := make(chan struct{})
	go func() {
		monitor.Refresh(context.Background())
		close(done)
	}()

	for i := 0; i < 3; i++ {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatal("dependency checks did not run concurrently")
		}
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Refresh did not return")
	}
}

func TestHealth_NilCheckIsReportedNotCrashed(t *testing.T) {
	monitor := NewHealthMonitor(map[string]DependencyCheck{
		"database": nil,
	})

	code, body := decodeHealth(t, monitor.Handler())
	if code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", code, http.StatusServiceUnavailable)
	}
	if body.Checks["database"].Error == "" {
		t.Fatal("expected an error explaining the check is not configured")
	}
}

func TestHealth_HTTPDependencyCheckRequiresURL(t *testing.T) {
	if err := HTTPDependencyCheck("")(context.Background()); err == nil {
		t.Fatal("expected an unconfigured URL to fail the check")
	}
	if err := StellarCoreDependencyCheck("")(context.Background()); err == nil {
		t.Fatal("expected an unconfigured core URL to fail the check")
	}
}

func TestHealth_StellarCoreCheckCallsGetInfo(t *testing.T) {
	var gotMethod string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotMethod = body.Method
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"info":{}}}`))
	}))
	defer upstream.Close()

	if err := StellarCoreDependencyCheck(upstream.URL)(context.Background()); err != nil {
		t.Fatalf("StellarCoreDependencyCheck() error: %v", err)
	}
	if gotMethod != "getInfo" {
		t.Fatalf("method = %q, want getInfo", gotMethod)
	}
}

func TestHealth_StellarCoreCheckRejectsErrorStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer upstream.Close()

	if err := StellarCoreDependencyCheck(upstream.URL)(context.Background()); err == nil {
		t.Fatal("expected a non-success status to fail the check")
	}
}

func TestHealth_StartIsIdempotent(t *testing.T) {
	var calls atomic.Int64
	monitor := NewHealthMonitor(map[string]DependencyCheck{
		"database": func(context.Context) error {
			calls.Add(1)
			return nil
		},
	}, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	monitor.Start(ctx)
	monitor.Start(ctx)

	if got := calls.Load(); got != 1 {
		t.Fatalf("Start called twice should only refresh once, got %d calls", got)
	}
}
