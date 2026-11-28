package platform

import "strings"

// hexDigits are the digits of a UUID, in either case.
const hexDigits = "0123456789abcdefABCDEF"

// BootID identifies this start of the system: the UUID the kernel makes anew
// at each boot, which no clock change moves, in upper case on macOS and lower
// case on Linux. The error is the system's when it cannot be read, or says
// that it read empty.
func BootID() (string, error) { return bootID() }

// BootIDLength is the length of a start identifier [BootID] gives: a UUID.
const BootIDLength = 36

// IsBootID reports whether text has the shape of a start identifier
// [BootID] gives, a UUID and nothing else: 36 characters, hexadecimal digits
// in groups of 8, 4, 4, 4 and 12 joined by hyphens, in either case, since
// kern.bootsessionuuid is upper case and Linux's boot_id lower case.
func IsBootID(text string) bool {
	if len(text) != BootIDLength {
		return false
	}
	for i := range len(text) {
		c := text[i]
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !strings.ContainsRune(hexDigits, rune(c)) {
				return false
			}
		}
	}
	return true
}
