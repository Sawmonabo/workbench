// Package machine coordinates reviewed native chezmoi operations.
package machine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/pelletier/go-toml/v2"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// releaseSource is the machine source digest a release build stamps into its
// executable (-ldflags -X). It is empty in any other build, which therefore
// accepts no release source.
var releaseSource string

// versionsFile is the machine source file that holds the management pins.
const versionsFile = "home/.chezmoidata/versions.toml"

// Requirements are the management pins from home/.chezmoidata/versions.toml.
type Requirements struct {
	ChezmoiSHA256  map[string]string
	UVSHA256       map[string]string
	Tomlkit        string
	TomlkitSHA256  string
	TomlkitURL     string
	TomlkitLicense string
	Chezmoi        string
	UV             string
	UVAdditional   []string
	Python         string
	PythonMinMinor int
	PythonMaxMinor int
	GitHubActions  map[string]Action
}

// Action is a GitHub Action pinned by commit, with the version it tags.
type Action struct {
	Version string `toml:"version"`
	Commit  string `toml:"commit"`
}

// Uses returns the workflow reference for the pinned action name.
func (r Requirements) Uses(name string) (string, error) {
	action, ok := r.GitHubActions[name]
	if !ok {
		return "", fmt.Errorf("no pinned GitHub Action %s in versions.toml", name)
	}
	return name + "@" + action.Commit + " # " + action.Version, nil
}

// ManagementRequirements reads the pins from the machine source this command
// uses: --source, else the active release. The source passes the same checks
// as any other read, so a release's pins are the ones it was released with.
func ManagementRequirements(c operation.Context) (Requirements, error) {
	source := c.Native.Source
	if source == "" {
		state, err := operation.ReadState(c.Paths)
		if err != nil {
			return Requirements{}, err
		}
		if state != nil && state.ActiveRelease != nil {
			source = state.ActiveRelease.Source
		}
	}
	if source == "" {
		return Requirements{}, operation.Fail(
			operation.ExitBlocked,
			"source",
			"No machine source to read tool versions from; pass --source or install a release",
		)
	}
	files, _, err := SourceSnapshot(source, c.Native.Developer)
	if err != nil {
		return Requirements{}, err
	}
	return SourceRequirements(files)
}

// SourceRequirements reads the pins from a machine source snapshot.
func SourceRequirements(files map[string][]byte) (Requirements, error) {
	requirements, err := ParseRequirements(files[versionsFile])
	if err != nil {
		return Requirements{}, operation.Fail(
			operation.ExitInvalid,
			"source",
			"The machine source's "+versionsFile+" is invalid: "+err.Error(),
		)
	}
	return requirements, nil
}

// ParseRequirements reads the management pins from a versions.toml. Setup
// management uses versions.uv and the first versions.python_pinned.
func ParseRequirements(data []byte) (Requirements, error) {
	var file struct {
		Versions struct {
			UV           string   `toml:"uv"`
			PythonPinned []string `toml:"python_pinned"`
		} `toml:"versions"`
		Management struct {
			Chezmoi        string            `toml:"chezmoi"`
			ChezmoiSHA256  map[string]string `toml:"chezmoi_sha256"`
			UVAdditional   []string          `toml:"uv_additional"`
			UVSHA256       map[string]string `toml:"uv_sha256"`
			PythonMinMinor int               `toml:"python_min_minor"`
			PythonMaxMinor int               `toml:"python_max_minor"`
			Tomlkit        string            `toml:"tomlkit"`
			TomlkitSHA256  string            `toml:"tomlkit_sha256"`
			TomlkitURL     string            `toml:"tomlkit_url"`
			TomlkitLicense string            `toml:"tomlkit_license"`
		} `toml:"management"`
		GitHubActions map[string]Action `toml:"github_actions"`
	}
	if err := toml.Unmarshal(data, &file); err != nil {
		return Requirements{}, err
	}
	if len(file.Versions.PythonPinned) == 0 {
		return Requirements{}, fmt.Errorf("versions.python_pinned is empty")
	}
	m := file.Management
	return Requirements{
		ChezmoiSHA256:  m.ChezmoiSHA256,
		UVSHA256:       m.UVSHA256,
		Tomlkit:        m.Tomlkit,
		TomlkitSHA256:  m.TomlkitSHA256,
		TomlkitURL:     m.TomlkitURL,
		TomlkitLicense: m.TomlkitLicense,
		Chezmoi:        m.Chezmoi,
		UV:             file.Versions.UV,
		UVAdditional:   m.UVAdditional,
		Python:         file.Versions.PythonPinned[0],
		PythonMinMinor: m.PythonMinMinor,
		PythonMaxMinor: m.PythonMaxMinor,
		GitHubActions:  file.GitHubActions,
	}, nil
}

// SourceDigest identifies machine source content: the SHA-256 of one
// "<sha256>  <path>" line per file, sorted by path, as sha256sum prints them.
// scripts/package-release.py computes the same digest for release.json.
func SourceDigest(files map[string][]byte) string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	listing := sha256.New()
	for _, name := range names {
		_, _ = fmt.Fprintf(listing, "%s  %s\n", operation.SHA256Hex(files[name]), name)
	}
	return hex.EncodeToString(listing.Sum(nil))
}

// SourceSnapshot reads the machine source at source before any native template
// evaluation. A release source (one with release.json) must be exactly the
// source its release build digested into release.json and stamped into this
// executable, so no other code runs during a release preview. Only a
// developer source, one selected with --source, may lack release.json: that
// checkout is bound by its actual content instead, and the digest enters the
// plan the user approves, so editing home/ needs no rebuild. Repository Git
// metadata is outside home and never copied or executed.
func SourceSnapshot(
	source string,
	developer bool,
) (map[string][]byte, operation.SourceIdentity, error) {
	if source == "" {
		return nil, operation.SourceIdentity{}, operation.Fail(
			operation.ExitBlocked,
			"source",
			"Select --source or activate a verified source before planning",
		)
	}
	files, err := readSource(source)
	if err != nil {
		return nil, operation.SourceIdentity{}, err
	}
	identity, err := releaseIdentity(source)
	switch {
	case err != nil:
		return nil, operation.SourceIdentity{}, err
	case identity.Release != "":
		if releaseSource == "" || identity.ContentDigest != releaseSource ||
			SourceDigest(files) != releaseSource {
			return nil, operation.SourceIdentity{}, operation.Fail(
				operation.ExitBlocked,
				"source_trust",
				"Release source differs from the source this executable was released with",
			)
		}
		return files, identity, nil
	case developer:
		identity = operation.SourceIdentity{
			Release:       "developer",
			ContentDigest: SourceDigest(files),
		}
		return files, identity, nil
	default:
		return nil, operation.SourceIdentity{}, operation.Fail(
			operation.ExitBlocked,
			"source_trust",
			"Release source has no release.json identity; reinstall the release",
		)
	}
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

// releaseIdentity returns the release and source digest named by the source's
// release.json, or an empty identity for a developer checkout without one.
// Release inspection owns artifact integrity.
func releaseIdentity(source string) (operation.SourceIdentity, error) {
	path := filepath.Join(source, "release.json")
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return operation.SourceIdentity{}, nil
	}
	if err != nil {
		return operation.SourceIdentity{}, operation.Fail(
			operation.ExitBlocked,
			"source",
			"Cannot inspect selected release identity",
		)
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return operation.SourceIdentity{}, operation.Fail(
			operation.ExitBlocked,
			"source_trust",
			"Release identity requires bounded regular metadata",
		)
	}
	metadata, err := os.ReadFile(path)
	if err != nil {
		return operation.SourceIdentity{}, err
	}
	var release struct {
		Release      string `json:"release"`
		SourceDigest string `json:"source_digest"`
	}
	if len(metadata) > 1<<20 || json.Unmarshal(metadata, &release) != nil ||
		release.Release == "" || !operation.ValidDigest(release.SourceDigest) {
		return operation.SourceIdentity{}, operation.Fail(
			operation.ExitBlocked,
			"source_trust",
			"Release metadata names no release and source digest",
		)
	}
	return operation.SourceIdentity{
		Release:       release.Release,
		ContentDigest: release.SourceDigest,
	}, nil
}
