// Package release verifies immutable bundles before staging or executing them.
package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Conservative engineering ceilings, not production capacity qualification.
const MaxDownload int64 = 128 << 20
const maxExpanded int64 = 256 << 20
const maxFiles = 4096

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type File struct {
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable"`
}
type Metadata struct {
	SchemaVersion int             `json:"schema_version"`
	StateVersion  int             `json:"state_version"`
	Release       string          `json:"release"`
	Target        string          `json:"target"`
	SourceDigest  string          `json:"source_digest"`
	Files         map[string]File `json:"files"`
	Requirements  json.RawMessage `json:"requirements"`
	Publication   string          `json:"publication"`
}
type Bundle struct {
	Metadata      Metadata
	Files         map[string][]byte
	ArchiveDigest string
}

func Target() string         { return runtime.GOOS + "-" + runtime.GOARCH }
func sum(data []byte) string { digest := sha256.Sum256(data); return hex.EncodeToString(digest[:]) }
func validDigest(value string) bool {
	data, err := hex.DecodeString(value)
	return err == nil && len(data) == sha256.Size && strings.ToLower(value) == value
}
func allowed(name string) bool {
	return name == "bin/workbench" || name == ".chezmoiroot" || strings.HasPrefix(name, "home/") || strings.HasPrefix(name, "project/") || strings.HasPrefix(name, "licenses/")
}
func member(name string) bool {
	return name != "" && name != "." && !strings.ContainsAny(name, "\\\x00\r\n") && !path.IsAbs(name) && path.Clean(name) == name && !strings.HasPrefix(name, "../")
}

// Verify reads the complete bounded archive before exposing any executable or
// extracting any member. Links, devices, duplicate entries and extra files fail.
func Verify(reader io.Reader, expected, version, target string) (Bundle, error) {
	bundle := Bundle{Files: map[string][]byte{}}
	fail := func(message string) (Bundle, error) { return Bundle{}, operation.Fail(2, "release_integrity", message) }
	if !validDigest(expected) {
		return fail("Supply an explicitly trusted lowercase SHA-256 before reading a release")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxDownload+1))
	if err != nil || int64(len(raw)) > MaxDownload {
		return fail("Release download exceeds its bound or cannot be read")
	}
	if sum(raw) != expected {
		return fail("Release archive SHA-256 mismatch; no executable was used")
	}
	bundle.ArchiveDigest = expected
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return fail("Release is not a gzip archive")
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	seen := map[string]bool{}
	var total int64
	for {
		header, readErr := tr.Next()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fail("Malformed release archive")
		}
		name := header.Name
		if header.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if !member(name) || seen[strings.ToLower(name)] || len(seen) >= maxFiles || header.Mode&07000 != 0 {
			return fail("Unsafe or duplicate archive member")
		}
		seen[strings.ToLower(name)] = true
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return fail("Archive directories cannot carry payload bytes")
			}
			if name != "bin" && name != "home" && name != "project" && name != "licenses" && !allowed(name+"/") {
				return fail("Unexpected release directory")
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > MaxDownload || (!allowed(name) && name != "release.json") {
			return fail("Release contains an unsupported file or layout")
		}
		total += header.Size
		if total > maxExpanded {
			return fail("Expanded release exceeds its size bound")
		}
		data, readErr := io.ReadAll(io.LimitReader(tr, header.Size+1))
		if readErr != nil || int64(len(data)) != header.Size {
			return fail("Truncated release member")
		}
		bundle.Files[name] = data
	}
	// Drain through the gzip checksum, retaining the same expanded bound.
	if n, drainErr := io.Copy(io.Discard, io.LimitReader(gz, maxExpanded-total+1)); drainErr != nil || n > maxExpanded-total {
		return fail("Invalid gzip trailer or oversized archive padding")
	}
	metadata := bundle.Files["release.json"]
	if len(metadata) == 0 || len(metadata) > 1<<20 {
		return fail("Missing or oversized release metadata")
	}
	if err = decodeMetadata(metadata, &bundle.Metadata); err != nil {
		return fail("Malformed release metadata")
	}
	m := bundle.Metadata
	if m.SchemaVersion != 1 || m.StateVersion != 1 || !identifier.MatchString(m.Release) || (version != "" && m.Release != version) || m.Target != target || !validDigest(m.SourceDigest) || m.Publication != "operator-trusted-unpublished" {
		return fail("Release version, target, state format or publication contract is unsupported")
	}
	if len(m.Files)+1 != len(bundle.Files) {
		return fail("Release manifest does not match the complete payload")
	}
	for name, entry := range m.Files {
		data, ok := bundle.Files[name]
		if !ok || !allowed(name) || !validDigest(entry.SHA256) || sum(data) != entry.SHA256 || int64(len(data)) != entry.Size || entry.Executable != (name == "bin/workbench") {
			return fail("Release payload integrity mismatch")
		}
	}
	for _, required := range []string{"bin/workbench", ".chezmoiroot", "home/.chezmoi.toml.tmpl", "licenses/NOTICE"} {
		if len(bundle.Files[required]) == 0 {
			return fail("Required release payload is missing")
		}
	}
	return bundle, nil
}

func (b Bundle) Identity() operation.SourceIdentity {
	return operation.SourceIdentity{Release: b.Metadata.Release, ContentDigest: b.Metadata.SourceDigest}
}
func (b Bundle) Directory(c operation.Context) string {
	return filepath.Join(c.Paths.Data, "releases", b.Metadata.Release+"-"+b.ArchiveDigest[:16])
}

func (b Bundle) Extract(directory string) error {
	if err := os.Mkdir(directory, 0700); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for name, data := range b.Files {
		if err = root.MkdirAll(filepath.Dir(name), 0700); err != nil {
			return err
		}
		mode := os.FileMode(0600)
		if name == "bin/workbench" {
			mode = 0700
		}
		file, openErr := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if openErr != nil {
			return openErr
		}
		_, writeErr := file.Write(data)
		if writeErr == nil {
			writeErr = file.Sync()
		}
		closeErr := file.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}

// CheckDirectory rechecks every immutable file immediately before handoff.
func (b Bundle) CheckDirectory(directory string) error {
	for name, data := range b.Files {
		full := filepath.Join(directory, name)
		if err := PrivateDirectory(directory, filepath.Dir(full), false); err != nil {
			return err
		}
		info, err := os.Lstat(full)
		if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(data)) {
			return operation.Fail(4, "release_conflict", "Staged release was modified")
		}
		actual, err := os.ReadFile(full)
		if err != nil || sum(actual) != sum(data) {
			return operation.Fail(4, "release_conflict", "Staged release content changed")
		}
	}
	return nil
}

func decodeMetadata(data []byte, target *Metadata) error {
	if err := uniqueJSON(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return operation.Fail(2, "release_metadata", "Trailing release metadata")
	}
	return nil
}

func uniqueJSON(decoder *json.Decoder) error {
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
			key, readErr := decoder.Token()
			if readErr != nil {
				return readErr
			}
			name, valid := key.(string)
			if !valid || seen[strings.ToLower(name)] {
				return operation.Fail(2, "release_metadata", "Duplicate release metadata keys")
			}
			seen[strings.ToLower(name)] = true
		}
		if err = uniqueJSON(decoder); err != nil {
			return err
		}
	}
	_, err = decoder.Token()
	return err
}

func (b Bundle) String() string { return fmt.Sprintf("%s (%s)", b.Metadata.Release, b.Metadata.Target) }
