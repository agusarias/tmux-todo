package scope

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agusarias/tmux-todo/internal/task"
)

// fakeSessionID is the id fakeTmux reports. Real tmux ids look like this.
const fakeSessionID = "$7"

// fakeTmux returns a Run that answers display-message with one call, the way
// tmux does, and records how many times it was invoked. The three lines are the
// three fields queryTmux asks for, in order.
func fakeTmux(t *testing.T, sessionName, panePath string, calls *int) func(string, ...string) ([]byte, error) {
	t.Helper()
	return func(name string, args ...string) ([]byte, error) {
		if calls != nil {
			*calls++
		}
		if name != "tmux" || len(args) < 2 || args[0] != "display-message" {
			return nil, fmt.Errorf("unexpected command %s %v", name, args)
		}
		return []byte(sessionName + "\n" + panePath + "\n" + fakeSessionID + "\n"), nil
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
	if got.SessionID != fakeSessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, fakeSessionID)
	}
	// One subprocess, not two: this runs on the popup's hot path.
	if calls != 1 {
		t.Errorf("tmux was queried %d times, want 1", calls)
	}
}

// TestResolveQueriesTmuxOnceForAllThreeFields is DoD 5: the session id rides on
// the existing display-message rather than adding a second subprocess to the
// popup's hot path. It asserts on the recorded argv, so folding the id in as a
// separate call fails here even though Resolve would still return the right
// values.
func TestResolveQueriesTmuxOnceForAllThreeFields(t *testing.T) {
	_, deep := repoFixture(t)
	var argv [][]string
	r := Resolver{
		TmuxEnv: "/tmp/tmux/default,1,0",
		Run: func(name string, args ...string) ([]byte, error) {
			argv = append(argv, append([]string{name}, args...))
			return []byte("pulsar\n" + deep + "\n$3\n"), nil
		},
	}

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(argv) != 1 {
		t.Fatalf("Resolve ran %d commands, want exactly 1: %v", len(argv), argv)
	}
	call := argv[0]
	if len(call) != 4 || call[0] != "tmux" || call[1] != "display-message" || call[2] != "-p" {
		t.Fatalf("call = %v, want tmux display-message -p <format>", call)
	}
	format := call[3]
	wantLines := []string{"#{session_name}", "#{pane_current_path}", "#{session_id}"}
	if lines := strings.Split(format, "\n"); !slicesEqual(lines, wantLines) {
		t.Errorf("format = %q, want the three fields one per line: %q", format, strings.Join(wantLines, "\\n"))
	}
	if got.SessionID != "$3" {
		t.Errorf("SessionID = %q, want $3", got.SessionID)
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// A tmux too old to report an id, or a format that came back short, must leave
// SessionID empty rather than inventing one — an empty id disables the rename
// map for that invocation, which is the best-effort behaviour design.md wants.
func TestResolveMissingSessionIDStaysEmpty(t *testing.T) {
	_, deep := repoFixture(t)
	r := Resolver{
		TmuxEnv: "/tmp/tmux/default,1,0",
		Run: func(string, ...string) ([]byte, error) {
			return []byte("pulsar\n" + deep + "\n"), nil
		},
	}

	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Session == nil {
		t.Fatal("Session should still resolve without an id")
	}
	if got.SessionID != "" {
		t.Errorf("SessionID = %q, want empty", got.SessionID)
	}
}

// TestSessionIDTargetsAnExactName pins the rename path's one subprocess. Both
// halves of the "=<name>:" target matter and were established against tmux 3.7b:
// "=" forces an exact match, and the trailing ":" is what makes tmux read the
// target as a session at all — "=dev" alone resolves to nothing and tmux says so
// by printing an empty line and exiting 0.
func TestSessionIDTargetsAnExactName(t *testing.T) {
	var argv [][]string
	r := Resolver{
		TmuxEnv: "/tmp/tmux/default,1,0",
		Run: func(name string, args ...string) ([]byte, error) {
			argv = append(argv, append([]string{name}, args...))
			return []byte("$4\n"), nil
		},
	}

	id, err := r.SessionID("pulsar")
	if err != nil {
		t.Fatalf("SessionID: %v", err)
	}
	if id != "$4" {
		t.Errorf("SessionID = %q, want $4 (trailing newline trimmed)", id)
	}
	if len(argv) != 1 {
		t.Fatalf("SessionID ran %d commands, want 1: %v", len(argv), argv)
	}
	want := []string{"tmux", "display-message", "-t", "=pulsar:", "-p", "#{session_id}"}
	if !slicesEqual(argv[0], want) {
		t.Errorf("call = %v, want %v", argv[0], want)
	}
}

// A session name with a space and a quote in it reaches tmux as one argv
// element. There is no shell in this path, which is the whole reason the hook
// passes a name and not an id — see the run-shell $0 trap in CLAUDE.md.
func TestSessionIDPassesMetacharactersThrough(t *testing.T) {
	const name = `it's a "session" $0; rm -rf`
	var got []string
	r := Resolver{
		TmuxEnv: "/tmp/tmux/default,1,0",
		Run: func(_ string, args ...string) ([]byte, error) {
			got = args
			return []byte("$5"), nil
		},
	}

	if _, err := r.SessionID(name); err != nil {
		t.Fatalf("SessionID: %v", err)
	}
	if len(got) != 5 || got[2] != "="+name+":" {
		t.Errorf("target = %q, want %q verbatim in one argument", got, "="+name+":")
	}
}

func TestSessionIDOutsideTmuxIsUnavailable(t *testing.T) {
	r := Resolver{
		TmuxEnv: "",
		Run: func(string, ...string) ([]byte, error) {
			t.Error("tmux was queried with $TMUX unset")
			return nil, errors.New("no tmux")
		},
	}
	if _, err := r.SessionID("pulsar"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("SessionID outside tmux = %v, want ErrUnavailable", err)
	}
}

func TestSessionIDErrors(t *testing.T) {
	base := Resolver{TmuxEnv: "/tmp/tmux/default,1,0"}

	if _, err := base.SessionID(""); err == nil {
		t.Error("SessionID accepted an empty name")
	}

	failing := base
	failing.Run = func(string, ...string) ([]byte, error) { return nil, errors.New("can't find session") }
	if _, err := failing.SessionID("gone"); err == nil {
		t.Error("SessionID swallowed a tmux failure")
	}

	silent := base
	silent.Run = func(string, ...string) ([]byte, error) { return []byte("  \n"), nil }
	if _, err := silent.SessionID("pulsar"); err == nil {
		t.Error("SessionID accepted an empty id as success")
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

// tmuxAlive reports whether a tmux server is actually reachable.
//
// That is not the same question as "$TMUX is set". The variable is inherited by
// every child process and outlives the server: ssh into a box where the session
// was killed, or a CI runner that inherited it, and $TMUX is non-empty while
// display-message fails. Gating on the variable alone made this test fail on
// exactly those machines —
// `TMUX="/tmp/fake,1,0" go test ./internal/scope/` used to report "Session is
// absent despite $TMUX being set", which was the test being wrong, not the code.
func tmuxAlive() bool {
	if os.Getenv("TMUX") == "" {
		return false
	}
	return exec.Command("tmux", "display-message", "-p", "#{session_name}").Run() == nil
}

// TestResolveAgainstRealEnvironment exercises the un-injected path with real
// exec and real cwd. Inside tmux it also covers the subprocess and reports the
// timing DoD 11 asks for; outside tmux it still checks that the real os.Getwd
// path resolves this checkout to its main repo.
func TestResolveAgainstRealEnvironment(t *testing.T) {
	r := NewResolver()

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
	if !tmuxAlive() {
		t.Log("no reachable tmux server: session scope is absent, as designed")
		if got.Session != nil {
			t.Errorf("Session = %+v with no tmux server reachable", *got.Session)
		}
		return
	}
	if got.Session == nil {
		t.Error("Session is absent despite a reachable tmux server")
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

// TestPackageResolveMatchesTheRealEnvironment covers the entry point every
// caller actually uses. It exists because the package shipped with
// `Resolve()` = `Resolver{}.Resolve()`, whose empty TmuxEnv made session scope
// permanently unavailable in the binary while all 28 tests passed — every one of
// them injected TmuxEnv by hand, so the suite validated the injected path and
// never the production default.
//
// The assertion is therefore deliberately about the *default wiring*, not about
// resolution logic that is already covered above: Resolve() must agree with an
// explicitly-wired Resolver, and must track the real environment.
func TestPackageResolveMatchesTheRealEnvironment(t *testing.T) {
	got, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	// The exact comparison the bug failed: the convenience entry point and a
	// hand-wired resolver must see the same world.
	wired, err := Resolver{TmuxEnv: os.Getenv("TMUX")}.Resolve()
	if err != nil {
		t.Fatalf("wired Resolve: %v", err)
	}
	if scopeString(got.Session) != scopeString(wired.Session) {
		t.Errorf("Resolve() session = %s, but Resolver{TmuxEnv: $TMUX} sees %s"+
			" — the package entry point is not wired to the real environment",
			scopeString(got.Session), scopeString(wired.Session))
	}

	if tmuxAlive() {
		if got.Session == nil {
			t.Fatal("Resolve() reports no session scope from inside a live tmux session")
		}
		out, err := exec.Command("tmux", "display-message", "-p", "#{session_name}").Output()
		if err != nil {
			t.Fatalf("ask tmux for the session name: %v", err)
		}
		if want := strings.TrimSpace(string(out)); got.Session.Key != want {
			t.Errorf("Resolve() session key = %q, want tmux's %q", got.Session.Key, want)
		}
		t.Logf("live tmux: Resolve() session = %s", scopeString(got.Session))
	} else if got.Session != nil {
		t.Errorf("Resolve() invented session scope %s with no reachable tmux server",
			scopeString(got.Session))
	}
}

// TestNewResolverCarriesTmuxEnv pins the constructor itself, so the default
// cannot regress behind a refactor of Resolve.
//
// It **fakes** $TMUX rather than comparing against whatever the host has. The
// first version of this test asserted `NewResolver().TmuxEnv == os.Getenv("TMUX")`,
// which on a machine with no tmux — every CI runner — is `"" == ""`: it passes
// against `return Resolver{}`, the exact bug it was written to catch. The whole
// regression net was invisible in the only environment it would ever run in.
// Measured, before this test changed: the mutated tree passed 36 of 36 with one
// skip under `env -u TMUX`.
//
// Both directions are pinned, because satisfying one by breaking the other is a
// refactor away: a constructor that hardcoded a non-empty string would pass the
// first leg, and the zero value passes the second.
func TestNewResolverCarriesTmuxEnv(t *testing.T) {
	t.Run("carries a set TMUX", func(t *testing.T) {
		// A sentinel the host cannot coincidentally match, so the assertion has
		// something to be wrong about wherever it runs. It is only ever read:
		// nothing here dials the socket.
		const fake = "/tmp/fake-tmux-socket,1,0"
		t.Setenv("TMUX", fake)

		if got := NewResolver().TmuxEnv; got != fake {
			t.Errorf("NewResolver().TmuxEnv = %q, want the faked $TMUX %q"+
				" — the constructor is not reading the environment, so session scope"+
				" is unreachable in the shipped binary", got, fake)
		}
		// The zero value is the trap this package shipped; it must stay
		// distinguishable so nobody reads Resolver{} as "the real environment".
		if zero := (Resolver{}); zero.TmuxEnv != "" {
			t.Error("Resolver{} picked up $TMUX; the injection seam is gone")
		}
	})

	t.Run("carries an unset TMUX", func(t *testing.T) {
		t.Setenv("TMUX", "")

		if got := NewResolver().TmuxEnv; got != "" {
			t.Errorf("NewResolver().TmuxEnv = %q with $TMUX unset, want empty", got)
		}
		// The other half of the contract: outside tmux there is no session
		// scope, rather than one with an empty key. Resolve() is the production
		// entry point, so this is the constructor's effect and not just its
		// field. It reaches no state dir — StickyDefault is what does that, and
		// it is not on this path.
		got, err := Resolve()
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got.Session != nil {
			t.Errorf("Resolve() invented session scope %s with $TMUX unset", scopeString(got.Session))
		}
		if got.Has(task.ScopeSession) {
			t.Error("Has(session) is true with $TMUX unset")
		}
	})
}

// TestStickyDefaultReachesSessionInTheRealEnvironment is the user-visible half
// of the same bug: StickyDefault degrades session -> dir off `rs.Session != nil`,
// so a permanently-nil Session silently downgraded every user's default.
func TestStickyDefaultReachesSessionInTheRealEnvironment(t *testing.T) {
	if !tmuxAlive() {
		t.Skip("needs a reachable tmux server")
	}
	r := NewResolver()
	r.StateDir = t.TempDir() // never touch the user's real sticky default

	rs, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if err := r.SetStickyDefault(task.ScopeSession); err != nil {
		t.Fatalf("SetStickyDefault: %v", err)
	}
	if got := r.StickyDefault(rs); got != task.ScopeSession {
		t.Errorf("StickyDefault = %q inside tmux with session stored, want %q"+
			" — session scope is unreachable through the production path", got, task.ScopeSession)
	}
}

func scopeString(s *task.Scope) string {
	if s == nil {
		return "<absent>"
	}
	return fmt.Sprintf("%s=%s", s.Kind, s.Key)
}

func BenchmarkResolve(b *testing.B) {
	r := NewResolver()
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

// TestLiveSessionsParsesOneNamePerLine — the format is one name per line, not a
// separated list, because a session name can contain spaces. Anything that
// splits on whitespace is wrong here, and the wrongness is invisible until
// somebody names a session "my project".
func TestLiveSessionsParsesOneNamePerLine(t *testing.T) {
	var argv [][]string
	r := Resolver{
		TmuxEnv: "/tmp/tmux/default,1,0",
		Run: func(name string, args ...string) ([]byte, error) {
			argv = append(argv, append([]string{name}, args...))
			return []byte("pulsar\nmy project\napi\n"), nil
		},
	}

	live := r.LiveSessions()
	for _, want := range []string{"pulsar", "my project", "api"} {
		if !live[want] {
			t.Errorf("LiveSessions is missing %q: %v", want, live)
		}
	}
	if len(live) != 3 {
		t.Errorf("LiveSessions = %v, want exactly the three names", live)
	}

	if len(argv) != 1 {
		t.Fatalf("LiveSessions ran %d commands, want 1: %v", len(argv), argv)
	}
	want := []string{"tmux", "list-sessions", "-F", "#{session_name}"}
	if !slicesEqual(argv[0], want) {
		t.Errorf("call = %v, want %v", argv[0], want)
	}
}

// TestLiveSessionsWithNoServerIsEmptyNotAnError — tmux absent, or no server
// running, means nothing is live. That is an answer, not a failure: the popup
// must still open, with every session group labelled "(not running)".
func TestLiveSessionsWithNoServerIsEmptyNotAnError(t *testing.T) {
	r := Resolver{
		TmuxEnv: "/tmp/tmux/default,1,0",
		Run: func(string, ...string) ([]byte, error) {
			return nil, errors.New("no server running on /private/tmp/tmux-501/default")
		},
	}
	if live := r.LiveSessions(); len(live) != 0 {
		t.Errorf("LiveSessions = %v with no server, want empty", live)
	}
}

// TestLiveSessionsDoesNotShortCircuitOutsideTmux — deliberately unlike
// queryTmux. `tdo tui` can run from a plain shell with a server up, and those
// sessions are still there to jump to; skipping the query outside tmux would make
// the outside-tmux jump impossible to label.
func TestLiveSessionsDoesNotShortCircuitOutsideTmux(t *testing.T) {
	var asked bool
	r := Resolver{
		TmuxEnv: "", // not inside tmux
		Run: func(string, ...string) ([]byte, error) {
			asked = true
			return []byte("pulsar\n"), nil
		},
	}
	if live := r.LiveSessions(); !live["pulsar"] {
		t.Errorf("LiveSessions = %v outside tmux, want the running session", live)
	}
	if !asked {
		t.Error("LiveSessions short circuited outside tmux; the outside-tmux jump needs the answer")
	}
}

// TestLiveSessionsAgainstARealServer is the production-path leg: an injected Run
// proves the parsing, not the wiring, and this repo has shipped that gap twice.
// It asserts against the tmux binary's own answer rather than against a fixture.
func TestLiveSessionsAgainstARealServer(t *testing.T) {
	if !tmuxAlive() {
		t.Skip("needs a reachable tmux server")
	}
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		t.Skipf("tmux list-sessions: %v", err)
	}
	live := NewResolver().LiveSessions()
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if name != "" && !live[name] {
			t.Errorf("tmux reports session %q running but LiveSessions does not: %v", name, live)
		}
	}
}

// EnvSession's whole point is the argv: the id comes out of $TMUX's third field
// and goes into a -t target, so tmux answers for the session the hook fired for
// rather than for whatever session it would pick with no client.
func TestEnvSessionTargetsTheIDFromTmuxEnv(t *testing.T) {
	var argv [][]string
	r := Resolver{
		TmuxEnv: "/private/tmp/tmux-501/default,52789,7",
		Run: func(name string, args ...string) ([]byte, error) {
			argv = append(argv, append([]string{name}, args...))
			return []byte("alpha2\n"), nil
		},
	}

	name, id, err := r.EnvSession()
	if err != nil {
		t.Fatalf("EnvSession: %v", err)
	}
	if name != "alpha2" || id != "$7" {
		t.Errorf("EnvSession = (%q, %q), want (\"alpha2\", \"$7\")", name, id)
	}
	if len(argv) != 1 {
		t.Fatalf("EnvSession ran %d commands, want 1: %v", len(argv), argv)
	}
	// No trailing ":" — a "$n" token is unambiguously a session id, unlike the
	// "=name" form which display-message parses as a pane name without one.
	want := []string{"tmux", "display-message", "-t", "$7", "-p", "#{session_name}"}
	if !slicesEqual(argv[0], want) {
		t.Errorf("call = %v, want %v", argv[0], want)
	}
}

// A name tmux cannot be asked to target as a *name* is reachable by id, which is
// the second reason the hook path goes through here: ":" is the character tmux
// splits a target on, so "=weird:name:" is unusable while "$2" is not.
func TestEnvSessionReachesANameWithAColon(t *testing.T) {
	r := Resolver{
		TmuxEnv: "/tmp/tmux/default,1,2",
		Run:     func(string, ...string) ([]byte, error) { return []byte("weird:name\n"), nil },
	}
	name, id, err := r.EnvSession()
	if err != nil {
		t.Fatalf("EnvSession: %v", err)
	}
	if name != "weird:name" || id != "$2" {
		t.Errorf("EnvSession = (%q, %q), want (\"weird:name\", \"$2\")", name, id)
	}
}

// Every unusable environment must fail, and must fail *without* querying tmux.
// Asking tmux anyway would mean asking it which session is current — the exact
// client-dependent question that made the hook resolve the wrong session.
func TestEnvSessionRefusesAnUnusableEnvironment(t *testing.T) {
	for _, tc := range []struct {
		name, env       string
		wantUnavailable bool
	}{
		{name: "outside tmux", env: "", wantUnavailable: true},
		{name: "no session field", env: "/tmp/tmux/default,1"},
		{name: "empty session field", env: "/tmp/tmux/default,1,"},
		{name: "socket path only", env: "/tmp/tmux/default"},
		{name: "session field carries the $ sigil", env: "/tmp/tmux/default,1,$3"},
		{name: "session field is a name", env: "/tmp/tmux/default,1,alpha"},
		{name: "session field has trailing junk", env: "/tmp/tmux/default,1,3x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Resolver{
				TmuxEnv: tc.env,
				Run: func(string, ...string) ([]byte, error) {
					t.Error("tmux was queried despite an unusable $TMUX — a fallback here" +
						" restores the client-dependent resolution this method replaces")
					return []byte("bravo\n"), nil
				},
			}
			name, id, err := r.EnvSession()
			if err == nil {
				t.Fatalf("EnvSession accepted %q, returning (%q, %q)", tc.env, name, id)
			}
			if tc.wantUnavailable && !errors.Is(err, ErrUnavailable) {
				t.Errorf("EnvSession outside tmux = %v, want ErrUnavailable", err)
			}
		})
	}
}

// tmux reports an unknown target by printing nothing and exiting 0 — the same
// trap SessionID documents — so an empty answer has to be caught here or the
// caller gets an empty session key.
func TestEnvSessionErrors(t *testing.T) {
	base := Resolver{TmuxEnv: "/tmp/tmux/default,1,3"}

	failing := base
	failing.Run = func(string, ...string) ([]byte, error) { return nil, errors.New("can't find session") }
	if _, _, err := failing.EnvSession(); err == nil {
		t.Error("EnvSession swallowed a tmux failure")
	}

	silent := base
	silent.Run = func(string, ...string) ([]byte, error) { return []byte("  \n"), nil }
	if _, _, err := silent.EnvSession(); err == nil {
		t.Error("EnvSession accepted an empty name as success")
	}
}

// The live leg: inside a real tmux, EnvSession must agree with what tmux itself
// says about the session $TMUX names. Skipped where there is no server, which is
// why the fake-argv tests above carry the load on CI.
func TestEnvSessionAgainstRealTmux(t *testing.T) {
	if !tmuxAlive() {
		t.Skip("no reachable tmux server")
	}
	env := os.Getenv("TMUX")
	if env == "" {
		t.Skip("not inside tmux")
	}
	r := Resolver{TmuxEnv: env}
	name, id, err := r.EnvSession()
	if err != nil {
		t.Fatalf("EnvSession: %v", err)
	}
	out, err := exec.Command("tmux", "display-message", "-t", id, "-p", "#{session_name}").Output()
	if err != nil {
		t.Fatalf("tmux display-message -t %s: %v", id, err)
	}
	if want := strings.TrimSpace(string(out)); name != want {
		t.Errorf("EnvSession name = %q, tmux says %q for %s", name, want, id)
	}
	if want := "$" + strings.Split(env, ",")[2]; id != want {
		t.Errorf("EnvSession id = %q, want %q from $TMUX", id, want)
	}
}
