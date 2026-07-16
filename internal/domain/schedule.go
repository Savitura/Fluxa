package domain

import (
	"time"

	"github.com/shopspring/decimal"
)

type ScheduleFrequency string
type ScheduleStatus string

const (
	FrequencyDaily   ScheduleFrequency = "daily"
	FrequencyWeekly  ScheduleFrequency = "weekly"
	FrequencyMonthly ScheduleFrequency = "monthly"

	ScheduleStatusActive    ScheduleStatus = "active"
	ScheduleStatusPaused    ScheduleStatus = "paused"
	ScheduleStatusCancelled ScheduleStatus = "cancelled"
	ScheduleStatusCompleted ScheduleStatus = "completed"
)

type Schedule struct {
	ID         string
	TenantID   *string
	FromWallet string
	ToWallet   string
	Asset      string
	Amount     decimal.Decimal
	Frequency  ScheduleFrequency
	NextRunAt  time.Time
	EndAt      *time.Time
	Status     ScheduleStatus
	CreatedAt  time.Time
	UpdatedAt  time.Time
}
