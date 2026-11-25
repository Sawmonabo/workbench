package display

import "time"

// Layouts of a time on the machine's clock, 12-hour with AM or PM, for
// [time.Time.Format] of the time in the machine's zone (see [time.Time.Local]).
const (
	// Time is a time of day: "5:07 PM".
	Time = "3:04 PM"
	// TimeSeconds is a time of day to the second: "5:07:09 PM".
	TimeSeconds = "3:04:05 PM"
	// Day is a day without its year: "Nov 18".
	Day = "Jan 2"
	// DayTime is a time of day with its day: "Nov 18 5:07 PM".
	DayTime = Day + " " + Time
	// DayTimeSeconds is a time of day to the second with its day:
	// "Nov 18 5:07:09 PM".
	DayTimeSeconds = Day + " " + TimeSeconds
	// Date is a day with its year: "Nov 18, 2026".
	Date = "Jan 2, 2006"
)

// Clock is t on the machine's clock: its time of day ("5:07 PM"), with its
// day ("Nov 18 5:07 PM") when that is not the day of now.
func Clock(t, now time.Time) string {
	t, now = t.Local(), now.Local()
	if t.Format(time.DateOnly) != now.Format(time.DateOnly) {
		return t.Format(DayTime)
	}
	return t.Format(Time)
}
