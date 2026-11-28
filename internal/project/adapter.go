package project

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
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
		"The Workbench record ["+strings.Join(provenance, ".")+"] in "+display.Path(path)+
			" is invalid",
		operation.Line{
			Label: "needs",
			Text:  "the integer schema_version = 1 and a policies list of names",
		},
		operation.FixText("correct it"),
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
			operation.Warn(
				"Workbench's private Python and TOML Kit are missing",
				machine.InstallFix(c)...),
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
			"TOML Kit could not edit the project's TOML files",
			operation.FixText("review unsupported syntax or the Workbench record"),
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
			operation.Warn(
				display.Path(
					documents[*refusal.Refused].Path,
				)+": its layout cannot be merged safely",
				operation.Cause(refusal.Reason),
			),
		)
		return nil, a.proposal.blocked(
			"project_editor",
			"A manifest's layout needs review",
			operation.Changed("no file"),
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
