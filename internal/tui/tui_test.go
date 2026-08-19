package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestQuitKeys(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'q'}},
		{Type: tea.KeyEsc},
		{Type: tea.KeyCtrlC},
	} {
		m, cmd := New("test").Update(key)
		if cmd == nil {
			t.Fatalf("key %q produced no command, want tea.Quit", key.String())
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("key %q did not produce tea.Quit", key.String())
		}
		if !m.(Model).quitting {
			t.Errorf("key %q left model not quitting", key.String())
		}
	}
}

func TestUnhandledKeyKeepsRunning(t *testing.T) {
	m, cmd := New("test").Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if cmd != nil {
		t.Error("unhandled key produced a command")
	}
	if m.(Model).quitting {
		t.Error("unhandled key set quitting")
	}
}

func TestViewMentionsVersion(t *testing.T) {
	if got := New("1.2.3").View(); !strings.Contains(got, "1.2.3") {
		t.Errorf("View() does not mention the version:\n%s", got)
	}
}
