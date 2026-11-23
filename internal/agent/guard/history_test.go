package guard

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestHistoryFilesArePrivate guards against credential exposure: a command's
// text may hold a token, so no other user may read the history, nor the WAL
// and shared-memory files SQLite makes beside it, which hold the same rows.
// It checks them after the first write, while the store is open, since the
// last connection to close removes the two side files, and again once a
// history left readable by others (a copy, or a mode another tool set) is
// opened, by the view and then by the writer: SQLite makes the side files
// with the history's own mode.
func TestHistoryFilesArePrivate(t *testing.T) {
	root := t.TempDir()
	for _, write := range []bool{true, false, true} {
		history, err := OpenHistory(root, write)
		if err != nil {
			t.Fatal(err)
		}
		if write {
			ended := time.Now()
			if err := history.Insert(t.Context(), Finished{
				CommandView: CommandView{
					ID:      "toolu_private_" + ended.Format(time.RFC3339Nano),
					Agent:   "claude",
					Command: "curl -H 'Authorization: Bearer synthetic-token' https://example.invalid",
					State:   stateDone,
					Started: ended.Add(-time.Minute),
					Ended:   ended,
					Peak:    1 << 20,
				},
				Graph:     []uint64{1 << 20},
				Processes: []ProcessPeak{{PID: 1234, Name: "curl", Peak: 1 << 20}},
			}); err != nil {
				t.Fatal(err)
			}
		} else if _, _, err := history.Count(t.Context()); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"history.db", "history.db-wal", "history.db-shm"} {
			info, err := os.Stat(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			if mode := info.Mode().Perm(); mode&0o077 != 0 {
				t.Errorf("%s has mode %v; only its owner may read it", name, mode)
			}
		}
		if err := history.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(root, "history.db"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
