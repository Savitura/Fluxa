package settlement

import (
	"context"
	"testing"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/hibiken/asynq"
	"github.com/shopspring/decimal"
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
