package costs

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Sawmonabo/workbench/internal/operation"
)

// transcriptRooter is a source that says where its transcripts live, so a
// hook's transcript path names the tool it fired for.
type transcriptRooter interface {
	TranscriptRoots(home string) []string
}

// toolOfTranscript is the tool whose transcript root contains path.
func toolOfTranscript(home, path string) (Tool, bool) {
	if path == "" {
		return Tool{}, false
	}
	for _, tool := range Tools {
		rooter, ok := tool.Source.(transcriptRooter)
		if !ok {
			continue
		}
		for _, root := range rooter.TranscriptRoots(home) {
			if within(root, path) {
				return tool, true
			}
		}
	}
	return Tool{}, false
}

// within reports whether path is inside root, comparing resolved symlinks too
// (the path need not exist yet: a session's transcript is written after its
// SessionStart hook fires).
func within(root, path string) bool {
	inside := func(root, path string) bool {
		rel, err := filepath.Rel(root, path)
		return err == nil && rel != "." && rel != ".." &&
			!strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	if inside(filepath.Clean(root), filepath.Clean(path)) {
		return true
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	// Resolve the longest existing directory part of path.
	dir, rest := filepath.Dir(filepath.Clean(path)), filepath.Base(path)
	for dir != filepath.Dir(dir) {
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			return inside(realRoot, filepath.Join(real, rest))
		}
		dir, rest = filepath.Dir(dir), filepath.Join(filepath.Base(dir), rest)
	}
	return false
}

// fileRead is one transcript read, ready to commit.
type fileRead struct {
	path     string
	vanished bool // the file is gone: forget it
	changed  bool // new bytes were read
	usage    []Usage
	tiers    []TierChange
	parents  []ThreadParent
	marks    []AccountMark
	row      FileRow
	err      error
}

// take keeps what a read that reached its last complete line found.
func (r *fileRead) take(tool Tool, state *FileState) {
	if f, ok := tool.Source.(finisher); ok {
		f.Finish(state)
	}
	r.changed, r.tiers, r.parents, r.marks = true, state.Tiers, state.Parents, state.Marks
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
	read.take(tool, state)
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
	return operation.SHA256Hex(buf), nil
}

// transcriptFiles lists, sorted, the files named *ext under roots: regular
// files and links to one. A root that is not a folder, or a link to one,
// holds none, and a folder that vanished or cannot be read is passed over.
func transcriptFiles(roots []string, ext string) ([]string, error) {
	var files []string
	for _, root := range roots {
		if !operation.LeadsToFolder(root) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			switch {
			case err != nil:
				// A folder that vanished or cannot be read holds nothing to ingest.
				if entry != nil && entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			case filepath.Ext(path) != ext:
			case entry.Type().IsRegular():
				files = append(files, path)
			case entry.Type()&fs.ModeSymlink != 0:
				if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
					files = append(files, path)
				}
			}
			return nil
		})
		if err != nil {
			return files, err
		}
	}
	sort.Strings(files)
	return files, nil
}
