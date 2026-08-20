package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// emptyEditHint is what a rejected empty edit says. An add with empty text
// cancels — nothing exists yet, so there is nothing to lose — but an edit is
// looking at a task on screen, and silently blanking text the user can see is
// destroying content.
const emptyEditHint = "text cannot be empty — esc to cancel"

// startAdd opens the input row above the list, on the sticky default scope.
func (m Model) startAdd() (tea.Model, tea.Cmd) {
	if m.view == viewAll {
		// The all-tasks view has an unambiguous answer already: the group the
		// cursor is in. See startAddInGroup.
		return m.startAddInGroup()
	}
	cycle := m.scopeCycle()
	if len(cycle) == 0 {
		// No scope to file a task under: adding could only fail.
		return m, nil
	}
	m.mode = modeInput
	m.inputKind = inputAdd
	m.inputTarget = 0
	m.inputScope = m.seedScope(cycle)
	m.inputPlace = task.Scope{}
	m.inputHint = ""
	m.input.reset()
	m.refreshViewport()
	return m, nil
}

// startEdit replaces the cursor row with the input row, pre-filled.
func (m Model) startEdit() (tea.Model, tea.Cmd) {
	target, ok := m.selectedTask()
	if !ok {
		return m, nil
	}
	m.mode = modeInput
	m.inputKind = inputEdit
	m.inputTarget = target.ID
	m.inputScope = target.Scope.Kind
	m.inputPlace = task.Scope{}
	m.inputHint = ""
	m.input.setValue(target.Text)
	m.refreshViewport()
	return m, nil
}

// seedScope picks the scope a new task starts on: the injected sticky default
// when it is usable here, otherwise the first available tier.
//
// Degrading rather than trusting the stored value is the same rule internal/scope
// applies to a stale preference — and it is what lets DoD 2 promise that Enter
// can never fail on scope grounds.
func (m Model) seedScope(cycle []task.ScopeKind) task.ScopeKind {
	for _, kind := range cycle {
		if kind == m.cfg.DefaultScope {
			return kind
		}
	}
	return cycle[0]
}

// updateInput handles keys while the input row is open.
//
// Tab, Enter and Esc are intercepted *before* the message reaches the text
// field, or Tab would insert a tab character instead of cycling the scope.
// Everything else — including q, j, k, d, space, u, ? and the digits — is
// literal text, which is the whole reason Update dispatches on the mode.
func (m Model) updateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "ctrl+c":
		m.closeInput()
		m.refreshViewport()
		return m, nil

	case "tab":
		// Inert while editing: `e` is purely textual and scope changes go
		// through `s`, so one key never does two jobs. Inert in the all-tasks
		// view too, where the group header already answered the scope question.
		if m.inputKind == inputAdd && m.inputPlace == (task.Scope{}) {
			m.inputScope = m.nextScope(m.inputScope)
			m.inputHint = ""
			m.refreshViewport()
		}
		return m, nil

	case "enter":
		return m.submitInput()
	}

	m.input.handleKey(msg)
	// Typing answers the complaint.
	if m.inputHint != "" && strings.TrimSpace(m.input.value()) != "" {
		m.inputHint = ""
	}
	m.refreshViewport()
	return m, nil
}

// submitInput saves the row, or refuses to.
func (m Model) submitInput() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.value())

	if m.inputKind == inputEdit {
		if text == "" {
			m.inputHint = emptyEditHint
			m.refreshViewport()
			return m, nil
		}
		target := m.inputTarget
		m.closeInput()
		m.refreshViewport()
		return m, m.writeThenReload(target, func(ctx context.Context, db *store.DB) error {
			return db.UpdateText(ctx, target, text)
		})
	}

	if text == "" {
		// Nothing exists yet, so closing the row loses nothing — and "I changed
		// my mind" is the common way an empty add row ends.
		m.closeInput()
		m.refreshViewport()
		return m, nil
	}
	scope, remember, ok := m.addTarget()
	if !ok {
		m.closeInput()
		m.refreshViewport()
		return m, nil
	}
	m.closeInput()
	m.refreshViewport()
	return m, m.addCmd(text, scope, remember)
}

// addTarget is where a submitted add goes, and whether it should move the
// sticky default.
//
// The two travel together because they are the same decision seen twice: an add
// the *user* aimed with Tab is a statement about where the next one should go,
// while an add aimed by the all-tasks view's group header is a placement inside
// a list — it says nothing about the next add, and letting it redirect one would
// be action at a distance from a keypress that never mentioned preferences.
func (m Model) addTarget() (scope task.Scope, remember bool, ok bool) {
	if m.inputPlace != (task.Scope{}) {
		return m.inputPlace, false, true
	}
	scope, ok = m.scopeFor(m.inputScope)
	return scope, ok, ok
}

// addCmd inserts the task, remembers its scope as the sticky default, and
// reloads with the cursor anchored to the new row.
//
// The sticky write is deliberately after the insert and its error deliberately
// dropped: the task is already saved by then, so failing the add over a
// preference file would trade the user's data for their convenience. internal/scope
// treats a corrupt sticky file the same way — silently, falling back.
func (m Model) addCmd(text string, scope task.Scope, remember bool) tea.Cmd {
	setSticky := m.cfg.SetSticky
	return m.query(0, func(ctx context.Context, db *store.DB) (int64, error) {
		added, err := db.Add(ctx, text, scope)
		if err != nil {
			return 0, err
		}
		if setSticky != nil && remember {
			_ = setSticky(scope.Kind)
		}
		return added.ID, nil
	})
}

// rescopeSelected moves the cursor row to the next available scope.
//
// It does not touch the sticky default: re-scoping an existing task is a
// correction to that task, not a statement about the next one. Tidying one old
// row should not silently redirect the next add.
func (m Model) rescopeSelected() (tea.Model, tea.Cmd) {
	target, ok := m.selectedTask()
	if !ok {
		return m, nil
	}
	next := m.nextScope(target.Scope.Kind)
	if next == target.Scope.Kind {
		return m, nil
	}
	scope, ok := m.scopeFor(next)
	if !ok {
		return m, nil
	}
	// Anchored to the id: with a tier filter on, the row leaves the view
	// entirely and anchorCursor clamps instead of chasing it.
	return m, m.writeThenReload(target.ID, func(ctx context.Context, db *store.DB) error {
		return db.Rescope(ctx, target.ID, scope)
	})
}

// closeInput returns to normal mode and lets the field go.
func (m *Model) closeInput() {
	m.mode = modeNormal
	m.inputKind = inputAdd
	m.inputPlace = task.Scope{}
	m.inputTarget = 0
	m.inputHint = ""
	m.input.reset()
}

// scopeCycle is the scope kinds this context actually has, in tier order.
//
// Everything that offers a scope — Tab and `s` — walks this one slice, so an
// unavailable scope is never offered and a submit can never fail on scope
// grounds. Outside tmux that is dir then global; with only global, cycling is a
// no-op rather than a trap.
func (m Model) scopeCycle() []task.ScopeKind {
	out := make([]task.ScopeKind, 0, len(m.cfg.Scopes))
	for _, s := range m.cfg.Scopes {
		if !containsKind(out, s.Kind) {
			out = append(out, s.Kind)
		}
	}
	return out
}

// nextScope steps one place along the cycle, wrapping. A kind that is not in the
// cycle at all — a task filed under a scope this context cannot resolve — starts
// from the beginning.
func (m Model) nextScope(current task.ScopeKind) task.ScopeKind {
	cycle := m.scopeCycle()
	if len(cycle) == 0 {
		return current
	}
	for i, kind := range cycle {
		if kind == current {
			return cycle[(i+1)%len(cycle)]
		}
	}
	return cycle[0]
}

// scopeFor finds the full scope — kind *and* key — for a kind in this context.
// The key is the store's, so it must come from Config rather than be constructed.
func (m Model) scopeFor(kind task.ScopeKind) (task.Scope, bool) {
	for _, s := range m.cfg.Scopes {
		if s.Kind == kind {
			return s, true
		}
	}
	return task.Scope{}, false
}

func containsKind(kinds []task.ScopeKind, want task.ScopeKind) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

// inputRowIndex is where the input row sits among the viewport rows: at the top
// for an add, in place of the edited row for an edit.
func (m Model) inputRowIndex() int {
	if m.inputKind == inputEdit {
		for i, r := range m.rows {
			if r.kind == rowTask && r.task.ID == m.inputTarget {
				return i
			}
		}
		return 0
	}
	// An add in the all-tasks view files into the group under the cursor, so
	// the row it opens belongs directly under that group's header rather than
	// at the top of a list where it would appear to be in the first group.
	if m.view == viewAll {
		if i, ok := m.cursorHeader(); ok {
			return i + 1
		}
	}
	return 0
}

// withInputRow splices the input row (and its hint, when there is one) into the
// rendered rows.
func (m Model) withInputRow(rows []string) []string {
	if m.mode != modeInput {
		return rows
	}
	at := m.inputRowIndex()
	if at > len(rows) {
		at = len(rows)
	}

	field := m.renderField()
	out := make([]string, 0, len(rows)+2)
	out = append(out, rows[:at]...)
	out = append(out, field)
	if m.inputHint != "" {
		out = append(out, hintStyle.Render(truncate(inputHintIndent+m.inputHint, m.vp.Width)))
	}
	if m.inputKind == inputEdit && at < len(rows) {
		// The edited row is replaced, not pushed down: the task is not in two
		// places at once.
		at++
	}
	return append(out, rows[at:]...)
}

// inputHintIndent lines a hint up under the text, not under the glyph.
const inputHintIndent = "    "

// renderField renders the input row itself: the same cursor mark and scope glyph
// a task row carries, then the text field.
//
// The field windows its own text rather than being handed to truncate(): its
// output carries the cursor's ANSI escapes, and slicing a rendered string cuts
// through them.
func (m Model) renderField() string {
	prefix := cursorMark + glyph(m.inputScope) + " "
	in := m.input
	in.setWidth(m.vp.Width - lipgloss.Width(prefix))
	return prefix + in.view()
}
