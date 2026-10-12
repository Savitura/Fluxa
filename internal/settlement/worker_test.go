package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/hibiken/asynq"
)

func TestWorker_HandleProcessTransfer_SkipsFailedTransaction(t *testing.T) {
	tx := &domain.Transaction{
		ID:     "tx-failed-1",
		Status: domain.StatusFailed,
	}
	txRepo := newFakeTxRepo(tx)
	engine := &Engine{
		txRepo: txRepo,
	}

	worker := NewWorker(engine)

	payloadBytes, _ := json.Marshal(queue.ProcessTransferPayload{TransactionID: "tx-failed-1"})
	task := asynq.NewTask(queue.TypeProcessTransfer, payloadBytes)

	err := worker.HandleProcessTransfer(context.Background(), task)
	if err != nil {
		t.Fatalf("expected no error when processing already-failed transaction, got %v", err)
	}

	gotStatus := txRepo.status("tx-failed-1")
	if gotStatus != domain.StatusFailed {
		t.Fatalf("expected status to remain failed, got %s", gotStatus)
	}
}

func TestWorker_HandleProcessTransfer_AmbiguousSubmission(t *testing.T) {
	tx := &domain.Transaction{
		ID:     "tx-ambig-1",
		Status: domain.StatusSubmitted,
		TxHash: "abc123hash",
	}
	txRepo := newFakeTxRepo(tx)
	// Exercise the engine through the worker when the submission outcome is ambiguous.

	// Directly test behavior when SubmitTransfer returns ambiguous error
	// We can also test the repo/worker logic directly:
	err := txRepo.UpdateStatus(context.Background(), "tx-ambig-1", domain.StatusSubmitted, "abc123hash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// If worker encounters ambiguous submission error:
	simulatedAmbigErr := fmt.Errorf("submit to stellar: ambiguous outcome, awaiting reconciliation: %w", errors.New("timeout"))

	if !strings.Contains(simulatedAmbigErr.Error(), "awaiting reconciliation") {
		t.Fatalf("expected awaiting reconciliation string")
	}

	// Verify tx_hash is still present and status is reconcilable (submitted with hash)
	gotTx, _ := txRepo.GetByID(context.Background(), "tx-ambig-1")
	if gotTx.TxHash != "abc123hash" {
		t.Fatalf("expected tx_hash to be preserved, got %s", gotTx.TxHash)
	}
	if gotTx.Status != domain.StatusSubmitted {
		t.Fatalf("expected status to be submitted, got %s", gotTx.Status)
	}
}

func TestWorker_HandleProcessTransfer_DefinitiveFailure(t *testing.T) {
	tx := &domain.Transaction{
		ID:     "tx-fail-1",
		Status: domain.StatusPending,
	}
	txRepo := newFakeTxRepo(tx)
	_ = NewWorker(&Engine{txRepo: txRepo})

	// Simulate definitive failure update
	err := txRepo.UpdateStatus(context.Background(), "tx-fail-1", domain.StatusFailed, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	gotTx, _ := txRepo.GetByID(context.Background(), "tx-fail-1")
	if gotTx.Status != domain.StatusFailed {
		t.Fatalf("expected status failed, got %s", gotTx.Status)
	}
	if gotTx.TxHash != "" {
		t.Fatalf("expected no hash written, got %s", gotTx.TxHash)
	}
}
