// Package machine coordinates reviewed native chezmoi operations.
package machine

import (
	_ "embed"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Regeneration is an explicit maintainer review boundary, not runtime approval.
//
//go:generate python3 ../../scripts/generate-source-trust.py
//go:embed source-trust.json
var sourceTrust []byte

// Requirements are the generated management pins and source hashes compiled
// into the binary from source-trust.json.
type Requirements struct {
	ChezmoiSHA256  map[string]string `json:"chezmoi_sha256"`
	UVSHA256       map[string]string `json:"uv_sha256"`
	Tomlkit        string            `json:"tomlkit"`
	TomlkitSHA256  string            `json:"tomlkit_sha256"`
	TomlkitURL     string            `json:"tomlkit_url"`
	TomlkitLicense string            `json:"tomlkit_license"`
	Files          map[string]string `json:"files"`
	Chezmoi        string            `json:"chezmoi"`
	UV             string            `json:"uv"`
	UVAdditional   []string          `json:"uv_additional"`
	Python         string            `json:"python"`
	PythonMinMinor int               `json:"python_min_minor"`
	PythonMaxMinor int               `json:"python_max_minor"`
}

// ManagementRequirements is generated from the canonical version data. Release
// tooling and setup must use it rather than maintaining another version list.
func ManagementRequirements() Requirements {
	var requirements Requirements
	if err := json.Unmarshal(sourceTrust, &requirements); err != nil {
		panic("invalid compiled machine source trust")
	}
	return requirements
}

// SourceSnapshot reads the machine source at source before any native template
// evaluation. A release source (one with release.json) must match the reviewed
// hashes compiled into this executable exactly. A developer checkout is bound
// by its actual content instead: the digest enters the plan the user approves,
// so editing home/ needs no trust regeneration or rebuild until a release.
// Repository Git metadata is outside home and never copied or executed.
func SourceSnapshot(source string) (map[string][]byte, operation.SourceIdentity, error) {
	trusted := operation.SHA256Hex(sourceTrust)
	identity := operation.SourceIdentity{Release: "developer", ContentDigest: trusted}
	if source == "" {
		return nil, identity, operation.Fail(
			operation.ExitBlocked,
			"source",
			"Select --source or activate a verified source before planning",
		)
	}
	files, err := readSource(source)
	if err != nil {
		return nil, identity, err
	}
	release, err := releaseName(source, trusted)
	if err != nil {
		return nil, identity, err
	}
	if release == "" {
		identity.ContentDigest = developerDigest(files)
		return files, identity, nil
	}
	if err = checkTrusted(files); err != nil {
		return nil, identity, err
	}
	identity.Release = release
	return files, identity, nil
}

// readSource reads .chezmoiroot and every file under home/, within the preview
// input bound.
func readSource(source string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	var total int64
	read := func(path string, info fs.FileInfo) error {
		name, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return operation.Fail(
				operation.ExitBlocked,
				"source",
				"Machine source requires bounded regular files",
			)
		}
		total += info.Size()
		if total > 16<<20 {
			return operation.Fail(
				operation.ExitBlocked,
				"source",
				"Machine source exceeds the preview input bound",
			)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return operation.Fail(
				operation.ExitInvalid,
				"source",
				"Cannot read selected machine source",
			)
		}
		files[name] = data
		return nil
	}
	marker := filepath.Join(source, ".chezmoiroot")
	info, err := os.Lstat(marker)
	if err == nil {
		err = read(marker, info)
	}
	if err == nil {
		err = filepath.Walk(
			filepath.Join(source, "home"),
			func(path string, info fs.FileInfo, walkErr error) error {
				if walkErr != nil || info.IsDir() {
					return walkErr
				}
				return read(path, info)
			},
		)
	}
	return files, err
}

// checkTrusted requires files to be exactly the reviewed set compiled into
// this executable.
func checkTrusted(files map[string][]byte) error {
	reviewed := ManagementRequirements().Files
	for name, data := range files {
		if reviewed[name] != operation.SHA256Hex(data) {
			return operation.Fail(
				operation.ExitBlocked,
				"source_trust",
				"Release source differs from the reviewed hashes compiled into this executable",
			)
		}
	}
	if len(files) != len(reviewed) {
		return operation.Fail(
			operation.ExitBlocked,
			"source_trust",
			"Reviewed machine source files are missing",
		)
	}
	return nil
}

// developerDigest identifies a developer checkout by its file hashes and this
// executable's management pins.
func developerDigest(files map[string][]byte) string {
	hashes := make(map[string]string, len(files))
	for name, data := range files {
		hashes[name] = operation.SHA256Hex(data)
	}
	encoded, _ := json.Marshal(struct {
		Files map[string]string `json:"files"`
		Trust string            `json:"trust"`
	}{hashes, operation.SHA256Hex(sourceTrust)})
	return operation.SHA256Hex(encoded)
}

// releaseName returns the release named by the source's release.json, or ""
// for a developer checkout without one. Release inspection owns artifact
// integrity; the already trusted source digest binds this display identity to
// the same machine payload.
func releaseName(source, sourceDigest string) (string, error) {
	path := filepath.Join(source, "release.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", operation.Fail(
			operation.ExitBlocked,
			"source",
			"Cannot inspect selected release identity",
		)
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return "", operation.Fail(
			operation.ExitBlocked,
			"source_trust",
			"Release identity requires bounded regular metadata",
		)
	}
	metadata, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	var release struct {
		Release      string `json:"release"`
		SourceDigest string `json:"source_digest"`
	}
	if len(metadata) > 1<<20 || json.Unmarshal(metadata, &release) != nil ||
		release.Release == "" ||
		release.SourceDigest != sourceDigest {
		return "", operation.Fail(
			operation.ExitBlocked,
			"source_trust",
			"Release metadata does not match the compiled machine payload",
		)
	}
	return release.Release, nil
}
