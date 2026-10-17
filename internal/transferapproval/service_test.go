package transferapproval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/shopspring/decimal"
)

type repoFake struct {
	created     *domain.TransferApprovalRequest
	expireBatch [][]domain.ExpiredTransferApproval
	expireCalls int
	expireErr   error
}

func (f *repoFake) GetPolicy(context.Context, string, string) (*domain.TransferApprovalPolicy, error) {
	return nil, nil
}
func (f *repoFake) PutPolicy(context.Context, *domain.TransferApprovalPolicy) error { return nil }
func (f *repoFake) CreateRequest(_ context.Context, r *domain.TransferApprovalRequest) error {
	f.created = r
	return nil
}
func (f *repoFake) ListRequests(context.Context, string, string, int, int) ([]*domain.TransferApprovalRequest, int, error) {
	return nil, 0, nil
}
func (f *repoFake) CastVote(context.Context, string, string, string, string, string) (*domain.TransferApprovalRequest, bool, error) {
	return nil, false, nil
}
func (f *repoFake) ExpirePending(context.Context, int) ([]domain.ExpiredTransferApproval, error) {
	if f.expireErr != nil {
		return nil, f.expireErr
	}
	if f.expireCalls >= len(f.expireBatch) {
		return nil, nil
	}
	batch := f.expireBatch[f.expireCalls]
	f.expireCalls++
	return batch, nil
}

type dispatched struct {
	tenantID string
	event    domain.EventType
	payload  map[string]interface{}
}

type webhookFake struct{ calls []dispatched }

func (w *webhookFake) Dispatch(ctx context.Context, event domain.EventType, payload interface{}) error {
	w.calls = append(w.calls, dispatched{tenant.IDFromContext(ctx), event, payload.(map[string]interface{})})
	return nil
}

func tenantTx() *domain.Transaction {
	tid := "tenant-1"
	return &domain.Transaction{ID: "txn-1", TenantID: &tid, Asset: "USDC", Amount: decimal.NewFromInt(500)}
}

func TestCreateRequestRecordsCreator(t *testing.T) {
	policy := &domain.TransferApprovalPolicy{RequiredApprovals: 1, ExpiresAfter: time.Hour}
	cases := []struct {
		name    string
		creator domain.TransferApprovalCreator
	}{
		{"dashboard user", domain.TransferApprovalCreator{UserID: "user-1"}},
		{"api key", domain.TransferApprovalCreator{APIKeyID: "key-1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &repoFake{}
			svc := NewService(repo, nil)

			if err := svc.CreateRequest(context.Background(), tenantTx(), tc.creator, policy); err != nil {
				t.Fatalf("CreateRequest: %v", err)
			}
			if repo.created.CreatorID != tc.creator.UserID || repo.created.CreatorAPIKeyID != tc.creator.APIKeyID {
				t.Fatalf("recorded creator = (%q, %q), want (%q, %q)",
					repo.created.CreatorID, repo.created.CreatorAPIKeyID, tc.creator.UserID, tc.creator.APIKeyID)
			}
		})
	}
}

func fullBatch(n int) []domain.ExpiredTransferApproval {
	batch := make([]domain.ExpiredTransferApproval, n)
	for i := range batch {
		batch[i] = domain.ExpiredTransferApproval{RequestID: "r", TenantID: "t", TransactionID: "x", TransactionFailed: true}
	}
	return batch
}

// #392: the sweep fails held transfers and tells the tenant via transfer.failed.
func TestExpireDueDispatchesTransferFailedPerTenant(t *testing.T) {
	repo := &repoFake{expireBatch: [][]domain.ExpiredTransferApproval{{
		{RequestID: "req-1", TenantID: "tenant-a", TransactionID: "txn-1", Asset: "USDC", Amount: decimal.RequireFromString("25.5"), TransactionFailed: true},
		// Transaction already left approval_pending: request expired, no webhook.
		{RequestID: "req-2", TenantID: "tenant-b", TransactionID: "txn-2", Asset: "USDC", Amount: decimal.NewFromInt(10)},
	}}}
	hooks := &webhookFake{}
	svc := NewService(repo, nil).WithWebhooks(hooks)

	report, err := svc.ExpireDue(context.Background())
	if err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	if report.Expired != 2 || report.TransactionsFailed != 1 {
		t.Fatalf("report = %+v, want 2 expired, 1 failed", report)
	}
	if len(hooks.calls) != 1 {
		t.Fatalf("dispatched %d webhooks, want 1", len(hooks.calls))
	}
	call := hooks.calls[0]
	if call.tenantID != "tenant-a" || call.event != domain.EventTransferFailed {
		t.Fatalf("dispatched (%q, %q), want (tenant-a, transfer.failed)", call.tenantID, call.event)
	}
	if call.payload["transaction_id"] != "txn-1" || call.payload["failure_reason"] != "approval expired" ||
		call.payload["amount"] != "25.5" || call.payload["approval_request_id"] != "req-1" {
		t.Fatalf("unexpected payload %v", call.payload)
	}
}

func TestExpireDueDrainsFullBatches(t *testing.T) {
	repo := &repoFake{expireBatch: [][]domain.ExpiredTransferApproval{fullBatch(expiryBatchSize), fullBatch(3)}}
	svc := NewService(repo, nil)

	report, err := svc.ExpireDue(context.Background())
	if err != nil {
		t.Fatalf("ExpireDue: %v", err)
	}
	if repo.expireCalls != 2 || report.Expired != expiryBatchSize+3 {
		t.Fatalf("calls=%d expired=%d, want 2 calls and %d expired", repo.expireCalls, report.Expired, expiryBatchSize+3)
	}
}

func TestExpireDuePropagatesRepoError(t *testing.T) {
	svc := NewService(&repoFake{expireErr: errors.New("db down")}, nil)
	if _, err := svc.ExpireDue(context.Background()); err == nil {
		t.Fatal("expected repo error to be returned so asynq retries the task")
	}
}
