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
	default:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
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

	// Includes the keys the follow-on UI tasks will claim: until they land,
	// pressing them must be a no-op rather than a surprise.
	for _, key := range []string{"x", "Z", "9", "0", "a", "e", "d", "s", "g", " "} {
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

// TestViewMentionsVersion survives the placeholder's removal: the running
// version stays visible in the popup.
func TestViewMentionsVersion(t *testing.T) {
	db := openDB(t)
	add(t, db, "something", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}, Version: "1.2.3"})
	if got := m.View(); !strings.Contains(got, "1.2.3") {
		t.Errorf("View() does not mention the version:\n%s", got)
	}
}

// TestFooterOnlyAdvertisesImplementedKeys — docs/design.md's mock shows the end
// state (a/e/space/d/s/g). Those keys belong to the follow-on tasks; listing
// them before they work would advertise dead keys.
func TestFooterOnlyAdvertisesImplementedKeys(t *testing.T) {
	db := openDB(t)
	add(t, db, "something", globalScope)
	footer := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}}).footer()

	for _, want := range []string{"1/2/3", "q quit"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer %q missing %q", footer, want)
		}
	}
	for _, unimplemented := range []string{"a add", "e edit", "space done", "d delete", "s re-scope", "g all"} {
		if strings.Contains(footer, unimplemented) {
			t.Errorf("footer advertises the unimplemented %q: %s", unimplemented, footer)
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
	if view := broken.View(); !strings.Contains(view, "cannot read tasks") {
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
