// Package jsonc edits JSON with comments in place, the way an editor does: a
// changed value or a new member is written into the text its owner wrote, and
// everything else keeps its bytes, comments, key order, indentation and line
// endings included. It owns the layout rules both the project extension
// recommendations and the VS Code settings merge follow, so a new member is
// laid out like its siblings at any depth and never packed onto one line of a
// multi-line object.
//
// It sits on the operation package only for the error type its refusals share
// with the rest of Workbench.
package jsonc

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/tailscale/hujson"
)

// Layout is how one object or array is written: where its members or entries
// start, which line ending it uses and whether they sit on lines of their own.
type Layout struct {
	// Indent starts each member or entry that has a line to itself, and Outer
	// the line the object or array itself opens and closes on.
	Indent, Outer string
	// Newline is the line ending the text uses.
	Newline string
	// Unit is one level of indentation, for values written whole.
	Unit string
	// Multiline reports members or entries on lines of their own.
	Multiline bool
}

// RootLayout reads how the top-level object of doc is written. unit is one
// level of indentation to use when the text shows none.
func RootLayout(doc hujson.Value, unit string) Layout {
	object, _ := doc.Value.(*hujson.Object)
	if object == nil {
		return Layout{Newline: "\n", Unit: unit}
	}
	lay := ObjectLayout(object, Layout{Newline: "\n", Unit: unit})
	if lay.Multiline && lay.Indent != "" {
		lay.Unit = lay.Indent
	}
	return lay
}

// ObjectLayout reads how obj, the value of a member written in the object or
// array laid out as parent, is written. An object with no members is
// multi-line when its body, a comment, spans lines or when the lines around it
// are multi-line.
func ObjectLayout(obj *hujson.Object, parent Layout) Layout {
	lay := Layout{Outer: parent.Indent, Newline: parent.Newline, Unit: parent.Unit}
	if len(obj.Members) > 0 {
		if indent, newline, ok := lineLayout(obj.Members[0].Name.BeforeExtra); ok {
			lay.Indent, lay.Newline, lay.Multiline = indent, newline, true
		}
		return lay
	}
	body := strings.TrimRight(string(obj.AfterExtra), " \t")
	if !strings.Contains(body, "\n") && !parent.Multiline {
		return lay
	}
	// A comment on one line of an empty object still leaves the object to the
	// layout of the lines around it.
	lay.Multiline = true
	if strings.Contains(body, "\n") {
		lay.Newline = "\n"
		if strings.Contains(body, "\r\n") {
			lay.Newline = "\r\n"
		}
	}
	lay.Indent = parent.Indent + parent.Unit
	// The first line shares the opening brace, so it says nothing about indentation.
	_, rest, _ := strings.Cut(body, "\n")
	for line := range strings.SplitSeq(rest, "\n") {
		trimmed := strings.TrimLeft(line, " \t")
		if trimmed != "" && trimmed != "\r" && line != trimmed {
			lay.Indent = line[:len(line)-len(trimmed)]
			break
		}
	}
	return lay
}

// ArrayLayout reads how arr, the value of a member written in the object laid
// out as parent, is written. An array with no entries takes the layout of its
// parent.
func ArrayLayout(arr *hujson.Array, parent Layout) Layout {
	lay := Layout{
		Outer:     parent.Indent,
		Newline:   parent.Newline,
		Unit:      parent.Unit,
		Indent:    parent.Indent + parent.Unit,
		Multiline: parent.Multiline,
	}
	if len(arr.Elements) > 0 {
		lay.Indent, lay.Newline, lay.Multiline = lineLayout(arr.Elements[0].BeforeExtra)
		if !lay.Multiline {
			lay.Newline = parent.Newline
		}
		return lay
	}
	// A comment that spans lines in an empty array puts its entries on lines.
	if strings.Contains(string(arr.AfterExtra), "\n") {
		lay.Multiline = true
		if strings.Contains(string(arr.AfterExtra), "\r\n") {
			lay.Newline = "\r\n"
		}
	}
	return lay
}

// lineLayout returns the indentation and line ending that extra, the text
// before a value, uses to start that value on its own line. multiline is false
// when the value shares its line with the text before it.
func lineLayout(extra hujson.Extra) (indent, newline string, multiline bool) {
	text := string(extra)
	last := strings.LastIndex(text, "\n")
	if last < 0 {
		return "", "", false
	}
	newline = "\n"
	if last > 0 && text[last-1] == '\r' {
		newline = "\r\n"
	}
	tail := text[last+1:]
	return tail[:len(tail)-len(strings.TrimLeft(tail, " \t"))], newline, true
}

// breakLine starts a value on its own line at indent. Any comment already in
// extra stays: hujson moves a comment written after the previous comma into
// the leading text of the first value inserted after it.
func breakLine(extra hujson.Extra, indent, newline string) hujson.Extra {
	text := strings.TrimRight(string(extra), " \t")
	if !strings.HasSuffix(text, "\n") {
		text += newline
	}
	return hujson.Extra(text + indent)
}

// OwnsLayout reports a document that is an empty object without comments:
// there is no user layout to preserve.
func OwnsLayout(doc hujson.Value) bool {
	object, ok := doc.Value.(*hujson.Object)
	return ok && len(object.Members) == 0 &&
		len(bytes.TrimSpace(slices.Concat(doc.BeforeExtra, object.AfterExtra, doc.AfterExtra))) == 0
}

// Empty reports an object with no members and nothing in it but blanks.
func Empty(obj *hujson.Object) bool {
	return len(obj.Members) == 0 && len(bytes.TrimSpace(obj.AfterExtra)) == 0
}

// Index is where the member called name is in obj, or -1.
func Index(obj *hujson.Object, name string) int {
	return slices.IndexFunc(obj.Members, func(member hujson.ObjectMember) bool {
		literal, _ := member.Name.Value.(hujson.Literal)
		return literal.String() == name
	})
}

// Entries is the array that is the value of the member called name in obj, or
// nil when there is no such member or it holds something else.
func Entries(obj *hujson.Object, name string) *hujson.Array {
	if index := Index(obj, name); index >= 0 {
		array, _ := obj.Members[index].Value.Value.(*hujson.Array)
		return array
	}
	return nil
}

// Indent lays out standard JSON as a new file: one level of unit per depth,
// ending in a newline.
func Indent(compact []byte, unit string) ([]byte, error) {
	var indented bytes.Buffer
	if err := json.Indent(&indented, bytes.TrimSpace(compact), "", unit); err != nil {
		return nil, err
	}
	indented.WriteByte('\n')
	return indented.Bytes(), nil
}

// Parse reads data as JSON with comments. hujson keeps the comments and
// whitespace it parses in the buffer it is given and edits them there, so the
// text is parsed from a copy and data stays as it was read.
func Parse(data []byte) (hujson.Value, error) {
	return hujson.Parse(bytes.Clone(data))
}

// Plain is doc as standard JSON, without comments or trailing commas.
func Plain(doc hujson.Value) []byte {
	plain := doc.Clone()
	plain.Standardize()
	return plain.Pack()
}

// UniqueKeys refuses an object, at any depth, with a key that is not a quoted
// string or that repeats: which value wins would be a guess.
func UniqueKeys(value hujson.Value) error {
	switch node := value.Value.(type) {
	case *hujson.Object:
		seen := map[string]bool{}
		for _, member := range node.Members {
			name, ok := member.Name.Value.(hujson.Literal)
			if !ok || name.Kind() != '"' {
				return operation.Fail(
					operation.ExitInvalid,
					"jsonc",
					"JSONC object keys must be quoted strings",
				)
			}
			key := name.String()
			if seen[key] {
				return operation.Fail(
					operation.ExitInvalid,
					"jsonc",
					"Duplicate JSONC keys require manual repair",
				)
			}
			seen[key] = true
			if err := UniqueKeys(member.Value); err != nil {
				return err
			}
		}
	case *hujson.Array:
		for _, element := range node.Elements {
			if err := UniqueKeys(element); err != nil {
				return err
			}
		}
	}
	return nil
}
