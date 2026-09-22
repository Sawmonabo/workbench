package project

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

var pythonChoice = regexp.MustCompile(`^3\.[0-9]{1,2}(\.[0-9]{1,2})?$`)

// CI integration has one conservative owner: one existing GitHub workflow at
// the selected workspace root, with an existing test job and Python choice.
// All unowned AST nodes must serialize identically before and after the merge.
func (p *Proposal) addCI(c operation.Context, owners []string) error {
	fail := func(message string) error {
		return operation.Fail(3, "project_ci", message+"; adapt project/python/checks.example.yml manually")
	}
	if len(owners) != 1 || owners[0] != c.Scope.Root {
		return fail("Automatic CI requires one Python workspace owner equal to the selected root")
	}
	pythonFile := filepath.Join(c.Scope.Root, ".python-version")
	pythonData, err := readMetadata(pythonFile)
	if err != nil {
		return err
	}
	python := strings.TrimSpace(string(pythonData))
	if !pythonChoice.MatchString(python) {
		return fail("An existing explicit .python-version is required; no Python baseline is chosen")
	}
	p.Plan.Inputs = append(p.Plan.Inputs, operation.Input{Name: pythonFile, Digest: hash(pythonData)})
	directory := filepath.Join(c.Scope.Root, ".github", "workflows")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fail("No readable existing GitHub workflow owner")
	}
	var workflows []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".yml") || strings.HasSuffix(entry.Name(), ".yaml") {
			workflows = append(workflows, filepath.Join(directory, entry.Name()))
		}
	}
	if len(workflows) != 1 {
		return fail("Multiple or missing workflows require an explicit integration decision")
	}
	data, err := readMetadata(workflows[0])
	if err != nil {
		return err
	}
	p.Plan.Inputs = append(p.Plan.Inputs, operation.Input{Name: workflows[0], Digest: hash(data)})
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil || len(file.Docs) != 1 {
		return fail("Workflow needs one valid YAML document without duplicate keys")
	}
	guard := &yamlGuard{}
	ast.Walk(guard, file.Docs[0])
	if guard.unsupported {
		return fail("Aliases, anchors, merge keys, tags or directives need manual workflow review")
	}
	if !bytes.Equal(data, []byte(file.String())) {
		return fail("Workflow formatting cannot roundtrip without normalization")
	}
	jobsPath, _ := yaml.PathString("$.jobs")
	jobsNode, err := jobsPath.FilterFile(file)
	if err != nil {
		return fail("Workflow jobs ownership is missing")
	}
	jobs, ok := jobsNode.(*ast.MappingNode)
	if !ok {
		return fail("Workflow jobs must be a mapping")
	}
	testOwned := false
	for _, job := range jobs.Values {
		key := job.Key.GetToken().Value
		if key != "test" && key != "tests" {
			continue
		}
		node, ok := job.Value.(*ast.MappingNode)
		if !ok {
			continue
		}
		steps := mappingValue(node, "steps")
		seq, ok := steps.(*ast.SequenceNode)
		if !ok {
			continue
		}
		for _, step := range seq.Values {
			mapping, ok := step.(*ast.MappingNode)
			if !ok {
				continue
			}
			run := mappingValue(mapping, "run")
			if run != nil {
				command := run.GetToken().Value
				for _, runner := range []string{"pytest", "unittest", "tox", "nox"} {
					testOwned = testOwned || strings.Contains(command, runner)
				}
			}
		}
	}
	if !testOwned {
		return fail("An existing test/tests job with an identifiable test command is required; tests will not be invented")
	}
	jobText := fmt.Sprintf(`workbench-python:
  name: Workbench Python checks (python-v1)
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
    - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1
      with:
        persist-credentials: false
    - uses: astral-sh/setup-uv@bec219d24cd3e171d82865faccec33120bb574f4
      with:
        version: %q
        python-version: %q
    - run: uv sync --locked --group dev
    - run: uv run --locked ruff check .
    - run: uv run --locked ruff format --check .
    - run: uv run --locked basedpyright
`, machine.ManagementRequirements().UV, python)
	addition, err := parser.ParseBytes([]byte(jobText), parser.ParseComments)
	if err != nil {
		return err
	}
	desired := addition.Docs[0].Body.(*ast.MappingNode)
	if existing := mappingValue(jobs, "workbench-python"); existing != nil {
		// Existing owned jobs are accepted unchanged only. User changes are never
		// guessed away; a differing recipe receives an explicit integration proposal.
		want := desired.Values[0].Value.String()
		have := existing.String()
		if strings.TrimSpace(have) != strings.TrimSpace(want) {
			var actual, expected any
			if yaml.Unmarshal([]byte(have), &actual) != nil || yaml.Unmarshal([]byte(want), &expected) != nil {
				return fail("Existing Workbench check job needs review")
			}
			if !reflect.DeepEqual(actual, expected) {
				return fail("Existing Workbench check job differs from the proposed recipe")
			}
		}
		return nil
	}
	unowned := make(map[*ast.MappingValueNode]string)
	root, ok := file.Docs[0].Body.(*ast.MappingNode)
	if !ok {
		return fail("Workflow root must be a mapping")
	}
	for _, node := range root.Values {
		if node.Key.GetToken().Value != "jobs" {
			unowned[node] = node.String()
		}
	}
	for _, node := range jobs.Values {
		unowned[node] = node.String()
	}
	if err = jobsPath.MergeFromFile(file, addition); err != nil {
		return err
	}
	for node, before := range unowned {
		if node.String() != before {
			return fail("Workflow edit did not preserve an unowned node and its attached comments")
		}
	}
	proposed := []byte(file.String())
	reparsed, err := parser.ParseBytes(proposed, parser.ParseComments)
	if err != nil || len(reparsed.Docs) != 1 {
		return fail("Proposed workflow failed reparse validation")
	}
	p.Plan.Effects = append(p.Plan.Effects, operation.Effect{Name: "ci-workflow-checks", Description: "The existing workflow will gain locked Python check steps. A future CI run may download dependencies and execute project build code; configure does not run CI", Privilege: "CI runner", Recovery: "Restoring the workflow file does not undo completed CI runs or their external effects"})
	return p.add(c, workflows[0], proposed, "Add one owned Python check job to the existing workflow; preserve existing test jobs, triggers and comments")
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
