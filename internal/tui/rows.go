package tui

import (
	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// The list-row model.
//
// The merged view's cursor used to be an index into []task.Task, which works
// only because every row there *is* a task. The all-tasks view's list is
// heterogeneous — group headers interleaved with task rows, and headers are not
// selectable — so that model does not stretch. Both views now flatten into
// []listRow and the cursor indexes that, which leaves anchorCursor, the viewport
// scroll math and the input row with one implementation instead of two. The
// merged view is the degenerate case where every row is a rowTask, in the same
// order as m.tasks, which is why none of its tests had to change.
type rowKind int

const (
	rowTask rowKind = iota
	rowHeader
)

// listRow is one line of the list: either a task or an all-tasks group header.
type listRow struct {
	kind rowKind
	// task is set for rowTask.
	task task.Task
	// group is set for rowHeader: the scope it heads, plus its tasks.
	group store.Group
	// live is set for rowHeader on a session group: whether that session is
	// running right now, per Config.LiveSessions. It is never discovered here.
	live bool
}

// selectable reports whether the cursor may land on this row. Headers may not:
// they are a label, and every action the popup has acts on a task.
func (r listRow) selectable() bool { return r.kind == rowTask }

// taskRows flattens the merged view: one selectable row per task, in the
// store's order.
func taskRows(tasks []task.Task) []listRow {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]listRow, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, listRow{kind: rowTask, task: t})
	}
	return out
}

// groupRows flattens the all-tasks view: a header per scope, then its tasks.
//
// live is asked per group rather than being carried on store.Group, because
// liveness is not a property of the stored data — it is an injected fact about
// right now, and store must stay environment-blind too.
func groupRows(groups []store.Group, live func(task.Scope) bool) []listRow {
	if len(groups) == 0 {
		return nil
	}
	out := make([]listRow, 0, len(groups)*2)
	for _, g := range groups {
		out = append(out, listRow{kind: rowHeader, group: g, live: live(g.Scope)})
		for _, t := range g.Tasks {
			out = append(out, listRow{kind: rowTask, task: t})
		}
	}
	return out
}

// rebuildRows re-flattens the row list from whichever source the current view
// reads. Called from the one place rows arrive, so "the cursor indexes rows"
// cannot drift out of sync with "rows describe the tasks".
func (m *Model) rebuildRows() {
	if m.view == viewAll {
		m.rows = groupRows(m.groups, m.isLive)
		return
	}
	m.rows = taskRows(m.tasks)
}

// selectedTask is the task under the cursor, if the cursor is on one.
//
// Every action that mutates a row goes through this rather than indexing
// m.tasks by m.cursor: in the all-tasks view those are different coordinate
// systems, and the bug that mismatch produces is "space completes the wrong
// task", which is silent.
func (m Model) selectedTask() (task.Task, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return task.Task{}, false
	}
	r := m.rows[m.cursor]
	if r.kind != rowTask {
		return task.Task{}, false
	}
	return r.task, true
}

// cursorGroup is the group the cursor is inside: the nearest header at or above
// it. It is what makes `a`, `r` and group delete act on "this group" without the
// header ever being selectable.
func (m Model) cursorGroup() (store.Group, bool) {
	if i, ok := m.cursorHeader(); ok {
		return m.rows[i].group, true
	}
	return store.Group{}, false
}

// cursorHeader is the index of the header the cursor sits under.
func (m Model) cursorHeader() (int, bool) {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return 0, false
	}
	for i := m.cursor; i >= 0; i-- {
		if m.rows[i].kind == rowHeader {
			return i, true
		}
	}
	return 0, false
}

// nextSelectable is the next index in the given direction the cursor may land
// on, or -1 when there is none. Stepping by selectable row rather than by line
// is what makes j/k skip headers without the caller knowing they exist.
func (m Model) nextSelectable(from, step int) int {
	for i := from + step; i >= 0 && i < len(m.rows); i += step {
		if m.rows[i].selectable() {
			return i
		}
	}
	return -1
}

// snapCursor moves the cursor off an unselectable row: forwards first, then
// backwards. Forwards first so that landing on a header puts the cursor on that
// group's first task, which is what a header is next to.
func (m *Model) snapCursor() {
	if m.cursor >= 0 && m.cursor < len(m.rows) && m.rows[m.cursor].selectable() {
		return
	}
	if i := m.nextSelectable(m.cursor, 1); i >= 0 {
		m.cursor = i
		return
	}
	if i := m.nextSelectable(m.cursor, -1); i >= 0 {
		m.cursor = i
		return
	}
	m.cursor = 0
}
