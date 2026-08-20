package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/agusarias/tmux-todo/internal/task"
)

// Scope glyphs, per docs/design.md's default-view mock.
//
// ⌘ (U+2318) and ◉ (U+25C9) are East-Asian *ambiguous* width: a terminal set to
// ambiguous-width=double renders them two cells wide and every column after
// them shifts. That is a known, accepted risk (see the task brief) — padding
// them with a width table was rejected in favour of matching the mock.
const (
	glyphSession = "⌘"
	glyphDir     = "·"
	glyphGlobal  = "◉"
)

// cursorMark prefixes the selected row. The design mock shows no cursor, but a
// list without one is unusable; a textual marker (rather than colour alone) is
// also what makes selection assertable in a headless test.
const (
	cursorMark = "▸ "
	cursorPad  = "  "
)

// ellipsis marks text truncated to fit the popup width. Task text is truncated
// rather than wrapped so one task is always one row.
const ellipsis = "…"

// labelGap separates a row's text column from its tier label.
const labelGap = "  "

// minTextWidth is the narrowest text column worth rendering; below it, labels
// stop being right-aligned rather than squeezing text away entirely.
const minTextWidth = 12

var (
	doneStyle     = lipgloss.NewStyle().Strikethrough(true).Faint(true)
	labelStyle    = lipgloss.NewStyle().Faint(true)
	selectedStyle = lipgloss.NewStyle().Bold(true)
)

// glyph returns the per-row scope marker for a kind.
func glyph(k task.ScopeKind) string {
	switch k {
	case task.ScopeSession:
		return glyphSession
	case task.ScopeDir:
		return glyphDir
	case task.ScopeGlobal:
		return glyphGlobal
	default:
		return "?"
	}
}

// scopeLabel renders the tier label shown on the first row of each scope:
// "(session: pulsar)", "(dir: ~/ws/pulsar)", "(global)". Dir keys are
// home-abbreviated for display only — the key in the database stays absolute.
func scopeLabel(s task.Scope, home string) string {
	switch s.Kind {
	case task.ScopeGlobal:
		return "(global)"
	case task.ScopeDir:
		return fmt.Sprintf("(dir: %s)", abbreviateHome(s.Key, home))
	default:
		return fmt.Sprintf("(%s: %s)", s.Kind, s.Key)
	}
}

// abbreviateHome rewrites a path under home as ~/…. It is a display transform
// and must never reach the store: scope keys are durable database keys, kept
// absolute and symlink-resolved (see internal/scope).
func abbreviateHome(path, home string) string {
	if home == "" || path == "" {
		return path
	}
	home = strings.TrimSuffix(home, "/")
	if path == home {
		return "~"
	}
	if strings.HasPrefix(path, home+"/") {
		return "~" + path[len(home):]
	}
	return path
}

// truncate shortens s to at most width display columns, ending in an ellipsis
// when anything was dropped.
func truncate(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for n := len(runes) - 1; n > 0; n-- {
		if candidate := string(runes[:n]) + ellipsis; lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return truncate(ellipsis, width)
}

// truncateLeft shortens s to at most width columns by dropping from the front,
// marking the cut with a leading ellipsis.
func truncateLeft(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	for n := 1; n < len(runes); n++ {
		if candidate := ellipsis + string(runes[n:]); lipgloss.Width(candidate) <= width {
			return candidate
		}
	}
	return truncate(ellipsis, width)
}

// renderOpts is everything renderRows needs beyond the tasks themselves. It is
// passed explicitly so row formatting stays a pure function of its inputs.
type renderOpts struct {
	Width  int    // total columns available to a row
	Home   string // for the ~ abbreviation; "" disables it
	Cursor int    // index of the selected row; out of range selects nothing
}

// renderRows formats tasks into one line each, **in the order given**.
//
// The order is the store's: List already returns scope tier then newest-first
// (see store.listOrder), so re-sorting here would be a second source of truth.
// This function only ever walks the slice.
func renderRows(tasks []task.Task, opts renderOpts) []string {
	if len(tasks) == 0 {
		return nil
	}

	labels := rowLabels(tasks, opts.Home)
	cols := columns(opts.Width, labels, taskTexts(tasks))

	rows := make([]string, 0, len(tasks))
	for i, t := range tasks {
		rows = append(rows, renderRow(t, labels[i], i == opts.Cursor, cols))
	}
	return rows
}

// taskTexts pulls the text out of each task, for the column budget.
func taskTexts(tasks []task.Task) []string {
	out := make([]string, len(tasks))
	for i, t := range tasks {
		out[i] = t.Text
	}
	return out
}

// rowLabels returns the tier label for each row, empty where the row is not the
// first of its scope. Detection is a comparison against the previous row, which
// relies on List's ordering keeping a scope's tasks contiguous.
func rowLabels(tasks []task.Task, home string) []string {
	labels := make([]string, len(tasks))
	for i, t := range tasks {
		if i > 0 && tasks[i-1].Scope == t.Scope {
			continue
		}
		labels[i] = scopeLabel(t.Scope, home)
	}
	return labels
}

// labelShare caps how much of a row a tier label may claim. A dir key is an
// absolute path and can be longer than the whole popup, so without a cap the
// label either squeezes the task text to nothing or is pushed off the edge.
const labelShare = 2

// layout is the column budget shared by every row: how wide the text column is,
// and how many columns labels get. Both are computed once per render so the
// labels line up in a column, as in docs/design.md's mock.
type layout struct {
	text  int
	label int
}

// columns splits the available width between task text and tier labels.
//
// Two invariants, both learned from a real capture rather than from a unit test:
//
//   - A label that exists must stay *visible*. Reserving only what happens to
//     be left over means a long dir path renders no label at all, because the
//     row overflows and the viewport clips the label away. The label is the only
//     thing naming the scope, so a truncated one beats none.
//   - Neither column starves the other. Labels take what they need when the text
//     does not want the room, and fall back to a fair split when it does.
func columns(width int, labels, texts []string) layout {
	gutter := lipgloss.Width(cursorMark) + lipgloss.Width(glyphSession) + 1
	avail := width - gutter
	if avail < minTextWidth {
		avail = minTextWidth
	}

	widest := widestOf(labels)
	if widest == 0 {
		return layout{text: avail}
	}
	gap := lipgloss.Width(labelGap)

	// What the labels may have: whatever the text does not need, but never less
	// than a fair share of the row just because one task has a long title.
	spare := avail - gap - widestOf(texts)
	if fair := avail / labelShare; spare < fair {
		spare = fair
	}
	label := min(widest, spare)

	// ...and never so much that the text column drops below readability.
	if room := avail - gap - minTextWidth; label > room {
		label = room
	}
	if label <= 0 {
		return layout{text: avail}
	}
	return layout{text: avail - label - gap, label: label}
}

// widestOf returns the widest display width in ss.
func widestOf(ss []string) int {
	var widest int
	for _, s := range ss {
		if w := lipgloss.Width(s); w > widest {
			widest = w
		}
	}
	return widest
}

// textStyle picks the styling for a row's task text: done tasks are struck
// through in place, so a completion stays legible and reversible where it
// happened rather than moving or vanishing. Done beats selected — the strike is
// information, the bold is only decoration.
//
// It returns a style instead of applying one so the choice is directly
// assertable. A test process has no colour profile, so lipgloss renders every
// style down to plain text; an assertion against rendered ANSI would pass no
// matter which style was chosen.
func textStyle(t task.Task, selected bool) lipgloss.Style {
	switch {
	case t.Done:
		return doneStyle
	case selected:
		return selectedStyle
	default:
		return lipgloss.NewStyle()
	}
}

// renderRow formats a single task line: cursor, glyph, text, tier label.
func renderRow(t task.Task, label string, selected bool, cols layout) string {
	textWidth := cols.text
	var b strings.Builder

	if selected {
		b.WriteString(cursorMark)
	} else {
		b.WriteString(cursorPad)
	}
	b.WriteString(glyph(t.Scope.Kind))
	b.WriteByte(' ')

	text := truncate(t.Text, textWidth)
	// Padding is measured on the plain text and written outside the styled
	// span: styling first would have the ANSI escapes count towards the width.
	pad := textWidth - lipgloss.Width(text)
	if pad < 0 {
		pad = 0
	}

	b.WriteString(textStyle(t, selected).Render(text))

	if label == "" || cols.label <= 0 {
		return strings.TrimRight(b.String(), " ")
	}
	// Truncated from the left: a dir label is a path, and the tail ("…/ws/p")
	// identifies it where the head ("/Users/…") does not.
	b.WriteString(strings.Repeat(" ", pad))
	b.WriteString(labelGap)
	b.WriteString(labelStyle.Render(truncateLeft(label, cols.label)))
	return b.String()
}

// Group header decoration for the all-tasks view, per docs/design.md's mock:
// "─ SESSION pulsar ─ (live)".
const (
	headerRule = "─"
	// headerGap separates a header's text from its right-aligned jump hint.
	headerGap = "  "
	// minHeaderText is the narrowest header text worth keeping. Below it the
	// jump hint is dropped instead of the scope: a header with no scope in it
	// names nothing, and the hint only says what Enter would do to it.
	minHeaderText = 12
)

// Jump hints. They live in the header, once per group, rather than on every row.
//
// Measured against the minimum contentWidth of 52 (docs/design.md's 60x15 floor
// leaves a 58-wide pane, less chromeWidth): the design's per-row
// "↵ switch there" is 14 columns and "↵ sesh connect <name>" is 18-35, i.e. 26%
// to 67% of every session row, repeated to say something identical for every row
// in the group. The header already carries the scope and the liveness label, so
// it is where "what Enter does" belongs, and the rows keep their full width for
// task text.
const (
	hintSwitch = "↵ switch"
	hintSesh   = "↵ sesh"
)

// Liveness labels. A session group with tasks that is not in Config.LiveSessions
// is "(not running)" — including the case where nothing is known to be running
// at all, which is what "absent beats empty" means here.
const (
	labelLive       = "(live)"
	labelNotRunning = "(not running)"
)

// renderGroupRows formats the all-tasks view: a header per scope, then its
// tasks at full width.
//
// Rows carry no glyph and no tier label, because the header above them carries
// the scope — that is the entire meaning of "wide" in docs/design.md. The frame
// itself does not change: display-popup cannot resize once open.
func renderGroupRows(rows []listRow, opts renderOpts) []string {
	if len(rows) == 0 {
		return nil
	}
	// The cursor mark is the rows' whole indent, which is what puts them under
	// their header the way the design's mock shows.
	textWidth := opts.Width - lipgloss.Width(cursorPad)
	if textWidth < 1 {
		textWidth = 1
	}

	out := make([]string, 0, len(rows))
	for i, r := range rows {
		if r.kind == rowHeader {
			out = append(out, renderHeader(r, opts.Home, opts.Width))
			continue
		}
		out = append(out, renderWideRow(r.task, i == opts.Cursor, textWidth))
	}
	return out
}

// renderWideRow is a task row with no scope column: cursor, text.
func renderWideRow(t task.Task, selected bool, width int) string {
	var b strings.Builder
	if selected {
		b.WriteString(cursorMark)
	} else {
		b.WriteString(cursorPad)
	}
	b.WriteString(textStyle(t, selected).Render(truncate(t.Text, width)))
	return b.String()
}

// renderHeader formats one group header, with the jump hint right-aligned.
//
// The hint wins its columns and the scope *key* left-truncates, which is the
// same rule columns() applies to tier labels and for the same reason: a row
// wider than the viewport is silently clipped, not wrapped, so a header sized
// from "whatever is left" renders no hint at all for a long session name. The
// key truncates from the left because its tail is what identifies it.
func renderHeader(r listRow, home string, width int) string {
	if width <= 0 {
		return ""
	}
	hint := headerHint(r)
	textRoom := width
	if hint != "" {
		room := width - lipgloss.Width(hint) - lipgloss.Width(headerGap)
		if room >= minHeaderText {
			textRoom = room
		} else {
			hint = ""
		}
	}

	text := headerText(r, home, textRoom)
	if hint == "" {
		return labelStyle.Render(text)
	}
	// Padding measured on the plain text and written outside the styled spans:
	// styling first would have the ANSI escapes count towards the width.
	pad := textRoom - lipgloss.Width(text)
	if pad < 0 {
		pad = 0
	}
	return labelStyle.Render(text) + strings.Repeat(" ", pad) + headerGap + hintStyle.Render(hint)
}

// headerHint is what Enter would do to this group, or "" where Enter does
// nothing: a dir or global group is not a place you can switch to.
func headerHint(r listRow) string {
	if r.group.Scope.Kind != task.ScopeSession {
		return ""
	}
	if r.live {
		return hintSwitch
	}
	return hintSesh
}

// headerText is the header's left side: the rule, the tier, the key, and the
// liveness label for a session.
func headerText(r listRow, home string, room int) string {
	s := r.group.Scope
	tier := strings.ToUpper(string(s.Kind))
	var live string
	if s.Kind == task.ScopeSession {
		live = " " + labelNotRunning
		if r.live {
			live = " " + labelLive
		}
	}
	key := s.Key
	if s.Kind == task.ScopeDir {
		key = abbreviateHome(key, home)
	}

	// Without the key: "─ GLOBAL ─", and the fallback when the key has no room.
	bare := headerRule + " " + tier + " " + headerRule + live
	if key == "" {
		return truncate(bare, room)
	}
	keyRoom := room - lipgloss.Width(bare) - 1
	if keyRoom < 1 {
		return truncate(bare, room)
	}
	return truncate(headerRule+" "+tier+" "+truncateLeft(key, keyRoom)+" "+headerRule+live, room)
}

// helpLines is the keymap overlay's content: every key this build binds, plus
// the running version.
//
// It exists because the footer cannot hold this. Eleven keys and a
// `git describe` version stamp come to well over the ~42 columns a 60%x60%
// popup on an 80-column terminal has, and the footer's truncation would have
// dropped the last keys silently — which is exactly what docs/design.md's own
// footer mock specified. Every line here is kept under 42 columns so the overlay
// renders whole at the same size; the caller still truncates, but at that width
// it has nothing to do.
//
// A pure function of the version so its width is directly assertable, in the
// same spirit as the rest of this file.
func helpLines(view viewKind, version string) []string {
	// Per view, so the overlay describes the list you are actually looking at.
	// The keys are the same set; what differs is that Enter, r and D only mean
	// something where there are groups, and Tab only means something where the
	// scope has not already been chosen by one.
	lines := []string{
		"j/k move · space done · q quit",
		"a add · e edit · s re-scope",
		"d delete · u undo (until close)",
	}
	if view == viewAll {
		lines = append(lines,
			"enter jump · r re-home · D del group",
			"1/2/3 filter tier · g merged view",
		)
	} else {
		lines = append(lines,
			"1/2/3 filter tier · tab scope (add)",
			"g all tasks (every scope)",
		)
	}
	lines = append(lines, "? or esc closes this")
	if version != "" {
		lines = append(lines, "v"+version)
	}
	return lines
}
