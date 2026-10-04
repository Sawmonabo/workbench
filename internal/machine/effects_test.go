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

// Windows setup forces its parts on while it is checked. If that force were
// saved as the owner's choice, turning Windows setup off later would still
// replace the Terminal and PowerShell files the owner never picked on their own.
// The saved selection keeps only what the owner chose for each part.
func TestWindowsPartsForcedByTheirParentAreNotSavedAsChosen(t *testing.T) {
	chosen := Selection{Select: []string{"font-registry"}}
	effects := applySelection(
		[]operation.Effect{
			{Name: "windows-files"},
			{Name: "terminal-adoption", Parent: "windows-files", Optional: true},
			{Name: "font-registry", Parent: "windows-files", Optional: true},
		},
		chosen,
		chosen,
	)
	for _, effect := range effects {
		if !effect.Checked {
			t.Fatalf("%s is not included while Windows setup is on", effect.Name)
		}
	}
	saved := selectionToSave(effects, chosen, chosen)
	if !slices.Equal(saved.Select, []string{"font-registry"}) {
		t.Fatalf("saved selection %v, want only the part the owner chose", saved.Select)
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
