package costs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const logLimit = 400 * 1024

// IngestOptions says what the worker was started for.
type IngestOptions struct {
	Event      string             // the hook event ("SessionStart", "SessionEnd"); "" for a manual run
	Transcript string             // the hook's transcript_path: names the tool the hook fired for
	SessionID  string             // the hook's session_id: the root session the worker binds to the sign-in
	Progress   operation.Progress // optional: a step per transcript
	Wait       bool               // wait for a running ingest to finish instead of skipping
}

// Ingest copies the usage of every recorded tool's transcripts into the
// ledger, one transaction per file, resuming from the stored offsets. Only one
// runs at a time: while another holds the lock this one logs that and returns,
// or with Wait runs once the other is done.
// Every failure is also written to the log and, once the ledger is open, to
// its last_error note; committed files are not reprocessed next time.
//
// It first runs the costs gate (see gate), which moves the costs folders under
// agent/ and the costs files into Workbench's folders when they still wait in
// the old ones. When another process moved the costs folders while it read or
// wrote in them (see [Paths.moved]), it runs once more where they are now: a
// session binding or a log line it lost there fails without an error, and
// reading again only finishes what the first run left.
//
// A worker started by a hook first binds the hook's session to the tool's
// current sign-in, before it contends for the lock, so a worker that skips
// because another is running loses no binding.
func Ingest(ctx context.Context, opts IngestOptions) (summary string, err error) {
	paths, err := Locations()
	if err != nil {
		return "", err
	}
	summary, used, err := ingest(ctx, paths, opts)
	if used.moved() {
		summary, _, err = ingest(ctx, paths, opts)
	}
	return summary, err
}

// lockIn takes the import lock in the state folder the gate made, without
// making a folder: a costs folder moved away from its old path in between
// leaves a file there, which fails it (see [Paths.moved]). A lock it finds
// held it tries again for about 100 ms before it gives up (see
// [operation.TakeLockIn]): an entry asking whether an import runs (status,
// and any costs entry that finds an old costs folder beside the new one, see
// [LocationsIn]) takes a shared lock on a free one for an instant, and a
// worker that met that instant would otherwise skip its import, as if
// another worker held the lock, and leave the usage for a later hook.
func lockIn(paths Paths) (release func(), held bool, err error) {
	file, held, err := operation.TakeLockIn(paths.lock())
	if err != nil || held {
		return nil, held, err
	}
	return func() { _ = file.Close() }, false, nil
}

// ingest is one run of [Ingest], which returns the paths it worked with.
func ingest(ctx context.Context, paths Paths, opts IngestOptions) (string, Paths, error) {
	// The worker moves the costs files itself when they still wait in the
	// claude-costs folders; it must not open a new, empty ledger beside them.
	paths, err := gate(ctx, paths, true)
	if err != nil {
		writeLog(paths, "not read: "+err.Error())
		return "", paths, err
	}
	bindSession(ctx, paths, opts)
	release, held, err := lockIn(paths)
	for err == nil && held && opts.Wait {
		select {
		case <-ctx.Done():
			return "", paths, ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
		release, held, err = lockIn(paths)
	}
	if err != nil {
		err = fmt.Errorf("cannot take the import lock: %w", operation.FileError(paths.lock(), err))
		writeLog(paths, "error: "+err.Error())
		return "", paths, err
	}
	if held {
		writeLog(paths, "skip: another worker holds the lock")
		return "skipped: another worker holds the lock", paths, nil
	}
	defer release()
	if _, err := operation.RotateOutput(paths.output(), logLimit); err != nil {
		writeLog(paths, "error: cannot rotate the worker's output file: "+err.Error())
	}
	summary, err := ingestLocked(ctx, paths, opts)
	if err != nil {
		writeLog(paths, "error: "+err.Error())
	}
	return summary, paths, err
}

// bindSession stores the sign-in the hook's session runs under:
// session_accounts(tool, session, now, sign-in). It runs in the detached
// worker, before the ingest lock, so the hook process itself stays within
// Codex's 3 s SessionEnd cap and a worker that skips for the lock still leaves
// the binding. The tool comes from the transcript path; a failure is logged
// and the session's rows fall back to observed sign-ins.
func bindSession(ctx context.Context, paths Paths, opts IngestOptions) {
	if opts.SessionID == "" {
		return
	}
	tool, ok := toolOfTranscript(paths.Home, opts.Transcript)
	if !ok {
		writeLog(
			paths,
			"session not bound: no tool owns transcript path "+strconv.Quote(opts.Transcript),
		)
		return
	}
	// Read the sign-in before the ledger can make this process wait.
	signIn := tool.Source.SignIn(paths.Home)
	at := time.Now()
	ledger, err := paths.open(ctx, nil)
	if err == nil {
		err = ledger.BindSession(ctx, tool.Name, opts.SessionID, at, signIn)
		_ = ledger.Close()
	}
	if err != nil {
		writeLog(paths, "session not bound: "+err.Error())
	}
}

func ingestLocked(ctx context.Context, paths Paths, opts IngestOptions) (string, error) {
	started := time.Now()
	ledger, err := paths.open(ctx, upgradeSteps(opts.Progress))
	if err != nil {
		return "", err
	}
	defer func() { _ = ledger.Close() }()
	ledger.compactAgain(ctx, paths)
	var errs []string
	files, rows := 0, 0
	full, err := ledger.fullTierCheckDue(ctx)
	if err != nil {
		return "", err
	}
	run := newRun()
	served := map[string]func(model, tier string) string{}
	signIns := keptSignIns(paths.Home)
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		accounts := tool.Source.Accounts(paths.Home)
		var labels map[string]string
		if directory, ok := tool.Source.(labelDirectory); ok {
			labels = directory.Labels(paths.Home)
		}
		if err := rereadIfRevised(ctx, ledger, tool, paths.Home); err != nil {
			return "", err
		}
		if s, ok := tool.Source.(tierServer); ok {
			served[tool.Name] = s.ServedTier(paths.Home)
		}
		transcripts, listErr := tool.Source.Transcripts(paths.Home)
		if listErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", tool.Name, listErr))
		}
		stop := false
		// The transcripts this run read, or found unchanged since the ledger read
		// them, and committed: the only ones DropHeldCopies may trust to hold a
		// response. A listed transcript whose read failed holds nothing yet.
		var held []string
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
				err = commitFile(ctx, ledger, run, tool, read, accounts, labels, served[tool.Name])
			}
			switch {
			case err == nil && read.changed:
				files++
				rows += len(read.usage)
			case err != nil && errors.Is(err, errLedger):
				// The ledger itself is unusable (locked beyond the timeout, disk
				// full): stop here, files committed so far stay committed.
				errs = append(errs, operation.FileError(read.path, err).Error())
				stop = true
				return false
			case err != nil:
				errs = append(errs, operation.FileError(read.path, err).Error())
			}
			if err == nil && !read.vanished {
				held = append(held, read.path)
			}
			return true
		})
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if stop {
			break
		}
		if err := ledger.DropHeldCopies(ctx, tool.Name, holdsIn(ctx, ledger, held)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: copies: %v", tool.Name, err))
		}
	}
	if full {
		run = nil
	}
	resolved, err := resolveRun(ctx, ledger, paths.Home, run, signIns, served)
	if err != nil {
		return "", err
	}
	errs = append(errs, resolved...)
	rates := refreshIfNeeded(ctx, ledger, paths.Home)
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
	return summary, finishIngest(ctx, ledger, paths, summary, errs)
}

// keptSignIns are the sign-ins each tool keeps on disk now (signInDirectory),
// read before its transcripts so a sign-in that moves on during the run still
// names the rows it made.
func keptSignIns(home string) map[string][]SignIn {
	out := map[string][]SignIn{}
	for _, tool := range Tools {
		if directory, ok := tool.Source.(signInDirectory); ok {
			out[tool.Name] = directory.SignIns(home)
		}
	}
	return out
}

// resolveRun is what a run does once every transcript is committed: observe
// the sign-ins, then attribute rows to accounts and price their tiers. A
// cancelled run stops with ctx's error before the rates refresh and leaves the
// work marked for the next one; other failures come back as messages.
func resolveRun(
	ctx context.Context,
	ledger *Ledger,
	home string,
	run *Run,
	signIns map[string][]SignIn,
	served map[string]func(model, tier string) string,
) (errs []string, err error) {
	for _, step := range []struct {
		name string
		do   func() error
	}{
		{"sign-ins", func() error { return observeSignIns(ctx, ledger, home) }},
		{"accounts", func() error { return ledger.ResolveAccounts(ctx, run, signIns) }},
		{"service tiers", func() error { _, err := ledger.ResolveTiers(ctx, run, served); return err }},
	} {
		if err := step.do(); err != nil {
			// Cancelled mid-way: accounts_unresolved and tiers_unresolved stay
			// set, so the next run attributes and prices every row it left.
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			errs = append(errs, step.name+": "+err.Error())
		}
	}
	return errs, nil
}

// observeSignIns records every tool's current sign-in at this moment, after the
// run has read its transcripts, so every row it stored lies between this
// observation and an earlier run's. A tool with no transcripts is observed too.
// It also stores the account marks a tool keeps outside its transcripts
// (markSource): those it found when it couldn't read them all, whose error it
// then returns, so the run's notes name it.
func observeSignIns(ctx context.Context, ledger *Ledger, home string) error {
	now := time.Now()
	marks := map[string][]AccountMark{}
	var unread []error
	for _, tool := range Tools {
		if source, ok := tool.Source.(markSource); ok {
			found, err := source.Marks(ctx, home)
			marks[tool.Name] = found
			if err != nil {
				unread = append(unread, err)
			}
		}
	}
	err := ledger.Transaction(ctx, nil, func(tx *Tx) error {
		for _, tool := range Tools {
			if tool.Source == nil {
				continue
			}
			if err := tx.ObserveSignIn(tool.Name, now, tool.Source.SignIn(home)); err != nil {
				return err
			}
			for _, m := range marks[tool.Name] {
				if err := tx.AddMark(tool.Name, m); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return errors.Join(append([]error{err}, unread...)...)
}

// finishIngest stores a run's notes in the ledger and writes them to the log.
func finishIngest(
	ctx context.Context,
	ledger *Ledger,
	paths Paths,
	summary string,
	errs []string,
) error {
	for key, value := range map[string]string{
		"last_ingest_at":      stamp(time.Now()),
		"last_ingest_summary": summary,
		"last_error":          strings.Join(errs, "; "),
	} {
		if err := ledger.SetMeta(ctx, key, value); err != nil {
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

// errLedger marks a failure of the ledger, as opposed to one transcript.
var errLedger = errors.New("ledger")

// commitFile stores one read in one transaction: its rows, its tier changes,
// its thread links, its account marks and where it stopped. A vanished file is
// forgotten. run is the run's record of committed writes.
func commitFile(
	ctx context.Context,
	ledger *Ledger,
	run *Run,
	tool Tool,
	read fileRead,
	accounts, labels map[string]string,
	served func(model, tier string) string,
) error {
	if read.vanished {
		return wrapLedger(ledger.DeleteFile(ctx, read.path))
	}
	if !read.changed {
		return nil
	}
	return wrapLedger(ledger.Transaction(ctx, run, func(tx *Tx) error {
		for _, u := range read.usage {
			u.Tool = tool.Name
			attribute(&u, accounts, labels)
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
		for _, link := range read.parents {
			if err := tx.AddThreadParent(tool.Name, link); err != nil {
				return err
			}
		}
		for _, m := range read.marks {
			if err := tx.AddMark(tool.Name, m); err != nil {
				return err
			}
		}
		return tx.SetFile(read.path, read.row)
	}))
}

// attribute sets the account a row is stored with: the email of the sign-in
// that holds the AccountKey its transcript names (EvidenceTranscript),
// otherwise "unknown" with no evidence. The tool's current sign-in is not
// stamped on a row it has no evidence for: ResolveAccounts names an email only
// from a transcript's account id, a session binding or agreeing observations.
// The row's subscription is whatever its parser decided from the transcript,
// else "unknown".
func attribute(u *Usage, accounts, labels map[string]string) {
	if email := accounts[u.AccountKey]; u.AccountKey != "" && email != "" {
		u.Account, u.AccountEvidence = email, EvidenceTranscript
	} else {
		u.Account, u.AccountEvidence = "unknown", EvidenceUnknown
	}
	if u.SubscriptionEvidence != EvidenceTranscript {
		u.Subscription, u.SubscriptionLabel = "", ""
		u.SubscriptionEvidence = EvidenceUnknown
	} else if u.SubscriptionLabel == "" {
		u.SubscriptionLabel = labels[u.Subscription]
	}
}

// rereadIfRevised makes this run read every transcript of tool from the
// start when its parser's revision is not the one the ledger stored
// (reparser), and stores the new revision with that, in one transaction.
func rereadIfRevised(ctx context.Context, ledger *Ledger, tool Tool, home string) error {
	source, ok := tool.Source.(reparser)
	if !ok {
		return nil
	}
	return ledger.ForgetFiles(
		ctx, tool.Name, source.ParseRevision(), tool.Source.TranscriptRoots(home),
	)
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
// thread's lines up to a time: it was read and committed this run, in full, or
// the ledger read it after it was last written at or after that time.
func holdsIn(ctx context.Context, ledger *Ledger, paths []string) func(thread, since string) bool {
	return func(thread, since string) bool {
		if thread == "" {
			return false
		}
		for _, path := range paths {
			if strings.Contains(path, thread) {
				return true
			}
		}
		return ledger.readSince(ctx, thread, since)
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
				if previous, found, err := ledger.File(ctx, path); err == nil && found &&
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

// writeLog appends one timestamped line to the worker log, in the folder
// [Ingest] made or found, cut once it is over 400 KB (see [operation.AppendLog]).
func writeLog(paths Paths, message string) {
	operation.AppendLog(paths.log(), stamp(time.Now())+" "+message, logLimit)
}

// upgradeSteps shows an open's upgrade of the ledger (see [UpgradeSteps]) as
// steps of the import's progress, nil without progress.
func upgradeSteps(progress operation.Progress) UpgradeSteps {
	if progress == nil {
		return nil
	}
	return func(step string) {
		if step != "" && step != UpgradeWaiting {
			step = "upgrading the costs ledger, " + step
		}
		progress.Step(step)
	}
}
