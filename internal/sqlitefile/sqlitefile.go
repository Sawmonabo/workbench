// Package sqlitefile opens Workbench's private SQLite files through the pure-Go
// driver: it makes a missing file private before SQLite opens it, so the files
// SQLite makes beside it are private too, writes the connection string, says
// why a file cannot be opened in the operating system's words, switches the
// file to WAL, and takes group and other access off a file its caller has
// accepted and the files beside it.
package sqlitefile

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Sawmonabo/workbench/internal/display"
	"github.com/Sawmonabo/workbench/internal/operation"
	"golang.org/x/sys/unix"
	sqlite "modernc.org/sqlite" // the pure-Go driver, name "sqlite"
)

// Options say how [Open] opens a file.
type Options struct {
	// Create makes the file, empty and 0600, before SQLite opens it, when it
	// is missing. SQLite gives the -wal and -shm files the database file's
	// mode, so they are private from their first byte. O_EXCL makes it only
	// where nothing is, so a file already there, one another process made
	// first too, is never opened outside SQLite and keeps its content.
	Create bool
	// ReadOnly opens an existing file for queries only, never creating it
	// (whatever Create says) or switching its journal mode. The connection
	// opens with mode=rw, because a reader of a WAL file writes the -shm file
	// and a read-only connection can't remove the -wal and -shm files it makes
	// beside it, and with query_only(1), which refuses an ordinary write.
	ReadOnly bool
	// Pragmas run on each new connection, each as a _pragma, for example
	// "busy_timeout(5000)".
	Pragmas []string
	// TxLock is the lock BEGIN takes ("deferred", "immediate" or
	// "exclusive"); empty leaves the driver's default.
	TxLock string
	// Prepare, when set, runs on the opened database before the switch to
	// WAL, so a caller that checks the file is its own, or writes its schema,
	// does so before anything changes the file. An error it returns closes the
	// database and is returned as it is. Once it returns nil, the file is its
	// caller's, so Open makes it and its -wal and -shm files private (see
	// makePrivate); without Prepare it changes no mode.
	Prepare func(*sql.DB) error
}

// Open opens the SQLite file at path as o says and, unless o.ReadOnly,
// switches it to WAL, so a reader reads the last committed state while a
// writer writes; a file o.Prepare accepted it then makes private with the
// files beside it (see makePrivate). It opens the first connection itself, before o.Prepare, so
// a file SQLite cannot open (CANTOPEN) fails here with the operating
// system's reason (see openProblem); the connection then waits in the pool,
// where o.Prepare and the switch to WAL take it, so SQLite opens the file
// once. Any other error of that first connection, such as that of a file
// that is not a database, which a pragma meets, is left to o.Prepare, or to
// the caller without one, which meets it again on its own first connection
// and says it as it says its other errors. Its errors name path once (see
// [operation.FileError]), except those of o.Prepare.
//
// It never opens a file already at path outside the driver: closing that
// descriptor would drop every SQLite lock the process holds on the file,
// those of a connection it already has to it too (see [CheckHeader]). The
// private create opens only the file it makes, and the reason of a file
// SQLite cannot open is asked without opening it (see openProblem).
func Open(path string, o Options) (*sql.DB, error) {
	if !o.ReadOnly && o.Create {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		switch {
		case errors.Is(err, fs.ErrExist):
		case err != nil:
			return nil, operation.FileError(path, err)
		default:
			if err := file.Close(); err != nil {
				return nil, operation.FileError(path, err)
			}
		}
	}
	db, err := sql.Open("sqlite", Address(path, o))
	if err != nil {
		return nil, operation.FileError(path, err)
	}
	conn, err := db.Conn(context.Background())
	switch {
	case err == nil:
		_ = conn.Close()
	case code(err) == sqliteCantOpen:
		_ = db.Close()
		return nil, operation.FileError(path, openProblem(path, err))
	}
	if o.Prepare != nil {
		if err := o.Prepare(db); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	if !o.ReadOnly {
		if err := enableWAL(db); err != nil {
			_ = db.Close()
			return nil, operation.FileError(path, err)
		}
	}
	if o.Prepare != nil {
		makePrivate(path)
	}
	return db, nil
}

// Address is the connection string [Open] opens the file at path with as o
// says, for a caller that names the file to SQLite in a statement, such as
// ATTACH, with the same mode and pragmas.
func Address(path string, o Options) string {
	query := make([]string, 0, len(o.Pragmas)+3)
	if o.ReadOnly {
		query = append(query, "mode=rw", "_pragma=query_only(1)")
	}
	for _, pragma := range o.Pragmas {
		query = append(query, "_pragma="+pragma)
	}
	if o.TxLock != "" {
		query = append(query, "_txlock="+o.TxLock)
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: strings.Join(query, "&")}).String()
}

// Transact runs fn in one transaction on db, committed when fn returns nil
// and rolled back when it returns an error, which Transact returns.
func Transact(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// makePrivate takes group and other access off the file at path and its -wal
// and -shm files, when they have any: SQLite makes the two with the mode the
// file has, so a file another tool or a copy left readable by others gives
// them that mode too. It runs once the file's own checks have accepted it, so
// a foreign file is never touched, while the connection that made the -wal and
// -shm files holds them, and it changes the mode only. It is best effort: a
// mode that cannot be changed (a read-only mount) must not stop a file from
// being read or written, which for the costs ledger would risk the only
// lasting record of spend over a hardening step.
func makePrivate(path string) {
	for _, name := range append([]string{path}, SideFiles(path)...) {
		if info, err := os.Stat(name); err == nil && info.Mode().Perm()&0o077 != 0 {
			_ = os.Chmod(name, info.Mode().Perm()&^0o077)
		}
	}
}

// SideFiles are the files SQLite keeps beside the WAL database at path: its
// write-ahead log, path-wal, and its shared-memory index, path-shm.
func SideFiles(path string) []string { return []string{path + "-wal", path + "-shm"} }

// Remove deletes the closed database at path with the files SQLite keeps
// beside it, its side files and a rollback journal (-journal), those first:
// one left beside no database would pair with the next database made at
// path, which SQLite would then read through it
// (https://sqlite.org/howtocorrupt.html#_mispairing_database_files_and_hot_journals).
// A file already gone is no error.
func Remove(path string) error {
	for _, name := range append(SideFiles(path), path+"-journal", path) {
		if err := operation.RemoveIfThere(name); err != nil {
			return err
		}
	}
	return nil
}

// openProblem is the operating system's reason SQLite could not open the
// file at path, err being SQLite's CANTOPEN, which says only that it could
// not (unable to open database file): SQLite opens a file for reading and
// writing and, refused, for reading only, so the reason it met last is why
// the file cannot be read. faccessat(2) asks that with the IDs open(2)
// checks, without opening the file, which this process may hold a
// connection to (see [Open]). err stays as it is when the file can be read,
// since SQLite failed for another reason then, such as a process out of
// descriptors.
func openProblem(path string, err error) error {
	if problem := unix.Faccessat(unix.AT_FDCWD, path, unix.R_OK, unix.AT_EACCESS); problem != nil {
		return &fs.PathError{Op: "access", Path: path, Err: problem}
	}
	return err
}

// ContainsFold is the SQL name of a function every connection the driver
// opens has: contains_fold(needle, text, ...) is 1 when any text holds needle
// with case ignored as [HoldsFold] ignores it, through the same code. A NULL
// text holds nothing; a NULL needle is held by nothing. It is deterministic,
// so SQLite may run it once for equal arguments, and reads its arguments in
// place, without a copy.
const ContainsFold = "contains_fold"

func init() {
	sqlite.MustRegisterFunction(ContainsFold, &sqlite.FunctionImpl{
		NArgs:         -1,
		Deterministic: true,
		VolatileArgs:  true,
		Scalar:        containsFold,
	})
}

// HoldsFold reports whether any of texts holds needle with case ignored as
// Go's strings.EqualFold ignores it, by Unicode simple case folding, not
// ASCII letters only as LIKE does: "CAFÉ" holds "café", and "\u212a" (the
// Kelvin sign) holds "k". It is the rule of [ContainsFold] too, so what is
// searched in Go and in SQL matches alike.
func HoldsFold(needle string, texts ...string) bool {
	b := foldFor(needle)
	defer folded.Put(b)
	return slices.ContainsFunc(texts, b.holds)
}

// foldBuffers are what [HoldsFold] and containsFold fold their needle and
// each text into, kept between calls: want is the needle folded (see
// appendFolded), ascii whether it is all ASCII.
type foldBuffers struct {
	want, have []byte
	ascii      bool
}

var folded = sync.Pool{New: func() any { return new(foldBuffers) }}

// foldFor is buffers from the pool holding needle folded, to put back once
// done with.
func foldFor(needle string) *foldBuffers {
	b, ok := folded.Get().(*foldBuffers)
	if !ok {
		b = new(foldBuffers)
	}
	b.want = appendFolded(b.want[:0], needle)
	b.ascii = !slices.ContainsFunc(b.want, func(c byte) bool { return c >= utf8.RuneSelf })
	return b
}

// containsFold is [ContainsFold]. Its strings are views into SQLite's memory,
// valid only during the call, so nothing is kept of them: what the buffers
// keep is folded copies, overwritten by the next call.
func containsFold(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if len(args) < 2 {
		return nil, fmt.Errorf("%s takes a needle and at least one text", ContainsFold)
	}
	needle, ok := args[0].(string)
	if !ok {
		return int64(0), nil
	}
	b := foldFor(needle)
	defer folded.Put(b)
	for _, arg := range args[1:] {
		if text, ok := arg.(string); ok && b.holds(text) {
			return int64(1), nil
		}
	}
	return int64(0), nil
}

// holds reports whether text holds b.want. Only two characters past ASCII
// have an ASCII key, U+017F (ſ, whose key is S) and U+212A (the Kelvin sign,
// K), as a walk of every rune through foldKey shows; so an ASCII needle can
// match only ASCII characters of a text without those two, which is searched
// where it lies, with ASCII letters' case ignored, stopping at the first
// match, as LIKE does. Any other text is folded whole and searched.
func (b *foldBuffers) holds(text string) bool {
	if b.ascii && !strings.ContainsRune(text, '\u017f') && !strings.ContainsRune(text, '\u212a') {
		return containsASCIIFold(text, b.want)
	}
	b.have = appendFolded(b.have[:0], text)
	return bytes.Contains(b.have, b.want)
}

// containsASCIIFold reports whether text holds want, ASCII with its letters
// upper case, with the case of text's ASCII letters ignored. A byte past
// ASCII never equals one of want's, so it is never part of a match.
func containsASCIIFold(text string, want []byte) bool {
	n := len(want)
	for i := 0; i+n <= len(text); i++ {
		j := 0
		for j < n && upperASCII(text[i+j]) == want[j] {
			j++
		}
		if j == n {
			return true
		}
	}
	return false
}

// upperASCII is c with an ASCII lower-case letter made upper case.
func upperASCII(c byte) byte {
	if 'a' <= c && c <= 'z' {
		return c - ('a' - 'A')
	}
	return c
}

// appendFolded appends text to dst with each character replaced by its key
// under simple case folding (see foldKey): two texts strings.EqualFold calls
// equal append the same bytes. A byte that is not UTF-8 appends U+FFFD, as
// it reads in strings.EqualFold. ASCII, most of any command, takes the short
// way.
func appendFolded(dst []byte, text string) []byte {
	for i := 0; i < len(text); {
		if c := text[i]; c < utf8.RuneSelf {
			dst = append(dst, upperASCII(c))
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		dst = utf8.AppendRune(dst, foldKey(r))
		i += size
	}
	return dst
}

// foldKey is r's key under Unicode simple case folding: the smallest rune of
// the orbit unicode.SimpleFold steps r through, which holds every rune
// strings.EqualFold takes for r, so two runes it calls equal have one key.
func foldKey(r rune) rune {
	key := r
	for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
		key = min(key, f)
	}
	return key
}

// headerSize is the size of the header that starts every SQLite database
// file.
const headerSize = 100

// ErrNotADatabase is what [CheckHeader] returns, wrapped with the path and the
// reason, for a file whose header is not an SQLite database's.
var ErrNotADatabase = errors.New("not an SQLite database")

// CheckHeader reads the first 100 bytes of the database file open as file,
// the database header, and fails with [ErrNotADatabase] when they are not
// those of a database the writer could open
// (https://www.sqlite.org/fileformat2.html#the_database_header): the magic
// string "SQLite format 3\x00", a page size that is a power of two from 512
// to 65536 (65536 written as 1), file format versions of 1 (rollback
// journal) or 2 (WAL), the only values the format defines, at least 480
// usable bytes a page, and payload fractions of 64, 32 and 32. SQLite's own
// check on open, lockBtree in src/btree.c, refuses a file that fails any of
// these but the versions, of which it refuses a read version past 2 and opens
// a file whose write version is past 2 read-only. It reads nothing else, so
// it costs the same whatever the file's size; a page damaged past the header
// is SQLite's to find, when it reads it. A file that cannot be read fails
// with the error reading it, which names the file once (see [operation.FileError]).
//
// It reads through file and never opens the path: closing any descriptor of
// a file drops every POSIX advisory lock the process holds on it, SQLite's
// included, after which another process that closes the database, one built
// on SQLite before 3.51.0 such as Python's sqlite3, takes itself for its last
// user and deletes the -wal and -shm files from under the connection
// (https://sqlite.org/howtocorrupt.html#_posix_advisory_locks_canceled_by_a_separate_thread_doing_close_).
// So a process that holds a connection to the file reads it only through one
// descriptor it opens once and closes only after that connection has closed.
func CheckHeader(file *os.File) error {
	path := file.Name()
	header := make([]byte, headerSize)
	n, err := file.ReadAt(header, 0)
	if errors.Is(err, io.EOF) {
		return fmt.Errorf(
			"%s: %w: it holds %d bytes, less than a header",
			display.Path(path),
			ErrNotADatabase,
			n,
		)
	}
	if err != nil {
		return operation.FileError(path, err)
	}
	if reason := headerProblem(header); reason != "" {
		return fmt.Errorf("%s: %w: %s", display.Path(path), ErrNotADatabase, reason)
	}
	return nil
}

// headerProblem is what is wrong with header, a database header's 100 bytes,
// "" when nothing is.
func headerProblem(header []byte) string {
	pageSize := int(binary.BigEndian.Uint16(header[16:18]))
	if pageSize == 1 {
		pageSize = 1 << 16
	}
	switch {
	case string(header[:16]) != "SQLite format 3\x00":
		return "its header does not start with SQLite's magic string"
	case pageSize < 512 || pageSize&(pageSize-1) != 0:
		return "its page size is not a power of two from 512 to 65536"
	case header[18] < 1 || header[18] > 2 || header[19] < 1 || header[19] > 2:
		return "its file format versions are not 1 or 2"
	case pageSize-int(header[20]) < 480:
		return "its pages keep fewer than 480 usable bytes"
	case header[21] != 64 || header[22] != 32 || header[23] != 32:
		return "its payload fractions are not 64, 32 and 32"
	}
	return ""
}

// SQLite's result codes this package tells apart, which an extended code keeps
// in its low byte.
const (
	sqliteBusy     = 5
	sqliteCorrupt  = 11
	sqliteCantOpen = 14
	sqliteNotADB   = 26
)

// IsBusy reports whether err is SQLite's BUSY.
func IsBusy(err error) bool { return code(err) == sqliteBusy }

// IsDamaged reports whether err is SQLite's NOTADB, a file that is not a
// database, or its CORRUPT, a database whose content SQLite found malformed.
func IsDamaged(err error) bool {
	c := code(err)
	return c == sqliteCorrupt || c == sqliteNotADB
}

// code is the primary result code of the SQLite error in err, 0 when err holds
// none.
func code(err error) int {
	problem, ok := errors.AsType[*sqlite.Error](err)
	if !ok {
		return 0
	}
	return problem.Code() & 0xff
}

// enableWAL switches db to WAL. Doing so for a new file takes the database
// exclusively, which two creators doing it at once cannot both be granted:
// SQLite fails one of them with BUSY at once, without waiting out the busy
// timeout. The other has switched the journal by the time that one tries
// again, and a file already in WAL needs no lock.
func enableWAL(db *sql.DB) error {
	ctx := context.Background()
	var err error
	for attempt := range 50 {
		_, err = db.ExecContext(ctx, "PRAGMA journal_mode=WAL")
		if !IsBusy(err) {
			return err
		}
		time.Sleep(time.Duration(attempt+1) * time.Millisecond)
	}
	return err
}
