package project

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/project/gopolicy"
	"github.com/Sawmonabo/workbench/project/pythonpolicy"
	"github.com/Sawmonabo/workbench/project/rustpolicy"
	"github.com/goccy/go-yaml"
)

// languageEcosystems are the Dependabot ecosystems of each policy.
var languageEcosystems = map[string]string{
	"go":     gopolicy.Ecosystem,
	"python": pythonpolicy.Ecosystem,
	"rust":   rustpolicy.Ecosystem,
}

// dependabotConfigs are the two names Dependabot reads its configuration
// from, in its order: dependabot-core's config file fetcher takes the first
// that exists.
var dependabotConfigs = []string{
	filepath.Join(".github", "dependabot.yml"),
	filepath.Join(".github", "dependabot.yaml"),
}

// renovateConfigs are the files Renovate reads its configuration from in a
// GitHub repository, besides a "renovate" key in package.json. Renovate reads
// .gitlab/renovate.json* only on GitLab.
var renovateConfigs = []string{
	"renovate.json", "renovate.jsonc", "renovate.json5",
	filepath.Join(".github", "renovate.json"),
	filepath.Join(".github", "renovate.jsonc"),
	filepath.Join(".github", "renovate.json5"),
	".renovaterc", ".renovaterc.json", ".renovaterc.jsonc", ".renovaterc.json5",
}

// addDependabot plans .github/dependabot.yml with weekly updates for the
// ecosystems of languages and for GitHub Actions, unless the repository has
// a Dependabot configuration of its own, which is kept byte for byte; the
// plan then names the ecosystems it lacks. A Renovate configuration is
// reported, since both bots would open update pull requests.
func (p *Proposal) addDependabot(c operation.Context, languages []string) error {
	root := c.Scope.Root
	var ecosystems, ids []string
	for _, language := range languages {
		ecosystems = append(ecosystems, languageEcosystems[language])
		id, _ := policyContent(language)
		ids = append(ids, id)
	}
	ecosystems = append(ecosystems, "github-actions")
	if err := p.warnRenovate(root); err != nil {
		return err
	}
	data := renderDependabot(ecosystems, ids)
	found, err := p.existingInputs(root, dependabotConfigs)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		if err := p.add(
			c,
			filepath.Join(root, dependabotConfigs[0]),
			data,
			"Create the Dependabot configuration: weekly grouped updates for "+display.List(
				ecosystems,
			),
		); err != nil {
			return err
		}
		return p.addAutoMerge(c, ids)
	}
	if err := p.checkDependabot(root, found, ecosystems, data); err != nil {
		return err
	}
	return p.addAutoMerge(c, ids)
}

// checkDependabot reports the repository's own Dependabot configuration,
// found, which is kept byte for byte, and the ecosystems it lacks.
func (p *Proposal) checkDependabot(root string, found, ecosystems []string, data []byte) error {
	var config struct {
		Updates []dependabotUpdate `yaml:"updates"`
	}
	if yaml.Unmarshal(p.Inventory.inputs[found[0]], &config) != nil {
		return operation.Fail(
			operation.ExitInvalid,
			"dependabot",
			found[0]+" is not valid Dependabot configuration",
			operation.FixText("correct it"),
		)
	}
	p.keep(root, found[0], dependabotConfigs[0], data, "Dependabot")
	if len(found) > 1 {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			operation.Warn("Dependabot reads "+found[0]+" and ignores "+found[1]),
		)
	}
	var missing []string
	for _, ecosystem := range ecosystems {
		if !slices.ContainsFunc(
			config.Updates,
			func(update dependabotUpdate) bool { return update.Ecosystem == ecosystem },
		) {
			missing = append(missing, ecosystem)
		}
	}
	if len(missing) > 0 {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			operation.Warn(
				found[0]+" has no updates entry for "+display.List(missing),
				operation.Outcome("Dependabot opens no update pull requests for them"),
				operation.FixText("add them by hand"),
			),
		)
	}
	return nil
}

// dependabotUpdate is the part of a Dependabot updates entry the plan reads.
type dependabotUpdate struct {
	Ecosystem string `yaml:"package-ecosystem"`
}

// renderDependabot writes the Dependabot configuration for ecosystems, all
// at the repository root, for the policies ids. Each ecosystem's minor and
// patch updates form one group, a single weekly pull request; a major update
// matches no group and gets a pull request of its own.
func renderDependabot(ecosystems, ids []string) []byte {
	var text strings.Builder
	text.WriteString(comment("Written by workbench project apply (" + strings.Join(ids, ", ") +
		"). The project owns this file; Workbench never edits an existing one."))
	text.WriteString("version: 2\nupdates:\n")
	for _, ecosystem := range ecosystems {
		text.WriteString("  - package-ecosystem: " + ecosystem + "\n")
		text.WriteString("    directory: /\n")
		text.WriteString("    schedule:\n")
		text.WriteString("      interval: weekly\n")
		text.WriteString("    groups:\n")
		text.WriteString("      " + dependabotGroup + ":\n")
		text.WriteString("        patterns:\n")
		text.WriteString("          - \"*\"\n")
		text.WriteString("        update-types:\n")
		text.WriteString("          - minor\n")
		text.WriteString("          - patch\n")
	}
	return []byte(text.String())
}

// dependabotGroup names the group of minor and patch updates in branch
// names and pull request titles; a group name starts and ends with a letter.
const dependabotGroup = "minor-and-patch"

// warnRenovate warns when the repository at root configures Renovate, which
// would open update pull requests beside Dependabot's.
func (p *Proposal) warnRenovate(root string) error {
	found, err := p.existingInputs(root, renovateConfigs)
	if err != nil {
		return err
	}
	manifest := filepath.Join(root, "package.json")
	data, err := p.readInput(manifest)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(data, &doc) == nil {
		if _, ok := doc["renovate"]; ok {
			found = append(found, manifest+" (renovate key)")
		}
	}
	if len(found) > 0 {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			operation.Warn(
				"Renovate is configured",
				operation.Line{Label: "found", Text: strings.Join(found, ", ")},
				operation.Outcome("with Dependabot, two update bots would open pull requests "+
					"for the same dependencies"),
			),
		)
	}
	return nil
}
