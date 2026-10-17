package operation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

// Workbench's logs (the costs ingest log, the guard log) are appended to by
// many processes and cut in place, never replaced, because a child that
// [StartDetached] started keeps its stdout and stderr open on the file. Three
// rules keep every line [AppendLog] writes and let a reader follow the log:
//
//   - Every write opens the file with O_APPEND, so POSIX places it at the end
//     of the file with no intervening modification (write(2)), and with
//     O_NOFOLLOW, so a link put in the log's place is refused (open(2) on
//     Linux and macOS: ELOOP).
//   - [AppendLog] holds a shared flock(2) for its write and a cut holds an
//     exclusive one, so no appended line falls between the cut's read and its
//     truncate. The detached child's own output holds no lock; the cut copies
//     what it appended meanwhile, so only bytes written between that copy and
//     the truncate, or where a cut that grew the file wrote its end, can be
//     lost.
//   - A cut starts the file with a cut record, a JSON line naming the bytes
//     dropped from the log's start by every cut so far, so a [LogReader]
//     keeps its place by a byte count, as a journald cursor does
//     (sd_journal_get_cursor(3)), never by a line's content. tail -F instead
//     rereads a file that shrank from its start, repeating lines.

// errNotRegular is a log path that holds something other than a regular
// file. A link is refused by O_NOFOLLOW before this.
var errNotRegular = errors.New("not a regular file")

// openLog opens the regular file at path, never through a link. O_NONBLOCK
// keeps the open from waiting on a FIFO, which is then refused; it changes
// nothing for a regular file.
func openLog(path string, flag int) (*os.File, error) {
	file, err := os.OpenFile(path, flag|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return nil, err
	}
	if info, err := file.Stat(); err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, &os.PathError{Op: "open", Path: path, Err: errNotRegular}
	}
	return file, nil
}

// lockLog waits for a flock(2) on file, shared or exclusive.
func lockLog(file *os.File, how int) error {
	for {
		err := syscall.Flock(int(file.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// NotRegularLog reports whether err refused a log path that holds something
// other than a regular file: a link (ELOOP from O_NOFOLLOW) or anything else.
func NotRegularLog(err error) bool {
	return errors.Is(err, syscall.ELOOP) || errors.Is(err, errNotRegular)
}

// AppendLog appends line and a newline to the log at path, made 0600. A log
// over limit bytes is first cut to its newest half, from a line start (see
// [RewriteLog]). A log that can't be written loses the line: logging never
// fails its caller.
func AppendLog(path, line string, limit int) {
	file, err := openLog(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	if info, err := file.Stat(); err == nil && info.Size() > int64(limit) {
		_ = RewriteLog(path, func(body []byte) []byte {
			if len(body) <= limit {
				return body
			}
			tail := body[len(body)-limit/2:]
			return tail[bytes.IndexByte(tail, '\n')+1:]
		})
	}
	if lockLog(file, syscall.LOCK_SH) != nil {
		return
	}
	_, _ = file.WriteString(line + "\n")
}

// RewriteLog cuts the log at path in place: edit gets its lines after the
// cut record and returns those to keep, which may only drop lines. The kept
// lines are written from the file's start behind a new cut record, anything
// appended meanwhile is copied after them, and the file is truncated there,
// so its inode, mode and owner stay and a writer's O_APPEND descriptor keeps
// writing at its end. It is a separate O_RDWR descriptor because Linux
// pwrite(2) ignores the offset of an O_APPEND one. A log edit leaves as it is
// is not written.
func RewriteLog(path string, edit func(body []byte) []byte) error {
	file, err := openLog(path, os.O_RDWR)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if err = lockLog(file, syscall.LOCK_EX); err != nil {
		return err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}
	dropped, head := logCut(data)
	body := data[head:]
	kept := edit(body)
	if len(kept) == len(body) {
		return nil
	}
	cut := append(cutRecord(dropped+int64(len(body)-len(kept))), kept...)
	if _, err = file.WriteAt(cut, 0); err != nil {
		return err
	}
	// A cut that drops fewer bytes than its record adds, such as a migration
	// removing one short line, ends past the old end: copying goes on after it.
	read, write := max(int64(len(data)), int64(len(cut))), int64(len(cut))
	buffer := make([]byte, 64<<10)
	for {
		n, err := file.ReadAt(buffer, read)
		if n == 0 && errors.Is(err, io.EOF) {
			return file.Truncate(write)
		}
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		if _, err := file.WriteAt(buffer[:n], write); err != nil {
			return err
		}
		read, write = read+int64(n), write+int64(n)
	}
}

// cutEntry is a cut record: Dropped is the bytes every cut so far dropped
// from the log's start, not counting cut records.
type cutEntry struct {
	Time    time.Time `json:"time"`
	Level   string    `json:"level"`
	Msg     string    `json:"msg"`
	Event   string    `json:"event"`
	Dropped int64     `json:"dropped"`
}

// cutRecord is the cut record for dropped bytes, with its newline: a record
// as the guard log's [log/slog.JSONHandler] writes one, event "cut".
func cutRecord(dropped int64) []byte {
	record, _ := json.Marshal(cutEntry{
		Time: time.Now(), Level: "INFO", Msg: "log cut", Event: "cut", Dropped: dropped,
	})
	return append(record, '\n')
}

// logCut reads the cut record data starts with: the bytes dropped and the
// record's length with its newline, or 0 and 0 when data starts with none.
func logCut(data []byte) (dropped int64, length int) {
	end := bytes.IndexByte(data, '\n')
	if end < 0 || !bytes.HasPrefix(data, []byte(`{"time":`)) {
		return 0, 0
	}
	var entry cutEntry
	if json.Unmarshal(data[:end], &entry) != nil || entry.Event != "cut" ||
		entry.Msg != "log cut" || entry.Dropped < 0 {
		return 0, 0
	}
	return entry.Dropped, end + 1
}

// LogReader reads a log's complete lines as they are appended, by position:
// at counts the log's bytes before the next line to read, including those
// cuts dropped, so a cut that keeps its place (see [RewriteLog]) neither
// repeats nor skips a line, whatever the lines hold.
type LogReader struct {
	path string
	at   int64
	file os.FileInfo
}

// NewLogReader reads the log at path from its start.
func NewLogReader(path string) *LogReader { return &LogReader{path: path} }

// Read returns the complete lines appended since the last call, nil when there
// are none or the log is missing. It reads under a shared lock, so never in
// the middle of a cut. A log created or replaced is read from its start, as is
// one that shrank without a cut record; one cut while the reader was behind by
// more than the cut kept goes on from its oldest line, and those dropped
// before it are gone. A log whose cut record does not put the reader on a line
// start (a migration dropped lines inside it) is read from its oldest line.
func (r *LogReader) Read() ([]byte, error) {
	file, err := openLog(r.path, os.O_RDONLY)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			r.at, r.file = 0, nil
			return nil, nil
		}
		return nil, err
	}
	defer func() { _ = file.Close() }()
	if err = lockLog(file, syscall.LOCK_SH); err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if r.file != nil && !os.SameFile(r.file, info) {
		r.at = 0
	}
	r.file = info
	head := make([]byte, 512)
	n, err := file.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	dropped, length := logCut(head[:n])
	position := int64(length) + r.at - dropped
	switch {
	case r.at < dropped, position > info.Size():
		position = int64(length)
	case position > int64(length):
		var before [1]byte
		if _, err := file.ReadAt(before[:], position-1); err != nil || before[0] != '\n' {
			position = int64(length)
		}
	}
	data, err := io.ReadAll(io.NewSectionReader(file, position, info.Size()-position))
	if err != nil {
		return nil, err
	}
	data = data[:bytes.LastIndexByte(data, '\n')+1]
	r.at = dropped + position - int64(length) + int64(len(data))
	return data, nil
}
