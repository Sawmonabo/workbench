package operation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"syscall"
	"time"
)

// Workbench's logs (the costs ingest log, the guard log) are appended to by
// many processes and cut in place, never replaced, because a process may hold
// the file open to append to it (a watcher of an earlier build kept its own
// output open on the guard log). Three
// rules keep every line [AppendLog] writes and let a reader follow the log:
//
//   - Every write opens the file with O_APPEND, so POSIX places it at the end
//     of the file with no intervening modification (write(2)), and with
//     O_NOFOLLOW, so a link put in the log's place is refused (open(2) on
//     Linux and macOS: ELOOP).
//   - [AppendLog] holds a shared flock(2) for its write and a cut holds an
//     exclusive one, so no appended line falls between the cut's read and its
//     truncate. Every writer of a log goes through [AppendLog]; a detached
//     child's own output goes to a file of its own (see [StartDetached]).
//   - A cut starts the file with a cut record, a JSON line mapping the lines
//     it kept to where they were written (see [logMap]), so a [LogReader]
//     keeps its place by byte offset, never by a line's content, across a cut
//     that keeps the newest half and one that drops lines from the middle.
//     tail -F instead rereads a file that shrank from its start, repeating
//     lines.

// errNotRegular is a log path that holds something other than a regular
// file. A link is refused by O_NOFOLLOW before this.
var errNotRegular = errors.New("not a regular file")

// openLogFile opens the regular file at path, never through a link. O_NONBLOCK
// keeps the open from waiting on a FIFO, which is then refused; it changes
// nothing for a regular file.
func openLogFile(path string, flag int) (*os.File, error) {
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
	file, err := openLogFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	if info, err := file.Stat(); err == nil && info.Size() > int64(limit) {
		_ = RewriteLog(path, func(body []byte) [][2]int {
			if len(body) <= limit {
				return [][2]int{{0, len(body)}}
			}
			start := len(body) - limit/2
			start += bytes.IndexByte(body[start:], '\n') + 1
			return [][2]int{{start, len(body)}}
		})
	}
	if lockLog(file, syscall.LOCK_SH) != nil {
		return
	}
	_, _ = file.WriteString(line + "\n")
}

// RewriteLog cuts the log at path in place: edit gets its lines after the
// cut record and returns the byte ranges to keep, [start, end) in order, each
// whole lines. The kept lines are written from the file's start behind a new
// cut record, anything appended meanwhile is copied after them, and the file
// is truncated there, so its inode, mode and owner stay and a writer's
// O_APPEND descriptor keeps writing at its end. It is a separate O_RDWR
// descriptor because Linux pwrite(2) ignores the offset of an O_APPEND one. A
// log edit keeps whole is not written.
func RewriteLog(path string, edit func(body []byte) [][2]int) error {
	file, err := openLogFile(path, os.O_RDWR)
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
	old, head := readLogMap(data)
	body := data[head:]
	var cut logMap
	var kept []byte
	for _, keep := range edit(body) {
		kept = append(kept, body[keep[0]:keep[1]]...)
		for at := int64(keep[0]); at < int64(keep[1]); {
			offset, room := old.span(at)
			n := min(room, int64(keep[1])-at)
			if last := len(
				cut.Kept,
			) - 1; last >= 0 &&
				cut.Kept[last][0]+cut.Kept[last][1] == offset {
				cut.Kept[last][1] += n
			} else {
				cut.Kept = append(cut.Kept, [2]int64{offset, n})
			}
			at += n
		}
	}
	if len(kept) == len(body) {
		return nil
	}
	cut.Next, _ = old.span(int64(len(body)))
	record := cut.record()
	if _, err = file.WriteAt(append(record, kept...), 0); err != nil {
		return err
	}
	// A cut that drops fewer bytes than its record adds, such as a migration
	// removing one short line, ends past the old end: copying goes on after it.
	end := int64(len(record) + len(kept))
	read, write := max(int64(len(data)), end), end
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

// logMap places the lines after a cut record in the log as it was written,
// by offset: the bytes ever appended to it before a byte, cut ones included.
// Kept are the runs of written bytes the cuts left, [offset, length] in order,
// and Next is the offset of the byte after them, which bytes appended after
// the cut continue from. A log never cut has no runs and starts at offset 0.
type logMap struct {
	Kept [][2]int64 `json:"kept"`
	Next int64      `json:"next"`
}

// span is the offset of the byte at position p after the cut record, and how
// many bytes from it on follow each other in the log as written.
func (m logMap) span(p int64) (offset, room int64) {
	for _, run := range m.Kept {
		if p < run[1] {
			return run[0] + p, run[1] - p
		}
		p -= run[1]
	}
	return m.Next + p, math.MaxInt64
}

// position is where the first byte at or after offset sits after the cut
// record: a byte a cut dropped gives the next one kept.
func (m logMap) position(offset int64) int64 {
	var p int64
	for _, run := range m.Kept {
		if offset < run[0] {
			return p
		}
		if offset < run[0]+run[1] {
			return p + offset - run[0]
		}
		p += run[1]
	}
	return p + max(offset-m.Next, 0)
}

// cutEntry is a cut record, a record as the guard log's
// [log/slog.JSONHandler] writes one, event "cut".
type cutEntry struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"`
	Msg   string    `json:"msg"`
	Event string    `json:"event"`
	logMap
}

// record is the cut record for m, with its newline.
func (m logMap) record() []byte {
	record, _ := json.Marshal(cutEntry{
		Time: time.Now(), Level: "INFO", Msg: "log cut", Event: "cut", logMap: m,
	})
	return append(record, '\n')
}

// readLogMap reads the cut record data starts with: its map and its length
// with its newline, or no runs and 0 when data starts with none.
func readLogMap(data []byte) (logMap, int) {
	end := bytes.IndexByte(data, '\n')
	if end < 0 || !bytes.HasPrefix(data, []byte(`{"time":`)) {
		return logMap{}, 0
	}
	var entry cutEntry
	if json.Unmarshal(data[:end], &entry) != nil || entry.Event != "cut" ||
		entry.Msg != "log cut" || entry.Next < 0 {
		return logMap{}, 0
	}
	return entry.logMap, end + 1
}

// LogReader reads a log's complete lines as they are appended, by offset (see
// [logMap]), so a cut (see [RewriteLog]) neither repeats nor skips a line it
// kept, whatever the lines hold, as a journald cursor keeps a reader's place
// (sd_journal_get_cursor(3)). It keeps the log open, so when the log is
// renamed away and replaced, as a rotation does, it reads on in the old file,
// until the next replacement, as well as in the new one.
type LogReader struct {
	path              string
	current, previous *openLog
}

// openLog is a log a [LogReader] has open and its offset in it.
type openLog struct {
	file *os.File
	at   int64
}

// NewLogReader reads the log at path from its start.
func NewLogReader(path string) *LogReader { return &LogReader{path: path} }

// Close closes the logs the reader has open.
func (r *LogReader) Close() {
	for _, o := range []*openLog{r.current, r.previous} {
		if o != nil {
			_ = o.file.Close()
		}
	}
	r.current, r.previous = nil, nil
}

// Read returns the complete lines appended since the last call, nil when there
// are none or the log is missing: those of the file the log replaced first. A
// log created or replaced is read from its start, as is one that shrank
// without a cut record; one cut while the reader was behind goes on from the
// first line the cut kept after its place.
func (r *LogReader) Read() ([]byte, error) {
	var data []byte
	if r.previous != nil {
		more, err := r.previous.read()
		if err != nil {
			return nil, err
		}
		data = more
	}
	if r.current != nil {
		info, statErr := os.Lstat(r.path)
		open, err := r.current.file.Stat()
		if err != nil {
			return data, err
		}
		more, err := r.current.read()
		if data = append(data, more...); err != nil {
			return data, err
		}
		if statErr == nil && os.SameFile(info, open) {
			return data, nil
		}
		if r.previous != nil {
			_ = r.previous.file.Close()
		}
		r.previous, r.current = r.current, nil
	}
	file, err := openLogFile(r.path, os.O_RDONLY)
	if errors.Is(err, os.ErrNotExist) {
		return data, nil
	}
	if err != nil {
		return data, err
	}
	r.current = &openLog{file: file}
	more, err := r.current.read()
	return append(data, more...), err
}

// read reads the open log's new complete lines under a shared lock, so never
// in the middle of a cut.
func (r *openLog) read() ([]byte, error) {
	if err := lockLog(r.file, syscall.LOCK_SH); err != nil {
		return nil, err
	}
	defer func() { _ = lockLog(r.file, syscall.LOCK_UN) }()
	info, err := r.file.Stat()
	if err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(io.NewSectionReader(r.file, 0, info.Size())).ReadBytes('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	m, head := readLogMap(line)
	position := int64(head) + m.position(r.at)
	if position > info.Size() {
		position = int64(head)
	}
	data, err := io.ReadAll(io.NewSectionReader(r.file, position, info.Size()-position))
	if err != nil {
		return nil, err
	}
	data = data[:bytes.LastIndexByte(data, '\n')+1]
	r.at, _ = m.span(position - int64(head) + int64(len(data)))
	return data, nil
}
