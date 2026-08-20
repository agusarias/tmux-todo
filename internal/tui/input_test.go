package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// stickyRecorder captures what the popup asked to persist, which is the only way
// to see the sticky write from here: internal/tui must not know the state dir
// exists, so the write is injected and the assertion is on the call.
type stickyRecorder struct {
	kinds []task.ScopeKind
	err   error
}

func (s *stickyRecorder) set(kind task.ScopeKind) error {
	s.kinds = append(s.kinds, kind)
	return s.err
}

// listAll reads the store directly, so a test can tell "not on screen" from
// "not in the database" — the whole distinction the delete queue turns on.
func listAll(t *testing.T, db *store.DB) []task.Task {
	t.Helper()
	tasks, err := db.List(context.Background(), store.Filter{IncludeDone: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	return tasks
}

func allScopes() []task.Scope {
	return []task.Scope{sessionScope, dirScope, globalScope}
}

// TestAddOpensAnInputRowInTheViewport is DoD 1: the row is a *row*, not a sixth
// chrome line, so chromeHeight is untouched.
func TestAddOpensAnInputRowInTheViewport(t *testing.T) {
	db := openDB(t)
	add(t, db, "existing", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes(), DefaultScope: task.ScopeGlobal})

	before := strings.Count(m.frame(), "\n")
	m = pressAndSettle(t, m, "a")

	if m.mode != modeInput {
		t.Fatalf("mode = %d, want modeInput", m.mode)
	}
	if after := strings.Count(m.frame(), "\n"); after != before {
		t.Errorf("frame went from %d to %d rows; the input row is not inside the viewport", before+1, after+1)
	}
	m = typeText(t, m, "hi")
	if !strings.Contains(m.View(), "hi") {
		t.Errorf("what was typed is not on screen:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "existing") {
		t.Errorf("the list vanished behind the input row:\n%s", m.View())
	}
}

// TestTabCyclesOnlyAvailableScopes is DoD 2. The cycle is driven off
// Config.Scopes alone, so a scope this context does not have is never offered
// and Enter can never fail on scope grounds.
func TestTabCyclesOnlyAvailableScopes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []task.Scope
		want   []task.ScopeKind
	}{{
		name:   "all three",
		scopes: allScopes(),
		want:   []task.ScopeKind{task.ScopeSession, task.ScopeDir, task.ScopeGlobal, task.ScopeSession},
	}, {
		name:   "outside tmux",
		scopes: []task.Scope{dirScope, globalScope},
		want:   []task.ScopeKind{task.ScopeDir, task.ScopeGlobal, task.ScopeDir},
	}, {
		name:   "no directory",
		scopes: []task.Scope{sessionScope, globalScope},
		want:   []task.ScopeKind{task.ScopeSession, task.ScopeGlobal, task.ScopeSession},
	}, {
		name:   "global only — tab is a no-op, not a trap",
		scopes: []task.Scope{globalScope},
		want:   []task.ScopeKind{task.ScopeGlobal, task.ScopeGlobal, task.ScopeGlobal},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			m := newLoaded(t, Config{DB: db, Scopes: tc.scopes})
			m = pressAndSettle(t, m, "a")

			for i, want := range tc.want {
				if m.inputScope != want {
					t.Fatalf("after %d tabs the scope is %q, want %q", i, m.inputScope, want)
				}
				m = pressAndSettle(t, m, "tab")
			}
		})
	}
}

// The seed is the injected sticky default — but only when this context actually
// has that scope, so a stored preference from another pane cannot arm an add
// that could not be submitted.
func TestAddSeedsFromTheStickyDefaultAndDegrades(t *testing.T) {
	for _, tc := range []struct {
		name   string
		scopes []task.Scope
		seed   task.ScopeKind
		want   task.ScopeKind
	}{
		{"honours the default", allScopes(), task.ScopeDir, task.ScopeDir},
		{"degrades an unavailable default", []task.Scope{dirScope, globalScope}, task.ScopeSession, task.ScopeDir},
		{"no default falls to the top tier", allScopes(), "", task.ScopeSession},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			m := newLoaded(t, Config{DB: db, Scopes: tc.scopes, DefaultScope: tc.seed})
			m = pressAndSettle(t, m, "a")
			if m.inputScope != tc.want {
				t.Errorf("seeded scope = %q, want %q", m.inputScope, tc.want)
			}
		})
	}
}

// TestAddSavesTrimsAnchorsAndRemembers is DoD 3, all four clauses.
func TestAddSavesTrimsAnchorsAndRemembers(t *testing.T) {
	db := openDB(t)
	add(t, db, "already here", globalScope)
	sticky := &stickyRecorder{}
	m := newLoaded(t, Config{
		DB: db, Scopes: allScopes(), DefaultScope: task.ScopeGlobal, SetSticky: sticky.set,
	})

	m = pressAndSettle(t, m, "a")
	m = pressAndSettle(t, m, "tab") // global -> session
	m = typeText(t, m, "  rebase onto main  ")
	m = pressAndSettle(t, m, "enter")

	if m.mode != modeNormal {
		t.Errorf("the input row is still open after enter")
	}
	var saved *task.Task
	for _, tk := range listAll(t, db) {
		if tk.Text == "rebase onto main" {
			saved = &tk
		}
	}
	if saved == nil {
		t.Fatalf("no task with the trimmed text was saved: %v", texts(listAll(t, db)))
	}
	if saved.Scope != sessionScope {
		t.Errorf("saved scope = %+v, want %+v", saved.Scope, sessionScope)
	}
	if m.cursor < 0 || m.cursor >= len(m.tasks) || m.tasks[m.cursor].ID != saved.ID {
		t.Errorf("cursor is on %d, not on the new task", m.cursor)
	}
	if len(sticky.kinds) != 1 || sticky.kinds[0] != task.ScopeSession {
		t.Errorf("SetSticky calls = %v, want one call with session", sticky.kinds)
	}
}

// TestAddWithNoTextCancels is DoD 4: nothing exists yet, so closing the row
// loses nothing — and it must not move the sticky default either.
func TestAddWithNoTextCancels(t *testing.T) {
	for _, tc := range []struct {
		name  string
		typed string
		key   string
	}{
		{"empty enter", "", "enter"},
		{"whitespace enter", "   ", "enter"},
		{"esc after typing", "half a thought", "esc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			sticky := &stickyRecorder{}
			m := newLoaded(t, Config{DB: db, Scopes: allScopes(), SetSticky: sticky.set})

			m = pressAndSettle(t, m, "a")
			m = typeText(t, m, tc.typed)
			m = pressAndSettle(t, m, tc.key)

			if m.mode != modeNormal {
				t.Errorf("mode = %d, want the row closed", m.mode)
			}
			if got := listAll(t, db); len(got) != 0 {
				t.Errorf("a task was created anyway: %v", texts(got))
			}
			if len(sticky.kinds) != 0 {
				t.Errorf("SetSticky was called for a cancelled add: %v", sticky.kinds)
			}
		})
	}
}

// TestStickyFailureDoesNotFailTheAdd is DoD 5. By the time the preference is
// written the task is already saved, so a state-dir problem must not read as a
// failed add.
func TestStickyFailureDoesNotFailTheAdd(t *testing.T) {
	db := openDB(t)
	sticky := &stickyRecorder{err: errors.New("state dir is read-only")}
	m := newLoaded(t, Config{DB: db, Scopes: allScopes(), SetSticky: sticky.set})

	m = pressAndSettle(t, m, "a")
	m = typeText(t, m, "call the dentist")
	m = pressAndSettle(t, m, "enter")

	if m.err != nil {
		t.Errorf("the add reported an error from the sticky write: %v", m.err)
	}
	if got := texts(m.tasks); len(got) != 1 || got[0] != "call the dentist" {
		t.Errorf("rows = %v, want the saved task on screen", got)
	}
	if !strings.Contains(m.View(), "call the dentist") {
		t.Errorf("the saved task is not visible:\n%s", m.View())
	}
}

// A nil SetSticky is a supported configuration — "do not remember" — not a
// crash.
func TestAddWithoutAStickyWriter(t *testing.T) {
	db := openDB(t)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})
	m = pressAndSettle(t, m, "a")
	m = typeText(t, m, "no sticky here")
	m = pressAndSettle(t, m, "enter")

	if got := texts(m.tasks); len(got) != 1 {
		t.Errorf("rows = %v, want the task saved", got)
	}
}

// TestEditPrefillsAndSaves is DoD 6.
func TestEditPrefillsAndSaves(t *testing.T) {
	db := openDB(t)
	add(t, db, "older", globalScope)
	target := add(t, db, "fix auth redirct", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m = pressAndSettle(t, m, "e")
	if m.mode != modeInput || m.inputKind != inputEdit {
		t.Fatalf("e did not open an edit row (mode %d, kind %d)", m.mode, m.inputKind)
	}
	if got := m.input.value(); got != "fix auth redirct" {
		t.Errorf("input pre-filled with %q, want the task's current text", got)
	}
	if m.input.pos != len([]rune("fix auth redirct")) {
		t.Errorf("cursor at %d, want the end of the text", m.input.pos)
	}
	// "redirct" is missing its second e: step back over "ct" and type it in.
	m = pressAndSettle(t, m, "left")
	m = pressAndSettle(t, m, "left")
	m = typeText(t, m, "e")
	m = pressAndSettle(t, m, "enter")

	got := mustGet(t, db, target.ID)
	if got.Text != "fix auth redirect" {
		t.Errorf("saved text = %q, want the corrected text", got.Text)
	}
	if m.tasks[m.cursor].ID != target.ID {
		t.Errorf("cursor left the edited task")
	}
}

// TestEditRejectsEmptyText is DoD 7: the task is on screen, so blanking it
// silently would destroy something the user can see.
func TestEditRejectsEmptyText(t *testing.T) {
	db := openDB(t)
	target := add(t, db, "keep me", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m = pressAndSettle(t, m, "e")
	m = pressAndSettle(t, m, "ctrl+u") // clear the line
	m = pressAndSettle(t, m, "enter")

	if m.mode != modeInput {
		t.Errorf("the input row closed on a rejected edit")
	}
	if m.inputHint == "" {
		t.Errorf("nothing explained why enter did nothing")
	}
	if !strings.Contains(m.View(), "cannot be empty") {
		t.Errorf("the hint is not on screen:\n%s", m.View())
	}
	if got := mustGet(t, db, target.ID); got.Text != "keep me" {
		t.Errorf("text = %q, want it unchanged", got.Text)
	}
	// Typing again clears the complaint.
	m = typeText(t, m, "x")
	if m.inputHint != "" {
		t.Errorf("the hint survived the user answering it")
	}
}

// Esc abandons an edit, leaving the text exactly as it was.
func TestEditEscapeLeavesTheTextAlone(t *testing.T) {
	db := openDB(t)
	target := add(t, db, "original text", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m = pressAndSettle(t, m, "e")
	m = typeText(t, m, " and more")
	m = pressAndSettle(t, m, "esc")

	if m.mode != modeNormal {
		t.Errorf("esc did not close the row")
	}
	if got := mustGet(t, db, target.ID); got.Text != "original text" {
		t.Errorf("text = %q, want it unchanged", got.Text)
	}
}

// TestTabIsInertWhileEditing is DoD 8 — `e` is purely textual, and it must not
// insert a tab character either.
func TestTabIsInertWhileEditing(t *testing.T) {
	db := openDB(t)
	target := add(t, db, "some task", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m = pressAndSettle(t, m, "e")
	before := m.inputScope
	m = pressAndSettle(t, m, "tab")

	if m.inputScope != before {
		t.Errorf("tab changed the scope during an edit: %q -> %q", before, m.inputScope)
	}
	if got := m.input.value(); got != "some task" {
		t.Errorf("tab was typed into the field: %q", got)
	}
	m = pressAndSettle(t, m, "enter")
	if got := mustGet(t, db, target.ID); got.Scope != globalScope {
		t.Errorf("scope moved during an edit: %+v", got.Scope)
	}
}

// TestRescopeCyclesInPlace is DoD 9, including that it leaves the sticky default
// alone: correcting one old row must not redirect the next add.
func TestRescopeCyclesInPlace(t *testing.T) {
	db := openDB(t)
	target := add(t, db, "wrong scope", globalScope)
	sticky := &stickyRecorder{}
	m := newLoaded(t, Config{DB: db, Scopes: allScopes(), SetSticky: sticky.set})

	m = pressAndSettle(t, m, "s")

	got := mustGet(t, db, target.ID)
	if got.Scope != sessionScope {
		t.Errorf("scope = %+v, want it cycled to %+v", got.Scope, sessionScope)
	}
	if m.tasks[m.cursor].ID != target.ID {
		t.Errorf("the cursor did not follow the re-scoped task")
	}
	if len(sticky.kinds) != 0 {
		t.Errorf("re-scoping moved the sticky default: %v", sticky.kinds)
	}
}

// With only one scope there is nowhere to cycle to, and `s` must not write.
func TestRescopeWithOneScopeIsInert(t *testing.T) {
	db := openDB(t)
	target := add(t, db, "only global here", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})

	m = pressAndSettle(t, m, "s")
	if got := mustGet(t, db, target.ID); got.Scope != globalScope {
		t.Errorf("scope = %+v, want it untouched", got.Scope)
	}
}

// TestRescopeOutOfAFilteredTierClampsTheCursor is DoD 10 — a decision, not an
// accident: the row leaves the view and the cursor stays put rather than
// chasing it into a tier that is not on screen.
func TestRescopeOutOfAFilteredTierClampsTheCursor(t *testing.T) {
	db := openDB(t)
	add(t, db, "stays behind", globalScope)
	target := add(t, db, "moves away", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m = pressAndSettle(t, m, "3") // filter to global
	if len(m.tasks) != 2 {
		t.Fatalf("filtered rows = %v, want both global tasks", texts(m.tasks))
	}
	// Put the cursor on the newest row, which is the one we move.
	for i, tk := range m.tasks {
		if tk.ID == target.ID {
			m.cursor = i
		}
	}
	m = pressAndSettle(t, m, "s")

	if got := mustGet(t, db, target.ID); got.Scope == globalScope {
		t.Fatalf("the task did not move scope")
	}
	for _, tk := range m.tasks {
		if tk.ID == target.ID {
			t.Errorf("the re-scoped row is still in the filtered view")
		}
	}
	if m.cursor < 0 || m.cursor >= len(m.tasks) {
		t.Errorf("cursor = %d, out of range for %d rows", m.cursor, len(m.tasks))
	}
}

// TestInputModeTakesCommandKeysAsText is DoD 19, and it is the reason Update
// dispatches on the mode at all.
func TestInputModeTakesCommandKeysAsText(t *testing.T) {
	db := openDB(t)
	add(t, db, "an existing task", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})
	before := len(m.tasks)

	m = pressAndSettle(t, m, "a")
	const typed = "q j k d u ? 1 2 3 s e a"
	m = typeText(t, m, typed)

	if m.quitting {
		t.Fatal("typing q into the input row quit the popup")
	}
	if m.mode != modeInput {
		t.Fatalf("mode = %d, want the input row still open", m.mode)
	}
	if got := m.input.value(); got != typed {
		t.Errorf("field holds %q, want every key as literal text %q", got, typed)
	}
	if m.filter != "" {
		t.Errorf("a digit changed the filter to %q", m.filter)
	}
	if len(m.queued) != 0 {
		t.Errorf("d queued a delete from inside the input row")
	}
	if len(m.tasks) != before {
		t.Errorf("rows changed while typing: %v", texts(m.tasks))
	}
}

// TestHelpOverlayReplacesTheListAndGatesKeys is DoD 16.
func TestHelpOverlayReplacesTheListAndGatesKeys(t *testing.T) {
	db := openDB(t)
	add(t, db, "a visible task", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes(), Version: "1.2.3"})

	helped := pressAndSettle(t, m, "?")
	if helped.mode != modeHelp {
		t.Fatalf("mode = %d, want modeHelp", helped.mode)
	}
	view := helped.View()
	if strings.Contains(view, "a visible task") {
		t.Errorf("the overlay does not replace the list:\n%s", view)
	}
	for _, want := range []string{"a add", "d delete", "u undo", "1/2/3", "1.2.3"} {
		if !strings.Contains(view, want) {
			t.Errorf("the overlay is missing %q:\n%s", want, view)
		}
	}

	// Mutation keys are inert behind it: they would act on rows nobody can see.
	for _, key := range []string{"d", "a", "e", "s", " ", "j"} {
		next, _ := press(t, helped, key)
		if next.mode != modeHelp {
			t.Errorf("key %q left the overlay", key)
		}
		if len(next.queued) != 0 || next.cursor != helped.cursor {
			t.Errorf("key %q acted on the list from behind the overlay", key)
		}
	}

	for _, key := range []string{"?", "esc", "q"} {
		back, cmd := press(t, helped, key)
		if back.mode != modeNormal {
			t.Errorf("key %q did not dismiss the overlay", key)
		}
		if back.quitting {
			t.Errorf("key %q quit the popup instead of dismissing the overlay", key)
		}
		if cmd != nil {
			t.Errorf("key %q produced a command", key)
		}
	}
}

// The overlay is clipped to the rows the list has, so it can never make the
// frame taller than chromeHeight promises.
func TestHelpOverlayIsClippedToTheList(t *testing.T) {
	db := openDB(t)
	add(t, db, "something", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes(), Version: longVersion})
	sized, _ := m.Update(windowSize(48, 10))
	m = sized.(Model)
	m = pressAndSettle(t, m, "?")

	if got, want := len(m.helpBody()), m.listHeight(); got > want {
		t.Errorf("overlay is %d rows in a %d-row list", got, want)
	}
}
