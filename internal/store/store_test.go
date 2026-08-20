package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// openTemp opens a real SQLite file inside the test's temp dir. No mocks: the
// point of this package is talking to SQLite correctly.
func openTemp(t *testing.T) *DB {
	t.Helper()
	return openAt(t, filepath.Join(t.TempDir(), "tasks.db"))
}

func openAt(t *testing.T, path string) *DB {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%s): %v", path, err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// freezeClock pins the store's clock so timestamp assertions are exact.
func freezeClock(db *DB, at time.Time) {
	db.now = func() time.Time { return at }
}

func TestOpenAppliesPragmas(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	mode, err := db.JournalMode(ctx)
	if err != nil {
		t.Fatalf("JournalMode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	// busy_timeout and foreign_keys are per-connection, so this also proves the
	// DSN pragmas reach pooled connections rather than just the first one.
	for _, tc := range []struct{ pragma, want string }{
		{"busy_timeout", "5000"},
		{"foreign_keys", "1"},
	} {
		var got string
		if err := db.QueryRowContext(ctx, "PRAGMA "+tc.pragma).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", tc.pragma, err)
		}
		if got != tc.want {
			t.Errorf("PRAGMA %s = %q, want %q", tc.pragma, got, tc.want)
		}
	}
}

func TestOpenCreatesMissingDirectories(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "deeper", "tasks.db")
	db := openAt(t, path)
	if db.Path() != path {
		t.Errorf("Path = %q, want %q", db.Path(), path)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal(`Open("") returned no error`)
	}
}

func TestDefaultPathHonoursXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg-example")
	got, err := DefaultPath()
	if err != nil {
		t.Fatalf("DefaultPath: %v", err)
	}
	want := filepath.Join("/tmp/xdg-example", AppDir, DBName)
	if got != want {
		t.Errorf("DefaultPath = %q, want %q", got, want)
	}
}
