package schedule

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/fluxa/fluxa/internal/queue"
	"github.com/fluxa/fluxa/internal/stellar"
	"github.com/fluxa/fluxa/internal/transfer"
	"github.com/hibiken/asynq"
	"github.com/shopspring/decimal"
)

type transferCall struct {
	fromID, toID, asset string
	amount              decimal.Decimal
}

type fakeTransferSvc struct {
	calls []transferCall
}

func (f *fakeTransferSvc) InitiateTransfer(_ context.Context, fromID, toID, asset string, amount decimal.Decimal) (*domain.Transaction, error) {
	f.calls = append(f.calls, transferCall{fromID, toID, asset, amount})
	return &domain.Transaction{ID: "tx-1"}, nil
}

func (f *fakeTransferSvc) InitiateTransferIdempotent(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, idempotencyKey string) (*domain.Transaction, error) {
	return f.InitiateTransfer(ctx, fromID, toID, asset, amount)
}

func (f *fakeTransferSvc) InitiateBatchTransfer(_ context.Context, fromID, toID, asset string, amount decimal.Decimal, batchID, reference string) (*domain.Transaction, error) {
	return &domain.Transaction{ID: "tx-1"}, nil
}

func (f *fakeTransferSvc) PrepareBatchTransfer(_ context.Context, _ transfer.TransferParams) (*transfer.PreparedTransfer, error) {
	return nil, nil
}

func (f *fakeTransferSvc) PersistBatchTransfer(_ context.Context, _ *transfer.PreparedTransfer) (*domain.Transaction, error) {
	return nil, nil
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

func (f *fakeTransferSvc) InitiateTransferExt(ctx context.Context, params transfer.TransferParams) (*domain.Transaction, error) {
	return f.InitiateTransfer(ctx, params.FromID, params.ToID, params.Asset, params.Amount)
}

func (f *fakeTransferSvc) ListTransactionsFiltered(_ context.Context, _ domain.TransactionFilter) ([]*domain.Transaction, error) {
	return nil, nil
}

func (f *fakeTransferSvc) ListTransactions(_ context.Context, walletID string, limit, offset int) ([]*domain.Transaction, error) {
	return nil, nil
}

func TestHandleRunSchedules_FiresWeeklyScheduleAndAdvancesByExactlySevenDays(t *testing.T) {
	repo := newFakeScheduleRepo()
	dueAt := time.Now().UTC().Add(-30 * time.Second) // due within the 1-minute tick window
	sch := &domain.Schedule{
		ID:         "sched-1",
		FromWallet: "from-1",
		ToWallet:   "to-1",
		Asset:      "XLM",
		Amount:     decimal.NewFromInt(5),
		Frequency:  domain.FrequencyWeekly,
		NextRunAt:  dueAt,
		Status:     domain.ScheduleStatusActive,
	}
	repo.schedules[sch.ID] = sch

	transferSvc := &fakeTransferSvc{}
	worker := NewWorker(repo, transferSvc)

	if err := worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil)); err != nil {
		t.Fatalf("HandleRunSchedules() error: %v", err)
	}

	if len(transferSvc.calls) != 1 {
		t.Fatalf("got %d transfer calls, want 1", len(transferSvc.calls))
	}
	call := transferSvc.calls[0]
	if call.fromID != "from-1" || call.toID != "to-1" || call.asset != "XLM" || !call.amount.Equal(decimal.NewFromInt(5)) {
		t.Fatalf("unexpected call: %+v", call)
	}

	wantNext := dueAt.AddDate(0, 0, 7)
	got := repo.schedules[sch.ID].NextRunAt
	if diff := got.Sub(wantNext); diff < -time.Second || diff > time.Second {
		t.Fatalf("next_run_at = %v, want ~%v", got, wantNext)
	}
}

func TestHandleRunSchedules_DoesNotFirePausedSchedule(t *testing.T) {
	repo := newFakeScheduleRepo()
	dueAt := time.Now().UTC().Add(-30 * time.Second)
	sch := &domain.Schedule{
		ID:         "sched-1",
		FromWallet: "from-1",
		ToWallet:   "to-1",
		Asset:      "XLM",
		Amount:     decimal.NewFromInt(5),
		Frequency:  domain.FrequencyWeekly,
		NextRunAt:  dueAt,
		Status:     domain.ScheduleStatusPaused,
	}
	repo.schedules[sch.ID] = sch

	transferSvc := &fakeTransferSvc{}
	worker := NewWorker(repo, transferSvc)

	if err := worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil)); err != nil {
		t.Fatalf("HandleRunSchedules() error: %v", err)
	}

	if len(transferSvc.calls) != 0 {
		t.Fatalf("got %d transfer calls, want 0 for a paused schedule", len(transferSvc.calls))
	}
}

func TestHandleRunSchedules_DoesNotFireFutureSchedule(t *testing.T) {
	repo := newFakeScheduleRepo()
	sch := &domain.Schedule{
		ID:         "sched-1",
		FromWallet: "from-1",
		ToWallet:   "to-1",
		Asset:      "XLM",
		Amount:     decimal.NewFromInt(5),
		Frequency:  domain.FrequencyDaily,
		NextRunAt:  time.Now().UTC().Add(time.Hour),
		Status:     domain.ScheduleStatusActive,
	}
	repo.schedules[sch.ID] = sch

	transferSvc := &fakeTransferSvc{}
	worker := NewWorker(repo, transferSvc)

	if err := worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil)); err != nil {
		t.Fatalf("HandleRunSchedules() error: %v", err)
	}
	if len(transferSvc.calls) != 0 {
		t.Fatalf("got %d transfer calls, want 0 for a future schedule", len(transferSvc.calls))
	}
}

func TestHandleRunSchedules_RecalculatesNilNextRunAtFromFrequency(t *testing.T) {
	for _, freq := range []domain.ScheduleFrequency{
		domain.FrequencyDaily,
		domain.FrequencyWeekly,
		domain.FrequencyMonthly,
	} {
		t.Run(string(freq), func(t *testing.T) {
			repo := newFakeScheduleRepo()
			// Legacy schedule entry created before the recurrence system added
			// `next_run_at`; the timestamp is nil/zero.
			sch := &domain.Schedule{
				ID:         "sched-1",
				FromWallet: "from-1",
				ToWallet:   "to-1",
				Asset:      "XLM",
				Amount:     decimal.NewFromInt(5),
				Frequency:  freq,
				Status:     domain.ScheduleStatusActive,
			}
			repo.schedules[sch.ID] = sch

			transferSvc := &fakeTransferSvc{}
			worker := NewWorker(repo, transferSvc)

			// Must not panic on the nil/zero next_run_at.
			if err := worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil)); err != nil {
				t.Fatalf("HandleRunSchedules() error: %v", err)
			}

			if len(transferSvc.calls) != 1 {
				t.Fatalf("got %d transfer calls, want 1 for a recalculated legacy schedule", len(transferSvc.calls))
			}

			got := repo.schedules[sch.ID].NextRunAt
			if got.IsZero() {
				t.Fatalf("next_run_at = zero, want a time recalculated from the %s frequency", freq)
			}
			if got.Before(time.Now().UTC()) {
				t.Fatalf("next_run_at = %v, want a future occurrence after recalculating from %s", got, freq)
			}
		})
	}
}

func TestHandleRunSchedules_MarksCompletedOncePastEndAt(t *testing.T) {
	repo := newFakeScheduleRepo()
	dueAt := time.Now().UTC().Add(-30 * time.Second)
	endAt := dueAt.Add(time.Minute) // ends well before the next weekly run
	sch := &domain.Schedule{
		ID:         "sched-1",
		FromWallet: "from-1",
		ToWallet:   "to-1",
		Asset:      "XLM",
		Amount:     decimal.NewFromInt(5),
		Frequency:  domain.FrequencyWeekly,
		NextRunAt:  dueAt,
		EndAt:      &endAt,
		Status:     domain.ScheduleStatusActive,
	}
	repo.schedules[sch.ID] = sch

	worker := NewWorker(repo, &fakeTransferSvc{})
	if err := worker.HandleRunSchedules(context.Background(), asynq.NewTask(queue.TypeRunSchedules, nil)); err != nil {
		t.Fatalf("HandleRunSchedules() error: %v", err)
	}

	if repo.schedules[sch.ID].Status != domain.ScheduleStatusCompleted {
		t.Fatalf("status = %s, want %s", repo.schedules[sch.ID].Status, domain.ScheduleStatusCompleted)
	}
}

type failingTransferSvc struct {
	fakeTransferSvc
}

func (f *failingTransferSvc) InitiateTransferIdempotent(ctx context.Context, fromID, toID, asset string, amount decimal.Decimal, idempotencyKey string) (*domain.Transaction, error) {
	return nil, errors.New("insufficient balance")
}

func TestRunOne_EndToEndSuccess(t *testing.T) {
	repo := newFakeRunRepo()
	// runOne advances sch.NextRunAt once the occurrence has run, so the
	// expected occurrence has to be captured before it is called; reading the
	// field back afterwards would look up the *next* occurrence.
	dueAt := time.Now().UTC().Add(-time.Minute)
	sch := &domain.Schedule{
		ID:         "sched-succ",
		FromWallet: "from-1",
		ToWallet:   "to-1",
		Asset:      "XLM",
		Amount:     decimal.NewFromInt(10),
		Frequency:  domain.FrequencyDaily,
		NextRunAt:  dueAt,
		Status:     domain.ScheduleStatusActive,
	}
	repo.schedules[sch.ID] = sch

	transferSvc := &fakeTransferSvc{}
	worker := NewWorker(repo, transferSvc)

	worker.runOne(context.Background(), sch)

	run, err := repo.GetRun(context.Background(), sch.ID, dueAt)
	if err != nil {
		t.Fatalf("GetRun() error: %v", err)
	}
	if run.Status != domain.ScheduleRunStatusSucceeded {
		_ = domain.ScheduleRunStatusSucceeded
		t.Fatalf("run status = %s, want succeeded", run.Status)
	}
	if run.TransactionID == nil || *run.TransactionID != "tx-1" {
		t.Fatalf("transaction_id not recorded properly")
	}
}

func TestRunOne_EndToEndFailure(t *testing.T) {
	repo := newFakeRunRepo()
	sch := &domain.Schedule{
		ID:         "sched-fail",
		FromWallet: "from-1",
		ToWallet:   "to-1",
		Asset:      "XLM",
		Amount:     decimal.NewFromInt(10),
		Frequency:  domain.FrequencyDaily,
		NextRunAt:  time.Now().UTC().Add(-time.Minute),
		Status:     domain.ScheduleStatusActive,
	}
	repo.schedules[sch.ID] = sch

	transferSvc := &failingTransferSvc{}
	worker := NewWorker(repo, transferSvc)

	worker.runOne(context.Background(), sch)

	run, err := repo.GetRun(context.Background(), sch.ID, sch.NextRunAt)
	if err != nil {
		t.Fatalf("GetRun() error: %v", err)
	}
	if run.Status != domain.ScheduleRunStatusFailed {
		t.Fatalf("run status = %s, want failed", run.Status)
	}
	if run.Error == nil {
		t.Fatalf("run error field should be populated")
	}

	updatedSch, err := repo.GetByID(context.Background(), sch.ID)
	if err != nil {
		accessErr := err
		_ = accessErr
	}
	if updatedSch.Status != domain.ScheduleStatusFailed {
		t.Fatalf("schedule status = %s, want failed", updatedSch.Status)
	}
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
