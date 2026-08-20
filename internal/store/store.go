// Package store owns the SQLite database: the v1 schema, the migration runner
// and every query the CLI and TUI need. The driver is modernc.org/sqlite (pure
// Go) so the binary builds with CGO_ENABLED=0 and links nothing.
//
// The store is deliberately environment-blind: it accepts task.Scope values and
// never inspects tmux or the filesystem to work out what scope is active. That
// is internal/scope's job.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// AppDir is the directory name used under the XDG data dir.
const AppDir = "tmux-todo"

// DBName is the database filename inside AppDir.
const DBName = "tasks.db"

// ErrNotFound is returned when an operation names a task id that does not exist.
var ErrNotFound = errors.New("task not found")

// DB is a thin wrapper over *sql.DB carrying tmux-todo's schema helpers.
type DB struct {
	*sql.DB
	path string

	// now is the clock the store stamps rows with. Tests override it; callers
	// never have to thread a time through the API.
	now func() time.Time
}

// DefaultPath returns $XDG_DATA_HOME/tmux-todo/tasks.db, falling back to
// ~/.local/share/tmux-todo/tasks.db.
func DefaultPath() (string, error) {
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, AppDir, DBName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".local", "share", AppDir, DBName), nil
}

// journalWAL is the journal mode the store requires.
const journalWAL = "wal"

// dsn builds the connection string. These pragmas ride on the DSN because
// database/sql pools connections and they are per-connection settings: applied
// with a post-open Exec they would reach one connection and miss the others.
//
//	busy_timeout=5000  a contended connection waits instead of failing
//	                   SQLITE_BUSY. First on purpose, so the wait is in force
//	                   before anything else touches the file.
//	foreign_keys=ON    SQLite's default is off; v1 has no FKs but later schema
//	                   versions should not have to remember to switch it on.
//
// journal_mode is deliberately NOT here — see ensureJournalMode.
func dsn(path string) string {
	pragmas := []string{
		"busy_timeout(5000)",
		"foreign_keys(ON)",
	}
	// Built by hand rather than with url.Values.Encode: the order matters and
	// Encode makes no promise about preserving it.
	q := make([]string, 0, len(pragmas))
	for _, p := range pragmas {
		q = append(q, "_pragma="+url.QueryEscape(p))
	}
	return "file:" + path + "?" + strings.Join(q, "&")
}

// ensureJournalMode puts the database in WAL, retrying while another connection
// is in the way.
//
// WAL is a persistent property of the file, not of a connection, so it is set
// once here instead of on the DSN. That is not a style choice: SQLite refuses a
// journal_mode change while another connection is reading and answers
// SQLITE_BUSY *without* consulting busy_timeout, so as a DSN pragma it fires on
// every new pooled connection and turns concurrent first opens into hard
// failures. Setting it here means the second process usually finds WAL already
// in place and never asks.
func (db *DB) ensureJournalMode(ctx context.Context) error {
	const (
		attempts = 50
		backoff  = 20 * time.Millisecond
	)

	var lastErr error
	for i := 0; i < attempts; i++ {
		mode, err := db.JournalMode(ctx)
		if err != nil {
			return err
		}
		if mode == journalWAL {
			return nil
		}

		var got string
		switch err := db.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&got); {
		case err != nil:
			lastErr = err
		case got == journalWAL:
			return nil
		default:
			lastErr = fmt.Errorf("journal_mode is %q after requesting %s", got, journalWAL)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
	return fmt.Errorf("enable WAL on %s: %w", db.path, lastErr)
}

// Open opens (creating if needed) the database at path, applies the connection
// pragmas, runs any outstanding migrations and returns the handle. The parent
// directory is created when missing.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	sqlDB, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db := &DB{DB: sqlDB, path: path, now: time.Now}
	ctx := context.Background()
	if err := db.ensureJournalMode(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	if err := db.migrate(ctx); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Path returns the file the database was opened from.
func (db *DB) Path() string { return db.path }

// Version reads the recorded schema version. A migrated database reports the
// highest embedded migration; see SchemaVersion.
func (db *DB) Version(ctx context.Context) (int, error) {
	return readVersion(ctx, db.DB)
}

// JournalMode reports the journal mode in effect, read back from the database
// rather than assumed from the DSN.
func (db *DB) JournalMode(ctx context.Context) (string, error) {
	var mode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		return "", fmt.Errorf("read journal_mode: %w", err)
	}
	return mode, nil
}
