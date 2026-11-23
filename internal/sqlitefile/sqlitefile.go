// Package sqlitefile opens Workbench's private SQLite files through the pure-Go
// driver: it makes a missing file private before SQLite opens it, so the files
// SQLite makes beside it are private too, writes the connection string, and
// switches the file to WAL.
package sqlitefile

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"time"

	sqlite "modernc.org/sqlite" // the pure-Go driver, name "sqlite"
)

// Options say how [Open] opens a file.
type Options struct {
	// Create makes the file, empty and 0600, before SQLite opens it, when it
	// is missing. SQLite gives the -wal and -shm files the database file's
	// mode, so they are private from their first byte. O_CREATE without
	// O_EXCL leaves a file another process made first, and its content, alone.
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
	// database and is returned as it is.
	Prepare func(*sql.DB) error
}

// Open opens the SQLite file at path as o says and, unless o.ReadOnly,
// switches it to WAL, so a reader reads the last committed state while a
// writer writes. Its errors name path, except those of o.Prepare.
func Open(path string, o Options) (*sql.DB, error) {
	query := make([]string, 0, len(o.Pragmas)+3)
	if o.ReadOnly {
		query = append(query, "mode=rw", "_pragma=query_only(1)")
	} else if o.Create {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	for _, pragma := range o.Pragmas {
		query = append(query, "_pragma="+pragma)
	}
	if o.TxLock != "" {
		query = append(query, "_txlock="+o.TxLock)
	}
	address := &url.URL{Scheme: "file", Path: path, RawQuery: strings.Join(query, "&")}
	db, err := sql.Open("sqlite", address.String())
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
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
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return db, nil
}

// headerSize is the size of the header that starts every SQLite database
// file.
const headerSize = 100

// ErrNotADatabase is what [CheckHeader] returns, wrapped with the path and the
// reason, for a file whose header is not an SQLite database's.
var ErrNotADatabase = errors.New("not an SQLite database")

// CheckHeader reads the first 100 bytes of the file at path, the database
// header, and fails with [ErrNotADatabase] when they are not those of a
// database the writer could open
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
// with the error reading it.
func CheckHeader(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	header := make([]byte, headerSize)
	n, err := io.ReadFull(file, header)
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("%s: %w: it holds %d bytes, less than a header", path, ErrNotADatabase, n)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if reason := headerProblem(header); reason != "" {
		return fmt.Errorf("%s: %w: %s", path, ErrNotADatabase, reason)
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
	sqliteBusy    = 5
	sqliteCorrupt = 11
	sqliteNotADB  = 26
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
