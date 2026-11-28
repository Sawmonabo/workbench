package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// Unauthorized execution: a fix line is a command a person pastes into a
// shell, and a path in it can be any name on disk, such as a folder named
// $(rm -rf ~). The path is quoted (operation's shellWord), and in the live
// checklist and the guard view a fix line too wide for its line is broken
// inside that quoted word (cutCommandWord). A break that left a quote open or
// closed it early would hand the rest of the name to the shell as code. So
// each name below, drawn at widths that cut it, must paste as the one
// argument it is and run nothing it holds.
func TestWrappedFixRunsNothingInAPath(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash to paste the fix lines into")
	}
	dir := t.TempDir()
	ran := filepath.Join(dir, "ran")
	names := []string{
		"$(touch " + ran + ")",
		"`touch " + ran + "`",
		"it's; touch " + ran + "; echo '",
		`a"; touch ` + ran + " #",
		"tab\there; $(touch " + ran + ")",
		"a\\ b \\'$(touch " + ran + ")",
		strings.Repeat("it's ", 12) + "$(touch " + ran + ")",
	}
	c := operation.Context{Home: filepath.Join(dir, "home")}
	const lead = "    fix  "
	for _, name := range names {
		path := filepath.Join(dir, "a folder", "with long names", name, "end")
		command := `printf '<%s>\n' ` + c.ShellPath(path)
		for width := 16; width <= 120; width += 3 {
			lines := fixCommandLines(lead, command, width, breakLong)
			pasted := strings.Join(
				append([]string{strings.TrimPrefix(lines[0], lead)}, lines[1:]...),
				"\n",
			)
			out, err := exec.Command(bash, "--norc", "--noprofile", "-c", pasted).CombinedOutput()
			if _, statErr := os.Lstat(ran); statErr == nil {
				t.Fatalf(
					"pasting the fix drawn at %d columns ran a command from the path:\n%s",
					width,
					pasted,
				)
			}
			if err != nil || string(out) != "<"+path+">\n" {
				t.Fatalf("pasting the fix drawn at %d columns gave %q (%v), want the path %q:\n%s",
					width, out, err, path, pasted)
			}
		}
	}
}
