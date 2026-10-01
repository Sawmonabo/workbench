package operation

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Prevent unapproved script execution: consent is for the selection that was
// shown. A plan approved with an effect checked must be refused once that
// effect's Checked flag differs at recheck, which is what a hand edit of
// machine.toml between dry run and --approve-plan produces.
func TestApprovalCoversEffectSelection(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c := Context{
		Paths: Paths{
			State:  filepath.Join(root, "state"),
			Config: filepath.Join(root, "config"),
			Data:   filepath.Join(root, "data"),
			Cache:  filepath.Join(root, "cache"),
			Bin:    filepath.Join(root, "bin"),
		},
		Scope: Scope{Kind: "project", Root: filepath.Join(root, "project")},
	}
	if err = os.Mkdir(c.Scope.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	shown := Plan{
		Scope:    c.Scope,
		Complete: true,
		Effects:  []Effect{{Name: "runtimes", Checked: true}},
	}
	recheck := shown
	recheck.Effects = []Effect{{Name: "runtimes", Checked: false}}
	if shown.Digest() == recheck.Digest() {
		t.Fatal("digest ignores Checked")
	}
	probed := shown
	probed.Effects = []Effect{{Name: "runtimes", Checked: true, Delta: "Node 22 → 26", Probe: "ok"}}
	if shown.Digest() != probed.Digest() {
		t.Fatal("digest covers probe output; a differing recheck probe would void every approval")
	}
	applied := false
	err = WithMutation(
		context.Background(),
		c,
		shown,
		Consent{ApprovedDigest: shown.Digest(), CompleteInputs: true},
		func(context.Context, Context) (Plan, error) { return recheck, nil },
		func(*Mutation) error { applied = true; return nil },
	)
	if applied || ExitCode(err) != ExitConflict {
		t.Fatalf("selection change applied=%v err=%v", applied, err)
	}
}
