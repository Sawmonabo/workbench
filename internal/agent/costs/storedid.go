package costs

import (
	"encoding/hex"
	"errors"
	"iter"
	"math/bits"
	"strings"
)

// A response's ledger key (storedID) is stored as bytes that decode to exactly
// its text, by the first byte:
//
//   - idClaude: "req_011C" and 20 characters of 0-9A-Za-z, kept as one
//     base-62 number in 15 bytes, big-endian, digits in that order;
//   - idCodexResponse: "codex:resp_" and an even count, at least 2, of
//     lowercase hex digits, kept as the bytes they spell;
//   - idCodexCount: "codex:tc:" and the same, a token count's digest;
//   - idText: any other key (a copy key, any other shape), as its UTF-8 text.
//
// Every key has one form, so the bytes are unique when the keys are, and a
// range of keys that start alike (DropHeldCopies) is a range of idText bytes.
const (
	idText byte = iota
	idClaude
	idCodexResponse
	idCodexCount
)

const (
	claudePrefix        = "req_011C"
	claudeDigits        = 20
	claudeBytes         = 15
	codexResponsePrefix = "codex:resp_"
	codexCountPrefix    = "codex:tc:"
	base62              = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
)

// packID is the stored bytes of the ledger key id.
func packID(id string) []byte {
	if digits, ok := strings.CutPrefix(id, claudePrefix); ok && len(digits) == claudeDigits {
		if packed, ok := packBase62(digits); ok {
			return packed
		}
	}
	for _, shape := range []struct {
		prefix string
		tag    byte
	}{{codexResponsePrefix, idCodexResponse}, {codexCountPrefix, idCodexCount}} {
		if digits, ok := strings.CutPrefix(id, shape.prefix); ok && lowerHex(digits) {
			packed := make([]byte, 1+len(digits)/2)
			packed[0] = shape.tag
			_, _ = hex.Decode(packed[1:], []byte(digits))
			return packed
		}
	}
	return append([]byte{idText}, id...)
}

// packedIDs is the stored bytes of each of ids in hex, as a JSON array, for
// "unhex(value) FROM json_each(?)".
func packedIDs(ids iter.Seq[string]) string {
	var hexes []string
	for id := range ids {
		hexes = append(hexes, hex.EncodeToString(packID(id)))
	}
	return jsonText(hexes)
}

// lowerHex reports whether s is an even count, at least 2, of lowercase hex
// digits, which hex.EncodeToString gives back exactly.
func lowerHex(s string) bool {
	if len(s) < 2 || len(s)%2 != 0 {
		return false
	}
	for i := range len(s) {
		if c := s[i]; (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// packBase62 is idClaude and the 20 digits as one 120-bit number.
func packBase62(digits string) ([]byte, bool) {
	var hi, lo uint64
	for i := range len(digits) {
		d := strings.IndexByte(base62, digits[i])
		if d < 0 {
			return nil, false
		}
		carry, low := bits.Mul64(lo, 62)
		hi = hi*62 + carry
		var c uint64
		lo, c = bits.Add64(low, uint64(d), 0)
		hi += c
	}
	packed := make([]byte, 1+claudeBytes)
	packed[0] = idClaude
	for i := claudeBytes; i > 0; i-- {
		packed[i] = byte(lo)
		lo = lo>>8 | hi<<56
		hi >>= 8
	}
	return packed, true
}

// errPackedID is the error of stored bytes that are no ledger key's.
var errPackedID = errors.New("a stored response id the ledger cannot read")

// unpackID is the ledger key whose stored bytes are packed.
func unpackID(packed []byte) (string, error) {
	if len(packed) == 0 {
		return "", errPackedID
	}
	body := packed[1:]
	switch packed[0] {
	case idText:
		return string(body), nil
	case idClaude:
		if len(body) != claudeBytes {
			return "", errPackedID
		}
		var hi, lo uint64
		for _, b := range body {
			hi = hi<<8 | lo>>56
			lo = lo<<8 | uint64(b)
		}
		digits := make([]byte, claudeDigits)
		for i := claudeDigits - 1; i >= 0; i-- {
			var r uint64
			hi, r = hi/62, hi%62
			lo, r = bits.Div64(r, lo, 62)
			digits[i] = base62[r]
		}
		if hi != 0 || lo != 0 {
			return "", errPackedID // more than 20 base-62 digits
		}
		return claudePrefix + string(digits), nil
	case idCodexResponse, idCodexCount:
		if len(body) == 0 {
			return "", errPackedID
		}
		prefix := codexResponsePrefix
		if packed[0] == idCodexCount {
			prefix = codexCountPrefix
		}
		return prefix + hex.EncodeToString(body), nil
	}
	return "", errPackedID
}
