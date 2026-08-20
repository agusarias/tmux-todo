package tui

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// The delete queue.
//
// `d` does not delete. It pushes the row's id onto queued, which both hides the
// row and records the undo; the DELETE happens once, for the whole queue, as the
// popup closes. That is what makes `u` exact: the row is still in the database,
// so undo is "stop hiding it" and the id, the created_at, the done state and the
// position in the list all come back untouched. Restoring by re-inserting could
// not do that — store.Add assigns a new id and a new timestamp, so an undone task
// would come back as a different task, at the top of its tier, and any shell
// holding the old id would be wrong.
//
// The cost is stated rather than hidden: a queued row is still visible to a
// concurrent `tdo list` until the popup closes, and a popup that dies without a
// clean exit commits nothing. Both fail towards keeping the user's data.

// deletesCommittedMsg reports the outcome of the commit that runs on the way out.
type deletesCommittedMsg struct{ err error }

// queueDelete removes the cursor row from the view and remembers it. No DELETE
// is issued here.
func (m Model) queueDelete() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.tasks) {
		return m, nil
	}
	m.queued = append(m.queued, m.tasks[m.cursor].ID)
	// Reload rather than splice the row out locally: the store stays the single
	// source of truth for what is on screen, and dropQueued is applied there.
	return m, m.reloadCmd()
}

// undoDelete pops the most recent queued delete and puts the cursor back on the
// row that returns. LIFO, so repeated `u` unwinds repeated `d` in order.
func (m Model) undoDelete() (tea.Model, tea.Cmd) {
	if len(m.queued) == 0 {
		return m, nil
	}
	last := len(m.queued) - 1
	restored := m.queued[last]
	m.queued = m.queued[:last]
	return m, m.reloadAnchoredTo(restored)
}

// dropQueued removes queued rows from a freshly loaded set.
//
// Called from the rowsMsg handler, which every reload passes through, so a
// queued task cannot be reached by the cursor or acted on by space/e/s, and does
// not come back when another pane's insert triggers a re-query.
func (m Model) dropQueued(tasks []task.Task) []task.Task {
	if len(m.queued) == 0 || len(tasks) == 0 {
		return tasks
	}
	queued := make(map[int64]struct{}, len(m.queued))
	for _, id := range m.queued {
		queued[id] = struct{}{}
	}
	out := make([]task.Task, 0, len(tasks))
	for _, t := range tasks {
		if _, hidden := queued[t.ID]; !hidden {
			out = append(out, t)
		}
	}
	return out
}

// commitDeletesCmd issues the queued DELETEs. It runs as a command, so the I/O
// stays off Update, and its message is what triggers the quit.
//
// A row another pane already deleted counts as success: the outcome the user
// asked for is the outcome they have. Anything else is remembered but does not
// stop the rest of the queue — a failure on one id is no reason to leave the
// others alive.
func (m Model) commitDeletesCmd() tea.Cmd {
	db := m.cfg.DB
	ids := append([]int64(nil), m.queued...)

	return func() tea.Msg {
		if db == nil || len(ids) == 0 {
			return deletesCommittedMsg{}
		}
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()

		var failed error
		for _, id := range ids {
			switch err := db.Delete(ctx, id); {
			case err == nil, errors.Is(err, store.ErrNotFound):
			case failed == nil:
				failed = fmt.Errorf("commit queued deletes: %w", err)
			}
		}
		return deletesCommittedMsg{err: failed}
	}
}
