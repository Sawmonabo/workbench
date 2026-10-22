package machine

import (
	"bytes"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/Sawmonabo/workbench/internal/jsonc"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/tailscale/hujson"
)

// terminalProfileGUID is the form Windows Terminal writes a profile's guid in.
var terminalProfileGUID = regexp.MustCompile(
	`^\{[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}\}$`,
)

// SetTerminalDefault returns existing, the text of a Windows Terminal
// settings.json, with its defaultProfile set to guid and nothing else changed:
// comments, key order, indentation, a byte order mark and line endings stay.
// A defaultProfile that already opens that profile, by its guid in any case
// or by the name of the profile in the file's own list that has it, counts as
// set, and existing itself is returned. Text that is blank, is not a JSON
// object with comments, repeats a key, or whose edit cannot be proven to
// change that one value is refused with a reason, and the caller leaves the
// file as it is.
func SetTerminalDefault(existing []byte, guid string) ([]byte, error) {
	if !terminalProfileGUID.MatchString(guid) {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"terminal_settings",
			"the profile is not a Windows Terminal guid such as {00000000-0000-0000-0000-000000000000}",
		)
	}
	doc, have, err := readTerminalSettings(existing)
	if err != nil {
		return nil, err
	}
	if opensProfile(have, guid) {
		return existing, nil
	}
	body := bytes.TrimPrefix(existing, byteOrderMark)
	prefix := existing[:len(existing)-len(body)]
	root, _ := doc.Value.(*hujson.Object)
	lay := jsonc.RootLayout(doc, vscodeUnit)
	if index := jsonc.Index(root, "defaultProfile"); index >= 0 {
		err = jsonc.SetValue(&root.Members[index], lay, guid)
	} else {
		err = jsonc.AppendMember(root, lay, "defaultProfile", guid)
	}
	if err != nil {
		return nil, refuseTerminal("it could not be updated (" + oneLine(err.Error()) + ")")
	}
	want := maps.Clone(have)
	want["defaultProfile"] = guid
	edited := slices.Concat(prefix, doc.Pack())
	if !yieldsSettings(edited[len(prefix):], want) {
		return nil, refuseTerminal(
			"it could not be updated without changing anything else " +
				"(the edit did not give the old settings with only defaultProfile changed)",
		)
	}
	return edited, nil
}

// TerminalDefault is the defaultProfile of existing, a Windows Terminal
// settings.json, as JSON text, or "" when it has none or one that is not a
// string. It refuses what [SetTerminalDefault] refuses.
func TerminalDefault(existing []byte) (string, error) {
	_, have, err := readTerminalSettings(existing)
	if err != nil {
		return "", err
	}
	current, isString := have["defaultProfile"].(string)
	if !isString {
		return "", nil
	}
	// Written as Terminal writes it, without escaping <, > and &, so a
	// script that puts it back gives the same bytes.
	var text bytes.Buffer
	encoder := json.NewEncoder(&text)
	encoder.SetEscapeHTML(false)
	err = encoder.Encode(current)
	return strings.TrimSpace(text.String()), err
}

// readTerminalSettings parses a Windows Terminal settings.json, which is
// JSON with comments, into its document and its plain settings.
func readTerminalSettings(existing []byte) (hujson.Value, map[string]any, error) {
	body := bytes.TrimPrefix(existing, byteOrderMark)
	switch {
	case !utf8.Valid(body):
		return hujson.Value{}, nil, refuseTerminal("it is not valid UTF-8")
	case len(bytes.Trim(body, " \t\r\n")) == 0:
		// Terminal writes a fresh file in place of a blank one; this edits
		// only a file Terminal has written.
		return hujson.Value{}, nil, refuseTerminal("it is empty")
	}
	doc, err := jsonc.Parse(body)
	if err != nil {
		return hujson.Value{}, nil, refuseTerminal(
			"it is not valid JSON or JSONC (" + oneLine(err.Error()) + ")",
		)
	}
	if _, isObject := doc.Value.(*hujson.Object); !isObject {
		return hujson.Value{}, nil, refuseTerminal("it is not a JSON object")
	}
	if err = jsonc.UniqueKeys(doc); err != nil {
		return hujson.Value{}, nil, refuseTerminal(err.Error())
	}
	var have map[string]any
	if err = json.Unmarshal(jsonc.Plain(doc), &have); err != nil {
		return hujson.Value{}, nil, refuseTerminal(
			"it is not valid JSON or JSONC (" + oneLine(err.Error()) + ")",
		)
	}
	return doc, have, nil
}

// opensProfile reports that the defaultProfile in settings opens the profile
// guid: it is that guid, in any case, or the name of a profile in the
// settings' own list with that guid. Terminal accepts either form.
func opensProfile(settings map[string]any, guid string) bool {
	current, isString := settings["defaultProfile"].(string)
	if !isString {
		return false
	}
	if strings.EqualFold(current, guid) {
		return true
	}
	// "profiles" is an object with a "list", or the list itself.
	profiles := settings["profiles"]
	if object, isObject := profiles.(map[string]any); isObject {
		profiles = object["list"]
	}
	list, _ := profiles.([]any)
	return slices.ContainsFunc(list, func(entry any) bool {
		profile, _ := entry.(map[string]any)
		name, _ := profile["name"].(string)
		id, _ := profile["guid"].(string)
		return name == current && strings.EqualFold(id, guid)
	})
}

// refuseTerminal is a Terminal settings file Workbench leaves as it is, with
// the reason.
func refuseTerminal(reason string) error {
	return operation.Fail(operation.ExitBlocked, "terminal_settings", reason)
}
