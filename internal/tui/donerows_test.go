package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// doneRow builds a completed task with an explicit done_at, so the ordering
// assertions have something to be wrong about.
func doneRow(id int64, text string, scope task.Scope, doneAt time.Time) task.Task {
	return task.Task{ID: id, Text: text, Done: true, DoneAt: &doneAt, Scope: scope}
}

// pendingRow builds a pending task. Its DoneAt is nil, as the store's is.
func pendingRow(id int64, text string, scope task.Scope) task.Task {
	return task.Task{ID: id, Text: text, Scope: scope}
}

// TestPartitionDoneGroupsDoneAtTheEndOfEachTier is DoD 4 and DoD 5 over the pure
// function: pending first in arrival order, then done by done_at descending, per
// tier, with the tier order itself untouched.
func TestPartitionDoneGroupsDoneAtTheEndOfEachTier(t *testing.T) {
	base := time.Unix(1_760_000_000, 0)

	// As the store hands them over: tier order, newest-first within a tier,
	// pending and done interleaved.
	in := []task.Task{
		pendingRow(10, "s-pending-new", sessionScope),
		doneRow(9, "s-done-old", sessionScope, base.Add(-5*time.Hour)),
		pendingRow(8, "s-pending-old", sessionScope),
		doneRow(7, "s-done-new", sessionScope, base.Add(-time.Hour)),
		doneRow(6, "d-done", dirScope, base.Add(-2*time.Hour)),
		pendingRow(5, "d-pending", dirScope),
		pendingRow(4, "g-pending-new", globalScope),
		pendingRow(3, "g-pending-old", globalScope),
	}

	got := texts(partitionDone(in))
	want := []string{
		// session: pending in arrival order, then done newest-completed first.
		"s-pending-new", "s-pending-old", "s-done-new", "s-done-old",
		// dir: the done row moves below the pending one it arrived above.
		"d-pending", "d-done",
		// global: no done rows, so nothing moves.
		"g-pending-new", "g-pending-old",
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("partitionDone =\n  %v\nwant\n  %v", got, want)
	}
}

// TestPartitionDoneKeepsTierOrderAndMembership — nothing may be dropped,
// duplicated, or moved across a tier boundary. A partition that got the tier runs
// wrong would show up here as a row in the wrong tier, which the ordering test
// above could miss if it happened to land in the right slot.
func TestPartitionDoneKeepsTierOrderAndMembership(t *testing.T) {
	base := time.Unix(1_760_000_000, 0)
	in := []task.Task{
		doneRow(10, "s1", sessionScope, base.Add(-time.Hour)),
		doneRow(9, "s2", sessionScope, base.Add(-2*time.Hour)),
		pendingRow(8, "d1", dirScope),
		doneRow(7, "d2", dirScope, base.Add(-3*time.Hour)),
		pendingRow(6, "g1", globalScope),
	}
	got := partitionDone(in)

	if len(got) != len(in) {
		t.Fatalf("partitionDone returned %d rows, want %d", len(got), len(in))
	}
	seen := map[int64]int{}
	for _, r := range got {
		seen[r.ID]++
	}
	for _, r := range in {
		if seen[r.ID] != 1 {
			t.Errorf("id %d appears %d times, want exactly 1", r.ID, seen[r.ID])
		}
	}
	// Tier runs must still be contiguous and in session -> dir -> global order.
	var kinds []task.ScopeKind
	for _, r := range got {
		if len(kinds) == 0 || kinds[len(kinds)-1] != r.Scope.Kind {
			kinds = append(kinds, r.Scope.Kind)
		}
	}
	want := []task.ScopeKind{task.ScopeSession, task.ScopeDir, task.ScopeGlobal}
	if len(kinds) != len(want) {
		t.Fatalf("tier runs = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Errorf("tier runs = %v, want %v", kinds, want)
			break
		}
	}
}

// TestPartitionDoneWithAnAllDoneTier — a tier with no pending rows must not merge
// into its neighbour. This is the case the plan called out: deriving tiers from
// task.ScopeKinds() and filtering, rather than from runs of the rows' own kind,
// gets this wrong in a way the interleaved test above cannot see.
func TestPartitionDoneWithAnAllDoneTier(t *testing.T) {
	base := time.Unix(1_760_000_000, 0)
	in := []task.Task{
		doneRow(10, "s-done-old", sessionScope, base.Add(-9*time.Hour)),
		doneRow(9, "s-done-new", sessionScope, base.Add(-time.Hour)),
		pendingRow(8, "d-pending", dirScope),
		doneRow(7, "d-done", dirScope, base.Add(-2*time.Hour)),
	}
	got := texts(partitionDone(in))
	want := []string{"s-done-new", "s-done-old", "d-pending", "d-done"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("partitionDone = %v, want %v — an all-done tier must stay its own tier", got, want)
	}
}

// TestPartitionDoneToleratesANilDoneAt — DoD-adjacent robustness. Nothing in the
// current code path produces a done row with a NULL done_at (store.Complete always
// stamps it, and the query's DoneSince bound would exclude it), but DoneAt is a
// pointer and a comparison that dereferenced it blindly would panic inside the
// render loop rather than anywhere a test would normally look.
func TestPartitionDoneToleratesANilDoneAt(t *testing.T) {
	base := time.Unix(1_760_000_000, 0)
	nilDone := task.Task{ID: 8, Text: "no timestamp", Done: true, Scope: globalScope}
	in := []task.Task{
		pendingRow(10, "pending", globalScope),
		nilDone,
		doneRow(9, "stamped", globalScope, base.Add(-time.Hour)),
	}

	got := texts(partitionDone(in))
	want := []string{"pending", "stamped", "no timestamp"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("partitionDone = %v, want %v — a nil done_at sorts last, not first", got, want)
	}

	// Two of them must not swap with each other either, or the sort is not
	// stable and the order becomes arbitrary between runs.
	second := task.Task{ID: 7, Text: "also none", Done: true, Scope: globalScope}
	twice := texts(partitionDone([]task.Task{pendingRow(10, "pending", globalScope), nilDone, second}))
	if twice[1] != "no timestamp" || twice[2] != "also none" {
		t.Errorf("partitionDone = %v, want the two nil rows in arrival order", twice)
	}
}

// TestPartitionDoneOnEmptyAndSingletons — the boundary cases, because the tier
// walk is index arithmetic.
func TestPartitionDoneOnEmptyAndSingletons(t *testing.T) {
	if got := partitionDone(nil); len(got) != 0 {
		t.Errorf("partitionDone(nil) = %v, want empty", got)
	}
	if got := partitionDone([]task.Task{}); len(got) != 0 {
		t.Errorf("partitionDone(empty) = %v, want empty", got)
	}
	one := []task.Task{pendingRow(1, "only", globalScope)}
	if got := texts(partitionDone(one)); len(got) != 1 || got[0] != "only" {
		t.Errorf("partitionDone(one pending) = %v", got)
	}
	oneDone := []task.Task{doneRow(1, "only", globalScope, time.Unix(1, 0))}
	if got := texts(partitionDone(oneDone)); len(got) != 1 || got[0] != "only" {
		t.Errorf("partitionDone(one done) = %v", got)
	}
}

// TestMergedViewPutsDoneAtTheEndOfItsTier is DoD 4 through the real model, not
// the pure function — the wiring is the half a unit test over partitionDone
// cannot prove.
func TestMergedViewPutsDoneAtTheEndOfItsTier(t *testing.T) {
	db := openDB(t)
	sessionPending := add(t, db, "session pending", sessionScope)
	sessionDone := add(t, db, "session done", sessionScope)
	globalPending := add(t, db, "global pending", globalScope)
	globalDone := add(t, db, "global done", globalScope)

	base := time.Unix(1_760_000_000, 0)
	for _, id := range []int64{sessionDone.ID, globalDone.ID} {
		stampDone(t, db, id, base.Add(-time.Hour))
	}

	m := newLoaded(t, Config{
		DB:     db,
		Scopes: []task.Scope{sessionScope, dirScope, globalScope},
		Home:   "/Users/x",
		Now:    frozen(base),
	})

	got := texts(m.tasks)
	want := []string{
		sessionPending.Text, sessionDone.Text,
		globalPending.Text, globalDone.Text,
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("merged rows = %v, want %v — done rows belong at the end of their own tier", got, want)
	}
	// The done rows must not have been swept into one block at the very bottom:
	// the session done row sits above the global pending one.
	if got[1] != sessionDone.Text {
		t.Errorf("rows = %v, want the session tier's done row inside the session tier", got)
	}
}

// stampDone completes a row and sets its done_at to an exact time.
//
// store.Complete stamps db.now(), whose clock is private to internal/store, so
// two completions in one test land in the same second — and then "done_at DESC"
// is a tie that the stable sort resolves by the store's id order. An assertion
// built on that would pass with the done_at comparison deleted. Writing the
// column directly is what gives the ordering something to be wrong about.
func stampDone(t *testing.T, db *store.DB, id int64, at time.Time) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`UPDATE tasks SET done = 1, done_at = ? WHERE id = ?`, at.Unix(), id); err != nil {
		t.Fatalf("stamp done_at on %d: %v", id, err)
	}
}

// TestAllTasksViewPutsDoneAtTheEndOfEachGroup is DoD 6. Each group is one scope,
// so "end of the tier" means "end of the group" — and the two views must not
// disagree about that.
//
// The completion order deliberately CONTRADICTS the id order: the row added
// earlier (lower id) is completed later. So done_at DESC and id DESC disagree,
// and an implementation that fell back to the store's order would fail here.
func TestAllTasksViewPutsDoneAtTheEndOfEachGroup(t *testing.T) {
	db := openDB(t)
	base := time.Unix(1_760_000_000, 0)
	pending := add(t, db, "still to do", deadSession)
	lowID := add(t, db, "added first, finished last", deadSession)
	highID := add(t, db, "added last, finished first", deadSession)
	globalPending := add(t, db, "global pending", globalScope)

	stampDone(t, db, highID.ID, base.Add(-3*time.Hour))
	stampDone(t, db, lowID.ID, base.Add(-time.Hour))
	older, newer := highID, lowID

	cfg := allTasksConfig(db)
	cfg.Now = frozen(base)
	m := openAll(t, cfg)

	// The group under test, found by scope rather than by index.
	var group store.Group
	for _, g := range m.groups {
		if g.Scope == deadSession {
			group = g
		}
	}
	if len(group.Tasks) != 3 {
		t.Fatalf("the %v group holds %d rows, want 3", deadSession, len(group.Tasks))
	}
	got := texts(group.Tasks)
	want := []string{pending.Text, newer.Text, older.Text}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("group rows = %v, want %v (pending, then done newest-completed first)", got, want)
	}
	// And the other group is unaffected.
	for _, g := range m.groups {
		if g.Scope == globalScope && texts(g.Tasks)[0] != globalPending.Text {
			t.Errorf("the global group = %v, want %q", texts(g.Tasks), globalPending.Text)
		}
	}
}

// TestNoSeparatorRowBetweenPendingAndDone is DoD 8. Rows are the scarcest thing
// in the frame — 13 at the design's floor — so the row count must be exactly the
// task count.
func TestNoSeparatorRowBetweenPendingAndDone(t *testing.T) {
	db := openDB(t)
	for _, text := range []string{"one", "two", "three", "four"} {
		add(t, db, text, globalScope)
	}
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}, Home: "/Users/x"})
	m = pressAndSettle(t, m, "j")
	m = pressSpace(t, m)

	if len(m.rows) != len(m.tasks) {
		t.Errorf("%d rows for %d tasks — a separator row crept in", len(m.rows), len(m.tasks))
	}
	for i, r := range m.rows {
		if r.kind != rowTask {
			t.Errorf("row %d is not a task row (kind %v); the merged view has no headers or separators", i, r.kind)
		}
	}
}
