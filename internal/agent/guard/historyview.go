package guard

// This file is what the view and `agent guard history` need of the history
// beyond its store (history.go): where it and the copy of a damaged one are,
// the guard folder made for the command that sets the time kept, the time
// kept while none is set, and how much of a command a summary holds.

// HistoryKeepDefault is how long finished commands are kept while no time is
// set.
const HistoryKeepDefault = historyKeepDefault

// HistoryRoot is the guard folder where Root says, which holds the history.
// It creates nothing.
func HistoryRoot() (string, error) {
	state, err := stateFolder()
	if err != nil {
		return "", err
	}
	return Root(state), nil
}

// MakeHistoryRoot is HistoryRoot made private when it is missing, as the
// watcher makes it before it takes its lock (see folders.makeRoot), for the
// command that sets the time kept, which makes the history as the watcher's
// writer would (see OpenHistory). At its old path it makes nothing.
func MakeHistoryRoot() (string, error) {
	state, err := stateFolder()
	if err != nil {
		return "", err
	}
	f := locate(state, "history")
	return f.root, f.makeRoot()
}

// HistoryFile is the history's path in the guard folder root.
func HistoryFile(root string) string { return historyFile(root) }

// SummaryLength is how many characters of a command a [Summary] holds.
const SummaryLength = summaryLength

// HistoryDamagedFile is where the guard folder root keeps the copy of a
// history that could not be used (see [MoveDamaged]).
func HistoryDamagedFile(root string) string { return damagedFile(root) }
