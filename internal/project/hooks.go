package project

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
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
	noTests := false
	for _, language := range languages {
		jobs := languageHooks[language]
		if language == "python" && !p.listsPytest(selected, root) {
			jobs.PrePush.Jobs = nil
			noTests = true
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
	if noTests {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			"No pre-push test job for Python: the dev group in "+
				filepath.Join(
					root,
					"pyproject.toml",
				)+" lists no pytest, and Workbench invents no tests",
		)
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
	if len(found) == 0 {
		if err := p.add(
			c,
			filepath.Join(root, dependabotConfigs[0]),
			data,
			"Create the Dependabot configuration: weekly update pull requests for "+
				listed(
					ecosystems,
				)+" at the repository root, with each ecosystem's minor and patch updates grouped into one",
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
			"Invalid Dependabot configuration; correct it before planning: "+found[0],
		)
	}
	p.keep(root, found[0], dependabotConfigs[0], data, "Dependabot")
	if len(found) > 1 {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			"Dependabot reads "+found[0]+" and ignores "+found[1],
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

// autoMergeName is the workflow --dependabot writes to turn on auto-merge for
// Dependabot's patch and minor pull requests.
var autoMergeName = filepath.Join(".github", "workflows", "dependabot-auto-merge.yml")

// The marks of a workflow that already turns on auto-merge for Dependabot's
// pull requests: a condition that selects Dependabot (not one that leaves it
// out) or the fetch-metadata action, with an auto-merge call or a general
// auto-merge action; or an action made to merge Dependabot's pull requests.
var (
	selectsDependabot = regexp.MustCompile(
		`(?i)==\s*['"]dependabot\[bot\]['"]|dependabot/fetch-metadata`,
	)
	turnsOnAutoMerge = regexp.MustCompile(
		`(?i)gh\s+pr\s+merge\b[^\n]*--auto|enablePullRequestAutoMerge|` +
			`uses:\s*['"]?(pascalgn/automerge-action|peter-evans/enable-pull-request-automerge)@`,
	)
	mergesDependabot = regexp.MustCompile(
		`(?i)uses:\s*['"]?(fastify/github-action-merge-dependabot|ahmadnassri/action-dependabot-auto-merge)@`,
	)
	continuedLine = regexp.MustCompile(`\\\r?\n\s*`)
)

// autoMergeElsewhere reports whether the workflow text already turns on
// auto-merge for Dependabot's pull requests. A command continued over
// several lines is read as one.
func autoMergeElsewhere(workflow []byte) bool {
	text := continuedLine.ReplaceAll(workflow, []byte(" "))
	return mergesDependabot.Match(text) ||
		(selectsDependabot.Match(text) && turnsOnAutoMerge.Match(text))
}

// addAutoMerge plans the workflow that turns on GitHub auto-merge for
// Dependabot's patch and minor pull requests, unless the repository has it
// (named .yml or .yaml) or another workflow that does the same, which is kept
// and reported.
func (p *Proposal) addAutoMerge(c operation.Context, ids []string) error {
	root := c.Scope.Root
	path := filepath.Join(root, autoMergeName)
	requirements, err := machine.ManagementRequirements(c)
	if err != nil {
		return err
	}
	fetchMetadata, err := requirements.Uses("dependabot/fetch-metadata")
	if err != nil {
		return err
	}
	data := renderAutoMerge(fetchMetadata, ids)
	workflows, _ := workflowFiles(root)
	for _, workflow := range workflows {
		existing, err := p.readInput(workflow)
		if err != nil {
			return err
		}
		switch {
		case workflow == path || workflow == strings.TrimSuffix(path, ".yml")+".yaml":
			p.keep(root, workflow, autoMergeName, data, "Dependabot auto-merge")
			return nil
		case autoMergeElsewhere(existing):
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				"Kept the project's own Dependabot auto-merge configuration: "+workflow,
			)
			return nil
		}
	}
	if _, err = p.readInput(path); err != nil {
		return err
	}
	if err = p.add(
		c,
		path,
		data,
		"Create the workflow that turns on GitHub auto-merge for Dependabot's patch and minor pull requests, and only for those",
	); err != nil {
		return err
	}
	if p.change(path) == nil {
		return nil
	}
	p.Plan.Warnings = append(
		p.Plan.Warnings,
		"Dependabot auto-merge works on GitHub only. The workflow waits for the pull request's other checks "+
			"and merges only when at least one ran and none failed; a repository with no CI check keeps its "+
			"Dependabot pull requests open. Checks that start late can be missed, so also turn on \"Allow "+
			"auto-merge\" in the repository settings and add a branch rule on the default branch that "+
			"requires the CI check, which GitHub then waits for itself; Workbench sets neither",
	)
	p.Plan.Effects = append(p.Plan.Effects, operation.Effect{
		Name: "dependabot-auto-merge",
		Description: "GitHub merges Dependabot's patch and minor pull requests by squash, without review, once " +
			"the pull request's other checks have passed; a failed check, no check at all or a major update " +
			"leaves it open",
		Privilege: "CI runner",
		Recovery:  "Restoring the workflow does not undo merges it made",
	})
	return nil
}

// renderAutoMerge writes the auto-merge workflow for the policies ids, with
// fetchMetadata, the pinned dependabot/fetch-metadata reference. Its one job
// runs only for a pull request Dependabot opened, and turns on auto-merge
// only when the update's highest change, which fetch-metadata reports for a
// group too, is a semver patch or minor.
func renderAutoMerge(fetchMetadata string, ids []string) []byte {
	return []byte(comment("Written by workbench project apply ("+strings.Join(ids, ", ")+
		"). It turns on GitHub auto-merge for Dependabot's patch and minor pull requests and "+
		"leaves every other pull request alone. It first waits for the pull request's other "+
		"checks: a failed check, or no check at all, leaves the pull request open. With \"Allow "+
		"auto-merge\" in the repository settings and a branch rule that requires the CI check, "+
		"GitHub waits for that check itself as well. The project "+
		"owns this file; Workbench never edits an existing one.") + `name: Dependabot auto-merge
on: pull_request
permissions:
  checks: read
  contents: write
  pull-requests: write
  statuses: read
jobs:
  dependabot:
    if: github.event.pull_request.user.login == 'dependabot[bot]'
    runs-on: ubuntu-24.04
    timeout-minutes: 45
    env:
      PR_URL: ${{ github.event.pull_request.html_url }}
      GH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
    steps:
      - name: Read the Dependabot update
        id: metadata
        uses: ` + fetchMetadata + `
        with:
          github-token: ${{ secrets.GITHUB_TOKEN }}
      - name: Wait for the other checks of a patch or minor update
        id: checks
        if: >-
          steps.metadata.outputs.update-type == 'version-update:semver-patch' ||
          steps.metadata.outputs.update-type == 'version-update:semver-minor'
        run: |
          # The state of every check but this workflow's: none, fail, pending or pass.
          others='[.[] | select(.workflow != "Dependabot auto-merge") | .bucket]
            | if length == 0 then "none"
              elif any(. == "fail" or . == "cancel") then "fail"
              elif any(. == "pending") then "pending"
              elif any(. == "pass") then "pass"
              else "none" end'
          passes=0
          missing=0
          unread=0
          errors="$RUNNER_TEMP/checks.err"
          for _ in $(seq 1 120); do
            # This job is a check itself, so a list that cannot be read is an error, not an empty one.
            state=$(gh pr checks "$PR_URL" --json bucket,workflow --jq "$others" 2>"$errors") || state=unread
            case "$state" in
              pass) passes=$((passes + 1)); missing=0; unread=0 ;;
              fail) echo "A check failed, so this pull request stays open."; exit 1 ;;
              none) passes=0; missing=$((missing + 1)); unread=0 ;;
              pending) passes=0; missing=0; unread=0 ;;
              *) passes=0; unread=$((unread + 1)) ;;
            esac
            # Two passing polls in a row, so a job that starts after another ends is seen.
            [ "$passes" -lt 2 ] || exit 0
            # Three minutes without any other check: the repository runs none for it.
            [ "$missing" -lt 9 ] || { echo "No other check ran for this pull request, so it stays open."; exit 1; }
            # One minute of failed lookups: the checks cannot be read.
            if [ "$unread" -ge 3 ]; then
              echo "The checks of this pull request could not be read, so it stays open:"
              cat "$errors"
              exit 1
            fi
            sleep 20
          done
          echo "The other checks did not finish in 40 minutes, so this pull request stays open."
          exit 1
      - name: Merge once GitHub's required checks pass too
        if: steps.checks.outcome == 'success'
        run: gh pr merge --auto --squash "$PR_URL"
`)
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
