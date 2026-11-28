package release

import (
	"strconv"
	"strings"
)

// Newer reports whether release tag a is a later version than b, both of the
// form vMAJOR.MINOR.PATCH with an optional -prerelease, which comes before the
// release it precedes (v0.1.8-dev.1 is newer than v0.1.7, older than v0.1.8).
// Tags that do not parse are never newer.
func Newer(a, b string) bool {
	left, okA := parseTag(a)
	right, okB := parseTag(b)
	if !okA || !okB {
		return false
	}
	for i := range 3 {
		if left.Core[i] != right.Core[i] {
			return left.Core[i] > right.Core[i]
		}
	}
	switch {
	case left.Pre == right.Pre:
		return false
	case left.Pre == "":
		return true
	case right.Pre == "":
		return false
	}
	return prereleaseNewer(strings.Split(left.Pre, "."), strings.Split(right.Pre, "."))
}

// Version is a semantic version: its three numbers and its prerelease, ""
// for a release.
type Version struct {
	Core [3]uint64
	Pre  string
}

// ParseVersion parses MAJOR.MINOR.PATCH with an optional -prerelease and
// +build, which it drops; ok is false for anything else.
func ParseVersion(s string) (v Version, ok bool) {
	core, pre, _ := strings.Cut(s, "-")
	core, _, _ = strings.Cut(core, "+")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil {
			return v, false
		}
		v.Core[i] = n
	}
	v.Pre, _, _ = strings.Cut(pre, "+")
	return v, true
}

// parseTag parses a release tag, a [ParseVersion] version after a v.
func parseTag(s string) (Version, bool) {
	rest, ok := strings.CutPrefix(s, "v")
	if !ok {
		return Version{}, false
	}
	return ParseVersion(rest)
}

// prereleaseNewer compares dot-separated prerelease identifiers as semantic
// versioning does: numbers numerically and below words, words by text, and a
// longer list newer when every shared identifier is equal.
func prereleaseNewer(a, b []string) bool {
	for i := range min(len(a), len(b)) {
		x, errX := strconv.ParseUint(a[i], 10, 64)
		y, errY := strconv.ParseUint(b[i], 10, 64)
		switch {
		case errX == nil && errY == nil && x != y:
			return x > y
		case errX == nil && errY != nil:
			return false
		case errX != nil && errY == nil:
			return true
		case errX != nil && a[i] != b[i]:
			return a[i] > b[i]
		}
	}
	return len(a) > len(b)
}
