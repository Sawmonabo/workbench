package project

import (
	"bytes"
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

func mergeExtensions(data []byte) ([]byte, error) {
	if len(data) == 0 {
		data = []byte("{}\n")
	}
	doc, err := hujson.Parse(data)
	if err != nil {
		return nil, operation.Fail(operation.ExitInvalid, "jsonc", "Invalid extensions JSONC")
	}
	if err = uniqueKeys(doc); err != nil {
		return nil, err
	}
	plain := doc.Clone()
	plain.Standardize()
	var existing map[string]json.RawMessage
	if json.Unmarshal(plain.Pack(), &existing) != nil || existing == nil {
		return nil, operation.Fail(
			operation.ExitInvalid,
			"jsonc",
			"Extension recommendations require an object",
		)
	}
	values := make(map[string][]string)
	for _, key := range []string{"recommendations", "unwantedRecommendations"} {
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
	var patches []map[string]any
	for _, key := range []string{"recommendations", "unwantedRecommendations"} {
		if _, ok := existing[key]; !ok {
			patches = append(
				patches,
				map[string]any{"op": "add", "path": "/" + key, "value": extensionPolicy[key]},
			)
			continue
		}
		for _, id := range extensionPolicy[key] {
			if !slices.Contains(values[key], id) {
				patches = append(
					patches,
					map[string]any{"op": "add", "path": "/" + key + "/-", "value": id},
				)
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
	if ownsLayout(doc) {
		// Nothing of the user's is in the file, so lay it out like any new JSON file.
		var indented bytes.Buffer
		if err = json.Indent(&indented, bytes.TrimSpace(result), "", "  "); err != nil {
			return nil, err
		}
		indented.WriteByte('\n')
		result = indented.Bytes()
	} else {
		alignInserted(doc, changed)
		result = changed.Pack()
	}
	if _, err = hujson.Parse(result); err != nil {
		return nil, err
	}
	return result, nil
}

// ownsLayout reports a document that is an empty object without comments:
// there is no user layout to preserve.
func ownsLayout(doc hujson.Value) bool {
	object, ok := doc.Value.(*hujson.Object)
	return ok && len(object.Members) == 0 &&
		len(bytes.TrimSpace(slices.Concat(doc.BeforeExtra, object.AfterExtra, doc.AfterExtra))) == 0
}

// alignInserted gives the values that Patch appended to changed the layout of
// the values the user already wrote in original, so an existing multi-line
// file keeps one entry per line. Layout is copied only from the user's own
// values; a one-line array or object has none to copy and stays as written.
func alignInserted(original, changed hujson.Value) {
	before, isObject := original.Value.(*hujson.Object)
	after, grownObject := changed.Value.(*hujson.Object)
	if !isObject || !grownObject || len(before.Members) == 0 {
		return
	}
	for i := range after.Members {
		member := &after.Members[i]
		if i < len(before.Members) {
			existing, isArray := before.Members[i].Value.Value.(*hujson.Array)
			grown, grownArray := member.Value.Value.(*hujson.Array)
			if isArray && grownArray {
				alignElements(existing, grown)
			}
			continue
		}
		indent, newline, multiline := lineLayout(after.Members[i-1].Name.BeforeExtra)
		if !multiline {
			continue
		}
		member.Name.BeforeExtra = breakLine(member.Name.BeforeExtra, indent, newline)
		if len(member.Value.BeforeExtra) == 0 {
			member.Value.BeforeExtra = hujson.Extra(" ")
		}
		if array, ok := member.Value.Value.(*hujson.Array); ok {
			for j := 1; j < len(array.Elements); j++ {
				if len(array.Elements[j].BeforeExtra) == 0 {
					array.Elements[j].BeforeExtra = hujson.Extra(" ")
				}
			}
		}
	}
}

// alignElements puts each element that Patch appended to grown on its own
// line, indented like the first element the user wrote in existing.
func alignElements(existing, grown *hujson.Array) {
	if len(existing.Elements) == 0 {
		return
	}
	indent, newline, multiline := lineLayout(existing.Elements[0].BeforeExtra)
	if !multiline {
		return
	}
	for i := len(existing.Elements); i < len(grown.Elements); i++ {
		grown.Elements[i].BeforeExtra = breakLine(grown.Elements[i].BeforeExtra, indent, newline)
	}
}

// lineLayout returns the indentation and line ending that extra, the text
// before a value, uses to start that value on its own line. multiline is false
// when the value shares its line with the text before it.
func lineLayout(extra hujson.Extra) (indent, newline string, multiline bool) {
	text := string(extra)
	last := strings.LastIndex(text, "\n")
	if last < 0 {
		return "", "", false
	}
	newline = "\n"
	if last > 0 && text[last-1] == '\r' {
		newline = "\r\n"
	}
	tail := text[last+1:]
	return tail[:len(tail)-len(strings.TrimLeft(tail, " \t"))], newline, true
}

// breakLine starts a value on its own line at indent. Any comment already in
// extra stays: hujson moves a comment written after the previous comma into
// the leading text of the first value inserted after it.
func breakLine(extra hujson.Extra, indent, newline string) hujson.Extra {
	text := strings.TrimRight(string(extra), " \t")
	if !strings.HasSuffix(text, "\n") {
		text += newline
	}
	return hujson.Extra(text + indent)
}

func uniqueKeys(value hujson.Value) error {
	switch node := value.Value.(type) {
	case *hujson.Object:
		seen := map[string]bool{}
		for _, member := range node.Members {
			name, ok := member.Name.Value.(hujson.Literal)
			if !ok || name.Kind() != '"' {
				return operation.Fail(
					operation.ExitInvalid,
					"jsonc",
					"JSONC object keys must be quoted strings",
				)
			}
			key := name.String()
			if seen[key] {
				return operation.Fail(
					operation.ExitInvalid,
					"jsonc",
					"Duplicate JSONC keys require manual repair",
				)
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
