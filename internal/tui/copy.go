package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// The `y` copy.
//
// The popup decides *what* to copy; internal/cli decides *how*. That split is
// the same one jump.go draws, and for the same reason: the copy is a subprocess
// inside tmux and a raw escape sequence outside it, and this package runs
// neither. What is different here is the timing — the jump happens after Run
// returns, while the copy has to happen mid-popup, because the confirmation on
// the title line is half the feature. So Config.Copy is a func rather than an
// intent handed back at exit.
//
// The payload is the task's text verbatim: no id, no scope prefix, none of the
// rendered row's column padding or strikethrough. What lands in someone's paste
// buffer should be what they would have retyped.

// copiedMsg is the result of one Copy call, delivered back into Update so the
// notice is set on the model rather than from inside a command goroutine.
type copiedMsg struct {
	// text is what was handed to Copy — echoed back rather than re-read from
	// the cursor, because the list can have reloaded in between and the notice
	// must name the text that was actually copied.
	text string
	err  error
}

// copySelected copies the task under the cursor.
//
// It is a no-op — no call, no message, nothing on screen — when there is
// nothing to copy: an empty list, the cursor on an all-tasks group header, or
// no Copy injected at all. A notice in any of those cases would claim something
// happened.
func (m Model) copySelected() (tea.Model, tea.Cmd) {
	t, ok := m.selectedTask()
	if !ok || m.cfg.Copy == nil {
		return m, nil
	}
	return m, copyCmd(m.cfg.Copy, t.Text)
}

// copyCmd runs the injected copy off the update loop.
//
// The func is captured as an argument rather than read off m.cfg inside the
// closure, so the command cannot observe a later model.
func copyCmd(copy func(string) error, text string) tea.Cmd {
	return func() tea.Msg { return copiedMsg{text: text, err: copy(text)} }
}

// copied folds a finished copy into the notice.
func (m Model) copied(msg copiedMsg) Model {
	if msg.err != nil {
		// Says what failed rather than reporting success. Outside tmux a
		// terminal that ignores OSC 52 is indistinguishable from one that
		// honoured it, so this can only ever report what tdo itself could not
		// do — which is the honest limit of the feature, not a gap in it.
		m.notice, m.noticeErr = "copy failed: "+msg.err.Error(), true
		return m
	}
	m.notice, m.noticeErr = "copied: "+msg.text, false
	return m
}

// oneLine collapses every run of whitespace to a single space.
//
// For the *message* only — never the payload; the text handed to Copy is
// untouched. A task's text can hold a newline, and a newline on the title line
// would make it two rows, which is exactly the arithmetic that has broken this
// frame three times. Truncation handles width; nothing but this handles height.
//
// It is applied by titleLine, at the moment the notice becomes a frame line,
// rather than here where the notice is set. That is deliberate: the invariant
// chromeHeight depends on is "the line this renders is one row", and pinning it
// to the renderer makes it hold for every notice, not just the ones this file
// produces. TestFrameNeverExceedsThePane's "copied hostile" mode sets the field
// directly for exactly that reason, and it caught this being in the wrong place.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
