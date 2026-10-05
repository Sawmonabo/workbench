package machine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/Sawmonabo/workbench/internal/jsonc"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/tailscale/hujson"
)

// vscodeUnit is the indentation VS Code writes when it creates a settings file.
const vscodeUnit = "    "

var byteOrderMark = []byte{0xEF, 0xBB, 0xBF}

// MergeVSCodeSettings returns existing, the text of a user's VS Code
// settings.json, with the managed settings set in it, editing the way VS Code
// edits its own file: only the values that change and the members that are new
// are written, so comments, key order, indentation, a byte order mark and line
// endings survive. managedJSON is the JSON object Workbench manages.
//
// Managed values always win, including false, 0 and "". Objects merge
// recursively, so settings the owner keeps inside a managed object stay;
// scalars and arrays are replaced. An array whose managed entries are all
// objects with a string "name" is merged by name instead: the existing entries
// whose name no managed entry has stay, in their order, and the managed
// entries follow them.
//
// It returns existing itself when no value changes. A blank file counts as
// {}, as VS Code reads it. Text that is not a JSON object with comments, that
// repeats a key, or whose edit cannot be proven to give the old settings plus
// the managed ones is refused with a reason, and the caller leaves the file as
// it is.
func MergeVSCodeSettings(existing, managedJSON []byte) ([]byte, error) {
	var managed map[string]any
	if json.Unmarshal(managedJSON, &managed) != nil || len(managed) == 0 {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"vscode_settings",
			"the managed settings are not a JSON object",
		)
	}
	body := bytes.TrimPrefix(existing, byteOrderMark)
	prefix := existing[:len(existing)-len(body)]
	if !utf8.Valid(body) {
		return nil, refuseVSCode("it is not valid UTF-8")
	}
	if len(bytes.Trim(body, " \t\r\n")) == 0 {
		fresh, err := newVSCodeSettings(managed)
		if err != nil {
			return nil, err
		}
		if bytes.Contains(body, []byte("\r\n")) {
			fresh = bytes.ReplaceAll(fresh, []byte("\n"), []byte("\r\n"))
		}
		return slices.Concat(prefix, fresh), nil
	}
	doc, err := jsonc.Parse(body)
	if err != nil {
		return nil, refuseVSCode("it is not valid JSON or JSONC (" + oneLine(err.Error()) + ")")
	}
	root, isObject := doc.Value.(*hujson.Object)
	if !isObject {
		return nil, refuseVSCode("it is not a JSON object")
	}
	if err = jsonc.UniqueKeys(doc); err != nil {
		return nil, refuseVSCode(err.Error())
	}
	var have map[string]any
	if err = json.Unmarshal(jsonc.Plain(doc), &have); err != nil {
		return nil, refuseVSCode("it is not valid JSON or JSONC (" + oneLine(err.Error()) + ")")
	}
	want := mergeSettings(have, managed)
	if reflect.DeepEqual(want, have) {
		return existing, nil
	}
	if err = mergeObject(root, jsonc.RootLayout(doc, vscodeUnit), have, managed); err != nil {
		return nil, refuseVSCode("it could not be updated (" + oneLine(err.Error()) + ")")
	}
	merged := slices.Concat(prefix, doc.Pack())
	if !yieldsSettings(merged[len(prefix):], want) {
		return nil, refuseVSCode(
			"it could not be updated without changing anything else " +
				"(the edit did not give the old settings plus the managed ones)",
		)
	}
	return merged, nil
}

// refuseVSCode is a settings file Workbench leaves as it is, with the reason.
func refuseVSCode(reason string) error {
	return operation.Fail(operation.ExitBlocked, "vscode_settings", reason)
}

func oneLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// newVSCodeSettings is the settings file VS Code would start with, holding
// only the managed settings.
func newVSCodeSettings(managed map[string]any) ([]byte, error) {
	compact, err := json.Marshal(managed)
	if err != nil {
		return nil, err
	}
	return jsonc.Indent(compact, vscodeUnit)
}

// yieldsSettings reports that text is valid JSONC without repeated keys whose
// settings are want.
func yieldsSettings(text []byte, want map[string]any) bool {
	doc, err := hujson.Parse(text)
	if err != nil || jsonc.UniqueKeys(doc) != nil {
		return false
	}
	var got map[string]any
	return json.Unmarshal(jsonc.Plain(doc), &got) == nil && reflect.DeepEqual(got, want)
}

// mergeSettings is the settings that result from setting managed in have,
// which it does not change.
func mergeSettings(have, managed map[string]any) map[string]any {
	merged := maps.Clone(have)
	if merged == nil {
		merged = map[string]any{}
	}
	for key, want := range managed {
		switch want := want.(type) {
		case map[string]any:
			current, _ := have[key].(map[string]any)
			merged[key] = mergeSettings(current, want)
		case []any:
			current, _ := have[key].([]any)
			merged[key] = mergeList(current, want)
		default:
			merged[key] = want
		}
	}
	return merged
}

// entryName is the "name" of a list entry that is an object with a string one.
func entryName(entry any) (string, bool) {
	fields, isObject := entry.(map[string]any)
	if !isObject {
		return "", false
	}
	name, isString := fields["name"].(string)
	return name, isString
}

// managedNames is the set of names of a managed list that is merged by name,
// or nil for any other list.
func managedNames(want []any) map[string]bool {
	names := map[string]bool{}
	for _, entry := range want {
		name, ok := entryName(entry)
		if !ok {
			return nil
		}
		names[name] = true
	}
	if len(names) == 0 {
		return nil
	}
	return names
}

// mergeList is the list that results from setting want in have.
func mergeList(have, want []any) []any {
	names := managedNames(want)
	merged := make([]any, 0, len(have)+len(want))
	if names != nil {
		for _, entry := range have {
			if name, ok := entryName(entry); !ok || !names[name] {
				merged = append(merged, entry)
			}
		}
	}
	return append(merged, want...)
}

// mergeObject writes the managed members into obj, which lay describes and
// have holds the settings of. Members it leaves alone keep their bytes.
func mergeObject(obj *hujson.Object, lay jsonc.Layout, have, managed map[string]any) error {
	for _, key := range slices.Sorted(maps.Keys(managed)) {
		want := managed[key]
		index := jsonc.Index(obj, key)
		if index < 0 {
			if err := jsonc.AppendMember(obj, lay, key, want); err != nil {
				return err
			}
			continue
		}
		member := &obj.Members[index]
		if err := mergeMember(member, lay, have[key], want); err != nil {
			return err
		}
	}
	return nil
}

// mergeMember sets want, the managed value, in member, which holds current.
func mergeMember(member *hujson.ObjectMember, lay jsonc.Layout, current, want any) error {
	switch want := want.(type) {
	case map[string]any:
		object, isObject := member.Value.Value.(*hujson.Object)
		switch {
		case isObject && len(want) == 0:
			return nil
		case !isObject || jsonc.Empty(object):
			// Nothing of the owner's to keep inside, so it is written whole.
			return jsonc.SetValue(member, lay, want)
		}
		held, _ := current.(map[string]any)
		return mergeObject(object, jsonc.ObjectLayout(object, lay), held, want)
	case []any:
		array, isArray := member.Value.Value.(*hujson.Array)
		held, _ := current.([]any)
		if !isArray {
			return jsonc.SetValue(member, lay, mergeList(nil, want))
		}
		return mergeArray(array, jsonc.ArrayLayout(array, lay), held, want)
	}
	if reflect.DeepEqual(current, want) {
		return nil
	}
	return jsonc.SetValue(member, lay, want)
}

// mergeArray makes array, which holds have, the list mergeList gives,
// changing only the entries that differ.
func mergeArray(array *hujson.Array, lay jsonc.Layout, have, want []any) error {
	if reflect.DeepEqual(have, mergeList(have, want)) {
		return nil
	}
	return jsonc.RewriteArray(array, lay, arrayEntries(have, want))
}

// arrayEntries is the list mergeList gives, as the entries of have that stay
// and the managed ones that replace or follow them. A managed entry takes the
// place of an existing one of the same name, so the comments around that stay
// with it.
func arrayEntries(have, want []any) []jsonc.Entry {
	names := managedNames(want)
	var entries []jsonc.Entry
	if names == nil {
		for i, entry := range want {
			switch {
			case i < len(have) && reflect.DeepEqual(have[i], entry):
				entries = append(entries, jsonc.Entry{Keep: i})
			case i < len(have):
				entries = append(entries, jsonc.Entry{Keep: i, Value: entry, Set: true})
			default:
				entries = append(entries, jsonc.Entry{Keep: -1, Value: entry})
			}
		}
		return entries
	}
	taken := make([]bool, len(have))
	for i, entry := range have {
		if name, ok := entryName(entry); !ok || !names[name] {
			entries = append(entries, jsonc.Entry{Keep: i})
			taken[i] = true
		}
	}
	for _, entry := range want {
		name, _ := entryName(entry)
		index := -1
		for i, old := range have {
			if oldName, ok := entryName(old); ok && oldName == name && !taken[i] {
				index = i
				break
			}
		}
		switch {
		case index < 0:
			entries = append(entries, jsonc.Entry{Keep: -1, Value: entry})
		case reflect.DeepEqual(have[index], entry):
			entries = append(entries, jsonc.Entry{Keep: index})
			taken[index] = true
		default:
			entries = append(entries, jsonc.Entry{Keep: index, Value: entry, Set: true})
			taken[index] = true
		}
	}
	return entries
}

// EditVSCodeSettingsFile merges managedJSON into the settings file at path in
// place, for a settings file Workbench does not render as a target, such as the
// Windows user's. check only reports whether that would change the file. A
// file that is missing is created, unless check. The file must be an ordinary
// file with a single link; the new text is written to a file beside it, synced
// and renamed over it, with the file's mode. A file that cannot be read is
// refused like an invalid one, so check never reports it as a change.
func EditVSCodeSettingsFile(path string, managedJSON []byte, check bool) (changed bool, err error) {
	info, err := os.Lstat(path)
	if err != nil && !os.IsNotExist(err) {
		return false, refuseVSCode("it cannot be read (" + oneLine(err.Error()) + ")")
	}
	var existing []byte
	mode := os.FileMode(0o600)
	if err == nil {
		if linked, ok := info.Sys().(*syscall.Stat_t); !info.Mode().IsRegular() ||
			ok && linked.Nlink != 1 {
			return false, refuseVSCode("it must be an ordinary file with a single link")
		}
		mode = info.Mode().Perm()
		if existing, err = os.ReadFile(path); err != nil {
			return false, refuseVSCode("it cannot be read (" + oneLine(err.Error()) + ")")
		}
	}
	merged, err := MergeVSCodeSettings(existing, managedJSON)
	if err != nil {
		return false, err
	}
	changed = info == nil || !bytes.Equal(merged, existing)
	if check || !changed {
		return changed, nil
	}
	return true, replaceFile(path, merged, mode)
}

// replaceFile writes data to a file beside path, syncs it and renames it over
// path, so a failure leaves the old file whole.
func replaceFile(path string, data []byte, mode os.FileMode) error {
	temporary, err := os.CreateTemp(filepath.Dir(path), ".workbench-settings-")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	err = temporary.Chmod(mode)
	if err == nil {
		_, err = temporary.Write(data)
	}
	if err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return os.Rename(temporary.Name(), path)
}
