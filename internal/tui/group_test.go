package tui

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// TestRehomeMovesTheWholeGroup is DoD 14: `r` bulk-Rescopes every task in the
// group under the cursor to the next *currently active* scope.
func TestRehomeMovesTheWholeGroup(t *testing.T) {
	db := openDB(t)
	stranded := []string{"fix flaky test", "restart the worker", "bump the dep"}
	for _, text := range stranded {
		add(t, db, text, deadSession)
	}
	add(t, db, "unrelated", globalScope)
	m := openAll(t, allTasksConfig(db))

	m.cursor = rowOf(t, m, "restart the worker") // mid-group, not the first row
	m = pressAndSettle(t, m, "r")

	// session -> dir, the next tier in the active cycle.
	for _, text := range stranded {
		got := taskByText(t, listAll(t, db), text)
		if got.Scope != dirScope {
			t.Errorf("%q is filed under %v after r, want %v", text, got.Scope, dirScope)
		}
	}
	// And only that group moved.
	if got := taskByText(t, listAll(t, db), "unrelated"); got.Scope != globalScope {
		t.Errorf("r moved an unrelated group's task to %v", got.Scope)
	}
	// The cursor followed the group rather than staying at an index that now
	// belongs to somebody else.
	if got, ok := m.selectedTask(); !ok || got.Scope != dirScope {
		t.Errorf("after r the cursor is on %+v, want a re-homed row", got)
	}
}

// TestRehomeCyclesRoundToTheLiveSession — three presses walk the active cycle,
// and the last one lands the stranded tasks in the session the client is
// actually in. That is the motivating case for the feature, and it is only
// reachable because `r` cycles rather than picking one target.
func TestRehomeCyclesRoundToTheLiveSession(t *testing.T) {
	db := openDB(t)
	add(t, db, "fix flaky test", deadSession)
	m := openAll(t, allTasksConfig(db))

	want := []task.Scope{dirScope, globalScope, sessionScope}
	for i, wantScope := range want {
		m.cursor = rowOf(t, m, "fix flaky test")
		m = pressAndSettle(t, m, "r")
		got := taskByText(t, listAll(t, db), "fix flaky test")
		if got.Scope != wantScope {
			t.Fatalf("press %d put it in %v, want %v", i+1, got.Scope, wantScope)
		}
	}
}

// TestRehomeDoesNotTouchTheStickyDefault — re-homing is a correction to those
// tasks, not a statement about where the next add should go, exactly as `s` is
// for a single row.
func TestRehomeDoesNotTouchTheStickyDefault(t *testing.T) {
	db := openDB(t)
	add(t, db, "fix flaky test", deadSession)
	var wrote []task.ScopeKind
	cfg := allTasksConfig(db)
	cfg.SetSticky = func(k task.ScopeKind) error { wrote = append(wrote, k); return nil }

	m := openAll(t, cfg)
	m.cursor = rowOf(t, m, "fix flaky test")
	pressAndSettle(t, m, "r")

	if len(wrote) != 0 {
		t.Errorf("r wrote the sticky default %v; only the merged view's add does that", wrote)
	}
}

// TestRehomeWithNowhereToGoIsInert — one available tier means the cycle is a
// no-op, and a no-op must not be a write.
func TestRehomeWithNowhereToGoIsInert(t *testing.T) {
	db := openDB(t)
	add(t, db, "call the dentist", globalScope)
	cfg := allTasksConfig(db)
	cfg.Scopes = []task.Scope{globalScope}

	m := openAll(t, cfg)
	if _, cmd := press(t, m, "r"); cmd != nil {
		t.Errorf("r with one available scope issued a command, want none")
	}
	if got := taskByText(t, listAll(t, db), "call the dentist"); got.Scope != globalScope {
		t.Errorf("the task moved to %v", got.Scope)
	}
}

// TestGroupDeleteQueuesEveryTaskAndUndoRestoresThemAll is DoD 15: one keypress
// against a group of many, still queued rather than written, and one `u` brings
// the whole group back with its ids intact.
func TestGroupDeleteQueuesEveryTaskAndOneUndoRestoresThemAll(t *testing.T) {
	db := openDB(t)
	var doomed []int64
	for _, text := range []string{"fix flaky test", "restart the worker", "bump the dep"} {
		doomed = append(doomed, add(t, db, text, deadSession).ID)
	}
	keep := add(t, db, "call the dentist", globalScope)
	m := openAll(t, allTasksConfig(db))

	before := ids(m.tasks)
	m.cursor = rowOf(t, m, "restart the worker")
	m = pressAndSettle(t, m, "D")

	// One step holding the whole group: that is what makes one u enough.
	if len(m.queued) != 1 || len(m.queued[0]) != len(doomed) {
		t.Fatalf("queue = %v, want one undo step holding all %d tasks in the group", m.queued, len(doomed))
	}
	if got := texts(m.tasks); len(got) != 1 || got[0] != keep.Text {
		t.Errorf("rows after D = %v, want the other group alone", got)
	}
	// Nothing written: the whole point of routing through the queue.
	if got := listAll(t, db); len(got) != 4 {
		t.Errorf("store holds %v after D, want all four rows", texts(got))
	}

	// One u, and the group is back exactly as it was.
	m = pressAndSettle(t, m, "u")
	if len(m.queued) != 0 {
		t.Errorf("queue = %v after u, want it empty", m.queued)
	}
	after := ids(m.tasks)
	if len(after) != len(before) {
		t.Fatalf("rows after u = %v, want the %d from before", after, len(before))
	}
	for i := range before {
		if after[i] != before[i] {
			t.Errorf("row %d is id %d after u, want %d — the group did not come back as it was", i, after[i], before[i])
		}
	}
}

// TestGroupDeleteCommitsOnExit — the queue's other end: the rows really do go
// once the popup closes.
func TestGroupDeleteCommitsOnExit(t *testing.T) {
	db := openDB(t)
	add(t, db, "fix flaky test", deadSession)
	add(t, db, "restart the worker", deadSession)
	add(t, db, "call the dentist", globalScope)
	m := openAll(t, allTasksConfig(db))

	m.cursor = rowOf(t, m, "fix flaky test")
	m = pressAndSettle(t, m, "D")
	if _, err := run(m,
		tea.WithInput(strings.NewReader("q")),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
	); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := listAll(t, db); len(got) != 1 || got[0].Text != "call the dentist" {
		t.Errorf("store holds %v, want only the group that was not deleted", texts(got))
	}
}

// TestAFullyQueuedGroupDisappearsHeaderAndAll is DoD 16 — and it is the second
// of the two steps that is easy to skip: filtering the queued ids out of each
// group is obvious, dropping the groups that came out empty is not.
func TestAFullyQueuedGroupDisappearsHeaderAndAll(t *testing.T) {
	db := openDB(t)
	add(t, db, "fix flaky test", deadSession)
	add(t, db, "restart the worker", deadSession)
	add(t, db, "call the dentist", globalScope)
	m := openAll(t, allTasksConfig(db))

	if got := headers(m); len(got) != 2 {
		t.Fatalf("headers = %v, want both groups", got)
	}
	m.cursor = rowOf(t, m, "fix flaky test")
	m = pressAndSettle(t, m, "D")

	got := headers(m)
	if len(got) != 1 || got[0] != globalScope {
		t.Errorf("headers after deleting a whole group = %v, want the global group alone", got)
	}
	// The frame must not mention it either — an empty header left behind is a
	// group the user deleted still announcing itself.
	if frame := m.View(); strings.Contains(frame, "api") {
		t.Errorf("the deleted group's header is still on screen:\n%s", frame)
	}

	// Deleting row by row must reach the same place, or `d` and `D` disagree.
	m = pressAndSettle(t, m, "u")
	m.cursor = rowOf(t, m, "fix flaky test")
	m = pressAndSettle(t, m, "d")
	m.cursor = rowOf(t, m, "restart the worker")
	m = pressAndSettle(t, m, "d")
	if got := headers(m); len(got) != 1 || got[0] != globalScope {
		t.Errorf("headers after emptying a group with d = %v, want the global group alone", got)
	}
}

// TestGroupDeleteInTheMergedViewIsInert — `D` needs a group, and the merged view
// has none. A `D` that fell through to the whole list would be catastrophic.
func TestGroupDeleteInTheMergedViewIsInert(t *testing.T) {
	db := openDB(t)
	seedStranded(t, db)
	m := newLoaded(t, allTasksConfig(db))

	next, cmd := press(t, m, "D")
	if cmd != nil || len(next.queued) != 0 {
		t.Errorf("D in the merged view queued %v", next.queued)
	}
}

// TestAddFilesIntoTheGroupUnderTheCursor is DoD 18, including the part that
// looks like a bug and is not: the target may be a session that is not running.
func TestAddFilesIntoTheGroupUnderTheCursor(t *testing.T) {
	db := openDB(t)
	add(t, db, "fix flaky test", deadSession)
	add(t, db, "call the dentist", globalScope)

	var wrote []task.ScopeKind
	cfg := allTasksConfig(db)
	cfg.SetSticky = func(k task.ScopeKind) error { wrote = append(wrote, k); return nil }
	cfg.DefaultScope = task.ScopeGlobal

	m := openAll(t, cfg)
	m.cursor = rowOf(t, m, "fix flaky test")
	m = pressAndSettle(t, m, "a")
	if m.mode != modeInput {
		t.Fatal("a did not open the input row")
	}
	// The row sits under that group's header, not at the top of the list, or it
	// would look like it belongs to the first group.
	if got, want := m.inputRowIndex(), rowOf(t, m, "fix flaky test"); got != want {
		t.Errorf("the input row is at %d, want %d — directly under its header", got, want)
	}
	// Tab is inert: the group already answered the scope question.
	before := m.inputScope
	m = pressAndSettle(t, m, "tab")
	if m.inputScope != before {
		t.Errorf("tab moved the scope to %v; the group is the choice", m.inputScope)
	}

	m = typeText(t, m, "and this one too")
	m = pressAndSettle(t, m, "enter")

	got := taskByText(t, listAll(t, db), "and this one too")
	if got.Scope != deadSession {
		t.Errorf("the new task is filed under %v, want the group's %v — including its not-running key", got.Scope, deadSession)
	}
	// A placement, not a preference.
	if len(wrote) != 0 {
		t.Errorf("adding into a group wrote the sticky default %v", wrote)
	}
}

// TestAddInTheMergedViewStillCyclesAndRemembers — the guard that the change
// above did not leak into the merged view's add.
func TestAddInTheMergedViewStillCyclesAndRemembers(t *testing.T) {
	db := openDB(t)
	add(t, db, "call the dentist", globalScope)
	var wrote []task.ScopeKind
	cfg := allTasksConfig(db)
	cfg.SetSticky = func(k task.ScopeKind) error { wrote = append(wrote, k); return nil }
	cfg.DefaultScope = task.ScopeSession

	m := newLoaded(t, cfg)
	m = pressAndSettle(t, m, "a")
	if m.inputScope != task.ScopeSession {
		t.Fatalf("add seeded %v, want the sticky default", m.inputScope)
	}
	m = pressAndSettle(t, m, "tab")
	if m.inputScope != task.ScopeDir {
		t.Errorf("tab moved to %v, want dir", m.inputScope)
	}
	m = typeText(t, m, "typed in the merged view")
	m = pressAndSettle(t, m, "enter")

	if got := taskByText(t, listAll(t, db), "typed in the merged view"); got.Scope != dirScope {
		t.Errorf("filed under %v, want %v", got.Scope, dirScope)
	}
	if len(wrote) != 1 || wrote[0] != task.ScopeDir {
		t.Errorf("sticky writes = %v, want the one dir write", wrote)
	}
}

// TestSpaceAndEditActOnTheCursorRowInTheAllTasksView — the row model's real
// hazard: m.cursor indexes rows while m.tasks is flat, so an action that indexed
// m.tasks by m.cursor would silently hit the wrong task, off by the number of
// headers above it.
func TestMutationKeysActOnTheRightRowInTheAllTasksView(t *testing.T) {
	db := openDB(t)
	seedStranded(t, db)
	m := openAll(t, allTasksConfig(db))

	// A row deep in the list, with four headers above it.
	target := "call the dentist"
	m.cursor = rowOf(t, m, target)
	if m.cursor < 4 {
		t.Fatalf("cursor %d is not far enough into the list to catch the off-by-headers bug", m.cursor)
	}

	pressAndSettle(t, m, " ")
	if got := taskByText(t, listAll(t, db), target); !got.Done {
		t.Errorf("space completed something else; %q is still pending", target)
	}

	edited := pressAndSettle(t, m, "e")
	if got, ok := edited.selectedTask(); !ok || got.Text != target {
		t.Errorf("e opened on %q, want %q", got.Text, target)
	}

	deleted := pressAndSettle(t, m, "d")
	if len(deleted.queued) != 1 {
		t.Fatalf("d queued %v", deleted.queued)
	}
	if got := taskByText(t, listAll(t, db), target); len(deleted.queued[0]) != 1 || deleted.queued[0][0] != got.ID {
		t.Errorf("d queued id %d, want %q's id %d", deleted.queued[0], target, got.ID)
	}
}

// TestHelpDescribesTheViewOnScreen is DoD 19.
func TestHelpDescribesTheViewOnScreen(t *testing.T) {
	db := openDB(t)
	seedStranded(t, db)
	m := newLoaded(t, allTasksConfig(db))

	merged := strings.Join(pressAndSettle(t, m, "?").helpBody(), "\n")
	if !strings.Contains(merged, "tab") {
		t.Errorf("the merged view's overlay does not mention tab:\n%s", merged)
	}
	for _, absent := range []string{"jump", "re-home"} {
		if strings.Contains(merged, absent) {
			t.Errorf("the merged view's overlay mentions %q, which does nothing there:\n%s", absent, merged)
		}
	}

	all := strings.Join(pressAndSettle(t, openAll(t, allTasksConfig(db)), "?").helpBody(), "\n")
	for _, want := range []string{"jump", "re-home", "group"} {
		if !strings.Contains(all, want) {
			t.Errorf("the all-tasks overlay is missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "tab") {
		t.Errorf("the all-tasks overlay mentions tab, which is inert there:\n%s", all)
	}
}

// TestAllTasksRowsAndHeadersNeverExceedTheirWidth extends
// TestRowsNeverExceedTheirWidth to this view, headers included — and
// specifically to a header whose session name is long enough to collide with the
// right-aligned jump hint. A row wider than the viewport is silently *clipped*,
// not wrapped, which is how the tier labels vanished for real dir keys.
func TestAllTasksRowsAndHeadersNeverExceedTheirWidth(t *testing.T) {
	db := openDB(t)
	longName := strings.Repeat("deploy-pipeline-", 6) + "staging"
	add(t, db, "fix flaky test", task.Scope{Kind: task.ScopeSession, Key: longName})
	add(t, db, strings.Repeat("a very long task title ", 8), task.Scope{Kind: task.ScopeSession, Key: longName})
	add(t, db, "rebase onto main", sessionScope)
	add(t, db, "update README", task.Scope{Kind: task.ScopeDir, Key: "/Users/x/" + strings.Repeat("nested/", 12) + "repo"})
	add(t, db, "call the dentist", globalScope)

	for _, width := range []int{12, 20, 30, 42, 52, 58, 80, 120} {
		cfg := allTasksConfig(db)
		cfg.LiveSessions = map[string]bool{longName: true}
		m := openAll(t, cfg)
		sized, _ := m.Update(tea.WindowSizeMsg{Width: width + chromeWidth, Height: 40})
		m = sized.(Model)

		for i, line := range m.renderRowLines() {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("width %d: row %d is %d columns: %q", width, i, w, line)
			}
			if strings.Contains(line, "\n") {
				t.Errorf("width %d: row %d spans rows: %q", width, i, line)
			}
		}
	}
}

// TestALongSessionNameDoesNotEatTheJumpHint — the hint has to win its columns,
// the way columns() guarantees tier labels theirs. Without a budget the header
// overflows and the *hint* is what the viewport clips away, leaving a group
// whose whole purpose is unlabelled.
func TestALongSessionNameDoesNotEatTheJumpHint(t *testing.T) {
	longName := strings.Repeat("deploy-pipeline-", 6) + "staging"
	r := listRow{kind: rowHeader, group: groupOf(task.Scope{Kind: task.ScopeSession, Key: longName})}

	for _, width := range []int{42, 52, 58, 80} {
		got := renderHeader(r, "", width)
		if w := lipgloss.Width(got); w > width {
			t.Errorf("width %d: header is %d columns: %q", width, w, got)
		}
		if !strings.Contains(got, hintSesh) {
			t.Errorf("width %d: a long name pushed the jump hint out: %q", width, got)
		}
		// The name's *tail* is what identifies it, so it truncates from the left.
		if !strings.Contains(got, "staging") {
			t.Errorf("width %d: the name lost its tail: %q", width, got)
		}
	}
}

// taskByText finds a task by its text, failing if it is absent.
func taskByText(t *testing.T, tasks []task.Task, text string) task.Task {
	t.Helper()
	for _, task := range tasks {
		if task.Text == text {
			return task
		}
	}
	t.Fatalf("no task %q in %v", text, texts(tasks))
	return task.Task{}
}

// TestEveryMergedViewKeyStillWorksInTheAllTasksView is DoD 17. The two views
// differ only in layout, which is one less rule to learn — so the failure this
// guards against is a key that silently stops working because its handler
// indexed m.tasks, or because a guard turned it off for the wrong view.
func TestEveryMergedViewKeyStillWorksInTheAllTasksView(t *testing.T) {
	seed := func(t *testing.T) (Model, *store.DB) {
		t.Helper()
		db := openDB(t)
		seedStranded(t, db)
		m := openAll(t, allTasksConfig(db))
		m.cursor = rowOf(t, m, "update README") // a dir row, mid-list
		return m, db
	}

	t.Run("space completes", func(t *testing.T) {
		m, db := seed(t)
		pressAndSettle(t, m, " ")
		if !taskByText(t, listAll(t, db), "update README").Done {
			t.Error("space did nothing")
		}
	})
	t.Run("s re-scopes one row", func(t *testing.T) {
		m, db := seed(t)
		pressAndSettle(t, m, "s")
		got := taskByText(t, listAll(t, db), "update README")
		if got.Scope.Kind != task.ScopeGlobal {
			t.Errorf("s left it in %v, want the next tier", got.Scope)
		}
		// One row, not the group: that is what distinguishes s from r.
		if other := taskByText(t, listAll(t, db), "someone else's repo"); other.Scope != otherDir {
			t.Errorf("s moved the rest of the tier too: %v", other.Scope)
		}
	})
	t.Run("e edits", func(t *testing.T) {
		m, db := seed(t)
		m = pressAndSettle(t, m, "e")
		m = typeText(t, m, " (edited)")
		m = pressAndSettle(t, m, "enter")
		if _, err := findText(listAll(t, db), "update README (edited)"); err != nil {
			t.Errorf("e did not save: %v", texts(listAll(t, db)))
		}
	})
	t.Run("d then u", func(t *testing.T) {
		m, _ := seed(t)
		m = pressAndSettle(t, m, "d")
		if len(m.queued) != 1 {
			t.Fatalf("d queued %v", m.queued)
		}
		m = pressAndSettle(t, m, "u")
		if len(m.queued) != 0 {
			t.Errorf("u left %v queued", m.queued)
		}
	})
	t.Run("? opens the overlay", func(t *testing.T) {
		m, _ := seed(t)
		if m = pressAndSettle(t, m, "?"); m.mode != modeHelp {
			t.Error("? did not open the overlay")
		}
	})
	t.Run("q quits", func(t *testing.T) {
		m, _ := seed(t)
		m, cmd := press(t, m, "q")
		if !m.quitting || cmd == nil {
			t.Error("q did not quit")
		}
	})
	t.Run("a adds", func(t *testing.T) {
		m, db := seed(t)
		m = pressAndSettle(t, m, "a")
		m = typeText(t, m, "typed into a group")
		m = pressAndSettle(t, m, "enter")
		if _, err := findText(listAll(t, db), "typed into a group"); err != nil {
			t.Errorf("a did not save: %v", texts(listAll(t, db)))
		}
	})
	t.Run("the view survives every one of them", func(t *testing.T) {
		m, _ := seed(t)
		for _, key := range []string{"1", "1", "2", "2", "3", "3", "j", "k", " ", "s", "d", "u", "?", "?"} {
			m = pressAndSettle(t, m, key)
			if m.view != viewAll {
				t.Fatalf("%q dropped out of the all-tasks view", key)
			}
			if m.cursor < 0 || m.cursor >= len(m.rows) && len(m.rows) > 0 {
				t.Fatalf("%q left the cursor at %d for %d rows", key, m.cursor, len(m.rows))
			}
		}
	})
}

// findText reports whether a task with this text exists, without failing the
// test — for assertions that want to phrase their own message.
func findText(tasks []task.Task, text string) (task.Task, error) {
	for _, t := range tasks {
		if t.Text == text {
			return t, nil
		}
	}
	return task.Task{}, errNotFound
}

var errNotFound = errors.New("no such task")
