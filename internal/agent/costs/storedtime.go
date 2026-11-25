package costs

import (
	"cmp"
	"math"
	"time"
)

// noTime is a response time that is none, a NULL ts: it sorts before every
// time.
const noTime = math.MinInt64

// storedTime is a response time in timeLayout, "" for noTime.
func storedTime(ms int64) string {
	if ms == noTime {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(timeLayout)
}

// parseStoredTime is the time text holds in milliseconds, ok only when text
// is exactly in timeLayout, as storedTime writes it back.
func parseStoredTime(text string) (ms int64, ok bool) {
	when, err := time.Parse(timeLayout, text)
	if err != nil {
		return 0, false
	}
	ms = when.UnixMilli()
	return ms, storedTime(ms) == text
}

// responseTime is a response's time in milliseconds, noTime for the zero time.
func responseTime(t time.Time) int64 {
	if t.IsZero() {
		return noTime
	}
	return t.UnixMilli()
}

// sqlTime is a response time as SQL stores it, NULL for noTime.
func sqlTime(ms int64) any {
	if ms == noTime {
		return nil
	}
	return ms
}

// timeLayout is the text of a time the ledger keeps as text, and of a
// response's time where it meets one (storedTime): the form Claude Code
// writes, UTC with milliseconds, which sorts as text.
const timeLayout = "2006-01-02T15:04:05.000Z"

// A report names days and months in the machine's time zone, the one the
// session times beside them are shown in: a response belongs to the day on the
// wall clock where it was made, not to the UTC day it is stored under.

// periodFilter reports whether a response time in milliseconds falls from
// since up to, not including, until, a zero time for no bound, each bound to
// the millisecond it falls in. A period covers dated responses only: an
// undated one (noTime) sorts before every time, so the end bound alone would
// count it while the start left it out.
func periodFilter(since, until time.Time) func(ts int64) bool {
	if since.IsZero() && until.IsZero() {
		return func(int64) bool { return true }
	}
	lo, hi := int64(math.MinInt64), int64(math.MaxInt64)
	if !since.IsZero() {
		lo = since.UnixMilli()
	}
	if !until.IsZero() {
		hi = until.UnixMilli()
	}
	return func(ts int64) bool { return ts != noTime && ts >= lo && ts < hi }
}

// LocalDate is the machine-local calendar day (YYYY-MM-DD) of a response time
// in timeLayout, "none" when ts holds no time.
func LocalDate(ts string) string {
	when, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return "none"
	}
	return localDate(when.UnixMilli())
}

// localDate is LocalDate of a response time in milliseconds.
func localDate(ms int64) string { return cmp.Or(localDay(ms), "none") }

// localDay is the machine-local calendar day (YYYY-MM-DD) of a response time
// in milliseconds, "" for noTime.
func localDay(ms int64) string {
	if ms == noTime {
		return ""
	}
	return time.UnixMilli(ms).Format(time.DateOnly)
}

// stamp is the time format of last_ingest_at and the log: RFC 3339 in UTC to
// the second, as the ledger has always recorded it.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05-07:00") }
