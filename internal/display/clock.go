package display

import (
	"fmt"
	"time"
)

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

// StampWidth is the width of every [Stamp].
const StampWidth = len("Jan _2 12:04:05 PM")

// Stamp is t on the machine's clock to the second for a line of a log,
// StampWidth wide at any time so the fields after it line up: its day padded
// as [time.Stamp] pads it, then its time of day with the hour padded the same
// way, which no Go layout does for a 12-hour hour ("Oct  7  2:41:05 PM",
// "Oct 17 12:41:05 PM").
func Stamp(t time.Time) string {
	t = t.Local()
	return t.Format("Jan _2") + fmt.Sprintf(" %11s", t.Format(TimeSeconds))
}
