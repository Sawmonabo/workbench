// Package release verifies immutable bundles before staging or executing them.
package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Conservative engineering ceilings, not production capacity qualification.
const (
	MaxDownload int64 = 128 << 20
	maxExpanded int64 = 256 << 20
	maxFiles          = 4096
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// ValidTag reports whether tag is acceptable as a release identifier.
func ValidTag(tag string) bool { return identifier.MatchString(tag) }

// File is one bundle member's manifest entry.
type File struct {
	SHA256     string `json:"sha256"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable"`
}

// Metadata is a bundle's release.json: its release, target, source digest,
// file manifest and the versions.toml built into its executable.
type Metadata struct {
	SchemaVersion int             `json:"schema_version"`
	StateVersion  int             `json:"state_version"`
	Release       string          `json:"release"`
	Target        string          `json:"target"`
	SourceDigest  string          `json:"source_digest"`
	Files         map[string]File `json:"files"`
	Versions      string          `json:"versions"`
}

// Bundle is a verified release archive held in memory.
type Bundle struct {
	Metadata      Metadata
	Files         map[string][]byte
	ArchiveDigest string
}

// Target returns this binary's bundle target, such as darwin-arm64.
func Target() string { return runtime.GOOS + "-" + runtime.GOARCH }

func allowed(name string) bool {
	return name == "bin/workbench" || name == ".chezmoiroot" || strings.HasPrefix(name, "home/") ||
		strings.HasPrefix(name, "project/") ||
		strings.HasPrefix(name, "licenses/")
}

func member(name string) bool {
	return name != "" && name != "." && !strings.ContainsAny(name, "\\\x00\r\n") &&
		!path.IsAbs(name) &&
		path.Clean(name) == name &&
		!strings.HasPrefix(name, "../")
}

// Verify reads the complete bounded archive before exposing any executable or
// extracting any member. Links, devices, duplicate entries and extra files fail.
// Every member must match its SHA-256 in release.json, which catches a
// truncated or corrupted download; it is not an authenticity check.
func Verify(reader io.Reader, version, target string) (Bundle, error) {
	raw, err := io.ReadAll(io.LimitReader(reader, MaxDownload+1))
	if err != nil || int64(len(raw)) > MaxDownload {
		return Bundle{}, integrity("Release download exceeds its bound or cannot be read")
	}
	files, err := readMembers(raw)
	if err != nil {
		return Bundle{}, err
	}
	bundle := Bundle{Files: files, ArchiveDigest: operation.SHA256Hex(raw)}
	metadata := files["release.json"]
	if len(metadata) == 0 || len(metadata) > 1<<20 {
		return Bundle{}, integrity("Missing or oversized release metadata")
	}
	if err = operation.DecodeStrict(metadata, &bundle.Metadata); err != nil {
		return Bundle{}, integrity("Malformed release metadata")
	}
	if err = bundle.checkManifest(version, target); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func integrity(message string) error {
	return operation.Fail(operation.ExitInvalid, "release_integrity", message)
}

// readMembers reads every archive member within the file-count and expanded
// size bounds, rejecting unsafe names, special files and unexpected layout.
func readMembers(raw []byte) (map[string][]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, integrity("Release is not a gzip archive")
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	files := map[string][]byte{}
	seen := map[string]bool{}
	var total int64
	for {
		header, readErr := tr.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, integrity("Malformed release archive")
		}
		name := header.Name
		if header.Typeflag == tar.TypeDir {
			name = strings.TrimSuffix(name, "/")
		}
		if !member(name) || seen[strings.ToLower(name)] || len(seen) >= maxFiles ||
			header.Mode&0o7000 != 0 {
			return nil, integrity("Unsafe or duplicate archive member")
		}
		seen[strings.ToLower(name)] = true
		if header.Typeflag == tar.TypeDir {
			if header.Size != 0 {
				return nil, integrity("Archive directories cannot carry payload bytes")
			}
			top := slices.Contains([]string{"bin", "home", "project", "licenses"}, name)
			if !top && !allowed(name+"/") {
				return nil, integrity("Unexpected release directory")
			}
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > MaxDownload ||
			(!allowed(name) && name != "release.json") {
			return nil, integrity("Release contains an unsupported file or layout")
		}
		total += header.Size
		if total > maxExpanded {
			return nil, integrity("Expanded release exceeds its size bound")
		}
		data, readErr := io.ReadAll(io.LimitReader(tr, header.Size+1))
		if readErr != nil || int64(len(data)) != header.Size {
			return nil, integrity("Truncated release member")
		}
		files[name] = data
	}
	// Drain through the gzip checksum, retaining the same expanded bound.
	remaining := maxExpanded - total
	n, err := io.Copy(io.Discard, io.LimitReader(gz, remaining+1))
	if err != nil || n > remaining {
		return nil, integrity("Invalid gzip trailer or oversized archive padding")
	}
	return files, nil
}

// checkManifest requires the expected release, target and state format, and
// every payload file to match its manifest entry exactly.
func (b Bundle) checkManifest(version, target string) error {
	m := b.Metadata
	if m.SchemaVersion != 1 || m.StateVersion != 1 || !identifier.MatchString(m.Release) ||
		(version != "" && m.Release != version) ||
		m.Target != target ||
		!operation.ValidDigest(m.SourceDigest) {
		return integrity("Release version, target or state format is unsupported")
	}
	if len(m.Files)+1 != len(b.Files) {
		return integrity("Release manifest does not match the complete payload")
	}
	for name, entry := range m.Files {
		data, ok := b.Files[name]
		if !ok || !allowed(name) || !operation.ValidDigest(entry.SHA256) ||
			operation.SHA256Hex(data) != entry.SHA256 ||
			int64(len(data)) != entry.Size ||
			entry.Executable != (name == "bin/workbench") {
			return integrity("Release payload integrity mismatch")
		}
	}
	for _, required := range []string{"bin/workbench", ".chezmoiroot", "home/.chezmoi.toml.tmpl", "licenses/NOTICE"} {
		if len(b.Files[required]) == 0 {
			return integrity("Required release payload is missing")
		}
	}
	return nil
}

// Identity returns the release and source digest the bundle carries.
func (b Bundle) Identity() operation.SourceIdentity {
	return operation.SourceIdentity{
		Release:       b.Metadata.Release,
		ContentDigest: b.Metadata.SourceDigest,
	}
}

// Directory returns where the bundle is staged, named by release and archive
// digest.
func (b Bundle) Directory(c operation.Context) string {
	return filepath.Join(c.Paths.Data, "releases", b.Metadata.Release+"-"+b.ArchiveDigest[:16])
}

// Extract writes every verified member into the new directory, private and
// synced, without following links.
func (b Bundle) Extract(directory string) error {
	if err := os.Mkdir(directory, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	for name, data := range b.Files {
		if err = root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if name == "bin/workbench" {
			mode = 0o700
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
			return operation.Fail(
				operation.ExitConflict,
				"release_conflict",
				"Staged release was modified",
			)
		}
		actual, err := os.ReadFile(full)
		if err != nil || operation.SHA256Hex(actual) != operation.SHA256Hex(data) {
			return operation.Fail(
				operation.ExitConflict,
				"release_conflict",
				"Staged release content changed",
			)
		}
	}
	return nil
}

func (b Bundle) String() string { return fmt.Sprintf("%s (%s)", b.Metadata.Release, b.Metadata.Target) }
