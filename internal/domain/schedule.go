package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type ScheduleFrequency string
type ScheduleStatus string
type MissedRunPolicy string

// ScheduleRunStatus represents the lifecycle state of a single scheduled-payout
// occurrence.  Each (schedule_id, expected_run_at) pair produces exactly one
// run record whose status advances through this set.
type ScheduleRunStatus string

const (
	MissedRunPolicySkip    MissedRunPolicy = "skip"
	MissedRunPolicyRunOnce MissedRunPolicy = "run_once"

	FrequencyDaily   ScheduleFrequency = "daily"
	FrequencyWeekly  ScheduleFrequency = "weekly"
	FrequencyMonthly ScheduleFrequency = "monthly"

	ScheduleStatusActive     ScheduleStatus = "active"
	ScheduleStatusProcessing ScheduleStatus = "processing"
	ScheduleStatusFailed     ScheduleStatus = "failed"
	ScheduleStatusPaused     ScheduleStatus = "paused"
	ScheduleStatusCancelled  ScheduleStatus = "cancelled"
	ScheduleStatusCompleted  ScheduleStatus = "completed"

	// ScheduleRunStatusPending — the occurrence has been claimed by the worker
	// and a run record has been created, but payout initiation has not started.
	ScheduleRunStatusPending ScheduleRunStatus = "pending"

	// ScheduleRunStatusRunning — payout initiation is in progress (or the
	// worker may have crashed in the middle of it).  A run stuck in this state
	// after a reasonable grace period should be treated as ambiguous: the
	// transfer may or may not have been initiated.
	ScheduleRunStatusRunning ScheduleRunStatus = "running"

	// ScheduleRunStatusSucceeded — the transfer was successfully initiated and
	// the transaction_id has been recorded.
	ScheduleRunStatusSucceeded ScheduleRunStatus = "succeeded"

	// ScheduleRunStatusFailed — payout initiation failed.  The error field
	// carries a user-safe description of the failure.
	ScheduleRunStatusFailed ScheduleRunStatus = "failed"

	// ScheduleRunStatusSkipped — the occurrence was intentionally skipped
	// (e.g., the schedule was paused at run time).
	ScheduleRunStatusSkipped ScheduleRunStatus = "skipped"

	// ScheduleRunStatusCancelled — the schedule was cancelled before this
	// occurrence could complete.
	ScheduleRunStatusCancelled ScheduleRunStatus = "cancelled"
)

type Schedule struct {
	ID         string
	TenantID   *string
	FromWallet string
	ToWallet   string
	Asset           string
	Amount          decimal.Decimal
	Frequency       ScheduleFrequency
	Timezone        string
	MissedRunPolicy MissedRunPolicy
	NextRunAt       time.Time
	EndAt           *time.Time
	Status     ScheduleStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// ScheduleRun is a durable record of a single scheduled-payout occurrence.
// The pair (ScheduleID, ExpectedRunAt) is unique: the database enforces this
// with UNIQUE(schedule_id, expected_run_at), so concurrent workers cannot
// produce two run records for the same occurrence.
type ScheduleRun struct {
	ID            string
	ScheduleID    string
	TenantID      *string
	ExpectedRunAt time.Time
	Status        ScheduleRunStatus
	TransactionID *string // set when a transfer was successfully initiated
	Error         *string // user-safe error message; set on failed runs
	StartedAt     *time.Time
	CompletedAt   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}
