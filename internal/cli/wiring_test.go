package cli

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/agusarias/tmux-todo/internal/scope"
	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
	"github.com/agusarias/tmux-todo/internal/tui"
)

// The wiring tests below assert what runTUI actually *puts into* tui.Config.
//
// internal/tui is comprehensively tested against an injected Config; nothing
// tested the code that builds the real one, which is the shape of bug this repo
// has already shipped twice (scope.Resolve() was Resolver{}.Resolve() while 28
// tests passed, because every one of them injected TmuxEnv by hand). SetSticky
// has exactly one production construction site: runTUI could pass nil and the
// sticky default would silently stop persisting with the whole suite green.
//
// Two rules keep these tests from becoming the vacuous guard they exist to
// replace:
//
//   - Every exported field of tui.Config must appear in wiringChecks.
//     TestEveryTUIConfigFieldIsAsserted walks the struct with reflect and fails
//     on any field the map does not mention, so a new field breaks this test
//     until someone either asserts it or records why it needs no assertion.
//   - An assertion has to be able to fail against a plausibly-broken runTUI.
//     "non-nil" is not enough for a func field — a stub that does nothing is
//     non-nil — so SetSticky is asserted by round-tripping through the state dir.

// wiringFixture is the environment runTUI resolves against. Everything in it is
// a temp dir or a fake subprocess: the test must touch neither the developer's
// database, nor their sticky preference, nor a live tmux server.
type wiringFixture struct {
	// session is the tmux session name. Empty means the fixture is outside
	// tmux, which is how the DoD-5 leg is expressed.
	session string
	// live is what `tmux list-sessions` answers, and so what LiveSessions
	// should be built from. It is deliberately not the same set as session:
	// a wiring bug that passed the resolved session instead would show up.
	live []string
	// sticky is the preference seeded into the state dir before the run, or ""
	// for none.
	sticky task.ScopeKind
	// stickyView is the view preference seeded into the state dir before the
	// run. It is deliberately `true` in the main fixture, because `false` is
	// both the zero value of the field and what a *missing* preference reads
	// as — an assertion against false would pass with the wiring deleted.
	stickyView bool
	// popupKey is the raw @todo-key the plugin would have put in the popup's
	// environment, or "" for "no TDO_POPUP_KEY at all".
	//
	// It is a tmux spelling ("C-l"), not a Bubble Tea one, so the assertion
	// covers the translation as well as the plumbing — and it is deliberately
	// something other than the empty string the real environment has, because
	// `os.Getenv` returns "" on any CI runner and an assertion against "" would
	// pass with the wiring deleted. That is the vacuous-environment-guard trap
	// CLAUDE.md records for $TMUX.
	popupKey string

	// Filled in by install.
	dir       string
	home      string
	stateDir  string
	dbPath    string
	sessionID string
	tmuxCalls int
	// dbProbe is the result of querying the store *while the popup is
	// running*. runTUI closes the database on its way out, so a check made
	// after the run can only ever see a closed handle.
	dbProbe error
	// copyCalls records what the Config's Copy actually ran, and copyTTYBuf
	// what it wrote to the terminal. Both seams are substituted for the whole
	// test, so the Copy check can prove the injected func reaches tmux rather
	// than merely being non-nil — and so no test can put a task on the
	// developer's real clipboard.
	copyCalls  *[]copyCall
	copyTTYBuf *strings.Builder
}

func (f *wiringFixture) install(t *testing.T) *wiringFixture {
	t.Helper()

	dataHome := t.TempDir()
	f.dir = t.TempDir()
	f.home = t.TempDir()
	f.stateDir = t.TempDir()
	f.dbPath = filepath.Join(dataHome, store.AppDir, store.DBName)
	f.sessionID = "$7"

	t.Setenv("XDG_DATA_HOME", dataHome)
	t.Setenv("HOME", f.home)
	// StateDir is set on the resolver below, so this should never be consulted.
	// It is redirected anyway because SetSticky's assertion is a *write* path:
	// if StateDir were ever dropped, the fallback must still not be the
	// developer's real state dir.
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	if f.session == "" {
		unsetenv(t, "TMUX")
	} else {
		t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	}

	if f.popupKey == "" {
		unsetenv(t, popupKeyEnv)
	} else {
		t.Setenv(popupKeyEnv, f.popupKey)
	}

	if f.sticky != "" {
		// Seeded through the same public API the popup writes with. What is
		// under test here is runTUI's wiring, not scope's round trip.
		if err := (scope.Resolver{StateDir: f.stateDir}).SetStickyDefault(f.sticky); err != nil {
			t.Fatalf("seed sticky default: %v", err)
		}
	}
	if f.stickyView {
		if err := (scope.Resolver{StateDir: f.stateDir}).SetStickyAllTasks(true); err != nil {
			t.Fatalf("seed sticky view: %v", err)
		}
	}

	// Substituted before the run and for the whole test: the Copy assertion
	// calls cfg.Copy after runTUI returns, and a live copyRunner there would
	// shell out to tmux and overwrite the developer's paste buffer.
	f.copyCalls = fakeCopyRunner(t)
	f.copyTTYBuf = fakeTTY(t, nil)

	restore := newResolver
	t.Cleanup(func() { newResolver = restore })
	newResolver = func() scope.Resolver {
		return scope.Resolver{
			// Read from the environment exactly as scope.NewResolver does, so
			// the outside-tmux leg really does depend on $TMUX being unset.
			TmuxEnv:  os.Getenv("TMUX"),
			StateDir: f.stateDir,
			Run:      f.run,
			Getwd:    func() (string, error) { return f.dir, nil },
		}
	}
	return f
}

// run answers the two tmux queries a `tdo tui` run makes. Dispatching on the
// subcommand is what keeps the fake honest: answering both with the same string
// would hide a caller asking the wrong question.
func (f *wiringFixture) run(_ string, args ...string) ([]byte, error) {
	f.tmuxCalls++
	switch args[0] {
	case "display-message":
		return []byte(f.session + "\n" + f.dir + "\n" + f.sessionID + "\n"), nil
	case "list-sessions":
		return []byte(strings.Join(f.live, "\n") + "\n"), nil
	default:
		return nil, fmt.Errorf("unexpected tmux call: %v", args)
	}
}

// wantScopes is Resolved.Active() for this fixture, spelled out rather than
// obtained from the resolver: an assertion built by calling the same code the
// subject calls proves only that the code agrees with itself.
func (f *wiringFixture) wantScopes(t *testing.T) []task.Scope {
	t.Helper()
	dirKey, err := scope.DirKey(f.dir)
	if err != nil {
		t.Fatalf("scope.DirKey(%q): %v", f.dir, err)
	}
	var out []task.Scope
	if f.session != "" {
		out = append(out, task.Scope{Kind: task.ScopeSession, Key: f.session})
	}
	out = append(out, task.Scope{Kind: task.ScopeDir, Key: dirKey})
	return append(out, task.Scope{Kind: task.ScopeGlobal})
}

// wantLive is LiveSessions for this fixture. Nil when nothing is live, matching
// scope.LiveSessions' documented "nothing is known to be running".
func (f *wiringFixture) wantLive() map[string]bool {
	if len(f.live) == 0 {
		return nil
	}
	out := map[string]bool{}
	for _, name := range f.live {
		out[name] = true
	}
	return out
}

// runTUIAndCaptureConfig runs `tdo tui` with the popup stubbed out and returns
// the Config it was handed.
func runTUIAndCaptureConfig(t *testing.T, f *wiringFixture) tui.Config {
	t.Helper()
	var got tui.Config
	var calls int
	f.dbProbe = errNotProbed
	stubTUIProgram(t, func(c tui.Config) (tui.Jump, error) {
		calls++
		got = c
		// Asked here rather than in the DB check below: runTUI defers
		// closeDB, so the only moment the store is open is the one the popup
		// itself runs in.
		if c.DB != nil {
			_, f.dbProbe = c.DB.Count(context.Background(), store.Filter{})
		}
		return tui.Jump{}, nil
	})
	code, _, stderr := run(t, "tui")
	if code != 0 {
		t.Fatalf("exit code %d, want 0: %s", code, stderr)
	}
	if calls != 1 {
		t.Fatalf("the substituted program ran %d times, want 1 — nothing was captured", calls)
	}
	return got
}

// errNotProbed marks a fixture whose store was never queried, so a capture
// helper that stopped probing cannot pass the DB check by leaving a nil error.
var errNotProbed = errors.New("the store was never queried")

// wiringCheck asserts one field of the Config runTUI built. It is handed the
// field's name so the nil-is-correct helper can read the field generically.
type wiringCheck func(t *testing.T, name string, cfg tui.Config, f *wiringFixture)

// nilIsCorrect records that a field is deliberately left unset, with the reason
// in the source. It is a named helper rather than an omission from the map so
// that "no assertion needed" stays a decision someone made, which is the whole
// point of forcing every field to appear.
func nilIsCorrect(reason string) wiringCheck {
	return func(t *testing.T, name string, cfg tui.Config, _ *wiringFixture) {
		t.Helper()
		v := reflect.ValueOf(cfg).FieldByName(name)
		if !v.IsNil() {
			t.Errorf("%s = %v, want nil: %s", name, v, reason)
		}
	}
}

// The checks below deliberately do not call t.Helper(): each runs in its own
// subtest, and marking them helpers would report every failure at the dispatch
// line instead of the assertion that fired.
//
// wiringChecks is the field-name-to-assertion table. Every exported field of
// tui.Config must have an entry; see TestEveryTUIConfigFieldIsAsserted.
var wiringChecks = map[string]wiringCheck{
	"DB": func(t *testing.T, _ string, cfg tui.Config, f *wiringFixture) {
		if cfg.DB == nil {
			t.Fatal("DB is nil — the popup has no store to read")
		}
		if got := cfg.DB.Path(); got != f.dbPath {
			t.Errorf("DB.Path() = %q, want %q", got, f.dbPath)
		}
		// Open, not merely non-nil: a closed handle is also non-nil. The
		// probe was taken inside the stub, while the popup was "running".
		if f.dbProbe != nil {
			t.Errorf("the store handed to the popup is not usable: %v", f.dbProbe)
		}
	},

	"Scopes": func(t *testing.T, _ string, cfg tui.Config, f *wiringFixture) {
		want := f.wantScopes(t)
		if !reflect.DeepEqual(cfg.Scopes, want) {
			t.Errorf("Scopes = %+v, want %+v (tier order: session, dir, global)", cfg.Scopes, want)
		}
	},

	"Home": func(t *testing.T, _ string, cfg tui.Config, f *wiringFixture) {
		// The fixture points $HOME at a temp dir so this cannot pass as "" == "".
		if cfg.Home != f.home {
			t.Errorf("Home = %q, want %q", cfg.Home, f.home)
		}
	},

	"Version": func(t *testing.T, _ string, cfg tui.Config, _ *wiringFixture) {
		if cfg.Version != Version {
			t.Errorf("Version = %q, want %q", cfg.Version, Version)
		}
		if cfg.Version == "" {
			t.Error("Version is empty — the help overlay would show no version at all")
		}
	},

	"DefaultScope": func(t *testing.T, _ string, cfg tui.Config, f *wiringFixture) {
		// The fixture seeded a preference; the popup must start on it. The
		// degradation case is TestTUIConfigDefaultScopeDegrades.
		if cfg.DefaultScope != f.sticky {
			t.Errorf("DefaultScope = %q, want the stored sticky default %q", cfg.DefaultScope, f.sticky)
		}
	},

	"SetSticky": func(t *testing.T, _ string, cfg tui.Config, f *wiringFixture) {
		if cfg.SetSticky == nil {
			t.Fatal("SetSticky is nil — the sticky default would silently stop persisting")
		}
		// Behavioural, not non-nil: func(task.ScopeKind) error { return nil }
		// is non-nil and is exactly the bug shape worth catching. The write
		// must land where the resolver reads it back from.
		want := task.ScopeGlobal
		if f.sticky == want {
			t.Fatalf("fixture seeds %q, so a no-op SetSticky would pass this check", want)
		}
		if err := cfg.SetSticky(want); err != nil {
			t.Fatalf("SetSticky(%q): %v", want, err)
		}
		// A Resolved with every tier available, so StickyDefault's degradation
		// cannot rewrite what was stored.
		all := scope.Resolved{
			Session: &task.Scope{Kind: task.ScopeSession, Key: "any"},
			Dir:     &task.Scope{Kind: task.ScopeDir, Key: "/any"},
			Global:  task.Scope{Kind: task.ScopeGlobal},
		}
		if got := (scope.Resolver{StateDir: f.stateDir}).StickyDefault(all); got != want {
			t.Errorf("after SetSticky(%q) the state dir reads back %q — the write did not land", want, got)
		}
	},

	"LiveSessions": func(t *testing.T, _ string, cfg tui.Config, f *wiringFixture) {
		want := f.wantLive()
		if !reflect.DeepEqual(cfg.LiveSessions, want) {
			t.Errorf("LiveSessions = %v, want %v", cfg.LiveSessions, want)
		}
	},

	"Now": nilIsCorrect("the popup defaults it to time.Now; only tests inject a clock"),

	// The close key comes out of the popup's environment and through the
	// translation, so this asserts both halves at once: the fixture exports a
	// tmux spelling and the Config must carry the Bubble Tea one.
	//
	// The empty case is asserted by TestTUIConfigWiringWithNoPopupKey rather than
	// here, because "" is what a deleted wiring line also produces.
	"CloseKey": func(t *testing.T, _ string, cfg tui.Config, f *wiringFixture) {
		want := popupKey(f.popupKey)
		if want == "" {
			t.Fatalf("the fixture's popupKey %q does not translate, so this assertion could"+
				" not fail — pick one that does", f.popupKey)
		}
		if cfg.CloseKey != want {
			t.Errorf("CloseKey = %q, want %q from %s=%q", cfg.CloseKey, want, popupKeyEnv, f.popupKey)
		}
		// ...and it must not be the raw tmux name: that would never match a key
		// the popup sees, so the popup would silently never close.
		if cfg.CloseKey == f.popupKey {
			t.Errorf("CloseKey = %q, the untranslated tmux name — Update compares against"+
				" Bubble Tea key strings, so this would never fire", cfg.CloseKey)
		}
	},

	// The stored view preference must reach the popup. The fixture seeds `true`
	// precisely because `false` is the zero value AND what an unreadable file
	// degrades to, so an assertion against false would pass with the wiring line
	// deleted — the same vacuous-environment trap CloseKey documents for $TMUX.
	"AllTasks": func(t *testing.T, _ string, cfg tui.Config, f *wiringFixture) {
		if !f.stickyView {
			t.Fatal("the fixture seeds no view preference, so this assertion could not fail")
		}
		if !cfg.AllTasks {
			t.Error("AllTasks = false with the all-tasks view stored — the popup would open" +
				" in the merged list and `g` would be a keystroke on every open again")
		}
	},

	// Behavioural, not non-nil — the SetSticky rule. A stub that does nothing is
	// non-nil and is exactly the bug worth catching, so this writes through the
	// injected func and reads it back with a fresh Resolver.
	"SetAllTasks": func(t *testing.T, _ string, cfg tui.Config, f *wiringFixture) {
		if cfg.SetAllTasks == nil {
			t.Fatal("SetAllTasks is nil — the view would silently stop being remembered")
		}
		// The neighbouring scope preference is read BEFORE the write and
		// compared after, rather than against the value the fixture seeded:
		// wiringChecks runs as subtests over a map, so the SetSticky entry may
		// already have rewritten it. Comparing against a seeded constant made
		// this check fail on iteration order, which is a property of the test
		// and not of the code.
		all := scope.Resolved{
			Session: &task.Scope{Kind: task.ScopeSession, Key: "any"},
			Dir:     &task.Scope{Kind: task.ScopeDir, Key: "/any"},
			Global:  task.Scope{Kind: task.ScopeGlobal},
		}
		scopeBefore := (scope.Resolver{StateDir: f.stateDir}).StickyDefault(all)

		// Write the value the fixture did NOT seed, so a no-op setter fails.
		want := !f.stickyView
		if err := cfg.SetAllTasks(want); err != nil {
			t.Fatalf("SetAllTasks(%v): %v", want, err)
		}
		if got := (scope.Resolver{StateDir: f.stateDir}).StickyAllTasks(); got != want {
			t.Errorf("after SetAllTasks(%v) the state dir reads back %v — the write did not land",
				want, got)
		}
		// ...and it must not have disturbed the scope preference beside it.
		if got := (scope.Resolver{StateDir: f.stateDir}).StickyDefault(all); got != scopeBefore {
			t.Errorf("writing the view changed the stored scope default from %q to %q",
				scopeBefore, got)
		}
	},

	// Behavioural, not non-nil — same rule as SetSticky, and for the same
	// reason: `func(string) error { return nil }` is non-nil and is precisely
	// the bug shape worth catching. What is asserted is that the injected Copy
	// really loads a tmux buffer, with the text on stdin, because the fixture
	// is inside tmux.
	"Copy": func(t *testing.T, _ string, cfg tui.Config, f *wiringFixture) {
		if cfg.Copy == nil {
			t.Fatal("Copy is nil — y would be inert in the popup")
		}
		const text = `it's a "$HOME" task`
		if err := cfg.Copy(text); err != nil {
			t.Fatalf("Copy(%q): %v", text, err)
		}
		calls := *f.copyCalls
		if len(calls) != 1 {
			t.Fatalf("Copy made %d runs, want 1: %+v", len(calls), calls)
		}
		got := calls[0]
		want := []string{"load-buffer", "-w", "-"}
		if got.name != "tmux" || !reflect.DeepEqual(got.args, want) {
			t.Errorf("Copy ran %q %v, want tmux %v", got.name, got.args, want)
		}
		if got.stdin != text {
			t.Errorf("stdin = %q, want the text verbatim %q", got.stdin, text)
		}
		// Inside tmux the buffer is the whole path; an OSC 52 write here would
		// mean the branch read the environment wrongly.
		if f.copyTTYBuf.Len() != 0 {
			t.Errorf("Copy also wrote %q to the terminal; inside tmux it must not",
				f.copyTTYBuf.String())
		}
	},
}

// TestEveryTUIConfigFieldIsAsserted is the guard that keeps wiringChecks
// honest. It walks tui.Config with reflect and looks each field up in the map —
// never the reverse, which would be vacuous, since every entry trivially
// matches itself.
//
// Adding a field to tui.Config fails this test until someone either asserts it
// or records with nilIsCorrect why it needs no assertion.
func TestEveryTUIConfigFieldIsAsserted(t *testing.T) {
	typ := reflect.TypeOf(tui.Config{})
	seen := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		seen[field.Name] = true
		if _, ok := wiringChecks[field.Name]; !ok {
			t.Errorf("tui.Config.%s (%s) has no entry in wiringChecks: nothing asserts what"+
				" runTUI puts in it. Add an assertion, or nilIsCorrect with the reason.",
				field.Name, field.Type)
		}
	}
	for name := range wiringChecks {
		if !seen[name] {
			t.Errorf("wiringChecks has an entry for %q, which is not an exported field of"+
				" tui.Config — the assertion is dead", name)
		}
	}
}

// TestTUIConfigWiring is the in-tmux leg: every field of the Config runTUI
// built, checked against the environment the fixture set up.
func TestTUIConfigWiring(t *testing.T) {
	f := (&wiringFixture{
		session:    "work",
		live:       []string{"work", "spare"},
		sticky:     task.ScopeDir,
		stickyView: true,
		popupKey:   "C-l",
	}).install(t)

	cfg := runTUIAndCaptureConfig(t, f)

	for name, check := range wiringChecks {
		t.Run(name, func(t *testing.T) { check(t, name, cfg, f) })
	}
}

// TestTUIConfigWiringOutsideTmux is DoD 5: with $TMUX unset there is no session
// scope to be had, and the popup must not start a new task on one.
//
// Absent beats empty — a session scope with a "" key would be a database key
// nobody could ever resolve again.
func TestTUIConfigWiringOutsideTmux(t *testing.T) {
	f := (&wiringFixture{
		// No session, but a tmux server is still answering list-sessions:
		// `tdo tui` from a plain shell can still jump to a live session.
		live:   []string{"work"},
		sticky: task.ScopeGlobal,
	}).install(t)

	cfg := runTUIAndCaptureConfig(t, f)

	for _, s := range cfg.Scopes {
		if s.Kind == task.ScopeSession {
			t.Errorf("Scopes contains a session scope %+v outside tmux", s)
		}
	}
	if want := f.wantScopes(t); !reflect.DeepEqual(cfg.Scopes, want) {
		t.Errorf("Scopes = %+v, want %+v", cfg.Scopes, want)
	}
	if cfg.DefaultScope == task.ScopeSession {
		t.Error("DefaultScope is session outside tmux — a new task would be filed to a scope" +
			" the user cannot submit to")
	}
	if cfg.DefaultScope != task.ScopeGlobal {
		t.Errorf("DefaultScope = %q, want the stored sticky default %q", cfg.DefaultScope, task.ScopeGlobal)
	}
	if got, want := cfg.LiveSessions, f.wantLive(); !reflect.DeepEqual(got, want) {
		t.Errorf("LiveSessions = %v, want %v — liveness does not depend on being inside tmux", got, want)
	}

	// The other half of the copy branch. wiringChecks asserts the in-tmux path;
	// without this leg a runTUI that always returned loadBuffer would pass
	// every assertion in the suite, and `y` from a plain shell would be a dead
	// key that reported success.
	if cfg.Copy == nil {
		t.Fatal("Copy is nil outside tmux — y would be inert in a hand-run `tdo tui`")
	}
	const text = `it's a "$HOME" task`
	if err := cfg.Copy(text); err != nil {
		t.Fatalf("Copy(%q) outside tmux: %v", text, err)
	}
	if calls := *f.copyCalls; len(calls) != 0 {
		t.Errorf("Copy ran tmux %d times outside tmux: %+v", len(calls), calls)
	}
	escape := f.copyTTYBuf.String()
	if escape == "" {
		t.Fatal("Copy wrote nothing to the terminal outside tmux")
	}
	payload := strings.TrimSuffix(strings.TrimPrefix(escape, "\x1b]52;c;"), "\a")
	decoded, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatalf("the escape %q does not carry a base64 payload: %v", escape, err)
	}
	if string(decoded) != text {
		t.Errorf("the escape's payload decodes to %q, want %q", decoded, text)
	}
}

// TestTUIConfigDefaultScopeDegrades is DoD 3's second half: a stored preference
// naming a scope this context does not have must fall back to the first
// available one, never seed a scope the user cannot submit to.
func TestTUIConfigDefaultScopeDegrades(t *testing.T) {
	f := (&wiringFixture{
		// Sticky says session; the fixture is outside tmux, so there is none.
		sticky: task.ScopeSession,
	}).install(t)

	cfg := runTUIAndCaptureConfig(t, f)

	if len(cfg.Scopes) == 0 {
		t.Fatal("Scopes is empty — every context has at least the global scope")
	}
	if cfg.DefaultScope != cfg.Scopes[0].Kind {
		t.Errorf("DefaultScope = %q, want the first available scope %q", cfg.DefaultScope, cfg.Scopes[0].Kind)
	}
	if cfg.DefaultScope != task.ScopeDir {
		t.Errorf("DefaultScope = %q, want %q (session is unavailable, dir is next)",
			cfg.DefaultScope, task.ScopeDir)
	}
	if want := f.wantScopes(t); !reflect.DeepEqual(cfg.Scopes, want) {
		t.Errorf("Scopes = %+v, want %+v", cfg.Scopes, want)
	}
}

// unsetenv removes a variable for the duration of one test. t.Setenv is what
// registers the restore; the unset is what the test actually wants.
func unsetenv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
}

// TestTUIConfigWiringWithNoPopupKey is DoD 8 at the wiring level: a hand-run
// `tdo tui`, or a prefix-table install, sets no TDO_POPUP_KEY, and the popup must
// get no close key rather than some default.
//
// Its companion in internal/tui proves "" is inert; this one proves runTUI
// actually produces "" instead of, say, the literal env-var name.
func TestTUIConfigWiringWithNoPopupKey(t *testing.T) {
	f := (&wiringFixture{session: "work", live: []string{"work"}}).install(t)

	cfg := runTUIAndCaptureConfig(t, f)

	if cfg.CloseKey != "" {
		t.Errorf("CloseKey = %q with no %s in the environment, want empty", cfg.CloseKey, popupKeyEnv)
	}
}

// TestTUIConfigWiringIgnoresAnUntranslatableKey — a @todo-key the translation
// does not understand must reach the popup as "" rather than as itself. A raw
// tmux name in CloseKey would be a key that never fires; worse, a partially
// translated one could shadow a real binding.
func TestTUIConfigWiringIgnoresAnUntranslatableKey(t *testing.T) {
	f := (&wiringFixture{session: "work", live: []string{"work"}, popupKey: "F13"}).install(t)

	cfg := runTUIAndCaptureConfig(t, f)

	if cfg.CloseKey != "" {
		t.Errorf("CloseKey = %q for an unsupported key name, want empty", cfg.CloseKey)
	}
}

// TestTUIConfigWiringWithNoStoredView is the other direction of DoD 7: a state
// dir that has never seen this preference must open the merged list.
//
// Its companion in wiringChecks proves `true` reaches the popup; this one proves
// runTUI actually produces `false` from an absent file rather than the assertion
// there passing on a zero value it never set.
func TestTUIConfigWiringWithNoStoredView(t *testing.T) {
	f := (&wiringFixture{session: "work", live: []string{"work"}, sticky: task.ScopeDir}).install(t)

	cfg := runTUIAndCaptureConfig(t, f)

	if cfg.AllTasks {
		t.Error("AllTasks = true with no stored preference, want the merged list")
	}
	if cfg.SetAllTasks == nil {
		t.Error("SetAllTasks is nil — a first-time user's view would never be remembered")
	}
}

// TestTUIConfigWiringWithACorruptStoredView — a preference file holding junk must
// degrade to the merged list, through the real wiring rather than only through
// internal/scope's own unit test. This is the "a corrupt one-word file must never
// stop the popup from opening" rule, asserted at the level a user would feel it.
func TestTUIConfigWiringWithACorruptStoredView(t *testing.T) {
	f := (&wiringFixture{session: "work", live: []string{"work"}, sticky: task.ScopeDir}).install(t)
	if err := os.MkdirAll(f.stateDir, 0o755); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(f.stateDir, "default-view"), []byte("\x00garbage"), 0o644); err != nil {
		t.Fatalf("seed a corrupt preference: %v", err)
	}

	cfg := runTUIAndCaptureConfig(t, f)

	if cfg.AllTasks {
		t.Error("AllTasks = true for a corrupt preference file, want the merged list")
	}
}
