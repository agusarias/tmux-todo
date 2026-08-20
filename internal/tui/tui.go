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
	// Version is shown in the footer.
	Version string
	// Now is the clock the visibility cutoff is measured against. Injected, and
	// defaulting to time.Now, for the same reason the store's DB.now is: a test
	// can advance it 25h and prove a completed row leaves the view, which is not
	// provable against the real clock.
	//
	// This is a deliberate revision of popup-tui-merged-list's note that the TUI
	// holds no clock. An injected clock keeps that note's intent — the model is
	// still a pure function of its inputs.
	Now func() time.Time
}

// now reads the injected clock, falling back to the real one.
func (c Config) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

// mode is the key-dispatch mode. Only normal mode exists today; the input mode
// that task-create-edit-rescope adds is the reason Update dispatches on this
// rather than switching on the key alone — in an input row `q` is a literal
// keystroke, not quit.
type mode int

const (
	modeNormal mode = iota
)

// viewKind selects which list is on screen. Only the merged view exists today;
// the all-tasks view (`g`) becomes another case here rather than a restructure.
type viewKind int

const (
	viewMerged viewKind = iota
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

	tasks    []task.Task
	cursor   int
	vp       viewport.Model
	ready    bool // a query has come back at least once
	err      error
	quitting bool

	width  int
	height int
}

// rowsMsg carries the result of a store query back into Update.
type rowsMsg struct {
	tasks []task.Task
	err   error
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
	m.vp = viewport.New(m.listWidth(), m.listHeight())
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
	db := m.cfg.DB
	scopes := m.activeScopes()
	filter := store.Filter{Scopes: scopes, IncludeDone: true, DoneSince: m.doneSince()}
	current := m.tasks

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()

		if write != nil && db != nil {
			if err := write(ctx, db); err != nil {
				// Keep the rows on screen: a failed write changed nothing, and
				// blanking the list would read as "your tasks are gone".
				return rowsMsg{tasks: current, err: err, anchor: anchor}
			}
		}

		// An empty store.Filter.Scopes means *every scope in the database*, not
		// "the active set" (see store.Filter). So an empty active set must short
		// circuit: querying with it would show tasks from scopes the user is not
		// in — other sessions, other repos.
		if db == nil || len(scopes) == 0 {
			return rowsMsg{anchor: anchor}
		}
		tasks, err := db.List(ctx, filter)
		return rowsMsg{tasks: tasks, err: err, anchor: anchor}
	}
}

// Update dispatches on the model's mode, then on the message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case rowsMsg:
		m.tasks = msg.tasks
		m.err = msg.err
		m.ready = true
		if msg.anchor != 0 {
			m.anchorCursor(msg.anchor)
		}
		m.clampCursor()
		m.refreshViewport()
		return m, nil

	case tea.WindowSizeMsg:
		// Popups do not resize once open, so this rarely fires — but sizing
		// belongs to the tmux integration task, not to a hardcoded constant.
		m.width, m.height = msg.Width, msg.Height
		m.vp.Width = m.listWidth()
		m.vp.Height = m.listHeight()
		m.refreshViewport()
		return m, nil

	case tea.KeyMsg:
		switch m.mode {
		case modeNormal:
			return m.updateNormal(msg)
		}
	}
	return m, nil
}

// updateNormal handles keys in normal mode. Unhandled keys do nothing at all —
// in particular they do not quit.
func (m Model) updateNormal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "q", "esc", "ctrl+c":
		m.quitting = true
		return m, tea.Quit

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
	return m, nil
}

// toggleDone flips the cursor row between pending and complete.
//
// It is a toggle rather than a one-way action because it is the only undo the
// product has — docs/design.md defers an undo stack — so a mis-press has to be
// fixable by pressing the same key again.
func (m Model) toggleDone() (tea.Model, tea.Cmd) {
	if m.cursor < 0 || m.cursor >= len(m.tasks) || m.cfg.DB == nil {
		return m, nil
	}
	target := m.tasks[m.cursor]

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
	for i, t := range m.tasks {
		if t.ID == id {
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
	if len(m.tasks) == 0 {
		m.cursor = 0
		return
	}
	m.cursor += delta
	m.clampCursor()
	m.refreshViewport()
}

func (m *Model) clampCursor() {
	if m.cursor >= len(m.tasks) {
		m.cursor = len(m.tasks) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// listWidth is how many columns a row gets, inside the box's border and padding.
func (m Model) listWidth() int {
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
	rows := renderRows(m.tasks, renderOpts{
		Width:  m.vp.Width,
		Home:   m.cfg.Home,
		Cursor: m.cursor,
	})
	m.vp.SetContent(strings.Join(rows, "\n"))

	switch h := m.vp.Height; {
	case h <= 0:
	case m.cursor < m.vp.YOffset:
		m.vp.SetYOffset(m.cursor)
	case m.cursor >= m.vp.YOffset+h:
		m.vp.SetYOffset(m.cursor - h + 1)
	}
}

// View renders the popup: title, list (or an empty state), footer.
func (m Model) View() string {
	if m.quitting {
		return ""
	}
	// The box is pinned to the popup width so its border does not jump around
	// as the content narrows — filtering to one tier should not visibly resize
	// the frame. The -2 is the border's own two columns.
	box := boxStyle.Width(m.width - 2)
	return box.Render(strings.Join([]string{
		m.titleLine(),
		"",
		m.body(),
		"",
		m.footer(),
	}, "\n"))
}

func (m Model) titleLine() string {
	title := titleStyle.Render("tdo")
	if m.filter != "" {
		return title + hintStyle.Render(fmt.Sprintf("  filter: %s", m.filter))
	}
	return title
}

// body is the list, or whichever empty state applies.
func (m Model) body() string {
	if m.err != nil {
		// The store's errors already name the operation ("list tasks: …",
		// "complete task 3: …"), so prefixing our own guess would only risk
		// contradicting them.
		return errStyle.Render(m.err.Error())
	}
	if !m.ready {
		return emptyStyle.Render("loading…")
	}
	if len(m.tasks) == 0 {
		return emptyStyle.Render(m.emptyText())
	}
	return m.vp.View()
}

// emptyText distinguishes "there is nothing here at all" from "your filter hid
// everything" — the second is a state the user can undo, and saying so is the
// difference between a hint and a dead end.
func (m Model) emptyText() string {
	if m.filter != "" {
		return fmt.Sprintf("no %s tasks — press %s again for all scopes", m.filter, filterKey(m.filter))
	}
	return "no tasks yet"
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

// footer lists only the keys this build actually binds. docs/design.md's mock
// shows the end state (a/e/space/d/s/g included); those keys belong to the three
// follow-on UI tasks, and advertising them before they work would be a lie.
func (m Model) footer() string {
	return hintStyle.Render(fmt.Sprintf(
		"1/2/3 filter · j/k move · space done · q quit · v%s", m.cfg.Version))
}

// Run starts the popup and blocks until it exits.
func Run(cfg Config) error {
	if _, err := tea.NewProgram(New(cfg)).Run(); err != nil {
		return fmt.Errorf("run tui: %w", err)
	}
	return nil
}
