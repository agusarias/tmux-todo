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
}

// New builds the model from cfg without touching the database; the first query
// is issued by Init.
func New(cfg Config) Model {
	m := Model{
		cfg:    cfg,
		mode:   modeNormal,
		view:   viewMerged,
		width:  defaultWidth,
		height: defaultHeight,
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

// reloadCmd re-runs the query and replaces the rows. Every action that changes
// what should be on screen goes through this, which is what lets the popup stay
// open across actions instead of exiting to refresh.
func (m Model) reloadCmd() tea.Cmd {
	db := m.cfg.DB
	scopes := m.activeScopes()

	// An empty store.Filter.Scopes means *every scope in the database*, not
	// "the active set" (see store.Filter). So an empty active set must short
	// circuit: querying with it would show tasks from scopes the user is not
	// in — other sessions, other repos.
	if db == nil || len(scopes) == 0 {
		return func() tea.Msg { return rowsMsg{} }
	}

	filter := store.Filter{Scopes: scopes, IncludeDone: true}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		tasks, err := db.List(ctx, filter)
		return rowsMsg{tasks: tasks, err: err}
	}
}

// Update dispatches on the model's mode, then on the message.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case rowsMsg:
		m.tasks = msg.tasks
		m.err = msg.err
		m.ready = true
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

	case "1":
		return m.toggleFilter(task.ScopeSession)
	case "2":
		return m.toggleFilter(task.ScopeDir)
	case "3":
		return m.toggleFilter(task.ScopeGlobal)
	}
	return m, nil
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
		return errStyle.Render("cannot read tasks: " + m.err.Error())
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
		"1/2/3 filter · j/k move · q quit · v%s", m.cfg.Version))
}

// Run starts the popup and blocks until it exits.
func Run(cfg Config) error {
	if _, err := tea.NewProgram(New(cfg)).Run(); err != nil {
		return fmt.Errorf("run tui: %w", err)
	}
	return nil
}
