package machine

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// The no-prompt apply runs the saved selection without asking, so an effect the
// owner skipped must stay unchecked however it was decided: checking it would
// run provisioning (sudo, installers) the owner declined, with no prompt left
// to stop it.
func TestSavedSelectionNeverChecksASkippedEffect(t *testing.T) {
	saved := Selection{
		Skip:    []string{"runtimes"},
		Decided: []string{"runtimes", "global-tools"},
	}
	effects := applySelection(
		[]operation.Effect{{Name: "runtimes"}, {Name: "global-tools"}},
		saved,
		saved,
	)
	if effects[0].Checked {
		t.Fatal("a skipped effect was checked")
	}
	if !effects[1].Checked || effects[1].New {
		t.Fatal("a decided effect was not kept as chosen")
	}
}

// Native keeps a script that several effects share while any one of them is
// checked, so the script itself has to skip the sections of the ones that are
// not. Unticking Homebrew packages must not run the sudo Homebrew installer, and
// unticking Mac apps or Work tools must not install apps, the work extension or
// the Bitwarden CLI: with an approved plan there is no prompt left to stop them.
// The flag above is not enough, because the script is what runs. Every shared
// macOS and Linux script, with the shared templates it includes, must read each
// owner's switch with the default 1 that keeps a direct chezmoi run working.
func TestSharedScriptsGateEverySectionOnItsOwnEffect(t *testing.T) {
	home := filepath.Join("..", "..", "home")
	platforms := map[string][]operation.Effect{
		"darwin": append(slices.Clone(macOSEffects), workToolsEffect),
		"linux":  {linuxPackagesEffect, linuxEditorEffect, workToolsEffect},
	}
	include := regexp.MustCompile(`includeTemplate "\.chezmoitemplates/([^"]+)"`)
	shared := 0
	for platform, effects := range platforms {
		paths, err := filepath.Glob(
			filepath.Join(home, ".chezmoiscripts", platform, "run_*.sh.tmpl"),
		)
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range paths {
			base := filepath.Base(path)
			script := strings.TrimSuffix(base[strings.LastIndex(base, "_")+1:], ".sh.tmpl")
			var owners []string
			for _, effect := range effects {
				if slices.Contains(effectSources[effect.Name], script) {
					owners = append(owners, effect.Name)
				}
			}
			if len(owners) < 2 {
				continue
			}
			shared++
			text, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			code := string(text)
			for _, match := range include.FindAllStringSubmatch(string(text), -1) {
				partial, err := os.ReadFile(filepath.Join(home, ".chezmoitemplates", match[1]))
				if err != nil {
					t.Fatal(err)
				}
				code += "\n" + string(partial)
			}
			// Only code counts: a comment naming the switch gates nothing.
			var lines []string
			for line := range strings.SplitSeq(code, "\n") {
				if !strings.HasPrefix(strings.TrimSpace(line), "#") {
					lines = append(lines, line)
				}
			}
			code = strings.Join(lines, "\n")
			for _, owner := range owners {
				if !strings.Contains(code, "${"+effectVariable(owner)+":-1}") {
					t.Errorf(
						"%s runs a section of %s without checking %s",
						base,
						owner,
						effectVariable(owner),
					)
				}
			}
		}
	}
	if shared == 0 {
		t.Fatal("found no shared script to check")
	}
}

// Windows setup is on by default, and three of its parts replace the owner's own
// files and settings outside any checkpoint: the Windows Terminal settings, the
// PowerShell profile and the font registry. If ticking Windows setup ticked
// them, a first apply on a WSL host, or --yes on a machine that never decided,
// would overwrite files the owner never picked. A part runs only when its own
// name is selected, and only that choice is saved.
func TestWindowsSetupNeverTicksItsParts(t *testing.T) {
	windows := func() []operation.Effect {
		return []operation.Effect{
			{Name: "windows-files"},
			{Name: "terminal-adoption", Parent: "windows-files", Optional: true},
			{Name: "powershell-adoption", Parent: "windows-files", Optional: true},
			{Name: "font-registry", Parent: "windows-files", Optional: true},
		}
	}
	for _, test := range []struct {
		chosen  Selection
		checked []string
	}{
		{Selection{}, []string{"windows-files"}},
		{Selection{Select: []string{"font-registry"}}, []string{"windows-files", "font-registry"}},
	} {
		var checked []string
		for _, effect := range applySelection(windows(), test.chosen, test.chosen) {
			if effect.Checked {
				checked = append(checked, effect.Name)
			}
		}
		if !slices.Equal(checked, test.checked) {
			t.Fatalf(
				"with %v selected, %v are checked, want %v",
				test.chosen.Select,
				checked,
				test.checked,
			)
		}
		saved := selectionToSave(
			applySelection(windows(), test.chosen, test.chosen),
			test.chosen,
			test.chosen,
		)
		if !slices.Equal(saved.Select, test.chosen.Select) {
			t.Fatalf(
				"saved selection %v, want only what the owner chose: %v",
				saved.Select,
				test.chosen.Select,
			)
		}
	}
}

// A step the probe found nothing to change for sits on the Already set line,
// where the owner cannot turn it off. Recording it as decided would let the
// release that later gives it work (say, a sudo step) run it with no row ever
// shown to untick, so it must stay undecided until it has something to do.
func TestStepNeverShownWithWorkIsNotRecordedAsDecided(t *testing.T) {
	effects := []operation.Effect{
		{Name: "sysctl", NoChange: true, Checked: true},
		{Name: "runtimes", Checked: true},
	}
	saved := selectionToSave(effects, Selection{}, Selection{})
	if !slices.Equal(saved.Decided, []string{"runtimes"}) {
		t.Fatalf("decided %v, want only the step the owner could see work for", saved.Decided)
	}
}
