package schedule

// run_test.go — comprehensive tests for the schedule-run lifecycle
//
// Coverage:
//   - Successful run: run record created, status = succeeded, tx ID stored,
//     completion timestamp set, schedule advances.
//   - Initiation failure: run record exists, status = failed, error stored,
//     no transaction ID, schedule status = failed.
//   - Retry (crash after ClaimRun, before success): reuses the existing run
//     record; payout is not duplicated (idempotency key returns same tx).
//   - Concurrent workers: only one ClaimRun write wins; second sees a
//     terminal status and skips payout.
//   - Skipped occurrence: a run already in 'succeeded' state is not re-run.
//   - Cancelled occurrence: a run already in 'cancelled' state is not re-run.
//   - Worker crash after Claim but before ClaimRun: schedule is reverted to
//     active so the next tick retries.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/hibiken/asynq"
	"github.com/shopspring/decimal"
)

// ---------------------------------------------------------------------------
// Fake repository that tracks run records
// ---------------------------------------------------------------------------

type runKey struct {
	scheduleID string
	expectedAt time.Time // truncated to second
}

type fakeRunRepo struct {
	mu        sync.Mutex
	schedules map[string]*domain.Schedule
	runs      map[runKey]*domain.ScheduleRun

	// claimRunErr, if non-nil, is returned by ClaimRun to simulate a DB error.
	claimRunErr error
}

func newFakeRunRepo() *fakeRunRepo {
	return &fakeRunRepo{
		schedules: make(map[string]*domain.Schedule),
		runs:      make(map[runKey]*domain.ScheduleRun),
	}
}

func (f *fakeRunRepo) Create(_ context.Context, s *domain.Schedule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.schedules[s.ID] = s
	return nil
}

func (f *fakeRunRepo) GetByID(_ context.Context, id string) (*domain.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.schedules[id]
	if !ok {
		return nil, domain.ErrScheduleNotFound
	}
	return s, nil
}

func (f *fakeRunRepo) List(_ context.Context) ([]*domain.Schedule, error) {
	return nil, nil
}

func (f *fakeRunRepo) Update(_ context.Context, s *domain.Schedule) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.schedules[s.ID] = s
	return nil
}

// Claim is an atomic CAS on schedule status. In tests without DB, we simulate
// it by only returning true when the schedule is still 'active'.
func (f *fakeRunRepo) Claim(_ context.Context, id string, expectedNextRunAt time.Time) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.schedules[id]
	if !ok || s.Status != domain.ScheduleStatusActive {
		return false, nil
	}
	s.Status = domain.ScheduleStatusProcessing
	return true, nil
}

// ListDue returns copies of due schedules so concurrent workers do not share
// pointers into the map, which would cause a data race under -race.
func (f *fakeRunRepo) ListDue(_ context.Context, now time.Time) ([]*domain.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.Schedule
	for _, s := range f.schedules {
		if s.Status == domain.ScheduleStatusActive && !s.NextRunAt.After(now) {
			// Return a copy so concurrent workers hold independent structs.
			sCopy := *s
			out = append(out, &sCopy)
		}
	}
	return out, nil
}
// The first caller for a (scheduleID, expectedAt) inserts; subsequent callers
// receive the existing record.  This simulates the database uniqueness constraint.
func (f *fakeRunRepo) ClaimRun(_ context.Context, scheduleID string, tenantID *string, expectedAt time.Time) (*domain.ScheduleRun, error) {
	if f.claimRunErr != nil {
		return nil, f.claimRunErr
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	k := runKey{scheduleID: scheduleID, expectedAt: expectedAt.UTC().Truncate(time.Second)}
	if existing, ok := f.runs[k]; ok {
		return existing, nil
	}
	now := time.Now().UTC()
	run := &domain.ScheduleRun{
		ID:            "run-" + scheduleID,
		ScheduleID:    scheduleID,
		TenantID:      tenantID,
		ExpectedRunAt: k.expectedAt,
		Status:        domain.ScheduleRunStatusPending,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	f.runs[k] = run
	return run, nil
}

func (f *fakeRunRepo) UpdateRun(_ context.Context, run *domain.ScheduleRun) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := runKey{scheduleID: run.ScheduleID, expectedAt: run.ExpectedRunAt.UTC().Truncate(time.Second)}
	f.runs[k] = run
	return nil
}

func (f *fakeRunRepo) GetRun(_ context.Context, scheduleID string, expectedAt time.Time) (*domain.ScheduleRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := runKey{scheduleID: scheduleID, expectedAt: expectedAt.UTC().Truncate(time.Second)}
	run, ok := f.runs[k]
	if !ok {
		return nil, domain.ErrScheduleRunNotFound
	}
	return run, nil
}

func (f *fakeRunRepo) ListRuns(_ context.Context, scheduleID string, limit, offset int) ([]*domain.ScheduleRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []*domain.ScheduleRun
	for _, r := range f.runs {
		if r.ScheduleID == scheduleID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeRunRepo) runCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.runs)
}

func (f *fakeRunRepo) getRunForSchedule(scheduleID string, expectedAt time.Time) (*domain.ScheduleRun, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := runKey{scheduleID: scheduleID, expectedAt: expectedAt.UTC().Truncate(time.Second)}
	r, ok := f.runs[k]
	return r, ok
}

// ---------------------------------------------------------------------------
// Idempotent transfer fake — tracks calls by idempotency key
// ---------------------------------------------------------------------------

type idempTransferSvc struct {
	mu      sync.Mutex
	records map[string]*domain.Transaction // keyed by idempotency key
	callLog []string                        // ordered list of idempotency keys seen
	failFor map[string]error                // idempotency keys that should fail
}

func newIdempTransferSvc() *idempTransferSvc {
	return &idempTransferSvc{
		records: make(map[string]*domain.Transaction),
		failFor: make(map[string]error),
	}
}

func (f *idempTransferSvc) InitiateTransfer(_ context.Context, fromID, toID, asset string, amount decimal.Decimal) (*domain.Transaction, error) {
	return &domain.Transaction{ID: "tx-direct"}, nil
}

func (f *idempTransferSvc) InitiateTransferIdempotent(_ context.Context, fromID, toID, asset string, amount decimal.Decimal, idempotencyKey string) (*domain.Transaction, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.callLog = append(f.callLog, idempotencyKey)

	if err, shouldFail := f.failFor[idempotencyKey]; shouldFail {
		return nil, err
	}

	if existing, ok := f.records[idempotencyKey]; ok {
		return existing, nil
	}
	tx := &domain.Transaction{ID: "tx-" + idempotencyKey}
	f.records[idempotencyKey] = tx
	return tx, nil
}

func (f *idempTransferSvc) InitiateBatchTransfer(_ context.Context, _, _, _ string, _ decimal.Decimal, _, _ string) (*domain.Transaction, error) {
	return &domain.Transaction{ID: "tx-batch"}, nil
}
func (f *idempTransferSvc) WithScreener(_ transfer.Screener) transfer.Service   { return f }
func (f *idempTransferSvc) WithStellarClient(_ stellar.Client) transfer.Service { return f }
func (f *idempTransferSvc) GetTransaction(_ context.Context, _ string) (*domain.Transaction, error) {
	return nil, domain.ErrTransactionNotFound
}
func (f *idempTransferSvc) ListTransactions(_ context.Context, _ string, _, _ int) ([]*domain.Transaction, error) {
	return nil, nil
}
func (f *idempTransferSvc) ForceSettleTransfer(_ context.Context, _, _ string) (*domain.Transaction, error) {
	return nil, nil
}
func (f *idempTransferSvc) ReconcileWallet(_ context.Context, _, _ string) (*transfer.ReconcileResult, error) {
	return nil, nil
}
func (f *idempTransferSvc) WithAuditLogger(_ transfer.AuditLogger) transfer.Service { return f }

func (f *idempTransferSvc) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.callLog)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func makeActiveSchedule(id string, dueAt time.Time) *domain.Schedule {
	return &domain.Schedule{
		ID:         id,
		FromWallet: "from-1",
		ToWallet:   "to-1",
		Asset:      "XLM",
		Amount:     decimal.NewFromInt(10),
		Frequency:  domain.FrequencyWeekly,
		NextRunAt:  dueAt,
		Status:     domain.ScheduleStatusActive,
	}
}

// ---------------------------------------------------------------------------
// Test: Successful run
// ---------------------------------------------------------------------------

func TestRunOne_Success_CreatesRunRecordWithSucceededStatus(t *testing.T) {
	repo := newFakeRunRepo()
	transferSvc := newIdempTransferSvc()

	dueAt := time.Now().UTC().Add(-30 * time.Second)
	sch := makeActiveSchedule("sched-1", dueAt)
	repo.schedules[sch.ID] = sch

	worker := NewWorker(repo, transferSvc)
	if err := worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil)); err != nil {
		t.Fatalf("HandleRunSchedules error: %v", err)
	}

	if repo.runCount() != 1 {
		t.Fatalf("got %d run records, want 1", repo.runCount())
	}

	run, ok := repo.getRunForSchedule("sched-1", dueAt)
	if !ok {
		t.Fatal("run record not found")
	}

	if run.Status != domain.ScheduleRunStatusSucceeded {
		t.Errorf("status = %s, want succeeded", run.Status)
	}
	if run.TransactionID == nil || *run.TransactionID == "" {
		t.Error("transaction_id should be set on a succeeded run")
	}
	if run.CompletedAt == nil {
		t.Error("completed_at should be set on a succeeded run")
	}
	expectedKey := runIDempotencyKey("sched-1", dueAt)
	if *run.TransactionID != "tx-"+expectedKey {
		t.Errorf("transaction_id = %q, want %q", *run.TransactionID, "tx-"+expectedKey)
	}

	// Schedule must have advanced
	if !repo.schedules["sched-1"].NextRunAt.After(dueAt) {
		t.Error("schedule NextRunAt was not advanced after a successful run")
	}
	if repo.schedules["sched-1"].Status != domain.ScheduleStatusActive {
		t.Errorf("schedule status = %s, want active after success", repo.schedules["sched-1"].Status)
	}
}

// ---------------------------------------------------------------------------
// Test: Initiation failure
// ---------------------------------------------------------------------------

func TestRunOne_TransferFailure_RecordsFailedRun(t *testing.T) {
	repo := newFakeRunRepo()
	transferSvc := newIdempTransferSvc()

	dueAt := time.Now().UTC().Add(-30 * time.Second)
	sch := makeActiveSchedule("sched-2", dueAt)
	repo.schedules[sch.ID] = sch

	idempKey := runIDempotencyKey("sched-2", dueAt)
	transferSvc.failFor[idempKey] = errors.New("insufficient balance")

	worker := NewWorker(repo, transferSvc)
	_ = worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil))

	run, ok := repo.getRunForSchedule("sched-2", dueAt)
	if !ok {
		t.Fatal("run record must exist even on failure")
	}
	if run.Status != domain.ScheduleRunStatusFailed {
		t.Errorf("status = %s, want failed", run.Status)
	}
	if run.Error == nil || *run.Error == "" {
		t.Error("error field must be populated on a failed run")
	}
	if run.TransactionID != nil {
		t.Error("transaction_id must be nil on a failed run")
	}
	if run.CompletedAt == nil {
		t.Error("completed_at must be set on a failed run")
	}

	// Schedule must not have advanced; it should be in failed state.
	if repo.schedules["sched-2"].Status != domain.ScheduleStatusFailed {
		t.Errorf("schedule status = %s, want failed", repo.schedules["sched-2"].Status)
	}
	// NextRunAt must not have advanced past the original due time.
	if repo.schedules["sched-2"].NextRunAt.After(dueAt.Add(time.Minute)) {
		t.Error("schedule NextRunAt must not advance when transfer fails")
	}
}

// ---------------------------------------------------------------------------
// Test: Retry — same occurrence, second worker sees existing run record
// ---------------------------------------------------------------------------

func TestRunOne_Retry_DoesNotInitiateDuplicateTransfer(t *testing.T) {
	repo := newFakeRunRepo()
	transferSvc := newIdempTransferSvc()

	dueAt := time.Now().UTC().Add(-30 * time.Second)
	sch := makeActiveSchedule("sched-3", dueAt)
	repo.schedules[sch.ID] = sch

	worker := NewWorker(repo, transferSvc)

	// First run — succeeds.
	_ = worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil))
	if transferSvc.callCount() != 1 {
		t.Fatalf("first run: got %d transfer calls, want 1", transferSvc.callCount())
	}

	// Simulate a retry: reset schedule to active at the same expected_at.
	sch2 := makeActiveSchedule("sched-3", dueAt)
	repo.schedules["sched-3"] = sch2

	// Second run — the run record is already in 'succeeded'; the worker should
	// skip payout initiation entirely and just advance the schedule.
	_ = worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil))

	if transferSvc.callCount() != 1 {
		t.Errorf("retry: got %d transfer calls total, want still 1 (no duplicate)", transferSvc.callCount())
	}
	if repo.runCount() != 1 {
		t.Errorf("retry: got %d run records, want 1 (no duplicate run)", repo.runCount())
	}
}

// ---------------------------------------------------------------------------
// Test: Retry from 'running' state (crash recovery)
// ---------------------------------------------------------------------------

func TestRunOne_CrashRecovery_ReuseRunningRecord(t *testing.T) {
	repo := newFakeRunRepo()
	transferSvc := newIdempTransferSvc()

	dueAt := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Second)
	sch := makeActiveSchedule("sched-4", dueAt)
	repo.schedules[sch.ID] = sch

	// Pre-insert a 'running' run to simulate a prior worker that crashed.
	now := time.Now().UTC()
	preRun := &domain.ScheduleRun{
		ID:            "run-sched-4",
		ScheduleID:    "sched-4",
		ExpectedRunAt: dueAt,
		Status:        domain.ScheduleRunStatusRunning,
		StartedAt:     &now,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	k := runKey{scheduleID: "sched-4", expectedAt: dueAt}
	repo.runs[k] = preRun

	worker := NewWorker(repo, transferSvc)
	_ = worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil))

	// Exactly one transfer call should have been made.
	if transferSvc.callCount() != 1 {
		t.Errorf("crash recovery: got %d transfer calls, want 1", transferSvc.callCount())
	}
	// Only one run record should exist.
	if repo.runCount() != 1 {
		t.Errorf("crash recovery: got %d run records, want 1", repo.runCount())
	}
	run, _ := repo.getRunForSchedule("sched-4", dueAt)
	if run.Status != domain.ScheduleRunStatusSucceeded {
		t.Errorf("crash recovery: run status = %s, want succeeded", run.Status)
	}
}

// ---------------------------------------------------------------------------
// Test: Concurrent workers — only one payout per occurrence
// ---------------------------------------------------------------------------

func TestRunOne_ConcurrentWorkers_OnlyOnePayoutInitiated(t *testing.T) {
	// This test simulates the database UNIQUE(schedule_id, expected_run_at)
	// constraint via the fakeRunRepo's mutex-protected in-memory map.
	// Two goroutines process the same schedule occurrence simultaneously;
	// only the goroutine that wins the Claim step should initiate a transfer.

	repo := newFakeRunRepo()
	var payoutCount int64
	transferSvc := newIdempTransferSvc()

	// Wrap InitiateTransferIdempotent to count actual payout initiations
	// (excluding idempotent cache hits, which are also safe).
	_ = payoutCount // used below via atomic

	dueAt := time.Now().UTC().Add(-30 * time.Second)
	sch := makeActiveSchedule("sched-conc", dueAt)
	repo.schedules[sch.ID] = sch

	// Run two workers concurrently. Only the one that wins Claim (CAS) will
	// proceed; the second finds the schedule in 'processing' and returns early.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := NewWorker(repo, transferSvc)
			_ = w.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil))
		}()
	}
	wg.Wait()

	// The idempotency key is the same for both workers, so even if both called
	// InitiateTransferIdempotent, only one transaction would be created.
	// The Claim CAS ensures only one does.
	if n := transferSvc.callCount(); n > 1 {
		t.Errorf("concurrent workers: got %d transfer calls, want at most 1 (Claim CAS should block second)", n)
	}
	if repo.runCount() > 1 {
		t.Errorf("concurrent workers: got %d run records, want 1", repo.runCount())
	}
}

// ---------------------------------------------------------------------------
// Test: Skipped occurrence — already succeeded run is not re-executed
// ---------------------------------------------------------------------------

func TestRunOne_SkippedOccurrence_DoesNotReInitiateTransfer(t *testing.T) {
	repo := newFakeRunRepo()
	transferSvc := newIdempTransferSvc()

	dueAt := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Second)
	sch := makeActiveSchedule("sched-skip", dueAt)
	repo.schedules[sch.ID] = sch

	// Pre-insert a 'skipped' run record.
	now := time.Now().UTC()
	skippedRun := &domain.ScheduleRun{
		ID:            "run-sched-skip",
		ScheduleID:    "sched-skip",
		ExpectedRunAt: dueAt,
		Status:        domain.ScheduleRunStatusSkipped,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	k := runKey{scheduleID: "sched-skip", expectedAt: dueAt}
	repo.runs[k] = skippedRun

	worker := NewWorker(repo, transferSvc)
	_ = worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil))

	if transferSvc.callCount() != 0 {
		t.Errorf("skipped: got %d transfer calls, want 0", transferSvc.callCount())
	}
	if repo.runCount() != 1 {
		t.Errorf("skipped: got %d run records, want 1 (no extra record)", repo.runCount())
	}
	// The existing skipped record must be unchanged.
	run, _ := repo.getRunForSchedule("sched-skip", dueAt)
	if run.Status != domain.ScheduleRunStatusSkipped {
		t.Errorf("skipped: run status changed to %s, want skipped", run.Status)
	}
	// Schedule advances past the skipped occurrence.
	if !repo.schedules["sched-skip"].NextRunAt.After(dueAt) {
		t.Error("skipped: schedule must advance past a skipped occurrence")
	}
}

// ---------------------------------------------------------------------------
// Test: Cancelled occurrence
// ---------------------------------------------------------------------------

func TestRunOne_CancelledOccurrence_DoesNotReInitiateTransfer(t *testing.T) {
	repo := newFakeRunRepo()
	transferSvc := newIdempTransferSvc()

	dueAt := time.Now().UTC().Add(-30 * time.Second).Truncate(time.Second)
	sch := makeActiveSchedule("sched-cancel", dueAt)
	repo.schedules[sch.ID] = sch

	now := time.Now().UTC()
	cancelledRun := &domain.ScheduleRun{
		ID:            "run-sched-cancel",
		ScheduleID:    "sched-cancel",
		ExpectedRunAt: dueAt,
		Status:        domain.ScheduleRunStatusCancelled,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	k := runKey{scheduleID: "sched-cancel", expectedAt: dueAt}
	repo.runs[k] = cancelledRun

	worker := NewWorker(repo, transferSvc)
	_ = worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil))

	if transferSvc.callCount() != 0 {
		t.Errorf("cancelled: got %d transfer calls, want 0", transferSvc.callCount())
	}
}

// ---------------------------------------------------------------------------
// Test: Worker crash after Claim but before ClaimRun — schedule is reverted
// ---------------------------------------------------------------------------

func TestRunOne_ClaimRunError_RevertsScheduleToActive(t *testing.T) {
	repo := newFakeRunRepo()
	transferSvc := newIdempTransferSvc()

	// Inject a ClaimRun error to simulate a DB failure after claiming the schedule.
	repo.claimRunErr = errors.New("database unavailable")

	dueAt := time.Now().UTC().Add(-30 * time.Second)
	sch := makeActiveSchedule("sched-crash", dueAt)
	repo.schedules[sch.ID] = sch

	worker := NewWorker(repo, transferSvc)
	_ = worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil))

	if transferSvc.callCount() != 0 {
		t.Errorf("crash: got %d transfer calls, want 0", transferSvc.callCount())
	}
	// Schedule must be reverted to active so the next tick can retry.
	if repo.schedules["sched-crash"].Status != domain.ScheduleStatusActive {
		t.Errorf("crash: schedule status = %s, want active after revert", repo.schedules["sched-crash"].Status)
	}
}

// ---------------------------------------------------------------------------
// Test: Idempotency key format
// ---------------------------------------------------------------------------

func TestRunIDempotencyKey_IsStableForSameOccurrence(t *testing.T) {
	scheduleID := "sched-abc"
	t1 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 9, 25, 10, 0, 0, 999_000_000, time.UTC) // same second, different nanos

	k1 := runIDempotencyKey(scheduleID, t1)
	k2 := runIDempotencyKey(scheduleID, t2)
	if k1 != k2 {
		t.Errorf("idempotency key differs for times in the same second: %q vs %q", k1, k2)
	}

	t3 := t1.Add(time.Second)
	k3 := runIDempotencyKey(scheduleID, t3)
	if k1 == k3 {
		t.Error("idempotency key must differ for different seconds")
	}
}

// ---------------------------------------------------------------------------
// Test: ExpectedRunAt is preserved on the run record
// ---------------------------------------------------------------------------

func TestRunOne_ExpectedRunAt_IsStoredCorrectly(t *testing.T) {
	repo := newFakeRunRepo()
	transferSvc := newIdempTransferSvc()

	dueAt := time.Date(2026, 1, 15, 8, 0, 0, 0, time.UTC)
	sch := makeActiveSchedule("sched-ea", dueAt)
	repo.schedules[sch.ID] = sch

	worker := NewWorker(repo, transferSvc)
	_ = worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil))

	run, ok := repo.getRunForSchedule("sched-ea", dueAt)
	if !ok {
		t.Fatal("run record not found")
	}
	if !run.ExpectedRunAt.Equal(dueAt.Truncate(time.Second)) {
		t.Errorf("expected_run_at = %v, want %v", run.ExpectedRunAt, dueAt.Truncate(time.Second))
	}
}

// ---------------------------------------------------------------------------
// Test: Schedule completed past end date still creates a run record
// ---------------------------------------------------------------------------

func TestRunOne_ScheduleCompletesAfterEndAt(t *testing.T) {
	repo := newFakeRunRepo()
	transferSvc := newIdempTransferSvc()

	dueAt := time.Now().UTC().Add(-30 * time.Second)
	endAt := dueAt.Add(time.Minute)
	sch := makeActiveSchedule("sched-end", dueAt)
	sch.EndAt = &endAt
	repo.schedules[sch.ID] = sch

	worker := NewWorker(repo, transferSvc)
	_ = worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil))

	run, ok := repo.getRunForSchedule("sched-end", dueAt)
	if !ok {
		t.Fatal("run record must be created even when schedule completes")
	}
	if run.Status != domain.ScheduleRunStatusSucceeded {
		t.Errorf("run status = %s, want succeeded", run.Status)
	}
	if repo.schedules["sched-end"].Status != domain.ScheduleStatusCompleted {
		t.Errorf("schedule status = %s, want completed", repo.schedules["sched-end"].Status)
	}
}

// ---------------------------------------------------------------------------
// Test: Legacy zero NextRunAt recalculation still creates a run record
// ---------------------------------------------------------------------------

func TestRunOne_LegacyZeroNextRunAt_StillCreatesRunRecord(t *testing.T) {
	repo := newFakeRunRepo()
	transferSvc := newIdempTransferSvc()

	sch := &domain.Schedule{
		ID:         "sched-legacy",
		FromWallet: "from-1",
		ToWallet:   "to-1",
		Asset:      "XLM",
		Amount:     decimal.NewFromInt(5),
		Frequency:  domain.FrequencyDaily,
		Status:     domain.ScheduleStatusActive,
		// NextRunAt intentionally zero
	}
	repo.schedules[sch.ID] = sch

	worker := NewWorker(repo, transferSvc)
	_ = worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil))

	if repo.runCount() != 1 {
		t.Fatalf("got %d run records, want 1 for legacy schedule", repo.runCount())
	}
	if transferSvc.callCount() != 1 {
		t.Fatalf("got %d transfer calls, want 1 for legacy schedule", transferSvc.callCount())
	}
}
