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

// TestStickyAllTasksRoundTrip writes with one Resolver and reads with another,
// so nothing is served from in-process state: the popup that writes on quit and
// the popup that reads on open are separate processes sharing only the file.
func TestStickyAllTasksRoundTrip(t *testing.T) {
	dir := t.TempDir()

	for _, want := range []bool{true, false, true} {
		if err := (Resolver{StateDir: dir}).SetStickyAllTasks(want); err != nil {
			t.Fatalf("SetStickyAllTasks(%v): %v", want, err)
		}
		if got := (Resolver{StateDir: dir}).StickyAllTasks(); got != want {
			t.Errorf("StickyAllTasks = %v, want %v", got, want)
		}
	}
}

// TestStickyAllTasksUnreadableMeansMergedList is DoD 5. Every way the file can
// fail to say "all" has to mean the merged list, silently — a preference file is
// never a reason for the popup not to open.
func TestStickyAllTasksUnreadableMeansMergedList(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(t *testing.T, dir string)
	}{
		{"missing", func(*testing.T, string) {}},
		{"empty", func(t *testing.T, dir string) { writeView(t, dir, "") }},
		{"whitespace", func(t *testing.T, dir string) { writeView(t, dir, "   \n\t\n") }},
		{"junk", func(t *testing.T, dir string) { writeView(t, dir, "not-a-view") }},
		{"the other file's vocabulary", func(t *testing.T, dir string) { writeView(t, dir, "global") }},
		{"almost right", func(t *testing.T, dir string) { writeView(t, dir, "alll") }},
		{"wrong case", func(t *testing.T, dir string) { writeView(t, dir, "ALL") }},
		{"binary", func(t *testing.T, dir string) { writeView(t, dir, "\x00\xff\x01") }},
		{"a directory where the file goes", func(t *testing.T, dir string) {
			if err := os.MkdirAll(filepath.Join(dir, stickyViewFile), 0o755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
		}},
		{"state dir is a file", func(t *testing.T, dir string) {
			// r.StateDir points at something that cannot hold files at all.
			if err := os.WriteFile(filepath.Join(dir, "notadir"), []byte("x"), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.write(t, dir)
			state := dir
			if tc.name == "state dir is a file" {
				state = filepath.Join(dir, "notadir")
			}
			if got := (Resolver{StateDir: state}).StickyAllTasks(); got {
				t.Errorf("StickyAllTasks = true for a %s file, want the merged list", tc.name)
			}
		})
	}

	// The positive control: with the file saying exactly "all", the same code
	// path must answer true — otherwise every case above passes vacuously.
	dir := t.TempDir()
	writeView(t, dir, stickyViewAll)
	if !(Resolver{StateDir: dir}).StickyAllTasks() {
		t.Fatal("StickyAllTasks = false for a file holding \"all\"; the table above proves nothing")
	}
	// ...and a trailing newline, which is what SetStickyAllTasks itself writes.
	writeView(t, dir, stickyViewAll+"\n")
	if !(Resolver{StateDir: dir}).StickyAllTasks() {
		t.Error("a trailing newline defeated the read; SetStickyAllTasks writes one")
	}
}

// writeView puts a literal body in the sticky-view file, bypassing the setter —
// which is the point: the setter can only produce well-formed files, and what is
// under test is what happens when something else produced it.
func writeView(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, stickyViewFile), []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", stickyViewFile, err)
	}
}

// TestStickyViewAndScopeAreIndependent is DoD 6, and the reason the two
// preferences are two files.
//
// They share a directory and are both one word long, so the failure worth ruling
// out is one being read as the other — or one write clobbering the other. This
// asserts both directions and both absences.
func TestStickyViewAndScopeAreIndependent(t *testing.T) {
	dir := t.TempDir()
	r := Resolver{StateDir: dir}

	// Writing the view must not create or disturb the scope preference.
	if err := r.SetStickyAllTasks(true); err != nil {
		t.Fatalf("SetStickyAllTasks: %v", err)
	}
	if got, ok := r.storedStickyDefault(); ok {
		t.Errorf("writing the view produced a stored scope default %q; the files must be separate", got)
	}
	if got := r.StickyDefault(inTmux()); got != task.ScopeSession {
		t.Errorf("StickyDefault = %q after writing only the view, want the unstored default", got)
	}

	// Writing the scope must not disturb the view.
	if err := r.SetStickyDefault(task.ScopeGlobal); err != nil {
		t.Fatalf("SetStickyDefault: %v", err)
	}
	if !r.StickyAllTasks() {
		t.Error("writing the scope default cleared the stored view preference")
	}

	// Both now hold their own value, read back independently.
	if got, ok := r.storedStickyDefault(); !ok || got != task.ScopeGlobal {
		t.Errorf("stored scope = %q/%v, want global/true", got, ok)
	}
	if !r.StickyAllTasks() {
		t.Error("stored view = false, want true")
	}

	// Flipping the view back must leave the scope alone.
	if err := r.SetStickyAllTasks(false); err != nil {
		t.Fatalf("SetStickyAllTasks(false): %v", err)
	}
	if r.StickyAllTasks() {
		t.Error("stored view = true after setting false")
	}
	if got, ok := r.storedStickyDefault(); !ok || got != task.ScopeGlobal {
		t.Errorf("stored scope = %q/%v after a view write, want global/true", got, ok)
	}

	// They really are two files on disk, not one with two words in it.
	for _, name := range []string{stickyFile, stickyViewFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected a %s file in the state dir: %v", name, err)
		}
	}
	// And nothing else was left behind — a failed atomic write would leave a
	// temp file next to them.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read state dir: %v", err)
	}
	if len(entries) != 2 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("state dir holds %v, want exactly the two preference files", names)
	}
}

// TestSetStickyAllTasksReportsAnUnwritableStateDir — the popup drops this error
// on purpose, but the function must still produce one rather than silently
// pretending it saved.
func TestSetStickyAllTasksReportsAnUnwritableStateDir(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := (Resolver{StateDir: filepath.Join(blocked, "state")}).SetStickyAllTasks(true); err == nil {
		t.Error("SetStickyAllTasks reported success with an unwritable state dir")
	}
}

// TestStickyAllTasksHonoursXDGStateHome — the same directory as the scope
// preference, resolved the same way. Asserted through the environment rather
// than an explicit StateDir, because the explicit field is what every other test
// uses and would hide a stateDir() that stopped being consulted.
func TestStickyAllTasksHonoursXDGStateHome(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	if err := (Resolver{}).SetStickyAllTasks(true); err != nil {
		t.Fatalf("SetStickyAllTasks: %v", err)
	}
	want := filepath.Join(state, AppDir, stickyViewFile)
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected the preference at %s: %v", want, err)
	}
	if !(Resolver{}).StickyAllTasks() {
		t.Error("StickyAllTasks did not read back what it wrote through XDG_STATE_HOME")
	}
}
