package jsonc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/tailscale/hujson"
)

// render is value as JSON text for a member or entry written in an object or
// array laid out as lay. A composite is laid out at the indentation of the
// line it starts on in a multi-line object or array, and kept compact in a
// one-line one.
func (lay Layout) render(value any) ([]byte, error) {
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	text := bytes.TrimSpace(encoded.Bytes())
	if !lay.Multiline || text[0] != '{' && text[0] != '[' {
		return text, nil
	}
	var laid bytes.Buffer
	if err := json.Indent(&laid, text, lay.Indent, lay.Unit); err != nil {
		return nil, err
	}
	return bytes.ReplaceAll(laid.Bytes(), []byte("\n"), []byte(lay.Newline)), nil
}

// spaceEntries separates the entries of a compact array with ", ", which is
// how a one-line object or array holds them.
func spaceEntries(value hujson.ValueTrimmed) {
	if array, ok := value.(*hujson.Array); ok {
		for i := 1; i < len(array.Elements); i++ {
			array.Elements[i].BeforeExtra = hujson.Extra(" ")
		}
	}
}

// patch applies RFC 6902 operations to target alone, so a pointer is one name
// or index. hujson moves the comments around a changed member or entry with it.
func patch(target hujson.ValueTrimmed, operations ...string) error {
	root := hujson.Value{Value: target}
	return root.Patch([]byte("[" + strings.Join(operations, ",") + "]"))
}

// pointer is the RFC 6901 form of a member name or an array index.
func pointer(name string) string {
	return "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(name)
}

// keepLineEnding puts back the carriage return hujson drops from the text that
// ends a composite when it moves a trailing comment out of it, so a file that
// ends its lines with CRLF is not left with a bare LF before its closing
// brace.
func keepLineEnding(lay Layout, extra *hujson.Extra) {
	if lay.Newline == "\r\n" {
		text := strings.ReplaceAll(string(*extra), "\r\n", "\n")
		*extra = hujson.Extra(strings.ReplaceAll(text, "\n", "\r\n"))
	}
}

// inline separates a value from the text before it with one space on a line it
// shares. A comment hujson moved there stays; one that ends in a line comment
// puts the value on the next line, at lay's indentation plus one level.
func inline(extra hujson.Extra, lay Layout) hujson.Extra {
	if extra.IsStandard() {
		return hujson.Extra(" ")
	}
	text := strings.TrimRight(string(extra), " \t")
	last := text[strings.LastIndex(text, "\n")+1:]
	switch {
	case strings.HasSuffix(text, "\n"):
		return hujson.Extra(text + lay.Outer + lay.Unit)
	case strings.Contains(last, "//"):
		return hujson.Extra(text + lay.Newline + lay.Outer + lay.Unit)
	}
	return hujson.Extra(text + " ")
}

// onOwnLines starts the value after extra on a line of its own at lay's
// indentation, and each comment line in extra there too: hujson carries a
// moved value's comments along, but not the indentation they were written at.
func onOwnLines(extra hujson.Extra, lay Layout) hujson.Extra {
	lines := strings.Split(strings.TrimRight(string(extra), " \t"), "\n")
	for i := 1; i < len(lines); i++ {
		if text := strings.TrimLeft(lines[i], " \t"); text != "" {
			lines[i] = lay.Indent + text
		}
	}
	return breakLine(hujson.Extra(strings.Join(lines, "\n")), lay.Indent, lay.Newline)
}

// pointerIndex is the pointer that adds an entry at index to an array of count
// entries; hujson spells the end "-".
func pointerIndex(index, count int) string {
	if index >= count {
		return "/-"
	}
	return pointer(strconv.Itoa(index))
}

func quote(text string) string {
	quoted, _ := json.Marshal(text)
	return string(quoted)
}

func addOperation(path string, value []byte) string {
	return `{"op":"add","path":` + quote(path) + `,"value":` + string(value) + `}`
}

// AppendMember adds "name": value after the last member of obj, which lay
// describes as it was written before. In a multi-line object the member starts
// its own line at the indentation of the others; in a one-line object it
// follows ", ". A comment in an otherwise empty object stays above the member.
func AppendMember(obj *hujson.Object, lay Layout, name string, value any) error {
	text, err := lay.render(value)
	if err != nil {
		return err
	}
	// The body of an empty object, which hujson may move into the new member.
	comment := strings.TrimRight(string(obj.AfterExtra), " \t\r\n")
	wasEmpty := len(obj.Members) == 0
	if err = patch(obj, addOperation(pointer(name), text)); err != nil {
		return err
	}
	keepLineEnding(lay, &obj.AfterExtra)
	member := &obj.Members[len(obj.Members)-1]
	member.Value.BeforeExtra = hujson.Extra(" ")
	if lay.Multiline {
		member.Name.BeforeExtra = breakLine(member.Name.BeforeExtra, lay.Indent, lay.Newline)
	} else {
		member.Name.BeforeExtra = inline(member.Name.BeforeExtra, lay)
		spaceEntries(member.Value.Value)
	}
	if wasEmpty {
		openFirst(lay, comment, &member.Name.BeforeExtra, &obj.AfterExtra)
	}
	return nil
}

// openFirst lays out the first member or entry of a composite that was empty.
// Its body held only comment, which stays above the new value: on its own line
// in a multi-line composite, before the value in a one-line one. before is the
// text before the new value, and closing the text before the closing bracket.
func openFirst(lay Layout, comment string, before, closing *hujson.Extra) {
	switch {
	case lay.Multiline:
		*before = hujson.Extra(comment + lay.Newline + lay.Indent)
		*closing = hujson.Extra(lay.Newline + lay.Outer)
	case comment != "":
		*before = inline(hujson.Extra(comment), lay)
		*closing = hujson.Extra(" ")
	}
}

// AppendElement adds value after the last entry of arr, which lay describes as
// it was written before: on a line of its own at the indentation of the others
// in a multi-line array, after ", " in a one-line one.
func AppendElement(arr *hujson.Array, lay Layout, value any) error {
	return InsertElement(arr, lay, len(arr.Elements), value)
}

// InsertElement adds value to arr as its entry number index, laid out like
// AppendElement.
func InsertElement(arr *hujson.Array, lay Layout, index int, value any) error {
	text, err := lay.render(value)
	if err != nil {
		return err
	}
	// The body of an empty array, which hujson leaves after the new entry.
	comment := strings.TrimRight(string(arr.AfterExtra), " \t\r\n")
	wasEmpty := len(arr.Elements) == 0
	if err = patch(arr, addOperation(pointerIndex(index, len(arr.Elements)), text)); err != nil {
		return err
	}
	keepLineEnding(lay, &arr.AfterExtra)
	element := &arr.Elements[index]
	switch {
	case wasEmpty:
		openFirst(lay, comment, &element.BeforeExtra, &arr.AfterExtra)
	case lay.Multiline:
		element.BeforeExtra = breakLine(element.BeforeExtra, lay.Indent, lay.Newline)
	case index > 0:
		element.BeforeExtra = inline(element.BeforeExtra, lay)
	}
	if next := index + 1; next < len(arr.Elements) {
		keepLineEnding(lay, &arr.Elements[next].BeforeExtra)
		if !lay.Multiline && index == 0 {
			arr.Elements[next].BeforeExtra = inline(arr.Elements[next].BeforeExtra, lay)
		}
	}
	return nil
}

// SetValue replaces the value of member, which lay describes as it was
// written, and keeps the comments around it.
func SetValue(member *hujson.ObjectMember, lay Layout, value any) error {
	return setValue(&member.Value, lay, value)
}

func setValue(slot *hujson.Value, lay Layout, value any) error {
	text, err := lay.render(value)
	if err != nil {
		return err
	}
	parsed, err := hujson.Parse(text)
	if err != nil {
		return err
	}
	if !lay.Multiline {
		spaceEntries(parsed.Value)
	}
	slot.Value = parsed.Value
	return nil
}

// Entry is one entry of the array RewriteArray leaves behind.
type Entry struct {
	// Keep is the index of the existing entry that stays, comments and all;
	// negative for a new entry.
	Keep int
	// Value is the new entry, or what replaces the kept one when Set.
	Value any
	Set   bool
}

// RewriteArray makes arr hold entries in order. A kept entry keeps its bytes
// and the comments around it, wherever it ends up; an existing entry no
// entry keeps is removed; the new entries go in between.
func RewriteArray(arr *hujson.Array, lay Layout, entries []Entry) error {
	count := len(arr.Elements)
	kept := make([]bool, count)
	var order, want []int
	for _, entry := range entries {
		if entry.Keep < 0 {
			continue
		}
		if entry.Keep >= count || kept[entry.Keep] {
			return fmt.Errorf("jsonc: array entry %d kept twice or out of range", entry.Keep)
		}
		kept[entry.Keep] = true
		want = append(want, entry.Keep)
		if entry.Set {
			if err := setValue(&arr.Elements[entry.Keep], lay, entry.Value); err != nil {
				return err
			}
		}
	}
	for i := count - 1; i >= 0; i-- {
		if !kept[i] {
			remove := `{"op":"remove","path":` + quote(pointer(strconv.Itoa(i))) + `}`
			if err := patch(arr, remove); err != nil {
				return err
			}
			keepLineEnding(lay, &arr.AfterExtra)
		}
	}
	for i := range count {
		if kept[i] {
			order = append(order, i)
		}
	}
	for to, index := range want {
		from := slices.Index(order, index)
		if from == to {
			continue
		}
		move := `{"op":"move","from":` + quote(pointer(strconv.Itoa(from))) +
			`,"path":` + quote(pointer(strconv.Itoa(to))) + `}`
		if err := patch(arr, move); err != nil {
			return err
		}
		keepLineEnding(lay, &arr.AfterExtra)
		order = slices.Insert(slices.Delete(order, from, from+1), to, index)
		if lay.Multiline {
			element := &arr.Elements[to]
			element.BeforeExtra = onOwnLines(element.BeforeExtra, lay)
		}
	}
	for position, entry := range entries {
		if entry.Keep < 0 {
			if err := InsertElement(arr, lay, position, entry.Value); err != nil {
				return err
			}
		}
	}
	return nil
}
