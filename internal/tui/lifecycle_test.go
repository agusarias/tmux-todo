package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// frozen returns a clock stuck at t, so "hidden after 24h" is provable rather
// than asserted.
func frozen(t time.Time) func() time.Time { return func() time.Time { return t } }

// pressSpace sends the toggle key and folds the resulting message back in, as
// the Bubble Tea runtime would.
func pressSpace(t *testing.T, m Model) Model {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
	m = next.(Model)
	if cmd == nil {
		return m
	}
	settled, _ := m.Update(cmd())
	return settled.(Model)
}

func mustGet(t *testing.T, db *store.DB, id int64) task.Task {
	t.Helper()
	got, err := db.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("Get %d: %v", id, err)
	}
	return got
}

// TestSpaceTogglesBothDirections — DoD 1. Completion is reversible, because it
// is the only undo the product has.
func TestSpaceTogglesBothDirections(t *testing.T) {
	db := openDB(t)
	added := add(t, db, "toggle me", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})

	m = pressSpace(t, m)
	if !mustGet(t, db, added.ID).Done {
		t.Fatal("space did not complete the cursor row")
	}
	if len(m.tasks) != 1 || !m.tasks[0].Done {
		t.Errorf("model rows after completing = %+v, want the row present and done", m.tasks)
	}

	m = pressSpace(t, m)
	if mustGet(t, db, added.ID).Done {
		t.Error("space did not uncomplete an already-done row")
	}
	if len(m.tasks) != 1 || m.tasks[0].Done {
		t.Errorf("model rows after uncompleting = %+v, want the row present and pending", m.tasks)
	}
}

// TestSpaceKeepsTheRowVisibleAndInPlace — the design's core feel requirement:
// the row must not vanish under the cursor, and must not jump.
func TestSpaceKeepsTheRowVisibleAndInPlace(t *testing.T) {
	db := openDB(t)
	for _, text := range []string{"first", "second", "third"} {
		add(t, db, text, globalScope)
	}
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})

	// Put the cursor on the middle row.
	m = pressAndSettle(t, m, "j")
	middle := m.tasks[m.cursor]
	if middle.Text != "second" {
		t.Fatalf("cursor is on %q, expected the middle row", middle.Text)
	}

	m = pressSpace(t, m)

	if got := texts(m.tasks); len(got) != 3 {
		t.Fatalf("rows = %q, want all three still visible", got)
	}
	if m.tasks[1].ID != middle.ID {
		t.Errorf("completing reordered the list: position 1 is now %q", m.tasks[1].Text)
	}
	if !m.tasks[1].Done {
		t.Error("the toggled row is not marked done")
	}
	if !strings.Contains(m.View(), middle.Text) {
		t.Errorf("the completed row vanished from the view:\n%s", m.View())
	}
}

// TestCursorReAnchorsOnTaskID — DoD 6. Re-anchoring by index would drift onto a
// neighbour whenever the visible row set changes, which makes pressing space
// twice on one row impossible.
func TestCursorReAnchorsOnTaskID(t *testing.T) {
	db := openDB(t)
	for _, text := range []string{"first", "second", "third"} {
		add(t, db, text, globalScope)
	}
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})
	m = pressAndSettle(t, m, "j")
	target := m.tasks[m.cursor]

	m = pressSpace(t, m)
	if got := m.tasks[m.cursor]; got.ID != target.ID {
		t.Errorf("cursor moved to %q (id %d), want to stay on %q (id %d)",
			got.Text, got.ID, target.Text, target.ID)
	}

	// And pressing it again must undo the same row, not a neighbour.
	m = pressSpace(t, m)
	if mustGet(t, db, target.ID).Done {
		t.Error("the second press did not undo the same row")
	}
}

// TestCursorReAnchorsWhenRowsShift is the test that actually discriminates
// between anchoring by id and by index.
//
// TestCursorReAnchorsOnTaskID above matches DoD 6's wording, but on its own it
// is vacuous: completing a row does not reorder the list, so the cursor's index
// still lands on the same task either way — it passes with the id lookup deleted.
// Indices only shift when the row *set* changes, which happens for real when
// another pane adds a task while the popup is open: new rows sort newest-first,
// so they land above the cursor and push it down.
func TestCursorReAnchorsWhenRowsShift(t *testing.T) {
	db := openDB(t)
	for _, text := range []string{"first", "second", "third"} {
		add(t, db, text, globalScope)
	}
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})
	m = pressAndSettle(t, m, "j")
	target := m.tasks[m.cursor]
	before := m.cursor

	// Another pane adds two tasks. They are newer, so they sort above every
	// existing row and shift the cursor's task down by two.
	add(t, db, "from another pane", globalScope)
	add(t, db, "and another", globalScope)

	next, _ := m.Update(m.reloadAnchoredTo(target.ID)())
	got := next.(Model)

	if got.tasks[got.cursor].ID != target.ID {
		t.Errorf("cursor landed on %q, want to stay anchored to %q",
			got.tasks[got.cursor].Text, target.Text)
	}
	if got.cursor == before {
		t.Errorf("cursor index is still %d; the rows did not shift, so this test proves nothing", before)
	}
}

// TestCursorClampsWhenAnchorLeavesTheView — completing a row that was already
// outside the visibility window removes it, so the anchor is legitimately gone.
func TestCursorClampsWhenAnchorLeavesTheView(t *testing.T) {
	db := openDB(t)
	now := time.Unix(1_760_000_000, 0)
	for _, text := range []string{"first", "second"} {
		add(t, db, text, globalScope)
	}

	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}, Now: frozen(now)})
	m = pressAndSettle(t, m, "j")

	// Rows arrive with an anchor id that is not in the list at all.
	next, _ := m.Update(rowsMsg{tasks: m.tasks[:1], anchor: 9999})
	got := next.(Model)
	if got.cursor < 0 || got.cursor >= len(got.tasks) {
		t.Errorf("cursor = %d with %d rows; a missing anchor must clamp, not dangle",
			got.cursor, len(got.tasks))
	}
}

// TestDoneRowCompletedBeforeOpenIsHidden — a row already done when the popup
// opened is not "reversible in the moment", so it starts out hidden.
func TestDoneRowCompletedBeforeOpenIsHidden(t *testing.T) {
	db := openDB(t)
	stale := add(t, db, "finished earlier", globalScope)
	pending := add(t, db, "still to do", globalScope)
	if err := db.Complete(context.Background(), stale.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	// The popup opens a minute later.
	open := time.Now().Add(time.Minute)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}, Now: frozen(open)})

	if got := texts(m.tasks); len(got) != 1 || got[0] != pending.Text {
		t.Errorf("rows = %q, want only the pending task", got)
	}
	// Hidden, never deleted.
	if _, err := db.Get(context.Background(), stale.ID); err != nil {
		t.Errorf("the hidden row left the database: %v", err)
	}
}

// TestDoneRowHiddenAfterRetentionWindow — DoD 3, and the reason the clock is
// injected at all: advance it past the retention window and the row leaves the
// view while staying in the database.
func TestDoneRowHiddenAfterRetentionWindow(t *testing.T) {
	db := openDB(t)
	added := add(t, db, "completed in this session", globalScope)
	if err := db.Complete(context.Background(), added.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	done := mustGet(t, db, added.ID)

	// A popup opened long ago, so openedAt loses to the retention arm and the
	// 24h boundary is the one actually doing the work.
	openedLongAgo := done.DoneAt.Add(-time.Hour)
	cfg := Config{DB: db, Scopes: []task.Scope{globalScope}, Now: frozen(openedLongAgo)}

	m := New(cfg)
	m.openedAt = openedLongAgo

	// Still inside the window: visible.
	m.cfg.Now = frozen(done.DoneAt.Add(time.Hour))
	next, _ := m.Update(m.reloadCmd()())
	if visible := next.(Model); len(visible.tasks) != 1 {
		t.Fatalf("rows = %q an hour after completion, want the row visible", texts(visible.tasks))
	}

	// 25h later: gone from the view.
	m.cfg.Now = frozen(done.DoneAt.Add(store.DoneRetention + time.Hour))
	next, _ = m.Update(m.reloadCmd()())
	aged := next.(Model)
	if len(aged.tasks) != 0 {
		t.Errorf("rows = %q past the retention window, want it hidden", texts(aged.tasks))
	}
	if _, err := db.Get(context.Background(), added.ID); err != nil {
		t.Errorf("the aged-out row was deleted; this task never reaps: %v", err)
	}
}

// TestDoneSinceIsTheLaterBoundary pins the max() arithmetic in both directions.
func TestDoneSinceIsTheLaterBoundary(t *testing.T) {
	base := time.Unix(1_760_000_000, 0)

	// Popup opened recently: openedAt wins over now-24h.
	recent := Model{cfg: Config{Now: frozen(base)}, openedAt: base.Add(-time.Minute)}
	if got, want := recent.doneSince(), base.Add(-time.Minute); !got.Equal(want) {
		t.Errorf("with a fresh popup doneSince = %v, want the open time %v", got, want)
	}

	// Popup left open for days: the retention window wins.
	stale := Model{cfg: Config{Now: frozen(base)}, openedAt: base.Add(-72 * time.Hour)}
	if got, want := stale.doneSince(), base.Add(-store.DoneRetention); !got.Equal(want) {
		t.Errorf("with a long-open popup doneSince = %v, want the retention cutoff %v", got, want)
	}
}

// TestConfigNowDefaultsToRealClock — the injection must not require every caller
// to supply a clock.
func TestConfigNowDefaultsToRealClock(t *testing.T) {
	before := time.Now()
	got := Config{}.now()
	if got.Before(before.Add(-time.Minute)) || got.After(time.Now().Add(time.Minute)) {
		t.Errorf("Config{}.now() = %v, want roughly the real clock", got)
	}
}

// TestStruckRowsAreNotAnEmptyState — DoD 10. A tier whose only rows are done
// shows those struck rows, not "matched nothing".
func TestStruckRowsAreNotAnEmptyState(t *testing.T) {
	db := openDB(t)
	sessionTask := add(t, db, "session thing", sessionScope)
	add(t, db, "global thing", globalScope)

	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{sessionScope, globalScope}})
	// Complete the only session row during this session, then filter to it.
	m = pressSpace(t, m)
	if !mustGet(t, db, sessionTask.ID).Done {
		t.Fatal("setup: the session row was not completed")
	}

	filtered := pressAndSettle(t, m, "1")
	if len(filtered.tasks) != 1 {
		t.Fatalf("rows = %q, want the struck session row still listed", texts(filtered.tasks))
	}
	view := filtered.View()
	if strings.Contains(view, "no session tasks") {
		t.Errorf("a tier holding only done rows rendered the empty state:\n%s", view)
	}
	if !strings.Contains(view, "session thing") {
		t.Errorf("the struck row is missing from the view:\n%s", view)
	}
}

// TestSpaceOnAnEmptyListIsInert — no cursor row, nothing to toggle, no panic.
func TestSpaceOnAnEmptyListIsInert(t *testing.T) {
	db := openDB(t)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})
	if got := pressSpace(t, m); len(got.tasks) != 0 || got.quitting {
		t.Errorf("space on an empty list changed the model: %+v", got.tasks)
	}
}

// TestToggleFailureIsSurfaced — a write that fails must say so rather than
// looking like a no-op.
func TestToggleFailureIsSurfaced(t *testing.T) {
	db := openDB(t)
	add(t, db, "toggle me", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})

	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	broken := pressSpace(t, m)
	if broken.err == nil {
		t.Fatal("toggling against a closed database reported no error")
	}
	// The store names the operation itself; the view must not swallow it.
	if view := broken.View(); !strings.Contains(view, "complete task") {
		t.Errorf("the failure is not surfaced in the view:\n%s", view)
	}
}
