// Package store owns the SQLite database. The driver is modernc.org/sqlite
// (pure Go) so the binary builds with CGO_ENABLED=0 and links nothing.
//
// This scaffold provides only Open plus the schema_version bookkeeping that
// every later migration builds on; task queries belong to the store-and-
// migrations task.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go SQLite driver, registered as "sqlite"
)

// AppDir is the directory name used under the XDG data dir.
const AppDir = "tmux-todo"

// DBName is the database filename inside AppDir.
const DBName = "tasks.db"

// DB is a thin wrapper over *sql.DB carrying tmux-todo's schema helpers.
type DB struct {
	*sql.DB
	path string
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

// Open opens (creating if needed) the database at path, ensures the
// schema_version table exists, and returns the handle. The parent directory is
// created when missing.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("store: empty database path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create data dir: %w", err)
	}
	sqlDB, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	db := &DB{DB: sqlDB, path: path}
	if err := db.ensureSchemaVersion(); err != nil {
		sqlDB.Close()
		return nil, err
	}
	return db, nil
}

// Path returns the file the database was opened from.
func (db *DB) Path() string { return db.path }

func (db *DB) ensureSchemaVersion() error {
	const ddl = `CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER NOT NULL
	)`
	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM schema_version`).Scan(&n); err != nil {
		return fmt.Errorf("count schema_version: %w", err)
	}
	if n == 0 {
		if _, err := db.Exec(`INSERT INTO schema_version (version) VALUES (0)`); err != nil {
			return fmt.Errorf("seed schema_version: %w", err)
		}
	}
	return nil
}

// Version reads the recorded schema version. A fresh database reports 0; the
// migrations task raises it as it adds tables.
func (db *DB) Version() (int, error) {
	var v int
	if err := db.QueryRow(`SELECT version FROM schema_version LIMIT 1`).Scan(&v); err != nil {
		return 0, fmt.Errorf("read schema_version: %w", err)
	}
	return v, nil
}

// SetVersion records the schema version.
func (db *DB) SetVersion(v int) error {
	if _, err := db.Exec(`UPDATE schema_version SET version = ?`, v); err != nil {
		return fmt.Errorf("write schema_version: %w", err)
	}
	return nil
}
