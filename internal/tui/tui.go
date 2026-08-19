// Package tui hosts the Bubble Tea popup. This scaffold ships a placeholder
// model that proves the Charm stack links and runs inside display-popup; the
// merged list, input row and all-tasks view arrive with their own tasks.
package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("205"))
	hintStyle  = lipgloss.NewStyle().Faint(true)
	boxStyle   = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 2)
)

// Model is the placeholder root model.
type Model struct {
	version  string
	quitting bool
}

// New returns a placeholder model labelled with the running version.
func New(version string) Model { return Model{version: version} }

func (m Model) Init() tea.Cmd { return nil }

// Update quits on q, Esc or ctrl+c and ignores everything else.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "ctrl+c":
			m.quitting = true
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	body := fmt.Sprintf("%s\n\n%s\n\n%s",
		titleStyle.Render("tdo"),
		"scaffold placeholder — no tasks yet",
		hintStyle.Render(fmt.Sprintf("version %s · q quit", m.version)),
	)
	return boxStyle.Render(body) + "\n"
}

// Run starts the popup and blocks until it exits.
func Run(version string) error {
	if _, err := tea.NewProgram(New(version)).Run(); err != nil {
		return fmt.Errorf("run tui: %w", err)
	}
	return nil
}
