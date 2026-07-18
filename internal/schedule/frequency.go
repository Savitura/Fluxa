package schedule

import (
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

// AddInterval returns the next occurrence of t for the given frequency.
func AddInterval(t time.Time, freq domain.ScheduleFrequency) time.Time {
	switch freq {
	case domain.FrequencyDaily:
		return t.AddDate(0, 0, 1)
	case domain.FrequencyWeekly:
		return t.AddDate(0, 0, 7)
	case domain.FrequencyMonthly:
		return t.AddDate(0, 1, 0)
	default:
		return t
	}
}
