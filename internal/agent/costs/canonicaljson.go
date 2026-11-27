package costs

import (
	"bytes"
	"encoding/json"
	"slices"
)

// canonicalJSON re-encodes raw with sorted keys and exact numbers, so the same
// value gives the same text however Codex spaced or ordered it. The text is
// what encoding/json writes for raw decoded with UseNumber, and is part of
// every token_count row's key, so it must never change. A value made only of
// objects, arrays, numbers, literals and plain printable ASCII strings is
// written directly; anything else (escapes, HTML characters, other bytes,
// repeated keys) takes the decode-and-marshal path.
func canonicalJSON(raw json.RawMessage) string {
	if out, rest, ok := appendCanonical(nil, skipSpace(raw), 0); ok && len(skipSpace(rest)) == 0 {
		return string(out)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if decoder.Decode(&value) != nil {
		return string(raw)
	}
	out, err := json.Marshal(value)
	if err != nil {
		return string(raw)
	}
	return string(out)
}

// appendCanonical appends the canonical form of the value at the front of
// src, nested depth deep, to dst and returns what follows it; ok is false when
// the value is malformed or needs the decode-and-marshal path.
func appendCanonical(dst, src []byte, depth int) (out, rest []byte, ok bool) {
	if len(src) == 0 || depth > 32 {
		return dst, src, false
	}
	switch c := src[0]; {
	case c == '{':
		return appendCanonicalObject(dst, src[1:], depth+1)
	case c == '[':
		dst = append(dst, '[')
		src = skipSpace(src[1:])
		if len(src) > 0 && src[0] == ']' {
			return append(dst, ']'), src[1:], true
		}
		for {
			if dst, src, ok = appendCanonical(dst, src, depth+1); !ok {
				return dst, src, false
			}
			src = skipSpace(src)
			if len(src) == 0 {
				return dst, src, false
			}
			switch src[0] {
			case ',':
				dst, src = append(dst, ','), skipSpace(src[1:])
			case ']':
				return append(dst, ']'), src[1:], true
			default:
				return dst, src, false
			}
		}
	case c == '"':
		text, rest, ok := plainString(src)
		return append(append(append(dst, '"'), text...), '"'), rest, ok
	case c == '-' || (c >= '0' && c <= '9'):
		n := numberLength(src)
		return append(dst, src[:n]...), src[n:], n > 0
	}
	for _, literal := range []string{"true", "false", "null"} {
		if bytes.HasPrefix(src, []byte(literal)) {
			return append(dst, literal...), src[len(literal):], true
		}
	}
	return dst, src, false
}

// appendCanonicalObject writes the object whose members start at src with
// its keys in byte order, as encoding/json writes a map.
func appendCanonicalObject(dst, src []byte, depth int) (out, rest []byte, ok bool) {
	type member struct {
		key        []byte
		start, end int // the value's text in values
	}
	var buffer [16]member
	members := buffer[:0]
	var values []byte
	src = skipSpace(src)
	if len(src) > 0 && src[0] == '}' {
		return append(dst, '{', '}'), src[1:], true
	}
	for {
		m := member{start: len(values)}
		if m.key, src, ok = plainString(src); !ok {
			return dst, src, false
		}
		src = skipSpace(src)
		if len(src) == 0 || src[0] != ':' {
			return dst, src, false
		}
		if values, src, ok = appendCanonical(values, skipSpace(src[1:]), depth); !ok {
			return dst, src, false
		}
		m.end = len(values)
		members = append(members, m)
		src = skipSpace(src)
		if len(src) == 0 {
			return dst, src, false
		}
		if src[0] == '}' {
			break
		}
		if src[0] != ',' {
			return dst, src, false
		}
		src = skipSpace(src[1:])
	}
	slices.SortFunc(members, func(a, b member) int { return bytes.Compare(a.key, b.key) })
	dst = append(dst, '{')
	for i, m := range members {
		if i > 0 {
			if bytes.Equal(m.key, members[i-1].key) {
				return dst, src, false // a repeated key: the decoder keeps the last
			}
			dst = append(dst, ',')
		}
		dst = append(append(append(append(dst, '"'), m.key...), '"', ':'), values[m.start:m.end]...)
	}
	return append(dst, '}'), src[1:], true
}

// plainString splits a string of printable ASCII off the front of src, one
// encoding/json writes back unchanged: no escapes and none of <, >, &.
func plainString(src []byte) (text, rest []byte, ok bool) {
	if len(src) == 0 || src[0] != '"' {
		return nil, src, false
	}
	for i := 1; i < len(src); i++ {
		switch c := src[i]; {
		case c == '"':
			return src[1:i], src[i+1:], true
		case c < 0x20 || c > 0x7e || c == '\\' || c == '<' || c == '>' || c == '&':
			return nil, src, false
		}
	}
	return nil, src, false
}

// numberLength is the length of the JSON number at the front of src, 0 when
// there is none.
func numberLength(src []byte) int {
	i := 0
	digits := func() int {
		start := i
		for i < len(src) && src[i] >= '0' && src[i] <= '9' {
			i++
		}
		return i - start
	}
	if i < len(src) && src[i] == '-' {
		i++
	}
	switch n := digits(); {
	case n == 0, n > 1 && src[i-n] == '0':
		return 0
	}
	if i < len(src) && src[i] == '.' {
		i++
		if digits() == 0 {
			return 0
		}
	}
	if i < len(src) && (src[i] == 'e' || src[i] == 'E') {
		i++
		if i < len(src) && (src[i] == '+' || src[i] == '-') {
			i++
		}
		if digits() == 0 {
			return 0
		}
	}
	return i
}

func skipSpace(src []byte) []byte {
	for len(src) > 0 && (src[0] == ' ' || src[0] == '\t' || src[0] == '\n' || src[0] == '\r') {
		src = src[1:]
	}
	return src
}
