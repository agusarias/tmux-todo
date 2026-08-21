// Package tui hosts the Bubble Tea popup: the merged task list that is the
// product's whole surface.
//
// The package is deliberately environment-blind. It never resolves a scope,
// asks tmux anything, or reads the clock: the active scopes, the home directory
// and the version all arrive in a Config from internal/cli. That is what keeps
// Update and View pure functions of injected data, and what makes this package
// testable without a tmux server — see the brief for docs/tasks.
package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/agusarias/tmux-todo/internal/store"
	"github.com/agusarias/tmux-todo/internal/task"
)

// queryTimeout bounds a store query so a locked database cannot hang the popup.
const queryTimeout = 5 * time.Second

// Default popup geometry, used until the first WindowSizeMsg arrives. A
// display-popup does not resize once open, so in practice these are what the
// first frame is drawn with.
const (
	defaultWidth  = 72
	defaultHeight = 18
	// chromeHeight is the rows View spends on everything that is not the list:
	// the box's two border rows, the title, the footer, and the blank line on
	// either side of the body. Undercount it and the frame is taller than the
	// pane, which scrolls the top of the list off screen rather than clipping
	// the bottom — the first rows to vanish are the session tier and the tier
	// labels, i.e. exactly what the merged view exists to show.
	//
	// It stays a constant rather than becoming computed, because every line it
	// counts is now one row *by construction*: the title, the footer and the
	// single-line body states are each truncated to contentWidth before they
	// are assembled. The constant was what made the footer bug invisible, so
	// the invariant it depends on is asserted directly by
	// TestFrameNeverExceedsThePane rather than left as a comment.
	chromeHeight = 6
	// chromeWidth is the columns the box costs: two border columns plus
	// boxStyle's padding of two on each side. Rows sized to the full width
	// would wrap inside it and one task would stop being one row.
	chromeWidth = 6
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	hintStyle  = lipgloss.NewStyle().Faint(true)
	emptyStyle = lipgloss.NewStyle().Faint(true).Italic(true)
	errStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("204"))
	boxStyle   = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 2)
)

// Config is everything the popup needs from its caller. Nothing in it is
// discovered by this package.
type Config struct {
	// DB is the open store. Required.
	DB *store.DB
	// Scopes are the resolved active scopes, in tier order. They become the
	// store.Filter's Scopes on every query — see storeFilter for why this
	// must never be left empty.
	Scopes []task.Scope
	// Home is the directory abbreviated to ~ when displaying dir scope keys.
	// Display only; the keys themselves stay absolute.
	Home string
	// Version is shown in the help overlay. It is deliberately not in the
	// footer: stamped from `git describe --tags --always --dirty`, it is long
	// enough to push the footer past the 52 columns the popup's 60 x 15 floor
	// leaves for content (design.md), and a footer that truncates hides
	// keybindings. footerText is 39 columns so it survives either that floor or
	// the 42 a bare 60%x60% popup would have given.
	Version string
	// DefaultScope is the scope kind a new task starts on — the resolved sticky
	// default, worked out by internal/cli, because this package must not import
	// internal/scope. An empty or unavailable value degrades to the first scope
	// in Scopes, so a stale stored preference can never seed a scope the user
	// cannot submit to.
	DefaultScope task.ScopeKind
	// SetSticky persists the scope kind an add was filed under, so the next add
	// starts there. Injected rather than reached for, exactly as Now is: the
	// write lands in the XDG state dir, which this package must not know about.
	//
	// Nil is fine and means "do not remember". An error from it never fails the
	// add — the task is already saved by then.
	SetSticky func(task.ScopeKind) error
	// LiveSessions is the set of tmux session names running right now, resolved
	// once by internal/cli before the popup opens. It is what labels an
	// all-tasks group `(live)` or `(not running)`, and this package must never
	// discover it: asking tmux from here would put a subprocess on a keypress
	// inside the popup and make rendering depend on a call that can fail.
	//
	// Nil is fine and means "nothing is known to be running", which labels every
	// session group `(not running)`. The staleness is deliberate and cheap: a
	// session killed while the popup is open still reads `(live)`, and Enter then
	// falls through to the create-and-switch path anyway, so a wrong label costs
	// nothing.
	LiveSessions map[string]bool
	// Now is the clock the visibility cutoff is measured against. Injected, and
	// defaulting to time.Now, for the same reason the store's DB.now is: a test
	// can advance it 25h and prove a completed row leaves the view, which is not
	// provable against the real clock.
	//
	// This is a deliberate revision of popup-tui-merged-list's note that the TUI
	// holds no clock. An injected clock keeps that note's intent — the model is
	// still a pure function of its inputs.
	Now func() time.Time
	// CloseKey is a Bubble Tea key string ("ctrl+l") that closes the popup, or
	// "" for none. It is the key that *opened* it: the plugin passes @todo-key
	// into the popup's environment and internal/cli translates it, because this
	// package must not read an env var or ask tmux anything.
	//
	// "" is the normal state for a hand-run `tdo tui` and for a prefix-table
	// install, and it must stay inert rather than defaulting to something:
	// guessing a close key would shadow a popup binding the user did not ask to
	// lose. The precedence rule is the same one — the close check runs *after*
	// each mode's own key switch, so a CloseKey that collides with `a` or `d`
	// keeps doing its old job and simply does not close.
	CloseKey string
}

// closesOn reports whether msg is the configured close key.
//
// The empty check is load-bearing rather than defensive: without it every
// unhandled key would match "" and quit the popup.
func (c Config) closesOn(msg tea.KeyMsg) bool {
	return c.CloseKey != "" && msg.String() == c.CloseKey
}

// now reads the injected clock, falling back to the real one.
func (c Config) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

// mode is the key-dispatch mode. It is why Update dispatches on the mode before
// the key rather than switching on the key alone: in the input row `q` is a
// literal keystroke, not quit, and with the help overlay up the mutation keys
// must not fire behind it.
type mode int

const (
	modeNormal mode = iota
	modeInput
	modeHelp
)

// inputKind distinguishes the two things the one input row does. They differ in
// three places — where the row sits, what an empty Enter means, and whether Tab
// cycles the scope — and every one of those differences is a decision recorded
// in the brief, so they are worth naming rather than inferring from inputTarget
// being zero.
type inputKind int

const (
	inputAdd inputKind = iota
	inputEdit
)

// viewKind selects which list is on screen: the merged list over the active
// scopes, or the wide list over every scope in the database.
type viewKind int

const (
	viewMerged viewKind = iota
	viewAll
)

// Model is the popup's root model.
type Model struct {
	cfg  Config
	mode mode
	view viewKind

	// filter narrows the list to a single scope tier. The zero value means no
	// filter, i.e. the merged view over every active scope.
	filter task.ScopeKind

	// openedAt is when this popup opened. Rows completed before it were already
	// done when the user arrived, so they are not "reversible in the moment"
	// and start out hidden.
	openedAt time.Time

	// tasks are the rows on screen, flat and in screen order — the merged
	// list's order, or the all-tasks view's groups concatenated. groups is the
	// all-tasks view's shape of the same data.
	tasks  []task.Task
	groups []store.Group
	// rows is what the cursor indexes: tasks and, in the all-tasks view, the
	// group headers between them. See rows.go.
	rows   []listRow
	cursor int
	// savedCursor remembers where the cursor was in the view that is not on
	// screen, so `g` and `g` again come back to where you were. Indexed by
	// viewKind; the entry for the current view is stale by design.
	savedCursor [2]int
	vp          viewport.Model
	ready       bool // a query has come back at least once
	err         error
	quitting    bool

	// The input row. inputScope is the Tab-selected scope and matters only for
	// an add; inputTarget is the task under edit and matters only for an edit.
	input      field
	inputKind  inputKind
	inputScope task.ScopeKind
	// inputPlace is the *whole* scope an add files into when the all-tasks view
	// picked it from a group header — kind and key. It exists because that key
	// may not be in Config.Scopes at all (a session that is not running), so
	// scopeFor could not find it. The zero value means "look the kind up in the
	// active set", which is the merged view's add.
	inputPlace  task.Scope
	inputTarget int64
	inputHint   string

	// queued is the LIFO undo stack of rows removed from the view but not from
	// the database, and also the filter set that hides them. Because nothing was
	// written, `u` restores a row at its original position with its original id
	// for free — the store still has it. See delete.go.
	//
	// One entry per *keypress*, not per row: `d` pushes one id and `D` pushes a
	// whole group's, so one `u` undoes exactly one action either way. Flattening
	// this into a single []int64 is what made group delete need three undos.
	queued [][]int64
	// commitErr is what the queued deletes did on the way out. Update stores it
	// and Run returns it: a delete that failed must not be reported as done.
	commitErr error
	// jump is the session Enter asked for, as an *intent*: this package runs no
	// subprocess and imports no os/exec. Run hands it to internal/cli, which
	// owns the tmux and sesh invocations — and which runs them only after the
	// queued deletes have committed, because Enter closes the popup.
	jump Jump

	width  int
	height int
}

// rowsMsg carries the result of a store query back into Update.
type rowsMsg struct {
	tasks []task.Task
	// groups is filled instead of tasks when the query was the all-tasks
	// view's. Both are carried on one message because a view switch is a
	// re-query, and two message types would mean two handlers with the same
	// anchor, clamp and viewport logic.
	groups []store.Group
	view   viewKind
	err    error
	// anchor is the task id the cursor should land on once the rows are in.
	// Zero means "keep the current index". Re-anchoring by *id* rather than by
	// index is what makes space usable twice in a row: a toggle can change how
	// many rows are visible, and an index would drift onto a neighbour.
	anchor int64
}

// New builds the model from cfg without touching the database; the first query
// is issued by Init.
func New(cfg Config) Model {
	m := Model{
		cfg:      cfg,
		mode:     modeNormal,
		view:     viewMerged,
		openedAt: cfg.now(),
		width:    defaultWidth,
		height:   defaultHeight,
	}
	m.vp = viewport.New(m.contentWidth(), m.listHeight())
	return m
}

// Init loads the first batch of rows.
func (m Model) Init() tea.Cmd { return m.reloadCmd() }

// activeScopes are the scopes a query should cover: the injected set, narrowed
// to the filtered tier when a filter is on.
func (m Model) activeScopes() []task.Scope {
	if m.filter == "" {
		return m.cfg.Scopes
	}
	out := make([]task.Scope, 0, len(m.cfg.Scopes))
	for _, s := range m.cfg.Scopes {
		if s.Kind == m.filter {
			out = append(out, s)
		}
	}
	return out
}

// doneSince is the boundary below which completed rows stop being shown:
// whichever of "this popup opened" and "one retention window ago" is later.
//
// In practice openedAt almost always wins — the 24h arm only bites for a popup
// left open longer than that — but docs/design.md specifies "whichever comes
// first", and the arithmetic is cheap. The point of the rule is that a row you
// completed just now stays under your cursor, struck through, while one you
// completed yesterday is already gone when you arrive.
func (m Model) doneSince() time.Time {
	retention := m.cfg.now().Add(-store.DoneRetention)
	if m.openedAt.After(retention) {
		return m.openedAt
	}
	return retention
}

// reloadCmd re-runs the query and replaces the rows. Every action that changes
// what should be on screen goes through this, which is what lets the popup stay
// open across actions instead of exiting to refresh.
func (m Model) reloadCmd() tea.Cmd { return m.reloadAnchoredTo(0) }

// reloadAnchoredTo re-queries and puts the cursor back on the given task id.
func (m Model) reloadAnchoredTo(anchor int64) tea.Cmd { return m.writeThenReload(anchor, nil) }

// writeThenReload optionally performs a store write, then re-runs the query and
// replaces the rows — one command producing one message.
//
// Write and re-read are deliberately the same command rather than a
// tea.Sequence of two: the model never patches its own copy of a row to reflect
// a write, so the store stays the single source of truth for what is done, and
// the whole action stays a single testable step.
func (m Model) writeThenReload(anchor int64, write func(context.Context, *store.DB) error) tea.Cmd {
	if write == nil {
		return m.query(anchor, nil)
	}
	return m.query(anchor, func(ctx context.Context, db *store.DB) (int64, error) {
		return anchor, write(ctx, db)
	})
}

// query is the single place this package reads the store. The write it takes
// returns the anchor as well as an error, because an insert only learns the id
// it should put the cursor on by performing the insert.
func (m Model) query(anchor int64, write func(context.Context, *store.DB) (int64, error)) tea.Cmd {
	db := m.cfg.DB
	view := m.view
	scopes := m.activeScopes()
	// The all-tasks view leaves Scopes nil *deliberately*, which is the one
	// place in this package that wants store.Filter's "every scope in the
	// database" reading — including keys no longer active, which is the whole
	// point of the view. It shares DoneSince with the merged view so the two
	// cannot disagree about which completed rows are still visible.
	filter := store.Filter{IncludeDone: true, DoneSince: m.doneSince()}
	if view != viewAll {
		filter.Scopes = scopes
	}
	current := m.tasks
	currentGroups := m.groups

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()

		if write != nil && db != nil {
			chosen, err := write(ctx, db)
			if err != nil {
				// Keep the rows on screen: a failed write changed nothing, and
				// blanking the list would read as "your tasks are gone".
				return rowsMsg{tasks: current, groups: currentGroups, view: view, err: err, anchor: anchor}
			}
			anchor = chosen
		}
		if db == nil {
			return rowsMsg{view: view, anchor: anchor}
		}
		if view == viewAll {
			groups, err := db.ListGrouped(ctx, filter)
			return rowsMsg{groups: groups, view: view, err: err, anchor: anchor}
		}

		// An empty store.Filter.Scopes means *every scope in the database*, not
		// "the active set" (see store.Filter). So an empty active set must short
		// circuit: querying with it would show tasks from scopes the user is not
		// in — other sessions, other repos.
		if len(scopes) == 0 {
			return rowsMsg{view: view, anchor: anchor}
		}
		tasks, err := db.List(ctx, filter)
		return rowsMsg{tasks: tasks, view: view, err: err, anchor: anchor}
	}
}

// Update dispatches on the model's mode, then on the message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case rowsMsg:
		if msg.view != m.view {
			// A reload issued before a `g` and landing after it. Applying it
			// would fill one view's rows while the other is on screen.
			return m, nil
		}
		// The queue filter lives here, at the one point every reload passes
		// through, so "a queued row is unreachable on *every* reload" is
		// structural rather than something each caller has to remember.
		if m.view == viewAll {
			m.groups = m.visibleGroups(msg.groups)
			m.tasks = groupTasks(m.groups)
		} else {
			m.tasks = m.dropQueued(msg.tasks)
			m.groups = nil
		}
		m.err = msg.err
		m.ready = true
		m.rebuildRows()
		if msg.anchor != 0 {
			m.anchorCursor(msg.anchor)
		}
		m.clampCursor()
		// An edit whose row went away — another pane deleted it, or the reload
		// that arrived was a `d` on it — has nothing left to save to.
		if m.mode == modeInput && m.inputKind == inputEdit && !m.hasTask(m.inputTarget) {
			m.closeInput()
		}
		m.refreshViewport()
		return m, nil

	case deletesCommittedMsg:
		// Quit *in response to* the commit finishing, rather than racing it:
		// tea.Quit issued alongside the commit could take effect first.
		m.commitErr = msg.err
		return m, tea.Quit

	case tea.WindowSizeMsg:
		// Popups do not resize once open, so this rarely fires — but sizing
		// belongs to the tmux integration task, not to a hardcoded constant.
		m.width, m.height = msg.Width, msg.Height
		m.vp.Width = m.contentWidth()
		m.vp.Height = m.listHeight()
		m.refreshViewport()
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeNormal:
			return m.updateNormal(msg)
		case modeInput:
			return m.updateInput(msg)
		case modeHelp:
			return m.updateHelp(msg)
		}
	}
	return m, nil
}

// updateNormal handles keys in normal mode. Unhandled keys do nothing at all —
// in particular they do not quit.
func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		return m.quit()

	case "?":
		m.mode = modeHelp
		return m, nil

	case "a":
		return m.startAdd()

	case "e":
		return m.startEdit()

	case "s":
		return m.rescopeSelected()

	case "g":
		return m.toggleAllTasks()

	case "enter":
		return m.jumpToSelected()

	case "r":
		return m.rehomeGroup()

	case "d":
		return m.queueDelete()

	case "D":
		return m.queueGroupDelete()

	case "u":
		return m.undoDelete()

	case "j", "down":
		m.moveCursor(1)
		return m, nil

	case "k", "up":
		m.moveCursor(-1)
		return m, nil

	case " ":
		return m.toggleDone()

	case "1":
		return m.toggleFilter(task.ScopeSession)
	case "2":
		return m.toggleFilter(task.ScopeDir)
	case "3":
		return m.toggleFilter(task.ScopeGlobal)
	}
	// After the switch, never inside it: every binding above wins over the close
	// key, so a @todo-key of `a` still opens the input row. That precedence is
	// structural here rather than a rule each case has to remember.
	if m.cfg.closesOn(msg) {
		return m.quit()
	}
	return m, nil
}

// quit commits any queued deletes and then exits.
//
// With an empty queue the exit path is exactly what it was before this task:
// one tea.Quit, no command in between. That matters because it is the path
// every user who never presses `d` takes.
func (m Model) quit() (tea.Model, tea.Cmd) {
	m.quitting = true
	if len(m.queued) == 0 {
		return m, tea.Quit
	}
	return m, m.commitDeletesCmd()
}

// updateHelp handles keys while the keymap overlay is up. Everything else is
// inert: the overlay covers the list, so a mutation key would act on a row the
// user cannot see.
//
// `q` dismisses rather than quits. It reads oddly for one keypress and is the
// better trade: two presses of `q` exit from anywhere, and a `q` that quit from
// behind an overlay would exit a popup the user had only opened to read.
func (m Model) updateHelp(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "?", "esc", "q", "ctrl+c":
		m.mode = modeNormal
		return m, nil
	}
	// The close key quits outright from here rather than dismissing the overlay:
	// "the same key always closes the popup" is the whole feature, and a user who
	// pressed it wants out, not one screen back. `q` still dismisses, because the
	// switch above runs first.
	if m.cfg.closesOn(msg) {
		return m.quit()
	}
	return m, nil
}

// hasTask reports whether a task id is among the visible rows.
func (m Model) hasTask(id int64) bool {
	for _, t := range m.tasks {
		if t.ID == id {
			return true
		}
	}
	return false
}

// toggleDone flips the cursor row between pending and complete.
//
// It is a toggle rather than a one-way action because it is the only undo the
// product has — docs/design.md defers an undo stack — so a mis-press has to be
// fixable by pressing the same key again.
func (m Model) toggleDone() (tea.Model, tea.Cmd) {
	target, ok := m.selectedTask()
	if !ok || m.cfg.DB == nil {
		return m, nil
	}

	return m, m.writeThenReload(target.ID, func(ctx context.Context, db *store.DB) error {
		if target.Done {
			return db.Uncomplete(ctx, target.ID)
		}
		return db.Complete(ctx, target.ID)
	})
}

// anchorCursor puts the cursor on a task id, or as close as it can get. The row
// can legitimately be gone — uncompleting is fine, but completing a row that was
// already outside the visibility window removes it — so this clamps rather than
// insisting.
func (m *Model) anchorCursor(id int64) {
	for i, r := range m.rows {
		if r.kind == rowTask && r.task.ID == id {
			m.cursor = i
			return
		}
	}
	m.clampCursor()
}

// toggleFilter switches to a single-tier view, or back to the merged view when
// the tier already on screen is selected again. There is no separate un-filter
// key: Esc is bound to close, and making it clear-the-filter first would make
// the popup feel sticky.
func (m Model) toggleFilter(kind task.ScopeKind) (tea.Model, tea.Cmd) {
	if m.filter == kind {
		m.filter = ""
	} else {
		m.filter = kind
	}
	// The row under the old cursor is unrelated to the new list.
	m.cursor = 0
	m.vp.SetYOffset(0)
	return m, m.reloadCmd()
}

// moveCursor steps the selection, clamped at both ends, and scrolls the
// viewport just enough to keep it visible.
func (m *Model) moveCursor(delta int) {
	if len(m.rows) == 0 {
		m.cursor = 0
		return
	}
	// Stepped one *selectable* row at a time rather than by index, so a group
	// header is skipped rather than landed on and then snapped away from —
	// which would make one j at the end of a group move two rows.
	step, n := 1, delta
	if delta < 0 {
		step, n = -1, -delta
	}
	for range n {
		next := m.nextSelectable(m.cursor, step)
		if next < 0 {
			break
		}
		m.cursor = next
	}
	m.clampCursor()
	m.refreshViewport()
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	m.snapCursor()
}

// contentWidth is how many columns anything inside the box gets, once its
// border and padding are paid for.
//
// *Every* line View assembles must fit this, not just the task rows. lipgloss
// wraps an over-wide line rather than clipping it, so one long chrome line
// silently becomes two — and then the frame is taller than chromeHeight
// promises. That is how the footer shipped a bug: at 48 columns it wrapped, the
// frame ran a row over the pane, and the terminal scrolled the top border and
// the session tier away.
func (m Model) contentWidth() int {
	if w := m.width - chromeWidth; w > 0 {
		return w
	}
	return 1
}

// listHeight is how many rows the viewport gets.
func (m Model) listHeight() int {
	if h := m.height - chromeHeight; h > 0 {
		return h
	}
	return 1
}

// refreshViewport re-renders the rows into the viewport and scrolls the cursor
// into view.
func (m *Model) refreshViewport() {
	rows := m.renderRowLines()
	// The input row goes *inside* the viewport rather than becoming a sixth
	// chrome line: chromeHeight is a constant, three frame bugs have already
	// shipped in that arithmetic, and a row the list scrolls is the cheaper
	// half of the trade.
	rows = m.withInputRow(rows)
	m.vp.SetContent(strings.Join(rows, "\n"))

	focus := m.focusRow()
	switch h := m.vp.Height; {
	case h <= 0:
	case focus < m.vp.YOffset:
		m.vp.SetYOffset(focus)
	case focus >= m.vp.YOffset+h:
		m.vp.SetYOffset(focus - h + 1)
	}
}

// renderRowLines formats the list for whichever view is on screen.
//
// The two views are genuinely different layouts — the merged list shares one
// column budget between task text and tier labels, the all-tasks list gives
// rows the full width because the header carries the scope — so they stay two
// pure functions over the same row slice rather than one function with a flag.
func (m Model) renderRowLines() []string {
	opts := renderOpts{
		Width:  m.vp.Width,
		Home:   m.cfg.Home,
		Cursor: m.cursorRow(),
	}
	if m.view == viewAll {
		return renderGroupRows(m.rows, opts)
	}
	return renderRows(m.tasks, opts)
}

// cursorRow is which task row carries the cursor mark. While adding, the input
// row has the focus and no task row is marked.
func (m Model) cursorRow() int {
	if m.mode == modeInput && m.inputKind == inputAdd {
		return -1
	}
	return m.cursor
}

// focusRow is the viewport row to keep on screen: the input row while it is
// open, the cursor otherwise.
func (m Model) focusRow() int {
	if m.mode == modeInput {
		return m.inputRowIndex()
	}
	if m.inputKind == inputAdd && m.mode == modeInput {
		return 0
	}
	return m.cursor
}

// View renders the popup: title, list (or an empty state), footer.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	// The box is pinned to the popup width so its border does not jump around
	// as the content narrows — filtering to one tier should not visibly resize
	// the frame. The -2 is the border's own two columns.
	return clampHeight(m.frame(), m.height)
}

// frame assembles the popup before the height backstop is applied.
//
// Split out from View so a test can assert the frame *already* fits the pane —
// if it only checked View's output, clampHeight would hide a miscounted
// chromeHeight by silently trimming the overflow.
func (m Model) frame() string {
	box := boxStyle.Width(m.width - 2)
	return box.Render(strings.Join([]string{
		m.titleLine(),
		"",
		m.body(),
		"",
		m.footer(),
	}, "\n"))
}

// clampHeight drops any rows past the pane's height.
//
// A backstop, not the mechanism: with every chrome line truncated to one row
// the frame already fits, and this should never fire. It exists because the
// failure it prevents is asymmetric — a frame one row too tall makes the
// terminal *scroll*, which costs the rows at the **top** (the box border, the
// session tier, the tier labels), while clipping our own bottom row costs the
// bottom border. Losing a border beats losing the list.
func clampHeight(frame string, height int) string {
	if height <= 0 {
		return frame
	}
	lines := strings.Split(frame, "\n")
	if len(lines) <= height {
		return frame
	}
	return strings.Join(lines[:height], "\n")
}

func (m Model) titleLine() string {
	const name = "tdo"
	width := m.contentWidth()
	if m.filter == "" {
		return titleStyle.Render(truncate(name, width))
	}
	// Truncation is applied to the plain text and the styles wrap the result:
	// slicing a rendered string would cut through an ANSI escape.
	room := width - lipgloss.Width(name)
	if room <= 0 {
		return titleStyle.Render(truncate(name, width))
	}
	suffix := fmt.Sprintf("  filter: %s", m.filter)
	return titleStyle.Render(name) + hintStyle.Render(truncate(suffix, room))
}

// body is the list, or whichever empty state applies.
func (m Model) body() string {
	if m.err != nil {
		// The store's errors already name the operation ("list tasks: …",
		// "complete task 3: …"), so prefixing our own guess would only risk
		// contradicting them.
		// Truncated like every other chrome line. Losing the tail of an error
		// is a real cost, but a wrapped one breaks the frame, and the store
		// puts the operation at the *front* of its messages.
		return errStyle.Render(truncate(m.err.Error(), m.contentWidth()))
	}
	if !m.ready {
		return emptyStyle.Render(truncate("loading…", m.contentWidth()))
	}
	if m.mode == modeHelp {
		// Replaces the list, like the error and loading states, so the overlay
		// costs no chrome row.
		return strings.Join(m.helpBody(), "\n")
	}
	// With the input row open the list is never empty, even before the first
	// task exists — the row the user is typing into is itself a row.
	if len(m.tasks) == 0 && m.mode != modeInput {
		return emptyStyle.Render(truncate(m.emptyText(), m.contentWidth()))
	}
	return m.vp.View()
}

// helpBody is the keymap overlay, clipped to the rows the list has. Each line
// is truncated like every other line View assembles: lipgloss wraps rather than
// clips, and a wrapped help line would push the frame past the pane.
func (m Model) helpBody() []string {
	lines := helpLines(m.view, m.cfg.Version)
	if h := m.listHeight(); len(lines) > h {
		lines = lines[:h]
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, hintStyle.Render(truncate(line, m.contentWidth())))
	}
	return out
}

// emptyText distinguishes "there is nothing here at all" from "your filter hid
// everything" — the second is a state the user can undo, and saying so is the
// difference between a hint and a dead end.
func (m Model) emptyText() string {
	// A list emptied by `d` is not an empty list: the rows are still in the
	// store and `u` brings them back. Saying "no tasks yet" here would be a lie
	// that also hides the way out.
	if ids := m.queuedIDs(); len(ids) > 0 {
		return fmt.Sprintf("%s queued for deletion — u undoes", plural(len(ids), "task"))
	}
	if m.filter != "" {
		return fmt.Sprintf("no %s tasks — press %s again for all scopes", m.filter, filterKey(m.filter))
	}
	return "no tasks yet"
}

// plural renders a count with its noun: "1 task", "2 tasks".
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

// filterKey is the digit bound to a tier, for the empty-state hint.
func filterKey(kind task.ScopeKind) string {
	switch kind {
	case task.ScopeSession:
		return "1"
	case task.ScopeDir:
		return "2"
	case task.ScopeGlobal:
		return "3"
	default:
		return "?"
	}
}

// footerText is the hint line, kept to 39 columns so it survives untruncated in
// the narrowest popup the design specifies: ~60%x60% of an 80-column terminal is
// 48 columns, which leaves contentWidth 42.
//
// It does not enumerate the keymap, and it no longer carries the version. The
// full list is eleven keys and the version is stamped from `git describe`, so
// spelling either out here would put the line back over the width and truncation
// would silently eat the keys at the end — which is how `docs/design.md`'s own
// footer mock came to specify a line 93 columns wide. `? keys` is the pointer;
// helpLines is the list.
const footerText = "j/k move · space done · ? keys · q quit"

func (m Model) footer() string {
	return hintStyle.Render(truncate(footerText, m.contentWidth()))
}

// Run starts the popup and blocks until it exits, returning the jump the user
// asked for (the zero Jump means none) and the outcome of the queued deletes.
//
// The order matters and is structural: the deletes commit *inside* the program's
// event loop, so by the time Run returns there is nothing left to lose and
// internal/cli can run the jump. It returns the final model's commitErr, so a
// queued delete that failed on the way out reaches internal/cli and the exit
// code. The popup has already gone by
// then, and reporting "deleted" for a row still in the database would be worse
// than the delete not happening.
func Run(cfg Config) (Jump, error) { return run(New(cfg)) }

// run is Run with the program options exposed, so a test can drive the real
// event loop with a scripted input and no terminal. Without this seam the only
// thing asserting that Run reports commitErr would be inspection — and "the
// production entry point is not what the tests exercise" is a bug this repo has
// shipped twice.
func run(m Model, opts ...tea.ProgramOption) (Jump, error) {
	final, err := tea.NewProgram(m, opts...).Run()
	if err != nil {
		return Jump{}, fmt.Errorf("run tui: %w", err)
	}
	if final, ok := final.(Model); ok {
		return final.jump, final.commitErr
	}
	return Jump{}, nil
}
