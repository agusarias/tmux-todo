package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// fieldKey adds the one key no model test needs, so keyMsg stays the shared
// spelling of everything else.
func fieldKey(key string) tea.KeyMsg {
	if key == "ctrl+z" {
		return tea.KeyMsg{Type: tea.KeyCtrlZ}
	}
	return keyMsg(key)
}

// newField is a field wide enough that windowing never kicks in.
func newField(value string) field {
	f := field{}
	f.setWidth(80)
	f.setValue(value)
	return f
}

func TestFieldEditing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start string
		keys  []string
		want  string
		pos   int
	}{
		{"types at the end", "", []string{"a", "b", "c"}, "abc", 3},
		{"backspace deletes before the cursor", "abc", []string{"backspace"}, "ab", 2},
		{"backspace at the start is inert", "abc", []string{"left", "left", "left", "backspace"}, "abc", 0},
		{"left then type inserts mid-string", "ac", []string{"left", "b"}, "abc", 2},
		{"right walks forward", "abc", []string{"left", "left", "right"}, "abc", 2},
		{"right at the end is inert", "ab", []string{"right", "right"}, "ab", 2},
		{"home jumps to the start", "abc", []string{"home", "x"}, "xabc", 1},
		{"end jumps to the end", "abc", []string{"home", "end", "x"}, "abcx", 4},
		{"ctrl+u clears to the start", "abcdef", []string{"left", "left", "ctrl+u"}, "ef", 0},
		{"ctrl+k clears to the end", "abcdef", []string{"left", "left", "ctrl+k"}, "abcd", 4},
		{"delete removes under the cursor", "abc", []string{"home", "delete"}, "bc", 0},
		{"space is a character", "ab", []string{" ", "c"}, "ab c", 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newField(tc.start)
			for _, key := range tc.keys {
				f.handleKey(fieldKey(key))
			}
			if got := f.value(); got != tc.want {
				t.Errorf("value = %q, want %q", got, tc.want)
			}
			if f.pos != tc.pos {
				t.Errorf("cursor = %d, want %d", f.pos, tc.pos)
			}
		})
	}
}

// A pasted newline or tab would turn one row into two, which is the frame bug
// this package has shipped three times. They become spaces instead.
func TestFieldFlattensWhitespaceThatWouldWrap(t *testing.T) {
	f := newField("")
	f.insert([]rune("two\nlines\there"))
	if got := f.value(); got != "two lines here" {
		t.Errorf("value = %q, want the newline and tab flattened", got)
	}
	if strings.ContainsAny(f.view(), "\n\t") {
		t.Errorf("view still contains a row-breaking character: %q", f.view())
	}
}

// TestFieldViewNeverExceedsItsWidth — the field is one row inside the viewport,
// so an over-wide render wraps and the frame grows. The cursor has to stay
// visible whatever the text does, which is what the windowing is for.
func TestFieldViewNeverExceedsItsWidth(t *testing.T) {
	long := strings.Repeat("abcdefghij", 12)

	for _, width := range []int{1, 2, 5, 12, 40, 200} {
		for _, at := range []int{0, 1, 37, len(long) / 2, len(long)} {
			f := field{}
			f.setWidth(width)
			f.setValue(long)
			f.pos = at

			view := f.view()
			if w := lipgloss.Width(view); w > width {
				t.Errorf("width %d, cursor %d: view is %d columns: %q", width, at, w, view)
			}
			if strings.Contains(view, "\n") {
				t.Errorf("width %d, cursor %d: view spans rows", width, at)
			}
			if width >= 2 && view == "" {
				t.Errorf("width %d, cursor %d: nothing rendered", width, at)
			}
		}
	}
}

// The window follows the cursor: typing past the right edge must scroll rather
// than hide what is being typed.
func TestFieldWindowFollowsTheCursor(t *testing.T) {
	f := field{}
	f.setWidth(10)
	f.setValue("0123456789abcdefghij")

	f.pos = 0
	if got := f.view(); !strings.HasPrefix(got, "0") {
		t.Errorf("cursor at the start shows %q, want the head of the text", got)
	}
	f.pos = len(f.value())
	if got := f.view(); !strings.Contains(got, "j") {
		t.Errorf("cursor at the end shows %q, want the tail of the text", got)
	}
}

func TestFieldReportsWhatItConsumed(t *testing.T) {
	f := newField("")
	if !f.handleKey(fieldKey("a")) {
		t.Error("a printable rune was not consumed")
	}
	if f.handleKey(fieldKey("ctrl+z")) {
		t.Error("an unused key was reported as consumed")
	}
}
