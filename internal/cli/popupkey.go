package cli

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// popupKeyEnv is the variable the plugin's display-popup sets. It is an
// environment variable rather than a flag or a tmux query because the popup's
// hot path cannot afford a second subprocess, and because a hand-run `tdo tui`
// should get no close key at all — an unset variable says that with no special
// casing.
const popupKeyEnv = "TDO_POPUP_KEY"

// popupKey translates a tmux key name (@todo-key) into the Bubble Tea key string
// the popup will see, or "" when it cannot.
//
// "" is always a safe answer: it means "no close key", which is the behaviour
// every install had before this existed. A *wrong* answer is not safe — it would
// be a key that either never closes the popup or shadows one of its bindings —
// so anything unrecognised degrades rather than guesses.
//
// Every result is produced by asking Bubble Tea itself, via
// tea.KeyMsg.String(), rather than by writing the string out. That is deliberate:
// the value has to equal what Update compares against, and hand-written names
// disagree in ways nobody would predict. tmux's `C-Space` is the case that
// proves it — Bubble Tea renders that chord "ctrl+@", not "ctrl+space", because
// the terminal sends NUL. Deriving cannot get that wrong; a table would have.
func popupKey(name string) string {
	if name == "" {
		return ""
	}
	// Named keys are case-insensitive the way tmux writes them (Enter, enter),
	// but a bare rune is not: `T` and `t` are different keys.
	if alt, base, ok := strings.Cut(name, "-"); ok && len(base) > 0 {
		switch alt {
		case "C", "c":
			return ctrlKey(base)
		case "M", "m":
			return altKey(base)
		}
		// Anything else with a dash (S-, C-M-, a literal "a-b") is not a shape
		// this understands. No close key beats a wrong one.
		return ""
	}
	if t, ok := namedKeys[strings.ToLower(name)]; ok {
		return tea.KeyMsg{Type: t}.String()
	}
	// A bare single rune is itself. Counted in runes, not bytes, so a
	// multi-byte key name is one key and not three.
	if r := []rune(name); len(r) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: r}.String()
	}
	return ""
}

// ctrlKey renders C-<x>.
//
// Ctrl+letter is computed rather than tabulated: tea.KeyCtrlA is 1, so the type
// is simply the letter's offset from 'a'. That also means the collisions come
// out right for free — C-i IS Tab and C-m IS Enter at the terminal level, and
// String() reports them as "tab" and "enter", which is exactly what Update will
// see when the user presses them.
func ctrlKey(base string) string {
	if t, ok := ctrlNamedKeys[strings.ToLower(base)]; ok {
		return tea.KeyMsg{Type: t}.String()
	}
	r := []rune(strings.ToLower(base))
	if len(r) != 1 || r[0] < 'a' || r[0] > 'z' {
		return ""
	}
	return tea.KeyMsg{Type: tea.KeyType(r[0] - 'a' + 1)}.String()
}

// altKey renders M-<x>: a named key or a rune, with Alt set. Bubble Tea prefixes
// either with "alt+".
func altKey(base string) string {
	if t, ok := namedKeys[strings.ToLower(base)]; ok {
		return tea.KeyMsg{Type: t, Alt: true}.String()
	}
	if r := []rune(base); len(r) == 1 {
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: r, Alt: true}.String()
	}
	return ""
}

// namedKeys maps the tmux key names worth supporting to their Bubble Tea types.
// The names are tmux's spellings (BSpace, DC, PPage), with the common aliases
// alongside, because @todo-key is copied out of a user's tmux.conf.
//
// Deliberately not exhaustive: a name that is absent yields no close key, which
// is a working popup with q/esc. Adding one is a two-line change with a test.
var namedKeys = map[string]tea.KeyType{
	"enter":     tea.KeyEnter,
	"escape":    tea.KeyEsc,
	"esc":       tea.KeyEsc,
	"tab":       tea.KeyTab,
	"space":     tea.KeySpace,
	"bspace":    tea.KeyBackspace,
	"backspace": tea.KeyBackspace,
	"dc":        tea.KeyDelete,
	"delete":    tea.KeyDelete,
	"ic":        tea.KeyInsert,
	"insert":    tea.KeyInsert,
	"home":      tea.KeyHome,
	"end":       tea.KeyEnd,
	"ppage":     tea.KeyPgUp,
	"pgup":      tea.KeyPgUp,
	"npage":     tea.KeyPgDown,
	"pgdn":      tea.KeyPgDown,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
	"f1":        tea.KeyF1,
	"f2":        tea.KeyF2,
	"f3":        tea.KeyF3,
	"f4":        tea.KeyF4,
	"f5":        tea.KeyF5,
	"f6":        tea.KeyF6,
	"f7":        tea.KeyF7,
	"f8":        tea.KeyF8,
	"f9":        tea.KeyF9,
	"f10":       tea.KeyF10,
	"f11":       tea.KeyF11,
	"f12":       tea.KeyF12,
}

// ctrlNamedKeys are the C-<name> chords whose Bubble Tea type is not the
// letter-offset arithmetic. C-Space is the one that matters: the terminal sends
// NUL, so Bubble Tea calls it "ctrl+@".
var ctrlNamedKeys = map[string]tea.KeyType{
	"space": tea.KeyCtrlAt,
	"up":    tea.KeyCtrlUp,
	"down":  tea.KeyCtrlDown,
	"left":  tea.KeyCtrlLeft,
	"right": tea.KeyCtrlRight,
}
