package tui

import (
	"context"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// viewRecorder is a fake SetAllTasks. It records every value it was handed —
// not just a call count, because the bug worth catching is persisting the wrong
// view, which a counter cannot see.
type viewRecorder struct {
	calls []bool
	err   error
}

func (v *viewRecorder) set(all bool) error {
	v.calls = append(v.calls, all)
	return v.err
}

// stickyModel is a loaded model with a recording SetAllTasks over one task.
func stickyModel(t *testing.T, rec *viewRecorder) Model {
	t.Helper()
	db := openDB(t)
	add(t, db, "rebase onto main", sessionScope)
	add(t, db, "call the dentist", globalScope)
	return newLoaded(t, Config{
		DB:          db,
		Scopes:      []task.Scope{sessionScope, dirScope, globalScope},
		Home:        "/Users/x",
		Version:     "dev",
		SetAllTasks: rec.set,
	})
}

// TestQuitPersistsTheView is DoD 2's second half and the core of DoD 3: quitting
// records the view the popup is in, from either view.
func TestQuitPersistsTheView(t *testing.T) {
	for _, tc := range []struct {
		name string
		keys []string
		want bool
	}{
		{"from the merged list", nil, false},
		{"from the all-tasks view", []string{"g"}, true},
		{"after toggling back", []string{"g", "g"}, false},
		{"after toggling twice into it", []string{"g", "g", "g"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &viewRecorder{}
			m := stickyModel(t, rec)
			for _, k := range tc.keys {
				m = pressAndSettle(t, m, k)
			}
			m = pressAndSettle(t, m, "q")

			if len(rec.calls) != 1 {
				t.Fatalf("SetAllTasks called %d times, want exactly 1 per popup: %v", len(rec.calls), rec.calls)
			}
			if rec.calls[0] != tc.want {
				t.Errorf("persisted %v, want %v", rec.calls[0], tc.want)
			}
		})
	}
}

// TestTogglingTheViewPersistsNothing is DoD 2's first half. `g` is a view
// change, not a preference change: an accidental press that is immediately
// undone must never reach the file.
func TestTogglingTheViewPersistsNothing(t *testing.T) {
	rec := &viewRecorder{}
	m := stickyModel(t, rec)

	for _, k := range []string{"g", "g", "g", "j", "1", "g"} {
		m = pressAndSettle(t, m, k)
		if len(rec.calls) != 0 {
			t.Fatalf("SetAllTasks called after %q, before any quit: %v", k, rec.calls)
		}
	}

	// ...and then the quit writes exactly once, so the test above is not just
	// asserting that the setter is never called at all.
	m = pressAndSettle(t, m, "q")
	if len(rec.calls) != 1 {
		t.Errorf("SetAllTasks called %d times on quit, want 1", len(rec.calls))
	}
}

// TestQuitPersistsOnBothQuitPaths is DoD 3, spelled out.
//
// quit() returns early when nothing is queued, and that early return is the path
// almost every popup takes. A persist hung off commitDeletesCmd would save the
// preference only for users who had pressed `d` — which is why this asserts the
// empty-queue path explicitly rather than trusting the table above to have
// covered it.
func TestQuitPersistsOnBothQuitPaths(t *testing.T) {
	t.Run("empty delete queue", func(t *testing.T) {
		rec := &viewRecorder{}
		m := stickyModel(t, rec)
		m = pressAndSettle(t, m, "g")
		if len(m.queued) != 0 {
			t.Fatalf("the queue is not empty (%d); this leg tests the early return", len(m.queued))
		}

		next, cmd := m.Update(keyMsg("q"))
		if !next.(Model).quitting {
			t.Error("the model is not quitting")
		}
		if cmd == nil {
			t.Fatal("quit returned no command; it must at least tea.Quit")
		}
		if len(rec.calls) != 1 || !rec.calls[0] {
			t.Errorf("SetAllTasks calls = %v, want exactly [true] on the empty-queue path", rec.calls)
		}
	})

	t.Run("non-empty delete queue", func(t *testing.T) {
		rec := &viewRecorder{}
		m := stickyModel(t, rec)
		m = pressAndSettle(t, m, "g")
		m = pressAndSettle(t, m, "d")
		if len(m.queued) == 0 {
			t.Fatal("nothing was queued; this leg tests the commit path")
		}

		next, cmd := m.Update(keyMsg("q"))
		if cmd == nil {
			t.Fatal("quit returned no command; the queued deletes must still commit")
		}
		if len(rec.calls) != 1 || !rec.calls[0] {
			t.Errorf("SetAllTasks calls = %v, want exactly [true] on the commit path", rec.calls)
		}
		// The commit still runs, and the persist did not displace it.
		if _, ok := cmd().(deletesCommittedMsg); !ok {
			t.Error("the command quit returned is not the delete commit")
		}
		_ = next
	})
}

// TestPersistFailureStillQuitsAndStillCommits is DoD 4. A preference file is
// never worth an exit.
func TestPersistFailureStillQuitsAndStillCommits(t *testing.T) {
	rec := &viewRecorder{err: errors.New("read-only file system")}
	m := stickyModel(t, rec)
	m = pressAndSettle(t, m, "g")
	m = pressAndSettle(t, m, "d")
	queued := m.queuedIDs()
	if len(queued) == 0 {
		t.Fatal("nothing queued")
	}

	next, cmd := m.Update(keyMsg("q"))
	got := next.(Model)

	if !got.quitting {
		t.Error("a failed persist stopped the quit")
	}
	if cmd == nil {
		t.Fatal("a failed persist swallowed the delete commit")
	}
	msg, ok := cmd().(deletesCommittedMsg)
	if !ok {
		t.Fatalf("command returned %T, want deletesCommittedMsg", cmd())
	}
	if msg.err != nil {
		t.Errorf("the delete commit failed: %v", msg.err)
	}
	// The rows really went, so "still commits" is about the database and not
	// just about the message type.
	for _, id := range queued {
		if _, err := got.cfg.DB.Get(context.Background(), id); !errors.Is(err, store.ErrNotFound) {
			t.Errorf("queued row %d survived the commit: %v", id, err)
		}
	}
	// And nothing was rendered about it: the popup is on its way out.
	if view := got.View(); strings.Contains(view, "read-only") {
		t.Errorf("the persist error reached the frame:\n%s", view)
	}
	if got.err != nil {
		t.Errorf("the persist error became a model error: %v", got.err)
	}
}

// TestQuitWithNoSetterIsInert — a nil SetAllTasks must not panic on the exit
// path. `tdo tui` run by hand takes this path if the state dir is unusable.
func TestQuitWithNoSetterIsInert(t *testing.T) {
	db := openDB(t)
	add(t, db, "rebase onto main", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: []task.Scope{globalScope}})

	next, cmd := m.Update(keyMsg("q"))
	if !next.(Model).quitting {
		t.Error("the popup did not quit with no SetAllTasks injected")
	}
	if cmd == nil {
		t.Error("quit returned no command")
	}
}

// TestConfigAllTasksPicksTheOpeningView is DoD 9. The view must be chosen before
// Init() runs, because the two views issue *different queries* — so a model that
// set it afterwards would render one frame of the merged list and waste a query
// on the popup's cold-start path.
func TestConfigAllTasksPicksTheOpeningView(t *testing.T) {
	for _, want := range []bool{true, false} {
		db := openDB(t)
		add(t, db, "rebase onto main", sessionScope)
		add(t, db, "call the dentist", globalScope)
		cfg := Config{
			DB:           db,
			Scopes:       []task.Scope{sessionScope, dirScope, globalScope},
			Home:         "/Users/x",
			LiveSessions: map[string]bool{"pulsar": true},
			AllTasks:     want,
		}

		// Before Init: the constructor alone must have decided.
		fresh := New(cfg)
		gotView := fresh.view == viewAll
		if gotView != want {
			t.Errorf("AllTasks=%v: New gave view %v, want all-tasks=%v", want, fresh.view, want)
		}

		// And the *first* query is the right one — groups, not the merged list.
		next, _ := fresh.Update(fresh.Init()())
		loaded := next.(Model)
		if loaded.err != nil {
			t.Fatalf("first query failed: %v", loaded.err)
		}
		if want && len(loaded.groups) == 0 {
			t.Error("opened in the all-tasks view but the first query returned no groups")
		}
		if !want && len(loaded.groups) != 0 {
			t.Errorf("opened in the merged list but the first query returned %d groups", len(loaded.groups))
		}
		// The frame proves it too: only the all-tasks view renders group headers.
		hasHeader := false
		for _, r := range loaded.rows {
			if r.kind == rowHeader {
				hasHeader = true
			}
		}
		if hasHeader != want {
			t.Errorf("AllTasks=%v: group headers present = %v, want %v", want, hasHeader, want)
		}
	}
}

// TestTUIDoesNotImportScope is DoD 8. The whole Config seam exists to keep this
// true, and this task added two more fields to it — so the constraint is worth
// asserting rather than trusting.
//
// It parses the package's own imports rather than shelling out to `go list`, so
// it works with no toolchain assumptions and names the offending file.
func TestTUIDoesNotImportScope(t *testing.T) {
	const forbidden = `"github.com/agusarias/tmux-todo/internal/scope"`

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	checked := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", e.Name(), err)
		}
		checked++
		for _, imp := range f.Imports {
			if imp.Path.Value == forbidden {
				t.Errorf("%s imports internal/scope; the popup must stay environment-blind — "+
					"preferences arrive through tui.Config", e.Name())
			}
		}
	}
	if checked == 0 {
		t.Fatal("no .go files were parsed, so this guard asserted nothing")
	}
	// The guard has to be able to fire: prove the matcher recognises an import
	// that IS present, or a typo in `forbidden` would make this pass forever.
	sentinel := `"github.com/agusarias/tmux-todo/internal/store"`
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, imp := range f.Imports {
			if imp.Path.Value == sentinel {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("the positive control failed: no file imports %s, so the matcher above proves nothing", sentinel)
	}
}

// quitCmdKinds is a compile-time reminder that quit()'s two paths are both
// covered above; tea.Quit is a func, so it cannot be compared directly.
var _ tea.Cmd = tea.Quit
