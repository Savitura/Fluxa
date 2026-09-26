package queue

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/hibiken/asynq"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

// otlpSink is a minimal OTLP/HTTP collector. It records that spans were
// exported, which lets the test assert the "traces reach the configured
// endpoint" requirement without standing up a real collector.
type otlpSink struct {
	mu       sync.Mutex
	requests int
	payloads [][]byte
}

func (o *otlpSink) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		if r.Body != nil {
			body, _ = io.ReadAll(r.Body)
		}
		o.mu.Lock()
		o.requests++
		o.payloads = append(o.payloads, body)
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

// enableTracing boots the real tracing stack with tracing enabled and the OTLP
// exporter pointed at a local test collector. It returns the collector and the
// shutdown func, which callers must invoke to flush the batcher.
func enableTracing(t *testing.T) (*otlpSink, tracing.Shutdown) {
	t.Helper()
	sink := &otlpSink{}
	ts := httptest.NewServer(sink.handler())
	t.Cleanup(ts.Close)

	shutdown, err := tracing.Init(context.Background(), tracing.Config{
		Enabled:          true,
		ExporterEndpoint: ts.URL,
		ServiceName:      "fluxa-test",
	})
	if err != nil {
		t.Fatalf("tracing.Init() error: %v", err)
	}
	t.Cleanup(func() {
		if err := tracing.ShutdownWithTimeout(shutdown, 10*time.Second); err != nil {
			t.Errorf("tracing shutdown: %v", err)
		}
	})
	if !tracing.Enabled() {
		t.Fatal("expected tracing to be enabled after Init")
	}
	return sink, shutdown
}

// roundTrip serialises a payload the way Enqueue* does and then rebuilds the
// worker-side context the way a task handler does.
func roundTrip(t *testing.T, payload any) (*asynq.Task, context.Context) {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	task := asynq.NewTask(TypeProcessTransfer, raw)
	return task, ContextFromTask(context.Background(), task)
}

// TestTraceContextIsInjectedIntoTaskPayload is the enqueue half of the
// propagation contract: the dispatching request's traceparent must travel with
// the job.
func TestTraceContextIsInjectedIntoTaskPayload(t *testing.T) {
	enableTracing(t)

	ctx, span := otel.Tracer("api").Start(context.Background(), "POST /v1/transfers",
		trace.WithSpanKind(trace.SpanKindServer))
	wantTraceID := span.SpanContext().TraceID().String()
	injected := traceContext(ctx)
	span.End()

	if injected == nil {
		t.Fatal("expected a trace context to be injected into the payload")
	}
	if injected.TraceParent == "" {
		t.Fatal("expected a traceparent in the injected context")
	}

	_, restored := roundTrip(t, ProcessTransferPayload{TransactionID: "tx-1", Trace: injected})

	got := trace.SpanContextFromContext(restored)
	if !got.IsValid() {
		t.Fatal("worker context has no valid span context")
	}
	if got.TraceID().String() != wantTraceID {
		t.Fatalf("trace ID = %s, want the dispatching request's %s", got.TraceID(), wantTraceID)
	}
}

// TestWorkerSpanInheritsEnqueueTrace is the acceptance criterion: worker jobs
// inherit trace context from the dispatching API request, so the worker's span
// is a child of the enqueue span within one trace.
func TestWorkerSpanInheritsEnqueueTrace(t *testing.T) {
	enableTracing(t)

	ctx, enqueue := otel.Tracer("api").Start(context.Background(), "enqueue",
		trace.WithSpanKind(trace.SpanKindProducer))
	enqueueSpanID := enqueue.SpanContext().SpanID()
	enqueueTraceID := enqueue.SpanContext().TraceID().String()
	injected := traceContext(ctx)
	enqueue.End()

	_, restored := roundTrip(t, ProcessTransferPayload{TransactionID: "tx-1", Trace: injected})

	_, worker := otel.Tracer("worker").Start(restored, "consume",
		trace.WithSpanKind(trace.SpanKindConsumer))
	workerContext := worker.SpanContext()
	worker.End()

	if workerContext.SpanID() == enqueueSpanID {
		t.Fatal("the worker must create a new span, not reuse the enqueue span")
	}
	if workerContext.TraceID().String() != enqueueTraceID {
		t.Fatalf("worker trace = %s, want the dispatching trace %s",
			workerContext.TraceID(), enqueueTraceID)
	}
}

func TestContextFromTaskWithNoTraceIsPassthrough(t *testing.T) {
	enableTracing(t)
	_, restored := roundTrip(t, ProcessTransferPayload{TransactionID: "tx-1"})
	if trace.SpanContextFromContext(restored).IsValid() {
		t.Fatal("expected no span context when the payload carries no trace metadata")
	}
}

func TestContextFromTaskNilTask(t *testing.T) {
	if ctx := ContextFromTask(context.Background(), nil); ctx == nil {
		t.Fatal("ContextFromTask(nil) must not return a nil context")
	}
}

func TestContextFromTaskMalformedPayload(t *testing.T) {
	// A payload that is not valid JSON must not panic the worker.
	task := asynq.NewTask(TypeProcessTransfer, []byte("not json"))
	if ctx := ContextFromTask(context.Background(), task); ctx == nil {
		t.Fatal("expected a usable context for a malformed payload")
	}
}

// TestEveryTaskPayloadCarriesTraceContext documents that all job payloads
// gained a trace field, so no worker entry point is left untraced.
func TestEveryTaskPayloadCarriesTraceContext(t *testing.T) {
	enableTracing(t)

	ctx, span := otel.Tracer("api").Start(context.Background(), "enqueue")
	injected := traceContext(ctx)
	span.End()

	cases := []struct {
		name    string
		payload any
		idField string
		idValue string
	}{
		{"transfer:process", ProcessTransferPayload{TransactionID: "tx-1", Trace: injected}, "transaction_id", "tx-1"},
		{"transfer:confirm", ConfirmTxPayload{TransactionID: "tx-1", TxHash: "hash", Trace: injected}, "tx_hash", "hash"},
		{"indexer:sync", SyncLedgerPayload{WalletID: "w-1", Cursor: "c", Trace: injected}, "wallet_id", "w-1"},
		{"webhook:deliver", WebhookDeliverPayload{DeliveryID: "d-1", Trace: injected}, "delivery_id", "d-1"},
		{"webhook:tenant-deliver", WebhookDeliverPayload{DeliveryID: "d-1", TenantID: "t-1", Config: true, Trace: injected}, "tenant_id", "t-1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task, restored := roundTrip(t, tc.payload)
			if task.Type() == "" {
				t.Fatal("expected a task type")
			}
			if !trace.SpanContextFromContext(restored).IsValid() {
				t.Fatal("expected the worker context to carry a trace")
			}

			raw, err := json.Marshal(tc.payload)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got, _ := body[tc.idField].(string); got != tc.idValue {
				t.Fatalf("%s = %q, want %q", tc.idField, got, tc.idValue)
			}
			traceBody, ok := body["trace"].(map[string]any)
			if !ok {
				t.Fatalf("expected a trace object in the payload, body=%v", body)
			}
			if got, _ := traceBody["traceparent"].(string); got == "" {
				t.Fatalf("expected traceparent in the serialised payload, body=%v", traceBody)
			}
		})
	}
}

// TestTraceFieldsOmittedWhenEmpty keeps payloads byte-compatible with the
// pre-tracing format when tracing is off, so in-flight jobs are unaffected.
func TestTraceFieldsOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(ProcessTransferPayload{TransactionID: "tx-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `{"transaction_id":"tx-1"}` {
		t.Fatalf("payload = %s, want no trace field when tracing is disabled", raw)
	}

	raw, err = json.Marshal(WebhookDeliverPayload{DeliveryID: "d-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `{"delivery_id":"d-1"}` {
		t.Fatalf("payload = %s, want no tenant_id/config/trace when unset", raw)
	}
}

// TestTracesAreExportedToConfiguredEndpoint closes the loop on the exporter
// requirement: spans must reach whatever OTEL_EXPORTER_ENDPOINT points at.
func TestTracesAreExportedToConfiguredEndpoint(t *testing.T) {
	sink, shutdown := enableTracing(t)

	_, span := otel.Tracer("api").Start(context.Background(), "exported")
	span.End()

	// Shutdown flushes the batcher, so this does not depend on the exporter's
	// periodic schedule.
	if err := tracing.ShutdownWithTimeout(shutdown, 10*time.Second); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	if sink.count() == 0 {
		t.Fatal("no spans were exported to the configured OTLP endpoint")
	}
}
