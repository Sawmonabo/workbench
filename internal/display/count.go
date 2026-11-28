package display

import (
	"strconv"
	"strings"
)

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

// Size is size bytes in the largest binary unit that divides it: "1 MiB",
// "64 KiB", "900 bytes".
func Size(size int64) string {
	switch {
	case size != 0 && size%(1<<20) == 0:
		return strconv.FormatInt(size>>20, 10) + " MiB"
	case size != 0 && size%(1<<10) == 0:
		return strconv.FormatInt(size>>10, 10) + " KiB"
	}
	return Count(int(size), "byte")
}

// Number is n with a comma between each group of three digits: "12,096".
func Number(n int64) string { return Decimal(float64(n), 0) }

// Decimal is v to places decimal places, with a comma between each group of
// three digits of its whole part: "1,234.50".
func Decimal(v float64, places int) string {
	s := strconv.FormatFloat(v, 'f', places, 64)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	whole, fraction, _ := strings.Cut(s, ".")
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + "," + whole[i:]
	}
	if fraction != "" {
		whole += "." + fraction
	}
	return sign + whole
}
