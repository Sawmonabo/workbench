package costs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Sawmonabo/workbench/internal/operation"
)

const logKeep = 200 * 1024

// IngestOptions says what the worker was started for.
type IngestOptions struct {
	Event      string             // the hook event ("SessionStart", "SessionEnd"); "" for a manual run
	Transcript string             // the hook's transcript_path, whose session's rows are tagged "session"
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
tools:
	for _, tool := range Tools {
		if tool.Source == nil {
			continue
		}
		account := tool.Source.Account(paths.Home)
		transcripts, listErr := tool.Source.Transcripts(paths.Home)
		if listErr != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", tool.Name, listErr))
		}
		for i, path := range transcripts {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			if opts.Progress != nil {
				opts.Progress.Step(fmt.Sprintf(
					"%d/%d %s", i+1, len(transcripts), clipRunes(filepath.Base(filepath.Dir(path)), 40),
				))
			}
			source := "sweep"
			if opts.Event == "SessionEnd" && tool.Source.Session(path, opts.Transcript) {
				source = "session"
			}
			n, changed, fileErr := ingestFile(ctx, ledger, tool, path, account, source)
			switch {
			case fileErr == nil && changed:
				files++
				rows += n
			case fileErr != nil && errors.Is(fileErr, errLedger):
				// The ledger itself is unusable (locked beyond the timeout, disk
				// full): stop here, files committed so far stay committed.
				errs = append(errs, fmt.Sprintf("%s: %v", path, fileErr))
				break tools
			case fileErr != nil:
				errs = append(errs, fmt.Sprintf("%s: %v", path, fileErr))
			}
		}
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
	for key, value := range map[string]string{
		"last_ingest_at":      stamp(time.Now()),
		"last_ingest_summary": summary,
		"last_error":          strings.Join(errs, "; "),
	} {
		if err := ledger.SetMeta(key, value); err != nil {
			return summary, err
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
	return summary, nil
}

// errLedger marks a failure of the ledger, as opposed to one transcript.
var errLedger = errors.New("ledger")

// ingestFile ingests the new bytes of one transcript in one transaction. It
// reports changed false when the file was unchanged or has vanished. It stops
// at the last complete line, so a transcript still being written is picked up
// next run from that offset.
func ingestFile(
	ctx context.Context,
	ledger *Ledger,
	tool Tool,
	path, account, source string,
) (rows int, changed bool, err error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, wrapLedger(ledger.DeleteFile(path))
	}
	if err != nil {
		return 0, false, err
	}
	previous, found, err := ledger.File(path)
	if err != nil {
		return 0, false, wrapLedger(err)
	}
	size, mtime := info.Size(), info.ModTime().UnixNano()
	offset := int64(0)
	if found {
		if previous.Size == size && previous.ModTimeNanos == mtime {
			return 0, false, nil
		}
		if size >= previous.Size && mtime >= previous.ModTimeNanos {
			offset = previous.Offset
		}
	}
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, wrapLedger(ledger.DeleteFile(path))
	}
	if err != nil {
		return 0, false, err
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 0, false, err
	}
	state := &FileState{Path: path}
	var usage []Usage
	reader := bufio.NewReaderSize(file, 1<<20)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 || line[len(line)-1] != '\n' {
			if readErr != nil && !errors.Is(readErr, io.EOF) {
				return 0, false, readErr
			}
			break
		}
		offset += int64(len(line))
		usage = append(usage, tool.Source.Parse(bytes.TrimRight(line, "\r\n"), state)...)
	}
	err = ledger.Transaction(ctx, func(tx *Tx) error {
		for _, u := range usage {
			u.Tool, u.Account = tool.Name, account
			if err := tx.Upsert(u, source); err != nil {
				return err
			}
		}
		return tx.SetFile(path, FileRow{Offset: offset, Size: size, ModTimeNanos: mtime})
	})
	if err != nil {
		return 0, false, wrapLedger(err)
	}
	return len(usage), true, nil
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

// Hook is the hook entry. Claude Code pipes a JSON object (hook_event_name,
// transcript_path) on stdin; Hook starts `workbench costs ingest --worker`
// detached with those two values and returns. It prints nothing and never
// fails: a terminal, a closed or empty stdin, or anything unreadable just
// means no event, and the worker then sweeps everything. Reading stdin gives
// up after half a second.
func Hook(stdin *os.File) {
	defer func() { _ = recover() }()
	event, transcript := readHookInput(stdin)
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
	_ = operation.StartDetached(
		executable,
		[]string{"costs", "ingest", "--worker", "--quiet", event, transcript},
		logFile,
	)
}

func readHookInput(stdin *os.File) (event, transcript string) {
	if stdin == nil || operation.IsTerminal(stdin) {
		return "", ""
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
		}
		if json.Unmarshal(data, &input) != nil {
			return "", ""
		}
		return input.Event, input.Transcript
	case <-time.After(500 * time.Millisecond):
		return "", ""
	}
}
