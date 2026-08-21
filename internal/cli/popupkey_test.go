package cli

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// DoD 5. The table is the contract between the plugin's @todo-key and what the
// popup's Update compares against, so every row is also checked against Bubble
// Tea's own rendering of the corresponding KeyMsg — see
// TestPopupKeyAgreesWithWhatTheModelWouldSee below. A row that is merely
// self-consistent proves nothing.
func TestPopupKeyTranslation(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
	}{
		// The documented cases.
		{"ctrl chord", "C-l", "ctrl+l"},
		{"alt chord", "M-t", "alt+t"},
		{"a bare rune", "t", "t"},
		{"an unknown name", "F13", ""},

		// Case: the modifier and named keys are tmux spellings, so accept either
		// case; a bare rune is case-SENSITIVE because `T` and `t` differ.
		{"lowercase modifier", "c-l", "ctrl+l"},
		{"lowercase alt modifier", "m-t", "alt+t"},
		{"ctrl of an uppercase letter", "C-L", "ctrl+l"},
		{"an uppercase rune stays uppercase", "T", "T"},
		{"a named key, tmux spelling", "Enter", "enter"},
		{"a named key, lowercased", "enter", "enter"},

		// Named keys.
		{"escape", "Escape", "esc"},
		{"esc alias", "Esc", "esc"},
		{"tab", "Tab", "tab"},
		{"space", "Space", " "},
		{"tmux's BSpace", "BSpace", "backspace"},
		{"tmux's DC", "DC", "delete"},
		{"tmux's PPage", "PPage", "pgup"},
		{"an arrow", "Up", "up"},
		{"a function key", "F5", "f5"},
		{"the last function key", "F12", "f12"},

		// The case the plan flagged and a hand-written table would have got
		// wrong: the terminal sends NUL for ctrl+space, so Bubble Tea calls it
		// "ctrl+@".
		{"ctrl+space is ctrl+@", "C-Space", "ctrl+@"},
		// The terminal-level collisions, which come out right because the ctrl
		// type is computed rather than named.
		{"C-i is tab", "C-i", "tab"},
		{"C-m is enter", "C-m", "enter"},
		{"C-c", "C-c", "ctrl+c"},

		// Alt with a named key.
		{"alt of a named key", "M-Up", "alt+up"},
		{"alt of an arrow, lowercased", "m-left", "alt+left"},

		// Everything unrecognised degrades to no close key. A wrong key is worse
		// than none: it would either never fire or shadow a popup binding.
		{"empty", "", ""},
		{"ctrl with no key", "C-", ""},
		{"alt with no key", "M-", ""},
		{"ctrl of a non-letter", "C-1", ""},
		{"ctrl of a word", "C-nope", ""},
		{"a shift chord", "S-Tab", ""},
		{"a double modifier", "C-M-x", ""},
		{"a multi-rune name", "nope", ""},
		{"a stray dash", "a-b", ""},
		{"just a dash", "-", "-"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := popupKey(tc.in); got != tc.want {
				t.Errorf("popupKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// The translation is only worth anything if it equals what the model actually
// sees. This drives the real tea.KeyMsg for each chord and compares its String()
// with the translation — which is the assertion that would have caught a
// hand-written "ctrl+space".
func TestPopupKeyAgreesWithWhatTheModelWouldSee(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		msg  tea.KeyMsg
	}{
		{"C-l", "C-l", tea.KeyMsg{Type: tea.KeyCtrlL}},
		{"C-c", "C-c", tea.KeyMsg{Type: tea.KeyCtrlC}},
		{"C-Space", "C-Space", tea.KeyMsg{Type: tea.KeyCtrlAt}},
		{"C-i", "C-i", tea.KeyMsg{Type: tea.KeyTab}},
		{"M-t", "M-t", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}, Alt: true}},
		{"M-Up", "M-Up", tea.KeyMsg{Type: tea.KeyUp, Alt: true}},
		{"t", "t", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}}},
		{"Space", "Space", tea.KeyMsg{Type: tea.KeySpace}},
		{"Enter", "Enter", tea.KeyMsg{Type: tea.KeyEnter}},
		{"F5", "F5", tea.KeyMsg{Type: tea.KeyF5}},
		{"Up", "Up", tea.KeyMsg{Type: tea.KeyUp}},
		{"BSpace", "BSpace", tea.KeyMsg{Type: tea.KeyBackspace}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, want := popupKey(tc.in), tc.msg.String()
			if got != want {
				t.Errorf("popupKey(%q) = %q, but pressing that key produces %q — the popup"+
					" would never close", tc.in, got, want)
			}
			if got == "" {
				t.Errorf("popupKey(%q) is empty, so the key would never close the popup", tc.in)
			}
		})
	}
}

// Every entry in the named tables has to render to something, or it is a row
// that silently means "no close key" while looking supported.
func TestEveryNamedKeyRendersToSomething(t *testing.T) {
	for name, typ := range namedKeys {
		if s := (tea.KeyMsg{Type: typ}).String(); s == "" {
			t.Errorf("namedKeys[%q] renders to the empty string", name)
		}
		if got := popupKey(name); got == "" {
			t.Errorf("popupKey(%q) is empty although the name is in namedKeys", name)
		}
	}
	for name, typ := range ctrlNamedKeys {
		if s := (tea.KeyMsg{Type: typ}).String(); s == "" {
			t.Errorf("ctrlNamedKeys[%q] renders to the empty string", name)
		}
		if got := popupKey("C-" + name); got == "" {
			t.Errorf("popupKey(\"C-%s\") is empty although the name is in ctrlNamedKeys", name)
		}
	}
}

// Every ctrl+letter must translate, and to something distinct — the arithmetic
// is the part most likely to be quietly off by one.
func TestCtrlOfEveryLetterTranslates(t *testing.T) {
	seen := map[string]string{}
	for c := 'a'; c <= 'z'; c++ {
		name := "C-" + string(c)
		got := popupKey(name)
		if got == "" {
			t.Errorf("popupKey(%q) is empty", name)
			continue
		}
		if prev, dup := seen[got]; dup {
			t.Errorf("popupKey(%q) and popupKey(%q) both give %q", prev, name, got)
		}
		seen[got] = name
		// And it agrees with the key the model would see.
		want := (tea.KeyMsg{Type: tea.KeyType(c - 'a' + 1)}).String()
		if got != want {
			t.Errorf("popupKey(%q) = %q, want %q", name, got, want)
		}
	}
}
