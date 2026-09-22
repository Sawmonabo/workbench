package operation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

// SHA256Hex returns the lowercase hex SHA-256 of data, the digest form every
// Workbench record and plan input uses.
func SHA256Hex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// ValidDigest reports whether value is a digest in the [SHA256Hex] form.
func ValidDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && strings.ToLower(value) == value
}

// DecodeStrict decodes data as exactly one JSON value into target and rejects
// unknown fields. encoding/json keeps the last of repeated keys and matches
// struct fields case-insensitively, so keys that repeat within an object under
// that matching are rejected too: a duplicate could hide a contradictory value
// from one of the record's readers.
func DecodeStrict(data []byte, target any) error {
	if err := uniqueKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if !errors.Is(decoder.Decode(new(any)), io.EOF) {
		return errors.New("data follows the JSON value")
	}
	return nil
}

// uniqueKeys reads one JSON value and rejects case-insensitively repeated keys
// in any object inside it.
func uniqueKeys(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, _ := key.(string)
			if seen[strings.ToLower(name)] {
				return errors.New("repeated key " + name)
			}
			seen[strings.ToLower(name)] = true
		}
		if err := uniqueKeys(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}
