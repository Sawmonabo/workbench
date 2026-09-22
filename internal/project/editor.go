package project

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	pythonpolicy "github.com/Sawmonabo/workbench/project/python"
	"github.com/tailscale/hujson"
)

//go:embed adapter.py
var adapter string

func transform(ctx context.Context, c operation.Context, python operation.Dependency, library, version, release string, documents []string) ([]string, error) {
	input, err := json.Marshal(map[string]any{"version": version, "policy": pythonpolicy.Policy, "release": release, "documents": documents})
	if err != nil {
		return nil, err
	}
	output, err := operation.Run(ctx, c, nil, operation.Process{Executable: python.Path, Args: []string{"-I", "-S", "-B", "-c", adapter, library}, Directory: "/", Environment: []string{"PATH=/usr/bin:/bin"}, Input: input, Timeout: 15 * time.Second, OutputLimit: 8 << 20})
	if err != nil {
		return nil, operation.Fail(3, "project_editor", "TOML preservation adapter failed; review unsupported syntax or provenance before changing files")
	}
	var result []string
	if json.Unmarshal([]byte(output.Stdout), &result) != nil || len(result) != len(documents) {
		return nil, operation.Fail(1, "project_editor", "Invalid bounded adapter response")
	}
	return result, nil
}

func mergeExtensions(data []byte) ([]byte, error) {
	if len(data) == 0 {
		data = []byte("{}\n")
	}
	doc, err := hujson.Parse(data)
	if err != nil {
		return nil, operation.Fail(2, "jsonc", "Invalid extensions JSONC")
	}
	if err = uniqueKeys(doc); err != nil {
		return nil, err
	}
	plain := doc.Clone()
	plain.Standardize()
	var existing map[string]json.RawMessage
	if json.Unmarshal(plain.Pack(), &existing) != nil || existing == nil {
		return nil, operation.Fail(2, "jsonc", "Extension recommendations require an object")
	}
	var policy map[string][]string
	_ = json.Unmarshal(pythonpolicy.Extensions, &policy)
	values := make(map[string][]string)
	for _, key := range []string{"recommendations", "unwantedRecommendations"} {
		if raw, ok := existing[key]; ok {
			var entries []string
			if json.Unmarshal(raw, &entries) != nil {
				return nil, operation.Fail(2, "jsonc", "Recommendations must be arrays of strings")
			}
			values[key] = entries
		}
	}
	for _, id := range append(append([]string{}, values["recommendations"]...), policy["recommendations"]...) {
		if slices.Contains(values["unwantedRecommendations"], id) || slices.Contains(policy["unwantedRecommendations"], id) {
			return nil, operation.Fail(4, "extensions", "Contradictory recommendations require a project decision")
		}
	}
	var patches []map[string]any
	for _, key := range []string{"recommendations", "unwantedRecommendations"} {
		if _, ok := existing[key]; !ok {
			patches = append(patches, map[string]any{"op": "add", "path": "/" + key, "value": policy[key]})
			continue
		}
		for _, id := range policy[key] {
			if !slices.Contains(values[key], id) {
				patches = append(patches, map[string]any{"op": "add", "path": "/" + key + "/-", "value": id})
			}
		}
	}
	if len(patches) == 0 {
		return data, nil
	}
	patch, err := json.Marshal(patches)
	if err != nil {
		return nil, err
	}
	changed := doc.Clone()
	if err = changed.Patch(patch); err != nil {
		return nil, err
	}
	result := changed.Pack()
	if _, err = hujson.Parse(result); err != nil {
		return nil, err
	}
	return result, nil
}

func uniqueKeys(value hujson.Value) error {
	switch node := value.Value.(type) {
	case *hujson.Object:
		seen := map[string]bool{}
		for _, member := range node.Members {
			name, ok := member.Name.Value.(hujson.Literal)
			if !ok || name.Kind() != '"' {
				return operation.Fail(2, "jsonc", "JSONC object keys must be quoted strings")
			}
			key := name.String()
			if seen[key] {
				return operation.Fail(2, "jsonc", "Duplicate JSONC keys require manual repair")
			}
			seen[key] = true
			if err := uniqueKeys(member.Value); err != nil {
				return err
			}
		}
	case *hujson.Array:
		for _, element := range node.Elements {
			if err := uniqueKeys(element); err != nil {
				return err
			}
		}
	}
	return nil
}

// Append only reviewed patterns. Existing negations may change the meaning of
// an appended rule; such files get a proposal instead of speculative rewriting.
func mergeIgnore(data []byte) ([]byte, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "!") && line != "!.env.example" {
			return nil, operation.Fail(3, "gitignore", "Existing negations require review before adding ignore rules")
		}
	}
	result := string(data)
	for _, entry := range strings.Split(strings.TrimSpace(pythonpolicy.Ignore), "\n") {
		// Test, distribution, coverage and secret-file patterns are optional assets;
		// their relevance cannot be inferred merely from Python ownership.
		if !slices.Contains([]string{".venv/", "__pycache__/", "*.py[cod]", ".ruff_cache/"}, entry) {
			continue
		}
		if !slices.Contains(lines, entry) {
			if result != "" && !strings.HasSuffix(result, "\n") {
				result += "\n"
			}
			result += entry + "\n"
		}
	}
	return []byte(result), nil
}

func adapterDependency(ctx context.Context, c operation.Context) (operation.Dependency, string, string, []operation.Dependency, error) {
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return operation.Dependency{}, "", "", nil, err
	}
	var recorded []operation.Dependency
	if state != nil {
		recorded = state.Dependencies
	}
	deps, _ := machine.ResolveDependencies(ctx, c, recorded, os.Getenv("PATH"))
	library, err := machine.TomlkitPath(c)
	if err != nil {
		return operation.Dependency{}, "", "", deps, err
	}
	for _, dep := range deps {
		if dep.Name == "python3" {
			return dep, library, filepath.Base(filepath.Dir(library)), deps, nil
		}
	}
	return operation.Dependency{}, "", "", deps, operation.Fail(3, "dependency", fmt.Sprintf("Approved management Python and TOML Kit setup required before project preview (%s)", filepath.Base(library)))
}
