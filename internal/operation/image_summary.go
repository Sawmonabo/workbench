package operation

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Sawmonabo/workbench/internal/display"
)

// ChangeSummary describes before becoming after without showing content: line
// counts, sizes, modes and link targets. last is the image Workbench last left
// at the path, or nil when it never wrote one; the summary says when the
// current target differs from it, so approval never overwrites local edits
// unnoticed.
func ChangeSummary(before, after Image, last *Image) string {
	parts := []string{contentSummary(before, after)}
	if before.Kind == after.Kind && before.Kind != ImageAbsent && before.Mode != after.Mode {
		parts = append(parts, fmt.Sprintf("mode %04o → %04o", before.Mode, after.Mode))
	}
	switch {
	case !EditedOutside(before, last):
	case last == nil:
		parts = append(parts, "not previously written by Workbench")
	default:
		parts = append(parts, "edited outside Workbench since it last wrote it")
	}
	return strings.Join(
		slices.DeleteFunc(parts, func(part string) bool { return part == "" }),
		"; ",
	)
}

// EditedOutside reports whether an existing target is not what Workbench last
// left there: it never wrote the path, or its content has changed since.
// last is the image Workbench last left at the path, or nil.
func EditedOutside(before Image, last *Image) bool {
	return before.Kind != ImageAbsent && (last == nil || !sameImageContent(*last, before))
}

func contentSummary(before, after Image) string {
	switch {
	case before.Kind != after.Kind && before.Kind != ImageAbsent && after.Kind != ImageAbsent:
		return string(before.Kind) + " → " + string(after.Kind)
	case after.Kind == ImageSymlink && before.Link != after.Link:
		return "link → " + after.Link
	case after.Kind == ImageDirectory || before.Kind == ImageDirectory:
		return ""
	case after.Kind == ImageAbsent:
		return "removes " + textSize(before.Data)
	case before.Kind == ImageAbsent:
		return "new, " + textSize(after.Data)
	case bytes.Equal(before.Data, after.Data):
		return ""
	case !isText(before.Data) || !isText(after.Data):
		return fmt.Sprintf("binary, %d → %d bytes", len(before.Data), len(after.Data))
	}
	added, removed, _ := diffLines(before.Data, after.Data)
	return fmt.Sprintf("+%d −%d lines", added, removed)
}

// textSize is "N lines" for text and "N bytes" otherwise.
func textSize(data []byte) string {
	if !isText(data) {
		return display.Count(len(data), "byte")
	}
	lines := 0
	for range strings.Lines(string(data)) {
		lines++
	}
	return display.Count(lines, "line")
}

func isText(data []byte) bool { return utf8.Valid(data) && !bytes.ContainsRune(data, 0) }
