package machine

import (
	"strings"
	"testing"
)

// Unauthorized execution: update --local-build installs, without asking, the
// tools a checkout's versions.toml pins, and a coding agent may run it. The pin
// file is edited freely, and chezmoi and uv are executed once installed, so a
// pin that moved a download to another repository, host or uv option would run
// code nobody reviewed. Each of these pins must be refused before any download.
func TestPinsCannotRedirectToolDownloads(t *testing.T) {
	const pins = `[versions]
uv = "0.11.21"
python_pinned = ["3.12.12"]

[management]
chezmoi = "2.70.3"
tomlkit = "0.15.1"
tomlkit_url = "https://files.pythonhosted.org/packages/13/bc/tomlkit-0.15.1-py3-none-any.whl"
`
	read := func(old, replacement string) error {
		_, err := SourceRequirements(map[string][]byte{
			versionsFile: []byte(strings.Replace(pins, old, replacement, 1)),
		})
		return err
	}
	if err := read("", ""); err != nil {
		t.Fatalf("the reviewed pin shape was refused: %v", err)
	}
	for name, change := range map[string][2]string{
		"another repository": {`"2.70.3"`, `"2.70.3/../../../evil/repo/releases/download/v1"`},
		"a uv option":        {`["3.12.12"]`, `["--python-downloads-json-url=https://evil.example/x.json"]`},
		"another wheel host": {"files.pythonhosted.org", "evil.example"},
	} {
		if err := read(change[0], change[1]); err == nil {
			t.Errorf("a pin naming %s was accepted", name)
		}
	}
}
