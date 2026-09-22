package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
)

// Prevent unauthorized executable extraction via path traversal. This is a
// catastrophic execution-boundary regression, not a matrix of ordinary
// archive/library behavior.
func TestRejectEscapingOrUnverifiedExecutable(t *testing.T) {
	var raw bytes.Buffer
	gz := gzip.NewWriter(&raw)
	archive := tar.NewWriter(gz)
	if err := archive.WriteHeader(
		&tar.Header{Name: "../workbench", Mode: 0o700, Size: 4},
	); err != nil {
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
	if err == nil || len(bundle.Files) != 0 {
		t.Fatal("unsafe executable payload passed verification")
	}
}
