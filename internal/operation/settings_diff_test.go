package operation

import (
	"bytes"
	"testing"
)

// The plan binds the exact before and after image of every file, and the before
// image is what revert restores. Risk: describing a commented settings file's
// changes blanked the comments in the bytes it was given (hujson standardizes in
// place), which made apply refuse every VS Code settings.json with a comment and
// would have left a comment-less file as the recovery copy.
func TestSettingsDiffLeavesImagesIntact(t *testing.T) {
	before := []byte("// mine\n{\n  \"a\": 1, // kept\n}\n")
	after := []byte("// mine\n{\n  \"a\": 2, // kept\n}\n")
	wasBefore, wasAfter := bytes.Clone(before), bytes.Clone(after)
	if _, _, ok := SettingsDiff("json", before, after); !ok {
		t.Fatal("a commented settings file did not parse")
	}
	if !bytes.Equal(before, wasBefore) || !bytes.Equal(after, wasAfter) {
		t.Fatal("describing the change altered the file images it was given")
	}
}
