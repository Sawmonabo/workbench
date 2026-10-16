package machine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// probeTimeout bounds one script's probe; a slow version lookup must not
// hold the plan. It is generous because the first call from WSL to Windows
// after a quiet spell can take several seconds while Windows wakes up.
const probeTimeout = 15 * time.Second

// probeLine is one probe output line: "NAME: TEXT" for a change, "NAME: = TEXT"
// when the effect has nothing to do and "NAME: ? TEXT" when the script could
// not check, TEXT then saying why in plain words. The marker is the only
// signal; nothing matches on the words of TEXT.
var probeLine = regexp.MustCompile(`^([a-z0-9-]+): ([=?] )?(.+)$`)

// probeResult is what the probe lines of one effect said, kept apart by kind:
// the text of the lines that answered and of those that could not check, each
// joined with "; ", whether every line reported no change, and how many lines
// were changes and how many could not check. Keeping them apart lets a step
// show what was checked beside what was not, even when one script printed
// both.
type probeResult struct {
	answered, unanswered string
	noChange             bool
	changes, checks      int
}

// merge adds another result for the same effect.
func (r probeResult) merge(other probeResult) probeResult {
	return probeResult{
		joinProbeText(r.answered, other.answered),
		joinProbeText(r.unanswered, other.unanswered),
		r.noChange && other.noChange,
		r.changes + other.changes,
		r.checks + other.checks,
	}
}

// joinProbeText joins two texts of one kind with "; ", skipping an empty one.
func joinProbeText(a, b string) string {
	if a == "" || b == "" {
		return a + b
	}
	return a + "; " + b
}

// migrationsScript carries the migrations step. Its probe runs alone, before
// the others, under its own planning steps, so an apply says that it checks
// migrations and what it found.
const migrationsScript = "05-migrations"

// probeTally gathers the probe results of every script, from the parallel
// probes as well as the migrations probe.
type probeTally struct {
	mu      sync.Mutex
	results map[string][]probeResult
	outcome map[string]probeOutcome
}

// record keeps one script's lines and how its probe ended, for each effect
// the script carries. A failed or timed-out probe is never overwritten by
// another script's answer for the same effect.
func (t *probeTally) record(name string, lines map[string]probeResult, status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for effect, result := range lines {
		t.results[effect] = append(t.results[effect], result)
	}
	for _, effect := range scriptEffects(name) {
		if previous := t.outcome[effect].status; previous != "failed" &&
			previous != "timeout" {
			t.outcome[effect] = probeOutcome{status, probeNote(name, status)}
		}
	}
}

// probeEffects fills each effect's Delta from the active scripts run with
// WORKBENCH_PROBE=1: the migrations script's alone first, then the others in
// parallel. A probe that fails, times out or prints anything but effect lines
// leaves its effects with a ProbeNote saying, in plain words, what could not
// be checked; the plan never blocks on a probe, and an unprobed effect stays
// checked. Probes write nothing: Go telemetry and Node's compile cache are
// switched off. It returns the context's error when the run was interrupted,
// so the plan stops instead of showing every effect unprobed.
func (p *preparation) probeEffects(ctx context.Context, c operation.Context) error {
	// Nothing here touches the plan digest: a probe failure shows as a note
	// on the effect, never as a warning the digest would cover.
	scripts, err := p.scriptSources(ctx, c)
	if err != nil {
		return ctx.Err()
	}
	directory := filepath.Join(p.scratch, "probe")
	if err = os.Mkdir(directory, 0o700); err != nil {
		return ctx.Err()
	}
	environment := scriptEnvironment(c, p.Plan.Dependencies)
	environment = append(
		environment,
		"WORKBENCH_PROBE=1",
		"GOTELEMETRY=off",
		"NODE_DISABLE_COMPILE_CACHE=1",
	)
	for i, value := range environment {
		if rest, ok := strings.CutPrefix(value, "PATH="); ok {
			environment[i] = "PATH=" + filepath.Join(p.scratch, "bin") + ":" + rest
		}
	}
	tally := &probeTally{results: map[string][]probeResult{}, outcome: map[string]probeOutcome{}}
	p.probeMigrations(ctx, c, directory, environment, scripts, tally)
	c.Step("checking what each step would do here")
	var wg sync.WaitGroup
	for name, contents := range scripts {
		if !p.active[name] || name == migrationsScript {
			continue
		}
		wg.Add(1)
		go func(name, contents string) {
			defer wg.Done()
			lines, status := p.runProbe(ctx, c, directory, environment, name, contents)
			tally.record(name, lines, status)
		}(name, contents)
	}
	wg.Wait()
	results, outcome := tally.results, tally.outcome
	if err = ctx.Err(); err != nil {
		return err
	}
	for i := range p.Plan.Effects {
		effect := &p.Plan.Effects[i]
		result, probed := outcome[effect.Name]
		if !probed {
			continue
		}
		status := result.status
		effect.Probe, effect.ProbeNote = status, result.note
		found := results[effect.Name]
		if status != "ok" {
			// A script that did not answer leaves its effect unchecked even
			// when another script that carries the effect did: half an answer
			// would read as a change, or as nothing to change.
			continue
		}
		fillProbed(effect, found)
	}
	return nil
}

// fillProbed sets an effect from what every script that carries it answered:
// what the probes checked is its Delta and what they could not check its
// ProbeNote, side by side, so a step whose scripts printed both shows both.
func fillProbed(effect *operation.Effect, found []probeResult) {
	texts, noChange, why := make([]string, 0, len(found)), len(found) > 0, []string{}
	for _, result := range found {
		if result.answered != "" {
			texts = append(texts, result.answered)
		}
		if result.unanswered != "" {
			why = append(why, result.unanswered)
		}
		noChange = noChange && result.noChange
	}
	slices.Sort(texts)
	effect.Delta = strings.Join(texts, "; ")
	if len(why) > 0 {
		slices.Sort(why)
		effect.Probe = "unanswered"
		effect.ProbeNote = strings.Join(slices.Compact(why), "; ") + "; checked again when applied"
		return
	}
	effect.NoChange = noChange
}

// probeMigrations runs the migrations script's probe alone under its own
// planning steps: "checking migrations", then what it found.
func (p *preparation) probeMigrations(
	ctx context.Context,
	c operation.Context,
	directory string,
	environment []string,
	scripts map[string]string,
	tally *probeTally,
) {
	contents, ok := scripts[migrationsScript]
	if !ok || !p.active[migrationsScript] {
		return
	}
	c.Step("checking migrations")
	lines, status := p.runProbe(ctx, c, directory, environment, migrationsScript, contents)
	tally.record(migrationsScript, lines, status)
	result, found := lines["migrations"]
	c.Step(migrationsOutcome(result, found, status))
}

// migrationsOutcome is the planning step that says what the migrations probe
// found: how many migrations the apply would run and how many need the owner
// (a conflict, or one the installed workbench doesn't know), none to run, or
// that the probe itself failed or timed out.
func migrationsOutcome(result probeResult, found bool, status string) string {
	if status != "ok" || !found {
		return "migrations: couldn't check"
	}
	var parts []string
	if result.changes > 0 {
		parts = append(parts, fmt.Sprintf("%d to run", result.changes))
	}
	if result.checks == 1 {
		parts = append(parts, "1 needs attention")
	} else if result.checks > 1 {
		parts = append(parts, fmt.Sprintf("%d need attention", result.checks))
	}
	if len(parts) == 0 {
		return "migrations: none to run"
	}
	return "migrations: " + strings.Join(parts, ", ")
}

// probeOutcome is how one effect's probe ended: the status for --json and the
// plain-words note the plan shows when the probe could not answer.
type probeOutcome struct{ status, note string }

// probeNote says, in plain words, what a probe that did not answer left
// unchecked. It is empty for a probe that answered. A script that talks to
// Windows has "windows" in its name, and those are the ones that can be slow
// on a cold start.
func probeNote(script, status string) string {
	windows := strings.Contains(script, "windows")
	switch {
	case status == "timeout" && windows:
		return "Windows didn't answer in time; checked again when applied"
	case status == "timeout":
		return "Didn't answer in time; checked again when applied"
	case status == "failed" && windows:
		return "Couldn't check the Windows side; checked again when applied"
	case status == "failed":
		return "Couldn't check what this would do here; checked again when applied"
	}
	return ""
}

// scriptSources renders every provisioning script native would consider,
// keyed by name without chezmoi's prefixes and .sh suffix.
func (p *preparation) scriptSources(
	ctx context.Context,
	c operation.Context,
) (map[string]string, error) {
	rendered, err := p.run(ctx, c, "dump", "--include=scripts", "--format=json")
	if err != nil {
		return nil, err
	}
	var entries map[string]struct {
		Type     string `json:"type"`
		Contents string `json:"contents"`
	}
	if json.Unmarshal([]byte(rendered), &entries) != nil {
		return nil, operation.Fail(
			operation.ExitFailed,
			"native",
			"Unexpected native script output",
		)
	}
	scripts := map[string]string{}
	for name, entry := range entries {
		if entry.Type == "script" {
			scripts[strings.TrimSuffix(filepath.Base(name), ".sh")] = entry.Contents
		}
	}
	return scripts, nil
}

// runProbe runs one rendered script in probe mode and parses its effect
// lines. The status is "ok" only for exit 0 with at least one effect line and
// nothing else on stdout.
func (p *preparation) runProbe(
	ctx context.Context,
	c operation.Context,
	directory string,
	environment []string,
	name, contents string,
) (map[string]probeResult, string) {
	path := filepath.Join(directory, name+".sh")
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		return nil, "failed"
	}
	output, err := operation.Run(ctx, c, nil, operation.Process{
		Executable:  "/bin/bash",
		Args:        []string{path},
		Directory:   directory,
		Environment: environment,
		Secrets:     p.secrets,
		Timeout:     probeTimeout,
		OutputLimit: 64 << 10,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ""
		}
		// operation.Run reports expiry as its own Error in the "timeout"
		// category, not as a wrapped context error.
		if operation.Category(err) == "timeout" {
			return nil, "timeout"
		}
		return nil, "failed"
	}
	lines, ok := parseProbe(output.Stdout)
	if !ok {
		return nil, "failed"
	}
	return lines, "ok"
}

// parseProbe reads one script's probe output into a result per effect. It is
// false for anything but at least one effect line and nothing else.
func parseProbe(stdout string) (map[string]probeResult, bool) {
	lines := map[string]probeResult{}
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		match := probeLine.FindStringSubmatch(line)
		if match == nil {
			return nil, false
		}
		var next probeResult
		switch match[2] {
		case "":
			next.answered, next.changes = match[3], 1
		case "= ":
			next.answered, next.noChange = match[3], true
		case "? ":
			next.unanswered, next.checks = match[3], 1
		}
		if previous, ok := lines[match[1]]; ok {
			next = previous.merge(next)
		}
		lines[match[1]] = next
	}
	return lines, len(lines) > 0
}
