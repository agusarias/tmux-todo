package store

import (
	"path/filepath"
	"testing"
)

// openTemp opens a real SQLite file inside the test's temp dir. No mocks: the
// point of this package is talking to SQLite correctly.
func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOpenCreatesSchemaVersion(t *testing.T) {
	db := openTemp(t)

	var name string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_version'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("schema_version table missing: %v", err)
	}

	v, err := db.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != 0 {
		t.Errorf("fresh database reports version %d, want 0", v)
	}
}

func TestVersionRoundTrip(t *testing.T) {
	db := openTemp(t)

	if err := db.SetVersion(3); err != nil {
		t.Fatalf("SetVersion: %v", err)
	}
	v, err := db.Version()
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v != 3 {
		t.Errorf("Version = %d, want 3", v)
	}
}

func TestOpenIsIdempotentAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "tasks.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := db.SetVersion(7); err != nil {
		t.Fatalf("SetVersion: %v", err)
	}
	if db.Path() != path {
		t.Errorf("Path = %q, want %q", db.Path(), path)
	}
	db.Close()

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer reopened.Close()

	v, err := reopened.Version()
	if err != nil {
		t.Fatalf("Version after reopen: %v", err)
	}
	if v != 7 {
		t.Errorf("version after reopen = %d, want 7", v)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("Open(\"\") returned no error")
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
