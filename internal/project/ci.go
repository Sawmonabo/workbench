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
	"github.com/Sawmonabo/workbench/project/pythonpolicy"
	"github.com/goccy/go-yaml"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
)

var pythonVersion = regexp.MustCompile(`^3\.[0-9]{1,2}(\.[0-9]{1,2})?$`)

// ownedJob is one Workbench check job proposed for a workflow.
type ownedJob struct {
	name     string
	addition *ast.File
	desired  *ast.MappingNode
	// refsMayDiffer keeps an existing job that differs from the recipe only in
	// its uses: refs, which Dependabot bumps, and reports it.
	refsMayDiffer bool
	// effect says what a future CI run of the job does.
	effect string
	// fail words a refusal for the job's language.
	fail func(string) error
}

// addCI proposes each selected language's owned check job. Jobs aimed at the
// same workflow are merged into one edit of it. All unowned AST nodes must
// serialize identically before and after the merge.
func (p *Proposal) addCI(c operation.Context, selected []Project) error {
	var paths []string
	jobs := map[string][]ownedJob{}
	if python := withLanguage(selected, "python"); len(python) > 0 {
		path, job, err := p.pythonCI(c, ownersOf(python))
		if err != nil {
			return err
		}
		paths, jobs[path] = append(paths, path), append(jobs[path], job)
	}
	if modules := withLanguage(selected, "go"); len(modules) > 0 {
		path, job, err := p.goCI(c, modules[0])
		if err != nil {
			return err
		}
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
		jobs[path] = append(jobs[path], job)
	}
	if roots := rustRoots(selected); len(roots) > 0 {
		path, job, err := p.rustCI(c, roots[0])
		if err != nil {
			return err
		}
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
		jobs[path] = append(jobs[path], job)
	}
	var effects []string
	for _, path := range paths {
		added, err := p.addJobs(c, path, jobs[path])
		if err != nil {
			return err
		}
		effects = append(effects, added...)
	}
	if len(effects) > 0 {
		p.Plan.Effects = append(
			p.Plan.Effects,
			operation.Effect{
				Name:        "ci-workflow-checks",
				Description: strings.Join(effects, ". ") + "; project apply does not run CI",
				Privilege:   "CI runner",
				Recovery:    "Restoring the workflow file does not undo completed CI runs or their external effects",
			},
		)
	}
	return nil
}

// pythonCI returns the Python check job and its workflow: the one existing
// GitHub workflow at the selected workspace root, with an existing test job
// and Python choice.
func (p *Proposal) pythonCI(c operation.Context, owners []string) (string, ownedJob, error) {
	if len(owners) != 1 || owners[0] != c.Scope.Root {
		return "", ownedJob{}, pythonCI.refuse(
			"Automatic CI requires one Python workspace owner equal to the selected root",
		)
	}
	python, err := p.pythonChoice(c.Scope.Root)
	if err != nil {
		return "", ownedJob{}, err
	}
	path, data, err := p.readWorkflow(c.Scope.Root)
	if err != nil {
		return "", ownedJob{}, err
	}
	file, err := parseWorkflow(data, pythonCI.refuse)
	if err != nil {
		return "", ownedJob{}, err
	}
	jobs, err := workflowJobs(file, pythonCI.refuse)
	if err != nil {
		return "", ownedJob{}, err
	}
	if !slices.ContainsFunc(jobs.Values, runsTests) {
		return "", ownedJob{}, pythonCI.refuse(
			"An existing test/tests job with an identifiable test command is required; tests will not be invented",
		)
	}
	requirements, err := machine.ManagementRequirements(c)
	if err != nil {
		return "", ownedJob{}, err
	}
	addition, desired, err := checkJob(python, requirements)
	if err != nil {
		return "", ownedJob{}, err
	}
	return path, ownedJob{
		name:     "workbench-python",
		addition: addition,
		desired:  desired,
		effect:   "The existing workflow will gain locked Python check steps. A future CI run may download dependencies and execute project build code",
		fail:     pythonCI.refuse,
	}, nil
}

// addJobs merges jobs into the workflow at path as one edit and returns the
// effects of the jobs it adds. An owned job already there is checked against
// its recipe instead.
func (p *Proposal) addJobs(c operation.Context, path string, jobs []ownedJob) ([]string, error) {
	data, err := p.Inventory.read(path)
	if err != nil {
		return nil, err
	}
	// Jobs are in language order, so a shared workflow's refusal names Python's
	// manual recipe whenever a Python job is among them.
	fail := jobs[0].fail
	file, err := parseWorkflow(data, fail)
	if err != nil {
		return nil, err
	}
	jobsPath, _ := yaml.PathString("$.jobs")
	var effects, added []string
	for _, job := range jobs {
		node, err := workflowJobs(file, fail)
		if err != nil {
			return nil, err
		}
		if existing := mappingValue(node, job.name); existing != nil {
			refs, problem := checkOwnedJob(existing, job.desired)
			switch {
			case problem != "":
				return nil, fail(problem)
			case refs && !job.refsMayDiffer:
				return nil, fail("Existing Workbench check job differs from the proposed recipe")
			case refs:
				p.Plan.Warnings = append(
					p.Plan.Warnings,
					"Kept "+job.name+" in "+path+": it differs from the recipe only in its uses: refs",
				)
			}
			continue
		}
		if err = mergeJob(file, jobsPath, node, job.addition, fail); err != nil {
			return nil, err
		}
		effects, added = append(effects, job.effect), append(added, job.name)
	}
	if len(added) == 0 {
		return nil, nil
	}
	var description string
	switch {
	case slices.Equal(added, []string{"workbench-python"}):
		description = "Add one owned Python check job to the existing workflow; preserve existing test jobs, triggers and comments"
	case slices.Equal(added, []string{"workbench-go"}):
		description = "Add one owned Go check job to the workflow that runs go test; preserve existing jobs, triggers and comments"
	case slices.Equal(added, []string{"workbench-rust"}):
		description = "Add one owned Rust check job to the workflow that runs cargo test; preserve existing jobs, triggers and comments"
	default:
		description = "Add the owned check jobs " + strings.Join(added, " and ") +
			" to the existing workflow; preserve existing jobs, triggers and comments"
	}
	return effects, p.add(c, path, []byte(file.String()), description)
}

// ciRemedy is what a person does by hand when Workbench refuses to add a
// policy's CI job: that policy's own instructions.
type ciRemedy string

// pythonCI is the Python policy's remedy.
const pythonCI ciRemedy = "adapt project/pythonpolicy/checks.example.yml manually"

// refuse refuses a CI change with message and what to do by hand instead.
func (r ciRemedy) refuse(message string) error {
	return operation.Fail(operation.ExitBlocked, "project_ci", message+"; "+string(r))
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
		return "", pythonCI.refuse(
			"An existing explicit .python-version is required; no Python baseline is chosen",
		)
	}
	p.Plan.Inputs = append(
		p.Plan.Inputs,
		operation.Input{Name: path, Digest: operation.SHA256Hex(data)},
	)
	return python, nil
}

// readWorkflow reads the single workflow under root, besides the Dependabot
// auto-merge workflow, and binds it as a plan input.
func (p *Proposal) readWorkflow(root string) (string, []byte, error) {
	workflows, readable := workflowFiles(root)
	if !readable {
		return "", nil, pythonCI.refuse("No readable existing GitHub workflow owner")
	}
	// The Dependabot auto-merge workflow --dependabot writes runs no tests and
	// is not a candidate.
	workflows = slices.DeleteFunc(workflows, func(path string) bool {
		return path == filepath.Join(root, autoMergeName)
	})
	if len(workflows) != 1 {
		return "", nil, pythonCI.refuse(
			"Multiple or missing workflows require an explicit integration decision",
		)
	}
	data, err := p.readInput(workflows[0])
	return workflows[0], data, err
}

// workflowFiles lists the regular workflow files under root; readable is
// false when root has no readable workflow folder.
func workflowFiles(root string) (workflows []string, readable bool) {
	directory := filepath.Join(root, ".github", "workflows")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, false
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".yml") || strings.HasSuffix(entry.Name(), ".yaml") {
			workflows = append(workflows, filepath.Join(directory, entry.Name()))
		}
	}
	return workflows, true
}

// parseWorkflow parses a workflow that must hold one document that roundtrips
// exactly and uses no YAML feature the merge could silently change. fail
// words a refusal for the language whose job is being added.
func parseWorkflow(data []byte, fail func(string) error) (*ast.File, error) {
	file, err := parser.ParseBytes(data, parser.ParseComments)
	if err != nil || len(file.Docs) != 1 {
		return nil, fail("Workflow needs one valid YAML document without duplicate keys")
	}
	guard := &yamlGuard{}
	ast.Walk(guard, file.Docs[0])
	if guard.unsupported {
		return nil, fail(
			"Aliases, anchors, merge keys, tags or directives need manual workflow review",
		)
	}
	if !bytes.Equal(data, []byte(file.String())) {
		return nil, fail("Workflow formatting cannot roundtrip without normalization")
	}
	return file, nil
}

// workflowJobs returns the workflow's jobs mapping.
func workflowJobs(file *ast.File, fail func(string) error) (*ast.MappingNode, error) {
	jobsPath, _ := yaml.PathString("$.jobs")
	jobsNode, err := jobsPath.FilterFile(file)
	if err != nil {
		return nil, fail("Workflow jobs ownership is missing")
	}
	jobs, ok := jobsNode.(*ast.MappingNode)
	if !ok {
		return nil, fail("Workflow jobs must be a mapping")
	}
	return jobs, nil
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
		// A block scalar (run: | or >-) holds its text in a literal node; its token
		// is only the indicator.
		var command string
		switch run := mappingValue(mapping, "run").(type) {
		case *ast.LiteralNode:
			if run.Value == nil {
				continue
			}
			command = run.Value.Value
		case *ast.StringNode:
			command = run.Value
		default:
			continue
		}
		for _, runner := range []string{"pytest", "unittest", "tox", "nox"} {
			if strings.Contains(command, runner) {
				return true
			}
		}
	}
	return false
}

// checkJob renders the owned job for python as a one-entry jobs mapping, with
// the uv and GitHub Action pins from versions.toml.
func checkJob(
	python string,
	requirements machine.Requirements,
) (*ast.File, *ast.MappingNode, error) {
	checkout, err := requirements.Uses("actions/checkout")
	if err != nil {
		return nil, nil, err
	}
	setupUV, err := requirements.Uses("astral-sh/setup-uv")
	if err != nil {
		return nil, nil, err
	}
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
    - uses: %s
      with:
        persist-credentials: false
    - uses: %s
      with:
        version: %q
        python-version: %q
    - run: uv sync --locked --group dev
    - run: uv run --locked ruff check .
    - run: uv run --locked ruff format --check .
    - run: uv run --locked basedpyright
`, pythonpolicy.ID, checkout, setupUV, requirements.UV, python)
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

// checkOwnedJob compares an existing owned job with its recipe. User changes
// are never guessed away: problem names a difference that needs review, and
// refsOnly reports a job that differs from the recipe only in its uses: refs.
func checkOwnedJob(existing ast.Node, desired *ast.MappingNode) (refsOnly bool, problem string) {
	want := desired.Values[0].Value.String()
	have := existing.String()
	if strings.TrimSpace(have) == strings.TrimSpace(want) {
		return false, ""
	}
	var actual, expected any
	if yaml.Unmarshal([]byte(have), &actual) != nil ||
		yaml.Unmarshal([]byte(want), &expected) != nil {
		return false, "Existing Workbench check job needs review"
	}
	if reflect.DeepEqual(actual, expected) {
		return false, ""
	}
	if reflect.DeepEqual(withoutRefs(actual), withoutRefs(expected)) {
		return true, ""
	}
	return false, "Existing Workbench check job differs from the proposed recipe"
}

// withoutRefs returns value with the @ref of every uses: entry removed.
func withoutRefs(value any) any {
	switch node := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(node))
		for key, item := range node {
			if uses, ok := item.(string); ok && key == "uses" {
				item, _, _ = strings.Cut(uses, "@")
			}
			result[key] = withoutRefs(item)
		}
		return result
	case []any:
		result := make([]any, len(node))
		for i, item := range node {
			result[i] = withoutRefs(item)
		}
		return result
	}
	return value
}

// mergeJob adds the job to the workflow file and proves every unowned node
// serializes as before and the result parses again.
func mergeJob(
	file *ast.File,
	jobsPath *yaml.Path,
	jobs *ast.MappingNode,
	addition *ast.File,
	fail func(string) error,
) error {
	root, ok := file.Docs[0].Body.(*ast.MappingNode)
	if !ok {
		return fail("Workflow root must be a mapping")
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
		return err
	}
	for node, before := range unowned {
		if node.String() != before {
			return fail(
				"Workflow edit did not preserve an unowned node and its attached comments",
			)
		}
	}
	if reparsed, err := parser.ParseBytes(
		[]byte(file.String()),
		parser.ParseComments,
	); err != nil ||
		len(reparsed.Docs) != 1 {
		return fail("Proposed workflow failed reparse validation")
	}
	return nil
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
