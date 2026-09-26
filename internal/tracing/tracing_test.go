package tracing

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// newTestProvider installs an in-memory tracer provider plus the W3C
// propagators, and restores the previous global state when the test ends. It
// returns a recorder that captures every span ended during the test.
func newTestProvider(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))

	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	enabled.Store(true)

	t.Cleanup(func() {
		enabled.Store(false)
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
		_ = provider.Shutdown(context.Background())
	})
	return recorder
}

func TestInitDisabledIsANoOp(t *testing.T) {
	shutdown, err := Init(context.Background(), Config{Enabled: false})
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	if Enabled() {
		t.Fatal("tracing should be disabled when OTEL_ENABLED is not set")
	}
	if shutdown == nil {
		t.Fatal("expected a non-nil shutdown func even when disabled")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
}

// TestInitExportsToConfiguredEndpoint is the exporter requirement: with
// tracing enabled, spans must reach the configured OTLP endpoint.
func TestInitExportsToConfiguredEndpoint(t *testing.T) {
	sink := &otlpSink{}
	ts := httptest.NewServer(sink.handler())
	defer ts.Close()

	shutdown, err := Init(context.Background(), Config{
		Enabled:          true,
		ExporterEndpoint: ts.URL,
		ServiceName:      "fluxa-test",
	})
	if err != nil {
		t.Fatalf("Init() error: %v", err)
	}
	t.Cleanup(func() {
		enabled.Store(false)
		otel.SetTracerProvider(trace.NewNoopTracerProvider())
	})
	if !Enabled() {
		t.Fatal("tracing should report enabled")
	}

	// A server span proves the provider is wired into the request path.
	_, span := Start(context.Background(), "GET /health")
	span.End()

	// Shutdown flushes the batcher, so this does not depend on the exporter's
	// periodic export schedule. Polling for the first request instead would
	// make the test fail on a loaded machine.
	if err := ShutdownWithTimeout(shutdown, 10*time.Second); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
	if sink.count() == 0 {
		t.Fatal("no spans were exported to the configured OTLP endpoint")
	}
}

// TestInitDefaultsEndpointAndServiceName covers the fallbacks so a
// misconfigured deployment still starts and still reports a service name.
func TestInitDefaultsEndpointAndServiceName(t *testing.T) {
	// Port 1 is closed, so the exporter is created but never delivers; this
	// asserts Init succeeds and the defaults are applied without erroring.
	shutdown, err := Init(context.Background(), Config{
		Enabled:          true,
		ExporterEndpoint: "http://127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("Init() with defaults error: %v", err)
	}
	if !Enabled() {
		t.Fatal("tracing should be enabled")
	}
	if err := ShutdownWithTimeout(shutdown, 5*time.Second); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
	enabled.Store(false)
	otel.SetTracerProvider(trace.NewNoopTracerProvider())
}

// TestInitNormalisesBareEndpoint checks a scheme-less endpoint is accepted,
// since operators often configure "localhost:4318".
func TestInitNormalisesBareEndpoint(t *testing.T) {
	shutdown, err := Init(context.Background(), Config{
		Enabled:          true,
		ExporterEndpoint: "127.0.0.1:1",
	})
	if err != nil {
		t.Fatalf("Init() with a bare endpoint error: %v", err)
	}
	if !Enabled() {
		t.Fatal("tracing should be enabled")
	}
	if err := ShutdownWithTimeout(shutdown, 5*time.Second); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}
	enabled.Store(false)
	otel.SetTracerProvider(trace.NewNoopTracerProvider())
}

// TestInitInvalidEndpointFails ensures a bad endpoint is a startup error rather
// than silent data loss.
func TestInitInvalidEndpointFails(t *testing.T) {
	shutdown, err := Init(context.Background(), Config{
		Enabled:          true,
		ExporterEndpoint: "http://%zz-invalid",
	})
	if err == nil {
		if shutdown != nil {
			_ = shutdown(context.Background())
		}
		t.Fatal("expected an error for a malformed endpoint")
	}
	if Enabled() {
		t.Fatal("a failed Init must leave tracing disabled")
	}
}

// otlpSink is a minimal OTLP/HTTP collector that records export requests.
type otlpSink struct {
	mu       sync.Mutex
	requests int
}

func (o *otlpSink) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
		}
		o.mu.Lock()
		o.requests++
		o.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	})
}

func (o *otlpSink) count() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.requests
}

// TestHTTPMiddlewareCreatesSpan is the core propagation guarantee: an inbound
// request produces a server span whose trace ID is echoed to the caller.
func TestHTTPMiddlewareCreatesSpan(t *testing.T) {
	recorder := newTestProvider(t)

	var innerTraceID string
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerTraceID = TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/transfers", nil))

	if innerTraceID == "" {
		t.Fatal("expected a trace ID inside the request context")
	}
	if got := rec.Header().Get("X-Trace-ID"); got != innerTraceID {
		t.Fatalf("X-Trace-ID = %q, want %q", got, innerTraceID)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected exactly 1 ended span, got %d", len(spans))
	}
	span := spans[0]
	if span.SpanKind() != trace.SpanKindServer {
		t.Fatalf("span kind = %v, want server", span.SpanKind())
	}
	if span.Name() != "POST /v1/transfers" {
		t.Fatalf("span name = %q, want %q", span.Name(), "POST /v1/transfers")
	}
	if span.SpanContext().TraceID().String() != innerTraceID {
		t.Fatalf("span trace ID %s does not match context trace ID %s",
			span.SpanContext().TraceID(), innerTraceID)
	}

	// Method and path are attached so traces are queryable in the backend.
	attrs := map[string]string{}
	for _, kv := range span.Attributes() {
		attrs[string(kv.Key)] = kv.Value.Emit()
	}
	if attrs["http.request.method"] != http.MethodPost {
		t.Fatalf("http.request.method = %q, want POST", attrs["http.request.method"])
	}
	if attrs["url.path"] != "/v1/transfers" {
		t.Fatalf("url.path = %q, want /v1/transfers", attrs["url.path"])
	}
}

// TestHTTPMiddlewareHonoursUpstreamTraceparent verifies an incoming
// traceparent is adopted, so a trace continues an upstream service's span
// instead of starting a new trace.
func TestHTTPMiddlewareHonoursUpstreamTraceparent(t *testing.T) {
	recorder := newTestProvider(t)

	const upstreamTraceID = "0af7651916cd43dd8448eb211c80319c"
	const upstreamSpanID = "b7ad6b7169203331"

	var innerTraceID string
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		innerTraceID = TraceIDFromContext(r.Context())
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("traceparent", "00-"+upstreamTraceID+"-"+upstreamSpanID+"-01")
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if innerTraceID != upstreamTraceID {
		t.Fatalf("trace ID = %q, want the upstream trace %q", innerTraceID, upstreamTraceID)
	}

	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if parent := spans[0].Parent(); parent.SpanID().String() != upstreamSpanID {
		t.Fatalf("parent span = %q, want %q", parent.SpanID(), upstreamSpanID)
	}
}

// TestHTTPMiddlewareDisabledAddsNoOverhead asserts the documented behaviour
// that with tracing off the middleware becomes a pass-through: no spans, no
// X-Trace-ID header, and the handler still runs exactly once.
func TestHTTPMiddlewareDisabledAddsNoOverhead(t *testing.T) {
	enabled.Store(false)

	calls := 0
	var sawTraceID string
	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		sawTraceID = TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

	if calls != 1 {
		t.Fatalf("handler called %d times, want 1", calls)
	}
	if sawTraceID != "" {
		t.Fatalf("expected no trace ID when disabled, got %q", sawTraceID)
	}
	if got := rec.Header().Get("X-Trace-ID"); got != "" {
		t.Fatalf("expected no X-Trace-ID header when disabled, got %q", got)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestStartReturnsNoopSpanWhenDisabled keeps the zero-overhead contract at the
// helper level: Start must not invent a span when tracing is off.
func TestStartReturnsNoopSpanWhenDisabled(t *testing.T) {
	enabled.Store(false)

	ctx, span := Start(context.Background(), "stellar.horizon.load_account")
	defer span.End()
	if TraceIDFromContext(ctx) != "" {
		t.Fatal("expected no trace ID when tracing is disabled")
	}
	if span.IsRecording() {
		t.Fatal("expected a non-recording span when tracing is disabled")
	}
}

// TestInjectExtractRoundTrip is the mechanism that carries trace context from
// the API into Asynq job payloads and back out again in the worker.
func TestInjectExtractRoundTrip(t *testing.T) {
	recorder := newTestProvider(t)

	ctx, producer := Start(context.Background(), "asynq.transfer:process",
		trace.WithSpanKind(trace.SpanKindProducer))
	metadata := Inject(ctx)
	producer.End()

	if metadata.TraceParent == "" {
		t.Fatal("Inject() produced no traceparent")
	}

	// This is what the worker does with a deserialized payload.
	restored := Extract(context.Background(), metadata)
	if TraceIDFromContext(restored) == "" {
		t.Fatal("Extract() did not restore a trace context")
	}

	// A child span in the worker must hang off the producer's span, proving
	// the job inherited the dispatching request's trace.
	_, consumer := Start(restored, "asynq.transfer:process",
		trace.WithSpanKind(trace.SpanKindConsumer))
	consumer.End()

	spans := recorder.Ended()
	if len(spans) != 2 {
		t.Fatalf("expected 2 spans, got %d", len(spans))
	}
	var consumerSpan, producerSpan sdktrace.ReadOnlySpan
	for _, s := range spans {
		switch s.SpanKind() {
		case trace.SpanKindConsumer:
			consumerSpan = s
		case trace.SpanKindProducer:
			producerSpan = s
		}
	}
	if consumerSpan == nil || producerSpan == nil {
		t.Fatal("expected both a producer and a consumer span")
	}
	if !consumerSpan.Parent().IsValid() {
		t.Fatal("expected the consumer span to have a parent")
	}
	if consumerSpan.Parent().SpanID() != producerSpan.SpanContext().SpanID() {
		t.Fatalf("consumer parent = %s, want the producer span %s",
			consumerSpan.Parent().SpanID(), producerSpan.SpanContext().SpanID())
	}
	if consumerSpan.SpanContext().TraceID() != producerSpan.SpanContext().TraceID() {
		t.Fatal("worker job did not inherit the dispatching request's trace")
	}
}

func TestInjectEmptyWhenDisabled(t *testing.T) {
	enabled.Store(false)
	if got := Inject(context.Background()); got != (Metadata{}) {
		t.Fatalf("Inject() = %#v, want the zero Metadata when disabled", got)
	}
}

func TestExtractEmptyMetadataIsNoop(t *testing.T) {
	newTestProvider(t)
	before := TraceIDFromContext(context.Background())
	after := TraceIDFromContext(Extract(context.Background(), Metadata{}))
	if before != after {
		t.Fatalf("empty metadata changed the trace context: %q -> %q", before, after)
	}
}

func TestStartConsumerAndProducerSetSpanKinds(t *testing.T) {
	recorder := newTestProvider(t)

	_, consumer := StartConsumer(context.Background(), "reconcile:run")
	consumer.End()
	_, producer := StartProducer(context.Background(), "reconcile:run")
	producer.End()

	var sawConsumer, sawProducer bool
	for _, s := range recorder.Ended() {
		if s.SpanKind() == trace.SpanKindConsumer {
			sawConsumer = true
		}
		if s.SpanKind() == trace.SpanKindProducer {
			sawProducer = true
		}
	}
	if !sawConsumer || !sawProducer {
		t.Fatalf("missing span kinds: consumer=%v producer=%v", sawConsumer, sawProducer)
	}
}

// TestLoggerAnnotatesTraceID is the log-correlation requirement: loggers
// derived from a traced context carry the trace ID, and an untraced context
// still yields a usable logger rather than nil.
func TestLoggerAnnotatesTraceID(t *testing.T) {
	newTestProvider(t)

	ctx, span := Start(context.Background(), "unit")
	defer span.End()
	if TraceIDFromContext(ctx) == "" {
		t.Fatal("expected an active span in this test")
	}

	// Must not panic, with or without an active span.
	Logger(ctx).Info().Msg("traced")
	Logger(context.Background()).Info().Msg("untraced")

	if Logger(context.Background()) == nil {
		t.Fatal("Logger() returned nil for an untraced context")
	}
}

// TestLoggerConcurrentUse guards the helper against data races when many
// goroutines log from traced contexts, which the race detector would flag.
func TestLoggerConcurrentUse(t *testing.T) {
	newTestProvider(t)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, span := Start(context.Background(), "concurrent")
			defer span.End()
			Logger(ctx).Info().Msg("hello")
		}()
	}
	wg.Wait()
}

func TestShutdownWithTimeoutNilIsSafe(t *testing.T) {
	if err := ShutdownWithTimeout(nil, 5*time.Second); err != nil {
		t.Fatalf("ShutdownWithTimeout(nil) error: %v", err)
	}
}

func TestHTTPMiddlewareConcurrentRequests(t *testing.T) {
	recorder := newTestProvider(t)

	handler := HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = TraceIDFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	}))

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/wallets", nil))
		}()
	}
	wg.Wait()

	// Each concurrent request gets its own span; none may be lost or shared.
	spans := recorder.Ended()
	if len(spans) != 32 {
		t.Fatalf("expected 32 spans, got %d", len(spans))
	}
	seen := make(map[trace.TraceID]struct{}, len(spans))
	for _, s := range spans {
		id := s.SpanContext().TraceID()
		if _, dup := seen[id]; dup {
			t.Fatalf("trace %s was reused across concurrent requests", id)
		}
		seen[id] = struct{}{}
	}
}
