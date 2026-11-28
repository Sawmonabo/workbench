package project

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/machine"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/project/rustpolicy"
	"github.com/goccy/go-yaml/ast"
	"github.com/goccy/go-yaml/parser"
	"github.com/pelletier/go-toml/v2"
)

// rustRoots returns the selected Cargo packages and workspace roots that own
// their configuration; members are configured through their root.
func rustRoots(selected []Project) []Project {
	var roots []Project
	for _, project := range withLanguage(selected, "rust") {
		if project.Root == project.Owner {
			roots = append(roots, project)
		}
	}
	return roots
}

// rustFile is one whole file the Rust policy writes when the project has none.
type rustFile struct {
	path, description string
	data              []byte
}

// addRustPolicy plans the Rust policy at each root, a single crate or a
// workspace root, and the members of rust it owns: the manifest settings
// through the TOML Kit adapter, then rustfmt.toml, deny.toml and .mbx.toml,
// each written only when the project has none of its own.
func (p *Proposal) addRustPolicy(
	ctx context.Context,
	c operation.Context,
	options ApplyOptions,
	roots, rust []Project,
) error {
	var documents []adapterDocument
	var paths, descriptions []string
	var files []rustFile
	for _, root := range roots {
		var members []Project
		for _, project := range ownedBy(rust, root.Root) {
			if project.Root != root.Root {
				members = append(members, project)
			}
		}
		manifests, err := p.rustManifests(root, members)
		if err != nil {
			return err
		}
		for _, manifest := range manifests {
			documents = append(documents, manifest.document)
			paths = append(paths, manifest.path)
			descriptions = append(descriptions, manifest.description)
		}
		found, err := p.rustFiles(root)
		if err != nil {
			return err
		}
		files = append(files, found...)
		if err = p.rustToolchain(root.Root); err != nil {
			return err
		}
	}
	tool, err := p.adapter(ctx, c, options)
	if err != nil {
		return err
	}
	rendered, err := tool.transform(ctx, c, documents)
	if err != nil {
		return err
	}
	for i, path := range paths {
		if err = p.add(c, path, []byte(rendered[i]), descriptions[i]); err != nil {
			return err
		}
	}
	for _, file := range files {
		if err = p.add(c, file.path, file.data, file.description); err != nil {
			return err
		}
	}
	return nil
}

// rustManifest is one Cargo.toml the policy merges into.
type rustManifest struct {
	path, description string
	document          adapterDocument
}

// optIn is the member setting that applies the workspace lints to a member.
const optIn = "[lints]\nworkspace = true\n"

// rustManifests returns the manifest merges for root and its members. The
// lints are added only where the manifest has no lint table: an existing
// [lints] or [workspace.lints] is the project's own selection, like an
// existing Ruff selection, and is kept whole. A lint added beside a group
// such as clippy::all at the same priority fails clippy's deny-by-default
// lint_groups_priority. The dev profile setting is added where missing, and
// the record goes in Cargo's metadata table. Members opt into the workspace
// lints only when this plan creates [workspace.lints]; a member with lints of
// its own, or one that does not opt into lints the project already has, is
// named.
func (p *Proposal) rustManifests(root Project, members []Project) ([]rustManifest, error) {
	manifest := filepath.Join(root.Root, "Cargo.toml")
	doc := root.metadata
	workspace, isWorkspace := doc["workspace"]
	policy := rustpolicy.Lints
	provenance := []string{"package", "metadata", "workbench"}
	lints, hasLints := doc["lints"]
	if isWorkspace {
		policy = strings.ReplaceAll(rustpolicy.Lints, "[lints.", "[workspace.lints.")
		provenance = []string{"workspace", "metadata", "workbench"}
		lints, hasLints = table(workspace)["lints"]
	}
	if err := checkRecord(manifest, doc, provenance); err != nil {
		return nil, err
	}
	if hasLints && !lintGroups(lints) {
		name := "[lints]"
		if isWorkspace {
			name = "[workspace.lints]"
		}
		return nil, operation.Fail(
			operation.ExitBlocked,
			"rust_lints",
			"The "+name+" table in "+manifest+" is not a table of lint groups (rust, clippy, rustdoc); review it before planning",
		)
	}
	if hasLints {
		policy = ""
		if missingLints(table(lints)) {
			p.Plan.Warnings = append(
				p.Plan.Warnings,
				operation.Warn(
					"Kept the project's own lint levels; Workbench adds no lints beside them: "+manifest,
				),
			)
		}
	}
	if policy != "" {
		policy += "\n"
	}
	policy += rustpolicy.Profile
	optInto := map[string]bool{}
	if isWorkspace {
		optInto = p.memberOptIns(root, members, hasLints)
	}
	if optInto[root.Root] {
		policy += "\n" + optIn
	}
	description := "Merge missing Rust lints, the dev profile's line-tables debug info and the Workbench record; keep every existing value and comment"
	if hasLints {
		description = "Merge the dev profile's line-tables debug info, where missing, and the Workbench record; keep every existing value and comment"
	}
	if optInto[root.Root] {
		description = "Merge missing Rust lints, the dev profile's line-tables debug info, the root package's opt-in to the workspace lints and the Workbench record; keep every existing value and comment"
	}
	result := []rustManifest{{
		path:        manifest,
		description: description,
		document: adapterDocument{
			Path:       manifest,
			Source:     string(p.Inventory.inputs[manifest]),
			Policy:     policy,
			PolicyID:   rustpolicy.ID,
			Provenance: provenance,
		},
	}}
	for _, member := range members {
		if !optInto[member.Root] {
			continue
		}
		path := filepath.Join(member.Root, "Cargo.toml")
		result = append(result, rustManifest{
			path:        path,
			description: "Opt the workspace member into the workspace lints",
			document: adapterDocument{
				Path:   path,
				Source: string(p.Inventory.inputs[path]),
				Policy: optIn,
			},
		})
	}
	return result, nil
}

// memberOptIns returns the members of the workspace at root that this plan
// opts into the workspace lints: those without lints of their own, when the
// plan creates [workspace.lints] (hasLints is false). The workspace's own
// package is a member too, and opts in from the root manifest. Members that
// keep lints of their own, or that do not opt into lints the project already
// has, are named.
func (p *Proposal) memberOptIns(root Project, members []Project, hasLints bool) map[string]bool {
	optInto := map[string]bool{}
	var ownLints, notOptedIn []string
	candidates := members
	if _, isPackage := root.metadata["package"]; isPackage {
		candidates = append([]Project{root}, members...)
	}
	for _, member := range candidates {
		memberLints, has := member.metadata["lints"]
		switch {
		case !hasLints && !has:
			optInto[member.Root] = true
		case table(memberLints)["workspace"] == true:
		case !hasLints:
			ownLints = append(ownLints, filepath.Join(member.Root, "Cargo.toml"))
		default:
			notOptedIn = append(notOptedIn, filepath.Join(member.Root, "Cargo.toml"))
		}
	}
	if len(ownLints) > 0 {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			operation.Warn(
				"Members with a [lints] table of their own keep it and do not get the workspace lints: "+
					strings.Join(
						ownLints,
						", ",
					),
			),
		)
	}
	if len(notOptedIn) > 0 {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			operation.Warn(
				"Members that do not opt into the project's [workspace.lints] with [lints] workspace = true, left as they are: "+
					strings.Join(
						notOptedIn,
						", ",
					),
			),
		)
	}
	return optInto
}

// lintGroups reports whether a [lints] or [workspace.lints] value is a table
// of lint group tables, the only form the policy can merge into. A single
// crate's [lints] workspace = true is not: it names lints the crate has no
// workspace to inherit from.
func lintGroups(value any) bool {
	lints, ok := value.(map[string]any)
	if !ok {
		return false
	}
	for _, group := range lints {
		if _, ok := group.(map[string]any); !ok {
			return false
		}
	}
	return true
}

// policyLints are the policy's lint groups, decoded once.
var policyLints = func() map[string]any {
	var lints map[string]any
	if err := toml.Unmarshal([]byte(rustpolicy.Lints), &lints); err != nil {
		panic("invalid embedded lints.toml: " + err.Error())
	}
	return table(lints["lints"])
}()

// missingLints reports whether an existing lint table lacks a lint the policy
// sets, so that keeping it is worth naming; the table a previous apply wrote
// has them all.
func missingLints(existing map[string]any) bool {
	for group, levels := range policyLints {
		for name := range table(levels) {
			if _, ok := table(existing[group])[name]; !ok {
				return true
			}
		}
	}
	return false
}

var edition = regexp.MustCompile(`^20[0-9]{2}$`)

// rustEdition returns the edition root's manifest sets, its package's own or
// the workspace's, or "" when it sets none.
func rustEdition(doc map[string]any) string {
	value := nested(doc, "package")["edition"]
	if _, isPackage := doc["package"]; !isPackage || table(value)["workspace"] == true {
		value = nested(doc, "workspace", "package")["edition"]
	}
	text, _ := value.(string)
	return text
}

// rustFiles returns the whole files root lacks. rustfmt and cargo-deny read
// their configuration from the folder they run in or a parent, so a file at
// the root or above it inside the repository is the project's own; it is
// kept byte for byte and reported. Two candidates in one folder stop the
// plan, because the tool reads only one of them.
func (p *Proposal) rustFiles(root Project) ([]rustFile, error) {
	var files []rustFile
	formatEdition := rustEdition(root.metadata)
	rustfmt := []byte(strings.ReplaceAll(rustpolicy.Rustfmt, "EDITION", formatEdition))
	existing, err := p.toolConfig(
		root.Root,
		"rustfmt",
		[]string{"rustfmt.toml", ".rustfmt.toml"},
		true,
	)
	if err != nil {
		return nil, err
	}
	switch {
	case existing != "":
		p.keep(root.Root, existing, "rustfmt.toml", rustfmt, "rustfmt")
	case formatEdition == "":
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			operation.Warn("No rustfmt.toml written: "+filepath.Join(root.Root, "Cargo.toml")+
				" sets no edition (package.edition or workspace.package.edition) to format with"),
		)
	case !edition.MatchString(formatEdition):
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			operation.Warn(fmt.Sprintf(
				"No rustfmt.toml written: the manifest's edition %q is not a Rust edition",
				formatEdition,
			)),
		)
	default:
		files = append(files, rustFile{
			path:        filepath.Join(root.Root, "rustfmt.toml"),
			description: "Create the rustfmt configuration for edition " + formatEdition + ", so bare rustfmt and the editor format as cargo fmt does",
			data:        rustfmt,
		})
	}
	existing, err = p.toolConfig(
		root.Root,
		"cargo-deny",
		[]string{"deny.toml", ".deny.toml", filepath.Join(".cargo", "deny.toml")},
		true,
	)
	if err != nil {
		return nil, err
	}
	if existing != "" {
		p.keep(root.Root, existing, "deny.toml", rustpolicy.Deny, "cargo-deny")
	} else {
		files = append(files, rustFile{
			path:        filepath.Join(root.Root, "deny.toml"),
			description: "Create the cargo-deny configuration for cargo deny check advisories bans sources; licence checks are left to the project",
			data:        rustpolicy.Deny,
		})
	}
	mbx := filepath.Join(root.Root, ".mbx.toml")
	data, err := p.readInput(mbx)
	if err != nil {
		return nil, err
	}
	if data != nil {
		p.keep(root.Root, mbx, ".mbx.toml", rustpolicy.Mbx, "mbx")
	} else {
		files = append(files, rustFile{
			path:        mbx,
			description: "Create the mbx workspace setting that shares compiled crates between checkouts; the machine's compile budget stays in mbx's own config",
			data:        rustpolicy.Mbx,
		})
	}
	return files, nil
}

// toolConfig returns the configuration file of tool, one of names, nearest to
// root in root and its parents up to the repository root, or "" when there
// is none. Every candidate is bound as a plan input. When exclusive, two
// candidates in one folder stop the plan: the tool reads only one of them, so
// a setting written to the other would be dead.
func (p *Proposal) toolConfig(root, tool string, names []string, exclusive bool) (string, error) {
	var found string
	err := ancestors(root, func(parent string) (bool, error) {
		var here []string
		for _, name := range names {
			path := filepath.Join(parent, name)
			data, err := p.readInput(path)
			if err != nil {
				return false, err
			}
			if data != nil {
				here = append(here, path)
			}
		}
		if exclusive && len(here) > 1 {
			return false, operation.Fail(
				operation.ExitBlocked,
				"rust_config",
				tool+" reads only one of "+strings.Join(here, " and ")+"; keep one before planning",
			)
		}
		if len(here) > 0 {
			found = here[0]
		}
		return found != "", nil
	})
	return found, err
}

// keep reports a whole file the project already has, unless it is exactly
// the file the policy writes at root, as a second apply finds it.
func (p *Proposal) keep(root, path, name string, policy []byte, tool string) {
	if path == filepath.Join(root, name) && bytes.Equal(p.Inventory.inputs[path], policy) {
		return
	}
	p.Plan.Warnings = append(
		p.Plan.Warnings,
		operation.Warn("Kept the project's own "+tool+" configuration: "+path),
	)
}

// rustToolchain warns when no rust-toolchain.toml or rust-toolchain pins
// root's toolchain, or when the pin does not install rustfmt and clippy. It
// never writes one: the project picks its toolchain.
func (p *Proposal) rustToolchain(root string) error {
	path, err := p.toolConfig(
		root,
		"rustup",
		[]string{"rust-toolchain.toml", "rust-toolchain"},
		false,
	)
	if err != nil {
		return err
	}
	if path == "" {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			operation.Warn("No rust-toolchain.toml pins the Rust toolchain at "+root+
				"; Workbench never writes one. Add one that lists rustfmt and clippy in components"),
		)
		return nil
	}
	var pin struct {
		Toolchain struct {
			Components []string `toml:"components"`
			Profile    string   `toml:"profile"`
		} `toml:"toolchain"`
	}
	_ = toml.Unmarshal(
		p.Inventory.inputs[path],
		&pin,
	) // A one-line channel file lists no components.
	components := pin.Toolchain.Components
	if pin.Toolchain.Profile == "default" || pin.Toolchain.Profile == "complete" {
		components = append(components, "rustfmt", "clippy")
	}
	if !slices.Contains(components, "rustfmt") || !slices.Contains(components, "clippy") {
		p.Plan.Warnings = append(
			p.Plan.Warnings,
			operation.Warn(
				path+" does not list both rustfmt and clippy in components, so cargo fmt and cargo clippy can be missing where it is installed",
			),
		)
	}
	return nil
}

// cargoTest matches a cargo test or cargo nextest command, with or without a
// +toolchain.
var cargoTest = regexp.MustCompile(`\bcargo\s+(\+\S+\s+)?(test|nextest)\b`)

// rustCI returns the Rust check job and the workflow it joins: the one
// workflow at the repository root with a run step that runs cargo test or
// cargo nextest.
func (p *Proposal) rustCI(c operation.Context, root Project) (string, ownedJob, error) {
	if root.Root != c.Scope.Root {
		return "", ownedJob{}, rustCI.refuse(
			"Automatic CI requires the Cargo package or workspace root at the selected root",
		)
	}
	if !isRepositoryRoot(c.Scope.Root) {
		return "", ownedJob{}, rustCI.refuse(
			"GitHub reads workflows at the repository root; select the folder that holds .git",
		)
	}
	toolchain := false
	for _, name := range []string{"rust-toolchain.toml", "rust-toolchain"} {
		data, err := p.readInput(filepath.Join(root.Root, name))
		if err != nil {
			return "", ownedJob{}, err
		}
		toolchain = toolchain || data != nil
	}
	if !toolchain {
		return "", ownedJob{}, rustCI.refuse(
			"An existing rust-toolchain.toml at the root is required; the job installs the toolchain it names and Workbench picks none",
		)
	}
	lock, err := p.readInput(filepath.Join(root.Root, "Cargo.lock"))
	if err != nil {
		return "", ownedJob{}, err
	}
	if lock == nil {
		return "", ownedJob{}, rustCI.refuse(
			"An existing Cargo.lock at the root is required; the job's cargo clippy --locked checks against it",
		)
	}
	path, err := p.testWorkflow(
		c.Scope.Root,
		"cargo test or cargo nextest",
		cargoTest,
		rustCI.refuse,
	)
	if err != nil {
		return "", ownedJob{}, err
	}
	requirements, versions, err := readSourceVersions(c)
	if err != nil {
		return "", ownedJob{}, err
	}
	pins := rustPins{
		cargoDeny: versions.CargoTools["cargo-deny"],
		gitleaks:  versions.Binaries["gitleaks"],
	}
	pins.cargoDenySHA256 = versions.CI.CargoDeny[pins.cargoDeny]
	pins.gitleaksSHA256 = versions.CI.Gitleaks[pins.gitleaks]
	if err = checkPins([]pinCheck{
		{"cargo_tools.cargo-deny", pins.cargoDeny, machine.IsPlainVersion},
		{
			"project_ci.cargo_deny_linux_x64_sha256 for cargo-deny " + pins.cargoDeny,
			pins.cargoDenySHA256,
			operation.ValidDigest,
		},
		{"linux_binaries.gitleaks", pins.gitleaks, machine.IsPlainVersion},
		{
			"project_ci.gitleaks_linux_x64_sha256 for gitleaks " + pins.gitleaks,
			pins.gitleaksSHA256,
			operation.ValidDigest,
		},
	}); err != nil {
		return "", ownedJob{}, err
	}
	if pins.checkout, err = requirements.Uses("actions/checkout"); err != nil {
		return "", ownedJob{}, err
	}
	addition, desired, err := rustJob(pins)
	if err != nil {
		return "", ownedJob{}, err
	}
	return path, ownedJob{
		name:          "workbench-rust",
		addition:      addition,
		desired:       desired,
		refsMayDiffer: true,
		fail:          rustCI.refuse,
		effect:        "The workflow that runs cargo test will gain Rust check steps. A future CI run installs the toolchain rust-toolchain.toml names, downloads the crates' dependencies, the RustSec advisory database and the pinned cargo-deny and gitleaks, and compiles project code, which runs its build scripts and procedural macros",
	}, nil
}

// rustCI is the Rust policy's remedy.
const rustCI ciRemedy = "add the workbench-rust job described in project/rustpolicy/README.md manually"

// rustPins are the tool versions the Rust check job uses, from the selected
// machine source's versions.toml.
type rustPins struct {
	checkout        string
	cargoDeny       string
	cargoDenySHA256 string
	gitleaks        string
	gitleaksSHA256  string
}

// rustJob renders the owned Rust job as a one-entry jobs mapping. rustup,
// which the runner image carries, installs the toolchain rust-toolchain.toml
// names. The cargo-deny and gitleaks archives are checked against their
// SHA-256 before use. Each run is one plain line: go-yaml's merge does not
// re-indent the text of a block scalar, and mergeJob's reparse check would
// refuse the result.
func rustJob(pins rustPins) (*ast.File, *ast.MappingNode, error) {
	archive := "cargo-deny-" + pins.cargoDeny + "-x86_64-unknown-linux-musl"
	text := fmt.Sprintf(`workbench-rust:
  name: Workbench Rust checks (%[1]s)
  runs-on: ubuntu-24.04
  timeout-minutes: 30
  defaults:
    run:
      shell: bash
      working-directory: .
  permissions:
    contents: read
  concurrency:
    group: workbench-rust-${{ github.workflow }}-${{ github.ref }}
    cancel-in-progress: true
  steps:
    - uses: %[2]s
      with:
        persist-credentials: false
        fetch-depth: 0
    - run: rustup toolchain install
    - run: cargo fmt --all --check
    - run: cargo clippy --workspace --all-targets --locked -- -D warnings
    - name: Download cargo-deny
      env:
        CARGO_DENY_URL: https://github.com/EmbarkStudios/cargo-deny/releases/download/%[3]s/%[4]s.tar.gz
      run: curl -fsSL --retry 3 -o "$RUNNER_TEMP/cargo-deny.tar.gz" "$CARGO_DENY_URL"
    - name: Check the cargo-deny download
      run: printf '%%s  %%s\n' '%[5]s' "$RUNNER_TEMP/cargo-deny.tar.gz" | sha256sum --check --strict
    - run: tar -xzf "$RUNNER_TEMP/cargo-deny.tar.gz" -C "$RUNNER_TEMP" --strip-components 1 %[4]s/cargo-deny
    - name: Check advisories, bans and sources with cargo-deny
      run: exec "$RUNNER_TEMP/cargo-deny" --locked check advisories bans sources
    - name: Download Gitleaks
      env:
        GITLEAKS_URL: https://github.com/gitleaks/gitleaks/releases/download/v%[6]s/gitleaks_%[6]s_linux_x64.tar.gz
      run: curl -fsSL --retry 3 -o "$RUNNER_TEMP/gitleaks.tar.gz" "$GITLEAKS_URL"
    - name: Check the Gitleaks download
      run: printf '%%s  %%s\n' '%[7]s' "$RUNNER_TEMP/gitleaks.tar.gz" | sha256sum --check --strict
    - run: tar -xzf "$RUNNER_TEMP/gitleaks.tar.gz" -C "$RUNNER_TEMP" gitleaks
    - name: Scan Git history with Gitleaks
      run: exec "$RUNNER_TEMP/gitleaks" git --redact --no-banner --exit-code 1
`, rustpolicy.ID, pins.checkout, pins.cargoDeny, archive, pins.cargoDenySHA256, pins.gitleaks, pins.gitleaksSHA256)
	file, err := parser.ParseBytes([]byte(text), parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}
	job, ok := file.Docs[0].Body.(*ast.MappingNode)
	if !ok {
		return nil, nil, errors.New("rust check job template is not a mapping")
	}
	return file, job, nil
}
