package tracing

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type Config struct {
	Enabled          bool
	ExporterEndpoint string
	ServiceName      string
}

type Metadata struct {
	TraceParent string `json:"traceparent,omitempty"`
	TraceState  string `json:"tracestate,omitempty"`
	Baggage     string `json:"baggage,omitempty"`
}

type Shutdown func(context.Context) error

var enabled atomic.Bool

func Init(ctx context.Context, cfg Config) (Shutdown, error) {
	if !cfg.Enabled {
		enabled.Store(false)
		return func(context.Context) error { return nil }, nil
	}

	endpoint := strings.TrimSpace(cfg.ExporterEndpoint)
	if endpoint == "" {
		endpoint = "http://localhost:4318"
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = "http://" + endpoint
	}
	// Validate here rather than letting the exporter discover the problem: the
	// OTLP client defers endpoint parsing to export time, so a typo in
	// OTEL_EXPORTER_ENDPOINT would otherwise start the service with tracing
	// silently dropping every span.
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse otlp exporter endpoint %q: %w", endpoint, err)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("otlp exporter endpoint %q has no host", endpoint)
	}

	options := []otlptracehttp.Option{otlptracehttp.WithEndpointURL(endpoint)}
	if strings.HasPrefix(strings.ToLower(endpoint), "http://") {
		options = append(options, otlptracehttp.WithInsecure())
	}
	exporter, err := otlptracehttp.New(ctx, options...)
	if err != nil {
		return nil, fmt.Errorf("create otlp trace exporter: %w", err)
	}

	serviceName := cfg.ServiceName
	if serviceName == "" {
		serviceName = "fluxa"
	}
	res, err := resource.New(ctx, resource.WithAttributes(attribute.String("service.name", serviceName)))
	if err != nil {
		return nil, fmt.Errorf("create tracing resource: %w", err)
	}
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))
	enabled.Store(true)

	return provider.Shutdown, nil
}

func Enabled() bool {
	return enabled.Load()
}

// HTTPMiddleware starts a server span for every inbound request, honouring an
// upstream traceparent when present so traces stitch together across services.
// The active trace ID is echoed back on X-Trace-ID and attached to the
// context's zerolog logger, which makes trace_id present on every log line
// emitted by the request path. When tracing is disabled the middleware is
// replaced by a pass-through, adding no measurable overhead.
func HTTPMiddleware(next http.Handler) http.Handler {
	if !Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parent := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := otel.Tracer("fluxa/http").Start(
			parent,
			r.Method+" "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
			),
		)
		defer span.End()
		if traceID := TraceIDFromContext(ctx); traceID != "" {
			w.Header().Set("X-Trace-ID", traceID)
			ctx = zerolog.Ctx(ctx).WithContext(
				log.Logger.With().Str("trace_id", traceID).Logger().WithContext(ctx),
			)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func Start(ctx context.Context, name string, options ...trace.SpanStartOption) (context.Context, trace.Span) {
	if !Enabled() {
		return ctx, trace.SpanFromContext(ctx)
	}
	return otel.Tracer("fluxa").Start(ctx, name, options...)
}

func StartConsumer(ctx context.Context, name string) (context.Context, trace.Span) {
	return Start(ctx, "asynq."+name, trace.WithSpanKind(trace.SpanKindConsumer))
}

func StartProducer(ctx context.Context, name string) (context.Context, trace.Span) {
	return Start(ctx, "asynq."+name, trace.WithSpanKind(trace.SpanKindProducer))
}

func TraceIDFromContext(ctx context.Context) string {
	spanContext := trace.SpanContextFromContext(ctx)
	if !spanContext.IsValid() {
		return ""
	}
	return spanContext.TraceID().String()
}

func Inject(ctx context.Context) Metadata {
	if !Enabled() {
		return Metadata{}
	}
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return Metadata{
		TraceParent: carrier.Get("traceparent"),
		TraceState:  carrier.Get("tracestate"),
		Baggage:     carrier.Get("baggage"),
	}
}

func Extract(ctx context.Context, metadata Metadata) context.Context {
	if !Enabled() || (metadata.TraceParent == "" && metadata.TraceState == "" && metadata.Baggage == "") {
		return ctx
	}
	carrier := propagation.MapCarrier{
		"traceparent": metadata.TraceParent,
		"tracestate":  metadata.TraceState,
		"baggage":     metadata.Baggage,
	}
	return otel.GetTextMapPropagator().Extract(ctx, carrier)
}

// Logger returns a zerolog logger annotated with the trace ID carried by ctx.
// It is a no-op passthrough when ctx has no active span, so call sites can call
// it unconditionally. Worker entry points use it so a job's log lines carry the
// same trace_id as the API request that dispatched the job.
func Logger(ctx context.Context) *zerolog.Logger {
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		return zerolog.Ctx(ctx)
	}
	logger := zerolog.Ctx(ctx).With().Str("trace_id", traceID).Logger()
	return &logger
}

func ShutdownWithTimeout(shutdown Shutdown, timeout time.Duration) error {
	if shutdown == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return shutdown(ctx)
}
