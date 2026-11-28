package machine

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Sawmonabo/workbench/internal/release"
)

// plainVersion is three dotted numbers; see [ValidVersion].
var plainVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// ValidVersion reports whether version is three dotted numbers, such as
// 1.2.3: the only shape a management or project CI pin may take, and a Node
// version newerVersion can compare.
func ValidVersion(version string) bool { return plainVersion.MatchString(version) }

// newerVersion reports whether dotted numeric version a is newer than b.
// Versions with a different number of parts are not compared, as in Homebrew,
// and parts that are not plain numbers keep Homebrew's record.
func newerVersion(a, b string) bool {
	left, right := strings.Split(a, "."), strings.Split(b, ".")
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		x, errX := strconv.ParseUint(left[i], 10, 64)
		y, errY := strconv.ParseUint(right[i], 10, 64)
		if errX != nil || errY != nil {
			return false
		}
		if x != y {
			return x > y
		}
	}
	return false
}

// agentNewer reports whether version a is newer than b, and whether the two
// could be compared at all: both must be versions [release.ParseVersion]
// reads. A prerelease ranks below its own release, as in Semantic Versioning,
// so 0.163.0 is newer than 0.163.0-alpha.3; two prereleases of the same
// release are not ranked, so neither is newer.
func agentNewer(a, b string) (newer, comparable bool) {
	left, okA := release.ParseVersion(a)
	right, okB := release.ParseVersion(b)
	if !okA || !okB {
		return false, false
	}
	if left.Core != right.Core {
		return slices.Compare(left.Core[:], right.Core[:]) > 0, true
	}
	return left.Pre == "" && right.Pre != "", true
}
