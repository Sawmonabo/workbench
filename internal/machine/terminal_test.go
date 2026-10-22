package machine

import (
	"bytes"
	"testing"
)

// The default-distro step writes the filter's output over the owner's Windows
// Terminal settings.json, which holds every profile, key binding and comment
// they have made. It keeps a copy beside it, but a change beyond the one key
// would go unnoticed until that copy is gone. Risk: a silent change to the
// owner's profiles and key bindings. Only defaultProfile may change, a file
// that already opens the profile must come back byte for byte, and text the
// filter cannot read must be refused rather than rewritten.
func TestTerminalDefaultChangesOnlyThatKey(t *testing.T) {
	const alma = "{fd5d9950-90aa-5dde-b1ed-c42625f1a0f0}"
	existing := []byte("\xef\xbb\xbf// mine\r\n{\r\n" +
		"    \"defaultProfile\": \"{61c54bbd-c2c6-5271-96e7-009a87ff44bf}\", // PowerShell\r\n" +
		"    /* kept */ \"profiles\": { \"list\": [ { \"guid\": \"" + alma + "\", \"name\": \"AlmaLinux-10\" }, ] },\r\n" +
		"}\r\n")
	edited, err := SetTerminalDefault(existing, alma, "")
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Replace(
		existing,
		[]byte("{61c54bbd-c2c6-5271-96e7-009a87ff44bf}"),
		[]byte(alma),
		1,
	)
	if !bytes.Equal(edited, want) {
		t.Fatalf("the edit changed more than defaultProfile:\n%q", edited)
	}
	again, err := SetTerminalDefault(edited, alma, "")
	if err != nil || !bytes.Equal(again, edited) {
		t.Fatalf("a file that already opens the profile changed: %v\n%q", err, again)
	}
	for _, bad := range []string{`{"a": 1, "a": 2}`, `{"a": `, `[1]`, " \r\n"} {
		if out, err := SetTerminalDefault([]byte(bad), alma, ""); err == nil {
			t.Fatalf("%q was not refused: %q", bad, out)
		}
	}
}
