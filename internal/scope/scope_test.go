package scope

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agusarias/tmux-todo/internal/task"
)

// fakeTmux returns a Run that answers display-message with one call, the way
// tmux does, and records how many times it was invoked.
func fakeTmux(t *testing.T, sessionName, panePath string, calls *int) func(string, ...string) ([]byte, error) {
	t.Helper()
	return func(name string, args ...string) ([]byte, error) {
		if calls != nil {
			*calls++
		}
		if name != "tmux" || len(args) < 2 || args[0] != "display-message" {
			return nil, fmt.Errorf("unexpected command %s %v", name, args)
		}
		return []byte(sessionName + "\n" + panePath + "\n"), nil
	}
}

func repoFixture(t *testing.T) (repo, deep string) {
	t.Helper()
	repo = mkdirAll(t, filepath.Join(t.TempDir(), "repo"))
	mkdirAll(t, filepath.Join(repo, ".git"))
	deep = mkdirAll(t, filepath.Join(repo, "pkg"))
	return repo, deep
}

func TestResolveInsideTmux(t *testing.T) {
	repo, deep := repoFixture(t)
	calls := 0
	r := Resolver{
		TmuxEnv: "/private/tmp/tmux-501/default,1234,0",
		Run:     fakeTmux(t, "pulsar", deep, &calls),
		Getwd:   func() (string, error) { return "", errors.New("should not be called") },
	}

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Session == nil {
		t.Fatal("Session is absent inside tmux")
	}
	if got.Session.Kind != task.ScopeSession || got.Session.Key != "pulsar" {
		t.Errorf("Session = %+v, want session/pulsar", *got.Session)
	}
	if got.Dir == nil {
		t.Fatal("Dir is absent")
	}
	if want := norm(t, repo); got.Dir.Key != want {
		t.Errorf("Dir key = %q, want the repo root %q", got.Dir.Key, want)
	}
	if got.Global.Kind != task.ScopeGlobal || got.Global.Key != "" {
		t.Errorf("Global = %+v, want global with an empty key", got.Global)
	}
	// One subprocess, not two: this runs on the popup's hot path.
	if calls != 1 {
		t.Errorf("tmux was queried %d times, want 1", calls)
	}
}

func TestResolveOutsideTmuxHasNoSession(t *testing.T) {
	repo, deep := repoFixture(t)
	r := Resolver{
		TmuxEnv: "",
		Run: func(string, ...string) ([]byte, error) {
			t.Error("tmux was queried with $TMUX unset")
			return nil, errors.New("no tmux")
		},
		Getwd: func() (string, error) { return deep, nil },
	}

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Session != nil {
		t.Errorf("Session = %+v outside tmux, want absent", *got.Session)
	}
	if got.Dir == nil {
		t.Fatal("Dir is absent; it should fall back to the working directory")
	}
	if want := norm(t, repo); got.Dir.Key != want {
		t.Errorf("Dir key = %q, want %q", got.Dir.Key, want)
	}

	if _, err := got.Lookup(task.ScopeSession); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Lookup(session) = %v, want ErrUnavailable", err)
	}
	if got.Has(task.ScopeSession) {
		t.Error("Has(session) is true outside tmux")
	}
}

func TestResolveFallsBackWhenTmuxQueryFails(t *testing.T) {
	repo, deep := repoFixture(t)
	r := Resolver{
		TmuxEnv: "/private/tmp/tmux-501/default,1,0",
		Run:     func(string, ...string) ([]byte, error) { return nil, errors.New("server gone") },
		Getwd:   func() (string, error) { return deep, nil },
	}

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Session != nil {
		t.Errorf("Session = %+v after a failed query, want absent", *got.Session)
	}
	if got.Dir == nil || got.Dir.Key != norm(t, repo) {
		t.Errorf("Dir = %+v, want the repo root from Getwd", got.Dir)
	}
}

// An empty session name must never become an empty-string key in the database.
func TestResolveEmptySessionNameStaysAbsent(t *testing.T) {
	_, deep := repoFixture(t)
	r := Resolver{
		TmuxEnv: "/tmp/tmux/default,1,0",
		Run:     fakeTmux(t, "", deep, nil),
	}

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Session != nil {
		t.Errorf("Session = %+v for an empty name, want absent", *got.Session)
	}
}

func TestResolveWithNoPathAtAll(t *testing.T) {
	r := Resolver{
		TmuxEnv: "",
		Getwd:   func() (string, error) { return "", errors.New("cwd is gone") },
	}

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Dir != nil {
		t.Errorf("Dir = %+v with no determinable path, want absent", *got.Dir)
	}
	if got.Session != nil {
		t.Error("Session should be absent")
	}
	if got.Global.Kind != task.ScopeGlobal {
		t.Error("Global must always be present")
	}
	if _, err := got.Lookup(task.ScopeDir); !errors.Is(err, ErrUnavailable) {
		t.Errorf("Lookup(dir) = %v, want ErrUnavailable", err)
	}
	// Global alone is still a usable list.
	if active := got.Active(); len(active) != 1 || active[0].Kind != task.ScopeGlobal {
		t.Errorf("Active() = %+v, want just global", active)
	}
}

// A pane path that has been deleted must not become a key.
func TestResolveIgnoresUnresolvablePanePath(t *testing.T) {
	gone := filepath.Join(t.TempDir(), "deleted")
	r := Resolver{
		TmuxEnv: "/tmp/tmux/default,1,0",
		Run:     fakeTmux(t, "pulsar", gone, nil),
		Getwd:   func() (string, error) { return "", errors.New("no cwd either") },
	}

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Dir != nil {
		t.Errorf("Dir = %+v for a deleted path, want absent", *got.Dir)
	}
	if got.Session == nil {
		t.Error("Session should still resolve when only the path is broken")
	}
}

func TestActiveIsInTierOrder(t *testing.T) {
	session := task.Scope{Kind: task.ScopeSession, Key: "pulsar"}
	dir := task.Scope{Kind: task.ScopeDir, Key: "/ws/pulsar"}
	rs := Resolved{
		Session: &session,
		Dir:     &dir,
		Global:  task.Scope{Kind: task.ScopeGlobal},
	}

	got := rs.Active()
	want := []task.ScopeKind{task.ScopeSession, task.ScopeDir, task.ScopeGlobal}
	if len(got) != len(want) {
		t.Fatalf("Active() returned %d scopes, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Kind != want[i] {
			t.Errorf("Active()[%d] = %q, want %q", i, got[i].Kind, want[i])
		}
	}

	// Absent scopes are skipped, not zero-filled.
	rs.Session = nil
	if got := rs.Active(); len(got) != 2 || got[0].Kind != task.ScopeDir {
		t.Errorf("Active() without a session = %+v, want dir then global", got)
	}
}

func TestLookupUnknownKind(t *testing.T) {
	rs := Resolved{Global: task.Scope{Kind: task.ScopeGlobal}}
	if _, err := rs.Lookup(task.ScopeKind("project")); err == nil {
		t.Error("Lookup accepted an unknown kind")
	}
}

// TestResolveAgainstRealEnvironment exercises the un-injected path: the zero
// Resolver, real exec, real cwd. Inside tmux it also covers the subprocess and
// reports the timing DoD 11 asks for; outside tmux it still checks that the real
// os.Getwd path resolves this checkout to its main repo.
func TestResolveAgainstRealEnvironment(t *testing.T) {
	r := Resolver{TmuxEnv: os.Getenv("TMUX")}

	start := time.Now()
	got, err := r.Resolve()
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	t.Logf("resolved in %s (TMUX=%q)", elapsed, os.Getenv("TMUX"))
	t.Logf("  session: %v", scopeString(got.Session))
	t.Logf("  dir:     %v", scopeString(got.Dir))

	if got.Dir == nil {
		t.Fatal("Dir is absent in the real environment")
	}
	// The test runs from the package directory inside a worktree, so the key
	// must be the main repo root — not the worktree, and not the package dir.
	if strings.HasSuffix(got.Dir.Key, filepath.Join("internal", "scope")) {
		t.Errorf("Dir key %q is the package directory, not a repo root", got.Dir.Key)
	}
	if os.Getenv("TMUX") == "" {
		t.Log("not inside tmux: session scope is absent, as designed")
		if got.Session != nil {
			t.Errorf("Session = %+v with $TMUX unset", *got.Session)
		}
		return
	}
	if got.Session == nil {
		t.Error("Session is absent despite $TMUX being set")
	}
	// Deliberately no wall-clock assertion here. The measured cold median is
	// ~5.7ms against a ~10ms budget, but the tail reaches 12ms on a busy machine,
	// and a threshold that fails one run in ten trains everyone to ignore the
	// suite. The budget is verified by the recorded measurement in the brief's
	// Evidence, not by this test — same call the scaffold task made for cold start.
	if elapsed > time.Second {
		t.Errorf("resolution took %s, which is not latency but a hang", elapsed)
	}
}

func scopeString(s *task.Scope) string {
	if s == nil {
		return "<absent>"
	}
	return fmt.Sprintf("%s=%s", s.Kind, s.Key)
}

func BenchmarkResolve(b *testing.B) {
	r := Resolver{TmuxEnv: os.Getenv("TMUX")}
	for b.Loop() {
		if _, err := r.Resolve(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDirKey(b *testing.B) {
	wd, err := os.Getwd()
	if err != nil {
		b.Fatal(err)
	}
	for b.Loop() {
		if _, err := DirKey(wd); err != nil {
			b.Fatal(err)
		}
	}
}
