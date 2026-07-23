package schedule

import (
	"context"
	"testing"
	"time"

	"github.com/fluxa/fluxa/internal/domain"
	"github.com/shopspring/decimal"
)

type fakeScheduleRepo struct {
	schedules map[string]*domain.Schedule
}

func newFakeScheduleRepo() *fakeScheduleRepo {
	return &fakeScheduleRepo{schedules: make(map[string]*domain.Schedule)}
}

func (f *fakeScheduleRepo) Create(_ context.Context, s *domain.Schedule) error {
	f.schedules[s.ID] = s
	return nil
}

func (f *fakeScheduleRepo) GetByID(_ context.Context, id string) (*domain.Schedule, error) {
	s, ok := f.schedules[id]
	if !ok {
		return nil, domain.ErrScheduleNotFound
	}
	return s, nil
}

func (f *fakeScheduleRepo) List(_ context.Context) ([]*domain.Schedule, error) {
	var out []*domain.Schedule
	for _, s := range f.schedules {
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeScheduleRepo) Update(_ context.Context, s *domain.Schedule) error {
	f.schedules[s.ID] = s
	return nil
}

func (f *fakeScheduleRepo) ListDue(_ context.Context, now time.Time) ([]*domain.Schedule, error) {
	var out []*domain.Schedule
	for _, s := range f.schedules {
		if s.Status == domain.ScheduleStatusActive && !s.NextRunAt.After(now) {
			out = append(out, s)
		}
	}
	return out, nil
}

type fakeWalletRepo struct {
	wallets map[string]*domain.Wallet
}

func newFakeWalletRepo(ids ...string) *fakeWalletRepo {
	w := &fakeWalletRepo{wallets: make(map[string]*domain.Wallet)}
	for _, id := range ids {
		w.wallets[id] = &domain.Wallet{ID: id, PublicKey: "G" + id}
	}
	return w
}

func (f *fakeWalletRepo) Create(_ context.Context, w *domain.Wallet) error {
	f.wallets[w.ID] = w
	return nil
}

func (f *fakeWalletRepo) GetByID(_ context.Context, id string) (*domain.Wallet, error) {
	w, ok := f.wallets[id]
	if !ok {
		return nil, domain.ErrWalletNotFound
	}
	return w, nil
}

func (f *fakeWalletRepo) GetByPublicKey(_ context.Context, pubKey string) (*domain.Wallet, error) {
	return nil, domain.ErrWalletNotFound
}

func (f *fakeWalletRepo) List(_ context.Context, limit, offset int) ([]*domain.Wallet, error) {
	return nil, nil
}

func (f *fakeWalletRepo) UpsertBalance(_ context.Context, walletID, assetCode, issuer string, balance decimal.Decimal) error {
	return nil
}

func (f *fakeWalletRepo) UpdateSyncCursor(_ context.Context, walletID, cursor string) error {
	return nil
}

func TestCreate_RejectsUnknownWallet(t *testing.T) {
	svc := NewService(newFakeScheduleRepo(), newFakeWalletRepo("from-1"))

	_, err := svc.Create(context.Background(), CreateInput{
		FromWalletID: "from-1",
		ToWalletID:   "missing-wallet",
		Asset:        "XLM",
		Amount:       decimal.NewFromInt(10),
		Frequency:    domain.FrequencyWeekly,
		StartAt:      time.Now().UTC(),
	})
	if err == nil {
		t.Fatal("expected error for unknown destination wallet")
	}
}

func TestUpdate_PausedSchedule_IsExcludedFromDue(t *testing.T) {
	repo := newFakeScheduleRepo()
	svc := NewService(repo, newFakeWalletRepo("from-1", "to-1"))

	sch, err := svc.Create(context.Background(), CreateInput{
		FromWalletID: "from-1",
		ToWalletID:   "to-1",
		Asset:        "XLM",
		Amount:       decimal.NewFromInt(10),
		Frequency:    domain.FrequencyWeekly,
		StartAt:      time.Now().UTC().Add(-time.Minute), // already due
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}

	paused := domain.ScheduleStatusPaused
	if _, err := svc.Update(context.Background(), sch.ID, UpdateInput{Status: &paused}); err != nil {
		t.Fatalf("Update() error: %v", err)
	}

	due, err := repo.ListDue(context.Background(), time.Now().UTC())
	if err != nil {
		t.Fatalf("ListDue() error: %v", err)
	}
	for _, d := range due {
		if d.ID == sch.ID {
			t.Fatal("paused schedule should not be returned by ListDue")
		}
	}
}

func TestUpdate_ResumeSkipsElapsedOccurrences(t *testing.T) {
	repo := newFakeScheduleRepo()
	svc := NewService(repo, newFakeWalletRepo("from-1", "to-1"))

	// Started three weeks ago and paused immediately, so several weekly
	// occurrences have elapsed while paused.
	sch, _ := svc.Create(context.Background(), CreateInput{
		FromWalletID: "from-1",
		ToWalletID:   "to-1",
		Asset:        "XLM",
		Amount:       decimal.NewFromInt(10),
		Frequency:    domain.FrequencyWeekly,
		StartAt:      time.Now().UTC().Add(-21 * 24 * time.Hour),
	})
	paused := domain.ScheduleStatusPaused
	_, _ = svc.Update(context.Background(), sch.ID, UpdateInput{Status: &paused})

	active := domain.ScheduleStatusActive
	resumed, err := svc.Update(context.Background(), sch.ID, UpdateInput{Status: &active})
	if err != nil {
		t.Fatalf("Update() error: %v", err)
	}
	if !resumed.NextRunAt.After(time.Now().UTC()) {
		t.Fatalf("next_run_at = %v, want a time in the future after resuming", resumed.NextRunAt)
	}
}
