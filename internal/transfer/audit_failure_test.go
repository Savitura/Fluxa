package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type failingAudit struct{ err error }

func (a failingAudit) Record(context.Context, AuditEntry) error { return a.err }

// #394: a failed audit insert must be visible — logged at error level with the
// action and resource, and counted — instead of being discarded.
func TestRecordAuditFailureIsLoggedAndCounted(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })

	s := &service{audit: failingAudit{err: errors.New("audit db down")}}
	before := AuditWriteFailures()

	s.recordAudit(context.Background(), "user-1", "transfer.cancel", "txn-1")

	if got := AuditWriteFailures() - before; got != 1 {
		t.Fatalf("AuditWriteFailures delta = %d, want 1", got)
	}
	var entry map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("expected one JSON log line, got %q: %v", buf.String(), err)
	}
	want := map[string]interface{}{
		"level":          "error",
		"audit_action":   "transfer.cancel",
		"audit_resource": "txn-1",
		"audit_actor":    "user-1",
		"error":          "audit db down",
	}
	for k, v := range want {
		if entry[k] != v {
			t.Errorf("log field %s = %v, want %v", k, entry[k], v)
		}
	}
}

func TestRecordAuditSuccessIsSilent(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Logger
	log.Logger = zerolog.New(&buf)
	t.Cleanup(func() { log.Logger = prev })

	s := &service{audit: failingAudit{}}
	before := AuditWriteFailures()

	s.recordAudit(context.Background(), "user-1", "transfer.created", "txn-1")

	if AuditWriteFailures() != before {
		t.Fatal("successful audit write must not be counted as a failure")
	}
	if buf.Len() != 0 {
		t.Fatalf("successful audit write logged %q", buf.String())
	}
}
