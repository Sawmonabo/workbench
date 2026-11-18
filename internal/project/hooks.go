package project

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/project/gopolicy"
	"github.com/Sawmonabo/workbench/project/pythonpolicy"
	"github.com/Sawmonabo/workbench/project/rustpolicy"
	"github.com/goccy/go-yaml"
)

// hookFile is the part of a lefthook configuration the policies write: the
// jobs of the pre-commit and pre-push hooks.
type hookFile struct {
	PreCommit hookGroup `yaml:"pre-commit"`
	PrePush   hookGroup `yaml:"pre-push"`
}

type hookGroup struct {
	Parallel bool      `yaml:"parallel,omitempty"`
	Jobs     []hookJob `yaml:"jobs"`
}

type hookJob struct {
	Name string `yaml:"name"`
	Glob string `yaml:"glob,omitempty"`
	Run  string `yaml:"run"`
}

// languageHooks are each policy's embedded lefthook jobs. An invalid embedded
// asset is a build defect, so it fails at startup like a bad regexp.
var languageHooks = map[string]hookFile{
	"go":     embeddedHooks(gopolicy.Hooks),
	"python": embeddedHooks(pythonpolicy.Hooks),
	"rust":   embeddedHooks(rustpolicy.Hooks),
}

func embeddedHooks(data []byte) hookFile {
	var hooks hookFile
	if err := yaml.UnmarshalWithOptions(data, &hooks, yaml.Strict()); err != nil {
		panic("invalid embedded lefthook.yml: " + err.Error())
	}
	return hooks
}

// languageEcosystems are the Dependabot ecosystems of each policy.
var languageEcosystems = map[string]string{
	"go":     gopolicy.Ecosystem,
	"python": pythonpolicy.Ecosystem,
	"rust":   rustpolicy.Ecosystem,
}

// lefthookConfigs are the main configuration files lefthook 2.2.2 reads at
// the repository root, in the order it tries them: each extension in turn,
// with the names lefthook, .lefthook and .config/lefthook. lefthook uses the
// first it finds, so any of them is the project's own configuration.
// lefthookLocals are the -local files it merges over the main one.
var lefthookConfigs, lefthookLocals = func() (main, local []string) {
	for _, extension := range []string{".yml", ".yaml", ".json", ".jsonc", ".toml"} {
		for _, name := range []string{"lefthook", ".lefthook", filepath.Join(".config", "lefthook")} {
			main = append(main, name+extension)
			local = append(local, name+"-local"+extension)
		}
	}
	return main, local
}()

// dependabotConfigs are the two names GitHub reads Dependabot's configuration
// from.
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

// addRepositoryFiles plans the files Git and GitHub read at the repository
// root: lefthook.yml for --hooks and .github/dependabot.yml for --dependabot,
// each written whole for every selected language and only when the
// repository has none of its own.
func (p *Proposal) addRepositoryFiles(
	c operation.Context,
	selected []Project,
	options ApplyOptions,
) error {
	languages, err := p.repositoryLanguages(c, selected)
	if err != nil {
		return err
	}
	if options.Hooks {
		if err = p.addHooks(c, selected, languages); err != nil {
			return err
		}
	}
	if options.Dependabot {
		return p.addDependabot(c, languages)
	}
	return nil
}

// repositoryLanguages returns the selected languages, in policy order, once
// every selected project they configure sits at the selected root and that
// root is the repository root: the jobs run there and Dependabot reads the
// manifests at directory /.
func (p *Proposal) repositoryLanguages(c operation.Context, selected []Project) ([]string, error) {
	if !isRepositoryRoot(c.Scope.Root) {
		return nil, repositoryFailure(
			"Git and GitHub read lefthook.yml and .github/dependabot.yml at the repository root; select the folder that holds .git",
		)
	}
	var languages []string
	for _, language := range configurable {
		projects := withLanguage(selected, language)
		if language == "rust" {
			projects = rustRoots(selected)
		}
		if len(projects) == 0 {
			continue
		}
		for _, project := range projects {
			if project.Owner != c.Scope.Root {
				return nil, repositoryFailure(
					"--hooks and --dependabot write jobs and updates for projects at the repository root, and " +
						project.Owner + " is below it",
				)
			}
		}
		languages = append(languages, language)
	}
	return languages, nil
}

func repositoryFailure(message string) error {
	return operation.Fail(
		operation.ExitBlocked,
		"project_repository",
		message+"; to add lefthook.yml or .github/dependabot.yml by hand, follow the policy READMEs in project/",
	)
}

// addHooks plans lefthook.yml with the jobs of languages, unless the
// repository has a lefthook configuration of its own, which is kept byte for
// byte and reported. Python's pre-push test job is written only when the dev
// group lists pytest; tests are never invented.
func (p *Proposal) addHooks(c operation.Context, selected []Project, languages []string) error {
	root := c.Scope.Root
	var found []string
	for _, name := range lefthookConfigs {
		data, err := p.readInput(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if data != nil {
			found = append(found, filepath.Join(root, name))
		}
	}
	var hooks hookFile
	var ids []string
	for _, language := range languages {
		jobs := languageHooks[language]
		if language == "python" && !p.listsPytest(selected, root) {
			jobs.PrePush.Jobs = nil
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				"No pre-push test job for Python: the dev group in "+
					filepath.Join(
						root,
						"pyproject.toml",
					)+" lists no pytest, and Workbench invents no tests",
			)
		}
		hooks.PreCommit.Parallel = hooks.PreCommit.Parallel || jobs.PreCommit.Parallel
		hooks.PreCommit.Jobs = appendJobs(hooks.PreCommit.Jobs, jobs.PreCommit.Jobs)
		hooks.PrePush.Jobs = appendJobs(hooks.PrePush.Jobs, jobs.PrePush.Jobs)
		id, _ := policyContent(language)
		ids = append(ids, id)
	}
	// The secret scan covers the whole commit, so it runs once, after the
	// language jobs.
	secrets := slices.IndexFunc(
		hooks.PreCommit.Jobs,
		func(job hookJob) bool { return job.Name == "secrets" },
	)
	if secrets >= 0 {
		job := hooks.PreCommit.Jobs[secrets]
		hooks.PreCommit.Jobs = append(slices.Delete(hooks.PreCommit.Jobs, secrets, secrets+1), job)
	}
	data, err := renderHooks(hooks, ids)
	if err != nil {
		return err
	}
	path := filepath.Join(root, "lefthook.yml")
	if len(found) > 0 {
		p.keep(root, found[0], "lefthook.yml", data, "lefthook")
		for _, other := range found[1:] {
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				"lefthook reads only one main configuration; this one is also present: "+other,
			)
		}
		return nil
	}
	for _, name := range lefthookLocals {
		local, err := p.readInput(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if local != nil {
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				"lefthook merges "+filepath.Join(
					root,
					name,
				)+" over the lefthook.yml this plan writes",
			)
		}
	}
	if err = p.add(
		c,
		path,
		data,
		"Create the lefthook configuration: format, lint, dependency-file and secret checks before each commit and tests before each push; run lefthook install once per clone to turn them on",
	); err != nil {
		return err
	}
	if p.change(path) == nil {
		return nil
	}
	p.hooks = path
	p.Plan.Effects = append(p.Plan.Effects, operation.Effect{
		Name:        "git-hook-checks",
		Description: "After lefthook install in a clone, each commit and push runs lefthook.yml, which builds and runs project code and tests; apply never runs lefthook install",
		Privilege:   "user",
		Recovery:    "Restoring lefthook.yml does not remove the Git hooks lefthook install wrote; run lefthook uninstall for that",
	})
	return nil
}

// appendJobs appends the jobs whose names are not already in jobs.
func appendJobs(jobs, more []hookJob) []hookJob {
	for _, job := range more {
		if !slices.ContainsFunc(jobs, func(have hookJob) bool { return have.Name == job.Name }) {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// renderHooks writes hooks as lefthook.yml for the policies ids. Every run
// is one plain line, as in the CI jobs, and the result must parse back to
// hooks exactly.
func renderHooks(hooks hookFile, ids []string) ([]byte, error) {
	var text strings.Builder
	text.WriteString(comment("Written by workbench project apply (" + strings.Join(ids, ", ") +
		"). Run `lefthook install` once per clone to turn on these Git hooks. The project owns " +
		"this file; Workbench never edits an existing one."))
	for _, group := range []struct {
		name  string
		hooks hookGroup
	}{{"pre-commit", hooks.PreCommit}, {"pre-push", hooks.PrePush}} {
		if len(group.hooks.Jobs) == 0 {
			continue
		}
		text.WriteString(group.name + ":\n")
		if group.hooks.Parallel {
			text.WriteString("  parallel: true\n")
		}
		text.WriteString("  jobs:\n")
		for _, job := range group.hooks.Jobs {
			text.WriteString("    - name: " + job.Name + "\n")
			if job.Glob != "" {
				fmt.Fprintf(&text, "      glob: %q\n", job.Glob)
			}
			text.WriteString("      run: " + job.Run + "\n")
		}
	}
	var parsed hookFile
	if err := yaml.UnmarshalWithOptions(
		[]byte(text.String()),
		&parsed,
		yaml.Strict(),
	); err != nil ||
		!reflect.DeepEqual(parsed, hooks) {
		return nil, operation.Fail(
			operation.ExitFailed,
			"project_hooks",
			"The combined lefthook configuration did not parse back to its jobs",
		)
	}
	return []byte(text.String()), nil
}

// listsPytest reports whether the dev group of the Python project at root,
// with the groups it includes, lists pytest.
func (p *Proposal) listsPytest(selected []Project, root string) bool {
	i := slices.IndexFunc(selected, func(project Project) bool {
		return project.Language == "python" && project.Root == root
	})
	return i >= 0 && devRequirements(selected[i].metadata)["pytest"]
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
	var found []string
	for _, name := range dependabotConfigs {
		existing, err := p.readInput(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if existing != nil {
			found = append(found, filepath.Join(root, name))
		}
	}
	switch len(found) {
	case 0:
		return p.add(
			c,
			filepath.Join(root, dependabotConfigs[0]),
			data,
			"Create the Dependabot configuration: weekly update pull requests for "+
				listed(ecosystems)+" at the repository root",
		)
	case 1:
	default:
		return operation.Fail(
			operation.ExitBlocked,
			"dependabot",
			"GitHub reads one Dependabot configuration, and both "+strings.Join(found, " and ")+
				" exist; keep one before planning",
		)
	}
	p.keep(root, found[0], dependabotConfigs[0], data, "Dependabot")
	var config struct {
		Updates []dependabotUpdate `yaml:"updates"`
	}
	if yaml.Unmarshal(p.Inventory.inputs[found[0]], &config) != nil {
		return operation.Fail(
			operation.ExitInvalid,
			"dependabot",
			"Invalid Dependabot configuration; correct it before planning: "+found[0],
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
			found[0]+" has no updates entry for "+listed(missing)+
				"; add them by hand for update pull requests",
		)
	}
	return nil
}

// dependabotUpdate is the part of a Dependabot updates entry the plan reads.
type dependabotUpdate struct {
	Ecosystem string `yaml:"package-ecosystem"`
}

// renderDependabot writes the Dependabot configuration for ecosystems, all
// at the repository root, for the policies ids.
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
	}
	return []byte(text.String())
}

// warnRenovate warns when the repository at root configures Renovate, which
// would open update pull requests beside Dependabot's.
func (p *Proposal) warnRenovate(root string) error {
	var found []string
	for _, name := range renovateConfigs {
		data, err := p.readInput(filepath.Join(root, name))
		if err != nil {
			return err
		}
		if data != nil {
			found = append(found, filepath.Join(root, name))
		}
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
			"Renovate is configured ("+strings.Join(found, ", ")+
				"); with Dependabot, two update bots would open pull requests for the same dependencies",
		)
	}
	return nil
}

// listed joins names as "a", "a and b" or "a, b and c".
func listed(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// comment returns text as YAML comment lines of at most 78 columns.
func comment(text string) string {
	var lines strings.Builder
	line := "#"
	for word := range strings.FieldsSeq(text) {
		if len(line)+1+len(word) > 78 && line != "#" {
			lines.WriteString(line + "\n")
			line = "#"
		}
		line += " " + word
	}
	return lines.String() + line + "\n"
}
