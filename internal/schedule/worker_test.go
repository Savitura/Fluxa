package schedule

import (
	"context"
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
