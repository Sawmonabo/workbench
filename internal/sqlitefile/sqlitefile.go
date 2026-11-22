// Package sqlitefile opens Workbench's private SQLite files through the pure-Go
// driver: it makes a missing file private before SQLite opens it, so the files
// SQLite makes beside it are private too, writes the connection string, and
// switches the file to WAL.
package sqlitefile

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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
