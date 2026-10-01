package machine

import (
	"context"
	"encoding/json"
	"errors"
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
// hold the plan.
const probeTimeout = 5 * time.Second

// probeLine is one probe output line: "NAME: TEXT" for a change, and
// "NAME: = TEXT" when the effect has nothing to do. The "= " marker is the only
// signal; nothing matches on the words of TEXT.
var probeLine = regexp.MustCompile(`^([a-z0-9-]+): (= )?(.+)$`)

// probeResult is what the probe lines of one effect said: their text joined
// with "; ", and whether every line reported no change.
type probeResult struct {
	text     string
	noChange bool
}

// merge adds another result for the same effect.
func (r probeResult) merge(other probeResult) probeResult {
	return probeResult{r.text + "; " + other.text, r.noChange && other.noChange}
}

// probeEffects fills each effect's Delta from the active scripts run with
// WORKBENCH_PROBE=1, in parallel. A probe that fails, times out or prints
// anything but effect lines leaves its effects marked failed; the plan never
// blocks on a probe, and an unprobed effect stays checked. Probes write
// nothing: Go telemetry and Node's compile cache are switched off. It returns
// the context's error when the run was interrupted, so the plan stops instead
// of showing every effect unprobed.
func (p *preparation) probeEffects(ctx context.Context, c operation.Context) error {
	// Nothing here touches the plan digest: a probe failure shows as
	// "unprobed" on the effect, never as a warning the digest would cover.
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
	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		results = map[string][]probeResult{}
		outcome = map[string]string{}
	)
	for name, contents := range scripts {
		if !p.active[name] {
			continue
		}
		wg.Add(1)
		go func(name, contents string) {
			defer wg.Done()
			lines, status := p.runProbe(ctx, c, directory, environment, name, contents)
			mu.Lock()
			defer mu.Unlock()
			for effect, result := range lines {
				results[effect] = append(results[effect], result)
			}
			for _, effect := range scriptEffects(name) {
				if outcome[effect] != "failed" && outcome[effect] != "timeout" {
					outcome[effect] = status
				}
			}
		}(name, contents)
	}
	wg.Wait()
	if err = ctx.Err(); err != nil {
		return err
	}
	for i := range p.Plan.Effects {
		effect := &p.Plan.Effects[i]
		status, probed := outcome[effect.Name]
		if !probed {
			continue
		}
		effect.Probe = status
		if found := results[effect.Name]; len(found) > 0 {
			texts, noChange := make([]string, 0, len(found)), true
			for _, result := range found {
				texts = append(texts, result.text)
				noChange = noChange && result.noChange
			}
			slices.Sort(texts)
			effect.Delta = strings.Join(texts, "; ")
			effect.NoChange = noChange && status == "ok"
		}
	}
	return nil
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
		var problem *operation.Error
		if errors.As(err, &problem) && problem.Category == "timeout" {
			return nil, "timeout"
		}
		return nil, "failed"
	}
	lines := map[string]probeResult{}
	for line := range strings.SplitSeq(strings.TrimSpace(output.Stdout), "\n") {
		match := probeLine.FindStringSubmatch(line)
		if match == nil {
			return nil, "failed"
		}
		next := probeResult{text: match[3], noChange: match[2] != ""}
		if previous, ok := lines[match[1]]; ok {
			next = previous.merge(next)
		}
		lines[match[1]] = next
	}
	if len(lines) == 0 {
		return nil, "failed"
	}
	return lines, "ok"
}
