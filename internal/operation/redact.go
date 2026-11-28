package operation

import (
	"bytes"
	"io"
	"sort"
	"strings"
)

// redactingWriter forwards whole lines after replacing known secret values;
// no secret spans a line. Only a stream that ended normally is flushed.
type redactingWriter struct {
	out     io.Writer
	secrets []string
	pending []byte
}

// Write never fails the child: progress is diagnostic, not part of the result.
func (w *redactingWriter) Write(data []byte) (int, error) {
	w.pending = append(w.pending, data...)
	if end := bytes.LastIndexAny(w.pending, "\r\n"); end >= 0 {
		_, _ = io.WriteString(w.out, redact(string(w.pending[:end+1]), w.secrets))
		w.pending = append(w.pending[:0], w.pending[end+1:]...)
	}
	return len(data), nil
}

func (w *redactingWriter) flush() {
	if len(w.pending) > 0 {
		_, _ = io.WriteString(w.out, redact(string(w.pending), w.secrets)+"\n")
		w.pending = nil
	}
}

// Redacted is the placeholder that stands in for a secret Workbench masks:
// a known secret value here, and a value shaped or named like a credential
// in a plan's diff.
const Redacted = "[REDACTED]"

// Redact replaces each known secret value in value with [Redacted].
func Redact(value string, secrets []string) string { return redact(value, secrets) }

func redact(value string, secrets []string) string {
	ordered := append([]string{}, secrets...)
	sort.Slice(ordered, func(i, j int) bool { return len(ordered[i]) > len(ordered[j]) })
	for _, secret := range ordered {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, Redacted)
		}
	}
	return value
}
