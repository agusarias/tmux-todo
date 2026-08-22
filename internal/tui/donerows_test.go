package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agusarias/tmux-todo/internal/config"
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

	got := texts(partitionDone(in, alwaysBelow))
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
	got := partitionDone(in, alwaysBelow)

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
	got := texts(partitionDone(in, alwaysBelow))
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

	got := texts(partitionDone(in, alwaysBelow))
	want := []string{"pending", "stamped", "no timestamp"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("partitionDone = %v, want %v — a nil done_at sorts last, not first", got, want)
	}

	// Two of them must not swap with each other either, or the sort is not
	// stable and the order becomes arbitrary between runs.
	second := task.Task{ID: 7, Text: "also none", Done: true, Scope: globalScope}
	twice := texts(partitionDone([]task.Task{pendingRow(10, "pending", globalScope), nilDone, second}, alwaysBelow))
	if twice[1] != "no timestamp" || twice[2] != "also none" {
		t.Errorf("partitionDone = %v, want the two nil rows in arrival order", twice)
	}
}

// TestPartitionDoneOnEmptyAndSingletons — the boundary cases, because the tier
// walk is index arithmetic.
func TestPartitionDoneOnEmptyAndSingletons(t *testing.T) {
	if got := partitionDone(nil, alwaysBelow); len(got) != 0 {
		t.Errorf("partitionDone(nil) = %v, want empty", got)
	}
	if got := partitionDone([]task.Task{}, alwaysBelow); len(got) != 0 {
		t.Errorf("partitionDone(empty) = %v, want empty", got)
	}
	one := []task.Task{pendingRow(1, "only", globalScope)}
	if got := texts(partitionDone(one, alwaysBelow)); len(got) != 1 || got[0] != "only" {
		t.Errorf("partitionDone(one pending) = %v", got)
	}
	oneDone := []task.Task{doneRow(1, "only", globalScope, time.Unix(1, 0))}
	if got := texts(partitionDone(oneDone, alwaysBelow)); len(got) != 1 || got[0] != "only" {
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

	// The shipped defaults, not `always`: every done row here was completed an
	// hour before the frozen `now` the popup opens at, which is exactly the set
	// config.OnStart groups below. Testing the default configuration is worth
	// more here than forcing the setting that makes the assertion easiest.
	m := newLoaded(t, Config{
		DB:     db,
		Scopes: []task.Scope{sessionScope, dirScope, globalScope},
		Home:   "/Users/x",
		Now:    frozen(base),
		Prefs:  config.Defaults(),
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
	// As above: both rows were completed before this popup opened, so the
	// default placement is what groups them.
	cfg.Prefs = config.Defaults()
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

// alwaysBelow is config.Always as a belowRule: every done row sinks. It is what
// every case above was written against, back when it was the only behaviour.
var alwaysBelow = func(t task.Task) bool { return t.Done }

// TestPartitionDoneNilRuleIsTheStoreOrder — config.Never, and also the reading of
// "no rule was wired". The one arrangement this function cannot get wrong.
func TestPartitionDoneNilRuleIsTheStoreOrder(t *testing.T) {
	base := time.Unix(1_760_000_000, 0)
	in := []task.Task{
		pendingRow(10, "pending-new", sessionScope),
		doneRow(9, "done-old", sessionScope, base.Add(-5*time.Hour)),
		pendingRow(8, "pending-old", sessionScope),
		doneRow(7, "done-new", sessionScope, base.Add(-time.Hour)),
	}
	got := texts(partitionDone(in, nil))
	want := []string{"pending-new", "done-old", "pending-old", "done-new"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("partitionDone(nil rule) =\n  %v\nwant the store's order\n  %v", got, want)
	}
}

// TestPartitionDoneOnStartMixedTier is the discriminating guard for config
// .OnStart: the one test here that fails when the partition keys off Done rather
// than off the caller's rule.
//
// The tier holds a pending row, a row done before the popup opened (which sinks)
// and a row completed during this session (which must not). Partitioning by Done
// drags the last one to the bottom with the first, which this catches. Verified
// by mutation, 2026-08-22.
func TestPartitionDoneOnStartMixedTier(t *testing.T) {
	openedAt := time.Unix(1_760_000_000, 0)
	below := onStartBelow(openedAt)

	in := []task.Task{
		doneRow(10, "done-this-session", sessionScope, openedAt.Add(time.Minute)),
		pendingRow(9, "pending", sessionScope),
		doneRow(8, "done-before-open", sessionScope, openedAt.Add(-time.Hour)),
	}
	got := texts(partitionDone(in, below))
	want := []string{"done-this-session", "pending", "done-before-open"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("partitionDone(on-start) =\n  %v\nwant\n  %v", got, want)
	}
}

// TestPartitionDoneOnStartTierWithNothingSinking pins the behaviour that a tier
// nothing sinks out of comes back exactly as it arrived.
//
// Read the next sentence before trusting this test: it is **masked by the
// short-circuit** and is not a discriminating guard. Break the partition so it
// keys off Done and this case still passes, because `sinking == 0` returns the
// tier before the broken loop can touch it. TestPartitionDoneOnStartMixedTier is
// the one that fails. Both were run against that mutation on 2026-08-22, which is
// the only reason the difference is known — the same relationship this repo
// already documents between TestCursorReAnchorsOnTaskID and
// TestCursorReAnchorsWhenRowsShift.
//
// It stays because the property is worth stating and it costs nothing, and
// because a future change to the short-circuit would make it bite.
func TestPartitionDoneOnStartTierWithNothingSinking(t *testing.T) {
	openedAt := time.Unix(1_760_000_000, 0)
	below := onStartBelow(openedAt)

	in := []task.Task{
		doneRow(10, "done-second", sessionScope, openedAt.Add(2*time.Minute)),
		pendingRow(9, "pending", sessionScope),
		doneRow(8, "done-first", sessionScope, openedAt.Add(time.Minute)),
	}
	got := texts(partitionDone(in, below))
	want := []string{"done-second", "pending", "done-first"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("partitionDone(on-start, nothing sinking) =\n  %v\nwant it untouched\n  %v", got, want)
	}
}

// TestOnStartBoundaryIsStrictlyBefore — timestamps are unix seconds, so a row
// completed in the very second the popup opened is ambiguous. It stays inline:
// right for your own keypress, harmless for another pane's.
func TestOnStartBoundaryIsStrictlyBefore(t *testing.T) {
	openedAt := time.Unix(1_760_000_000, 0)
	below := onStartBelow(openedAt)

	if below(doneRow(1, "same second", sessionScope, openedAt)) {
		t.Error("a row completed at exactly openedAt sinks; want it to stay inline")
	}
	if !below(doneRow(2, "one second earlier", sessionScope, openedAt.Add(-time.Second))) {
		t.Error("a row completed one second before the popup opened stays inline; want it below")
	}
	if below(doneRow(3, "one second later", sessionScope, openedAt.Add(time.Second))) {
		t.Error("a row completed after the popup opened sinks; want it to stay inline")
	}
}

// TestOnStartSinksANilDoneAt — a done row with no done_at cannot be shown to
// belong to this session, and must not be dereferenced to find out.
func TestOnStartSinksANilDoneAt(t *testing.T) {
	below := onStartBelow(time.Unix(1_760_000_000, 0))
	if !below(task.Task{ID: 1, Text: "no done_at", Done: true, Scope: sessionScope}) {
		t.Error("a done row with a nil done_at stays inline; want it below")
	}
}

// TestNoRuleSinksAPendingRow — every rule starts at Done. A pending row that
// sank would produce a list nothing else in this package expects.
func TestNoRuleSinksAPendingRow(t *testing.T) {
	pending := pendingRow(1, "pending", sessionScope)
	rules := map[string]belowRule{
		"always":   alwaysBelow,
		"on-start": onStartBelow(time.Unix(1_760_000_000, 0)),
	}
	for name, below := range rules {
		if below(pending) {
			t.Errorf("%s sinks a pending row", name)
		}
	}
}

// onStartBelow is config.OnStart's rule, built from a fixed openedAt so these
// cases stay pure. Model.belowRule is what wires the real one, and
// TestBelowRuleMatchesThePlacementSetting is what proves the two agree.
func onStartBelow(openedAt time.Time) belowRule {
	return func(t task.Task) bool {
		return t.Done && (t.DoneAt == nil || t.DoneAt.Before(openedAt))
	}
}

// TestBelowRuleMatchesThePlacementSetting closes the seam between the setting
// and the rule: a test that builds its own predicate proves nothing about which
// one Model.belowRule hands to partitionDone.
func TestBelowRuleMatchesThePlacementSetting(t *testing.T) {
	openedAt := time.Unix(1_760_000_000, 0)
	beforeOpen := doneRow(1, "before", sessionScope, openedAt.Add(-time.Hour))
	thisSession := doneRow(2, "during", sessionScope, openedAt.Add(time.Hour))
	pending := pendingRow(3, "pending", sessionScope)

	tests := []struct {
		placement          config.Placement
		wantBefore, during bool
	}{
		{config.Always, true, true},
		{config.Never, false, false},
		{config.OnStart, true, false},
		// The zero value is not one of the three and must behave as Never.
		{config.Placement(""), false, false},
	}

	for _, tt := range tests {
		t.Run(string(tt.placement), func(t *testing.T) {
			m := Model{
				cfg:      Config{Prefs: config.Prefs{CompleteToBottom: tt.placement}},
				openedAt: openedAt,
			}
			below := m.belowRule()
			// A nil rule is how "nothing sinks" is expressed, so normalise it
			// rather than special-casing every assertion below.
			if below == nil {
				below = func(task.Task) bool { return false }
			}
			if got := below(beforeOpen); got != tt.wantBefore {
				t.Errorf("row done before open: below = %v, want %v", got, tt.wantBefore)
			}
			if got := below(thisSession); got != tt.during {
				t.Errorf("row done this session: below = %v, want %v", got, tt.during)
			}
			if below(pending) {
				t.Error("a pending row sinks")
			}
		})
	}
}

// TestNewStampsOpenedAt — belowRule's on-start arm is measured against it, so a
// model whose openedAt was never set would treat every row as done-before-open.
func TestNewStampsOpenedAt(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	m := New(Config{Now: frozen(now)})
	if !m.openedAt.Equal(now) {
		t.Errorf("openedAt = %v, want %v", m.openedAt, now)
	}
}

// TestDoneSinceDoesNotConsultOpenedAt is a regression guard with a history: the
// max() against openedAt is what made store.DoneRetention dead code for four
// tasks, and this task puts openedAt back on the model for an unrelated reason.
// The field being in reach again is exactly when that bug could come back.
func TestDoneSinceDoesNotConsultOpenedAt(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	m := New(Config{Now: frozen(now)})
	m.openedAt = now.Add(-72 * time.Hour) // absurd, and must change nothing

	if want := now.Add(-store.DoneRetention); !m.doneSince().Equal(want) {
		t.Errorf("doneSince() = %v, want %v — it must depend on the clock alone", m.doneSince(), want)
	}
}
