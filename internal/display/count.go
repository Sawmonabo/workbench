package display

import "strconv"

// Count is n and noun, the noun plural unless n is 1: "1 line", "3 lines".
func Count(n int, noun string) string { return strconv.Itoa(n) + " " + Plural(n, noun) }

// Plural is noun as [Count] writes it after n, for a caller that styles the
// number apart: noun itself for 1, noun with an s otherwise.
func Plural(n int, noun string) string {
	if n != 1 {
		noun += "s"
	}
	return noun
}
