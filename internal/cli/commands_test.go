package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agusarias/tmux-todo/internal/scope"
	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// fakeContext installs the resolution seam so a test depends on neither the test
// process's cwd nor a live tmux server. It also redirects the sticky default's
// state dir — a test must never read or write the developer's real one.
//
// An empty session means "not inside tmux", which is how the outside-tmux legs
// are expressed; an empty dir means no directory could be determined at all,
// which is the only way to exercise Ruling A for dir scope.
type fakeContext struct {
	session  string
	dir      string
	stateDir string
}

func (f fakeContext) install(t *testing.T) {
	t.Helper()
	restore := newResolver
	t.Cleanup(func() { newResolver = restore })
	stateDir := f.stateDir
	if stateDir == "" {
		stateDir = t.TempDir()
	}
	newResolver = func() scope.Resolver {
		r := scope.Resolver{StateDir: stateDir}
		if f.session != "" {
			r.TmuxEnv = "/tmp/fake-tmux,1,0"
			r.Run = func(name string, args ...string) ([]byte, error) {
				return []byte(f.session + "\n" + f.dir), nil
			}
		}
		r.Getwd = func() (string, error) {
			if f.dir == "" {
				return "", fmt.Errorf("no working directory")
			}
			return f.dir, nil
		}
		return r
	}
}

// dirKey is what the scope package will make of a path. t.TempDir() sits under a
// symlink on macOS (/var -> /private/var), so a test that hardcoded the temp
// path would compare against the wrong key.
func dirKey(t *testing.T, path string) string {
	t.Helper()
	key, err := scope.DirKey(path)
	if err != nil {
		t.Fatalf("scope.DirKey(%q): %v", path, err)
	}
	return key
}

func newDB(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "tasks.db")
}

// seed inserts tasks with explicit scopes, bypassing resolution.
func seed(t *testing.T, path string, tasks ...task.Task) {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()
	for _, want := range tasks {
		if _, err := db.Add(context.Background(), want.Text, want.Scope); err != nil {
			t.Fatalf("Add(%q): %v", want.Text, err)
		}
	}
}

// --- add -------------------------------------------------------------------

// TestAddHonoursScopeFlagInEitherOrder is the highest-value test in this file.
// docs/design.md writes the scope flag *after* the text, and stdlib flag stops
// parsing at the first positional — so without the hoist in parseArgs, the
// trailing form files the task under the sticky default and still exits 0. A
// task in the wrong scope with no error is the worst failure this CLI has.
func TestAddHonoursScopeFlagInEitherOrder(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"flag after text (the documented order)", []string{"text first", "--global"}},
		{"flag before text", []string{"--global", "text first"}},
		{"flag after text, --db in between", nil}, // filled in below
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)
			path := newDB(t)

			args := append([]string{"add", "--db", path}, tc.args...)
			if tc.args == nil {
				args = []string{"add", "text first", "--db", path, "--global"}
			}
			code, stdout, stderr := run(t, args...)
			if code != 0 {
				t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
			}
			if strings.TrimSpace(stdout) != "1" {
				t.Errorf("stdout = %q, want the new task's id", stdout)
			}

			got := listScopes(t, path)
			if len(got) != 1 || got[0].Kind != task.ScopeGlobal {
				t.Errorf("task landed in %+v, want global — the scope flag was dropped", got)
			}
		})
	}
}

// listScopes reads back the scopes actually stored, which is the only way to
// prove where a task landed.
func listScopes(t *testing.T, path string) []task.Scope {
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
	out := make([]task.Scope, 0, len(tasks))
	for _, tk := range tasks {
		out = append(out, tk.Scope)
	}
	return out
}

func TestAddFallsBackToTheStickyDefault(t *testing.T) {
	dir := t.TempDir()
	stateDir := t.TempDir()
	fakeContext{session: "pulsar", dir: dir, stateDir: stateDir}.install(t)

	// With nothing stored the default is session inside tmux.
	path := newDB(t)
	if code, _, stderr := run(t, "add", "--db", path, "no flag"); code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	if got := listScopes(t, path); len(got) != 1 || got[0].Kind != task.ScopeSession {
		t.Errorf("got %+v, want the session scope", got)
	}

	// A stored preference is honoured. The CLI reads the sticky default; it must
	// never write one, or a shell alias running `tdo add --global` would change
	// what the next popup add defaults to.
	if err := (scope.Resolver{StateDir: stateDir}).SetStickyDefault(task.ScopeDir); err != nil {
		t.Fatalf("SetStickyDefault: %v", err)
	}
	path = newDB(t)
	if code, _, stderr := run(t, "add", "--db", path, "no flag"); code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	want := task.Scope{Kind: task.ScopeDir, Key: dirKey(t, dir)}
	if got := listScopes(t, path); len(got) != 1 || got[0] != want {
		t.Errorf("got %+v, want %+v", got, want)
	}

	// And --global does not become the new sticky default.
	if code, _, _ := run(t, "add", "--db", newDB(t), "explicit", "--global"); code != 0 {
		t.Fatal("add --global failed")
	}
	path = newDB(t)
	if code, _, _ := run(t, "add", "--db", path, "no flag again"); code != 0 {
		t.Fatal("add failed")
	}
	if got := listScopes(t, path); len(got) != 1 || got[0] != want {
		t.Errorf("the CLI wrote the sticky default: got %+v, want %+v", got, want)
	}
}

func TestAddSessionOutsideTmuxFails(t *testing.T) {
	fakeContext{dir: t.TempDir()}.install(t)
	path := newDB(t)

	code, _, stderr := run(t, "add", "--db", path, "nope", "--session")
	if code != 1 {
		t.Errorf("exit code %d, want 1 — an explicit --session must not be degraded", code)
	}
	if !strings.Contains(stderr, "not inside tmux") {
		t.Errorf("stderr does not name the reason: %q", stderr)
	}
	if got := listScopes(t, path); len(got) != 0 {
		t.Errorf("a task was filed anyway: %+v", got)
	}
}

// TestAddSessionOutsideTmuxThroughTheRealSeam covers the production wiring
// rather than the injected one. The seam's whole point is determinism, but a
// test that only ever exercises the fake cannot notice that newResolver stopped
// being scope.NewResolver — the exact class of bug that shipped twice in this
// repo (see the Resolver doc comment).
func TestAddSessionOutsideTmuxThroughTheRealSeam(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	code, _, stderr := run(t, "add", "--db", newDB(t), "nope", "--session")
	if code != 1 {
		t.Errorf("exit code %d, want 1 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stderr, "not inside tmux") {
		t.Errorf("stderr does not name the reason: %q", stderr)
	}
}

// TestAddRealSeamSeesTmux is the other half: with $TMUX set, the production
// resolver must reach for a session. Asserting only the outside-tmux leg would
// pass against a resolver that is permanently tmux-blind — which is precisely
// the bug that shipped.
func TestAddRealSeamSeesTmux(t *testing.T) {
	t.Setenv("TMUX", "/tmp/definitely-not-a-real-tmux-socket,1,0")
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	// The socket is fake, so the tmux query fails and session scope stays
	// unavailable — but the failure must come from tmux, not from the resolver
	// having never looked. NewResolver reading $TMUX is what this pins: with a
	// zero-value Resolver the code short-circuits before running anything.
	if got := newResolver().TmuxEnv; got == "" {
		t.Error("newResolver() did not read $TMUX — session scope is unreachable in the binary")
	}
}

func TestAddUsageErrors(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)
	path := newDB(t)

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"no text", []string{"add", "--db", path}, "needs the task text"},
		{"unquoted text", []string{"add", "--db", path, "fix", "flaky", "test"}, "exactly one"},
		{"two scope flags", []string{"add", "--db", path, "x", "--global", "--dir"}, "mutually exclusive"},
		{"typo'd flag", []string{"add", "--db", path, "x", "-sesion"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := run(t, tc.args...)
			if code != 2 {
				t.Errorf("exit code %d, want 2 (stderr: %s)", code, stderr)
			}
			if tc.want != "" && !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want it to mention %q", stderr, tc.want)
			}
		})
	}
	if got := listScopes(t, path); len(got) != 0 {
		t.Errorf("a usage error still wrote a task: %+v", got)
	}
}

// TestAddTextStartingWithADash pins the resolution of a genuine tension.
//
// Task text that starts with a dash cannot also be flag-typo protection: if
// leading-dash positionals were absorbed as text, then `tdo add x -sesion` would
// file a task called "-sesion" and exit 0, which is the silent-wrong-scope
// failure this whole command is built to avoid. So a dash-leading token is a
// flag, the usage error is the point, and "--" is the escape hatch — the same
// contract every other Unix CLI offers.
func TestAddTextStartingWithADash(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)

	// Without "--": a usage error, not a mangled task.
	path := newDB(t)
	code, _, stderr := run(t, "add", "--db", path, "-n looks like a flag", "--global")
	if code != 2 {
		t.Errorf("exit code %d, want 2 (stderr: %s)", code, stderr)
	}
	if got := listScopes(t, path); len(got) != 0 {
		t.Errorf("a dash-leading token was absorbed as task text: %+v", got)
	}

	// With "--": stored verbatim. The flag goes before the separator, as it must.
	path = newDB(t)
	code, _, stderr = run(t, "add", "--db", path, "--global", "--", "-n looks like a flag")
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()
	tasks, err := db.List(context.Background(), store.Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(tasks) != 1 || tasks[0].Text != "-n looks like a flag" {
		t.Fatalf("got %+v, want the dash-leading text stored verbatim", tasks)
	}
	if tasks[0].Scope.Kind != task.ScopeGlobal {
		t.Errorf("scope = %v, want global — the flag before -- was dropped", tasks[0].Scope.Kind)
	}
}

// --- list ------------------------------------------------------------------

// TestListMergedSetIsInTierOrder pins DoD 3's ordering. The ordering itself is
// store.List's SQL, so this is a guard that the CLI does not re-sort — and that
// the flagless case populates Filter.Scopes rather than leaving it empty, which
// the store reads as "every scope".
func TestListMergedSetIsInTierOrder(t *testing.T) {
	dir := t.TempDir()
	fakeContext{session: "pulsar", dir: dir}.install(t)
	path := newDB(t)
	seed(t, path,
		task.Task{Text: "global one", Scope: task.Scope{Kind: task.ScopeGlobal}},
		task.Task{Text: "dir one", Scope: task.Scope{Kind: task.ScopeDir, Key: dirKey(t, dir)}},
		task.Task{Text: "session one", Scope: task.Scope{Kind: task.ScopeSession, Key: "pulsar"}},
		task.Task{Text: "session two", Scope: task.Scope{Kind: task.ScopeSession, Key: "pulsar"}},
		// An inactive scope: present in the database, absent from the merged set.
		task.Task{Text: "other session", Scope: task.Scope{Kind: task.ScopeSession, Key: "api"}},
	)

	code, stdout, stderr := run(t, "list", "--db", path)
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	got := textsOf(stdout)
	// Session tier newest-first, then dir, then global.
	want := []string{"session two", "session one", "dir one", "global one"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("rows out of order.\ngot:  %v\nwant: %v\n%s", got, want, stdout)
	}
	if strings.Contains(stdout, "other session") {
		t.Error("the flagless list leaked an inactive scope — Filter.Scopes was left empty")
	}
}

// textsOf pulls the task text off each plain-output row. The text is everything
// after the aligned id / marker / scope columns, so it is recovered by finding
// the marker and skipping the scope column.
func textsOf(stdout string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimRight(stdout, "\n"), "\n") {
		if line == "" {
			continue
		}
		_, rest, ok := strings.Cut(line, "] ")
		if !ok {
			continue
		}
		_, text, ok := strings.Cut(strings.TrimSpace(rest), "  ")
		if !ok {
			continue
		}
		out = append(out, strings.TrimSpace(text))
	}
	return out
}

func TestListScopeAllIncludesInactiveScopes(t *testing.T) {
	dir := t.TempDir()
	fakeContext{session: "pulsar", dir: dir}.install(t)
	path := newDB(t)
	seed(t, path,
		task.Task{Text: "mine", Scope: task.Scope{Kind: task.ScopeSession, Key: "pulsar"}},
		task.Task{Text: "theirs", Scope: task.Scope{Kind: task.ScopeSession, Key: "api"}},
		task.Task{Text: "elsewhere", Scope: task.Scope{Kind: task.ScopeDir, Key: "/somewhere/else"}},
	)

	code, stdout, stderr := run(t, "list", "--db", path, "--scope=all")
	if code != 0 {
		t.Fatalf("exit code %d, want 0 (stderr: %s)", code, stderr)
	}
	for _, want := range []string{"mine", "theirs", "elsewhere"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("--scope=all did not include %q:\n%s", want, stdout)
		}
	}
}

func TestListNarrowsToOneScope(t *testing.T) {
	dir := t.TempDir()
	fakeContext{session: "pulsar", dir: dir}.install(t)
	path := newDB(t)
	seed(t, path,
		task.Task{Text: "a global", Scope: task.Scope{Kind: task.ScopeGlobal}},
		task.Task{Text: "a dir", Scope: task.Scope{Kind: task.ScopeDir, Key: dirKey(t, dir)}},
		task.Task{Text: "a session", Scope: task.Scope{Kind: task.ScopeSession, Key: "pulsar"}},
	)

	for flag, want := range map[string]string{
		"--scope=session": "a session",
		"--scope=dir":     "a dir",
		"--scope=global":  "a global",
	} {
		code, stdout, stderr := run(t, "list", "--db", path, flag)
		if code != 0 {
			t.Fatalf("%s: exit code %d, want 0 (stderr: %s)", flag, code, stderr)
		}
		if got := textsOf(stdout); len(got) != 1 || got[0] != want {
			t.Errorf("%s: got %v, want exactly [%q]", flag, got, want)
		}
	}
}

// TestListUnavailableScopeFails is Ruling A: a read of an unavailable scope fails
// the way a write does. An empty list with exit 0 would be indistinguishable
// from a scope that genuinely has no tasks.
func TestListUnavailableScopeFails(t *testing.T) {
	path := newDB(t)
	seed(t, path, task.Task{Text: "something", Scope: task.Scope{Kind: task.ScopeGlobal}})

	for _, tc := range []struct {
		name  string
		ctx   fakeContext
		flag  string
		wants string
	}{
		{"session outside tmux", fakeContext{dir: t.TempDir()}, "--scope=session", "not inside tmux"},
		{"dir with no working directory", fakeContext{session: "pulsar"}, "--scope=dir", "no working directory"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.ctx.install(t)
			for _, cmd := range []string{"list", "count"} {
				code, stdout, stderr := run(t, cmd, "--db", path, tc.flag)
				if code != 1 {
					t.Errorf("%s: exit code %d, want 1", cmd, code)
				}
				if !strings.Contains(stderr, tc.wants) {
					t.Errorf("%s: stderr = %q, want it to mention %q", cmd, stderr, tc.wants)
				}
				if stdout != "" {
					t.Errorf("%s: wrote %q to stdout on a failure", cmd, stdout)
				}
			}
		})
	}
}

func TestListAndCountRejectAnUnknownScope(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)
	path := newDB(t)
	for _, cmd := range []string{"list", "count"} {
		code, _, stderr := run(t, cmd, "--db", path, "--scope=project")
		if code != 2 {
			t.Errorf("%s: exit code %d, want 2", cmd, code)
		}
		if !strings.Contains(stderr, `unknown scope "project"`) {
			t.Errorf("%s: stderr = %q", cmd, stderr)
		}
	}
}

func TestListHidesDoneUntilAllIsGiven(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)
	path := newDB(t)
	seed(t, path,
		task.Task{Text: "pending one", Scope: task.Scope{Kind: task.ScopeGlobal}},
		task.Task{Text: "finished one", Scope: task.Scope{Kind: task.ScopeGlobal}},
	)
	if code, _, stderr := run(t, "done", "--db", path, "2"); code != 0 {
		t.Fatalf("done: exit %d (stderr: %s)", code, stderr)
	}

	code, stdout, _ := run(t, "list", "--db", path)
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	if strings.Contains(stdout, "finished one") {
		t.Errorf("the default list showed a completed task:\n%s", stdout)
	}
	if !strings.Contains(stdout, "[ ] ") {
		t.Errorf("no pending marker in:\n%s", stdout)
	}

	code, stdout, _ = run(t, "list", "--db", path, "--all")
	if code != 0 {
		t.Fatalf("--all: exit code %d, want 0", code)
	}
	if !strings.Contains(stdout, "finished one") || !strings.Contains(stdout, "[x] ") {
		t.Errorf("--all did not show the completed task with its marker:\n%s", stdout)
	}
}

func TestListEmptyIsSilentAndSucceeds(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)
	path := newDB(t)

	code, stdout, stderr := run(t, "list", "--db", path)
	if code != 0 || stdout != "" {
		t.Errorf("exit %d, stdout %q — want 0 and nothing (stderr: %s)", code, stdout, stderr)
	}
	code, stdout, _ = run(t, "list", "--db", path, "--json")
	if code != 0 || stdout != "{\"tasks\":[]}\n" {
		t.Errorf("exit %d, stdout %q", code, stdout)
	}
}

func TestListRejectsPositionalArguments(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)
	if code, _, _ := run(t, "list", "--db", newDB(t), "session"); code != 2 {
		t.Errorf("exit code %d, want 2 — a stray positional is a typo, not a filter", code)
	}
}

// --- done / rm -------------------------------------------------------------

func TestDoneAndRm(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)
	path := newDB(t)
	seed(t, path,
		task.Task{Text: "to complete", Scope: task.Scope{Kind: task.ScopeGlobal}},
		task.Task{Text: "to delete", Scope: task.Scope{Kind: task.ScopeGlobal}},
	)

	if code, _, stderr := run(t, "done", "--db", path, "1"); code != 0 {
		t.Fatalf("done: exit %d (stderr: %s)", code, stderr)
	}
	if code, _, stderr := run(t, "rm", "--db", path, "2"); code != 0 {
		t.Fatalf("rm: exit %d (stderr: %s)", code, stderr)
	}

	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	one, err := db.Get(ctx, 1)
	if err != nil {
		t.Fatalf("Get(1): %v", err)
	}
	if !one.Done || one.DoneAt == nil {
		t.Errorf("task 1 is not completed: %+v", one)
	}
	if _, err := db.Get(ctx, 2); err == nil {
		t.Error("task 2 survived rm")
	}
}

// TestDoneAndRmOnAnUnknownIDFail — exit 0 on a no-op would tell the user
// something happened when nothing did.
func TestDoneAndRmOnAnUnknownIDFail(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)
	path := newDB(t)
	seed(t, path, task.Task{Text: "the only task", Scope: task.Scope{Kind: task.ScopeGlobal}})

	for _, cmd := range []string{"done", "rm"} {
		code, _, stderr := run(t, cmd, "--db", path, "999")
		if code != 1 {
			t.Errorf("%s 999: exit code %d, want 1", cmd, code)
		}
		if !strings.Contains(stderr, "999") || !strings.Contains(stderr, "not found") {
			t.Errorf("%s: stderr = %q, want it to name the id and the reason", cmd, stderr)
		}
	}
	// The real task is untouched.
	if got := listScopes(t, path); len(got) != 1 {
		t.Errorf("a failed mutation changed the database: %+v", got)
	}
}

func TestDoneAndRmUsageErrors(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)
	path := newDB(t)
	for _, args := range [][]string{
		{"done", "--db", path},
		{"done", "--db", path, "notanumber"},
		{"done", "--db", path, "1", "2"},
		{"rm", "--db", path},
		{"rm", "--db", path, "seven"},
	} {
		if code, _, stderr := run(t, args...); code != 2 {
			t.Errorf("%v: exit code %d, want 2 (stderr: %s)", args, code, stderr)
		}
	}
}

// TestMutationFlagAfterTheID — the same order-independence add needs, since
// `tdo done 7 --db /tmp/x` is a natural thing to type.
func TestMutationFlagAfterTheID(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)
	path := newDB(t)
	seed(t, path, task.Task{Text: "x", Scope: task.Scope{Kind: task.ScopeGlobal}})

	if code, _, stderr := run(t, "done", "1", "--db", path); code != 0 {
		t.Fatalf("exit %d, want 0 (stderr: %s) — --db after the id was dropped", code, stderr)
	}
}

// --- count -----------------------------------------------------------------

func TestCountTotalAndPending(t *testing.T) {
	dir := t.TempDir()
	fakeContext{session: "pulsar", dir: dir}.install(t)
	path := newDB(t)
	seed(t, path,
		task.Task{Text: "a", Scope: task.Scope{Kind: task.ScopeGlobal}},
		task.Task{Text: "b", Scope: task.Scope{Kind: task.ScopeSession, Key: "pulsar"}},
		task.Task{Text: "c", Scope: task.Scope{Kind: task.ScopeSession, Key: "api"}}, // inactive
	)
	if code, _, stderr := run(t, "done", "--db", path, "1"); code != 0 {
		t.Fatalf("done: exit %d (stderr: %s)", code, stderr)
	}

	for _, tc := range []struct {
		args []string
		want string
	}{
		// The active merged set: the "api" session task is not in it.
		{[]string{"count", "--db", path}, "2"},
		{[]string{"count", "--db", path, "--pending"}, "1"},
		{[]string{"count", "--db", path, "--scope=all"}, "3"},
		{[]string{"count", "--db", path, "--scope=session"}, "1"},
		{[]string{"count", "--db", path, "--scope=global", "--pending"}, "0"},
	} {
		code, stdout, stderr := run(t, tc.args...)
		if code != 0 {
			t.Fatalf("%v: exit %d (stderr: %s)", tc.args, code, stderr)
		}
		if stdout != tc.want+"\n" {
			t.Errorf("%v: stdout = %q, want a bare %q and a newline", tc.args, stdout, tc.want)
		}
	}
}

// --- shared plumbing -------------------------------------------------------

func TestEveryCommandTakesDB(t *testing.T) {
	fakeContext{session: "pulsar", dir: t.TempDir()}.install(t)
	dir := t.TempDir()
	// A --db path nothing else has touched must be created, which is how we know
	// the flag was honoured rather than the XDG default being used.
	for i, args := range [][]string{
		{"add", "x", "--global"},
		{"list"},
		{"count"},
	} {
		path := filepath.Join(dir, fmt.Sprintf("db%d.sqlite", i))
		code, _, stderr := run(t, append(args, "--db", path)...)
		if code != 0 {
			t.Fatalf("%v: exit %d (stderr: %s)", args, code, stderr)
		}
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%v: --db was ignored, no database at %s", args, path)
		}
	}
}

func TestUsageListsEveryCommand(t *testing.T) {
	code, stdout, _ := run(t, "help")
	if code != 0 {
		t.Fatalf("exit code %d, want 0", code)
	}
	for _, want := range []string{"add ", "list ", "done <id>", "rm <id>", "count ", "--json", "--scope=", "--db"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("usage does not mention %q:\n%s", want, stdout)
		}
	}
}
