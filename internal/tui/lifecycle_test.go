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

// TestSpaceMovesTheRowToTheEndOfItsTierAndTheCursorFollows is DoD 7, and it
// replaces TestSpaceKeepsTheRowVisibleAndInPlace.
//
// That test asserted the row "must not jump", which this task deliberately
// changed: a completed row now moves to the end of its tier (the user's
// placement call, 2026-08-21). What survives from it — and is the part the design
// actually cares about — is that the row does not vanish from under the cursor.
// So the requirement became "it moves, and the cursor moves with it", which is
// what keeps space-again-to-undo working on the row you just acted on.
//
// The round trip is the whole assertion: complete, complete again, and the row is
// back at its original index with its original id. A re-insert could not do that
// (store.Add would mint a new id and put it on top), so this also pins that the
// toggle stays a toggle.
func TestSpaceMovesTheRowToTheEndOfItsTierAndTheCursorFollows(t *testing.T) {
	db := openDB(t)
	for _, text := range []string{"first", "second", "third"} {
		add(t, db, text, globalScope)
	}
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})

	// Newest-first, so the list is [third second first] and j lands on "second".
	m = pressAndSettle(t, m, "j")
	middle := m.tasks[m.cursor]
	startIndex := m.cursor
	if middle.Text != "second" {
		t.Fatalf("cursor is on %q, expected the middle row", middle.Text)
	}

	m = pressSpace(t, m)

	if got := texts(m.tasks); len(got) != 3 {
		t.Fatalf("rows = %q, want all three still visible", got)
	}
	// Moved to the end of the tier...
	last := len(m.tasks) - 1
	if m.tasks[last].ID != middle.ID {
		t.Errorf("rows = %q, want the completed row %q last in its tier",
			texts(m.tasks), middle.Text)
	}
	if !m.tasks[last].Done {
		t.Error("the toggled row is not marked done")
	}
	// ...the pending rows keep their newest-first order above it...
	if got := texts(m.tasks); got[0] != "third" || got[1] != "first" {
		t.Errorf("pending rows = %q, want [third first] — completing must not reorder them", got[:2])
	}
	// ...the cursor went with it...
	if got := m.tasks[m.cursor]; got.ID != middle.ID {
		t.Errorf("cursor is on %q (id %d), want it to follow the completed row %q (id %d)",
			got.Text, got.ID, middle.Text, middle.ID)
	}
	if m.cursor != last {
		t.Errorf("cursor = %d, want %d (the end of the tier)", m.cursor, last)
	}
	// ...and it is still on screen, which is the requirement that predates this
	// task and must survive it.
	if !strings.Contains(m.View(), middle.Text) {
		t.Errorf("the completed row vanished from the view:\n%s", m.View())
	}

	// The round trip: space again undoes the same row and it returns to where it
	// was, with the id it always had.
	m = pressSpace(t, m)
	if mustGet(t, db, middle.ID).Done {
		t.Fatal("the second press did not undo the same row")
	}
	if got := texts(m.tasks); got[startIndex] != middle.Text {
		t.Errorf("after undo rows = %q, want %q back at index %d", got, middle.Text, startIndex)
	}
	if got := m.tasks[startIndex]; got.ID != middle.ID {
		t.Errorf("the row came back as id %d, want its original %d", got.ID, middle.ID)
	}
	if got := m.tasks[m.cursor]; got.ID != middle.ID {
		t.Errorf("after undo the cursor is on %q, want it still on %q", got.Text, middle.Text)
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

// TestDoneRowCompletedBeforeOpenIsVisible is DoD 2, and it is the assertion this
// task inverted.
//
// It used to be TestDoneRowCompletedBeforeOpenIsHidden and asserted the
// opposite, because doneSince() took the later of "popup opened" and "now-24h" —
// which made store.DoneRetention unreachable in practice and meant anything
// completed before you arrived was already gone. docs/design.md was amended
// (2026-08-21, user's instruction) to drop the openedAt clause; the old wording
// is quoted in the task brief's Decisions log.
//
// The row is completed by a *different* popup session — this one is created
// afterwards — which is the case the old rule could not show.
func TestDoneRowCompletedBeforeOpenIsVisible(t *testing.T) {
	db := openDB(t)
	stale := add(t, db, "finished earlier", globalScope)
	pending := add(t, db, "still to do", globalScope)
	if err := db.Complete(context.Background(), stale.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	done := mustGet(t, db, stale.ID)

	// A fresh popup, opening three hours after that completion.
	open := done.DoneAt.Add(3 * time.Hour)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}, Now: frozen(open)})

	got := texts(m.tasks)
	if len(got) != 2 {
		t.Fatalf("rows = %q, want both the pending and the recently-done row", got)
	}
	// Pending first, done at the end of the tier — DoD 4 in its smallest form.
	if got[0] != pending.Text || got[1] != stale.Text {
		t.Errorf("rows = %q, want [%q %q]: pending first, done last",
			got, pending.Text, stale.Text)
	}
	// Struck through, so "visible" does not read as "still to do". Asserted on
	// the style object: a test process has no colour profile, so lipgloss
	// renders plain text and an assertion over escapes would pass either way.
	if !textStyle(m.tasks[1], false).GetStrikethrough() {
		t.Error("the done row is not struck through, so it reads as pending")
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

	// No openedAt to arrange any more: the 24h boundary is the only rule, so
	// the clock is the whole input.
	m := New(Config{DB: db, Scopes: []task.Scope{globalScope}, Now: frozen(*done.DoneAt)})

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

// TestDoneSinceIsTheRetentionWindow is DoD 1: one retention window ago, and
// nothing else. It replaces TestDoneSinceIsTheLaterBoundary, which pinned the
// max() against openedAt that this task removed.
//
// The zero-value leg is the one worth having: with the field gone, a Model built
// without ever opening still has to answer now-24h rather than something derived
// from an uninitialised time.
func TestDoneSinceIsTheRetentionWindow(t *testing.T) {
	base := time.Unix(1_760_000_000, 0)
	m := Model{cfg: Config{Now: frozen(base)}}
	if got, want := m.doneSince(), base.Add(-store.DoneRetention); !got.Equal(want) {
		t.Errorf("doneSince = %v, want %v (now - DoneRetention)", got, want)
	}
	// It must not depend on when the popup was built. New() stamped openedAt
	// once; a doneSince that still consulted any such field would differ here.
	built := New(Config{Now: frozen(base)})
	if got, want := built.doneSince(), base.Add(-store.DoneRetention); !got.Equal(want) {
		t.Errorf("after New, doneSince = %v, want %v — it must not depend on open time", got, want)
	}
}

// TestDoneVisibilityEdges is DoD 3 at both edges. A window asserted only in the
// middle would pass with the comparison inverted or off by an hour.
func TestDoneVisibilityEdges(t *testing.T) {
	for _, tc := range []struct {
		name  string
		since time.Duration
		want  bool
	}{
		{"just completed", 0, true},
		{"3h ago", 3 * time.Hour, true},
		{"23h59m ago", 23*time.Hour + 59*time.Minute, true},
		{"24h01m ago", 24*time.Hour + time.Minute, false},
		{"25h ago", 25 * time.Hour, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			row := add(t, db, "finished", globalScope)
			if err := db.Complete(context.Background(), row.ID); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			done := mustGet(t, db, row.ID)

			m := newLoaded(t, Config{
				DB:     db,
				Scopes: []task.Scope{globalScope},
				Now:    frozen(done.DoneAt.Add(tc.since)),
			})

			visible := len(m.tasks) == 1
			if visible != tc.want {
				t.Errorf("completed %v ago: visible = %v, want %v (rows = %q)",
					tc.since, visible, tc.want, texts(m.tasks))
			}
			// Never deleted, at either edge. This task reaps nothing.
			if _, err := db.Get(context.Background(), row.ID); err != nil {
				t.Errorf("the row left the database: %v", err)
			}
		})
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
