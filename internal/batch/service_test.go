package batch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/tenant"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// fakeBatchRepo implements Repository over in-memory maps. SyncAggregates
// mirrors the production recompute-from-children behaviour under the same
// rules: recompute, persist, report the terminal transition.
type fakeBatchRepo struct {
	batches map[string]*domain.BatchTransfer
	txRepo  *fakeTxRepo
}

func newFakeBatchRepo(txRepo *fakeTxRepo) *fakeBatchRepo {
	return &fakeBatchRepo{batches: make(map[string]*domain.BatchTransfer), txRepo: txRepo}
}

func (f *fakeBatchRepo) Create(ctx context.Context, b *domain.BatchTransfer) error {
	if tID := tenant.IDFromContext(ctx); tID != "" {
		b.TenantID = &tID
	}
	f.batches[b.ID] = b
	return nil
}

func (f *fakeBatchRepo) GetByID(_ context.Context, id string) (*domain.BatchTransfer, error) {
	b, ok := f.batches[id]
	if !ok {
		return nil, domain.ErrBatchNotFound
	}
	return b, nil
}

func (f *fakeBatchRepo) SyncAggregates(_ context.Context, id string) (*domain.BatchTransfer, bool, error) {
	b, ok := f.batches[id]
	if !ok {
		return nil, false, domain.ErrBatchNotFound
	}
	status, succeeded, failed, _ := domain.SummarizeBatch(f.txRepo.byBatch[id])
	transitioned := !domain.IsTerminalBatchStatus(b.Status) && domain.IsTerminalBatchStatus(status)
	if status != b.Status || succeeded != b.SucceededCount || failed != b.FailedCount {
		b.Status = status
		b.SucceededCount = succeeded
		b.FailedCount = failed
		if transitioned && b.SettledAt == nil {
			now := time.Now().UTC()
			b.SettledAt = &now
		}
		b.UpdatedAt = time.Now().UTC()
	}
	return b, transitioned, nil
}

// fakeTxRepo implements transfer.Repository, storing transactions by batch.
type fakeTxRepo struct {
	byBatch map[string][]*domain.Transaction
}

func newFakeTxRepo() *fakeTxRepo {
	return &fakeTxRepo{byBatch: make(map[string][]*domain.Transaction)}
}

func (f *fakeTxRepo) Create(ctx context.Context, tx *domain.Transaction) error {
	if tenantID := tenant.IDFromContext(ctx); tenantID != "" {
		tx.TenantID = &tenantID
	}
	if tx.BatchID != nil {
		f.byBatch[*tx.BatchID] = append(f.byBatch[*tx.BatchID], tx)
	}
	return nil
}

func (f *fakeTxRepo) CreateWithMonthlyLimit(_ context.Context, tx *domain.Transaction, _ string, _ int, _ time.Month, _ int) error {
	return f.Create(context.TODO(), tx)
}

func (f *fakeTxRepo) GetByID(_ context.Context, id string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}

func (f *fakeTxRepo) ClaimForSubmission(_ context.Context, id string) error {
	return nil
}

func (f *fakeTxRepo) UpdateStatus(_ context.Context, id string, status domain.TransactionStatus, txHash string) error {
	return nil
}

func (f *fakeTxRepo) UpsertByTxHash(_ context.Context, tx *domain.Transaction) error {
	return nil
}

func (f *fakeTxRepo) GetByIdempotencyKey(ctx context.Context, orgID, idempotencyKey string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}

func (f *fakeTxRepo) ListByWallet(_ context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	return nil, nil
}

func (f *fakeTxRepo) ListByBatch(ctx context.Context, batchID string) ([]*domain.Transaction, error) {
	tenantID := tenant.IDFromContext(ctx)
	var txs []*domain.Transaction
	for _, tx := range f.byBatch[batchID] {
		if tenantID == "" || (tx.TenantID != nil && *tx.TenantID == tenantID) {
			txs = append(txs, tx)
		}
	}
	return txs, nil
}

func (f *fakeTxRepo) ExistsByTxHash(_ context.Context, txHash string) (bool, error) {
	return false, nil
}

func (f *fakeTxRepo) CountMonthlyTransfersByTenant(ctx context.Context, tenantID string, year int, month time.Month) (int, error) {
	return 0, nil
}

// fakeTransferSvc implements transfer.Service with the two batch entry points:
// PrepareBatchTransfer rejects items listed in failOn (the whole-batch reject
// path), and PersistBatchTransfer returns domain.ErrTransferBlockedSanctions
// for items listed in blockOn (the per-item failed-row path).
type fakeTransferSvc struct {
	txRepo  *fakeTxRepo
	failOn  map[string]bool
	blockOn map[string]bool
}

func (f *fakeTransferSvc) InitiateTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal) (*domain.Transaction, error) {
	return f.InitiateBatchTransfer(ctx, fromID, toID, asset, amount, "", "")
}

func (f *fakeTransferSvc) InitiateTransferIdempotent(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, idempotencyKey string) (*domain.Transaction, error) {
	return f.InitiateTransfer(ctx, fromID, toID, asset, amount)
}

func (f *fakeTransferSvc) InitiateBatchTransfer(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference string) (*domain.Transaction, error) {
	if f.failOn[toID] {
		return nil, errors.New("destination account does not exist")
	}

	tx := &domain.Transaction{
		ID:         uuid.New().String(),
		Type:       domain.TypeTransfer,
		Status:     domain.StatusPending,
		FromWallet: fromID,
		ToWallet:   toID,
		Asset:      asset,
		Amount:     amount,
		Reference:  reference,
		CreatedAt:  time.Now().UTC(),
	}
	if batchID != "" {
		tx.BatchID = &batchID
	}
	_ = f.txRepo.Create(ctx, tx)
	return tx, nil
}

func (f *fakeTransferSvc) PrepareBatchTransfer(_ context.Context, params transfer.TransferParams) (*transfer.PreparedTransfer, error) {
	if f.failOn[params.ToID] {
		return nil, errors.New("destination account does not exist")
	}
	tx := &domain.Transaction{
		ID:         uuid.New().String(),
		Type:       domain.TypeTransfer,
		Status:     domain.StatusPending,
		FromWallet: params.FromID,
		ToWallet:   params.ToID,
		Asset:      params.Asset,
		Amount:     params.Amount,
		Reference:  params.Reference,
		CreatedAt:  time.Now().UTC(),
	}
	if params.BatchID != "" {
		batchID := params.BatchID
		tx.BatchID = &batchID
	}
	return &transfer.PreparedTransfer{Tx: tx}, nil
}

func (f *fakeTransferSvc) PersistBatchTransfer(ctx context.Context, p *transfer.PreparedTransfer) (*domain.Transaction, error) {
	if f.blockOn[p.Tx.ToWallet] {
		return nil, domain.ErrTransferBlockedSanctions
	}
	if err := f.txRepo.Create(ctx, p.Tx); err != nil {
		return nil, err
	}
	return p.Tx, nil
}

func (f *fakeTransferSvc) WithScreener(_ transfer.Screener) transfer.Service {
	return f
}

func (f *fakeTransferSvc) WithStellarClient(_ stellar.Client) transfer.Service {
	return f
}

func (f *fakeTransferSvc) GetTransaction(_ context.Context, id string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}

func (f *fakeTransferSvc) ListTransactions(_ context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	return nil, nil
}

func (f *fakeTransferSvc) ForceSettleTransfer(_ context.Context, _, _ string) (*domain.Transaction, error) {
	return &domain.Transaction{ID: "tx-1"}, nil
}

func (f *fakeTransferSvc) ReconcileWallet(_ context.Context, _, _ string) (*transfer.ReconcileResult, error) {
	return &transfer.ReconcileResult{}, nil
}

func (f *fakeTransferSvc) WithAuditLogger(_ transfer.AuditLogger) transfer.Service {
	return f
}

// fakeWallets serves the pre-validation existence pass.
type fakeWallets struct {
	ids map[string]bool
}

func (f *fakeWallets) GetByID(_ context.Context, id string) (*domain.Wallet, error) {
	if f.ids[id] {
		return &domain.Wallet{ID: id}, nil
	}
	return nil, errors.New("wallet not found")
}

// fakeQueue records the post-commit settlement enqueue.
type fakeQueue struct {
	enqueued [][2]string
	failOn   map[string]bool
}

func (f *fakeQueue) EnqueueBatchTransfer(_ context.Context, txID, batchID string) error {
	if f.failOn[txID] {
		return errors.New("redis unavailable")
	}
	f.enqueued = append(f.enqueued, [2]string{txID, batchID})
	return nil
}

// fakeDispatcher captures batch-level webhook dispatches.
type fakeDispatcher struct {
	payloads []map[string]interface{}
}

func (f *fakeDispatcher) Dispatch(_ context.Context, eventType domain.EventType, payload interface{}) error {
	f.payloads = append(f.payloads, map[string]interface{}{"event": string(eventType), "payload": payload.(map[string]interface{})})
	return nil
}

func (f *fakeDispatcher) events() []string {
	var out []string
	for _, p := range f.payloads {
		out = append(out, p["event"].(string))
	}
	return out
}

type fixture struct {
	batchRepo   *fakeBatchRepo
	txRepo      *fakeTxRepo
	transferSvc *fakeTransferSvc
	queue       *fakeQueue
	wallets     *fakeWallets
	svc         Service
}

func newFixture() *fixture {
	return newFixtureWith(nil, nil)
}

func newFixtureWith(failOn, blockOn map[string]bool) *fixture {
	txRepo := newFakeTxRepo()
	batchRepo := newFakeBatchRepo(txRepo)
	transferSvc := &fakeTransferSvc{txRepo: txRepo, failOn: failOn, blockOn: blockOn}
	queue := &fakeQueue{}
	wallets := &fakeWallets{ids: map[string]bool{"source-wallet": true}}
	for i := 0; i < 100; i++ {
		wallets.ids[fmt.Sprintf("wallet-%d", i)] = true
	}
	svc := NewService(batchRepo, txRepo, transferSvc,
		WithWalletLookup(wallets),
		WithQueue(queue),
		WithTxRunner(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }),
	)
	return &fixture{batchRepo: batchRepo, txRepo: txRepo, transferSvc: transferSvc, queue: queue, wallets: wallets, svc: svc}
}

func makeItems(n int) []Item {
	items := make([]Item, n)
	for i := 0; i < n; i++ {
		items[i] = Item{
			ToWalletID: fmt.Sprintf("wallet-%d", i),
			Asset:      "XLM",
			Amount:     decimal.NewFromInt(10),
			Reference:  fmt.Sprintf("ref-%d", i),
		}
	}
	return items
}

func TestCreateBatch_LinksFiveTransactionsToBatch(t *testing.T) {
	f := newFixture()

	items := makeItems(5)
	result, err := f.svc.CreateBatch(context.Background(), "source-wallet", items)
	if err != nil {
		t.Fatalf("CreateBatch() error: %v", err)
	}
	if result.Batch.TotalCount != 5 {
		t.Fatalf("TotalCount = %d, want 5", result.Batch.TotalCount)
	}
	if result.Batch.Status != domain.BatchStatusPending {
		t.Fatalf("fresh batch status = %s, want %s", result.Batch.Status, domain.BatchStatusPending)
	}
	if len(result.Transactions) != 5 {
		t.Fatalf("got %d transactions, want 5", len(result.Transactions))
	}
	for _, tx := range result.Transactions {
		if tx.BatchID == nil || *tx.BatchID != result.Batch.ID {
			t.Fatalf("transaction %s not linked to batch %s", tx.ID, result.Batch.ID)
		}
		if tx.Status != domain.StatusPending {
			t.Fatalf("fresh item %s status = %s, want %s (settlement runs later)", tx.ID, tx.Status, domain.StatusPending)
		}
	}
	if len(f.queue.enqueued) != 5 {
		t.Fatalf("enqueued %d settlement jobs, want 5", len(f.queue.enqueued))
	}
	for _, job := range f.queue.enqueued {
		if job[1] != result.Batch.ID {
			t.Fatalf("enqueue batch id = %q, want %q", job[1], result.Batch.ID)
		}
	}
}

// A prepare failure on any item rejects the whole batch: nothing is
// persisted, nothing is enqueued.
func TestCreateBatch_PrepareFailureRejectsWholeBatch(t *testing.T) {
	f := newFixtureWith(map[string]bool{"wallet-2": true}, nil)

	items := makeItems(5)
	if _, err := f.svc.CreateBatch(context.Background(), "source-wallet", items); err == nil {
		t.Fatal("CreateBatch() error = nil, want prepare failure")
	} else if !strings.Contains(err.Error(), "destination account does not exist") {
		t.Fatalf("CreateBatch() error = %v, want underlying prepare error", err)
	}
	if len(f.batchRepo.batches) != 0 {
		t.Fatalf("persisted %d batches, want 0", len(f.batchRepo.batches))
	}
	if len(f.txRepo.byBatch) != 0 {
		t.Fatalf("persisted %d batch groups, want 0", len(f.txRepo.byBatch))
	}
	if len(f.queue.enqueued) != 0 {
		t.Fatalf("enqueued %d jobs, want 0", len(f.queue.enqueued))
	}
}

// A sanctions-blocked recipient becomes one failed item; its siblings still
// persist and the blocked row is not enqueued for settlement.
func TestCreateBatch_BlockedItemFailsAloneAndIsNotEnqueued(t *testing.T) {
	f := newFixtureWith(nil, map[string]bool{"wallet-2": true})

	items := makeItems(5)
	created, err := f.svc.CreateBatch(context.Background(), "source-wallet", items)
	if err != nil {
		t.Fatalf("CreateBatch() error: %v", err)
	}
	if len(created.Transactions) != 5 {
		t.Fatalf("got %d transactions, want 5", len(created.Transactions))
	}
	if len(f.queue.enqueued) != 4 {
		t.Fatalf("enqueued %d jobs, want 4 (blocked row excluded)", len(f.queue.enqueued))
	}

	result, err := f.svc.GetBatch(context.Background(), created.Batch.ID)
	if err != nil {
		t.Fatalf("GetBatch() error: %v", err)
	}
	if result.Batch.Status != domain.BatchStatusProcessing {
		t.Fatalf("status = %s, want %s (1 failed, 4 still settling)", result.Batch.Status, domain.BatchStatusProcessing)
	}
	if result.Batch.FailedCount != 1 {
		t.Fatalf("FailedCount = %d, want 1", result.Batch.FailedCount)
	}
	if result.Batch.SucceededCount != 0 {
		t.Fatalf("SucceededCount = %d, want 0", result.Batch.SucceededCount)
	}
	var blocked *domain.Transaction
	for _, tx := range result.Transactions {
		if tx.Status == domain.StatusFailed {
			blocked = tx
		}
	}
	if blocked == nil {
		t.Fatal("no failed item in batch")
	}
	if blocked.FailureReason != failureReasonBatchItem {
		t.Fatalf("failure reason = %q, want %q", blocked.FailureReason, failureReasonBatchItem)
	}
	if blocked.FailureMessage == "" {
		t.Fatal("failure message is empty")
	}
}

// The blocked failed row carries the submitting tenant, so the tenant can see
// which of its rows was blocked.
func TestCreateBatch_BlockedItemIsVisibleToSubmittingTenant(t *testing.T) {
	const tenantID = "tenant-1"
	f := newFixtureWith(nil, map[string]bool{"wallet-0": true})
	ctx := tenant.WithID(context.Background(), tenantID)

	created, err := f.svc.CreateBatch(ctx, "source-wallet", makeItems(1))
	if err != nil {
		t.Fatalf("CreateBatch() error: %v", err)
	}
	result, err := f.svc.GetBatch(ctx, created.Batch.ID)
	if err != nil {
		t.Fatalf("GetBatch() error: %v", err)
	}
	if len(result.Transactions) != 1 {
		t.Fatalf("tenant sees %d batch items, want 1", len(result.Transactions))
	}
	blocked := result.Transactions[0]
	if blocked.TenantID == nil || *blocked.TenantID != tenantID {
		t.Fatalf("blocked item tenant = %v, want %q", blocked.TenantID, tenantID)
	}
	if blocked.FailureReason != failureReasonBatchItem {
		t.Fatalf("failure reason = %q, want %q", blocked.FailureReason, failureReasonBatchItem)
	}
	if blocked.FailureMessage != domain.ErrTransferBlockedSanctions.Error() {
		t.Fatalf("failure message = %q, want %q", blocked.FailureMessage, domain.ErrTransferBlockedSanctions)
	}
}

// Settlement settles children later; the batch summary follows through
// SyncAggregates, and the read path self-heals stale aggregate columns.
func TestGetBatch_ComputesStatusFromSettledChildren(t *testing.T) {
	f := newFixture()
	created, err := f.svc.CreateBatch(context.Background(), "source-wallet", makeItems(2))
	if err != nil {
		t.Fatalf("CreateBatch() error: %v", err)
	}
	id := created.Batch.ID

	pending, err := f.svc.GetBatch(context.Background(), id)
	if err != nil {
		t.Fatalf("GetBatch() error: %v", err)
	}
	if pending.Batch.Status != domain.BatchStatusPending {
		t.Fatalf("status before settlement = %s, want %s", pending.Batch.Status, domain.BatchStatusPending)
	}

	for _, tx := range f.txRepo.byBatch[id] {
		tx.Status = domain.StatusConfirmed
		tx.TxHash = "hash-" + tx.ToWallet
	}

	result, err := f.svc.GetBatch(context.Background(), id)
	if err != nil {
		t.Fatalf("GetBatch() error: %v", err)
	}
	if result.Batch.Status != domain.BatchStatusCompleted {
		t.Fatalf("status after settlement = %s, want %s", result.Batch.Status, domain.BatchStatusCompleted)
	}
	if result.Batch.SucceededCount != 2 || result.Batch.FailedCount != 0 {
		t.Fatalf("counts = %d/%d, want 2/0", result.Batch.SucceededCount, result.Batch.FailedCount)
	}
	// The read path must have persisted the recomputation, not just reported it.
	stored, err := f.batchRepo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("stored GetByID() error: %v", err)
	}
	if stored.Status != domain.BatchStatusCompleted || stored.SucceededCount != 2 {
		t.Fatalf("stored batch = %s %d, want %s 2", stored.Status, stored.SucceededCount, domain.BatchStatusCompleted)
	}
	if stored.SettledAt == nil {
		t.Fatal("stored settled_at not set after completion")
	}
}

// The batch webhook fires exactly once, on the terminal transition, with the
// outcome the issue specifies: batch.completed for a clean run,
// batch.partially_failed when succeeded and failed coexist.
func TestSyncBatch_DispatchesBatchWebhookOnTerminalTransitionOnly(t *testing.T) {
	f := newFixture()
	dispatcher := &fakeDispatcher{}
	recorder := NewRecorder(f.batchRepo, dispatcher)

	created, err := f.svc.CreateBatch(context.Background(), "source-wallet", makeItems(2))
	if err != nil {
		t.Fatalf("CreateBatch() error: %v", err)
	}
	id := created.Batch.ID

	if err := recorder.SyncBatch(context.Background(), id); err != nil {
		t.Fatalf("SyncBatch() error: %v", err)
	}
	if len(dispatcher.events()) != 0 {
		t.Fatalf("dispatched %v before any child settled, want none", dispatcher.events())
	}

	txs := f.txRepo.byBatch[id]
	txs[0].Status = domain.StatusConfirmed
	if err := recorder.SyncBatch(context.Background(), id); err != nil {
		t.Fatalf("SyncBatch() error: %v", err)
	}
	if len(dispatcher.events()) != 0 {
		t.Fatalf("dispatched %v while batch still processing, want none", dispatcher.events())
	}

	txs[1].Status = domain.StatusFailed
	if err := recorder.SyncBatch(context.Background(), id); err != nil {
		t.Fatalf("SyncBatch() error: %v", err)
	}
	events := dispatcher.events()
	if len(events) != 1 || events[0] != string(domain.EventBatchPartiallyFailed) {
		t.Fatalf("events = %v, want exactly one %s", events, domain.EventBatchPartiallyFailed)
	}
	payload := dispatcher.payloads[0]["payload"].(map[string]interface{})
	if payload["batch_id"] != id || payload["status"] != string(domain.BatchStatusPartial) {
		t.Fatalf("payload = %v, want batch_id %q and status %q", payload, id, domain.BatchStatusPartial)
	}
	if payload["total_count"] != 2 || payload["succeeded_count"] != 1 || payload["failed_count"] != 1 {
		t.Fatalf("payload counts = total:%v succeeded:%v failed:%v, want 2/1/1",
			payload["total_count"], payload["succeeded_count"], payload["failed_count"])
	}
	if _, ok := payload["settled_at"].(string); !ok {
		t.Fatalf("payload settled_at = %v, want RFC3339 string", payload["settled_at"])
	}

	if err := recorder.SyncBatch(context.Background(), id); err != nil {
		t.Fatalf("SyncBatch() error: %v", err)
	}
	if len(dispatcher.events()) != 1 {
		t.Fatalf("dispatched %d events after terminal transition, want 1", len(dispatcher.events()))
	}
}

// A fully successful batch reports batch.completed, and an all-failed batch
// still completed processing — batch.completed carrying status "failed".
func TestSyncBatch_CompletedAndAllFailedReportBatchCompleted(t *testing.T) {
	tests := []struct {
		name      string
		failures  map[string]bool
		wantEvent domain.EventType
		wantStat  domain.BatchStatus
	}{
		{"all succeeded", nil, domain.EventBatchCompleted, domain.BatchStatusCompleted},
		{"all failed at persist", map[string]bool{"wallet-0": true, "wallet-1": true}, domain.EventBatchCompleted, domain.BatchStatusFailed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixtureWith(nil, tc.failures)
			dispatcher := &fakeDispatcher{}
			recorder := NewRecorder(f.batchRepo, dispatcher)

			created, err := f.svc.CreateBatch(context.Background(), "source-wallet", makeItems(2))
			if err != nil {
				t.Fatalf("CreateBatch() error: %v", err)
			}
			id := created.Batch.ID
			for _, tx := range f.txRepo.byBatch[id] {
				if tx.Status == domain.StatusPending {
					tx.Status = domain.StatusConfirmed
				}
			}
			if err := recorder.SyncBatch(context.Background(), id); err != nil {
				t.Fatalf("SyncBatch() error: %v", err)
			}
			events := dispatcher.events()
			if len(events) != 1 || events[0] != string(tc.wantEvent) {
				t.Fatalf("events = %v, want exactly one %s", events, tc.wantEvent)
			}
			payload := dispatcher.payloads[0]["payload"].(map[string]interface{})
			if payload["status"] != string(tc.wantStat) {
				t.Fatalf("payload status = %v, want %q", payload, tc.wantStat)
			}
		})
	}
}

func TestCreateBatch_RejectsEmptyAndOversizedBatches(t *testing.T) {
	f := newFixture()

	if _, err := f.svc.CreateBatch(context.Background(), "source-wallet", nil); !errors.Is(err, domain.ErrBatchEmpty) {
		t.Fatalf("empty batch error = %v, want ErrBatchEmpty", err)
	}

	items := makeItems(101)
	if _, err := f.svc.CreateBatch(context.Background(), "source-wallet", items); !errors.Is(err, domain.ErrBatchTooLarge) {
		t.Fatalf("oversized batch error = %v, want ErrBatchTooLarge", err)
	}
}

// Any invalid row rejects the whole batch with a per-row error list, before
// anything is prepared or written.
func TestCreateBatch_ValidationErrorsRejectWholeBatch(t *testing.T) {
	f := newFixture()

	items := []Item{
		{ToWalletID: "source-wallet", Asset: "XLM", Amount: decimal.NewFromInt(10)}, // self-transfer
		{ToWalletID: "wallet-0", Asset: "XLM", Amount: decimal.Zero},                // non-positive amount
		{ToWalletID: "wallet-999", Asset: "XLM", Amount: decimal.NewFromInt(10)},    // unknown destination
	}
	_, err := f.svc.CreateBatch(context.Background(), "source-wallet", items)
	var batchErr *BatchValidationError
	if !errors.As(err, &batchErr) {
		t.Fatalf("CreateBatch() error = %v, want *BatchValidationError", err)
	}
	got := map[int]string{}
	for _, ve := range batchErr.Errors {
		got[ve.Row] = ve.Field
	}
	if got[1] != "to_wallet_id" || got[2] != "amount" || got[3] != "to_wallet_id" {
		t.Fatalf("validation rows/fields = %v, want row1 to_wallet_id, row2 amount, row3 to_wallet_id", got)
	}
	if len(f.batchRepo.batches) != 0 || len(f.txRepo.byBatch) != 0 {
		t.Fatal("validation failure persisted state")
	}
}

// The source wallet itself is validated first: a missing source reports row 0.
func TestCreateBatch_MissingSourceWalletReportsRowZero(t *testing.T) {
	f := newFixture()

	_, err := f.svc.CreateBatch(context.Background(), "missing-wallet", makeItems(1))
	var batchErr *BatchValidationError
	if !errors.As(err, &batchErr) {
		t.Fatalf("CreateBatch() error = %v, want *BatchValidationError", err)
	}
	if len(batchErr.Errors) != 1 || batchErr.Errors[0].Row != 0 || batchErr.Errors[0].Field != "from_wallet_id" {
		t.Fatalf("errors = %+v, want single row 0 from_wallet_id", batchErr.Errors)
	}
}

// A queue outage after the commit must not fail the request: the batch is
// durable, an idempotent retry would duplicate it, and force-settle can still
// pick the row up later.
func TestCreateBatch_EnqueueFailureStillSucceeds(t *testing.T) {
	f := newFixture()
	fails := &failingQueue{}
	svc := NewService(f.batchRepo, f.txRepo, f.transferSvc,
		WithWalletLookup(f.wallets),
		WithQueue(fails),
		WithTxRunner(func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }),
	)

	result, err := svc.CreateBatch(context.Background(), "source-wallet", makeItems(3))
	if err != nil {
		t.Fatalf("CreateBatch() error: %v, want success despite enqueue failure", err)
	}
	if result.Batch.TotalCount != 3 {
		t.Fatalf("TotalCount = %d, want 3", result.Batch.TotalCount)
	}
	if fails.calls != 3 {
		t.Fatalf("enqueue attempted %d times, want 3", fails.calls)
	}
	if len(f.batchRepo.batches) != 1 {
		t.Fatalf("stored %d batches, want 1", len(f.batchRepo.batches))
	}
}

type failingQueue struct{ calls int }

func (f *failingQueue) EnqueueBatchTransfer(_ context.Context, _, _ string) error {
	f.calls++
	return errors.New("redis unavailable")
}

func TestExportCSV_IncludesStatusTxHashAndReference(t *testing.T) {
	f := newFixture()
	created, err := f.svc.CreateBatch(context.Background(), "source-wallet", makeItems(2))
	if err != nil {
		t.Fatalf("CreateBatch() error: %v", err)
	}

	tx := f.txRepo.byBatch[created.Batch.ID][0]
	tx.Status = domain.StatusConfirmed
	tx.TxHash = "hash-" + tx.ToWallet

	csv, err := f.svc.ExportCSV(context.Background(), created.Batch.ID)
	if err != nil {
		t.Fatalf("ExportCSV() error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(csv), "\n")
	if lines[0] != "to_wallet,asset,amount,reference,status,tx_hash" {
		t.Fatalf("header = %q", lines[0])
	}
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3 (header + 2 rows)", len(lines))
	}
	if !strings.Contains(lines[1], "ref-0") || !strings.Contains(lines[1], "confirmed") || !strings.Contains(lines[1], "hash-wallet-0") {
		t.Fatalf("settled row missing expected fields: %q", lines[1])
	}
	if !strings.Contains(lines[2], "ref-1") || !strings.Contains(lines[2], "pending") {
		t.Fatalf("settling row missing expected fields: %q", lines[2])
	}
}
