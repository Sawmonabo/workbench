package project

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/jsonc"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/project/gopolicy"
	"github.com/Sawmonabo/workbench/project/pythonpolicy"
	"github.com/Sawmonabo/workbench/project/rustpolicy"
	"github.com/tailscale/hujson"
)

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
