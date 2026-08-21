package tui

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

const modulePath = "github.com/agusarias/tmux-todo"

var (
	sessionScope = task.Scope{Kind: task.ScopeSession, Key: "pulsar"}
	dirScope     = task.Scope{Kind: task.ScopeDir, Key: "/Users/x/ws/pulsar"}
	globalScope  = task.Scope{Kind: task.ScopeGlobal}
)

// openDB opens a real SQLite database in a temp dir, per repo convention: the
// store is never mocked, so these tests exercise the same queries production
// runs — including the ORDER BY the view is forbidden from duplicating.
func openDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "tasks.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func add(t *testing.T, db *store.DB, text string, scope task.Scope) task.Task {
	t.Helper()
	added, err := db.Add(context.Background(), text, scope)
	if err != nil {
		t.Fatalf("add %q: %v", text, err)
	}
	return added
}

// newLoaded builds a model and runs its first query to completion, returning the
// model with rows in place — the equivalent of Init having been dispatched.
func newLoaded(t *testing.T, cfg Config) Model {
	t.Helper()
	m := New(cfg)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned no command, want the first reload")
	}
	next, _ := m.Update(cmd())
	loaded, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	if loaded.err != nil {
		t.Fatalf("loading rows failed: %v", loaded.err)
	}
	return loaded
}

// press sends one key and returns the resulting model plus any command.
func press(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(keyMsg(key))
	out, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return out, cmd
}

// pressAndSettle sends a key and, if it produced a command, feeds that command's
// message back in — so a filter change ends with its rows already loaded.
func pressAndSettle(t *testing.T, m Model, key string) Model {
	t.Helper()
	m, cmd := press(t, m, key)
	if cmd == nil {
		return m
	}
	next, _ := m.Update(cmd())
	out, ok := next.(Model)
	if !ok {
		t.Fatalf("Update returned %T, want Model", next)
	}
	return out
}

func keyMsg(key string) tea.KeyMsg {
	switch key {
	case "esc":
		return tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "left":
		return tea.KeyMsg{Type: tea.KeyLeft}
	case "right":
		return tea.KeyMsg{Type: tea.KeyRight}
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "delete":
		return tea.KeyMsg{Type: tea.KeyDelete}
	case "home":
		return tea.KeyMsg{Type: tea.KeyHome}
	case "end":
		return tea.KeyMsg{Type: tea.KeyEnd}
	case "ctrl+u":
		return tea.KeyMsg{Type: tea.KeyCtrlU}
	case "ctrl+k":
		return tea.KeyMsg{Type: tea.KeyCtrlK}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
}

// windowSize is the resize message, named so tests read as sizes rather than as
// bubbletea plumbing.
func windowSize(w, h int) tea.WindowSizeMsg { return tea.WindowSizeMsg{Width: w, Height: h} }

// typeText sends each rune of s as its own keystroke, the way a user types.
func typeText(t *testing.T, m Model, s string) Model {
	t.Helper()
	for _, r := range s {
		m = pressAndSettle(t, m, string(r))
	}
	return m
}

func texts(tasks []task.Task) []string {
	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.Text)
	}
	return out
}

// TestDoesNotImportScope pins the constraint that internal/tui is
// environment-blind: resolved scopes are injected through Config, so the model
// stays testable without a tmux server. Asserted against the import graph
// rather than by inspection, because a transitive import would be just as
// fatal and just as invisible in review.
func TestDoesNotImportScope(t *testing.T) {
	const forbidden = modulePath + "/internal/scope"

	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("locate module root: %v", err)
	}

	seen := map[string]bool{}
	var walk func(pkg string, chain []string)
	walk = func(pkg string, chain []string) {
		if seen[pkg] {
			return
		}
		seen[pkg] = true

		chain = append(chain, pkg)
		if pkg == forbidden {
			t.Errorf("internal/tui reaches internal/scope: %s", strings.Join(chain, " -> "))
			return
		}
		for _, imp := range moduleImports(t, root, pkg) {
			walk(imp, chain)
		}
	}
	walk(modulePath+"/internal/tui", nil)

	// Guard against the walker silently finding nothing at all.
	if !seen[modulePath+"/internal/store"] {
		t.Fatal("import walk never reached internal/store; the walker is broken, not the boundary")
	}
}

// moduleImports returns the imports of one package inside this module, itself
// restricted to module-local packages: an external dependency cannot import our
// internal/ tree, so following them would add noise and no coverage.
func moduleImports(t *testing.T, root, pkg string) []string {
	t.Helper()

	dir := filepath.Join(root, strings.TrimPrefix(pkg, modulePath+"/"))
	pkgs, err := parser.ParseDir(token.NewFileSet(), dir, func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}

	var out []string
	for _, p := range pkgs {
		for _, f := range p.Files {
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if strings.HasPrefix(path, modulePath+"/") {
					out = append(out, path)
				}
			}
		}
	}
	return out
}

// TestQueryIsScopedToConfig is the trap this task was most likely to fall into.
// store.Filter with an empty Scopes slice means *every scope in the database*,
// not the active set — so a model that forwards an unpopulated filter shows
// tasks from other sessions and other repos.
func TestQueryIsScopedToConfig(t *testing.T) {
	db := openDB(t)
	add(t, db, "mine", sessionScope)
	add(t, db, "someone else's session", task.Scope{Kind: task.ScopeSession, Key: "other"})
	add(t, db, "another repo", task.Scope{Kind: task.ScopeDir, Key: "/Users/x/ws/elsewhere"})

	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{sessionScope}})

	if got := texts(m.tasks); len(got) != 1 || got[0] != "mine" {
		t.Fatalf("rows = %q, want only [mine]: the filter leaked to inactive scopes", got)
	}
	if view := m.View(); strings.Contains(view, "someone else's session") || strings.Contains(view, "another repo") {
		t.Errorf("view shows tasks from inactive scopes:\n%s", view)
	}
}

// TestEmptyConfigScopesShowsNothing is the same trap at its edge: no active
// scopes must mean no rows, never "every scope in the database".
func TestEmptyConfigScopesShowsNothing(t *testing.T) {
	db := openDB(t)
	add(t, db, "global task", globalScope)
	add(t, db, "session task", sessionScope)

	m := newLoaded(t, Config{DB: db, Scopes: nil})

	if len(m.tasks) != 0 {
		t.Errorf("rows = %q, want none for an empty scope set", texts(m.tasks))
	}
}

// TestIncludesDoneTasks — done rows stay in the list (struck through) so
// completion is legible and reversible in the moment.
func TestIncludesDoneTasks(t *testing.T) {
	db := openDB(t)
	done := add(t, db, "already finished", globalScope)
	if err := db.Complete(context.Background(), done.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})

	if len(m.tasks) != 1 {
		t.Fatalf("rows = %q, want the done task included", texts(m.tasks))
	}
	if !m.tasks[0].Done {
		t.Error("task came back pending, want done")
	}
}

// TestRenderOrderMatchesStore pins the constraint that ordering is the store's
// job. store.List already returns scope tier then newest-first; the view must
// walk that slice, never re-sort it, or there are two sources of truth for
// order.
func TestRenderOrderMatchesStore(t *testing.T) {
	db := openDB(t)
	// Deliberately inserted in the opposite order to the expected display.
	add(t, db, "global first inserted", globalScope)
	add(t, db, "dir older", dirScope)
	add(t, db, "dir newer", dirScope)
	add(t, db, "session only", sessionScope)

	scopes := []task.Scope{sessionScope, dirScope, globalScope}
	m := newLoaded(t, Config{DB: db, Scopes: scopes, Home: "/Users/x"})

	want, err := db.List(context.Background(), store.Filter{Scopes: scopes, IncludeDone: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if got, expect := texts(m.tasks), texts(want); fmt.Sprint(got) != fmt.Sprint(expect) {
		t.Fatalf("model rows = %v, want store order %v", got, expect)
	}

	// And the rendered rows appear in that same order, top to bottom.
	rows := renderRows(m.tasks, renderOpts{Width: 80, Home: "/Users/x", Cursor: 0})
	if len(rows) != len(want) {
		t.Fatalf("rendered %d rows, want %d", len(rows), len(want))
	}
	for i, w := range want {
		if !strings.Contains(rows[i], w.Text) {
			t.Errorf("row %d = %q, want the text %q from List position %d", i, rows[i], w.Text, i)
		}
	}
}

// TestFilterKeysNarrowToOneTier covers 1/2/3 and the same-digit toggle back to
// the merged view.
func TestFilterKeysNarrowToOneTier(t *testing.T) {
	db := openDB(t)
	add(t, db, "session task", sessionScope)
	add(t, db, "dir task", dirScope)
	add(t, db, "global task", globalScope)
	scopes := []task.Scope{sessionScope, dirScope, globalScope}

	base := newLoaded(t, Config{DB: db, Scopes: scopes, Home: "/Users/x"})
	if len(base.tasks) != 3 {
		t.Fatalf("merged view has %d rows, want 3", len(base.tasks))
	}

	for _, tc := range []struct {
		key  string
		kind task.ScopeKind
		want string
	}{
		{"1", task.ScopeSession, "session task"},
		{"2", task.ScopeDir, "dir task"},
		{"3", task.ScopeGlobal, "global task"},
	} {
		filtered := pressAndSettle(t, base, tc.key)
		if filtered.filter != tc.kind {
			t.Errorf("%q set filter %q, want %q", tc.key, filtered.filter, tc.kind)
		}
		if got := texts(filtered.tasks); len(got) != 1 || got[0] != tc.want {
			t.Errorf("%q filtered to %q, want only [%s]", tc.key, got, tc.want)
		}

		// Same digit again returns to the merged list. There is no separate
		// un-filter key: Esc closes the popup.
		merged := pressAndSettle(t, filtered, tc.key)
		if merged.filter != "" {
			t.Errorf("%q pressed twice left filter %q, want cleared", tc.key, merged.filter)
		}
		if len(merged.tasks) != 3 {
			t.Errorf("%q pressed twice gave %q, want all 3 rows", tc.key, texts(merged.tasks))
		}
	}
}

// TestFilterChangeResetsCursor — the row under the old cursor has nothing to do
// with the new list, so the cursor goes back to the top.
func TestFilterChangeResetsCursor(t *testing.T) {
	db := openDB(t)
	add(t, db, "session a", sessionScope)
	add(t, db, "session b", sessionScope)
	add(t, db, "global a", globalScope)

	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{sessionScope, globalScope}})
	m = pressAndSettle(t, m, "j")
	m = pressAndSettle(t, m, "j")
	if m.cursor == 0 {
		t.Fatal("cursor never moved; the rest of this test proves nothing")
	}

	if filtered := pressAndSettle(t, m, "1"); filtered.cursor != 0 {
		t.Errorf("cursor = %d after filtering, want 0", filtered.cursor)
	}
}

// TestFilterOnUnavailableTierIsEmpty — filtering to a tier this context has no
// scope for must show nothing, not fall back to every scope in the database.
func TestFilterOnUnavailableTierIsEmpty(t *testing.T) {
	db := openDB(t)
	add(t, db, "global task", globalScope)
	add(t, db, "a session task", sessionScope)

	// Outside tmux there is no session scope at all: absent, not empty-keyed.
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})
	filtered := pressAndSettle(t, m, "1")

	if len(filtered.tasks) != 0 {
		t.Fatalf("rows = %q, want none: no session scope is active", texts(filtered.tasks))
	}
	if body := filtered.View(); !strings.Contains(body, "no session tasks") {
		t.Errorf("view lacks the filtered empty state:\n%s", body)
	}
}

// TestNavigationClampsAtBothEnds — j/k and the arrows move the cursor and stop
// at the ends rather than wrapping or running off the slice.
func TestNavigationClampsAtBothEnds(t *testing.T) {
	db := openDB(t)
	for _, text := range []string{"one", "two", "three"} {
		add(t, db, text, globalScope)
	}
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})

	if m.cursor != 0 {
		t.Fatalf("cursor starts at %d, want 0", m.cursor)
	}
	for _, key := range []string{"k", "up"} {
		if up := pressAndSettle(t, m, key); up.cursor != 0 {
			t.Errorf("%q at the top moved the cursor to %d, want 0", key, up.cursor)
		}
	}

	for i, key := range []string{"j", "down"} {
		m = pressAndSettle(t, m, key)
		if m.cursor != i+1 {
			t.Fatalf("after %q cursor = %d, want %d", key, m.cursor, i+1)
		}
	}
	// Already on the last row; further movement must clamp.
	for i := 0; i < 3; i++ {
		m = pressAndSettle(t, m, "j")
	}
	if want := len(m.tasks) - 1; m.cursor != want {
		t.Errorf("cursor = %d after running past the end, want %d", m.cursor, want)
	}
}

// TestNavigationWithNoRows — an empty list must not produce a cursor pointing
// at a row that does not exist.
func TestNavigationWithNoRows(t *testing.T) {
	db := openDB(t)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})

	for _, key := range []string{"j", "k", "down", "up"} {
		m = pressAndSettle(t, m, key)
		if m.cursor != 0 {
			t.Fatalf("%q on an empty list set cursor = %d, want 0", key, m.cursor)
		}
	}
}

// TestCursorFollowsSelectionIntoView — the selected row must stay on screen when
// the list is longer than the viewport.
func TestCursorFollowsSelectionIntoView(t *testing.T) {
	db := openDB(t)
	for i := 0; i < 40; i++ {
		add(t, db, fmt.Sprintf("task %02d", i), globalScope)
	}

	m := New(Config{DB: db, Scopes: []task.Scope{globalScope}})
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 10})
	m = sized.(Model)
	next, _ := m.Update(m.reloadCmd()())
	m = next.(Model)

	height := m.vp.Height
	if height <= 0 || height >= len(m.tasks) {
		t.Fatalf("viewport height %d does not exercise scrolling over %d rows", height, len(m.tasks))
	}

	for i := 0; i < len(m.tasks); i++ {
		if m.cursor < m.vp.YOffset || m.cursor >= m.vp.YOffset+height {
			t.Fatalf("cursor %d outside viewport rows [%d,%d)", m.cursor, m.vp.YOffset, m.vp.YOffset+height)
		}
		m = pressAndSettle(t, m, "j")
	}
	for i := 0; i < len(m.tasks); i++ {
		if m.cursor < m.vp.YOffset || m.cursor >= m.vp.YOffset+height {
			t.Fatalf("scrolling back: cursor %d outside viewport rows [%d,%d)", m.cursor, m.vp.YOffset, m.vp.YOffset+height)
		}
		m = pressAndSettle(t, m, "k")
	}
	if m.vp.YOffset != 0 {
		t.Errorf("returned to the top but YOffset = %d, want 0", m.vp.YOffset)
	}
}

func TestQuitKeys(t *testing.T) {
	db := openDB(t)
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		m, cmd := press(t, New(Config{DB: db, Scopes: []task.Scope{globalScope}}), key)
		if cmd == nil {
			t.Fatalf("key %q produced no command, want tea.Quit", key)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("key %q did not produce tea.Quit", key)
		}
		if !m.quitting {
			t.Errorf("key %q left model not quitting", key)
		}
		if m.View() != "" {
			t.Errorf("key %q still renders a view after quitting", key)
		}
	}
}

// TestUnhandledKeysDoNothing — in normal mode an unbound key is inert. It must
// not quit, and it must not disturb the selection.
func TestUnhandledKeysDoNothing(t *testing.T) {
	db := openDB(t)
	add(t, db, "one", globalScope)
	add(t, db, "two", globalScope)
	loaded := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})
	loaded = pressAndSettle(t, loaded, "j")

	// `g` used to be here as a key a follow-on task would claim; the all-tasks
	// view has now claimed it, and its own tests cover it. What is here instead
	// are that view's keys, which must stay inert in the *merged* view: enter
	// has nowhere to jump, and r and D need a group.
	for _, key := range []string{"x", "Z", "9", "0", "enter", "r", "D"} {
		m, cmd := press(t, loaded, key)
		if cmd != nil {
			t.Errorf("key %q produced a command, want none", key)
		}
		if m.quitting {
			t.Errorf("key %q set quitting", key)
		}
		if m.cursor != loaded.cursor {
			t.Errorf("key %q moved the cursor to %d, want %d", key, m.cursor, loaded.cursor)
		}
		if m.filter != loaded.filter {
			t.Errorf("key %q changed the filter to %q", key, m.filter)
		}
	}
}

// TestModeGatesKeyHandling is the seam task-create-edit-rescope needs: with an
// input mode on the model, `q` becomes a literal keystroke rather than quit.
// Only normal mode exists today, so this asserts dispatch happens on the mode
// at all — a flat switch on the key would make that task a refactor.
func TestModeGatesKeyHandling(t *testing.T) {
	db := openDB(t)
	m := New(Config{DB: db, Scopes: []task.Scope{globalScope}})
	if m.mode != modeNormal {
		t.Fatalf("new model mode = %d, want modeNormal", m.mode)
	}

	// An unknown mode reaches no handler, so even q is inert.
	m.mode = mode(99)
	next, cmd := m.Update(keyMsg("q"))
	if cmd != nil {
		t.Error("q produced a command outside normal mode; keys are not gated on the mode")
	}
	if next.(Model).quitting {
		t.Error("q quit outside normal mode; keys are not gated on the mode")
	}
}

// TestViewStartsInMergedView pins the view-mode seam: the all-tasks view that
// all-tasks-view-with-sesh-jump adds becomes another case, and `g` is not bound
// here.
func TestViewStartsInMergedView(t *testing.T) {
	db := openDB(t)
	m := New(Config{DB: db, Scopes: []task.Scope{globalScope}})
	if m.view != viewMerged {
		t.Errorf("new model view = %d, want viewMerged", m.view)
	}
}

// TestReloadCmdRefetches is the "popup stays open across actions" seam: rows are
// replaced by re-running the query, so a mutation added later needs no restart.
func TestReloadCmdRefetches(t *testing.T) {
	db := openDB(t)
	add(t, db, "first", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})
	if len(m.tasks) != 1 {
		t.Fatalf("rows = %q, want 1", texts(m.tasks))
	}

	// A change made behind the model's back, as a mutation keybinding would.
	add(t, db, "second", globalScope)

	next, _ := m.Update(m.reloadCmd()())
	if reloaded := next.(Model); len(reloaded.tasks) != 2 {
		t.Errorf("after reload rows = %q, want both tasks", texts(reloaded.tasks))
	}
}

// TestEmptyStatesAreDistinct — "nothing here at all" and "your filter hid
// everything" are different situations, and the second is one the user can undo.
func TestEmptyStatesAreDistinct(t *testing.T) {
	db := openDB(t)
	add(t, db, "global task", globalScope)
	scopes := []task.Scope{sessionScope, globalScope}

	bare := newLoaded(t, Config{DB: db, Scopes: []task.Scope{dirScope}})
	bareView := bare.View()
	if !strings.Contains(bareView, "no tasks yet") {
		t.Errorf("unfiltered empty state missing:\n%s", bareView)
	}

	filtered := pressAndSettle(t, newLoaded(t, Config{DB: db, Scopes: scopes}), "1")
	filteredView := filtered.View()
	if !strings.Contains(filteredView, "no session tasks") {
		t.Errorf("filtered empty state missing:\n%s", filteredView)
	}
	if bareView == filteredView {
		t.Error("both empty states render identically; they must be distinguishable")
	}
}

// TestViewMentionsVersion — the running version stays reachable from the popup,
// but it is no longer in the footer: at contentWidth 42 a `git describe` stamp
// pushes the keybindings off the end, and keys beat provenance. It lives in the
// `?` overlay instead, which is where a user goes looking for "what is this".
func TestViewMentionsVersion(t *testing.T) {
	db := openDB(t)
	add(t, db, "something", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}, Version: "1.2.3"})

	if got := m.footer(); strings.Contains(got, "1.2.3") {
		t.Errorf("footer still carries the version, which is what made it too wide: %q", got)
	}
	helped := pressAndSettle(t, m, "?")
	if got := helped.View(); !strings.Contains(got, "1.2.3") {
		t.Errorf("the ? overlay does not mention the version:\n%s", got)
	}
}

// TestFooterPointsAtTheOverlayInsteadOfEnumerating — the footer cannot hold
// eleven keys and a version stamp in 42 columns, and truncation would drop the
// last ones silently. It carries the few keys a first-time user needs and points
// at `?` for the rest.
func TestFooterPointsAtTheOverlayInsteadOfEnumerating(t *testing.T) {
	db := openDB(t)
	add(t, db, "something", globalScope)
	footer := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}}).footer()

	for _, want := range []string{"j/k move", "space done", "? keys", "q quit"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer %q missing %q", footer, want)
		}
	}
	// Real keys, but the overlay's job. Putting them back here is what pushes
	// the line over the width.
	for _, elsewhere := range []string{"a add", "e edit", "d delete", "s re-scope", "1/2/3"} {
		if strings.Contains(footer, elsewhere) {
			t.Errorf("footer enumerates %q instead of pointing at ? keys: %s", elsewhere, footer)
		}
	}
	// The keys it does not have must still exist somewhere.
	help := strings.Join(helpLines(viewMerged, "dev"), "\n")
	for _, want := range []string{"a add", "e edit", "s re-scope", "d delete", "u undo", "1/2/3", "tab"} {
		if !strings.Contains(help, want) {
			t.Errorf("the ? overlay is missing %q:\n%s", want, help)
		}
	}
}

// TestQueryFailureIsSurfaced — a broken store must say so, not render an empty
// list that reads as "you have no tasks".
func TestQueryFailureIsSurfaced(t *testing.T) {
	db := openDB(t)
	add(t, db, "something", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})

	// Closing the database makes the next query fail for real.
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	next, _ := m.Update(m.reloadCmd()())
	broken := next.(Model)

	if broken.err == nil {
		t.Fatal("query against a closed database reported no error")
	}
	if view := broken.View(); !strings.Contains(view, "list tasks") {
		t.Errorf("view hides the failure:\n%s", view)
	}
	if strings.Contains(broken.View(), "no tasks yet") {
		t.Error("a failed query renders as an empty list")
	}
}

// TestQueryTimeoutIsBounded documents that the popup cannot hang forever on a
// locked database.
func TestQueryTimeoutIsBounded(t *testing.T) {
	if queryTimeout <= 0 || queryTimeout > 10*time.Second {
		t.Errorf("queryTimeout = %v, want a small positive bound", queryTimeout)
	}
}

// longVersion is what `git describe --tags --always --dirty` produces on a
// dirty working tree — the Makefile stamps Version from exactly that, so the
// footer's length, and with it the narrowest width the popup can render at,
// varies with the build's git state rather than being a property of the code.
const longVersion = "v0.1.0-3-gec132f9-dirty"

// TestFrameNeverExceedsThePane is DoD 20, and it is the point of this
// fix-forward pass: the missing test was the root cause, not the missing
// truncate.
//
// Every frame bug in this task — three of them — was found by a capture at a
// single fixed 80x20 size, and none of the 38 unit tests asserted anything about
// the *assembled* frame. lipgloss wraps an over-wide line instead of clipping
// it, so one long chrome line quietly becomes two rows; the frame then runs past
// the pane, and a terminal responds by scrolling, which eats the rows at the top
// — the box border, the session tier, the tier labels. Truncating footer() alone
// would have fixed the instance and left the class open for the next key added
// to the hint line.
func TestFrameNeverExceedsThePane(t *testing.T) {
	db := openDB(t)
	// Enough rows to fill any viewport under test, across all three tiers so
	// the widest tier labels are in play too.
	//
	// Two thirds of them are DONE, and stamped inside the retention window, so
	// every size below renders a list that is mostly struck-through rows sitting
	// at the end of their tier. Before this task a done row was visible only in
	// the popup session that completed it, so no frame was ever measured with
	// them in it — and "more visible rows" is exactly the change that can walk
	// into the chromeHeight arithmetic these assertions exist to guard.
	frameClock := time.Unix(1_760_000_000, 0)
	for i := 0; i < 40; i++ {
		scope := []task.Scope{sessionScope, dirScope, globalScope}[i%3]
		added := add(t, db, fmt.Sprintf("task %02d with a fairly typical amount of text", i), scope)
		if i%3 != 0 {
			stampDone(t, db, added.ID, frameClock.Add(-time.Duration(i)*time.Minute))
		}
	}
	scopes := []task.Scope{sessionScope, dirScope, globalScope}

	sizes := []struct{ w, h int }{
		{40, 10}, {44, 12}, {48, 12}, {52, 14}, {64, 14},
		{72, 16}, {80, 20}, {100, 24}, {120, 30},
	}
	versions := []string{"dev", "ec132f9", longVersion}

	// The modes this task adds render inside the body, so they are the obvious
	// place for a chromeHeight miscount to reappear. "adding" also types a long
	// title, because an input row that failed to window its text would wrap.
	modes := []struct {
		name  string
		enter func(*testing.T, Model) Model
	}{
		{"normal", func(_ *testing.T, m Model) Model { return m }},
		{"adding", func(t *testing.T, m Model) Model {
			m = pressAndSettle(t, m, "a")
			return typeText(t, m, strings.Repeat("long title ", 12))
		}},
		{"editing", func(t *testing.T, m Model) Model { return pressAndSettle(t, m, "e") }},
		{"rejected edit", func(t *testing.T, m Model) Model {
			m = pressAndSettle(t, m, "e")
			m = pressAndSettle(t, m, "ctrl+u")
			return pressAndSettle(t, m, "enter")
		}},
		{"help", func(t *testing.T, m Model) Model { return pressAndSettle(t, m, "?") }},
		{"queued empty", func(t *testing.T, m Model) Model {
			for range 60 {
				m = pressAndSettle(t, m, "d")
			}
			return m
		}},
		// The copy notice replaces the title line, so it is the one chrome line
		// this task changes. Pressed for real, so the whole path — key, command,
		// message, notice, titleLine — is what gets measured.
		{"copied", func(t *testing.T, m Model) Model { return pressAndSettle(t, m, "y") }},
		// ...and the hostile notice, set directly. A task text can hold a
		// newline, and truncation clips *width* only: one stray row is exactly
		// the arithmetic that has broken this frame three times, so the frame
		// has to be measured against a notice no seeded row could produce.
		{"copied hostile", func(t *testing.T, m Model) Model {
			m = pressAndSettle(t, m, "y")
			m.notice = "copied: " + strings.Repeat("wide\nand\ttall ", 30)
			return m
		}},
	}

	// Both views, because the all-tasks view puts *more* lines in the body —
	// group headers between the rows — and chromeHeight is a constant that
	// counts only the chrome. A header that wrapped, or a header the viewport
	// height did not account for, reappears here as a frame taller than the
	// pane.
	for _, version := range versions {
		for _, view := range []string{"merged", "all"} {
			for _, size := range sizes {
				for _, filter := range []string{"", "1", "2", "3"} {
					for _, mode := range modes {
						name := fmt.Sprintf("v=%s/view=%s/%dx%d/filter=%q/%s", version, view, size.w, size.h, filter, mode.name)
						t.Run(name, func(t *testing.T) {
							m := newLoaded(t, Config{
								DB: db, Scopes: scopes, Home: "/Users/x", Version: version,
								LiveSessions: map[string]bool{"pulsar": true},
								// Without this the "copied" modes press an inert
								// key and assert nothing — the vacuous-guard trap.
								Copy: func(string) error { return nil },
								// Frozen inside the retention window, so the
								// seeded done rows are actually on screen. With
								// the real clock they age out and the mostly-done
								// list this fixture builds would silently become
								// a third of its size.
								Now: frozen(frameClock),
							})
							if len(m.tasks) != 40 {
								t.Fatalf("%d rows loaded, want all 40 (27 of them done) — "+
									"the done rows aged out and this size proves nothing about them",
									len(m.tasks))
							}
							sized, _ := m.Update(tea.WindowSizeMsg{Width: size.w, Height: size.h})
							m = sized.(Model)
							if view == "all" {
								m = pressAndSettle(t, m, "g")
							}
							if filter != "" {
								m = pressAndSettle(t, m, filter)
							}
							m = mode.enter(t, m)

							// Asserted on the *unclamped* frame: checking View's
							// output would let the clampHeight backstop hide a
							// miscounted chromeHeight instead of failing on it.
							lines := strings.Split(m.frame(), "\n")
							if len(lines) > size.h {
								t.Errorf("frame is %d rows in a %d-row pane; the terminal will scroll and the top rows are lost:\n%s",
									len(lines), size.h, m.frame())
							}
							if got := strings.Split(m.View(), "\n"); len(got) != len(lines) {
								t.Errorf("the height backstop fired (%d rows -> %d); it is a safety net, not the mechanism",
									len(lines), len(got))
							}
							for i, line := range lines {
								if w := lipgloss.Width(line); w > size.w {
									t.Errorf("line %d is %d columns in a %d-column pane: %q",
										i, w, size.w, line)
								}
							}
						})
					}
				}
			}
		}
	}
}

// TestChromeLinesFitTheContentWidth pins the invariant chromeHeight's constant
// depends on: each chrome line is exactly one row after truncation. If this
// breaks, chromeHeight is silently wrong and the frame grows.
func TestChromeLinesFitTheContentWidth(t *testing.T) {
	db := openDB(t)
	add(t, db, "something", globalScope)

	for _, width := range []int{20, 30, 40, 44, 48, 64, 80} {
		m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}, Version: longVersion})
		sized, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
		m = sized.(Model)

		for _, tc := range []struct {
			name string
			line string
		}{
			{"footer", m.footer()},
			{"title", m.titleLine()},
			{"filtered title", pressAndSettle(t, m, "1").titleLine()},
			{"empty state", pressAndSettle(t, m, "1").body()},
		} {
			if strings.Contains(tc.line, "\n") {
				t.Errorf("width %d: %s spans multiple rows: %q", width, tc.name, tc.line)
			}
			if w := lipgloss.Width(tc.line); w > m.contentWidth() {
				t.Errorf("width %d: %s is %d columns, over the %d-column content width: %q",
					width, tc.name, w, m.contentWidth(), tc.line)
			}
		}
	}
}

// TestFooterSurvivesTheNarrowestPopup is DoD 17, and it is the whole reason the
// version left the footer.
//
// docs/design.md's ~60%x60% popup is 48 columns on an 80-column terminal, so
// contentWidth is 42. The old footer was 49 columns with a `dev` stamp and 69
// with a `git describe --dirty` one, so it was *always* truncated there — and
// truncation removes the keys at the end, silently. This asserts the line needs
// no truncation at all at that width, with the longest version stamp the
// Makefile can produce, since the version must no longer be able to affect it.
func TestFooterSurvivesTheNarrowestPopup(t *testing.T) {
	db := openDB(t)
	add(t, db, "something", globalScope)

	const designWidth = 48 // 60% of an 80-column terminal
	for _, version := range []string{"", "dev", "ec132f9", longVersion} {
		m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}, Version: version})
		sized, _ := m.Update(tea.WindowSizeMsg{Width: designWidth, Height: 12})
		m = sized.(Model)

		if got := m.contentWidth(); got != 42 {
			t.Fatalf("contentWidth at %d columns = %d, want 42 — the premise of this test moved", designWidth, got)
		}
		footer := m.footer()
		if strings.Contains(footer, ellipsis) {
			t.Errorf("version %q: the footer was truncated at the design's own width: %q", version, footer)
		}
		if w := lipgloss.Width(footer); w > 42 {
			t.Errorf("version %q: footer is %d columns, over the 42 available: %q", version, w, footer)
		}
	}
}

// TestHelpOverlayFitsTheNarrowestPopup — the overlay took the keys the footer
// gave up, so it inherits the obligation to fit. Every line untruncated at 42
// columns, and the whole thing inside the rows the list has.
func TestHelpOverlayFitsTheNarrowestPopup(t *testing.T) {
	for _, version := range []string{"", "dev", longVersion} {
		for _, view := range []viewKind{viewMerged, viewAll} {
			for i, line := range helpLines(view, version) {
				if strings.Contains(line, "\n") {
					t.Errorf("view %d version %q: help line %d spans rows: %q", view, version, i, line)
				}
				if w := lipgloss.Width(line); w > 42 {
					t.Errorf("view %d version %q: help line %d is %d columns, over 42: %q", view, version, i, w, line)
				}
			}
		}
	}
}

// TestClampHeightIsABackstop — the truncation above should mean it never fires,
// so prove it does the right thing when handed something over-tall anyway.
func TestClampHeightIsABackstop(t *testing.T) {
	frame := "one\ntwo\nthree\nfour"
	if got := clampHeight(frame, 2); got != "one\ntwo" {
		t.Errorf("clampHeight(4 rows, 2) = %q, want the first two", got)
	}
	if got := clampHeight(frame, 10); got != frame {
		t.Error("clampHeight shortened a frame that already fits")
	}
	if got := clampHeight(frame, 0); got != frame {
		t.Error("clampHeight with an unknown height should leave the frame alone")
	}
}
