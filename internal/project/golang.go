package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/project/gopolicy"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/pelletier/go-toml/v2"
)

// golangciNames are the files golangci-lint reads its configuration from. It
// looks in the folder it runs in and then in each parent folder, so a file at
// the module or anywhere above it in the repository is the project's own.
var golangciNames = []string{".golangci.yml", ".golangci.yaml", ".golangci.toml", ".golangci.json"}

// recordName is the Go module's record of the Workbench policies it follows.
// go.mod has no place for tool data, so the record is a file of its own.
const recordName = ".workbench.toml"

// addGoPolicy plans the Go policy's files at module: the golangci-lint
// configuration, created only when the project has none, and the record.
func (p *Proposal) addGoPolicy(c operation.Context, module Project) error {
	existing, err := p.golangciConfig(module.Root)
	if err != nil {
		return err
	}
	switch {
	case existing == "":
		if err = p.add(
			c,
			filepath.Join(module.Root, ".golangci.yml"),
			gopolicy.Golangci,
			"Create the golangci-lint configuration: the standard linters plus errorlint, modernize, revive and unparam, formatted with gofumpt",
		); err != nil {
			return err
		}
	case existing != filepath.Join(module.Root, ".golangci.yml") ||
		!bytes.Equal(p.Inventory.inputs[existing], gopolicy.Golangci):
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			"Kept the project's own golangci-lint configuration: "+existing,
		)
		v2, err := golangciV2(existing, p.Inventory.inputs[existing])
		if err != nil {
			return err
		}
		if !v2 {
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				"golangci-lint v2 refuses this v1 config; run `golangci-lint migrate`: "+existing,
			)
		}
	}
	return p.addRecord(c, module.Root)
}

// golangciConfig returns the path of the golangci-lint configuration that
// applies at root, searching root and its parents up to the repository root
// as golangci-lint does, or "" when there is none. Every candidate is bound as
// a plan input. golangci-lint also falls back to the home folder; that file
// is the person's own and never reaches CI, so it does not count.
func (p *Proposal) golangciConfig(root string) (string, error) {
	var found string
	err := ancestors(root, func(parent string) (bool, error) {
		for _, name := range golangciNames {
			path := filepath.Join(parent, name)
			data, err := p.readInput(path)
			if err != nil {
				return false, err
			}
			if data != nil && found == "" {
				found = path
			}
		}
		return found != "", nil
	})
	return found, err
}

// golangciV2 reports whether the golangci-lint configuration at path declares
// version 2, which golangci-lint v2 requires. A file that does not parse stops
// the plan rather than being reported as kept.
func golangciV2(path string, data []byte) (bool, error) {
	var doc map[string]any
	var err error
	switch filepath.Ext(path) {
	case ".toml":
		err = toml.Unmarshal(data, &doc)
	case ".json":
		err = json.Unmarshal(data, &doc)
	default:
		err = yaml.Unmarshal(data, &doc)
	}
	if err != nil {
		return false, operation.Fail(
			operation.ExitInvalid,
			"golangci",
			"Invalid golangci-lint configuration; correct it before planning: "+path,
		)
	}
	// golangci-lint run accepts the number 2 as well as the string "2".
	return fmt.Sprint(doc["version"]) == "2", nil
}

// addRecord plans root's .workbench.toml so that it lists the Go policy. The
// file is Workbench's and written whole; one that already lists the policy is
// left byte for byte, and one Workbench cannot rewrite without losing data
// stops the plan.
func (p *Proposal) addRecord(c operation.Context, root string) error {
	path := filepath.Join(root, recordName)
	data, err := p.readInput(path)
	if err != nil {
		return err
	}
	var policies []string
	if data != nil {
		var record map[string]any
		if toml.Unmarshal(data, &record) != nil {
			return operation.Fail(
				operation.ExitInvalid,
				"provenance",
				"Invalid TOML in the Workbench record; correct it before planning: "+path,
			)
		}
		version, isInteger := record["schema_version"].(int64)
		list, isList := record["policies"].([]any)
		if !isInteger || version != 1 || !isList || len(record) != 2 {
			return operation.Fail(
				operation.ExitBlocked,
				"provenance",
				"Unsupported Workbench record; it needs schema_version = 1 and a policies list, nothing else: "+path,
			)
		}
		for _, item := range list {
			id, ok := item.(string)
			if !ok {
				return operation.Fail(
					operation.ExitBlocked,
					"provenance",
					"Unsupported Workbench record; policies must be strings: "+path,
				)
			}
			policies = append(policies, id)
		}
	}
	if slices.Contains(policies, gopolicy.ID) {
		return nil
	}
	policies = append(policies, gopolicy.ID)
	quoted := make([]string, len(policies))
	for i, id := range policies {
		quoted[i] = fmt.Sprintf("%q", id)
	}
	content := gopolicy.RecordHeader + "schema_version = 1\npolicies = [" +
		strings.Join(quoted, ", ") + "]\n"
	return p.add(
		c,
		path,
		[]byte(content),
		"Record that the module follows the Workbench policy "+gopolicy.ID+"; builds and tests never read it",
	)
}

// readInput reads a bounded metadata file the plan depends on and binds it,
// or its absence, as a plan input, so the approved plan goes stale when the
// file changes, appears or goes away.
func (p *Proposal) readInput(path string) ([]byte, error) {
	data, err := p.Inventory.read(path)
	if err != nil {
		return nil, err
	}
	digest := "absent"
	if data != nil {
		digest = operation.SHA256Hex(data)
	}
	p.Plan.Inputs = append(p.Plan.Inputs, operation.Input{Name: path, Digest: digest})
	return data, nil
}

// goDirective matches go.mod's go line, which actions/setup-go reads through
// go-version-file to choose the Go version.
var goDirective = regexp.MustCompile(`(?m)^go[ \t]+[0-9]`)

// goCI returns the Go check job and the workflow it joins: the one workflow at
// the repository root with a run step that runs go test.
func (p *Proposal) goCI(c operation.Context, module Project) (string, ownedJob, error) {
	if module.Root != c.Scope.Root {
		return "", ownedJob{}, goCIFailure(
			"Automatic CI requires the Go module at the selected root",
		)
	}
	if !isRepositoryRoot(c.Scope.Root) {
		return "", ownedJob{}, goCIFailure(
			"GitHub reads workflows at the repository root; select the folder that holds .git",
		)
	}
	if !goDirective.Match(p.Inventory.inputs[filepath.Join(module.Root, "go.mod")]) {
		return "", ownedJob{}, goCIFailure(
			"go.mod needs a go line, which the job's actions/setup-go reads to choose the Go version",
		)
	}
	path, err := p.testWorkflow(c.Scope.Root, "go test", goTest, goCIFailure)
	if err != nil {
		return "", ownedJob{}, err
	}
	pins, err := readGoPins(c)
	if err != nil {
		return "", ownedJob{}, err
	}
	addition, desired, err := goJob(pins)
	if err != nil {
		return "", ownedJob{}, err
	}
	return path, ownedJob{
		name:          "workbench-go",
		addition:      addition,
		desired:       desired,
		refsMayDiffer: true,
		fail:          goCIFailure,
		effect:        "The workflow that runs go test will gain Go check steps. A future CI run downloads the module's dependencies and the pinned golangci-lint, govulncheck and gitleaks, and builds and checks project code",
	}, nil
}

func goCIFailure(message string) error {
	return operation.Fail(
		operation.ExitBlocked,
		"project_ci",
		message+"; add the workbench-go job described in project/gopolicy/README.md manually",
	)
}

// isRepositoryRoot reports whether root holds .git, a folder or, in a linked
// worktree, a file.
func isRepositoryRoot(root string) bool {
	_, err := os.Lstat(filepath.Join(root, ".git"))
	return err == nil
}

// testWorkflow returns the one workflow under root with a run step that
// matches runs, which recognizes the language's test command (label). Other
// workflows, such as release workflows, are left alone, and one that is not
// valid YAML is skipped with a warning; only the chosen workflow must meet
// the merge's exact-roundtrip rules. fail words a refusal for the language.
func (p *Proposal) testWorkflow(
	root, label string,
	runs *regexp.Regexp,
	fail func(string) error,
) (string, error) {
	workflows, readable := workflowFiles(root)
	if !readable {
		return "", fail("No readable existing GitHub workflow owner")
	}
	var matches []string
	for _, path := range workflows {
		data, err := p.readInput(path)
		if err != nil {
			return "", err
		}
		var doc any
		if yaml.Unmarshal(data, &doc) != nil {
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				"Skipped a workflow that is not valid YAML while looking for "+label+": "+path,
			)
			continue
		}
		if runsCommand(doc, runs) {
			matches = append(matches, path)
		}
	}
	switch len(matches) {
	case 0:
		return "", fail(
			"No workflow has a run step that runs " + label + "; tests will not be invented",
		)
	case 1:
		return matches[0], nil
	default:
		return "", fail("Several workflows run " + label + ": " + strings.Join(matches, ", "))
	}
}

// goTest matches a go test command, and not one that merely ends in "go",
// such as cargo test.
var goTest = regexp.MustCompile(`\bgo\s+test\b`)

// runsCommand reports whether a parsed workflow has a job step whose run
// command matches command.
func runsCommand(doc any, command *regexp.Regexp) bool {
	workflow, _ := doc.(map[string]any)
	jobs, _ := workflow["jobs"].(map[string]any)
	for _, job := range jobs {
		steps, _ := table(job)["steps"].([]any)
		for _, step := range steps {
			if run, ok := table(step)["run"].(string); ok && command.MatchString(run) {
				return true
			}
		}
	}
	return false
}

// goPins are the tool versions the Go check job installs, from the selected
// machine source's versions.toml: the same pins the Linux machine installs.
type goPins struct {
	requirements   machine.Requirements
	golangciLint   string
	govulncheck    string
	gitleaks       string
	gitleaksSHA256 string
}

var (
	plainVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	goVersion    = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)
	sha256Hex    = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// sourceVersions are the versions.toml pins the generated CI jobs use.
type sourceVersions struct {
	Binaries   map[string]string `toml:"linux_binaries"`
	GoTools    map[string]string `toml:"linux_go_tools"`
	CargoTools map[string]string `toml:"cargo_tools"`
	CI         struct {
		Gitleaks  map[string]string `toml:"gitleaks_linux_x64_sha256"`
		CargoDeny map[string]string `toml:"cargo_deny_linux_x64_sha256"`
	} `toml:"project_ci"`
}

// readSourceVersions reads the CI jobs' pins from the machine source this
// command uses: --local-build, else the active release.
func readSourceVersions(c operation.Context) (machine.Requirements, sourceVersions, error) {
	var versions sourceVersions
	source := c.Native.Source
	if source == "" {
		state, err := operation.ReadState(c.Paths)
		if err != nil {
			return machine.Requirements{}, versions, err
		}
		if state != nil && state.ActiveRelease != nil {
			source = state.ActiveRelease.Source
		}
	}
	files, _, err := machine.SourceSnapshot(source, c.Native.Developer)
	if err != nil {
		return machine.Requirements{}, versions, err
	}
	requirements, err := machine.SourceRequirements(files)
	if err != nil {
		return machine.Requirements{}, versions, err
	}
	err = toml.Unmarshal(files["home/.chezmoidata/versions.toml"], &versions)
	return requirements, versions, err
}

// pinCheck is one pin a generated job needs, with the shape it must have.
type pinCheck struct {
	name, value string
	shape       *regexp.Regexp
}

// checkPins refuses a pin that does not have its shape. Every value lands in
// a workflow's shell commands or download URL, so each must be a plain
// version or digest.
func checkPins(pins []pinCheck) error {
	for _, pin := range pins {
		if !pin.shape.MatchString(pin.value) {
			return operation.Fail(
				operation.ExitInvalid,
				"source",
				fmt.Sprintf(
					"The machine source's versions.toml has no valid %s pin (%q)",
					pin.name,
					pin.value,
				),
			)
		}
	}
	return nil
}

// readGoPins reads the Go job's pins from the machine source this command
// uses.
func readGoPins(c operation.Context) (goPins, error) {
	requirements, versions, err := readSourceVersions(c)
	if err != nil {
		return goPins{}, err
	}
	pins := goPins{
		requirements: requirements,
		golangciLint: versions.Binaries["golangci-lint"],
		govulncheck:  versions.GoTools["golang.org/x/vuln/cmd/govulncheck"],
		gitleaks:     versions.Binaries["gitleaks"],
	}
	pins.gitleaksSHA256 = versions.CI.Gitleaks[pins.gitleaks]
	return pins, checkPins([]pinCheck{
		{"linux_binaries.golangci-lint", pins.golangciLint, plainVersion},
		{"linux_go_tools govulncheck", pins.govulncheck, goVersion},
		{"linux_binaries.gitleaks", pins.gitleaks, plainVersion},
		{
			"project_ci.gitleaks_linux_x64_sha256 for gitleaks " + pins.gitleaks,
			pins.gitleaksSHA256,
			sha256Hex,
		},
	})
}

// goJob renders the owned Go job as a one-entry jobs mapping. The gitleaks
// steps are those of Workbench's own secret-scan job: download the pinned
// release, check its SHA-256, then scan the history checked out in full. Each
// run is one plain line: go-yaml's merge does not re-indent the text of a
// block scalar, and mergeJob's reparse check would refuse the result.
func goJob(pins goPins) (*ast.File, *ast.MappingNode, error) {
	var uses []string
	for _, name := range []string{"actions/checkout", "actions/setup-go", "golangci/golangci-lint-action"} {
		ref, err := pins.requirements.Uses(name)
		if err != nil {
			return nil, nil, err
		}
		uses = append(uses, ref)
	}
	text := fmt.Sprintf(`workbench-go:
  name: Workbench Go checks (%[1]s)
  runs-on: ubuntu-24.04
  timeout-minutes: 15
  defaults:
    run:
      shell: bash
      working-directory: .
  permissions:
    contents: read
  concurrency:
    group: workbench-go-${{ github.workflow }}-${{ github.ref }}
    cancel-in-progress: true
  steps:
    - uses: %[2]s
      with:
        persist-credentials: false
        fetch-depth: 0
    - uses: %[3]s
      with:
        go-version-file: go.mod
    - uses: %[4]s
      with:
        version: v%[5]s
        install-only: true
    - run: golangci-lint run
    - run: go build ./...
    - run: go run golang.org/x/vuln/cmd/govulncheck@%[6]s ./...
    - name: Download Gitleaks
      env:
        GITLEAKS_URL: https://github.com/gitleaks/gitleaks/releases/download/v%[7]s/gitleaks_%[7]s_linux_x64.tar.gz
      run: curl -fsSL --retry 3 -o "$RUNNER_TEMP/gitleaks.tar.gz" "$GITLEAKS_URL"
    - name: Check the Gitleaks download
      run: printf '%%s  %%s\n' '%[8]s' "$RUNNER_TEMP/gitleaks.tar.gz" | sha256sum --check --strict
    - run: tar -xzf "$RUNNER_TEMP/gitleaks.tar.gz" -C "$RUNNER_TEMP" gitleaks
    - name: Scan Git history with Gitleaks
      run: exec "$RUNNER_TEMP/gitleaks" git --redact --no-banner --exit-code 1
`, gopolicy.ID, uses[0], uses[1], uses[2], pins.golangciLint, pins.govulncheck, pins.gitleaks, pins.gitleaksSHA256)
	file, err := parser.ParseBytes([]byte(text), parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}
	job, ok := file.Docs[0].Body.(*ast.MappingNode)
	if !ok {
		return nil, nil, errors.New("go check job template is not a mapping")
	}
	return file, job, nil
}
