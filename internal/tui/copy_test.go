package tui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/agusarias/tmux-todo/internal/task"
)

// recorder is a fake Copy: it records every call and answers with err.
//
// It records the *argument*, not "was I called" — the whole risk in this
// feature is copying the wrong string (the rendered row, a neighbouring task in
// the all-tasks view's other coordinate system), and a call counter cannot see
// any of that.
type recorder struct {
	calls []string
	err   error
}

func (r *recorder) copy(s string) error {
	r.calls = append(r.calls, s)
	return r.err
}

// copyModel is a loaded model over the given tasks with a recording Copy. It
// also hands back the rows as the *store* holds them, which is what the
// verbatim assertions compare against — store.Add trims its input, so the text
// the popup has is not always the text the test typed.
func copyModel(t *testing.T, rec *recorder, texts ...string) (Model, []task.Task) {
	t.Helper()
	db := openDB(t)
	var added []task.Task
	for _, text := range texts {
		added = append(added, add(t, db, text, globalScope))
	}
	return newLoaded(t, Config{
		DB:      db,
		Scopes:  []task.Scope{sessionScope, dirScope, globalScope},
		Home:    "/Users/x",
		Version: "dev",
		Copy:    rec.copy,
	}), added
}

// TestCopyPassesTheTaskTextVerbatim is the payload assertion behind DoD 1: what
// reaches Copy is the stored text, byte for byte.
//
// The awkward texts are the point. A quote and a `$` are what a shell would
// have mangled, and this is the seam where a future "just shell out to pbcopy"
// would reintroduce that; a text with a newline proves the collapse that keeps
// the *notice* one row does not touch the payload.
func TestCopyPassesTheTaskTextVerbatim(t *testing.T) {
	for _, text := range []string{
		`it's a "$HOME" task`,
		"rebase onto main",
		"two\nlines",
		"trailing space ",
		`back\slash and 'quotes' and $(subshell)`,
	} {
		t.Run(text, func(t *testing.T) {
			rec := &recorder{}
			m, added := copyModel(t, rec, text)
			m = pressAndSettle(t, m, "y")

			if len(rec.calls) != 1 {
				t.Fatalf("Copy called %d times, want 1: %q", len(rec.calls), rec.calls)
			}
			// Against the stored text, not the typed one: store.Add trims, and
			// the popup can only copy what it was given. Pinning the typed
			// string here would be asserting the store does not trim, which is
			// a different (and false) claim.
			if want := added[0].Text; rec.calls[0] != want {
				t.Errorf("Copy got %q, want the stored text %q verbatim", rec.calls[0], want)
			}
			if !strings.Contains(m.notice, "copied") {
				t.Errorf("notice = %q, want a confirmation", m.notice)
			}
		})
	}
}

// TestCopyCopiesTheSelectedTaskNotTheFirst — the cursor is the input, and in the
// all-tasks view m.cursor and m.tasks are different coordinate systems. A model
// that indexed m.tasks[m.cursor] would pass with one task and copy the wrong one
// here, silently.
func TestCopyCopiesTheSelectedTaskNotTheFirst(t *testing.T) {
	rec := &recorder{}
	// Added oldest-first, so the list shows them newest-first: third, second, first.
	m, _ := copyModel(t, rec, "first", "second", "third")
	m = pressAndSettle(t, m, "j")
	m = pressAndSettle(t, m, "y")

	if len(rec.calls) != 1 {
		t.Fatalf("Copy called %d times, want 1", len(rec.calls))
	}
	if rec.calls[0] != "second" {
		t.Errorf("Copy got %q, want the task under the cursor, %q", rec.calls[0], "second")
	}
}

// TestCopyNoticeReplacesTheTitle is DoD 4's visible half: the confirmation is on
// screen, and it is on the title's row rather than a new one.
func TestCopyNoticeReplacesTheTitle(t *testing.T) {
	rec := &recorder{}
	m, _ := copyModel(t, rec, "rebase onto main")
	before := len(strings.Split(m.frame(), "\n"))

	m = pressAndSettle(t, m, "y")

	if got := m.titleLine(); !strings.Contains(got, "copied: rebase onto main") {
		t.Errorf("titleLine() = %q, want the copy confirmation", got)
	}
	if strings.Contains(m.titleLine(), "tdo") {
		t.Errorf("titleLine() = %q, want the notice to replace the title, not join it", m.titleLine())
	}
	if after := len(strings.Split(m.frame(), "\n")); after != before {
		t.Errorf("the frame went from %d rows to %d — the notice must cost no row", before, after)
	}
	// And the title comes back when the notice clears.
	m = pressAndSettle(t, m, "j")
	if got := m.titleLine(); !strings.Contains(got, "tdo") {
		t.Errorf("after the notice cleared titleLine() = %q, want the title back", got)
	}
}

// TestCopyNoticeIsOneRowAndTruncated is DoD 4's arithmetic half, at the level
// titleLine can be asked directly: a long or multi-line text must not become a
// second row, at any width.
//
// A newline is the case that truncation cannot save: width is clipped, height is
// not, and one extra row is what makes the terminal scroll the top of the list
// away.
func TestCopyNoticeIsOneRowAndTruncated(t *testing.T) {
	texts := []string{
		strings.Repeat("a very long task text ", 20),
		"first line\nsecond line\nthird line",
		"tabs\tand\nnewlines\tmixed",
	}
	for _, width := range []int{40, 48, 60, 72, 100} {
		for _, text := range texts {
			rec := &recorder{}
			m, _ := copyModel(t, rec, text)
			sized, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 20})
			m = sized.(Model)
			m = pressAndSettle(t, m, "y")

			line := m.titleLine()
			if strings.Contains(line, "\n") {
				t.Errorf("width %d: the notice is %d rows: %q",
					width, len(strings.Split(line, "\n")), line)
			}
			if w := lipgloss.Width(line); w > m.contentWidth() {
				t.Errorf("width %d: the notice is %d columns, budget is %d: %q",
					width, w, m.contentWidth(), line)
			}
		}
	}
}

// TestCopyNoticeClearsOnTheNextKeypress is DoD 5. The mechanism is a keypress,
// not a timer: no tea.Tick is involved, which is why this test needs no clock.
func TestCopyNoticeClearsOnTheNextKeypress(t *testing.T) {
	rec := &recorder{}
	m, _ := copyModel(t, rec, "rebase onto main")
	m = pressAndSettle(t, m, "y")
	if m.notice == "" {
		t.Fatal("no notice after y, so there is nothing to clear")
	}

	// Every one of these is a *different* kind of key — a movement, a no-op, a
	// mode change — because "cleared before dispatch" has to hold for all of
	// them, not just the one the happy path uses.
	for _, key := range []string{"j", "k", "z", "?", "1", "y"} {
		t.Run(key, func(t *testing.T) {
			after := pressAndSettle(t, m, key)
			if key == "y" {
				// y re-copies, so it sets a *fresh* notice rather than leaving
				// none. What matters is that it is not the stale one surviving
				// untouched, which the call count proves.
				if len(rec.calls) < 2 {
					t.Errorf("y did not re-copy: %d calls", len(rec.calls))
				}
				return
			}
			if after.notice != "" {
				t.Errorf("notice survived %q: %q", key, after.notice)
			}
		})
	}
}

// TestCopyNoticeSurvivesAReload is the other half of DoD 5, and the direction
// that is easy to get wrong: the notice is cleared by keypresses *only*.
//
// A concurrent pane adding a task triggers a reload, and a confirmation that
// vanished because of something the user did not do reads as "the copy did not
// take".
func TestCopyNoticeSurvivesAReload(t *testing.T) {
	rec := &recorder{}
	m, _ := copyModel(t, rec, "rebase onto main")
	m = pressAndSettle(t, m, "y")
	if m.notice == "" {
		t.Fatal("no notice after y")
	}

	// The message a reload delivers, exactly as the store's query would.
	next, _ := m.Update(rowsMsg{tasks: m.tasks, view: m.view})
	after := next.(Model)
	if after.notice == "" {
		t.Error("a reload cleared the notice; only a keypress may")
	}
}

// TestCopyFailureSaysSo is DoD 6: an error is reported as one, never as success.
func TestCopyFailureSaysSo(t *testing.T) {
	rec := &recorder{err: errors.New("exit status 1")}
	m, _ := copyModel(t, rec, "rebase onto main")
	m = pressAndSettle(t, m, "y")

	if !strings.Contains(m.notice, "copy failed") {
		t.Errorf("notice = %q, want it to report the failure", m.notice)
	}
	if strings.Contains(m.notice, "copied:") {
		t.Errorf("notice = %q, reports success for a copy that failed", m.notice)
	}
	if !strings.Contains(m.notice, "exit status 1") {
		t.Errorf("notice = %q, want the underlying error in it", m.notice)
	}
	if !m.noticeErr {
		t.Error("noticeErr is false for a failed copy, so it renders as a hint")
	}
	// Asserted on the style object, not on rendered output: a test process has
	// no colour profile, so lipgloss renders plain text and an assertion over
	// the escapes would pass whatever style was picked.
	if errStyle.GetForeground() == hintStyle.GetForeground() {
		t.Fatal("errStyle and hintStyle are indistinguishable, so this leg proves nothing")
	}
}

// TestCopyIsInertWithNothingSelected is DoD 7. "No call and no message" is
// stronger than "no crash": a notice with no copy behind it is a lie, and a call
// with no selection would be copying whatever the zero task holds — "".
func TestCopyIsInertWithNothingSelected(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		rec := &recorder{}
		m, _ := copyModel(t, rec)
		m = pressAndSettle(t, m, "y")

		if len(rec.calls) != 0 {
			t.Errorf("Copy called %d times on an empty list: %q", len(rec.calls), rec.calls)
		}
		if m.notice != "" {
			t.Errorf("notice = %q on an empty list, want none", m.notice)
		}
	})

	t.Run("group header", func(t *testing.T) {
		rec := &recorder{}
		m, _ := copyModel(t, rec, "rebase onto main")
		m = pressAndSettle(t, m, "g")
		// Park the cursor on a header. snapCursor moves it off one by design,
		// so this reaches past the model's own API on purpose — the guard being
		// tested is copySelected's, and it has to hold even if some future path
		// leaves the cursor there.
		header := -1
		for i, r := range m.rows {
			if r.kind == rowHeader {
				header = i
				break
			}
		}
		if header < 0 {
			t.Fatal("the all-tasks view produced no group header")
		}
		m.cursor = header

		next, cmd := m.Update(keyMsg("y"))
		if cmd != nil {
			t.Error("y on a group header produced a command; it must be inert")
		}
		if got := next.(Model).notice; got != "" {
			t.Errorf("notice = %q on a group header, want none", got)
		}
		if len(rec.calls) != 0 {
			t.Errorf("Copy called %d times with the cursor on a header: %q", len(rec.calls), rec.calls)
		}
	})

	t.Run("no Copy injected", func(t *testing.T) {
		db := openDB(t)
		add(t, db, "rebase onto main", globalScope)
		m := newLoaded(t, Config{
			DB:      db,
			Scopes:  []task.Scope{globalScope},
			Version: "dev",
			// Copy deliberately nil: `tdo tui` where no copy path applies.
		})
		next, cmd := m.Update(keyMsg("y"))
		if cmd != nil {
			t.Error("y produced a command with no Copy injected; it must be inert")
		}
		if got := next.(Model).notice; got != "" {
			t.Errorf("notice = %q with no Copy injected, want none", got)
		}
	})
}

// TestCopyKeyIsNormalModeOnly is DoD 8. Both directions are real bugs: a `y`
// that copied instead of typing would make the letter unusable in a task, and a
// `y` behind the overlay would act on a row the user cannot see.
func TestCopyKeyIsNormalModeOnly(t *testing.T) {
	t.Run("input row types it", func(t *testing.T) {
		rec := &recorder{}
		m, _ := copyModel(t, rec, "rebase onto main")
		m = pressAndSettle(t, m, "a")
		m = typeText(t, m, "yak yoghurt")

		if len(rec.calls) != 0 {
			t.Errorf("Copy called %d times while typing: %q", len(rec.calls), rec.calls)
		}
		if got := m.input.value(); got != "yak yoghurt" {
			t.Errorf("input = %q, want %q — y must be literal text in the input row", got, "yak yoghurt")
		}
	})

	t.Run("help overlay is inert", func(t *testing.T) {
		rec := &recorder{}
		m, _ := copyModel(t, rec, "rebase onto main")
		m = pressAndSettle(t, m, "?")
		if m.mode != modeHelp {
			t.Fatalf("mode = %v, want the help overlay", m.mode)
		}
		m = pressAndSettle(t, m, "y")

		if len(rec.calls) != 0 {
			t.Errorf("Copy called %d times behind the overlay: %q", len(rec.calls), rec.calls)
		}
		if m.notice != "" {
			t.Errorf("notice = %q from behind the overlay, want none", m.notice)
		}
		if m.mode != modeHelp {
			t.Errorf("mode = %v, want y to leave the overlay up", m.mode)
		}
	})
}

// TestHelpOverlayListsTheCopyKey is DoD 10.
//
// It asserts the key is in the *clipped* overlay at the design's own popup
// height, not merely in helpLines: helpBody trims to listHeight, so a hint that
// exists but is never on screen is not a hint.
func TestHelpOverlayListsTheCopyKey(t *testing.T) {
	for _, view := range []viewKind{viewMerged, viewAll} {
		lines := helpLines(view, "dev")
		if !strings.Contains(strings.Join(lines, "\n"), "y copy") {
			t.Errorf("view %v: the keymap does not mention y:\n%s", view, strings.Join(lines, "\n"))
		}
	}

	rec := &recorder{}
	m, _ := copyModel(t, rec, "rebase onto main")
	// The design's floor: a 60x15 popup, which is 54 content columns.
	sized, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 15})
	m = sized.(Model)
	m = pressAndSettle(t, m, "?")

	body := strings.Join(m.helpBody(), "\n")
	if !strings.Contains(body, "y copy") {
		t.Errorf("the overlay on screen at 60x15 does not mention y:\n%s", body)
	}
	// The version must still survive: the reason `y` shares a line rather than
	// taking its own is that helpBody clips from the bottom.
	if !strings.Contains(body, "dev") {
		t.Errorf("adding the copy hint pushed the version off the overlay:\n%s", body)
	}
}
