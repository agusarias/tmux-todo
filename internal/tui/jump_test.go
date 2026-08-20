package tui

import (
	"go/parser"
	"go/token"
	"io/fs"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/agusarias/tmux-todo/internal/task"
)

// jumpFrom presses Enter on the row at index and returns the resulting model
// plus the command Enter produced.
func jumpFrom(t *testing.T, m Model, cursor int) (Model, tea.Cmd) {
	t.Helper()
	m.cursor = cursor
	m.clampCursor()
	return press(t, m, "enter")
}

// TestEnterOnALiveSessionAsksToSwitch is DoD 8, and TestEnterOnADeadSession... is
// DoD 9's model half: the model records *which* session and *whether it was
// live*, and stops there. Choosing between switch-client and sesh is
// internal/cli's job, tested in internal/cli/jump_test.go.
func TestEnterRecordsTheSessionAndItsLiveness(t *testing.T) {
	db := openDB(t)
	add(t, db, "fix flaky test", deadSession)
	add(t, db, "rebase onto main", sessionScope)
	m := openAll(t, allTasksConfig(db))

	for _, tc := range []struct {
		text     string
		wantJump Jump
	}{
		{"fix flaky test", Jump{Session: "api", Live: false}},
		{"rebase onto main", Jump{Session: "pulsar", Live: true}},
	} {
		at := rowOf(t, m, tc.text)
		jumped, cmd := jumpFrom(t, m, at)
		if jumped.jump != tc.wantJump {
			t.Errorf("Enter on %q recorded %+v, want %+v", tc.text, jumped.jump, tc.wantJump)
		}
		if !jumped.quitting {
			t.Errorf("Enter on %q did not start the exit; the jump is the one action that closes the popup", tc.text)
		}
		if cmd == nil {
			t.Errorf("Enter on %q produced no command, want the quit", tc.text)
		}
	}
}

// TestEnterOnDirOrGlobalIsANoOp is DoD 10: no jump, no close, no error. There is
// nothing to switch to.
func TestEnterOnDirOrGlobalIsANoOp(t *testing.T) {
	db := openDB(t)
	add(t, db, "update README", dirScope)
	add(t, db, "call the dentist", globalScope)
	m := openAll(t, allTasksConfig(db))

	for _, text := range []string{"update README", "call the dentist"} {
		next, cmd := jumpFrom(t, m, rowOf(t, m, text))
		if next.jump != (Jump{}) {
			t.Errorf("Enter on %q recorded a jump: %+v", text, next.jump)
		}
		if next.quitting {
			t.Errorf("Enter on %q closed the popup", text)
		}
		if cmd != nil {
			t.Errorf("Enter on %q produced a command, want none", text)
		}
	}
}

// TestEnterOnAHeaderCannotHappen — the cursor can never be on a header, so this
// asserts the guard rather than the key: a jump built off cursorGroup instead of
// the selected task would jump from a header, and DoD 6 says the cursor never
// gets there.
func TestEnterNeedsATaskRowNotAGroup(t *testing.T) {
	db := openDB(t)
	add(t, db, "fix flaky test", deadSession)
	m := openAll(t, allTasksConfig(db))

	// Force the cursor onto the header, which normal key handling never does.
	m.cursor = 0
	next, _ := press(t, m, "enter")
	if next.jump != (Jump{}) {
		t.Errorf("Enter with the cursor on a header recorded %+v, want no jump", next.jump)
	}
}

// TestEnterInTheMergedViewIsInert — the merged view has no groups and no jump;
// docs/design.md gives the close-the-popup power to the all-tasks view alone.
func TestEnterInTheMergedViewIsInert(t *testing.T) {
	db := openDB(t)
	add(t, db, "rebase onto main", sessionScope)
	m := newLoaded(t, allTasksConfig(db))

	next, cmd := press(t, m, "enter")
	if next.jump != (Jump{}) || next.quitting || cmd != nil {
		t.Errorf("Enter in the merged view jumped (%+v), quit (%v) or issued %v", next.jump, next.quitting, cmd != nil)
	}
}

// TestQueuedDeletesCommitBeforeTheJump is DoD 12. The jump closes the popup, so
// a jump that raced the commit would silently discard the user's deletes.
//
// The assertion is on the *database*, after the real event loop has run: queue a
// delete, jump, and the row must be gone by the time Run hands the jump back.
func TestQueuedDeletesCommitBeforeTheJump(t *testing.T) {
	db := openDB(t)
	doomed := add(t, db, "delete me", deadSession)
	add(t, db, "keep me", deadSession)
	m := openAll(t, allTasksConfig(db))

	m.cursor = rowOf(t, m, doomed.Text)
	m = pressAndSettle(t, m, "d")
	if len(m.queued) != 1 {
		t.Fatalf("queue = %v, want the one row", m.queued)
	}
	// Still in the database: `d` queues, it does not write.
	if got := listAll(t, db); len(got) != 2 {
		t.Fatalf("store holds %v before the jump, want both rows", texts(got))
	}

	jump, err := run(m,
		tea.WithInput(strings.NewReader("\r")),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if jump.Session != "api" {
		t.Fatalf("Run returned jump %+v, want the api session", jump)
	}
	// The whole point: by the time internal/cli has the jump, the commit is done.
	got := listAll(t, db)
	if len(got) != 1 || got[0].Text != "keep me" {
		t.Errorf("store holds %v after the jump, want the queued row committed", texts(got))
	}
}

// TestRunReturnsNoJumpOnAnOrdinaryExit — the zero Jump is what every exit other
// than Enter must produce, or internal/cli would switch sessions on `q`.
func TestRunReturnsNoJumpOnAnOrdinaryExit(t *testing.T) {
	db := openDB(t)
	add(t, db, "rebase onto main", sessionScope)
	m := openAll(t, allTasksConfig(db))

	jump, err := run(m,
		tea.WithInput(strings.NewReader("q")),
		tea.WithoutRenderer(),
		tea.WithoutSignalHandler(),
	)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if jump != (Jump{}) {
		t.Errorf("q returned jump %+v, want none", jump)
	}
}

// TestDoesNotImportExec is DoD 11's structural half: the jump leaves as an
// intent, so this package must have no way to run a subprocess at all. A
// reviewer cannot check that by reading; the compiler can.
//
// os/exec is the obvious one; syscall is here because "just shell out from the
// TUI" reaches for whatever is at hand, and the boundary this guards is
// "internal/tui discovers nothing about its environment".
func TestDoesNotImportExec(t *testing.T) {
	forbidden := map[string]bool{
		"os/exec": true,
		"syscall": true,
	}

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse internal/tui: %v", err)
	}

	var files int
	for _, p := range pkgs {
		for name, f := range p.Files {
			files++
			for _, imp := range f.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				if forbidden[path] {
					t.Errorf("%s imports %q; the jump must leave as a tui.Jump for internal/cli to run", name, path)
				}
			}
		}
	}
	// Guard against the walk silently finding nothing, which is how this kind
	// of test passes while covering zero files.
	if files < 5 {
		t.Fatalf("parsed only %d files in internal/tui; the walk is broken, not the boundary", files)
	}
}

// rowOf is the row index of the task with this text, failing if it is absent.
func rowOf(t *testing.T, m Model, text string) int {
	t.Helper()
	for i, r := range m.rows {
		if r.kind == rowTask && r.task.Text == text {
			return i
		}
	}
	t.Fatalf("no row for %q in %v", text, texts(m.tasks))
	return 0
}

// TestGroupHeadersCarryTheirScope keeps headerText honest about the three tiers,
// including the home abbreviation being display-only.
func TestGroupHeaderText(t *testing.T) {
	for _, tc := range []struct {
		name  string
		row   listRow
		wants []string
	}{
		{"live session", listRow{group: groupOf(sessionScope), live: true}, []string{"SESSION", "pulsar", labelLive}},
		{"dead session", listRow{group: groupOf(deadSession)}, []string{"SESSION", "api", labelNotRunning}},
		{"dir", listRow{group: groupOf(dirScope)}, []string{"DIR", "~/ws/pulsar"}},
		{"global", listRow{group: groupOf(globalScope)}, []string{"GLOBAL"}},
	} {
		got := headerText(tc.row, "/Users/x", 52)
		for _, want := range tc.wants {
			if !strings.Contains(got, want) {
				t.Errorf("%s header %q is missing %q", tc.name, got, want)
			}
		}
		if tc.row.group.Scope.Kind != task.ScopeSession {
			if strings.Contains(got, labelLive) || strings.Contains(got, labelNotRunning) {
				t.Errorf("%s header %q carries a liveness label; only a session can be running", tc.name, got)
			}
		}
	}

	// The absolute key is what the store holds; only the display abbreviates.
	if got := headerText(listRow{group: groupOf(dirScope)}, "", 52); !strings.Contains(got, dirScope.Key) {
		t.Errorf("with no home, the dir header %q should show the absolute key", got)
	}
}
