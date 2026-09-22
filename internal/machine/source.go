// Package machine coordinates reviewed native chezmoi operations.
package machine

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
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

func digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// SourceSnapshot checks every source byte before any native template evaluation.
// Repository Git metadata is outside home and never copied or executed.
func SourceSnapshot(source string) (map[string][]byte, operation.SourceIdentity, error) {
	identity := operation.SourceIdentity{Release: "developer", ContentDigest: digest(sourceTrust)}
	if source == "" {
		return nil, identity, operation.Fail(
			3,
			"source",
			"Select --source or activate a verified source before planning",
		)
	}
	requirements := ManagementRequirements()
	files := make(map[string][]byte)
	var total int64
	read := func(path string, info fs.FileInfo) error {
		name, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > 16<<20 {
			return operation.Fail(3, "source", "Machine source requires bounded regular files")
		}
		total += info.Size()
		if total > 16<<20 {
			return operation.Fail(3, "source", "Machine source exceeds the preview input bound")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return operation.Fail(2, "source", "Cannot read selected machine source")
		}
		if requirements.Files[name] != digest(data) {
			return operation.Fail(
				3,
				"source_trust",
				"Source differs from reviewed executable inputs; review changes, regenerate source trust and rebuild",
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
				if walkErr != nil {
					return walkErr
				}
				if info.IsDir() {
					return nil
				}
				return read(path, info)
			},
		)
	}
	if err != nil {
		return nil, identity, err
	}
	if len(files) != len(requirements.Files) {
		return nil, identity, operation.Fail(
			3,
			"source_trust",
			"Reviewed machine source files are missing",
		)
	}
	// Release inspection owns artifact integrity; the already trusted source
	// digest binds this display identity to the same machine payload.
	metadataPath := filepath.Join(source, "release.json")
	if metadataInfo, statErr := os.Lstat(metadataPath); statErr == nil {
		if !metadataInfo.Mode().IsRegular() || metadataInfo.Size() > 1<<20 {
			return nil, identity, operation.Fail(
				3,
				"source_trust",
				"Release identity requires bounded regular metadata",
			)
		}
		metadata, readErr := os.ReadFile(metadataPath)
		if readErr != nil {
			return nil, identity, readErr
		}
		var release struct {
			Release      string `json:"release"`
			SourceDigest string `json:"source_digest"`
		}
		if len(metadata) > 1<<20 || json.Unmarshal(metadata, &release) != nil ||
			release.Release == "" ||
			release.SourceDigest != identity.ContentDigest {
			return nil, identity, operation.Fail(
				3,
				"source_trust",
				"Release metadata does not match the compiled machine payload",
			)
		}
		identity.Release = release.Release
	} else if !os.IsNotExist(statErr) {
		return nil, identity, operation.Fail(
			3,
			"source",
			"Cannot inspect selected release identity",
		)
	}
	return files, identity, nil
}
