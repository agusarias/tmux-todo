package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

func run(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = Run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionFlagAndCommand(t *testing.T) {
	for _, arg := range []string{"--version", "version"} {
		code, stdout, stderr := run(t, arg)
		if code != 0 {
			t.Errorf("%s: exit code %d, want 0 (stderr: %s)", arg, code, stderr)
		}
		if strings.TrimSpace(stdout) != Version {
			t.Errorf("%s: stdout = %q, want %q", arg, strings.TrimSpace(stdout), Version)
		}
	}
}

func TestNoArgsPrintsUsage(t *testing.T) {
	code, stdout, _ := run(t)
	if code != 0 {
		t.Errorf("exit code %d, want 0", code)
	}
	if !strings.Contains(stdout, "usage:") {
		t.Errorf("stdout does not contain usage:\n%s", stdout)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := run(t, "frobnicate")
	if code != 2 {
		t.Errorf("exit code %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Errorf("stderr does not explain the failure:\n%s", stderr)
	}
}

func TestDoctorOpensDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	code, stdout, stderr := run(t, "doctor", "--db", path)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	latest, err := store.SchemaVersion()
	if err != nil {
		t.Fatalf("store.SchemaVersion: %v", err)
	}
	wants := []string{
		path,
		fmt.Sprintf("schema   %d (latest %d)", latest, latest),
		"journal  wal",
		"tasks    0 pending, 0 total",
		"ok",
	}
	for _, want := range wants {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor output missing %q:\n%s", want, stdout)
		}
	}
}

// TestDoctorCountsTasks exercises the whole chain from the CLI down to SQLite:
// the store writes rows, doctor reads them back through Count.
func TestDoctorCountsTasks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	ctx := context.Background()
	pending, err := db.Add(ctx, "still open", task.Scope{Kind: task.ScopeGlobal})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := db.Add(ctx, "will be done", task.Scope{Kind: task.ScopeSession, Key: "pulsar"}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := db.Complete(ctx, pending.ID+1); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	db.Close()

	code, stdout, stderr := run(t, "doctor", "--db", path)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "tasks    1 pending, 2 total") {
		t.Errorf("doctor did not report the task counts:\n%s", stdout)
	}
}

func TestDoctorReportsBadPath(t *testing.T) {
	// A path whose parent is an existing regular file cannot be created.
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	code, _, stderr := run(t, "doctor", "--db", filepath.Join(file, "tasks.db"))
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	if stderr == "" {
		t.Error("failure produced no stderr")
	}
}

// TestTUIWiringSmoke covers runTUI's assembly — resolve the data dir, open the
// store, resolve scopes, build the tui.Config — which had no test at all.
//
// It cannot get as far as a rendered popup: Bubble Tea needs a TTY and a test
// process has none, so tui.Run fails at the last step. Everything before that
// step is real, which is the part worth covering: a panic or a nil store would
// surface here, and the "database is created" assertion proves the wiring ran
// rather than bailing early.
//
// XDG_DATA_HOME is redirected first. Without it this test would open — and
// create — the developer's real task database.
func TestTUIWiringSmoke(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	code, _, stderr := run(t, "tui")

	if code != 1 {
		t.Errorf("exit code %d, want 1 (no TTY in a test process)", code)
	}
	if stderr == "" {
		t.Error("the failure produced no stderr")
	}
	// The store was opened before the TUI was reached, so the wiring ran.
	db := filepath.Join(dir, store.AppDir, store.DBName)
	if _, err := os.Stat(db); err != nil {
		t.Errorf("runTUI did not open a database at %s: %v", db, err)
	}
}

// TestTUIReportsAnUnopenableDatabase — the error path around store.Open, which
// is the one failure a user can actually hit (an unwritable data dir).
func TestTUIReportsAnUnopenableDatabase(t *testing.T) {
	file := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(file, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	t.Setenv("XDG_DATA_HOME", file)

	code, _, stderr := run(t, "tui")
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	if !strings.Contains(stderr, "tdo:") {
		t.Errorf("stderr does not name the failure: %q", stderr)
	}
}
