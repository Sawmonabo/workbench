package machine

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"github.com/Sawmonabo/workbench/internal/agent/toolconfig"
	"github.com/Sawmonabo/workbench/internal/operation"
	"github.com/Sawmonabo/workbench/internal/platform"
)

// stageNative copies the role-selected sources, answers and native state into
// a private scratch context, with a tool directory for modify scripts.
func (p *preparation) stageNative(
	c operation.Context,
	files map[string][]byte,
	answers Answers,
) error {
	made, scratch, native, err := scratchNative("workbench-preview-", c.Native)
	if err != nil {
		return err
	}
	p.scratch = made
	native.Source = filepath.Join(scratch, "source")
	native.Config = filepath.Join(scratch, "answers.json")
	p.native = native
	if err = writeRoleSources(native.Source, files, answers); err != nil {
		return err
	}
	nativeAnswers := map[string]any{}
	maps.Copy(nativeAnswers, answers)
	nativeAnswers["workbench_managed"] = true
	// Setup's single private uv owner must also be usable by new user shells.
	// Existing native shell targets expose its directory without replacing
	// any unrelated ~/.local/bin command or introducing another installer.
	if uv := dependency(p.Plan.Dependencies, "uv"); operation.Within(c.Paths.Data, uv) {
		nativeAnswers["workbench_uv_dir"] = filepath.Dir(uv)
	}
	// The Claude Code and Codex hooks run the installed workbench in the bin
	// folder this apply resolved, as the migrations and agent-costs-ingest scripts
	// do. Templates cannot read the caller's environment (the preview renders
	// with a fixed one), so the path reaches them as an answer.
	workbench := c.Paths.Entrypoint()
	nativeAnswers["workbench_bin"] = workbench
	// Codex keys the trust of its hooks by the spelling of hooks.json's path,
	// which follows $HOME as it is set; chezmoi renders with the home's
	// symlinks resolved, so the spellings reach the Codex config as an answer.
	nativeAnswers["codex_hooks_files"] = toolconfig.CodexHooksFiles(c.Native.Destination)
	// The env files live in the config folder this apply resolved, which the
	// templates cannot derive from the environment either.
	nativeAnswers["workbench_config"] = c.Paths.Config
	// The dispatch servers are defined once, in Go; the Codex template renders
	// them from this answer.
	if servers := dispatchServers(answers, workbench, c.Paths.Config, c.Home); servers != nil {
		nativeAnswers["dispatch_servers"] = servers
		// Workbench registers the servers but does not build them; one not
		// built yet fails in every work-folder session until it is. An
		// isolated destination registers none.
		for _, server := range servers {
			if c.Native.Destination != c.Home {
				break
			}
			entry := server.Args[len(server.Args)-1]
			if _, err := os.Stat(entry); errors.Is(err, fs.ErrNotExist) {
				p.Plan.Warnings = append(p.Plan.Warnings, operation.Warn(
					server.Name+" is registered for Claude Code and Codex but not built yet",
					operation.Cause(
						c.ShowPath(entry)+" is missing, so it fails in sessions in the work folder",
					),
					operation.FixText("build it in its checkout"),
				))
			}
		}
	}
	// The Claude Code settings get the floor of the copy installed now, so the
	// preview and the apply render the same one. An isolated destination
	// provisions nothing, so it has no agents to update or set a floor for.
	if c.Native.Destination == c.Home {
		p.findAgents(c.Home)
	}
	if floor := p.claudeFloor(files, answers); floor != "" {
		nativeAnswers["workbench_claude_floor"] = floor
	}
	config, _ := json.Marshal(map[string]any{"data": nativeAnswers})
	if err = os.WriteFile(native.Config, config, 0o600); err != nil {
		return err
	}
	if err = p.copyNativeState(c); err != nil {
		return err
	}
	// A private alias gives modify scripts the qualified interpreter even when
	// the installation exposes only a versioned basename. No global links.
	bin := filepath.Join(scratch, "bin")
	if err = os.Mkdir(bin, 0o700); err != nil {
		return err
	}
	python := dependency(p.Plan.Dependencies, "python3")
	if err = os.Symlink(python, filepath.Join(bin, "python3")); err != nil {
		return err
	}
	// The settings modify scripts run `workbench vscode-settings`, a plain
	// filter, so this executable answers on the scripts' PATH in the preview,
	// the probes and the apply alike. It is not a plan input of its own: the
	// merge it runs decides the rendered after-image of the settings files,
	// which the plan binds, so an executable that merges differently makes a
	// different plan and --approve-plan refuses the old digest.
	self, err := os.Executable()
	if err == nil {
		self, err = filepath.EvalSymlinks(self)
	}
	if err != nil {
		return err
	}
	if err = os.Symlink(self, filepath.Join(bin, "workbench")); err != nil {
		return err
	}
	// Native stages its atomic writes, scripts and diffs in TMPDIR, so they
	// stay in this private folder, whose group operation.CheckPreservable checks.
	p.environment = []string{
		"HOME=" + c.Native.Destination,
		"PATH=" + bin + ":/usr/bin:/bin",
		"TMPDIR=" + p.scratch,
		"LANG=C.UTF-8",
		"PYTHONNOUSERSITE=1",
		"PYTHONDONTWRITEBYTECODE=1",
	}
	// A Windows-side script renders the distribution name into the PowerShell
	// profile it writes. The apply renders with it (see scriptEnvironment), so
	// the preview must as well, or the plan compares a different profile and
	// never finds the one on disk already matching.
	if name := os.Getenv("WSL_DISTRO_NAME"); platform.WSL() && name != "" {
		p.environment = append(p.environment, "WSL_DISTRO_NAME="+name)
	}
	return nil
}

// scratchNative makes a private temporary folder, named from prefix, for one
// throwaway native chezmoi run, and returns native with its persistent state
// and cache in that folder, so the run never touches Workbench's own. made is
// the folder as made, which the caller deletes; folder is the same folder with
// its links resolved, where native's paths point. On failure nothing is left.
func scratchNative(
	prefix string,
	native operation.NativeContext,
) (made, folder string, _ operation.NativeContext, err error) {
	made, err = os.MkdirTemp("", prefix)
	if err != nil {
		return "", "", native, err
	}
	if folder, err = filepath.EvalSymlinks(made); err != nil {
		_ = os.RemoveAll(made)
		return "", "", native, err
	}
	native.PersistentState = filepath.Join(folder, "state.boltdb")
	native.Cache = filepath.Join(folder, "cache")
	return made, folder, native, nil
}

// writeRoleSources writes the source files the machine role owns.
//
// Native v2.70.3 ignores removals for ignored files and rejects a target
// simultaneously owned by an ordinary source and .chezmoiremove. Select the
// canonical role-owned sources in this private copy; native still owns every
// target, removal, diff and apply operation.
func writeRoleSources(source string, files map[string][]byte, answers Answers) error {
	var roles struct {
		Targets []struct {
			Source, Target string
			Roles          []string
		} `json:"role_targets"`
	}
	if err := json.Unmarshal(files["home/.chezmoidata/role-targets.json"], &roles); err != nil {
		return err
	}
	role, _ := answers["machine_role"].(string) // validateAnswers requires a string
	omitted := map[string]bool{}
	for _, target := range roles.Targets {
		if !slices.Contains(target.Roles, role) {
			omitted["home/"+target.Source] = true
		}
	}
	for name, data := range files {
		if omitted[name] {
			continue
		}
		path := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}

// copyNativeState copies Workbench's native chezmoi state into the scratch
// context and binds its digest, or records that none exists yet. Its folder
// is checked as a folder of Workbench's own files, without changing it.
func (p *preparation) copyNativeState(c operation.Context) error {
	if err := operation.CheckPrivateDirectory(c.Paths.State, "chezmoi"); err != nil {
		return err
	}
	data, found, err := operation.ReadPrivateRecord(
		c.Native.PersistentState,
		16<<20,
		"Workbench's chezmoi state",
	)
	if !found {
		p.Plan.Inputs = append(
			p.Plan.Inputs,
			operation.Input{Name: "native-state", Digest: "absent"},
		)
		return nil
	}
	if err != nil {
		return err
	}
	p.Plan.Inputs = append(
		p.Plan.Inputs,
		operation.Input{Name: "native-state", Digest: operation.SHA256Hex(data)},
	)
	return os.WriteFile(p.native.PersistentState, data, 0o600)
}

// run executes one reviewed read-only native chezmoi command in the scratch
// context and returns its exact private stdout.
func (p *preparation) run(
	ctx context.Context,
	c operation.Context,
	args ...string,
) (string, error) {
	return p.runIn(ctx, c, p.native, args...)
}

// runIn is [preparation.run] in the given native context.
func (p *preparation) runIn(
	ctx context.Context,
	c operation.Context,
	native operation.NativeContext,
	args ...string,
) (string, error) {
	prefix, err := native.Args()
	if err != nil {
		return "", err
	}
	prefix = append(
		prefix,
		"--config-format=json",
		"--no-tty",
		"--color=false",
		"--use-builtin-git=true",
	)
	output, err := operation.Run(
		ctx,
		c,
		nil,
		operation.Process{
			Executable:    p.executable,
			Args:          append(prefix, args...),
			Directory:     p.scratch,
			Environment:   p.environment,
			Secrets:       p.secrets,
			PrivateOutput: true,
			OutputLimit:   16 << 20,
		},
	)
	return output.Stdout, err
}
