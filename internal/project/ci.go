package project

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	pythonpolicy "github.com/Sawmonabo/workbench/project/python"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

var pythonVersion = regexp.MustCompile(`^3\.[0-9]{1,2}(\.[0-9]{1,2})?$`)

// addCI proposes the Python check job. CI integration has one conservative
// owner: one existing GitHub workflow at the selected workspace root, with an
// existing test job and Python choice. All unowned AST nodes must serialize
// identically before and after the merge.
func (p *Proposal) addCI(c operation.Context, owners []string) error {
	if len(owners) != 1 || owners[0] != c.Scope.Root {
		return ciFailure(
			"Automatic CI requires one Python workspace owner equal to the selected root",
		)
	}
	python, err := p.pythonChoice(c.Scope.Root)
	if err != nil {
		return err
	}
	path, file, err := p.readWorkflow(c.Scope.Root)
	if err != nil {
		return err
	}
	jobsPath, _ := yaml.PathString("$.jobs")
	jobsNode, err := jobsPath.FilterFile(file)
	if err != nil {
		return ciFailure("Workflow jobs ownership is missing")
	}
	jobs, ok := jobsNode.(*ast.MappingNode)
	if !ok {
		return ciFailure("Workflow jobs must be a mapping")
	}
	if !slices.ContainsFunc(jobs.Values, runsTests) {
		return ciFailure(
			"An existing test/tests job with an identifiable test command is required; tests will not be invented",
		)
	}
	addition, desired, err := checkJob(python)
	if err != nil {
		return err
	}
	if existing := mappingValue(jobs, "workbench-python"); existing != nil {
		return checkOwnedJob(existing, desired)
	}
	proposed, err := mergeJob(file, jobsPath, jobs, addition)
	if err != nil {
		return err
	}
	p.Plan.Effects = append(
		p.Plan.Effects,
		operation.Effect{
			Name:        "ci-workflow-checks",
			Description: "The existing workflow will gain locked Python check steps. A future CI run may download dependencies and execute project build code; configure does not run CI",
			Privilege:   "CI runner",
			Recovery:    "Restoring the workflow file does not undo completed CI runs or their external effects",
		},
	)
	return p.add(
		c,
		path,
		proposed,
		"Add one owned Python check job to the existing workflow; preserve existing test jobs, triggers and comments",
	)
}

func ciFailure(message string) error {
	return operation.Fail(
		3,
		"project_ci",
		message+"; adapt project/python/checks.example.yml manually",
	)
}

// pythonChoice reads the explicit Python version the CI job will use.
func (p *Proposal) pythonChoice(root string) (string, error) {
	path := filepath.Join(root, ".python-version")
	data, err := readMetadata(path)
	if err != nil {
		return "", err
	}
	python := strings.TrimSpace(string(data))
	if !pythonVersion.MatchString(python) {
		return "", ciFailure(
			"An existing explicit .python-version is required; no Python baseline is chosen",
		)
	}
	p.Plan.Inputs = append(p.Plan.Inputs, operation.Input{Name: path, Digest: hash(data)})
	return python, nil
}

// readWorkflow parses the single workflow under root. It must hold one
// document that roundtrips exactly and uses no YAML feature the merge could
// silently change.
func (p *Proposal) readWorkflow(root string) (string, *ast.File, error) {
	directory := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", nil, ciFailure("No readable existing GitHub workflow owner")
	}
	var workflows []string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".yml") || strings.HasSuffix(entry.Name(), ".yaml") {
			workflows = append(workflows, filepath.Join(directory, entry.Name()))
		}
	}
	if len(workflows) != 1 {
		return "", nil, ciFailure(
			"Multiple or missing workflows require an explicit integration decision",
		)
	}
	path := workflows[0]
	data, err := readMetadata(path)
	if err != nil {
		return "", nil, err
	}
	p.Plan.Inputs = append(p.Plan.Inputs, operation.Input{Name: path, Digest: hash(data)})
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil || len(file.Docs) != 1 {
		return "", nil, ciFailure("Workflow needs one valid YAML document without duplicate keys")
	}
	guard := &yamlGuard{}
	ast.Walk(guard, file.Docs[0])
	if guard.unsupported {
		return "", nil, ciFailure(
			"Aliases, anchors, merge keys, tags or directives need manual workflow review",
		)
	}
	if !bytes.Equal(data, []byte(file.String())) {
		return "", nil, ciFailure("Workflow formatting cannot roundtrip without normalization")
	}
	return path, file, nil
}

// runsTests reports whether job is a test/tests job with a step that runs a
// recognizable test command.
func runsTests(job *ast.MappingValueNode) bool {
	if key := job.Key.GetToken().Value; key != "test" && key != "tests" {
		return false
	}
	node, ok := job.Value.(*ast.MappingNode)
	if !ok {
		return false
	}
	steps, ok := mappingValue(node, "steps").(*ast.SequenceNode)
	if !ok {
		return false
	}
	for _, step := range steps.Values {
		mapping, ok := step.(*ast.MappingNode)
		if !ok {
			continue
		}
		run := mappingValue(mapping, "run")
		if run == nil {
			continue
		}
		command := run.GetToken().Value
		for _, runner := range []string{"pytest", "unittest", "tox", "nox"} {
			if strings.Contains(command, runner) {
				return true
			}
		}
	}
	return false
}

// checkJob renders the owned job for python as a one-entry jobs mapping.
func checkJob(python string) (*ast.File, *ast.MappingNode, error) {
	text := fmt.Sprintf(`workbench-python:
  name: Workbench Python checks (%s)
  runs-on: ubuntu-24.04
  timeout-minutes: 15
  defaults:
    run:
      shell: bash
      working-directory: .
  permissions:
    contents: read
  concurrency:
    group: workbench-python-${{ github.workflow }}-${{ github.ref }}
    cancel-in-progress: true
  steps:
    - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      with:
        persist-credentials: false
    - uses: astral-sh/setup-uv@bec219d24cd3e171d82865faccec33120bb574f4 # v10.1.0
      with:
        version: %q
        python-version: %q
    - run: uv sync --locked --group dev
    - run: uv run --locked ruff check .
    - run: uv run --locked ruff format --check .
    - run: uv run --locked basedpyright
`, pythonpolicy.ID, machine.ManagementRequirements().UV, python)
	file, err := parser.ParseBytes([]byte(text), parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}
	job, ok := file.Docs[0].Body.(*ast.MappingNode)
	if !ok {
		return nil, nil, errors.New("check job template is not a mapping")
	}
	return file, job, nil
}

// checkOwnedJob accepts an existing owned job only when it matches the recipe.
// User changes are never guessed away; a differing recipe receives an explicit
// integration proposal.
func checkOwnedJob(existing ast.Node, desired *ast.MappingNode) error {
	want := desired.Values[0].Value.String()
	have := existing.String()
	if strings.TrimSpace(have) == strings.TrimSpace(want) {
		return nil
	}
	var actual, expected any
	if yaml.Unmarshal([]byte(have), &actual) != nil ||
		yaml.Unmarshal([]byte(want), &expected) != nil {
		return ciFailure("Existing Workbench check job needs review")
	}
	if !reflect.DeepEqual(actual, expected) {
		return ciFailure("Existing Workbench check job differs from the proposed recipe")
	}
	return nil
}

// mergeJob adds the job to the workflow and returns the proposed file after
// proving every unowned node serializes as before.
func mergeJob(
	file *ast.File,
	jobsPath *yaml.Path,
	jobs *ast.MappingNode,
	addition *ast.File,
) ([]byte, error) {
	root, ok := file.Docs[0].Body.(*ast.MappingNode)
	if !ok {
		return nil, ciFailure("Workflow root must be a mapping")
	}
	unowned := make(map[*ast.MappingValueNode]string)
	for _, node := range root.Values {
		if node.Key.GetToken().Value != "jobs" {
			unowned[node] = node.String()
		}
	}
	for _, node := range jobs.Values {
		unowned[node] = node.String()
	}
	if err := jobsPath.MergeFromFile(file, addition); err != nil {
		return nil, err
	}
	for node, before := range unowned {
		if node.String() != before {
			return nil, ciFailure(
				"Workflow edit did not preserve an unowned node and its attached comments",
			)
		}
	}
	proposed := []byte(file.String())
	if reparsed, err := parser.ParseBytes(proposed, parser.ParseComments); err != nil ||
		len(reparsed.Docs) != 1 {
		return nil, ciFailure("Proposed workflow failed reparse validation")
	}
	return proposed, nil
}

func mappingValue(node *ast.MappingNode, key string) ast.Node {
	for _, entry := range node.Values {
		if entry.Key.GetToken().Value == key {
			return entry.Value
		}
	}
	return nil
}

type yamlGuard struct{ unsupported bool }

func (g *yamlGuard) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		return nil
	}
	switch node.(type) {
	case *ast.AliasNode, *ast.AnchorNode, *ast.MergeKeyNode, *ast.TagNode, *ast.DirectiveNode:
		g.unsupported = true
	}
	return g
}
