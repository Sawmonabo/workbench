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
// last connection to close removes the two side files.
func TestHistoryFilesArePrivate(t *testing.T) {
	root := t.TempDir()
	history, err := OpenHistory(root, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = history.Close() }()
	ended := time.Now()
	if err := history.Insert(t.Context(), Finished{
		CommandView: CommandView{
			ID:      "toolu_private",
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
	for _, name := range []string{"history.db", "history.db-wal", "history.db-shm"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode&0o077 != 0 {
			t.Errorf("%s has mode %v; only its owner may read it", name, mode)
		}
	}
}
