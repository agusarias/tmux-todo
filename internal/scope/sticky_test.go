package scope

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agusarias/tmux-todo/internal/task"
)

// resolvedFixtures describe which scopes exist, for the degradation table.
func inTmux() Resolved {
	session := task.Scope{Kind: task.ScopeSession, Key: "pulsar"}
	dir := task.Scope{Kind: task.ScopeDir, Key: "/ws/pulsar"}
	return Resolved{Session: &session, Dir: &dir, Global: task.Scope{Kind: task.ScopeGlobal}}
}

func outsideTmux() Resolved {
	dir := task.Scope{Kind: task.ScopeDir, Key: "/ws/pulsar"}
	return Resolved{Dir: &dir, Global: task.Scope{Kind: task.ScopeGlobal}}
}

func globalOnly() Resolved {
	return Resolved{Global: task.Scope{Kind: task.ScopeGlobal}}
}

// TestStickyDefaultRoundTrip writes with one Resolver and reads with another, so
// nothing is served from in-process state — the popup and the CLI are separate
// processes sharing only the file.
func TestStickyDefaultRoundTrip(t *testing.T) {
	dir := t.TempDir()

	for _, kind := range task.ScopeKinds() {
		if err := (Resolver{StateDir: dir}).SetStickyDefault(kind); err != nil {
			t.Fatalf("SetStickyDefault(%s): %v", kind, err)
		}
		fresh := Resolver{StateDir: dir}
		if got, ok := fresh.storedStickyDefault(); !ok || got != kind {
			t.Errorf("stored default = %q/%v, want %q/true", got, ok, kind)
		}
		// And through the public path, with every scope available so nothing degrades.
		if got := fresh.StickyDefault(inTmux()); got != kind {
			t.Errorf("StickyDefault = %q, want %q", got, kind)
		}
	}
}

func TestStickyDefaultFallbackWithNothingStored(t *testing.T) {
	r := Resolver{StateDir: t.TempDir()}

	if got := r.StickyDefault(inTmux()); got != task.ScopeSession {
		t.Errorf("in tmux with nothing stored = %q, want session", got)
	}
	if got := r.StickyDefault(outsideTmux()); got != task.ScopeDir {
		t.Errorf("outside tmux with nothing stored = %q, want dir", got)
	}
	if got := r.StickyDefault(globalOnly()); got != task.ScopeGlobal {
		t.Errorf("with only global available = %q, want global", got)
	}
}

// TestStickyDefaultDegrades is DoD 9: a stored preference that cannot be honored
// falls to the next tier down rather than erroring or filing the task wrongly.
func TestStickyDefaultDegrades(t *testing.T) {
	for _, tc := range []struct {
		name   string
		stored task.ScopeKind
		ctx    Resolved
		want   task.ScopeKind
	}{
		{"session stored, no session", task.ScopeSession, outsideTmux(), task.ScopeDir},
		{"session stored, nothing but global", task.ScopeSession, globalOnly(), task.ScopeGlobal},
		{"dir stored, no dir", task.ScopeDir, globalOnly(), task.ScopeGlobal},
		{"global stored, everything available", task.ScopeGlobal, inTmux(), task.ScopeGlobal},
		{"dir stored, everything available", task.ScopeDir, inTmux(), task.ScopeDir},
		{"session stored, in tmux", task.ScopeSession, inTmux(), task.ScopeSession},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Resolver{StateDir: t.TempDir()}
			if err := r.SetStickyDefault(tc.stored); err != nil {
				t.Fatalf("SetStickyDefault: %v", err)
			}
			if got := r.StickyDefault(tc.ctx); got != tc.want {
				t.Errorf("StickyDefault = %q, want %q", got, tc.want)
			}
		})
	}
}

// A corrupt one-word file must never stop the popup from opening.
func TestStickyDefaultToleratesBadFiles(t *testing.T) {
	for name, body := range map[string]string{
		"empty":       "",
		"whitespace":  "   \n",
		"unknown":     "project\n",
		"garbage":     "\x00\x01binary",
		"two-values":  "session dir\n",
		"with-a-path": "dir=/ws/pulsar\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, stickyFile), []byte(body), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			r := Resolver{StateDir: dir}
			if _, ok := r.storedStickyDefault(); ok {
				t.Errorf("%q was accepted as a stored kind", body)
			}
			// No error surfaces; the fallback simply applies.
			if got := r.StickyDefault(inTmux()); got != task.ScopeSession {
				t.Errorf("StickyDefault = %q, want the session fallback", got)
			}
		})
	}
}

func TestStickyDefaultMissingDirectoryIsNotAnError(t *testing.T) {
	r := Resolver{StateDir: filepath.Join(t.TempDir(), "never", "created")}
	if got := r.StickyDefault(outsideTmux()); got != task.ScopeDir {
		t.Errorf("StickyDefault = %q, want dir", got)
	}
}

// TestSetStickyDefaultIsAtomic checks the temp-file-and-rename write leaves no
// debris and no partial file.
func TestSetStickyDefaultIsAtomic(t *testing.T) {
	dir := t.TempDir()
	r := Resolver{StateDir: dir}

	if err := r.SetStickyDefault(task.ScopeDir); err != nil {
		t.Fatalf("SetStickyDefault: %v", err)
	}
	if err := r.SetStickyDefault(task.ScopeGlobal); err != nil {
		t.Fatalf("SetStickyDefault (overwrite): %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read state dir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != stickyFile {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("state dir holds %v, want just %q", names, stickyFile)
	}

	body, err := os.ReadFile(filepath.Join(dir, stickyFile))
	if err != nil {
		t.Fatalf("read sticky file: %v", err)
	}
	if strings.TrimSpace(string(body)) != string(task.ScopeGlobal) {
		t.Errorf("file holds %q, want %q", body, task.ScopeGlobal)
	}
}

func TestSetStickyDefaultRejectsUnknownKind(t *testing.T) {
	r := Resolver{StateDir: t.TempDir()}
	if err := r.SetStickyDefault(task.ScopeKind("project")); err == nil {
		t.Error("SetStickyDefault accepted an unknown kind")
	}
	if err := r.SetStickyDefault(""); err == nil {
		t.Error("SetStickyDefault accepted an empty kind")
	}
}

// The sticky default is a preference, so it lives in the XDG *state* dir, not
// alongside the user's tasks in the data dir.
func TestStateDirHonoursXDG(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg-state-example")
	got, err := Resolver{}.stateDir()
	if err != nil {
		t.Fatalf("stateDir: %v", err)
	}
	if want := filepath.Join("/tmp/xdg-state-example", AppDir); got != want {
		t.Errorf("stateDir = %q, want %q", got, want)
	}

	t.Setenv("XDG_STATE_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	got, err = Resolver{}.stateDir()
	if err != nil {
		t.Fatalf("stateDir: %v", err)
	}
	if want := filepath.Join(home, ".local", "state", AppDir); got != want {
		t.Errorf("stateDir fallback = %q, want %q", got, want)
	}

	// An explicit StateDir wins over both.
	if got, err := (Resolver{StateDir: "/explicit"}).stateDir(); err != nil || got != "/explicit" {
		t.Errorf("explicit StateDir = %q/%v, want /explicit", got, err)
	}
}
