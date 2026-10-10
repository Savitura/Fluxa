package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealthHandlerReturnsOKWhenDependenciesPass(t *testing.T) {
	handler := HealthHandler(map[string]DependencyCheck{
		"database": func(context.Context) error { return nil },
		"redis":    func(context.Context) error { return nil },
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	var body HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.Status != "ok" {
		t.Fatalf("expected ok status, got %q", body.Status)
	}
	if body.Checks["database"].Status != "connected" || body.Checks["redis"].Status != "connected" {
		t.Fatalf("expected successful dependency checks, got %#v", body.Checks)
	}
}

func TestHealthHandlerReturnsUnavailableWhenDependencyFails(t *testing.T) {
	handler := HealthHandler(map[string]DependencyCheck{
		"database": func(context.Context) error { return nil },
		"redis":    func(context.Context) error { return errors.New("connection refused") },
	})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status %d, got %d", http.StatusServiceUnavailable, rec.Code)
	}

	var body HealthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body.Status != "degraded" {
		t.Fatalf("expected degraded status, got %q", body.Status)
	}
	if body.Checks["redis"].Status != "unavailable" {
		t.Fatalf("expected redis to be unavailable, got %#v", body.Checks)
	}
	if body.Checks["redis"].Error != "connection refused" {
		t.Fatalf("expected redis failure in checks, got %#v", body.Checks)
	}
	if body.Checks["database"].Status != "connected" {
		t.Fatalf("expected database check to remain ok, got %#v", body.Checks)
	}
}

func TestHealthHandlerCachesDependencyChecksBriefly(t *testing.T) {
	calls := 0
	handler := HealthHandler(map[string]DependencyCheck{
		"database": func(context.Context) error {
			calls++
			return nil
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if calls != 1 {
		t.Fatalf("expected cached response to avoid second dependency check, got %d calls", calls)
	}
}

func TestHTTPDependencyCheckRejectsNonSuccessStatus(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()

	err := HTTPDependencyCheck(upstream.URL)(context.Background())
	if err == nil {
		t.Fatal("expected non-success status to fail")
	}
}
