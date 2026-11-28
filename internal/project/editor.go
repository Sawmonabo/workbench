package project

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/jsonc"
	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/project/gopolicy"
	"github.com/Sawmonabo/workbench/project/pythonpolicy"
	"github.com/Sawmonabo/workbench/project/rustpolicy"
	"github.com/tailscale/hujson"
)

//go:embed adapter.py
var adapter string

// adapterDocument is one manifest the TOML Kit adapter merges a policy into.
type adapterDocument struct {
	// Path names the manifest when its layout is refused; it is not sent.
	Path   string `json:"-"`
	Source string `json:"source"`
	Policy string `json:"policy"`
	// PolicyID is recorded at Provenance, the record's key path; a document
	// without a Provenance gets no record.
	PolicyID   string   `json:"policy_id,omitempty"`
	Provenance []string `json:"provenance"`
	// Ruff leaves the policy's Ruff rule selection out when the document has
	// a selection of its own.
	Ruff bool `json:"ruff"`
}

// checkRecord refuses a manifest's Workbench record that the adapter would
// refuse, naming the record and what it needs: a table at provenance in doc,
// the decoded manifest at path, below tables only, with the integer
// schema_version = 1 and, when present, a policies list of names.
func checkRecord(path string, doc map[string]any, provenance []string) error {
	var record any = doc
	valid := true
	for _, key := range provenance {
		fields, isTable := record.(map[string]any)
		if !isTable {
			valid = false
			break
		}
		value, ok := fields[key]
		if !ok {
			return nil
		}
		record = value
	}
	fields, isTable := record.(map[string]any)
	version, isInteger := fields["schema_version"].(int64)
	policies, hasPolicies := fields["policies"]
	list, isList := policies.([]any)
	names := !slices.ContainsFunc(list, func(policy any) bool {
		_, isText := policy.(string)
		return !isText
	})
	if valid && isTable && isInteger && version == 1 && (!hasPolicies || (isList && names)) {
		return nil
	}
	return operation.Fail(
		operation.ExitBlocked,
		"provenance",
		"The Workbench record ["+strings.Join(provenance, ".")+"] in "+path+
			" needs the integer schema_version = 1 and a policies list of names; correct it before planning",
	)
}

// tomlAdapter is the private Python and TOML Kit that edit manifests for
// proposal.
type tomlAdapter struct {
	python           operation.Dependency
	library, version string
	proposal         *Proposal
}

// adapter returns the private TOML Kit adapter, found once per plan, and binds
// its version and the apply options as plan inputs.
func (p *Proposal) adapter(
	ctx context.Context,
	c operation.Context,
	options ApplyOptions,
) (*tomlAdapter, error) {
	if p.toml != nil {
		return p.toml, nil
	}
	python, library, version, deps, err := adapterDependency(ctx, c)
	p.Plan.Dependencies = deps
	if err != nil {
		p.Plan.Prerequisites = append(
			p.Plan.Prerequisites,
			"Workbench's private Python and TOML Kit are missing; "+machine.InstallHint(c),
		)
		return nil, err
	}
	optionsData, _ := json.Marshal(options)
	p.Plan.Inputs = append(
		p.Plan.Inputs,
		operation.Input{Name: "tomlkit", Digest: operation.SHA256Hex([]byte(library + version))},
		operation.Input{Name: "project-options", Digest: operation.SHA256Hex(optionsData)},
	)
	p.toml = &tomlAdapter{python: python, library: library, version: version, proposal: p}
	return p.toml, nil
}

// transform merges each document's policy through the adapter and returns the
// rendered documents in order. A document whose layout the adapter refuses,
// because the merge would change the meaning of keys it already has or would
// add to an inline table, blocks the plan with a prerequisite that names it.
func (a *tomlAdapter) transform(
	ctx context.Context,
	c operation.Context,
	documents []adapterDocument,
) ([]string, error) {
	input, err := json.Marshal(
		map[string]any{
			"version":   a.version,
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
			Executable:  a.python.Path,
			Args:        []string{"-I", "-S", "-B", "-c", adapter, a.library},
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
	var refusal struct {
		Refused *int   `json:"refused"`
		Reason  string `json:"reason"`
	}
	if strings.HasPrefix(output.Stdout, "{") &&
		json.Unmarshal([]byte(output.Stdout), &refusal) == nil && refusal.Refused != nil &&
		*refusal.Refused >= 0 && *refusal.Refused < len(documents) {
		a.proposal.Plan.Prerequisites = append(
			a.proposal.Plan.Prerequisites,
			documents[*refusal.Refused].Path+": its layout cannot be merged safely: "+refusal.Reason,
		)
		return nil, a.proposal.blocked(
			"project_editor",
			"A manifest's layout needs review; no files changed",
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

// Each policy's embedded recommendation lists. An invalid embedded asset is
// a build defect, so it fails at startup like a bad regexp.
var (
	languageExtensions = map[string]map[string][]string{
		"go":     embeddedExtensions(gopolicy.Extensions),
		"python": embeddedExtensions(pythonpolicy.Extensions),
		"rust":   embeddedExtensions(rustpolicy.Extensions),
	}
)

func embeddedExtensions(data []byte) map[string][]string {
	var policy map[string][]string
	if err := json.Unmarshal(data, &policy); err != nil {
		panic("invalid embedded extensions.json: " + err.Error())
	}
	return policy
}

// extensionPolicy combines the recommendation lists of languages, each id
// once, in the order the languages are configured.
func extensionPolicy(languages []string) map[string][]string {
	policy := make(map[string][]string)
	for _, language := range configurable {
		if !slices.Contains(languages, language) {
			continue
		}
		lists := languageExtensions[language]
		for _, key := range extensionKeys {
			for _, id := range lists[key] {
				if !slices.Contains(policy[key], id) {
					policy[key] = append(policy[key], id)
				}
			}
		}
	}
	return policy
}

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

// mergeExtensions returns the merge of extensionPolicy into a project's
// .vscode/extensions.json. A list the policy leaves empty is never added.
func mergeExtensions(extensionPolicy map[string][]string) func([]byte) ([]byte, error) {
	return func(data []byte) ([]byte, error) { return mergeExtensionLists(data, extensionPolicy) }
}

func mergeExtensionLists(data []byte, extensionPolicy map[string][]string) ([]byte, error) {
	if len(data) == 0 {
		data = []byte("{}\n")
	}
	var existing map[string]json.RawMessage
	doc, err := jsonc.DecodeObject(data, &existing)
	if err != nil {
		return nil, operation.Fail(operation.ExitInvalid, "jsonc", extensionsReason(err))
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
		if len(extensionPolicy[key]) == 0 {
			continue
		}
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

// extensionsReason is why an extensions file that [jsonc.DecodeObject]
// refused is left as it is.
func extensionsReason(err error) string {
	switch {
	case errors.Is(err, jsonc.ErrNotObject):
		return "Extension recommendations require an object"
	case errors.Is(err, jsonc.ErrUnquotedKey):
		return "JSONC object keys must be quoted strings"
	case errors.Is(err, jsonc.ErrDuplicateKey):
		return "Duplicate JSONC keys require manual repair"
	}
	return "Invalid extensions JSONC"
}

// addExtensions writes additions into doc, the project's validated extensions
// file, in the style of the user's own text.
func addExtensions(doc hujson.Value, additions []extensionAddition) ([]byte, error) {
	// Nothing of the user's is in an empty file, so it is laid out like any new JSON file.
	owned := jsonc.OwnsLayout(doc)
	root, _ := doc.Value.(*hujson.Object) // DecodeObject refused any other document
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

// pythonIgnore is the part of pythonpolicy.Ignore --gitignore appends. Test,
// distribution, coverage and secret-file patterns are optional assets; their
// relevance cannot be inferred merely from Python ownership.
var pythonIgnore = []string{".venv/", "__pycache__/", "*.py[cod]", ".ruff_cache/"}

// ignoreEntries returns the entries --gitignore appends for languages, each
// once, in the order the languages are configured.
func ignoreEntries(languages []string) []string {
	var entries []string
	for _, language := range configurable {
		if !slices.Contains(languages, language) {
			continue
		}
		add := pythonIgnore
		switch language {
		case "go":
			add = strings.Split(strings.TrimSpace(gopolicy.Ignore), "\n")
		case "rust":
			add = strings.Split(strings.TrimSpace(rustpolicy.Ignore), "\n")
		}
		for _, entry := range add {
			if !slices.Contains(entries, entry) {
				entries = append(entries, entry)
			}
		}
	}
	return entries
}

// ignoreDescription describes the --gitignore edit for languages.
func ignoreDescription(languages []string) string {
	switch {
	case slices.Equal(languages, []string{"python"}):
		return "Append relevant Python environment/cache ignore entries preserving order"
	case slices.Equal(languages, []string{"go"}):
		return "Append Go build and test output ignore entries preserving order"
	case slices.Equal(languages, []string{"rust"}):
		return "Append the Rust build output ignore entry preserving order"
	case slices.Equal(languages, []string{"go", "python"}):
		return "Append relevant Python environment/cache and Go build output ignore entries preserving order"
	}
	parts := map[string]string{
		"go":     "Go build output",
		"python": "Python environment/cache",
		"rust":   "Rust build output",
	}
	var named []string
	for _, language := range []string{"python", "go", "rust"} {
		if slices.Contains(languages, language) {
			named = append(named, parts[language])
		}
	}
	return "Append relevant " + strings.Join(named[:len(named)-1], ", ") + " and " +
		named[len(named)-1] + " ignore entries preserving order"
}

// knownNegations are the negation lines no appended entry can change the
// meaning of: Python's secret-file example and the exception that lets Git
// commit the extension recommendations.
var knownNegations = []string{"!.env.example", "!.vscode/extensions.json"}

// mergeIgnore returns the edit that appends the absent entries to a
// .gitignore. Other negations may change the meaning of an appended rule, so
// a file holding one gets a proposal instead of speculative rewriting; a file
// that already holds every entry is left as it is, negations and all.
func mergeIgnore(entries []string) func([]byte) ([]byte, error) {
	return func(data []byte) ([]byte, error) {
		lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		var missing []string
		for _, entry := range entries {
			if !slices.Contains(lines, entry) {
				missing = append(missing, entry)
			}
		}
		if len(missing) == 0 {
			return data, nil
		}
		for _, line := range lines {
			if strings.HasPrefix(strings.TrimSpace(line), "!") &&
				!slices.Contains(knownNegations, line) {
				return nil, operation.Fail(
					operation.ExitBlocked,
					"gitignore",
					"Existing negations require review before adding ignore rules",
				)
			}
		}
		result := string(data)
		for _, entry := range missing {
			if result != "" && !strings.HasSuffix(result, "\n") {
				result += "\n"
			}
			result += entry + "\n"
		}
		return []byte(result), nil
	}
}

// warnIgnoredExtensions warns when owner's .gitignore, as the plan leaves it,
// ignores .vscode/ without excepting extensions.json: Git would then never
// commit the recommendations. Only these literal lines are checked; ignore
// rules are not evaluated.
func (p *Proposal) warnIgnoredExtensions(owner string) error {
	path := filepath.Join(owner, ".gitignore")
	data, err := readMetadata(path)
	if err != nil {
		return err
	}
	if change := p.change(path); change != nil {
		data = change.After.Data
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	ignored := false
	for _, line := range lines {
		switch strings.TrimSpace(line) {
		case ".vscode/", ".vscode/*", "/.vscode/", "/.vscode/*":
			ignored = true
		case "!.vscode/extensions.json", "!/.vscode/extensions.json":
			return nil
		}
	}
	if ignored {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			path+" ignores .vscode/, so Git will not commit .vscode/extensions.json; "+
				"to share the recommendations, ignore .vscode/* and add !.vscode/extensions.json",
		)
	}
	return nil
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
