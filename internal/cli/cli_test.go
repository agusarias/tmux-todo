package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agusarias/tmux-todo/internal/config"
	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
	"github.com/agusarias/tmux-todo/internal/tui"
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

// stubTUIProgram replaces the popup for the duration of one test.
//
// It exists because the popup must not actually start. TestTUIWiringSmoke used
// to rely on tui.Run *failing* for want of a terminal — which is not true inside
// tmux, where Bubble Tea opens /dev/tty directly, so the popup rendered into the
// developer's pane and the test hung until the timeout panic. See runTUIProgram.
func stubTUIProgram(t *testing.T, fn func(tui.Config) (tui.Jump, error)) {
	t.Helper()
	prev := runTUIProgram
	runTUIProgram = fn
	t.Cleanup(func() { runTUIProgram = prev })
}

// TestTUIWiringSmoke covers runTUI's assembly — resolve the data dir, open the
// store, resolve scopes, build the tui.Config — which had no test at all.
//
// The popup is stubbed out, so what is covered is everything up to and including
// the call: a panic or a nil store surfaces here, the "database is created"
// assertion proves the wiring ran rather than bailing early, and the call count
// proves it did not bail *late* either.
//
// That count is also the guard on the fix: it is the *seam* that keeps
// `make test` from hanging inside tmux, so a change that inlined tui.Run again
// would silently restore the hang — and a hang is the one failure mode a test
// suite cannot report on itself.
//
// What the tui.Config contains is deliberately not asserted here; that is
// docs/tasks/2026-08-20-assert-tui-config-wiring.md.
//
// XDG_DATA_HOME is redirected first. Without it this test would open — and
// create — the developer's real task database.
func TestTUIWiringSmoke(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)

	var calls int
	stubTUIProgram(t, func(tui.Config) (tui.Jump, error) {
		calls++
		return tui.Jump{}, nil
	})

	code, _, stderr := run(t, "tui")

	if code != 0 {
		t.Errorf("exit code %d, want 0: %s", code, stderr)
	}
	if stderr != "" {
		t.Errorf("a clean run wrote to stderr: %q", stderr)
	}
	if calls != 1 {
		t.Errorf("the substituted program ran %d times, want 1 — runTUI is not going"+
			" through runTUIProgram, so `go test` inside tmux starts a real popup and hangs", calls)
	}
	// The store was opened before the TUI was reached, so the wiring ran.
	db := filepath.Join(dir, store.AppDir, store.DBName)
	if _, err := os.Stat(db); err != nil {
		t.Errorf("runTUI did not open a database at %s: %v", db, err)
	}
}

// TestTUIErrorFromTheProgramIsReported — the seam must not swallow the failure
// path it replaced. A queued delete that failed on the way out reaches the exit
// code through here, and reporting "deleted" for a row still in the database
// would be worse than the delete not happening.
func TestTUIErrorFromTheProgramIsReported(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	stubTUIProgram(t, func(tui.Config) (tui.Jump, error) {
		return tui.Jump{}, errors.New("commit queued deletes: disk on fire")
	})

	code, _, stderr := run(t, "tui")
	if code != 1 {
		t.Errorf("exit code %d, want 1", code)
	}
	if !strings.Contains(stderr, "disk on fire") {
		t.Errorf("stderr does not carry the failure: %q", stderr)
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

// TestDoctorReportsTheConfig — DoD 3. A setting that falls back silently is the
// failure this repo keeps shipping, so doctor is where a typo becomes visible.
//
// The seeded file mixes two good lines with two bad ones, because the useful
// property is that doctor reports *both* halves: the effective values (which are
// what the popup will actually do) and the lines that did not survive (which are
// what the user thought they were setting).
func TestDoctorReportsTheConfig(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	dir := filepath.Join(configHome, config.AppDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	body := "complete-to-bottom always\nfollow-on-complet true\nfollow-on-uncomplete maybe\n"
	configPath := filepath.Join(dir, config.FileName)
	if err := os.WriteFile(configPath, []byte(body), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	path := filepath.Join(t.TempDir(), "tasks.db")
	code, stdout, stderr := run(t, "doctor", "--db", path)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}

	wants := []string{
		configPath,
		// The one good line took effect...
		"complete-to-bottom   always",
		// ...and the two bad ones fell back to their defaults...
		"follow-on-complete   false",
		"follow-on-uncomplete true",
		// ...while still being named, with their line numbers.
		"line 2: unknown setting: follow-on-complet true",
		"line 3: want true or false: follow-on-uncomplete maybe",
	}
	for _, want := range wants {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor output missing %q:\n%s", want, stdout)
		}
	}
}

// TestDoctorReportsTheConfigPathWithNoFile — the first question about a setting
// that is not working is which file the binary is even looking at, so the path is
// printed whether or not one exists, and a machine without one reports no
// problems rather than an error.
func TestDoctorReportsTheConfigPathWithNoFile(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	path := filepath.Join(t.TempDir(), "tasks.db")
	code, stdout, stderr := run(t, "doctor", "--db", path)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if want := filepath.Join(configHome, config.AppDir, config.FileName); !strings.Contains(stdout, want) {
		t.Errorf("doctor did not print the config path %q:\n%s", want, stdout)
	}
	// The shipped defaults, which is what a machine with no config file runs.
	for _, want := range []string{"complete-to-bottom   on-start", "follow-on-complete   false", "follow-on-uncomplete true"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("doctor output missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "         ! ") {
		t.Errorf("doctor reported a problem with no config file present:\n%s", stdout)
	}
}
