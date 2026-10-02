package operation

import (
	"fmt"
	"slices"
	"strings"
)

// diffContext is the unchanged lines shown around each change.
const diffContext = 3

// maxEditScript bounds the work a diff does. Texts that differ by more lines
// than this are shown as one replaced block: still correct, never slow.
const maxEditScript = 1000

// DiffLines is the line diff of two text bodies: how many lines were added and
// removed, and the changes as unified-diff text ("@@" headers, then lines
// prefixed " ", "-" or "+"). A line differs when its text or its line ending
// does; carriage returns are dropped from the shown text only.
//
// A line that moved is shown as the removed line where it left and the added
// line where it landed, and counted as both: a merge that moves a rule from one
// list or table to another changes what the file means, so the review must show
// it.
func DiffLines(before, after []byte) (added, removed int, lines []string) {
	a, b := splitLines(before), splitLines(after)
	start := 0
	for start < len(a) && start < len(b) && a[start] == b[start] {
		start++
	}
	endA, endB := len(a), len(b)
	for endA > start && endB > start && a[endA-1] == b[endB-1] {
		endA--
		endB--
	}
	script := editScript(a[start:endA], b[start:endB])
	ops := make([]diffOp, 0, len(a)+len(script))
	for _, line := range a[:start] {
		ops = append(ops, diffOp{'=', line})
	}
	ops = append(ops, script...)
	for _, line := range a[endA:] {
		ops = append(ops, diffOp{'=', line})
	}
	for _, op := range script {
		switch op.kind {
		case '+':
			added++
		case '-':
			removed++
		}
	}
	return added, removed, hunks(ops)
}

type diffOp struct {
	// kind is '=' unchanged, '-' removed or '+' added.
	kind byte
	text string
}

func splitLines(data []byte) []string {
	var lines []string
	for line := range strings.Lines(string(data)) {
		lines = append(lines, line)
	}
	return lines
}

// editScript is a shortest edit script (Myers) from a to b, which share no
// first or last line; or one replaced block when it is longer than
// maxEditScript.
func editScript(a, b []string) []diffOp {
	if len(a) == 0 || len(b) == 0 {
		return replaced(a, b)
	}
	maxD := min(len(a)+len(b), maxEditScript)
	offset := maxD + 1
	v := make([]int, 2*maxD+3)
	var trace [][]int
	for d := 0; d <= maxD; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < len(a) && y < len(b) && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= len(a) && y >= len(b) {
				return backtrack(a, b, trace, offset)
			}
		}
	}
	return replaced(a, b)
}

// backtrack walks the recorded rounds from the end back to the start.
func backtrack(a, b []string, trace [][]int, offset int) []diffOp {
	var reversed []diffOp
	x, y := len(a), len(b)
	for d, v := range slices.Backward(trace) {
		k := x - y
		previousK := k - 1
		if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
			previousK = k + 1
		}
		previousX := v[offset+previousK]
		previousY := previousX - previousK
		for x > previousX && y > previousY {
			reversed = append(reversed, diffOp{'=', a[x-1]})
			x--
			y--
		}
		if d > 0 {
			if x == previousX {
				reversed = append(reversed, diffOp{'+', b[previousY]})
			} else {
				reversed = append(reversed, diffOp{'-', a[previousX]})
			}
		}
		x, y = previousX, previousY
	}
	ops := make([]diffOp, len(reversed))
	for i, op := range reversed {
		ops[len(reversed)-1-i] = op
	}
	return ops
}

func replaced(a, b []string) []diffOp {
	ops := make([]diffOp, 0, len(a)+len(b))
	for _, line := range a {
		ops = append(ops, diffOp{'-', line})
	}
	for _, line := range b {
		ops = append(ops, diffOp{'+', line})
	}
	return ops
}

// hunks renders ops as unified diff text with diffContext lines around each
// change; changes whose context touches share one hunk.
func hunks(ops []diffOp) []string {
	var out []string
	from, to := -1, -1 // the current hunk is ops[from:to]
	flush := func() {
		if from >= 0 {
			out = append(out, hunk(ops, from, to)...)
		}
	}
	for i, op := range ops {
		if op.kind != '+' && op.kind != '-' {
			continue
		}
		if from >= 0 && i-to <= diffContext {
			to = min(i+diffContext+1, len(ops))
			continue
		}
		flush()
		from, to = max(i-diffContext, 0), min(i+diffContext+1, len(ops))
	}
	flush()
	return out
}

func hunk(ops []diffOp, from, to int) []string {
	startA, startB := 1, 1
	for _, op := range ops[:from] {
		if inOld(op) {
			startA++
		}
		if inNew(op) {
			startB++
		}
	}
	var countA, countB int
	body := make([]string, 0, to-from+1)
	for _, op := range ops[from:to] {
		if inOld(op) {
			countA++
		}
		if inNew(op) {
			countB++
		}
		prefix := " "
		if op.kind != '=' {
			prefix = string(op.kind)
		}
		body = append(body, prefix+strings.TrimRight(op.text, "\r\n"))
	}
	// An empty side is positioned at the line before the hunk.
	if countA == 0 {
		startA--
	}
	if countB == 0 {
		startB--
	}
	header := fmt.Sprintf("@@ -%s +%s @@", span(startA, countA), span(startB, countB))
	return append([]string{header}, body...)
}

// inOld and inNew say which file a line belongs to.
func inOld(op diffOp) bool { return op.kind != '+' }
func inNew(op diffOp) bool { return op.kind != '-' }

func span(start, count int) string {
	if count == 1 {
		return fmt.Sprint(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// FileDiff is [DiffLines] for two target images: ok is false unless both are
// regular text files or absent, so a link, a folder or binary content has no
// line diff. A created or removed file is all added or all removed lines.
func FileDiff(before, after Image) (added, removed int, lines []string, ok bool) {
	for _, image := range []Image{before, after} {
		if (image.Kind != ImageFile && image.Kind != ImageAbsent) || !isText(image.Data) {
			return 0, 0, nil, false
		}
	}
	added, removed, lines = DiffLines(before.Data, after.Data)
	return added, removed, lines, true
}
