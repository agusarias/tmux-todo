package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// deadSession is a scope key no client is in: a session that was renamed or
// killed while its tasks stayed filed under the old name. It is the whole reason
// this view exists, so almost every test here uses one.
var (
	deadSession  = task.Scope{Kind: task.ScopeSession, Key: "api"}
	otherDir     = task.Scope{Kind: task.ScopeDir, Key: "/Users/x/ws/elsewhere"}
	liveSessions = map[string]bool{"pulsar": true}
)

// allTasksConfig is the view's Config: the *active* scopes are only session
// pulsar, dir and global, while the database holds tasks under keys that are not
// active at all.
func allTasksConfig(db *store.DB) Config {
	return Config{
		DB:           db,
		Scopes:       []task.Scope{sessionScope, dirScope, globalScope},
		Home:         "/Users/x",
		LiveSessions: liveSessions,
	}
}

// seedStranded fills a database with two session groups (one live, one not), two
// dir groups (one active, one not) and a global group.
func seedStranded(t *testing.T, db *store.DB) {
	t.Helper()
	add(t, db, "rebase onto main", sessionScope)
	add(t, db, "check CI", sessionScope)
	add(t, db, "fix flaky test", deadSession)
	add(t, db, "update README", dirScope)
	add(t, db, "someone else's repo", otherDir)
	add(t, db, "call the dentist", globalScope)
}

// openAll opens the popup and presses `g`.
func openAll(t *testing.T, cfg Config) Model {
	t.Helper()
	m := newLoaded(t, cfg)
	m = pressAndSettle(t, m, "g")
	if m.view != viewAll {
		t.Fatalf("after g the view is %v, want the all-tasks view", m.view)
	}
	return m
}

// headers returns the group headers in the current row list, as plain scopes.
func headers(m Model) []task.Scope {
	var out []task.Scope
	for _, r := range m.rows {
		if r.kind == rowHeader {
			out = append(out, r.group.Scope)
		}
	}
	return out
}

// TestGToggles is DoD 1: the view flips both ways and each side keeps its own
// cursor for the life of the popup.
func TestGTogglesAndEachViewKeepsItsCursor(t *testing.T) {
	db := openDB(t)
	seedStranded(t, db)
	m := newLoaded(t, allTasksConfig(db))

	// Move down twice in the merged view, then leave and come back.
	m = pressAndSettle(t, m, "j")
	m = pressAndSettle(t, m, "j")
	mergedCursor := m.cursor
	if mergedCursor != 2 {
		t.Fatalf("merged cursor = %d after two j, want 2", mergedCursor)
	}

	m = pressAndSettle(t, m, "g")
	if m.view != viewAll {
		t.Fatalf("g did not switch to the all-tasks view")
	}
	// The all-tasks view starts at its own top, not at the merged cursor.
	if m.cursor != 1 {
		t.Errorf("all-tasks cursor = %d on arrival, want 1 (row 0 is a header)", m.cursor)
	}
	m = pressAndSettle(t, m, "j")
	m = pressAndSettle(t, m, "j")
	allCursor := m.cursor

	m = pressAndSettle(t, m, "g")
	if m.view != viewMerged {
		t.Fatalf("g again did not return to the merged view")
	}
	if m.cursor != mergedCursor {
		t.Errorf("merged cursor = %d on return, want the remembered %d", m.cursor, mergedCursor)
	}

	m = pressAndSettle(t, m, "g")
	if m.cursor != allCursor {
		t.Errorf("all-tasks cursor = %d on return, want the remembered %d", m.cursor, allCursor)
	}
}

// TestAllTasksListsEveryScope is DoD 2. The merged view cannot see the stranded
// rows *by construction* — that is the comparison that gives this test its
// teeth, since a view that merely listed the active scopes would pass a check
// that only counted rows.
func TestAllTasksListsEveryScopeIncludingInactiveOnes(t *testing.T) {
	db := openDB(t)
	seedStranded(t, db)

	merged := newLoaded(t, allTasksConfig(db))
	if got := texts(merged.tasks); len(got) != 4 {
		t.Fatalf("merged view shows %v, want only the four active-scope tasks", got)
	}

	m := openAll(t, allTasksConfig(db))
	// Groups run in tier order and, within a tier, in store.groupedOrder's
	// scope_key order — so "api" precedes "pulsar" and the *inactive* dir key
	// precedes the active one. Newest-first applies *inside* a group.
	want := []string{
		"fix flaky test",               // session api — not active, not running
		"check CI", "rebase onto main", // session pulsar, newest first
		"someone else's repo", // dir elsewhere — not active
		"update README",       // dir ~/ws/pulsar
		"call the dentist",    // global
	}
	if got := texts(m.tasks); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("all-tasks rows =\n  %v\nwant\n  %v", got, want)
	}

	// Groups in tier order, newest first inside a group.
	gotScopes := headers(m)
	wantScopes := []task.Scope{deadSession, sessionScope, otherDir, dirScope, globalScope}
	if len(gotScopes) != len(wantScopes) {
		t.Fatalf("headers = %v, want %v", gotScopes, wantScopes)
	}
	for i, want := range wantScopes {
		if gotScopes[i] != want {
			t.Errorf("header %d = %v, want %v", i, gotScopes[i], want)
		}
	}
}

// TestEmptyScopesAreAbsentNotEmptyHeaded is the other half of DoD 2.
func TestScopesWithNoTasksHaveNoHeader(t *testing.T) {
	db := openDB(t)
	add(t, db, "only this", globalScope)

	m := openAll(t, allTasksConfig(db))
	if got := headers(m); len(got) != 1 || got[0] != globalScope {
		t.Errorf("headers = %v, want the global group alone", got)
	}
}

// TestLivenessComesFromConfigAlone is DoD 4. The model is driven off Config with
// no tmux anywhere: the same database renders `(live)` or `(not running)` purely
// according to what was injected.
func TestLivenessComesFromConfigAlone(t *testing.T) {
	db := openDB(t)
	add(t, db, "rebase onto main", sessionScope)
	add(t, db, "fix flaky test", deadSession)

	for _, tc := range []struct {
		name         string
		live         map[string]bool
		wantLive     []string
		wantNotAlive []string
	}{
		{"pulsar live", map[string]bool{"pulsar": true}, []string{"pulsar"}, []string{"api"}},
		{"api live", map[string]bool{"api": true}, []string{"api"}, []string{"pulsar"}},
		{"both live", map[string]bool{"pulsar": true, "api": true}, []string{"pulsar", "api"}, nil},
		{"nothing known", nil, nil, []string{"pulsar", "api"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := allTasksConfig(db)
			cfg.LiveSessions = tc.live
			m := openAll(t, cfg)

			labels := map[string]bool{}
			for _, r := range m.rows {
				if r.kind == rowHeader && r.group.Scope.Kind == task.ScopeSession {
					labels[r.group.Scope.Key] = r.live
				}
			}
			for _, key := range tc.wantLive {
				if !labels[key] {
					t.Errorf("session %q reads not running, want live", key)
				}
			}
			for _, key := range tc.wantNotAlive {
				if labels[key] {
					t.Errorf("session %q reads live, want not running", key)
				}
			}

			// And it reaches the frame, not just the model.
			frame := m.View()
			for _, key := range tc.wantLive {
				if !strings.Contains(frame, key) {
					t.Errorf("frame does not mention session %q:\n%s", key, frame)
				}
			}
			if len(tc.wantLive) > 0 && !strings.Contains(frame, labelLive) {
				t.Errorf("frame has no %q label:\n%s", labelLive, frame)
			}
			if len(tc.wantNotAlive) > 0 && !strings.Contains(frame, labelNotRunning) {
				t.Errorf("frame has no %q label:\n%s", labelNotRunning, frame)
			}
		})
	}
}

// TestJumpHintIsOnTheHeaderNotTheRows is DoD 5: once per group, never per row.
func TestJumpHintIsOnTheHeaderNotTheRows(t *testing.T) {
	db := openDB(t)
	seedStranded(t, db)
	m := openAll(t, allTasksConfig(db))

	lines := m.renderRowLines()
	if len(lines) != len(m.rows) {
		t.Fatalf("%d lines for %d rows", len(lines), len(m.rows))
	}
	for i, r := range m.rows {
		line := lines[i]
		hasHint := strings.Contains(line, hintSwitch) || strings.Contains(line, hintSesh)
		switch {
		case r.kind == rowTask && hasHint:
			t.Errorf("task row %d carries a jump hint, want it on the header only: %q", i, line)
		case r.kind == rowHeader && r.group.Scope.Kind == task.ScopeSession && !hasHint:
			t.Errorf("session header %d has no jump hint: %q", i, line)
		case r.kind == rowHeader && r.group.Scope.Kind != task.ScopeSession && hasHint:
			t.Errorf("non-session header %d has a jump hint, but Enter does nothing there: %q", i, line)
		}
	}

	// The hint says which of the two things Enter will do.
	for i, r := range m.rows {
		if r.kind != rowHeader || r.group.Scope.Kind != task.ScopeSession {
			continue
		}
		want, unwanted := hintSesh, hintSwitch
		if r.live {
			want, unwanted = hintSwitch, hintSesh
		}
		if !strings.Contains(lines[i], want) || strings.Contains(lines[i], unwanted) {
			t.Errorf("header %q (live=%v) shows the wrong hint: %q", r.group.Scope.Key, r.live, lines[i])
		}
	}
}

// TestHeadersAreNotSelectable is DoD 6: walk the list end to end and assert
// every stop is a task, in both directions.
func TestHeadersAreNotSelectable(t *testing.T) {
	db := openDB(t)
	seedStranded(t, db)
	m := openAll(t, allTasksConfig(db))

	if len(m.rows) != 11 {
		t.Fatalf("rows = %d, want 6 tasks and 5 headers", len(m.rows))
	}

	// Down the whole list and back up, one press per row so the walk cannot
	// stop early, checking every position on the way.
	check := func(where string) {
		t.Helper()
		if m.cursor < 0 || m.cursor >= len(m.rows) {
			t.Fatalf("%s: cursor %d is out of range for %d rows", where, m.cursor, len(m.rows))
		}
		if m.rows[m.cursor].kind != rowTask {
			t.Fatalf("%s: cursor %d landed on a header (%v)", where, m.cursor, m.rows[m.cursor].group.Scope)
		}
	}
	check("on arrival")
	var seen []string
	for range len(m.rows) + 2 {
		check("going down")
		seen = append(seen, m.rows[m.cursor].task.Text)
		m = pressAndSettle(t, m, "j")
		check("after j")
	}
	for range len(m.rows) + 2 {
		check("going up")
		m = pressAndSettle(t, m, "k")
		check("after k")
	}
	if m.cursor != 1 {
		t.Errorf("k to the top left the cursor at %d, want 1 (row 0 is a header)", m.cursor)
	}

	// The walk really did visit every task, not just the first one repeatedly.
	for _, want := range texts(m.tasks) {
		if !containsString(seen, want) {
			t.Errorf("walking the list never stopped on %q", want)
		}
	}
}

// TestOneJStepsOneTaskAcrossAGroupBoundary — the header between two groups must
// cost no extra keypress, which is what stepping by selectable row buys over
// stepping by index and snapping afterwards.
func TestOneJStepsOneTaskAcrossAGroupBoundary(t *testing.T) {
	db := openDB(t)
	add(t, db, "last of its group", deadSession)
	add(t, db, "first of the next", sessionScope)
	m := openAll(t, allTasksConfig(db))

	if got, _ := m.selectedTask(); got.Text != "last of its group" {
		t.Fatalf("cursor starts on %q", got.Text)
	}
	m = pressAndSettle(t, m, "j")
	if got, _ := m.selectedTask(); got.Text != "first of the next" {
		t.Errorf("one j across a group boundary landed on %q, want the next group's first task", got.Text)
	}
}

// TestAllTasksHidesOldDoneRows is DoD 7: the same doneSince rule as the merged
// view, so the wide view is not a graveyard while the merged one stays clean.
func TestAllTasksSharesTheDoneVisibilityRule(t *testing.T) {
	db := openDB(t)
	pending := add(t, db, "still to do", deadSession)
	old := add(t, db, "done yesterday", deadSession)
	if err := db.Complete(context.Background(), old.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	done := mustGet(t, db, old.ID)

	// A popup opened long ago and now sitting past the retention window, so the
	// row is outside it on arrival — exactly the case the merged view hides.
	openedLongAgo := done.DoneAt.Add(-time.Hour)
	cfg := allTasksConfig(db)
	cfg.Now = frozen(openedLongAgo)
	m := New(cfg)
	m.openedAt = openedLongAgo
	m.cfg.Now = frozen(done.DoneAt.Add(store.DoneRetention + time.Hour))

	next, _ := m.Update(m.reloadCmd()())
	m = next.(Model)
	m = pressAndSettle(t, m, "g")
	if m.view != viewAll {
		t.Fatalf("g did not switch to the all-tasks view")
	}
	if got := texts(m.tasks); len(got) != 1 || got[0] != pending.Text {
		t.Errorf("all-tasks rows = %v, want the aged done row hidden", got)
	}
	// Still there in the store: hidden, never deleted.
	if got := listAll(t, db); len(got) != 2 {
		t.Errorf("store holds %v, want both rows", texts(got))
	}
}

// TestFilterKeysNarrowTheAllTasksViewByTier — DoD 17's 1/2/3 in this view means
// "show only this tier's groups", not "only the active scope of this tier": a
// tier filter that dropped the inactive keys would hide exactly the rows the
// view exists to surface.
func TestFilterKeysNarrowTheAllTasksViewByTier(t *testing.T) {
	db := openDB(t)
	seedStranded(t, db)
	m := openAll(t, allTasksConfig(db))

	m = pressAndSettle(t, m, "1")
	if got := headers(m); len(got) != 2 {
		t.Errorf("session filter shows %v, want both session groups including the dead one", got)
	}
	if got := texts(m.tasks); len(got) != 3 {
		t.Errorf("session filter rows = %v, want all three session tasks", got)
	}

	m = pressAndSettle(t, m, "1")
	if got := headers(m); len(got) != 5 {
		t.Errorf("unfiltering shows %v headers, want all five", got)
	}

	m = pressAndSettle(t, m, "2")
	for _, h := range headers(m) {
		if h.Kind != task.ScopeDir {
			t.Errorf("dir filter shows a %s group", h.Kind)
		}
	}
	if got := texts(m.tasks); len(got) != 2 {
		t.Errorf("dir filter rows = %v, want both dir tasks", got)
	}
}

// TestFilterSurvivesAViewSwitch — the tier filter is a property of the popup,
// not of one view, so `1` then `g` must not silently widen back.
func TestFilterSurvivesAViewSwitch(t *testing.T) {
	db := openDB(t)
	seedStranded(t, db)
	m := newLoaded(t, allTasksConfig(db))
	m = pressAndSettle(t, m, "3")
	m = pressAndSettle(t, m, "g")

	if got := headers(m); len(got) != 1 || got[0].Kind != task.ScopeGlobal {
		t.Errorf("headers after 3 then g = %v, want the global group alone", got)
	}
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// groupOf is a one-task group under a scope, for header tests that care about
// the scope and not the tasks.
func groupOf(s task.Scope) store.Group {
	return store.Group{Scope: s, Tasks: []task.Task{{ID: 1, Text: "x", Scope: s}}}
}
