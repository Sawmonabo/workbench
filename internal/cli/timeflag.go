package cli

import (
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// timeFlagForms are the values a --since or --until flag takes, for its help.
const timeFlagForms = "a duration ago (10m, 2h), today, yesterday, or a time in this " +
	"machine's time zone (2026-10-16, \"2026-10-16 21:00\", 21:00) or RFC 3339"

// parseTimeFlag reads a --since or --until value as `docker logs --since` and
// `journalctl --since` do: a Go duration is that long before now
// (journalctl's "-2h" counts the same), today and yesterday are midnight, and
// a date or time without a zone is in this machine's: a date alone is its
// midnight, a time alone is today. day reports a whole day (today, yesterday
// or a date alone), which --until includes up to the next midnight. Empty is
// the zero time, no bound.
func parseTimeFlag(flag, value string, now time.Time) (t time.Time, day bool, err error) {
	if value == "" {
		return time.Time{}, false, nil
	}
	if d, err := time.ParseDuration(value); err == nil {
		return now.Add(-d.Abs()), false, nil
	}
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	switch value {
	case "today":
		return midnight, true, nil
	case "yesterday":
		return midnight.AddDate(0, 0, -1), true, nil
	}
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, false, nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, value, time.Local); err == nil {
		return t, true, nil
	}
	for _, layout := range []string{
		"2006-01-02 15:04", time.DateTime, "2006-01-02T15:04", "2006-01-02T15:04:05",
	} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t, false, nil
		}
	}
	for _, layout := range []string{"15:04", time.TimeOnly} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return time.Date(now.Year(), now.Month(), now.Day(),
				t.Hour(), t.Minute(), t.Second(), 0, time.Local), false, nil
		}
	}
	return time.Time{}, false, operation.Fail(
		operation.ExitInvalid,
		"invocation",
		flag+" needs a duration (10m, 2h), today, yesterday, a local date or time "+
			"(2026-10-16, \"2026-10-16 21:00\", 21:00) or an RFC 3339 time",
	)
}
