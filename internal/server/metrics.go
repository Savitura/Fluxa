package server

import (
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type Metrics struct {
	StartedAt      time.Time     `json:"started_at"`
	TotalRequests  int64         `json:"total_requests"`
	ActiveRequests int64         `json:"active_requests"`
	TotalErrors    int64         `json:"total_errors"`
	UptimeSeconds  float64       `json:"uptime_seconds"`
	RequestsByCode map[int]int64 `json:"requests_by_code"`
	mu             sync.RWMutex
	requestsByCode map[int]*int64
}

var globalMetrics = &Metrics{
	StartedAt:      time.Now(),
	RequestsByCode: make(map[int]int64),
	requestsByCode: make(map[int]*int64),
}

func init() {
	for _, code := range []int{200, 201, 202, 400, 401, 403, 404, 429, 500} {
		var v int64
		globalMetrics.requestsByCode[code] = &v
	}
}

func (m *Metrics) RecordRequest(statusCode int) {
	atomic.AddInt64(&m.TotalRequests, 1)
	if statusCode >= 400 {
		atomic.AddInt64(&m.TotalErrors, 1)
	}
	m.mu.Lock()
	if counter, ok := m.requestsByCode[statusCode]; ok {
		atomic.AddInt64(counter, 1)
	}
	m.mu.Unlock()
}

func (m *Metrics) IncrementActive() {
	atomic.AddInt64(&m.ActiveRequests, 1)
}

func (m *Metrics) DecrementActive() {
	atomic.AddInt64(&m.ActiveRequests, -1)
}

func (m *Metrics) Snapshot() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	byCode := make(map[string]int64)
	for code, counter := range m.requestsByCode {
		byCode[http.StatusText(code)] = atomic.LoadInt64(counter)
	}

	return map[string]interface{}{
		"started_at":       m.StartedAt,
		"uptime_seconds":   time.Since(m.StartedAt).Seconds(),
		"total_requests":   atomic.LoadInt64(&m.TotalRequests),
		"active_requests":  atomic.LoadInt64(&m.ActiveRequests),
		"total_errors":     atomic.LoadInt64(&m.TotalErrors),
		"requests_by_code": byCode,
	}
}

func MetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		globalMetrics.IncrementActive()
		defer globalMetrics.DecrementActive()

		ww := &statusWriter{ResponseWriter: w, status: 200}
		next.ServeHTTP(ww, r)
		globalMetrics.RecordRequest(ww.status)
	})
}

func MetricsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(globalMetrics.Snapshot())
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}
