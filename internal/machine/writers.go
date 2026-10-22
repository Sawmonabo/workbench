package machine

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// writerRecord is the Windows record of which WSL distribution writes each
// file only one of them may write, and writerRecordNote what the steps' Undo
// says of it.
const (
	writerRecord     = `%LOCALAPPDATA%\Workbench\writers.json`
	writerRecordNote = writerRecord + " records which WSL distribution writes it; deleting that file is safe"
)

// writerSteps are the Windows-side steps whose file only one distribution
// writes: the one home/.chezmoitemplates/windows-writers.sh's record names. A
// step another installed distribution writes probes as "= written by NAME" and
// does nothing; its take-over-<step> effect moves it to this distribution.
var writerSteps = []string{
	"terminal-adoption", "powershell-adoption", "wsl-preferences", "default-distro",
}

// takeOverPrefix starts the name of a writer step's take-over effect.
const takeOverPrefix = "take-over-"

// windowsWriters is the plan input for the writer record: the SHA-256 of
// <LocalAppData>/Workbench/writers.json, "absent" without one, and "unknown"
// when Windows could not be asked or the file read. Each of the three calls
// has a minute, so a stalled /mnt/c never holds up the plan for longer. The
// scripts follow the record as they run, so an approved plan is refused (exit
// 4) once another distribution changed it. Its caller asks only on WSL, for
// the real home.
// cmd.exe gets what the scripts' Windows steps get from [scriptEnvironment]:
// a PATH of the system and Windows system folders, and the WSL variables
// interop needs.
func windowsWriters(ctx context.Context, c operation.Context) operation.Input {
	input := operation.Input{Name: "windows-writers", Digest: "unknown"}
	windows := windowsSystemDirectories(os.Getenv("PATH"))
	environment := []string{
		"PATH=" + strings.Join(append([]string{"/usr/bin", "/bin"}, windows...), ":"),
	}
	for _, name := range []string{"WSL_INTEROP", "WSL_DISTRO_NAME"} {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	cmd, err := operation.FindExecutable("cmd.exe", strings.Join(windows, ":"), nil)
	if err != nil {
		return input
	}
	answer, err := operation.Run(ctx, c, nil, operation.Process{
		Executable:  cmd,
		Args:        []string{"/c", "echo %LOCALAPPDATA%"},
		Directory:   filepath.Dir(cmd),
		Environment: environment,
		Timeout:     time.Minute,
		OutputLimit: 4 << 10,
	})
	local := strings.TrimSpace(answer.Stdout)
	if err != nil || local == "" || strings.HasPrefix(local, "%") {
		return input
	}
	// Run starts what a link points to, and wslpath is a link to WSL's /init,
	// which does what the name it was started as says: env starts it by name.
	converted, err := operation.Run(ctx, c, nil, operation.Process{
		Executable:  "/usr/bin/env",
		Args:        []string{"wslpath", "-u", local},
		Directory:   "/",
		Environment: environment,
		Timeout:     time.Minute,
		OutputLimit: 4 << 10,
	})
	folder := strings.TrimSpace(converted.Stdout)
	if err != nil || !filepath.IsAbs(folder) {
		return input
	}
	// A read on /mnt/c can stall like a Windows call, so cat reads the record
	// under the same cap. Its environment sets no locale, so cat names a
	// missing file in English.
	record, err := operation.Run(ctx, c, nil, operation.Process{
		Executable:  "/usr/bin/cat",
		Args:        []string{"--", filepath.Join(folder, "Workbench", "writers.json")},
		Directory:   "/",
		Environment: environment,
		Timeout:     time.Minute,
		OutputLimit: 64 << 10,
	})
	switch {
	case err == nil:
		input.Digest = operation.SHA256Hex([]byte(record.Stdout))
	case strings.HasSuffix(err.Error(), ": No such file or directory"):
		input.Digest = "absent"
	}
	return input
}

// takeOverEffects lists take-over-<step> for each writer step among probed
// whose probe said another installed distribution writes it. Ticking one runs
// the step from this distribution, which then records itself as the writer, so
// the effect is listed no more. probed is this plan's probed effects, or, for
// apply's recheck, which does not probe, those of the plan shown for approval.
func takeOverEffects(probed []operation.Effect) []operation.Effect {
	var effects []operation.Effect
	for _, step := range probed {
		distribution, ok := strings.CutPrefix(step.Delta, "written by ")
		if !ok || !step.NoChange || !slices.Contains(writerSteps, step.Name) {
			continue
		}
		effects = append(effects, operation.Effect{
			Name:     takeOverPrefix + step.Name,
			Optional: true,
			Description: "Run " + step.Name + " from this distribution in place of " +
				distribution + ", and record this distribution as its writer",
			Privilege: step.Privilege,
			Recovery:  step.Recovery,
			Title:     "Take over " + step.Title + " from " + distribution,
			Summary:   distribution + " writes it now; this distribution writes it from this apply on",
			What: "Runs the " + step.Title + " step from this distribution, although " +
				distribution + " is recorded as the one that writes it, and records this distribution instead. " +
				distribution + " then leaves it alone; moving it back is workbench apply --choose there.",
			Touches: step.Touches + ", and " + writerRecord,
			RunsAs:  step.RunsAs,
			Undo:    step.Undo,
		})
	}
	return effects
}
