package display

import (
	"fmt"
	"time"
)

// Duration is how long something took, in its two largest units and without
// spaces, so it is one word in a logfmt field too: tenths of a second under
// 10 s ("4.2s"), whole seconds under a minute ("53s"), then minutes and
// seconds ("12m5s"), hours and minutes ("3h10m") and days and hours ("5d7h"),
// each rounded to its smaller unit.
func Duration(d time.Duration) string {
	switch {
	case d.Round(100*time.Millisecond) < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d.Round(time.Second) < time.Minute:
		return fmt.Sprintf("%.0fs", d.Seconds())
	case d.Round(time.Second) < time.Hour:
		d = d.Round(time.Second)
		return fmt.Sprintf("%dm%ds", d/time.Minute, d%time.Minute/time.Second)
	case d.Round(time.Minute) < 24*time.Hour:
		d = d.Round(time.Minute)
		return fmt.Sprintf("%dh%dm", d/time.Hour, d%time.Hour/time.Minute)
	}
	d = d.Round(time.Hour)
	return fmt.Sprintf("%dd%dh", d/(24*time.Hour), d%(24*time.Hour)/time.Hour)
}
