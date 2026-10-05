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

	"github.com/Sawmonabo/workbench/internal/jsonc"
	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	pythonpolicy "github.com/Sawmonabo/workbench/project/python"
	"github.com/tailscale/hujson"
)

//go:embed adapter.py
var adapter string

func transform(
	ctx context.Context,
	c operation.Context,
	python operation.Dependency,
	library, version string,
	documents []string,
) ([]string, error) {
	input, err := json.Marshal(
		map[string]any{
			"version":   version,
			"policy":    pythonpolicy.Policy,
			"policy_id": pythonpolicy.ID,
			"documents": documents,
		},
	)
	if err != nil {
		return nil, err
	}
	output, err := operation.Run(
		ctx,
		c,
		nil,
		operation.Process{
			Executable:  python.Path,
			Args:        []string{"-I", "-S", "-B", "-c", adapter, library},
			Directory:   "/",
			Environment: []string{"PATH=/usr/bin:/bin"},
			Input:       input,
			Timeout:     15 * time.Second,
			OutputLimit: 8 << 20,
		},
	)
	if err != nil {
		return nil, operation.Fail(
			operation.ExitBlocked,
			"project_editor",
			"TOML preservation adapter failed; review unsupported syntax or provenance before changing files",
		)
	}
	var result []string
	if json.Unmarshal([]byte(output.Stdout), &result) != nil || len(result) != len(documents) {
		return nil, operation.Fail(
			operation.ExitFailed,
			"project_editor",
			"Invalid bounded adapter response",
		)
	}
	return result, nil
}

// extensionPolicy is the embedded recommendation policy. An invalid embedded
// asset is a build defect, so it fails at startup like a bad regexp.
var extensionPolicy = func() map[string][]string {
	var policy map[string][]string
	if err := json.Unmarshal(pythonpolicy.Extensions, &policy); err != nil {
		panic("invalid embedded extensions.json: " + err.Error())
	}
	return policy
}()

// extensionKeys are the two lists of .vscode/extensions.json Workbench keeps in
// line with the project policy, in the order it adds them.
var extensionKeys = []string{"recommendations", "unwantedRecommendations"}

// extensionAddition is what the policy adds to one list of a project's
// extension recommendations: the whole list when the project has none, or the
// entries it lacks.
type extensionAddition struct {
	key string
	ids []string
	// whole is true when the list is absent and ids is all of it.
	whole bool
}

func mergeExtensions(data []byte) ([]byte, error) {
	if len(data) == 0 {
		data = []byte("{}\n")
	}
	doc, err := jsonc.Parse(data)
	if err != nil {
		return nil, operation.Fail(operation.ExitInvalid, "jsonc", "Invalid extensions JSONC")
	}
	if err = jsonc.UniqueKeys(doc); err != nil {
		return nil, err
	}
	var existing map[string]json.RawMessage
	if json.Unmarshal(jsonc.Plain(doc), &existing) != nil || existing == nil {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"jsonc",
			"Extension recommendations require an object",
		)
	}
	values := make(map[string][]string)
	for _, key := range extensionKeys {
		if raw, ok := existing[key]; ok {
			var entries []string
			if json.Unmarshal(raw, &entries) != nil {
				return nil, operation.Fail(
					operation.ExitInvalid,
					"jsonc",
					"Recommendations must be arrays of strings",
				)
			}
			values[key] = entries
		}
	}
	for _, id := range append(append([]string{}, values["recommendations"]...), extensionPolicy["recommendations"]...) {
		if slices.Contains(values["unwantedRecommendations"], id) ||
			slices.Contains(extensionPolicy["unwantedRecommendations"], id) {
			return nil, operation.Fail(
				operation.ExitConflict,
				"extensions",
				"Contradictory recommendations require a project decision",
			)
		}
	}
	var additions []extensionAddition
	for _, key := range extensionKeys {
		if _, ok := existing[key]; !ok {
			additions = append(additions, extensionAddition{key, extensionPolicy[key], true})
			continue
		}
		var missing []string
		for _, id := range extensionPolicy[key] {
			if !slices.Contains(values[key], id) {
				missing = append(missing, id)
			}
		}
		if len(missing) > 0 {
			additions = append(additions, extensionAddition{key, missing, false})
		}
	}
	if len(additions) == 0 {
		return data, nil
	}
	return addExtensions(doc, additions)
}

// addExtensions writes additions into doc, the project's validated extensions
// file, in the style of the user's own text.
func addExtensions(doc hujson.Value, additions []extensionAddition) ([]byte, error) {
	// Nothing of the user's is in an empty file, so it is laid out like any new JSON file.
	owned := jsonc.OwnsLayout(doc)
	root, isObject := doc.Value.(*hujson.Object)
	if !isObject {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"jsonc",
			"Extension recommendations require an object",
		)
	}
	lay := jsonc.RootLayout(doc, "  ")
	for _, addition := range additions {
		if addition.whole {
			if err := jsonc.AppendMember(root, lay, addition.key, addition.ids); err != nil {
				return nil, err
			}
			continue
		}
		list := jsonc.Entries(root, addition.key)
		if list == nil {
			return nil, operation.Fail(
				operation.ExitInvalid,
				"jsonc",
				"Recommendations must be arrays of strings",
			)
		}
		listLay := jsonc.ArrayLayout(list, lay)
		for _, id := range addition.ids {
			if err := jsonc.AppendElement(list, listLay, id); err != nil {
				return nil, err
			}
		}
	}
	result := doc.Pack()
	if owned {
		var err error
		if result, err = jsonc.Indent(result, "  "); err != nil {
			return nil, err
		}
	}
	if _, err := hujson.Parse(result); err != nil {
		return nil, err
	}
	return result, nil
}

// Append only reviewed patterns. Existing negations may change the meaning of
// an appended rule; such files get a proposal instead of speculative rewriting.
func mergeIgnore(data []byte) ([]byte, error) {
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "!") && line != "!.env.example" {
			return nil, operation.Fail(
				operation.ExitBlocked,
				"gitignore",
				"Existing negations require review before adding ignore rules",
			)
		}
	}
	result := string(data)
	for entry := range strings.SplitSeq(strings.TrimSpace(pythonpolicy.Ignore), "\n") {
		// Test, distribution, coverage and secret-file patterns are optional assets;
		// their relevance cannot be inferred merely from Python ownership.
		if !slices.Contains(
			[]string{".venv/", "__pycache__/", "*.py[cod]", ".ruff_cache/"},
			entry,
		) {
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

func adapterDependency(
	ctx context.Context,
	c operation.Context,
) (operation.Dependency, string, string, []operation.Dependency, error) {
	state, err := operation.ReadState(c.Paths)
	if err != nil {
		return operation.Dependency{}, "", "", nil, err
	}
	var recorded []operation.Dependency
	if state != nil {
		recorded = state.Dependencies
	}
	requirements, err := machine.ManagementRequirements(c)
	if err != nil {
		return operation.Dependency{}, "", "", nil, err
	}
	deps, _ := machine.ResolveDependencies(ctx, c, requirements, recorded, os.Getenv("PATH"))
	library, err := machine.TomlkitPath(c, requirements)
	if err != nil {
		return operation.Dependency{}, "", "", deps, err
	}
	for _, dep := range deps {
		if dep.Name == "python3" {
			return dep, library, filepath.Base(filepath.Dir(library)), deps, nil
		}
	}
	return operation.Dependency{}, "", "", deps, operation.Fail(
		operation.ExitBlocked,
		"dependency",
		fmt.Sprintf(
			"Approved management Python and TOML Kit setup required before project preview (%s)",
			filepath.Base(library),
		),
	)
}
