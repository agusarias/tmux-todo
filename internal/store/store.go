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

// dsn builds the connection string. The pragmas ride on the DSN rather than a
// post-open Exec because database/sql pools connections: a pragma applied to
// one connection would not reach the others.
//
//	journal_mode=WAL   readers never block the writer, so several open popups
//	                   coexist. Costs the tasks.db-wal / tasks.db-shm sidecars.
//	busy_timeout=5000  a contended writer waits instead of failing SQLITE_BUSY.
//	foreign_keys=ON    SQLite's default is off; v1 has no FKs but later schema
//	                   versions should not have to remember to switch it on.
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(ON)")
	return "file:" + path + "?" + q.Encode()
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
	if err := db.migrate(context.Background()); err != nil {
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
	var v int
	if err := db.QueryRowContext(ctx, `SELECT version FROM schema_version WHERE id = 1`).Scan(&v); err != nil {
		return 0, fmt.Errorf("read schema_version: %w", err)
	}
	return v, nil
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
