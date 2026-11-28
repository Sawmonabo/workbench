package display

import (
	"fmt"
	"time"
)

// Duration is how long something took, in its two largest units and without
// spaces, so it is one word in a logfmt field too: tenths of a second under
// 10 s ("4.2s"), whole seconds under a minute ("53s"), then minutes and
// seconds ("12m5s"), hours and minutes ("3h10m") and days and hours ("5d7h"),
// each rounded to its smaller unit, which is left out when it is zero ("12m",
// "22h", "3d").
func Duration(d time.Duration) string {
	switch {
	case d.Round(100*time.Millisecond) < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d.Round(time.Second) < time.Minute:
		return fmt.Sprintf("%.0fs", d.Seconds())
	case d.Round(time.Second) < time.Hour:
		d = d.Round(time.Second)
		return units(d/time.Minute, "m", d%time.Minute/time.Second, "s")
	case d.Round(time.Minute) < 24*time.Hour:
		d = d.Round(time.Minute)
		return units(d/time.Hour, "h", d%time.Hour/time.Minute, "m")
	}
	d = d.Round(time.Hour)
	return units(d/(24*time.Hour), "d", d%(24*time.Hour)/time.Hour, "h")
}

// units is large of its unit, then small of the smaller unit unless it is
// zero: "12m5s", "12m".
func units(large time.Duration, largeUnit string, small time.Duration, smallUnit string) string {
	if small == 0 {
		return fmt.Sprintf("%d%s", large, largeUnit)
	}
	return fmt.Sprintf("%d%s%d%s", large, largeUnit, small, smallUnit)
}
