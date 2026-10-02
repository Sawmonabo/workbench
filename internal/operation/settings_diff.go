package operation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/tailscale/hujson"
)

// SettingChange is one changed leaf of a merged settings file. Path is the key
// path ("hooks.SessionStart[0].hooks[0].timeout"); Before and After are the
// leaf's values as text, empty on the side where the leaf is absent.
type SettingChange struct {
	Path          string
	Before, After string
	// Follows is, for a plain item of a list, the item before it in the old
	// and new lists, so a caller can tell a value that follows a credential
	// flag ("--token", "value") though a list of plain values is compared as a
	// set.
	Follows []string
	// Added and Removed mark a leaf present on only one side; Reordered marks
	// a list whose items are the same in a different order.
	Added, Removed, Reordered bool
}

// SettingsDiff is the semantic difference of two JSON (comments and trailing
// commas tolerated, as the global VS Code and Claude Code files may have them)
// or TOML bodies, chosen by format ("json" or "toml"): one change per leaf
// whose value differs, in path order. A merge that re-serializes a whole file
// reorders its keys, so a line diff of it hides what changed; this shows only
// the settings. rewritten is true when the lines differ by more than those
// settings (the file is re-ordered or re-laid-out). ok is false unless both
// bodies parse, so the caller falls back to the line diff.
//
// An array whose items are all plain values is compared as a set of items, so
// a rule merged in at its front does not shift every other row; arrays that
// hold objects or arrays are compared item by index.
func SettingsDiff(
	format string,
	before, after []byte,
) (changes []SettingChange, rewritten, ok bool) {
	old, ok := parseSettings(format, before)
	if !ok {
		return nil, false, false
	}
	next, ok := parseSettings(format, after)
	if !ok {
		return nil, false, false
	}
	collectChanges("", old, next, &changes)
	if len(changes) == 0 && bytes.Equal(before, after) {
		return nil, false, true
	}
	// The layout-independent diff is the floor of what any rendering changes.
	// A body that differs by more lines than that was rewritten.
	canonicalBefore, ok1 := canonicalSettings(format, old)
	canonicalAfter, ok2 := canonicalSettings(format, next)
	if !ok1 || !ok2 {
		return changes, true, true
	}
	ra, rr, _ := DiffLines(before, after)
	ca, cr, _ := DiffLines(canonicalBefore, canonicalAfter)
	return changes, ra+rr > ca+cr, true
}

func parseSettings(format string, data []byte) (any, bool) {
	var value any
	switch format {
	case "toml":
		if toml.Unmarshal(data, &value) != nil {
			return nil, false
		}
	case "json":
		standard, err := hujson.Standardize(data)
		if err != nil {
			return nil, false
		}
		decoder := json.NewDecoder(bytes.NewReader(standard))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil || decoder.More() {
			return nil, false
		}
	default:
		return nil, false
	}
	if _, isMap := value.(map[string]any); !isMap {
		return nil, false
	}
	return value, true
}

// canonicalSettings is a parsed document laid out one way whatever its source
// layout: sorted keys, one leaf per line.
func canonicalSettings(format string, value any) ([]byte, bool) {
	var (
		out []byte
		err error
	)
	if format == "toml" {
		out, err = toml.Marshal(value)
	} else {
		out, err = json.MarshalIndent(value, "", "  ")
	}
	return out, err == nil
}

// collectChanges walks old and next together and appends one change per leaf
// that differs.
func collectChanges(path string, old, next any, out *[]SettingChange) {
	oldMap, oldIsMap := old.(map[string]any)
	nextMap, nextIsMap := next.(map[string]any)
	if oldIsMap && nextIsMap {
		keys := maps.Clone(oldMap)
		maps.Copy(keys, nextMap)
		for _, key := range slices.Sorted(maps.Keys(keys)) {
			collectChanges(joinKey(path, key), oldMap[key], nextMap[key], out)
		}
		return
	}
	oldList, oldIsList := old.([]any)
	nextList, nextIsList := next.([]any)
	if oldIsList && nextIsList {
		if plainList(oldList) && plainList(nextList) {
			collectPlainList(path, oldList, nextList, out)
			return
		}
		for i := range max(len(oldList), len(nextList)) {
			var o, n any
			if i < len(oldList) {
				o = oldList[i]
			}
			if i < len(nextList) {
				n = nextList[i]
			}
			item := path + "[" + strconv.Itoa(i) + "]"
			first := len(*out)
			collectChanges(item, o, n, out)
			for j := first; j < len(*out); j++ {
				if (*out)[j].Path == item {
					(*out)[j].Follows = []string{
						previousText(oldList, i),
						previousText(nextList, i),
					}
				}
			}
		}
		return
	}
	switch {
	case old == nil && next != nil:
		addLeaves(path, next, false, out)
	case old != nil && next == nil:
		addLeaves(path, old, true, out)
	case old != nil && !equalLeaf(old, next):
		if !oldIsMap && !oldIsList && !nextIsMap && !nextIsList {
			*out = append(*out, SettingChange{
				Path: path, Before: leafText(old), After: leafText(next),
			})
			return
		}
		// A leaf became a table or list, or the reverse.
		addLeaves(path, old, true, out)
		addLeaves(path, next, false, out)
	}
}

// addLeaves lists every leaf of a value that exists on one side only.
func addLeaves(path string, value any, removed bool, out *[]SettingChange) {
	switch v := value.(type) {
	case map[string]any:
		if len(v) == 0 {
			break
		}
		for _, key := range slices.Sorted(maps.Keys(v)) {
			addLeaves(joinKey(path, key), v[key], removed, out)
		}
		return
	case []any:
		if len(v) == 0 {
			break
		}
		for i, item := range v {
			addLeaves(path+"["+strconv.Itoa(i)+"]", item, removed, out)
		}
		return
	}
	change := SettingChange{Path: path, Added: !removed, Removed: removed}
	if removed {
		change.Before = leafText(value)
	} else {
		change.After = leafText(value)
	}
	*out = append(*out, change)
}

// plainList reports whether every item is a plain value, not a table or list.
func plainList(list []any) bool {
	for _, item := range list {
		switch item.(type) {
		case map[string]any, []any:
			return false
		}
	}
	return true
}

// collectPlainList compares two lists of plain values as multisets of items.
// Equal items in a different order are one "reordered" change.
func collectPlainList(path string, old, next []any, out *[]SettingChange) {
	unmatched := map[string]int{} // items of next that old does not account for
	for _, item := range next {
		unmatched[leafText(item)]++
	}
	first := len(*out)
	for i, item := range old {
		text := leafText(item)
		if unmatched[text] > 0 {
			unmatched[text]--
			continue
		}
		*out = append(*out, SettingChange{
			Path: path + "[]", Before: text, Removed: true, Follows: []string{previousText(old, i)},
		})
	}
	for i, item := range next {
		text := leafText(item)
		if unmatched[text] > 0 {
			unmatched[text]--
			*out = append(*out, SettingChange{
				Path:    path + "[]",
				After:   text,
				Added:   true,
				Follows: []string{previousText(next, i)},
			})
		}
	}
	if len(*out) == first && !slices.EqualFunc(old, next, equalLeaf) {
		*out = append(*out, SettingChange{Path: path, Reordered: true})
	}
}

func previousText(list []any, i int) string {
	if i == 0 || i > len(list) {
		return ""
	}
	return leafText(list[i-1])
}

func equalLeaf(a, b any) bool { return leafText(a) == leafText(b) }

func joinKey(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// leafText is a leaf value as the text the owner reads: strings quoted, empty
// tables and lists as {} and [].
func leafText(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return strconv.Quote(v)
	case json.Number:
		return v.String()
	case map[string]any:
		return "{}"
	case []any:
		return "[]"
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
