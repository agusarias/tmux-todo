package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// field is the popup's one-line text editor.
//
// It is hand-rolled rather than bubbles/textinput, which the plan expected to be
// free: bubbles v1.0.0 is already required, but textinput imports
// github.com/atotto/clipboard, which is in neither go.sum nor the module cache.
// Using it would mean a new module in the build graph — the one thing this
// task's constraints rule out — for a single-line field with no clipboard, no
// placeholder, no suggestions and no validation. What is left is small enough to
// be a pure value type, which also makes every editing rule directly assertable
// without a Bubble Tea program.
//
// Deliberately absent: word motions, kill-ring, undo. This edits task titles.
type field struct {
	runes []rune
	// pos is the cursor, in runes, from 0 to len(runes) inclusive — one past
	// the end is where typing appends.
	pos int
	// width is the display columns the field may use. The text scrolls
	// horizontally inside it rather than wrapping, because the row must stay
	// one row: see chromeHeight.
	width int
}

var cursorStyle = lipgloss.NewStyle().Reverse(true)

// value returns the text as typed, untrimmed. Trimming is a decision the caller
// makes at submit time and differs between add and edit.
func (f field) value() string { return string(f.runes) }

// setValue replaces the text and parks the cursor at the end, which is where an
// edit wants it.
func (f *field) setValue(s string) {
	f.runes = []rune(s)
	f.pos = len(f.runes)
}

func (f *field) reset() {
	f.runes = nil
	f.pos = 0
}

func (f *field) setWidth(w int) {
	if w < 1 {
		w = 1
	}
	f.width = w
}

// handleKey applies one keystroke. It reports whether the key was consumed as
// editing, so a caller can tell "typed a character" from "pressed a key this
// field has no use for".
//
// The keys the caller intercepts first — enter, tab, esc — never reach here.
func (f *field) handleKey(msg tea.KeyMsg) bool {
	if isTextKey(msg) {
		runes := msg.Runes
		if msg.Type == tea.KeySpace {
			runes = []rune{' '}
		}
		f.insert(runes)
		return true
	}
	switch msg.Type {
	case tea.KeyBackspace:
		if f.pos > 0 {
			f.runes = append(f.runes[:f.pos-1], f.runes[f.pos:]...)
			f.pos--
		}
		return true

	case tea.KeyDelete:
		if f.pos < len(f.runes) {
			f.runes = append(f.runes[:f.pos], f.runes[f.pos+1:]...)
		}
		return true

	case tea.KeyLeft:
		if f.pos > 0 {
			f.pos--
		}
		return true

	case tea.KeyRight:
		if f.pos < len(f.runes) {
			f.pos++
		}
		return true

	case tea.KeyHome, tea.KeyCtrlA:
		f.pos = 0
		return true

	case tea.KeyEnd, tea.KeyCtrlE:
		f.pos = len(f.runes)
		return true

	case tea.KeyCtrlU:
		f.runes = append([]rune(nil), f.runes[f.pos:]...)
		f.pos = 0
		return true

	case tea.KeyCtrlK:
		f.runes = f.runes[:f.pos]
		return true
	}
	return false
}

// insert types runes at the cursor.
func (f *field) insert(runes []rune) {
	if len(runes) == 0 {
		return
	}
	// Newlines and tabs would break the one-row invariant; a bracketed paste can
	// deliver both.
	clean := make([]rune, 0, len(runes))
	for _, r := range runes {
		switch r {
		case '\n', '\r', '\t':
			clean = append(clean, ' ')
		default:
			clean = append(clean, r)
		}
	}
	out := make([]rune, 0, len(f.runes)+len(clean))
	out = append(out, f.runes[:f.pos]...)
	out = append(out, clean...)
	out = append(out, f.runes[f.pos:]...)
	f.runes = out
	f.pos += len(clean)
}

// view renders the visible window of the text with the cursor drawn on it.
//
// The window is chosen so the cursor is always on screen, scrolled from the left
// when the text is longer than the field. The result is at most width columns —
// asserted, because a row that overflows is a row that wraps, and a wrapped row
// makes the frame taller than chromeHeight promises.
func (f field) view() string {
	if f.width <= 0 {
		return ""
	}
	// One extra cell so the cursor has somewhere to sit past the last rune.
	cells := append(append([]rune(nil), f.runes...), ' ')
	pos := f.pos
	if pos > len(cells)-1 {
		pos = len(cells) - 1
	}

	var b strings.Builder
	used := 0
	for i := f.windowStart(cells, pos); i < len(cells); i++ {
		cell := string(cells[i])
		w := lipgloss.Width(cell)
		if used+w > f.width {
			break
		}
		used += w
		if i == pos {
			b.WriteString(cursorStyle.Render(cell))
		} else {
			b.WriteString(cell)
		}
	}
	return b.String()
}

// windowStart is the first rune to draw: far enough back that the cursor cell
// still fits inside width.
func (f field) windowStart(cells []rune, pos int) int {
	used := 0
	for i := pos; i >= 0; i-- {
		used += lipgloss.Width(string(cells[i]))
		if used > f.width {
			return i + 1
		}
	}
	return 0
}

// isTextKey reports whether handleKey would treat msg as literal text to insert.
//
// It is a named predicate rather than an inline case list because a second caller
// needs the same answer: the close-key check in updateInput must not fire for a
// key the row would type. Two copies of "runes or space" would be two chances to
// forget tea.KeySpace, and forgetting it means a @todo-key of Space quits the
// popup instead of inserting a space.
//
// The Alt check is the non-obvious half. Bubble Tea reports alt+t as
// KeyRunes{'t'} with Alt set, so a type-only test calls a chord "text" — which
// both made alt+<rune> insert a bare letter into the input row and would have
// stopped an alt close key working there. A modifier means it is not typing.
func isTextKey(msg tea.KeyMsg) bool {
	if msg.Alt {
		return false
	}
	return msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace
}
