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
// not. Unticking Homebrew packages must not run the sudo Homebrew installer,
// unticking Mac apps or Work tools must not install apps, the work extension or
// the Bitwarden CLI, and a Windows part left off must not replace Windows files:
// with an approved plan there is no prompt left to stop them. The flag above is
// not enough, because the script is what runs. Every shared script, with the
// shared templates it includes, must read each owner's switch: with the default
// 1 that keeps a direct chezmoi run working for the steps that are on by
// default, and 0 or empty for the optional ones.
func TestSharedScriptsGateEverySectionOnItsOwnEffect(t *testing.T) {
	home := filepath.Join("..", "..", "home")
	platforms := map[string][]operation.Effect{
		"darwin": append(slices.Clone(macOSEffects), workToolsEffect),
		"linux":  {linuxPackagesEffect, linuxEditorEffect, workToolsEffect},
		"wsl":    append(slices.Clone(windowsHostEffects), optionalEffects...),
	}
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
			owners := slices.DeleteFunc(slices.Clone(effects), func(effect operation.Effect) bool {
				return !slices.Contains(effectSources[effect.Name], script)
			})
			if len(owners) < 2 {
				continue
			}
			shared++
			code := scriptCode(t, home, path)
			for _, owner := range owners {
				fallback := "1"
				if slices.ContainsFunc(
					optionalEffects,
					func(e operation.Effect) bool { return e.Name == owner.Name },
				) {
					fallback = "0?"
				}
				gate := regexp.MustCompile(
					`\$\{` + effectVariable(owner.Name) + `:-` + fallback + `\}`,
				)
				if !gate.MatchString(code) {
					t.Errorf(
						"%s runs a section of %s without checking %s",
						base,
						owner.Name,
						effectVariable(owner.Name),
					)
				}
			}
		}
	}
	if shared == 0 {
		t.Fatal("found no shared script to check")
	}
}

// scriptCode is the code of the template at path and of the shared templates
// it includes, without comment lines: a comment naming a switch gates nothing.
func scriptCode(t *testing.T, home, path string) string {
	t.Helper()
	text, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	code := string(text)
	include := regexp.MustCompile(`includeTemplate "\.chezmoitemplates/([^"]+)"`)
	for _, match := range include.FindAllStringSubmatch(code, -1) {
		partial, err := os.ReadFile(filepath.Join(home, ".chezmoitemplates", match[1]))
		if err != nil {
			t.Fatal(err)
		}
		code += "\n" + string(partial)
	}
	var lines []string
	for line := range strings.SplitSeq(code, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "#") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// Windows setup and its three parts write to the Windows side outside any
// checkpoint, and the parts replace the owner's own files and settings: the
// Windows Terminal settings, the PowerShell profile and the font registry. If
// any of them were on by default, or ticking Windows setup ticked its parts, a
// first apply on a WSL host, or --yes on a machine that never decided, would
// overwrite files the owner never picked. Each runs only when its own name is
// selected, and only that choice is saved. The real definitions are used, so a
// step turned back on by default fails here.
func TestWindowsStepsRunOnlyWhenSelectedByName(t *testing.T) {
	windows := func() []operation.Effect {
		return append(slices.Clone(windowsHostEffects), optionalEffects...)
	}
	for _, test := range []struct {
		chosen  Selection
		checked []string
	}{
		{Selection{}, []string{"wsl-preferences"}},
		{
			Selection{Select: []string{"windows-files"}},
			[]string{"windows-files", "wsl-preferences"},
		},
		{
			Selection{Select: []string{"font-registry"}},
			[]string{"wsl-preferences", "font-registry"},
		},
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
