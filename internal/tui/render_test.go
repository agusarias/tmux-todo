package tui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/agusarias/tmux-todo/internal/task"
)

func row(text string, scope task.Scope) task.Task {
	return task.Task{Text: text, Scope: scope}
}

func TestGlyphPerScope(t *testing.T) {
	for _, tc := range []struct {
		kind task.ScopeKind
		want string
	}{
		{task.ScopeSession, "⌘"},
		{task.ScopeDir, "·"},
		{task.ScopeGlobal, "◉"},
	} {
		if got := glyph(tc.kind); got != tc.want {
			t.Errorf("glyph(%q) = %q, want %q", tc.kind, got, tc.want)
		}
	}
	if got := glyph(task.ScopeKind("nonsense")); got == "" {
		t.Error("an unknown kind rendered no glyph; a row with no marker is worse than a wrong one")
	}
}

// TestEveryRowHasItsGlyph — the glyph is per-row, unlike the tier label.
func TestEveryRowHasItsGlyph(t *testing.T) {
	tasks := []task.Task{
		row("s1", sessionScope),
		row("s2", sessionScope),
		row("d1", dirScope),
		row("g1", globalScope),
	}
	rows := renderRows(tasks, renderOpts{Width: 80, Home: "/Users/x", Cursor: -1})

	for i, r := range rows {
		want := glyph(tasks[i].Scope.Kind)
		if !strings.Contains(r, want) {
			t.Errorf("row %d (%q) has no %q glyph: %q", i, tasks[i].Text, want, r)
		}
	}
}

// TestTierLabelOnFirstRowOfEachTierOnly — per docs/design.md's mock, the label
// names the scope once and the glyph carries every row after it.
func TestTierLabelOnFirstRowOfEachTierOnly(t *testing.T) {
	tasks := []task.Task{
		row("rebase onto main", sessionScope),
		row("check CI", sessionScope),
		row("fix auth redirect", dirScope),
		row("write migration", dirScope),
		row("call the dentist", globalScope),
	}
	rows := renderRows(tasks, renderOpts{Width: 80, Home: "/Users/x", Cursor: -1})

	labelled := map[int]string{
		0: "(session: pulsar)",
		2: "(dir: ~/ws/pulsar)",
		4: "(global)",
	}
	for i, r := range rows {
		want, expected := labelled[i]
		switch {
		case expected && !strings.Contains(r, want):
			t.Errorf("row %d should carry %q: %q", i, want, r)
		case !expected && strings.Contains(r, "("):
			t.Errorf("row %d should carry no tier label: %q", i, r)
		}
	}
}

// TestLabelRepeatsForANewScopeInTheSameTier — two dir scopes are two different
// lists; reusing the first one's label would attribute tasks to the wrong repo.
func TestLabelRepeatsForANewScopeInTheSameTier(t *testing.T) {
	other := task.Scope{Kind: task.ScopeDir, Key: "/Users/x/ws/other"}
	rows := renderRows([]task.Task{
		row("a", dirScope),
		row("b", other),
	}, renderOpts{Width: 80, Home: "/Users/x", Cursor: -1})

	if !strings.Contains(rows[1], "~/ws/other") {
		t.Errorf("second dir scope lost its label: %q", rows[1])
	}
}

func TestScopeLabelFormats(t *testing.T) {
	for _, tc := range []struct {
		scope task.Scope
		home  string
		want  string
	}{
		{sessionScope, "/Users/x", "(session: pulsar)"},
		{dirScope, "/Users/x", "(dir: ~/ws/pulsar)"},
		{dirScope, "", "(dir: /Users/x/ws/pulsar)"},
		{globalScope, "/Users/x", "(global)"},
	} {
		if got := scopeLabel(tc.scope, tc.home); got != tc.want {
			t.Errorf("scopeLabel(%v, %q) = %q, want %q", tc.scope, tc.home, got, tc.want)
		}
	}
}

// TestAbbreviateHomeIsDisplayOnly — the ~ form exists for the popup only. Scope
// keys in the database stay absolute and symlink-resolved (internal/scope), so
// this transform must never be reversed into a query.
func TestAbbreviateHomeIsDisplayOnly(t *testing.T) {
	for _, tc := range []struct {
		path, home, want string
	}{
		{"/Users/x/ws/p", "/Users/x", "~/ws/p"},
		{"/Users/x", "/Users/x", "~"},
		{"/Users/x/", "/Users/x", "~/"},
		{"/Users/x/ws/p", "/Users/x/", "~/ws/p"},
		// Not under home: left alone.
		{"/opt/src/p", "/Users/x", "/opt/src/p"},
		// A sibling whose name merely starts with home's must not be rewritten.
		{"/Users/xavier/ws/p", "/Users/x", "/Users/xavier/ws/p"},
		// No home known: absolute paths, not a bare ~.
		{"/Users/x/ws/p", "", "/Users/x/ws/p"},
		{"", "/Users/x", ""},
	} {
		if got := abbreviateHome(tc.path, tc.home); got != tc.want {
			t.Errorf("abbreviateHome(%q, %q) = %q, want %q", tc.path, tc.home, got, tc.want)
		}
	}
}

// TestTruncateFitsWidth — long task text is truncated, never wrapped: one task
// must stay one row or the glyph column stops meaning anything.
func TestTruncateFitsWidth(t *testing.T) {
	const long = "rebase the whole stack onto main and then rerun the integration suite"

	for _, width := range []int{1, 2, 5, 12, 30, len(long) - 1} {
		got := truncate(long, width)
		if w := lipgloss.Width(got); w > width {
			t.Errorf("truncate(width=%d) produced %d columns: %q", width, w, got)
		}
		if !strings.HasSuffix(got, ellipsis) {
			t.Errorf("truncate(width=%d) = %q, want an ellipsis to mark the cut", width, got)
		}
	}

	if got := truncate(long, len(long)); got != long {
		t.Errorf("text that fits was altered: %q", got)
	}
	if got := truncate(long, 500); got != long {
		t.Errorf("text well under the width was altered: %q", got)
	}
	if got := truncate("abc", 0); got != "" {
		t.Errorf("truncate to zero width = %q, want empty", got)
	}
}

func TestRenderRowTruncatesToWidth(t *testing.T) {
	long := strings.Repeat("very long task text ", 20)
	rows := renderRows([]task.Task{row(long, globalScope)}, renderOpts{Width: 40, Cursor: -1})

	if len(rows) != 1 {
		t.Fatalf("rendered %d rows for one task, want 1 — text was wrapped", len(rows))
	}
	if strings.Contains(rows[0], "\n") {
		t.Errorf("row contains a newline; text was wrapped rather than truncated: %q", rows[0])
	}
	if !strings.Contains(rows[0], ellipsis) {
		t.Errorf("over-long row was not truncated: %q", rows[0])
	}
}

// TestNarrowWidthStillRenders — a popup can be sized absurdly small; that must
// degrade, not panic or blank the list.
func TestNarrowWidthStillRenders(t *testing.T) {
	tasks := []task.Task{row("fix auth redirect", dirScope), row("call the dentist", globalScope)}
	for _, width := range []int{0, 1, 4, 10, 20} {
		rows := renderRows(tasks, renderOpts{Width: width, Home: "/Users/x", Cursor: 0})
		if len(rows) != len(tasks) {
			t.Fatalf("width %d rendered %d rows, want %d", width, len(rows), len(tasks))
		}
		for i, r := range rows {
			if strings.TrimSpace(r) == "" {
				t.Errorf("width %d row %d rendered blank", width, i)
			}
		}
	}
}

// TestDoneRowsAreStruckThroughInPlace — a completed task stays where it was in
// the order, marked rather than moved or hidden.
func TestDoneRowsAreStruckThroughInPlace(t *testing.T) {
	pending := row("still to do", globalScope)
	done := row("already finished", globalScope)
	done.Done = true

	rows := renderRows([]task.Task{pending, done}, renderOpts{Width: 60, Cursor: -1})

	if !strings.Contains(rows[1], done.Text) {
		t.Fatalf("done row lost its text: %q", rows[1])
	}
	// Order is untouched: the done task stays second, where List put it.
	if !strings.Contains(rows[0], pending.Text) {
		t.Errorf("completing a task reordered the list: %q", rows[0])
	}

	// Asserted on the chosen style, not on rendered output: a test process has
	// no colour profile, so lipgloss renders every style to plain text and
	// comparing rendered strings would pass whatever style was picked.
	if !textStyle(done, false).GetStrikethrough() {
		t.Error("done task is not styled strikethrough")
	}
	if textStyle(pending, false).GetStrikethrough() {
		t.Error("pending task is styled strikethrough")
	}
	if !textStyle(done, true).GetStrikethrough() {
		t.Error("a selected done task lost its strikethrough; the strike is information, the cursor styling is not")
	}
}

// TestCursorMarksTheSelectedRow — selection is shown with a text marker, not
// colour alone, so it survives a terminal with no colour profile.
func TestCursorMarksTheSelectedRow(t *testing.T) {
	tasks := []task.Task{row("one", globalScope), row("two", globalScope), row("three", globalScope)}

	rows := renderRows(tasks, renderOpts{Width: 60, Cursor: 1})
	if !strings.HasPrefix(rows[1], cursorMark) {
		t.Errorf("selected row lacks the cursor mark: %q", rows[1])
	}
	for _, i := range []int{0, 2} {
		if strings.HasPrefix(rows[i], cursorMark) {
			t.Errorf("unselected row %d carries the cursor mark: %q", i, rows[i])
		}
	}

	// The glyph column must not shift as the cursor moves, so the cursor mark
	// and the blank that stands in for it are the same width.
	gutter := lipgloss.Width(cursorMark)
	if pad := lipgloss.Width(cursorPad); gutter != pad {
		t.Errorf("cursor mark is %d columns and its padding %d; the list shifts as the cursor moves", gutter, pad)
	}
	for i, r := range rows {
		if col := lipgloss.Width(r[:strings.Index(r, glyphGlobal)]); col != gutter {
			t.Errorf("row %d has its glyph at column %d, want %d", i, col, gutter)
		}
	}

	// An out-of-range cursor selects nothing rather than panicking.
	for _, cursor := range []int{-1, len(tasks), 99} {
		for i, r := range renderRows(tasks, renderOpts{Width: 60, Cursor: cursor}) {
			if strings.HasPrefix(r, cursorMark) {
				t.Errorf("cursor %d marked row %d: %q", cursor, i, r)
			}
		}
	}
}

// TestLabelsRightAlign — with room to spare the labels line up in a column, as
// in the design mock.
func TestLabelsRightAlign(t *testing.T) {
	rows := renderRows([]task.Task{
		row("short", sessionScope),
		row("a considerably longer task text", dirScope),
	}, renderOpts{Width: 80, Home: "/Users/x", Cursor: -1})

	// Measured in display columns, not byte offsets: the glyphs are multi-byte
	// and of differing byte length, so strings.Index would compare nothing.
	col := func(r, label string) int {
		i := strings.Index(r, label)
		if i < 0 {
			return -1
		}
		return lipgloss.Width(r[:i])
	}
	first := col(rows[0], "(session")
	second := col(rows[1], "(dir")
	if first < 0 || second < 0 {
		t.Fatalf("labels missing: %q / %q", rows[0], rows[1])
	}
	if first != second {
		t.Errorf("labels start at columns %d and %d, want one column:\n%s\n%s", first, second, rows[0], rows[1])
	}
}

func TestRenderRowsEmptyInput(t *testing.T) {
	if rows := renderRows(nil, renderOpts{Width: 60, Cursor: 0}); rows != nil {
		t.Errorf("renderRows(nil) = %q, want nil", rows)
	}
}

// TestLabelSurvivesALongDirKey is a regression test for a defect a headless test
// with tidy fixtures could not see: a dir scope key is an absolute path, and
// when the label was given only the width left over after the text, a long path
// produced *no label at all* — the row was rendered, then the label ran past the
// viewport edge and was clipped away. The label is the only thing naming the
// scope, so it has to survive.
func TestLabelSurvivesALongDirKey(t *testing.T) {
	long := task.Scope{
		Kind: task.ScopeDir,
		Key:  "/private/tmp/claude-501/-Users-someone-workspace-todo/deadbeef-cafe/scratchpad/xdg/repo",
	}
	tasks := []task.Task{row("fix auth redirect", long), row("write migration", long)}

	for _, width := range []int{40, 60, 74, 80, 120} {
		rows := renderRows(tasks, renderOpts{Width: width, Cursor: -1})

		// Left truncation drops the "(dir: " head, so the tail is what proves
		// the label is still there — the glyph already says which tier it is.
		if !strings.Contains(rows[0], "xdg/repo)") {
			t.Errorf("width %d dropped the dir label entirely: %q", width, rows[0])
		}
		// And the row still fits, so the viewport cannot clip the label off.
		if w := lipgloss.Width(rows[0]); w > width {
			t.Errorf("width %d produced a %d-column row: %q", width, w, rows[0])
		}
		// The tail of the path is what identifies it, so that is what is kept.
		if !strings.Contains(rows[0], "repo") {
			t.Errorf("width %d truncated the label from the wrong end: %q", width, rows[0])
		}
	}
}

// TestRowsNeverExceedTheirWidth — a row wider than the viewport is silently
// clipped by it, which is how the label defect above hid. Hold every row to the
// budget it was given.
func TestRowsNeverExceedTheirWidth(t *testing.T) {
	tasks := []task.Task{
		row("a short one", sessionScope),
		row(strings.Repeat("long ", 40), dirScope),
		row("call the dentist", globalScope),
	}
	for _, width := range []int{20, 40, 74, 80, 200} {
		for i, r := range renderRows(tasks, renderOpts{Width: width, Home: "/Users/x", Cursor: 0}) {
			if w := lipgloss.Width(r); w > width {
				t.Errorf("width %d row %d is %d columns: %q", width, i, w, r)
			}
		}
	}
}

func TestTruncateLeftKeepsTheTail(t *testing.T) {
	for _, tc := range []struct {
		in    string
		width int
		want  string
	}{
		{"(dir: ~/ws/pulsar)", 40, "(dir: ~/ws/pulsar)"},
		{"(dir: ~/ws/pulsar)", 18, "(dir: ~/ws/pulsar)"},
		{"(dir: ~/ws/pulsar)", 10, "…s/pulsar)"},
		{"(global)", 0, ""},
	} {
		got := truncateLeft(tc.in, tc.width)
		if got != tc.want {
			t.Errorf("truncateLeft(%q, %d) = %q, want %q", tc.in, tc.width, got, tc.want)
		}
		if w := lipgloss.Width(got); w > tc.width {
			t.Errorf("truncateLeft(%q, %d) = %q, %d columns", tc.in, tc.width, got, w)
		}
	}
}

// TestColumnsKeepTextReadable — labels are capped so an absurd one cannot
// squeeze the task text down to nothing.
func TestColumnsKeepTextReadable(t *testing.T) {
	labels := []string{"(dir: " + strings.Repeat("x", 200) + ")"}
	for _, width := range []int{30, 50, 80, 200} {
		cols := columns(width, labels, []string{"a task"})
		if cols.text < minTextWidth {
			t.Errorf("width %d left only %d columns for text, want at least %d", width, cols.text, minTextWidth)
		}
		if cols.label <= 0 {
			t.Errorf("width %d reserved no label column at all", width)
		}
	}
}
