package tui

import (
	"testing"
	"time"

	"github.com/agusarias/tmux-todo/internal/config"
	"github.com/agusarias/tmux-todo/internal/task"
)

// Where the cursor goes when a row is toggled.
//
// **Every test in this file sets complete-to-bottom `always`, and that is not
// incidental.** Under the shipped default (`on-start`) a row you complete does
// not move at all, so the cursor lands on it whether it followed or held its
// index — every assertion here would pass with follow-on-complete deleted. This
// is the third time this package has had to be careful about exactly that; see
// TestCursorReAnchorsOnTaskID's history in CLAUDE.md.
//
// TestFollowSettingsAreVacuousUnderTheDefault at the bottom is what stops that
// caution from being a comment nobody re-checks: it asserts the vacuity, so if
// the default ever changes such that a completed row *does* move, that test goes
// red and points here.

// alwaysPrefs is config.Defaults() with the placement forced to `always`, which
// is the only configuration in which the cursor can be observed to follow or not
// follow a completed row.
func alwaysPrefs(followComplete, followUncomplete bool) config.Prefs {
	return config.Prefs{
		FollowOnComplete:   followComplete,
		FollowOnUncomplete: followUncomplete,
		CompleteToBottom:   config.Always,
	}
}

// threeGlobal seeds three global tasks and returns a model with the cursor on
// the middle one. Newest sorts first, so the rows read third, second, first.
func threeGlobal(t *testing.T, prefs config.Prefs) (Model, []task.Task) {
	t.Helper()
	db := openDB(t)
	var added []task.Task
	for _, text := range []string{"first", "second", "third"} {
		added = append(added, add(t, db, text, globalScope))
	}
	m := newLoaded(t, Config{
		DB:     db,
		Scopes: []task.Scope{globalScope},
		Prefs:  prefs,
		Now:    frozen(time.Unix(1_760_000_000, 0)),
	})
	m = pressAndSettle(t, m, "j") // onto "second", the middle row
	if got := m.tasks[m.cursor].Text; got != "second" {
		t.Fatalf("fixture: cursor starts on %q, want the middle row", got)
	}
	return m, added
}

// TestCursorHoldsItsPositionOnComplete is the task's headline: the default.
//
// Completing the middle row sinks it to the tier's end, so the row that was
// below it slides up into the cursor's slot and is what gets selected.
func TestCursorHoldsItsPositionOnComplete(t *testing.T) {
	m, _ := threeGlobal(t, alwaysPrefs(false, true))
	before := m.cursor

	m = pressSpace(t, m)

	if m.cursor != before {
		t.Errorf("cursor moved from index %d to %d; want it to hold its position", before, m.cursor)
	}
	if got := m.tasks[m.cursor].Text; got != "first" {
		t.Errorf("cursor landed on %q, want %q — the row that slid up", got, "first")
	}
	if m.tasks[m.cursor].Done {
		t.Error("cursor is on a done row; it followed the completed task down")
	}
}

// TestCursorFollowsOnCompleteWhenAsked is the same fixture with the setting on,
// and is what proves the setting is read rather than the behaviour hard-coded.
func TestCursorFollowsOnCompleteWhenAsked(t *testing.T) {
	m, added := threeGlobal(t, alwaysPrefs(true, true))
	target := m.tasks[m.cursor]

	m = pressSpace(t, m)

	if got := m.tasks[m.cursor]; got.ID != target.ID {
		t.Errorf("cursor landed on %q, want it to follow %q down", got.Text, target.Text)
	}
	if !m.tasks[m.cursor].Done {
		t.Error("the followed row is not done")
	}
	// And it really did move: the completed row is last, not where it started.
	if last := m.tasks[len(m.tasks)-1]; last.ID != added[1].ID {
		t.Errorf("last row is %q, want the completed %q — the fixture did not reorder, so this test proves nothing",
			last.Text, added[1].Text)
	}
}

// TestCursorFollowsOnUncompleteByDefault — uncompleting is "I want this back",
// so the resurrected row is what you are left pointing at.
func TestCursorFollowsOnUncompleteByDefault(t *testing.T) {
	m, _ := threeGlobal(t, alwaysPrefs(true, true))
	target := m.tasks[m.cursor]

	m = pressSpace(t, m) // complete; cursor follows it to the end
	if m.tasks[m.cursor].ID != target.ID {
		t.Fatalf("fixture: cursor is not on the completed row")
	}
	m = pressSpace(t, m) // uncomplete

	if got := m.tasks[m.cursor]; got.ID != target.ID {
		t.Errorf("cursor landed on %q, want it to follow %q back up", got.Text, target.Text)
	}
	if m.tasks[m.cursor].Done {
		t.Error("the row under the cursor is still done")
	}
	// The row moved back up, so following it was a real choice.
	if m.cursor == len(m.tasks)-1 {
		t.Error("the uncompleted row is still last; the fixture did not reorder")
	}
}

// TestCursorHoldsItsPositionOnUncompleteWhenAsked — the other direction of the
// other setting, so all four combinations are pinned.
func TestCursorHoldsItsPositionOnUncompleteWhenAsked(t *testing.T) {
	m, _ := threeGlobal(t, alwaysPrefs(true, false))
	target := m.tasks[m.cursor]

	m = pressSpace(t, m) // complete; follow-on-complete is on, so cursor goes down
	if m.tasks[m.cursor].ID != target.ID {
		t.Fatalf("fixture: cursor is not on the completed row")
	}
	at := m.cursor

	m = pressSpace(t, m) // uncomplete; follow-on-uncomplete is off

	if m.cursor != at {
		t.Errorf("cursor moved from index %d to %d; want it to hold its position", at, m.cursor)
	}
	if got := m.tasks[m.cursor]; got.ID == target.ID {
		t.Errorf("cursor is still on %q; it followed the row back up", got.Text)
	}
}

// TestCursorHoldsPositionCompletingTheLastRow — DoD 9's second half. There is no
// row to slide up, so holding the index must clamp rather than dangle.
func TestCursorHoldsPositionCompletingTheLastRow(t *testing.T) {
	m, _ := threeGlobal(t, alwaysPrefs(false, true))
	m = pressAndSettle(t, m, "j") // onto the last row
	if m.cursor != len(m.tasks)-1 {
		t.Fatalf("fixture: cursor is at %d, want the last of %d rows", m.cursor, len(m.tasks))
	}

	m = pressSpace(t, m)

	if m.cursor < 0 || m.cursor >= len(m.rows) {
		t.Fatalf("cursor = %d with %d rows; holding the index must clamp", m.cursor, len(m.rows))
	}
	if !m.rows[m.cursor].selectable() {
		t.Error("cursor came to rest on an unselectable row")
	}
}

// TestCursorHoldsItsPositionInTheAllTasksView — DoD 9. The all-tasks view's rows
// interleave unselectable group headers, so "hold the index" has a way to be
// wrong here that the merged view does not: landing on a header.
func TestCursorHoldsItsPositionInTheAllTasksView(t *testing.T) {
	db := openDB(t)
	// Two rows in one group, so completing the first leaves a sibling to slide
	// up, and a second group below it so a mis-clamp has somewhere wrong to go.
	first := add(t, db, "g-first", globalScope)
	add(t, db, "g-second", globalScope)
	add(t, db, "s-only", sessionScope)

	m := newLoaded(t, Config{
		DB:       db,
		Scopes:   []task.Scope{sessionScope, globalScope},
		Prefs:    alwaysPrefs(false, true),
		Now:      frozen(time.Unix(1_760_000_000, 0)),
		AllTasks: true,
	})
	if m.view != viewAll {
		t.Fatalf("fixture: view = %v, want the all-tasks view", m.view)
	}
	// Walk down to "g-second"'s group, onto the newest global row.
	for m.tasks[indexOfTask(t, m)].ID != mustNewestGlobal(t, m) {
		m = pressAndSettle(t, m, "j")
	}
	before := m.cursor
	target, ok := m.selectedTask()
	if !ok {
		t.Fatal("fixture: cursor is not on a task")
	}

	m = pressSpace(t, m)

	if !m.rows[m.cursor].selectable() {
		t.Fatalf("cursor rests on a group header at index %d", m.cursor)
	}
	if m.cursor != before {
		t.Errorf("cursor moved from %d to %d; want it to hold its position", before, m.cursor)
	}
	if got, _ := m.selectedTask(); got.ID == target.ID {
		t.Errorf("cursor is still on %q; it followed the completed row", got.Text)
	}
	_ = first
}

// indexOfTask is the cursor's index into m.tasks, which in the all-tasks view is
// a different coordinate system from m.cursor.
func indexOfTask(t *testing.T, m Model) int {
	t.Helper()
	sel, ok := m.selectedTask()
	if !ok {
		t.Fatal("cursor is not on a task")
	}
	for i, task := range m.tasks {
		if task.ID == sel.ID {
			return i
		}
	}
	t.Fatal("cursor's task is not in m.tasks")
	return 0
}

// mustNewestGlobal is the id of the newest global row, which is where the
// all-tasks fixture wants its cursor.
func mustNewestGlobal(t *testing.T, m Model) int64 {
	t.Helper()
	for _, g := range m.groups {
		if g.Scope.Kind == task.ScopeGlobal && len(g.Tasks) > 0 {
			return g.Tasks[0].ID
		}
	}
	t.Fatal("no global group")
	return 0
}

// TestFollowSettingsAreVacuousUnderTheDefault states the trap this file exists
// to avoid, as an assertion rather than a comment.
//
// Under complete-to-bottom `on-start` a row completed during this session does
// not move, so the cursor is on it either way and neither follow setting is
// observable. Every cursor test above therefore forces `always`. If a future
// change makes a completed row move under the default, this goes red — and the
// fix is to re-read the tests above, not to delete this.
func TestFollowSettingsAreVacuousUnderTheDefault(t *testing.T) {
	for _, follow := range []bool{true, false} {
		prefs := config.Defaults()
		prefs.FollowOnComplete = follow

		db := openDB(t)
		for _, text := range []string{"first", "second", "third"} {
			add(t, db, text, globalScope)
		}
		m := newLoaded(t, Config{
			DB:     db,
			Scopes: []task.Scope{globalScope},
			Prefs:  prefs,
			Now:    frozen(time.Unix(1_760_000_000, 0)),
		})
		m = pressAndSettle(t, m, "j")
		target := m.tasks[m.cursor]
		before := m.cursor

		m = pressSpace(t, m)

		if m.cursor != before || m.tasks[m.cursor].ID != target.ID {
			t.Errorf("follow-on-complete=%v under the default placement moved the cursor "+
				"from %d (%q) to %d (%q). A completed row now moves under the default, so the "+
				"tests in this file may no longer be forcing `always` for the right reason",
				follow, before, target.Text, m.cursor, m.tasks[m.cursor].Text)
		}
		if !m.tasks[m.cursor].Done {
			t.Errorf("follow-on-complete=%v: the row under the cursor is not the one just completed", follow)
		}
	}
}
