package queue

import (
	"context"
	"encoding/json"

	"github.com/fluxa/fluxa/internal/tracing"
	"github.com/hibiken/asynq"
)

const (
	TypeProcessTransfer      = "transfer:process"
	TypeConfirmTx            = "transfer:confirm"
	TypeSyncLedger           = "indexer:sync"
	TypeReconcile            = "reconcile:run"
	TypeBalanceReconcile     = "reconcile:balance"
	TypeWebhookDeliver       = "webhook:deliver"
	TypeTenantWebhookDeliver = "webhook:tenant-deliver"
	TypeRunSchedules         = "schedule:run"
	TypeTreasurySweep        = "treasury:sweep"
)

type TraceContext struct {
	TraceParent string `json:"traceparent,omitempty"`
	TraceState  string `json:"tracestate,omitempty"`
	Baggage     string `json:"baggage,omitempty"`
}

// The Trace fields are pointers so that `omitempty` actually elides them.
// omitempty has no effect on struct values, which would append a noisy
// `"trace":{}` to every job payload even when tracing is disabled; keeping them
// optional means payloads stay byte-identical to the pre-tracing format and
// jobs enqueued by an older build still decode cleanly.
type ProcessTransferPayload struct {
	TransactionID string        `json:"transaction_id"`
	Trace         *TraceContext `json:"trace,omitempty"`
}

type ConfirmTxPayload struct {
	TransactionID string        `json:"transaction_id"`
	TxHash        string        `json:"tx_hash"`
	Trace         *TraceContext `json:"trace,omitempty"`
}

type SyncLedgerPayload struct {
	WalletID string        `json:"wallet_id"`
	Cursor   string        `json:"cursor"`
	Trace    *TraceContext `json:"trace,omitempty"`
}

type WebhookDeliverPayload struct {
	DeliveryID string        `json:"delivery_id"`
	TenantID   string        `json:"tenant_id,omitempty"`
	Config     bool          `json:"config,omitempty"`
	Trace      *TraceContext `json:"trace,omitempty"`
}

// traceContext captures the active trace context for propagation to a job. It
// returns nil when tracing is disabled, which keeps the payload unchanged.
func traceContext(ctx context.Context) *TraceContext {
	metadata := tracing.Inject(ctx)
	if metadata.TraceParent == "" && metadata.TraceState == "" && metadata.Baggage == "" {
		return nil
	}
	return &TraceContext{
		TraceParent: metadata.TraceParent,
		TraceState:  metadata.TraceState,
		Baggage:     metadata.Baggage,
	}
}

// ContextFromTask restores the dispatching request's trace context inside a
// worker so the job's spans become children of the originating request's trace.
// It is a no-op for payloads with no trace metadata, which is the case for
// every job enqueued while tracing was disabled.
func ContextFromTask(ctx context.Context, task *asynq.Task) context.Context {
	if task == nil {
		return ctx
	}
	var payload struct {
		Trace *TraceContext `json:"trace"`
	}
	if err := json.Unmarshal(task.Payload(), &payload); err != nil || payload.Trace == nil {
		return ctx
	}
	return tracing.Extract(ctx, tracing.Metadata{
		TraceParent: payload.Trace.TraceParent,
		TraceState:  payload.Trace.TraceState,
		Baggage:     payload.Trace.Baggage,
	})
}
