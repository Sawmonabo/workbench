package costs

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const logKeep = 200 * 1024

// IngestOptions says what the worker was started for.
type IngestOptions struct {
	Event      string             // the hook event ("SessionStart", "SessionEnd"); "" for a manual run
	Transcript string             // the hook's transcript_path; ingest no longer reads it
	SessionID  string             // the hook's session_id: the root session whose binding the hook stored
	Progress   operation.Progress // optional: a step per transcript
}

// Ingest copies the usage of every recorded tool's transcripts into the
// ledger, one transaction per file, resuming from the stored offsets. Only one
// runs at a time: while another holds the lock this one logs that and returns.
// Every failure is also written to the log and, once the ledger is open, to
// its last_error note; committed files are not reprocessed next time.
func Ingest(ctx context.Context, opts IngestOptions) (summary string, err error) {
	paths, err := Locations()
	if err != nil {
		return "", err
	}
	release, held, err := operation.TryLock(paths.lock())
	if err != nil {
		return "", fmt.Errorf("cannot open state dir %s: %w", paths.State, err)
	}
	if held {
		writeLog(paths, "skip: another worker holds the lock")
		return "skipped: another worker holds the lock", nil
	}
	defer release()
	truncateLog(paths)
	summary, err = ingestLocked(ctx, paths, opts)
	if err != nil {
		writeLog(paths, "error: "+err.Error())
	}
	return summary, err
}

func ingestLocked(ctx context.Context, paths Paths, opts IngestOptions) (string, error) {
	started := time.Now()
	ledger, err := OpenLedger(paths.Ledger, true)
	if err != nil {
		return "", err
	}
	defer func() { _ = ledger.Close() }()
	var errs []string
	files, rows := 0, 0
	full, err := ledger.fullTierCheckDue()
	if err != nil {
		return "", err
	}
	run := NewRun()
	served := map[string]func(model, tier string) string{}
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		signIn := tool.Source.SignIn(paths.Home)
		var accounts map[string]string
		if directory, ok := tool.Source.(accountDirectory); ok {
			accounts = directory.Accounts(paths.Home)
		}
		if s, ok := tool.Source.(tierServer); ok {
			served[tool.Name] = s.ServedTier(paths.Home)
		}
		transcripts, listErr := tool.Source.Transcripts(paths.Home)
		if listErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", tool.Name, listErr))
		}
		stop := false
		readEach(ctx, ledger, tool, transcripts, func(i int, read fileRead) bool {
			if opts.Progress != nil {
				opts.Progress.Step(fmt.Sprintf(
					"%d/%d %s",
					i+1,
					len(transcripts),
					clipRunes(filepath.Base(filepath.Dir(read.path)), 40),
				))
			}
			err := read.err
			if err == nil {
				err = commitFile(ctx, ledger, run, tool, read, signIn, accounts, served[tool.Name])
			}
			switch {
			case err == nil && read.changed:
				files++
				rows += len(read.usage)
			case err != nil && errors.Is(err, errLedger):
				// The ledger itself is unusable (locked beyond the timeout, disk
				// full): stop here, files committed so far stay committed.
				errs = append(errs, fmt.Sprintf("%s: %v", read.path, err))
				stop = true
				return false
			case err != nil:
				errs = append(errs, fmt.Sprintf("%s: %v", read.path, err))
			}
			return true
		})
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if stop {
			break
		}
		if err := ledger.DropHeldCopies(ctx, tool.Name, holdsIn(ledger, transcripts)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: copies: %v", tool.Name, err))
		}
	}
	if full {
		run = nil
	}
	if _, err := ledger.ResolveTiers(ctx, run, served); err != nil {
		// Cancelled while pricing tiers: tiersUnresolved stays set, so the
		// next run checks every row; stop before the rates refresh.
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		errs = append(errs, "service tiers: "+err.Error())
	}
	rates := refreshIfNeeded(ctx, ledger, paths)
	event := opts.Event
	if event == "" {
		event = "manual"
	}
	summary := fmt.Sprintf(
		"%s: %d files, %d records in %.1fs; %s",
		event,
		files,
		rows,
		time.Since(started).Seconds(),
		rates,
	)
	return summary, finishIngest(ledger, paths, summary, errs)
}

// finishIngest stores a run's notes in the ledger and writes them to the log.
func finishIngest(ledger *Ledger, paths Paths, summary string, errs []string) error {
	for key, value := range map[string]string{
		"last_ingest_at":      stamp(time.Now()),
		"last_ingest_summary": summary,
		"last_error":          strings.Join(errs, "; "),
	} {
		if err := ledger.SetMeta(key, value); err != nil {
			return err
		}
	}
	line := summary
	if len(errs) > 0 {
		line += fmt.Sprintf("; %d errors", len(errs))
	}
	writeLog(paths, line)
	for _, e := range errs {
		writeLog(paths, "error: "+e)
	}
	return nil
}

// tierServer is a source whose tool may send a request at another tier than
// the one selected for it: ServedTier says which, for a model and a selected
// tier ("" for standard).
type tierServer interface {
	ServedTier(home string) func(model, tier string) string
}

// errLedger marks a failure of the ledger, as opposed to one transcript.
var errLedger = errors.New("ledger")

// fileRead is one transcript read, ready to commit.
type fileRead struct {
	path     string
	vanished bool // the file is gone: forget it
	changed  bool // new bytes were read
	usage    []Usage
	tiers    []TierChange
	row      FileRow
	err      error
}

// headBytes bounds the first-line digest: a first line longer than this is
// digested by its first headBytes bytes, which are written once.
const headBytes = 64 << 10

// readFile reads the new bytes of one transcript. It resumes at the stored
// offset with the source's stored state, unless the file shrank, went back in
// time or no longer starts with the line it started with (rewritten in place,
// as `codex migrate-rollouts` does, including a rewrite caught while its first
// line is still being written); then it reads from the start with empty
// state, and since every key is derived from content a re-read rewrites the
// same rows. It stops at the last complete line, so a transcript still being
// written is picked up next run from that offset. It gives up with ctx's error
// within about readUnit bytes of ctx being cancelled.
func readFile(ctx context.Context, ledger *Ledger, tool Tool, path string) fileRead {
	read := fileRead{path: path}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		read.vanished = true
		return read
	}
	if err != nil {
		read.err = err
		return read
	}
	previous, found, err := ledger.File(path)
	if err != nil {
		read.err = wrapLedger(err)
		return read
	}
	size, mtime := info.Size(), info.ModTime().UnixNano()
	if found && previous.Size == size && previous.ModTimeNanos == mtime {
		return read
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		read.vanished = true
		return read
	}
	if err != nil {
		read.err = err
		return read
	}
	defer func() { _ = file.Close() }()
	head, err := firstLineDigest(file)
	if err != nil {
		read.err = err
		return read
	}
	state := &FileState{Path: path}
	offset := int64(0)
	if found && size >= previous.Size && mtime >= previous.ModTimeNanos &&
		(previous.Head == "" || previous.Head == head) {
		offset, state.Saved = previous.Offset, previous.State
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		read.err = err
		return read
	}
	// A line is parsed in place in the reader's buffer; one longer than the
	// buffer is gathered into long. Parse keeps nothing of the line.
	reader := bufio.NewReaderSize(file, 1<<20)
	var (
		long      []byte
		unchecked int // bytes read since ctx was last checked
	)
	for {
		line, readErr := reader.ReadSlice('\n')
		if unchecked += len(line); unchecked >= readUnit {
			if read.err = ctx.Err(); read.err != nil {
				return read
			}
			unchecked = 0
		}
		if errors.Is(readErr, bufio.ErrBufferFull) {
			long = append(long, line...)
			continue
		}
		if len(long) > 0 {
			long = append(long, line...)
			line = long
		}
		if len(line) == 0 || line[len(line)-1] != '\n' {
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				read.err = readErr
				return read
			}
			break
		}
		offset += int64(len(line))
		read.usage = append(read.usage, tool.Source.Parse(bytes.TrimRight(line, "\r\n"), state)...)
		long = long[:0]
	}
	read.changed, read.tiers = true, state.Tiers
	read.row = FileRow{
		Offset:       offset,
		Size:         size,
		ModTimeNanos: mtime,
		State:        state.Saved,
		Head:         head,
	}
	return read
}

// firstLineDigest is the SHA-256 of the file's first line (its first
// headBytes bytes when longer), or "" while that line is still being written.
func firstLineDigest(file *os.File) (string, error) {
	buf := make([]byte, headBytes)
	n, err := file.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	buf = buf[:n]
	if i := bytes.IndexByte(buf, '\n'); i >= 0 {
		buf = buf[:i+1]
	} else if n < headBytes {
		return "", nil
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:]), nil
}

// commitFile stores one read in one transaction: its rows, its tier changes
// and where it stopped. A vanished file is forgotten. run is the run's record
// of committed writes.
func commitFile(
	ctx context.Context,
	ledger *Ledger,
	run *Run,
	tool Tool,
	read fileRead,
	signIn SignIn,
	accounts map[string]string,
	served func(model, tier string) string,
) error {
	if read.vanished {
		return wrapLedger(ledger.DeleteFile(read.path))
	}
	if !read.changed {
		return nil
	}
	return wrapLedger(ledger.Transaction(ctx, run, func(tx *Tx) error {
		for _, u := range read.usage {
			u.Tool = tool.Name
			attribute(&u, signIn, accounts)
			if base, tier := tool.SplitModel(u.Model); served != nil && tier != "" {
				u.Model = base
				if tier = served(base, tier); tier != "" {
					u.Model += "@" + tier
				}
			}
			if err := tx.Upsert(u); err != nil {
				return err
			}
		}
		for _, change := range read.tiers {
			if err := tx.AddTierChange(change); err != nil {
				return err
			}
		}
		return tx.SetFile(read.path, read.row)
	}))
}

// attribute gives a row read from a transcript the account and subscription
// it is stored with. A row whose transcript decided its subscription
// (EvidenceTranscript) takes the email of the sign-in that holds its
// AccountKey, "unknown" when none does; ResolveAccounts fills that in later.
// Any other row is stamped with the tool's current sign-in under
// EvidenceUnknown, which ResolveAccounts raises once a binding or observation
// names the session or time.
func attribute(u *Usage, signIn SignIn, accounts map[string]string) {
	if u.Evidence == EvidenceTranscript {
		u.Account = cmp.Or(accounts[u.AccountKey], "unknown")
		return
	}
	u.Account, u.Subscription, u.SubscriptionLabel = signIn.Account, signIn.Subscription, signIn.Label
	u.Evidence = EvidenceUnknown
}

// readAhead bounds how far reads run ahead of the commits: the size of the
// transcripts read or being read but not yet committed, counted in readUnit
// steps. A read keeps only its rows, a small part of its file, so this bounds
// memory. Counting bytes rather than files lets the reads run far enough ahead
// that a large transcript is started well before the commits reach it, instead
// of the commits waiting on it while the other readers sit idle.
const (
	readAhead = 1 << 30
	readUnit  = 1 << 20
)

// holdsIn reports, for a run that read paths, whether a transcript holds a
// thread's lines up to a time: it is listed this run, as every listed one is
// read in full, or the ledger read it after it was last written at or after
// that time.
func holdsIn(ledger *Ledger, paths []string) func(thread, since string) bool {
	return func(thread, since string) bool {
		if thread == "" {
			return false
		}
		for _, path := range paths {
			if strings.Contains(path, thread) {
				return true
			}
		}
		return ledger.readSince(thread, since)
	}
}

// readEach reads the transcripts on every CPU and hands each read to commit
// in list order, so files commit one at a time in the order a serial run
// would. commit returns false to stop.
func readEach(
	ctx context.Context,
	ledger *Ledger,
	tool Tool,
	paths []string,
	commit func(int, fileRead) bool,
) {
	workers := runtime.GOMAXPROCS(0)
	var wg sync.WaitGroup
	defer wg.Wait() // after cancel below: deferred calls run last-in first-out
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make([]chan fileRead, len(paths))
	for i := range results {
		results[i] = make(chan fileRead, 1)
	}
	// Each read takes a unit of the budget per readUnit of its file left to read, at least
	// one and at most all, before it starts, and returns them once committed.
	budget := make(chan struct{}, readAhead/readUnit)
	units := make([]int, len(paths))
	jobs := make(chan int)
	wg.Go(func() {
		defer close(jobs)
		for i, path := range paths {
			units[i] = 1
			if info, err := os.Stat(path); err == nil {
				left := info.Size() // what a read of this file goes through
				if previous, found, err := ledger.File(path); err == nil && found &&
					previous.Offset <= left {
					left -= previous.Offset
				}
				units[i] = int(min(max(left/readUnit, 1), readAhead/readUnit))
			}
			for range units[i] {
				select {
				case budget <- struct{}{}:
				case <-ctx.Done():
					return
				}
			}
			select {
			case jobs <- i:
			case <-ctx.Done():
				return
			}
		}
	})
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				read := fileRead{path: paths[i], err: ctx.Err()}
				if read.err == nil {
					read = readFile(ctx, ledger, tool, paths[i])
				}
				results[i] <- read
			}
		})
	}
	for i := range paths {
		var read fileRead
		select {
		case read = <-results[i]:
		case <-ctx.Done():
			return
		}
		for range units[i] {
			<-budget
		}
		if !commit(i, read) {
			return
		}
	}
}

func wrapLedger(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", errLedger, err)
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// writeLog appends one timestamped line to the worker log.
func writeLog(paths Paths, message string) {
	if os.MkdirAll(paths.State, 0o700) != nil {
		return
	}
	file, err := os.OpenFile(paths.log(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = fmt.Fprintf(file, "%s %s\n", stamp(time.Now()), message)
}

// truncateLog keeps the most recent 200 KB of the log, from a line start.
func truncateLog(paths Paths) {
	data, err := os.ReadFile(paths.log())
	if err != nil || len(data) <= logKeep {
		return
	}
	tail := data[len(data)-logKeep:]
	_ = os.WriteFile(paths.log(), tail[bytes.IndexByte(tail, '\n')+1:], 0o600)
}

// Hook is the hook entry. Claude Code and Codex pipe a JSON object
// (hook_event_name, transcript_path, session_id) on stdin; Hook starts
// `workbench costs ingest --worker` detached with those three values and
// returns. The hooks run synchronously (Claude Code stops a background hook
// that is still running when a headless session ends), which costs a session
// the few milliseconds of one process spawn. Hook prints nothing and never
// fails: a terminal, a closed or empty stdin, or anything unreadable just
// means no event, and the worker then sweeps everything. Reading stdin gives
// up after half a second.
func Hook(stdin *os.File) {
	defer func() { _ = recover() }()
	event, transcript, session := readHookInput(stdin)
	paths, err := Locations()
	if err != nil || os.MkdirAll(paths.State, 0o700) != nil {
		return
	}
	logFile, err := os.OpenFile(paths.log(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = logFile.Close() }()
	executable, err := os.Executable()
	if err != nil {
		return
	}
	// "--" keeps a value that starts with a dash from being read as a flag.
	_ = operation.StartDetached(
		executable,
		[]string{"costs", "ingest", "--worker", "--quiet", "--", event, transcript, session},
		logFile,
	)
}

func readHookInput(stdin *os.File) (event, transcript, session string) {
	if stdin == nil || operation.IsTerminal(stdin) {
		return "", "", ""
	}
	read := make(chan []byte, 1)
	go func() {
		data, _ := io.ReadAll(io.LimitReader(stdin, 1<<20))
		read <- data
	}()
	select {
	case data := <-read:
		var input struct {
			Event      string `json:"hook_event_name"`
			Transcript string `json:"transcript_path"`
			Session    string `json:"session_id"`
		}
		if json.Unmarshal(data, &input) != nil {
			return "", "", ""
		}
		return input.Event, input.Transcript, input.Session
	case <-time.After(500 * time.Millisecond):
		return "", "", ""
	}
}
