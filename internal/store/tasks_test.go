package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/agusarias/tmux-todo/internal/task"
)

var (
	globalScope  = task.Scope{Kind: task.ScopeGlobal}
	sessionScope = task.Scope{Kind: task.ScopeSession, Key: "pulsar"}
	dirScope     = task.Scope{Kind: task.ScopeDir, Key: "/Users/x/ws/pulsar"}
)

// TestAddListRoundTrip is DoD 8: every field survives the trip through SQLite,
// including the second on the timestamps.
func TestAddListRoundTrip(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	created := time.Unix(1_760_000_000, 0)
	freezeClock(db, created)

	added, err := db.Add(ctx, "  rebase onto main  ", sessionScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.Text != "rebase onto main" {
		t.Errorf("text = %q, want it trimmed", added.Text)
	}
	if added.ID == 0 {
		t.Error("Add returned no id")
	}
	if !added.CreatedAt.Equal(created) {
		t.Errorf("returned CreatedAt = %v, want %v", added.CreatedAt, created)
	}
	if added.Done || added.DoneAt != nil {
		t.Error("a new task should be pending with no done_at")
	}

	listed, err := db.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 {
		t.Fatalf("List returned %d tasks, want 1", len(listed))
	}
	got := listed[0]
	if got.ID != added.ID || got.Text != added.Text || got.Scope != sessionScope {
		t.Errorf("round-trip mismatch: %+v vs %+v", got, added)
	}
	if !got.CreatedAt.Equal(created) {
		t.Errorf("CreatedAt = %v, want %v", got.CreatedAt, created)
	}
	if got.DoneAt != nil {
		t.Errorf("DoneAt = %v, want nil", got.DoneAt)
	}

	fetched, err := db.Get(ctx, added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !fetched.CreatedAt.Equal(created) || fetched.Text != added.Text {
		t.Errorf("Get returned %+v, want %+v", fetched, added)
	}
}

// TestCompleteUncompleteDoneAt is the other half of DoD 8: the nil/non-nil
// distinction on DoneAt is preserved in both directions.
func TestCompleteUncompleteDoneAt(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	freezeClock(db, time.Unix(1_760_000_000, 0))

	added, err := db.Add(ctx, "call the dentist", globalScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	completedAt := time.Unix(1_760_000_500, 0)
	freezeClock(db, completedAt)
	if err := db.Complete(ctx, added.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	done, err := db.Get(ctx, added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !done.Done {
		t.Error("task not marked done")
	}
	if done.DoneAt == nil {
		t.Fatal("DoneAt is nil after Complete")
	}
	if !done.DoneAt.Equal(completedAt) {
		t.Errorf("DoneAt = %v, want %v", *done.DoneAt, completedAt)
	}
	if !done.CreatedAt.Equal(time.Unix(1_760_000_000, 0)) {
		t.Errorf("Complete disturbed CreatedAt: %v", done.CreatedAt)
	}

	// Pending-only listing hides it; IncludeDone shows it.
	pending, err := db.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("List pending: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("pending list has %d tasks, want 0", len(pending))
	}
	all, err := db.List(ctx, Filter{IncludeDone: true})
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("full list has %d tasks, want 1", len(all))
	}

	if err := db.Uncomplete(ctx, added.ID); err != nil {
		t.Fatalf("Uncomplete: %v", err)
	}
	back, err := db.Get(ctx, added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if back.Done || back.DoneAt != nil {
		t.Errorf("after Uncomplete: done=%v doneAt=%v, want false/nil", back.Done, back.DoneAt)
	}
}

// TestListOrdering is DoD 7: scope tier first (session, dir, global), newest
// first inside a tier. Every tier is pinned explicitly because a CASE-based
// ordering is easy to get subtly wrong.
func TestListOrdering(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	// Added oldest-first, deliberately not in tier order.
	seq := []struct {
		text  string
		scope task.Scope
	}{
		{"global old", globalScope},
		{"dir old", dirScope},
		{"session old", sessionScope},
		{"global new", globalScope},
		{"dir new", dirScope},
		{"session new", sessionScope},
	}
	base := time.Unix(1_760_000_000, 0)
	for i, s := range seq {
		freezeClock(db, base.Add(time.Duration(i)*time.Minute))
		if _, err := db.Add(ctx, s.text, s.scope); err != nil {
			t.Fatalf("Add %q: %v", s.text, err)
		}
	}

	got, err := db.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{
		"session new", "session old",
		"dir new", "dir old",
		"global new", "global old",
	}
	if len(got) != len(want) {
		t.Fatalf("List returned %d tasks, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Text != want[i] {
			t.Errorf("position %d = %q, want %q", i, got[i].Text, want[i])
		}
	}
}

// TestListSameSecondTiebreak pins the id tiebreak: within one second, newest
// still wins, which is why milliseconds were not needed.
func TestListSameSecondTiebreak(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	freezeClock(db, time.Unix(1_760_000_000, 0))

	for _, text := range []string{"first", "second", "third"} {
		if _, err := db.Add(ctx, text, globalScope); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}
	got, err := db.List(ctx, Filter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"third", "second", "first"}
	for i := range want {
		if got[i].Text != want[i] {
			t.Errorf("position %d = %q, want %q", i, got[i].Text, want[i])
		}
	}
}

// TestListFiltersByScope covers the merged popup's case: only the scopes that
// are active right now.
func TestListFiltersByScope(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	other := task.Scope{Kind: task.ScopeSession, Key: "api"}
	for _, s := range []struct {
		text  string
		scope task.Scope
	}{
		{"here", sessionScope},
		{"elsewhere", other},
		{"everywhere", globalScope},
	} {
		if _, err := db.Add(ctx, s.text, s.scope); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	got, err := db.List(ctx, Filter{Scopes: []task.Scope{sessionScope, globalScope}})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var texts []string
	for _, g := range got {
		texts = append(texts, g.Text)
	}
	want := "here,everywhere"
	if strings.Join(texts, ",") != want {
		t.Errorf("filtered list = %q, want %q", strings.Join(texts, ","), want)
	}

	n, err := db.Count(ctx, Filter{Scopes: []task.Scope{other}})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 1 {
		t.Errorf("Count for the inactive session = %d, want 1", n)
	}
}

// TestListGroupedIncludesInactiveScopes is the all-tasks view's case: every
// scope in the database, including session names that are not running.
func TestListGroupedIncludesInactiveScopes(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	deadSession := task.Scope{Kind: task.ScopeSession, Key: "api"}
	adds := []struct {
		text  string
		scope task.Scope
	}{
		{"switch there", sessionScope},
		{"fix flaky test", deadSession},
		{"update README", dirScope},
		{"call the dentist", globalScope},
		{"second dentist task", globalScope},
	}
	base := time.Unix(1_760_000_000, 0)
	for i, a := range adds {
		freezeClock(db, base.Add(time.Duration(i)*time.Minute))
		if _, err := db.Add(ctx, a.text, a.scope); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	groups, err := db.ListGrouped(ctx, Filter{})
	if err != nil {
		t.Fatalf("ListGrouped: %v", err)
	}
	if len(groups) != 4 {
		t.Fatalf("got %d groups, want 4", len(groups))
	}

	// Session groups first (sorted by key), then dir, then global.
	wantScopes := []task.Scope{deadSession, sessionScope, dirScope, globalScope}
	for i, want := range wantScopes {
		if groups[i].Scope != want {
			t.Errorf("group %d scope = %+v, want %+v", i, groups[i].Scope, want)
		}
	}
	last := groups[len(groups)-1]
	if len(last.Tasks) != 2 {
		t.Fatalf("global group has %d tasks, want 2", len(last.Tasks))
	}
	if last.Tasks[0].Text != "second dentist task" {
		t.Errorf("global group not newest-first: %q", last.Tasks[0].Text)
	}
}

func TestUpdateTextRescopeDelete(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	added, err := db.Add(ctx, "fix auth redirect", dirScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := db.UpdateText(ctx, added.ID, "  fix auth redirect loop  "); err != nil {
		t.Fatalf("UpdateText: %v", err)
	}
	if err := db.Rescope(ctx, added.ID, globalScope); err != nil {
		t.Fatalf("Rescope: %v", err)
	}
	got, err := db.Get(ctx, added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Text != "fix auth redirect loop" {
		t.Errorf("text = %q", got.Text)
	}
	if got.Scope != globalScope {
		t.Errorf("scope = %+v, want %+v", got.Scope, globalScope)
	}

	if err := db.Delete(ctx, added.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := db.Get(ctx, added.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: %v, want ErrNotFound", err)
	}
}

func TestMissingIDReportsNotFound(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	ops := map[string]func() error{
		"Get":        func() error { _, err := db.Get(ctx, 404); return err },
		"Complete":   func() error { return db.Complete(ctx, 404) },
		"Uncomplete": func() error { return db.Uncomplete(ctx, 404) },
		"UpdateText": func() error { return db.UpdateText(ctx, 404, "x") },
		"Rescope":    func() error { return db.Rescope(ctx, 404, globalScope) },
		"Delete":     func() error { return db.Delete(ctx, 404) },
	}
	for name, op := range ops {
		if err := op(); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s on a missing id: %v, want ErrNotFound", name, err)
		}
	}
}

func TestValidationRejectsBadInput(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()

	if _, err := db.Add(ctx, "   ", globalScope); err == nil {
		t.Error("Add accepted blank text")
	}
	if _, err := db.Add(ctx, "x", task.Scope{Kind: task.ScopeKind("project"), Key: "k"}); err == nil {
		t.Error("Add accepted an unknown scope kind")
	}
	if _, err := db.Add(ctx, "x", task.Scope{Kind: task.ScopeSession}); err == nil {
		t.Error("Add accepted a session scope with no key")
	}

	// A global scope drops any key it is handed, matching the schema CHECK.
	added, err := db.Add(ctx, "global with stray key", task.Scope{Kind: task.ScopeGlobal, Key: "ignored"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if added.Scope.Key != "" {
		t.Errorf("global scope kept key %q", added.Scope.Key)
	}

	if err := db.UpdateText(ctx, added.ID, " "); err == nil {
		t.Error("UpdateText accepted blank text")
	}
}

func TestPurgeDoneUsesCallerCutoff(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	base := time.Unix(1_760_000_000, 0)

	freezeClock(db, base)
	old, err := db.Add(ctx, "old done", globalScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	recent, err := db.Add(ctx, "recent done", globalScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	pending, err := db.Add(ctx, "still pending", globalScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	freezeClock(db, base) // completed 25h before the cutoff
	if err := db.Complete(ctx, old.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	freezeClock(db, base.Add(24*time.Hour+time.Hour)) // completed after it
	if err := db.Complete(ctx, recent.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	n, err := db.PurgeDone(ctx, base.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("PurgeDone: %v", err)
	}
	if n != 1 {
		t.Errorf("PurgeDone removed %d rows, want 1", n)
	}
	if _, err := db.Get(ctx, old.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("old done task survived: %v", err)
	}
	if _, err := db.Get(ctx, recent.ID); err != nil {
		t.Errorf("recently done task was purged: %v", err)
	}
	if _, err := db.Get(ctx, pending.ID); err != nil {
		t.Errorf("pending task was purged: %v", err)
	}
}

// TestConcurrentHandles is DoD 9: two independent *DB handles on one file, as
// two open popups would be, interleaving writes and reads without SQLITE_BUSY.
func TestConcurrentHandles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.db")
	one := openAt(t, path)
	two := openAt(t, path)
	ctx := context.Background()

	const rounds = 40
	errs := make(chan error, 4*rounds)
	var wg sync.WaitGroup

	writer := func(db *DB, label string, scope task.Scope) {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			added, err := db.Add(ctx, fmt.Sprintf("%s-%d", label, i), scope)
			if err != nil {
				errs <- fmt.Errorf("%s add: %w", label, err)
				return
			}
			if i%2 == 0 {
				if err := db.Complete(ctx, added.ID); err != nil {
					errs <- fmt.Errorf("%s complete: %w", label, err)
					return
				}
			}
		}
	}
	reader := func(db *DB, label string) {
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if _, err := db.List(ctx, Filter{IncludeDone: true}); err != nil {
				errs <- fmt.Errorf("%s list: %w", label, err)
				return
			}
			if _, err := db.Count(ctx, Filter{}); err != nil {
				errs <- fmt.Errorf("%s count: %w", label, err)
				return
			}
		}
	}

	wg.Add(4)
	go writer(one, "one", sessionScope)
	go writer(two, "two", dirScope)
	go reader(one, "one")
	go reader(two, "two")
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent access failed: %v", err)
	}

	// Both handles see all the writes.
	for i, db := range []*DB{one, two} {
		n, err := db.Count(ctx, Filter{IncludeDone: true})
		if err != nil {
			t.Fatalf("handle %d Count: %v", i, err)
		}
		if n != 2*rounds {
			t.Errorf("handle %d sees %d tasks, want %d", i, n, 2*rounds)
		}
	}
}

// TestDoneSinceBoundsOnlyDoneRows is the single most important test in the
// completed-task-lifecycle task. The bound exists to age *completed* rows out of
// the view, and the obvious predicate — `AND done_at >= ?` — silently drops every
// pending row instead, because a pending row's done_at is NULL and no comparison
// against NULL is ever true. That failure would empty the popup.
func TestDoneSinceBoundsOnlyDoneRows(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	base := time.Unix(1_760_000_000, 0)

	freezeClock(db, base)
	pending, err := db.Add(ctx, "still to do", globalScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	recent, err := db.Add(ctx, "just finished", globalScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	stale, err := db.Add(ctx, "finished long ago", globalScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Complete one an hour ago and one two days ago.
	freezeClock(db, base.Add(-time.Hour))
	if err := db.Complete(ctx, recent.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	freezeClock(db, base.Add(-48*time.Hour))
	if err := db.Complete(ctx, stale.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	cutoff := base.Add(-DoneRetention)
	got, err := db.List(ctx, Filter{IncludeDone: true, DoneSince: cutoff})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	ids := map[int64]bool{}
	for _, task := range got {
		ids[task.ID] = true
	}
	if !ids[pending.ID] {
		t.Error("the pending task was filtered out by DoneSince; the predicate is catching NULL done_at")
	}
	if !ids[recent.ID] {
		t.Error("a task completed inside the window is hidden")
	}
	if ids[stale.ID] {
		t.Error("a task completed before the cutoff is still visible")
	}

	// Hidden is not deleted: this task never reaps rows.
	if _, err := db.Get(ctx, stale.ID); err != nil {
		t.Errorf("the aged-out task left the database: %v", err)
	}
	total, err := db.Count(ctx, Filter{IncludeDone: true})
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if total != 3 {
		t.Errorf("Count = %d, want all 3 rows still stored", total)
	}
}

// TestDoneSinceIsInclusive pins >= rather than >, so a row completed exactly on
// the boundary stays visible.
func TestDoneSinceIsInclusive(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	at := time.Unix(1_760_000_000, 0)

	freezeClock(db, at)
	added, err := db.Add(ctx, "boundary", globalScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := db.Complete(ctx, added.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, err := db.List(ctx, Filter{IncludeDone: true, DoneSince: at})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("a row completed exactly at the cutoff was hidden; the bound is exclusive")
	}

	// One second later it is out.
	got, err = db.List(ctx, Filter{IncludeDone: true, DoneSince: at.Add(time.Second)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("a row completed before the cutoff is still visible: %+v", got)
	}
}

// TestDoneSinceZeroKeepsPreviousBehaviour — the field is additive, so call sites
// written before it existed keep working unchanged.
func TestDoneSinceZeroKeepsPreviousBehaviour(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	freezeClock(db, time.Unix(1_700_000_000, 0))

	added, err := db.Add(ctx, "ancient history", globalScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := db.Complete(ctx, added.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, err := db.List(ctx, Filter{IncludeDone: true})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("with a zero DoneSince the row should be unbounded, got %d rows", len(got))
	}
}

// TestDoneSinceIgnoredWhenDoneExcluded — with IncludeDone false there are no done
// rows to bound, and the bound must not start filtering pending ones.
func TestDoneSinceIgnoredWhenDoneExcluded(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	base := time.Unix(1_760_000_000, 0)
	freezeClock(db, base)

	if _, err := db.Add(ctx, "pending", globalScope); err != nil {
		t.Fatalf("Add: %v", err)
	}
	got, err := db.List(ctx, Filter{DoneSince: base.Add(time.Hour)})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("DoneSince filtered a pending row when IncludeDone was false, got %d rows", len(got))
	}
}

// TestDoneSinceCombinesWithScopes guards the placeholder ordering: DoneSince's
// argument is bound before the scope arguments, and a mismatch there would
// silently compare the wrong values rather than error.
func TestDoneSinceCombinesWithScopes(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	base := time.Unix(1_760_000_000, 0)
	freezeClock(db, base)

	mine, err := db.Add(ctx, "mine, done recently", sessionScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if _, err := db.Add(ctx, "other scope", dirScope); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := db.Complete(ctx, mine.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	got, err := db.List(ctx, Filter{
		Scopes:      []task.Scope{sessionScope},
		IncludeDone: true,
		DoneSince:   base.Add(-DoneRetention),
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0].ID != mine.ID {
		t.Errorf("scope + DoneSince together returned %+v, want just the session task", got)
	}
}

// TestDoneRetentionIsTheSingleDefinition — the 24h number lives here and nowhere
// else, so `cli` and `tui` cannot drift from it.
func TestDoneRetentionIsTheSingleDefinition(t *testing.T) {
	if DoneRetention != 24*time.Hour {
		t.Errorf("DoneRetention = %v, want 24h per docs/design.md", DoneRetention)
	}
}

// TestCompleteIsReversible — space is a toggle, and it is the only undo the
// product has until an undo stack lands.
func TestCompleteIsReversible(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	freezeClock(db, time.Unix(1_760_000_000, 0))

	added, err := db.Add(ctx, "toggle me", globalScope)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := db.Complete(ctx, added.ID); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	done, err := db.Get(ctx, added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !done.Done || done.DoneAt == nil {
		t.Fatalf("after Complete: done=%v done_at=%v", done.Done, done.DoneAt)
	}

	if err := db.Uncomplete(ctx, added.ID); err != nil {
		t.Fatalf("Uncomplete: %v", err)
	}
	back, err := db.Get(ctx, added.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if back.Done || back.DoneAt != nil {
		t.Errorf("after Uncomplete: done=%v done_at=%v, want pending with no stamp", back.Done, back.DoneAt)
	}
}
