package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agusarias/tmux-todo/internal/task"
)

// ids lists the ids on screen, so a test can talk about identity rather than
// text — which is the whole point of the undo mechanism.
func ids(tasks []task.Task) []int64 {
	out := make([]int64, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, t.ID)
	}
	return out
}

func indexOf(tasks []task.Task, id int64) int {
	for i, t := range tasks {
		if t.ID == id {
			return i
		}
	}
	return -1
}

// quitAndCommit runs the real exit path: the key, the command it returns, and
// the message that command produces. It returns the model after the commit has
// been applied, plus the command the commit message produced (tea.Quit).
func quitAndCommit(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	m, cmd := press(t, m, key)
	if cmd == nil {
		t.Fatalf("%q produced no command on exit", key)
	}
	msg := cmd()
	if _, ok := msg.(tea.QuitMsg); ok {
		// The empty-queue path: exits directly, exactly as it did before the
		// delete queue existed.
		return m, cmd
	}
	if _, ok := msg.(deletesCommittedMsg); !ok {
		t.Fatalf("%q produced %T, want the delete commit", key, msg)
	}
	next, quit := m.Update(msg)
	return next.(Model), quit
}

// TestDeleteQueuesWithoutWriting is DoD 11: `d` takes the row off screen and
// touches nothing in the database.
func TestDeleteQueuesWithoutWriting(t *testing.T) {
	db := openDB(t)
	keep := add(t, db, "keep me", globalScope)
	doomed := add(t, db, "delete me", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m.cursor = indexOf(m.tasks, doomed.ID)
	m = pressAndSettle(t, m, "d")

	if indexOf(m.tasks, doomed.ID) >= 0 {
		t.Errorf("the row is still on screen: %v", texts(m.tasks))
	}
	if indexOf(m.tasks, keep.ID) < 0 {
		t.Errorf("d took the wrong row: %v", texts(m.tasks))
	}
	if got := m.queued; len(got) != 1 || got[0] != doomed.ID {
		t.Errorf("queue = %v, want just the deleted id", got)
	}

	// The row is untouched in the store — that is what makes undo exact.
	stored := mustGet(t, db, doomed.ID)
	if stored.Text != doomed.Text || stored.Scope != doomed.Scope || stored.Done != doomed.Done {
		t.Errorf("the queued row changed in the store: %+v", stored)
	}
	if !stored.CreatedAt.Equal(doomed.CreatedAt) {
		t.Errorf("created_at moved: %v -> %v", doomed.CreatedAt, stored.CreatedAt)
	}
	if len(listAll(t, db)) != 2 {
		t.Errorf("a DELETE reached the database before the popup closed")
	}
}

// TestUndoRestoresTheSameRowInTheSamePlace is DoD 12, and the id assertion is
// the point: re-inserting could restore the text but never the identity, and a
// shell holding the old id would be wrong.
func TestUndoRestoresTheSameRowInTheSamePlace(t *testing.T) {
	db := openDB(t)
	for _, text := range []string{"first", "second", "third", "fourth"} {
		add(t, db, text, globalScope)
	}
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})
	before := ids(m.tasks)

	// Delete from the middle, where a re-insert would visibly land elsewhere.
	m.cursor = 2
	victim := m.tasks[2]
	m = pressAndSettle(t, m, "d")
	if indexOf(m.tasks, victim.ID) >= 0 {
		t.Fatal("the row survived d")
	}

	m = pressAndSettle(t, m, "u")

	if got := ids(m.tasks); len(got) != len(before) {
		t.Fatalf("ids after undo = %v, want %v", got, before)
	}
	for i := range before {
		if m.tasks[i].ID != before[i] {
			t.Errorf("row %d is id %d, want %d — the list is not as it was", i, m.tasks[i].ID, before[i])
		}
	}
	restored := m.tasks[2]
	if restored.ID != victim.ID {
		t.Errorf("restored id = %d, want the original %d", restored.ID, victim.ID)
	}
	if !restored.CreatedAt.Equal(victim.CreatedAt) {
		t.Errorf("restored created_at = %v, want the original %v", restored.CreatedAt, victim.CreatedAt)
	}
	if m.cursor != 2 {
		t.Errorf("cursor = %d, want it back on the restored row at 2", m.cursor)
	}
	if len(m.queued) != 0 {
		t.Errorf("queue = %v, want it empty", m.queued)
	}
}

// Repeated d unwinds in LIFO order, and u on an empty queue is a no-op rather
// than an error.
func TestUndoIsLastInFirstOut(t *testing.T) {
	db := openDB(t)
	for _, text := range []string{"one", "two", "three"} {
		add(t, db, text, globalScope)
	}
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m.cursor = 0
	first := m.tasks[0].ID
	m = pressAndSettle(t, m, "d")
	second := m.tasks[0].ID
	m = pressAndSettle(t, m, "d")

	if got := m.queued; len(got) != 2 || got[0] != first || got[1] != second {
		t.Fatalf("queue = %v, want [%d %d]", got, first, second)
	}
	m = pressAndSettle(t, m, "u")
	if indexOf(m.tasks, second) < 0 || indexOf(m.tasks, first) >= 0 {
		t.Errorf("the first u restored the wrong row: %v", ids(m.tasks))
	}
	m = pressAndSettle(t, m, "u")
	if indexOf(m.tasks, first) < 0 {
		t.Errorf("the second u did not restore the earlier delete: %v", ids(m.tasks))
	}

	next, cmd := press(t, m, "u")
	if cmd != nil {
		t.Errorf("u on an empty queue produced a command")
	}
	if len(next.tasks) != 3 {
		t.Errorf("u on an empty queue changed the rows: %v", texts(next.tasks))
	}
}

// TestQueuedRowStaysGoneAcrossReloads is DoD 13. Hiding the row once is not
// enough: any reload — another pane inserting, a filter change, a toggle —
// re-queries the store, which still has the row.
func TestQueuedRowStaysGoneAcrossReloads(t *testing.T) {
	db := openDB(t)
	doomed := add(t, db, "delete me", globalScope)
	add(t, db, "keep me", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m.cursor = indexOf(m.tasks, doomed.ID)
	m = pressAndSettle(t, m, "d")

	// Another pane inserts, which is what triggers a re-query in real use.
	add(t, db, "from another pane", globalScope)

	for _, reload := range []struct {
		name string
		next func(Model) Model
	}{
		{"plain reload", func(m Model) Model {
			next, _ := m.Update(m.reloadCmd()())
			return next.(Model)
		}},
		{"filter on", func(m Model) Model { return pressAndSettle(t, m, "3") }},
		{"filter off", func(m Model) Model { return pressAndSettle(t, pressAndSettle(t, m, "3"), "3") }},
		{"toggle done", func(m Model) Model { return pressAndSettle(t, m, " ") }},
	} {
		got := reload.next(m)
		if indexOf(got.tasks, doomed.ID) >= 0 {
			t.Errorf("%s brought the queued row back: %v", reload.name, texts(got.tasks))
		}
		// ...and the cursor cannot be walked onto it either.
		for i := 0; i < len(got.tasks)+2; i++ {
			got = pressAndSettle(t, got, "j")
			if got.cursor < len(got.tasks) && got.tasks[got.cursor].ID == doomed.ID {
				t.Fatalf("%s: the cursor reached a queued row", reload.name)
			}
		}
	}
}

// TestCommitOnExitDeletesForReal is DoD 14, and the mutation proof in the brief
// is against this test: delete the commitDeletesCmd call in quit() and it fails.
func TestCommitOnExitDeletesForReal(t *testing.T) {
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		t.Run(key, func(t *testing.T) {
			db := openDB(t)
			doomed := add(t, db, "delete me", globalScope)
			keep := add(t, db, "keep me", globalScope)
			m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

			m.cursor = indexOf(m.tasks, doomed.ID)
			m = pressAndSettle(t, m, "d")

			m, quit := quitAndCommit(t, m, key)

			if m.commitErr != nil {
				t.Errorf("commit reported %v", m.commitErr)
			}
			if quit == nil {
				t.Fatal("the commit did not lead to a quit")
			}
			if _, ok := quit().(tea.QuitMsg); !ok {
				t.Error("the popup did not quit after committing")
			}
			remaining := listAll(t, db)
			if len(remaining) != 1 || remaining[0].ID != keep.ID {
				t.Errorf("store holds %v, want only the kept task", texts(remaining))
			}
		})
	}
}

// An undone delete is not committed: `u` is the cancel, not a second queue.
func TestUndoneDeletesAreNotCommitted(t *testing.T) {
	db := openDB(t)
	add(t, db, "one", globalScope)
	add(t, db, "two", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m = pressAndSettle(t, m, "d")
	m = pressAndSettle(t, m, "d")
	m = pressAndSettle(t, m, "u")

	m, _ = quitAndCommit(t, m, "q")
	if m.commitErr != nil {
		t.Fatalf("commit failed: %v", m.commitErr)
	}
	if got := listAll(t, db); len(got) != 1 {
		t.Errorf("store holds %v, want the undone task alone", texts(got))
	}
}

// TestEmptyQueueExitsExactlyAsBefore is the other half of DoD 14: a user who
// never pressed d must take the same exit path they always did.
func TestEmptyQueueExitsExactlyAsBefore(t *testing.T) {
	db := openDB(t)
	add(t, db, "untouched", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	next, cmd := press(t, m, "q")
	if cmd == nil {
		t.Fatal("q produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("q produced %T, want tea.Quit with nothing in between", cmd())
	}
	if !next.quitting {
		t.Error("q left the model not quitting")
	}
	if len(listAll(t, db)) != 1 {
		t.Error("the task went away without a delete")
	}
}

// TestDyingWithoutCommittingKeepsTheTasks is DoD 15. A SIGKILLed popup, a closed
// pane, a sleeping machine: the model is simply dropped, and the queued rows must
// survive. That is the direction this design fails in, on purpose.
func TestDyingWithoutCommittingKeepsTheTasks(t *testing.T) {
	db := openDB(t)
	add(t, db, "one", globalScope)
	add(t, db, "two", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m = pressAndSettle(t, m, "d")
	m = pressAndSettle(t, m, "d")
	if len(m.queued) != 2 {
		t.Fatalf("queue = %v, want two", m.queued)
	}
	m = Model{} // the popup dies; nothing runs the quit path
	_ = m

	if got := listAll(t, db); len(got) != 2 {
		t.Errorf("store holds %v, want both tasks alive", texts(got))
	}
}

// TestCommitFailureReachesRun is the rest of the critical surface: reporting
// "deleted" for a row still in the database is worse than the delete not
// happening, so the error has to come back out.
func TestCommitFailureReachesRun(t *testing.T) {
	db := openDB(t)
	add(t, db, "delete me", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})
	m = pressAndSettle(t, m, "d")

	// Closing the database makes the DELETE fail for real.
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	m, quit := quitAndCommit(t, m, "q")

	if m.commitErr == nil {
		t.Fatal("a failed commit reported success")
	}
	if !strings.Contains(m.commitErr.Error(), "commit queued deletes") {
		t.Errorf("commitErr does not name what failed: %v", m.commitErr)
	}
	if quit == nil {
		t.Error("a failed commit did not quit")
	}
}

// A row another pane deleted first is not a failure: the user asked for it gone,
// and it is gone.
func TestCommitToleratesAnAlreadyDeletedRow(t *testing.T) {
	db := openDB(t)
	doomed := add(t, db, "delete me", globalScope)
	add(t, db, "keep me", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m.cursor = indexOf(m.tasks, doomed.ID)
	m = pressAndSettle(t, m, "d")

	if err := db.Delete(t.Context(), doomed.ID); err != nil {
		t.Fatalf("racing delete: %v", err)
	}
	m, _ = quitAndCommit(t, m, "q")
	if m.commitErr != nil {
		t.Errorf("commit reported %v for a row that was already gone", m.commitErr)
	}
}

// A list emptied by d is not an empty list: the rows are still there and u
// brings them back, so "no tasks yet" would be a lie that hides the way out.
func TestEmptyStateSaysTheRowsAreQueued(t *testing.T) {
	db := openDB(t)
	add(t, db, "only task", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})

	m = pressAndSettle(t, m, "d")
	if len(m.tasks) != 0 {
		t.Fatalf("rows = %v, want none left", texts(m.tasks))
	}
	view := m.View()
	if strings.Contains(view, "no tasks yet") {
		t.Errorf("the queued row is reported as no tasks at all:\n%s", view)
	}
	if !strings.Contains(view, "u undoes") {
		t.Errorf("the empty state does not offer the way back:\n%s", view)
	}
}

// d with nothing under the cursor must not queue a phantom id.
func TestDeleteOnAnEmptyListIsInert(t *testing.T) {
	db := openDB(t)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})

	next, cmd := press(t, m, "d")
	if cmd != nil {
		t.Error("d on an empty list produced a command")
	}
	if len(next.queued) != 0 {
		t.Errorf("queue = %v, want empty", next.queued)
	}
}

// dropQueued is the single filter point; this pins it directly so a refactor
// that moves the call has to keep the behaviour.
func TestDropQueuedFiltersByID(t *testing.T) {
	m := Model{queued: []int64{2, 4}}
	in := []task.Task{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}
	if got := ids(m.dropQueued(in)); len(got) != 3 || got[0] != 1 || got[1] != 3 || got[2] != 5 {
		t.Errorf("dropQueued = %v, want [1 3 5]", got)
	}
	empty := Model{}
	if got := ids(empty.dropQueued(in)); len(got) != len(in) {
		t.Errorf("dropQueued with no queue changed the rows: %v", got)
	}
}

// TestRunReturnsTheCommitError drives the real event loop — Run's own code path,
// with a scripted keyboard and no terminal — because the contract under test is
// "the process exits non-zero when a queued delete failed", and that only holds
// if Run actually looks at the final model.
func TestRunReturnsTheCommitError(t *testing.T) {
	db := openDB(t)
	add(t, db, "delete me", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})
	m = pressAndSettle(t, m, "d")
	if len(m.queued) != 1 {
		t.Fatalf("queue = %v, want one", m.queued)
	}
	// The commit will fail for real against a closed database.
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	err := run(m,
		tea.WithInput(strings.NewReader("q")),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
	)
	if err == nil {
		t.Fatal("Run reported success for a commit that failed")
	}
	if !strings.Contains(err.Error(), "commit queued deletes") {
		t.Errorf("Run returned %v, want the commit failure", err)
	}
}

// The same path with a working store returns nil, so the error above is the
// commit talking and not the program.
func TestRunReturnsNilOnACleanExit(t *testing.T) {
	db := openDB(t)
	doomed := add(t, db, "delete me", globalScope)
	add(t, db, "keep me", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()})
	m.cursor = indexOf(m.tasks, doomed.ID)
	m = pressAndSettle(t, m, "d")

	if err := run(m,
		tea.WithInput(strings.NewReader("q")),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
	); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := listAll(t, db); len(got) != 1 || got[0].Text != "keep me" {
		t.Errorf("store holds %v, want the queued row committed and the other kept", texts(got))
	}
}
