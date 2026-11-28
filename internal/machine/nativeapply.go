package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Apply executes the prepared configuration through the same native owner.
// Callers must hold mutation authority and a durable checkpoint of any target changes.
// An interactive run owns terminal; otherwise redacted output goes to progress.
func (p *preparation) Apply(
	ctx context.Context,
	c operation.Context,
	m *operation.Mutation,
	terminal *os.File,
	progress io.Writer,
) error {
	if err := m.Check(); err != nil {
		return err
	}
	if !p.Plan.Complete || p.scratch == "" || p.Plan.Scope != c.Scope {
		return operation.Fail(
			operation.ExitBlocked,
			"plan",
			"Native apply requires an open complete prepared plan",
		)
	}
	if c.Native.PersistentState != filepath.Join(c.Paths.State, "chezmoi", "chezmoi.boltdb") {
		return operation.Fail(operation.ExitInvalid, "state", "Unexpected native state target")
	}
	if _, err := operation.PrivateDirectory(c.Paths.State, "chezmoi"); err != nil {
		return err
	}
	native := p.native
	report := ""
	native.PersistentState = c.Native.PersistentState
	args, err := native.Args()
	if err != nil {
		return err
	}
	args = append(args, "--config-format=json", "--color=false", "--use-builtin-git=true")
	if terminal == nil {
		args = append(args, "--no-tty")
	}
	// --keep-going: a failed step does not stop the steps after it, and chezmoi
	// runs a run_once_ or run_onchange_ script that failed again next time.
	args = append(args, "apply", "--force", "--keep-going")
	environment := p.environment
	if c.Native.Destination != c.Home {
		// An isolated destination gets files only, rendered under the same
		// environment as the preview; its scripts provision the real home.
		args = append(args, "--exclude=scripts")
	} else if environment, report, err = p.scriptsEnvironment(ctx, c); err != nil {
		return err
	}
	request := operation.Process{
		Executable:  p.executable,
		Args:        args,
		Directory:   p.scratch,
		Environment: environment,
		Secrets:     p.secrets,
		Mutates:     true,
		Terminal:    terminal,
	}
	if terminal == nil {
		request.Progress = progress
	}
	_, err = operation.Run(ctx, c, m, request)
	if report != "" {
		p.blocked = readBlocked(report, p.Plan.Effects)
	}
	return err
}

// scriptsEnvironment selects the scripts this apply runs and returns the
// environment they run with, and the file in which a script that cannot do
// one effect says so and goes on with the others, so one part's failure does
// not stop the rest.
func (p *preparation) scriptsEnvironment(
	ctx context.Context,
	c operation.Context,
) (environment []string, report string, err error) {
	if err = p.selectScripts(); err != nil {
		return nil, "", err
	}
	environment = scriptEnvironment(
		c,
		p.Plan.Dependencies,
		p.scratch,
		filepath.Join(p.scratch, "bin"),
	)
	report = filepath.Join(p.scratch, "effect-report")
	if err = p.runScriptsThroughStepRunner(); err != nil {
		return nil, "", err
	}
	environment = append(environment, "WORKBENCH_EFFECT_REPORT="+report)
	if p.askpass != "" {
		// The darwin scripts give it to Homebrew's own commands and the
		// Touch ID step's one sudo command only.
		environment = append(environment, "WORKBENCH_SUDO_ASKPASS="+p.askpass)
	}
	// Every effect is gated on its own: a shared script runs only the
	// sections whose effect is checked, and the optional ones keep their
	// existing == 1 checks.
	for _, effect := range p.Plan.Effects {
		if effect.Fixed {
			continue
		}
		value := "0"
		if effect.Checked {
			value = "1"
		}
		environment = append(environment, EffectVariable(effect.Name)+"="+value)
	}
	environment = append(environment, p.updateEnvironment()...)
	agents, err := p.agentEnvironment(ctx, c, report)
	if err != nil {
		return nil, "", err
	}
	environment = append(environment, agents...)
	return environment, report, nil
}

// updateEnvironment tells the apply scripts which Homebrew updates the plan
// checks, and whether Homebrew's index is already fresh.
func (p *preparation) updateEnvironment() []string {
	var environment []string
	if updates := p.checkedUpdates(p.appUpdates); len(updates) > 0 {
		environment = append(environment, "WORKBENCH_APP_UPDATES="+strings.Join(updates, " "))
	}
	if updates := p.checkedUpdates(p.toolUpdates); len(updates) > 0 {
		environment = append(environment, "WORKBENCH_TOOL_UPDATES="+strings.Join(updates, " "))
	}
	if p.brewRefreshed {
		environment = append(environment, "WORKBENCH_BREW_REFRESHED=1")
	}
	return environment
}

// stepRunner is the interpreter chezmoi runs each setup script with. A script
// that ends nonzero without naming any of its effects in the report gets each
// checked effect it owns named there (see the case list Workbench writes in),
// so the apply shows which step failed. A script ended by a signal, such as an
// interrupt, is not reported.
const stepRunner = `#!/bin/sh
script=$1
bash "$script"
status=$?
if [ "$status" -eq 0 ] || [ "$status" -ge 128 ] || [ -z "${WORKBENCH_EFFECT_REPORT:-}" ]; then
    exit "$status"
fi
name=${script##*/}
name=${name%%.sh}
name=${name##*.}
effects=
case $name in
%s
esac
tab=$(printf '\t')
for effect in $effects; do
    if grep -q "^$effect$tab" "$WORKBENCH_EFFECT_REPORT" 2>/dev/null; then
        exit "$status"
    fi
done
for effect in $effects; do
    printf '%%s\t%%s\n' "$effect" "its script stopped with exit status $status before it finished; the output above says why, and the next apply tries again" >>"$WORKBENCH_EFFECT_REPORT"
done
exit "$status"
`

// runScriptsThroughStepRunner writes [stepRunner] for this plan's checked
// effects and makes it chezmoi's interpreter for .sh scripts in the private
// config.
func (p *preparation) runScriptsThroughStepRunner() error {
	owned := map[string][]string{}
	for _, effect := range p.Plan.Effects {
		if !effect.Checked || effect.Fixed {
			continue
		}
		sources := effectSources[effect.Name]
		if script := p.updateScript(effect.Name); script != "" {
			sources = []string{script}
		}
		for _, source := range sources {
			if !strings.HasPrefix(source, ".") {
				owned[source] = append(owned[source], effect.Name)
			}
		}
	}
	var cases []string
	for _, script := range slices.Sorted(maps.Keys(owned)) {
		cases = append(
			cases,
			fmt.Sprintf("%s) effects='%s' ;;", script, strings.Join(owned[script], " ")),
		)
	}
	runner := filepath.Join(p.scratch, "step-runner")
	if err := os.WriteFile(
		runner,
		fmt.Appendf(nil, stepRunner, strings.Join(cases, "\n")),
		0o700,
	); err != nil {
		return err
	}
	data, err := os.ReadFile(p.native.Config)
	if err != nil {
		return err
	}
	config := map[string]any{}
	if err = json.Unmarshal(data, &config); err != nil {
		return err
	}
	config["interpreters"] = map[string]any{"sh": map[string]any{"command": runner}}
	data, _ = json.Marshal(config)
	return os.WriteFile(p.native.Config, data, 0o600)
}

// readBlocked reads the file in which a script names each effect it could not
// do, one "effect, tab, reason" line each, keeping the effects of this plan. An
// effect that more than one part of its script could not do has a line for each,
// and its reason is all of theirs, so none is lost. A take-over runs its step,
// so what blocks the step blocks its take-over too.
func readBlocked(path string, effects []operation.Effect) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	reasons := map[string][]string{}
	for line := range strings.SplitSeq(string(data), "\n") {
		step, reason, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		for _, name := range []string{step, takeOverPrefix + step} {
			if !slices.ContainsFunc(effects, func(effect operation.Effect) bool {
				return effect.Name == name
			}) {
				continue
			}
			list := reasons[name]
			if reason != "" && !slices.Contains(list, reason) {
				list = append(list, reason)
			}
			reasons[name] = list
		}
	}
	blocked := map[string]string{}
	for name, list := range reasons {
		blocked[name] = strings.Join(list, "; ")
	}
	return blocked
}

// selectScripts applies the selection to the private source copy. A script
// whose listed effects are all unchecked is deleted, so native neither runs it
// nor records it as run; a later apply with an effect checked runs it as if
// new. A script that stays but has an unchecked owner gets a comment naming the
// unchecked effects, so its content, which chezmoi hashes to decide whether a
// run-once or on-change script ran, differs from the fully checked one: the
// effect's section runs when it is checked later. A checked update effect
// owns the script that runs it (see [preparation.updateScript]) like a listed
// effect; the agent updates script, which has no other owner, is deleted
// without one. A script that stays and carries a checked effect whose probe
// found something to do runs every time in this apply; see [runEveryTime].
// The plan's source identity and digest are computed from the unmodified source
// before this runs.
func (p *preparation) selectScripts() error {
	checked, listed, due := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, effect := range p.Plan.Effects {
		checked[effect.Name], listed[effect.Name] = effect.Checked, true
		due[effect.Name] = effect.Checked && hasWork(effect)
	}
	root := filepath.Join(p.native.Source, "home", ".chezmoiscripts")
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		base := entry.Name()
		if !strings.HasPrefix(base, "run_") || !strings.HasSuffix(base, ".sh.tmpl") {
			return nil
		}
		script := strings.TrimSuffix(base[strings.LastIndex(base, "_")+1:], ".sh.tmpl")
		owners := scriptEffects(script)
		// A checked update keeps the script that runs it, even with the apps
		// and the work extension unchecked.
		for _, effect := range p.Plan.Effects {
			if p.updateScript(effect.Name) == script {
				owners = append(owners, effect.Name)
			}
		}
		if len(owners) == 0 {
			if script == agentUpdatesScript {
				return os.Remove(path)
			}
			return nil
		}
		var skipped []string
		for _, name := range owners {
			if listed[name] && !checked[name] {
				skipped = append(skipped, name)
			}
		}
		if !slices.ContainsFunc(owners, func(name string) bool { return checked[name] }) {
			return os.Remove(path)
		}
		if slices.ContainsFunc(owners, func(name string) bool { return due[name] }) {
			if path, err = runEveryTime(path); err != nil {
				return err
			}
		}
		if len(skipped) == 0 {
			return nil
		}
		return markSkipped(path, skipped)
	})
}

// runEveryTime renames a run_once_ or run_onchange_ script to a plain run_
// one and returns its new path. chezmoi decides whether those run from their
// content alone, so a script whose tool was removed after it ran would not run
// although its probe found something to do. A run_ script always runs, under
// the same target name and so in the same order, and chezmoi records the same
// content state for it as for the script it stands in for, so the next apply
// with nothing to do runs nothing.
func runEveryTime(path string) (string, error) {
	base := filepath.Base(path)
	rest, ok := strings.CutPrefix(base, "run_once_")
	if !ok {
		if rest, ok = strings.CutPrefix(base, "run_onchange_"); !ok {
			return path, nil
		}
	}
	plain := filepath.Join(filepath.Dir(path), "run_"+rest)
	return plain, os.Rename(path, plain)
}

// markSkipped inserts a comment naming the skipped effects directly after the
// script's shebang line. A line at the end would render on every platform,
// since the templates wrap their text in a platform condition.
func markSkipped(path string, skipped []string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	start := strings.Index(text, "#!")
	if start < 0 || !strings.Contains(text[start:], "\n") {
		return operation.Fail(
			operation.ExitFailed,
			"native",
			"A provisioning script has no shebang line",
		)
	}
	end := start + strings.Index(text[start:], "\n")
	note := "\n# workbench: skipped " + strings.Join(skipped, ", ")
	return os.WriteFile(path, []byte(text[:end]+note+text[end:]), info.Mode().Perm())
}

// checkedUpdates keeps the Homebrew updates whose update-<name> effect is
// checked.
func (p *preparation) checkedUpdates(names []string) []string {
	var kept []string
	for _, name := range names {
		for _, effect := range p.Plan.Effects {
			if effect.Name == updateEffectPrefix+name && effect.Checked {
				kept = append(kept, name)
			}
		}
	}
	return kept
}
