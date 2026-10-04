package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

// Prevent unauthorized executable extraction via path traversal. This is a
// catastrophic execution-boundary regression, not a matrix of ordinary
// archive/library behavior. Each name passes one guard and is stopped only by
// the other, and the exact message pins which guard fired: member() rejects
// the dot-dot name that has an allowed prefix, allowed() rejects the clean name
// outside the release layout. Disabling either guard changes the error.
func TestRejectEscapingExecutable(t *testing.T) {
	for name, want := range map[string]string{
		"home/../../workbench": "Unsafe or duplicate archive member",
		"evil/workbench":       "unsupported file or layout",
	} {
		var raw bytes.Buffer
		gz := gzip.NewWriter(&raw)
		archive := tar.NewWriter(gz)
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o700, Size: 4}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte("evil")); err != nil {
			t.Fatal(err)
		}
		if err := archive.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		bundle, err := Verify(bytes.NewReader(raw.Bytes()), "", Target())
		if err == nil || len(bundle.Files) != 0 || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: want rejection containing %q, got %v", name, want, err)
		}
	}
}
