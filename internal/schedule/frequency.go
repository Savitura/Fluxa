package schedule

import (
	"time"

	"github.com/fluxa/fluxa/internal/domain"
)

// AddInterval returns the next occurrence of t for the given frequency in the given timezone.
func AddInterval(t time.Time, freq domain.ScheduleFrequency, tz string) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	local := t.In(loc)
	switch freq {
	case domain.FrequencyDaily:
		local = local.AddDate(0, 0, 1)
	case domain.FrequencyWeekly:
		local = local.AddDate(0, 0, 7)
	case domain.FrequencyMonthly:
		local = local.AddDate(0, 1, 0)
	}
	return local.UTC()
}
