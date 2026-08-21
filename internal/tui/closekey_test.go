package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ctrlL is the close key these tests configure. It is a chord rather than a
// letter so it collides with nothing the popup already binds — the collision
// case has its own test below.
const ctrlL = "ctrl+l"

func ctrlLMsg() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyCtrlL} }

// closeCfg is a loaded model whose close key is set, so every test below asks
// the same question of the same shape of model.
func closeCfg(t *testing.T, key string) Model {
	t.Helper()
	db := openDB(t)
	add(t, db, "a task", globalScope)
	return newLoaded(t, Config{DB: db, Scopes: allScopes(), CloseKey: key})
}

// DoD 6, normal mode. The assertion is on the real exit path — the command the
// key produced must be tea.Quit — not merely on a flag, because a model that set
// quitting without returning a command would leave the popup on screen.
func TestCloseKeyQuitsFromNormalMode(t *testing.T) {
	m := closeCfg(t, ctrlL)

	next, cmd := m.Update(ctrlLMsg())
	out := next.(Model)
	if !out.quitting {
		t.Error("the close key left the model not quitting")
	}
	if cmd == nil {
		t.Fatal("the close key produced no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("the close key produced %T, want tea.Quit", cmd())
	}
}

// DoD 6, help mode. It quits outright rather than dismissing the overlay: the
// point of the feature is that this key always closes the popup.
func TestCloseKeyQuitsFromHelpMode(t *testing.T) {
	m := closeCfg(t, ctrlL)
	m, _ = press(t, m, "?")
	if m.mode != modeHelp {
		t.Fatalf("mode = %v, want modeHelp", m.mode)
	}

	next, cmd := m.Update(ctrlLMsg())
	out := next.(Model)
	if !out.quitting {
		t.Error("the close key did not quit from the help overlay")
	}
	if out.mode == modeNormal && !out.quitting {
		t.Error("the close key merely dismissed the overlay")
	}
	if cmd == nil {
		t.Fatal("no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("produced %T, want tea.Quit", cmd())
	}
}

// `q` must still *dismiss* from help rather than quit — the switch runs before
// the close check, so today's behaviour is untouched.
func TestHelpStillDismissesOnQ(t *testing.T) {
	m := closeCfg(t, ctrlL)
	m, _ = press(t, m, "?")
	m, cmd := press(t, m, "q")
	if m.mode != modeNormal {
		t.Errorf("mode = %v, want modeNormal — q should dismiss the overlay", m.mode)
	}
	if m.quitting {
		t.Error("q quit from the help overlay instead of dismissing it")
	}
	if cmd != nil {
		t.Errorf("q in help produced %T, want no command", cmd())
	}
}

// DoD 6, input mode, with the queued-delete commit path — the reason quit() is
// routed through rather than tea.Quit being returned directly. A close key that
// exited without committing would silently drop the user's deletes.
func TestCloseKeyQuitsFromInputModeAndCommitsQueuedDeletes(t *testing.T) {
	db := openDB(t)
	add(t, db, "older", globalScope)
	add(t, db, "newer", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes(), CloseKey: ctrlL})

	// Queue a delete on the cursor row, whatever it is — the list is newest
	// first, so hardcoding which task that is only invites the assertion to be
	// written backwards.
	doomed, ok := m.selectedTask()
	if !ok {
		t.Fatal("no row under the cursor")
	}
	m, _ = press(t, m, "d")
	if len(m.queued) != 1 {
		t.Fatalf("queued = %v, want one row queued", m.queued)
	}
	m, _ = press(t, m, "a")
	if m.mode != modeInput {
		t.Fatalf("mode = %v, want modeInput", m.mode)
	}
	m, _ = press(t, m, "x")
	if m.input.value() == "" {
		t.Fatal("nothing was typed into the input row")
	}

	// The close key from inside the input row: quits, and the queued delete is
	// committed on the way out.
	next, cmd := m.Update(ctrlLMsg())
	m = next.(Model)
	if !m.quitting {
		t.Error("the close key did not quit from input mode")
	}
	if cmd == nil {
		t.Fatal("no command on exit")
	}
	msg := cmd()
	if _, ok := msg.(deletesCommittedMsg); !ok {
		t.Fatalf("exit produced %T, want the queued deletes to be committed", msg)
	}
	settled, quit := m.Update(msg)
	if settled.(Model).commitErr != nil {
		t.Errorf("commit failed: %v", settled.(Model).commitErr)
	}
	if quit == nil {
		t.Fatal("the commit produced no quit")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Errorf("the commit produced %T, want tea.Quit", quit())
	}
	got := texts(listAll(t, db))
	if len(got) != 1 || got[0] == doomed.Text {
		t.Errorf("store holds %v, want the queued row (%q) gone — the close key exited"+
			" without committing the delete queue", got, doomed.Text)
	}
}

// DoD 7. A printable rune as the close key is literal text in the input row, so
// it must type rather than quit — the same "existing action wins" rule normal
// mode applies. Space is in the table because tea.KeySpace is NOT tea.KeyRunes:
// a check written against KeyRunes alone would quit the popup instead of
// inserting a space, and that is the bug this table exists to catch.
func TestPrintableCloseKeyTypesInInputModeInsteadOfQuitting(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     string
		msg     tea.KeyMsg
		wantVal string
	}{
		{"a letter", "x", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}, "x"},
		{"a digit", "7", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'7'}}, "7"},
		{"space", " ", tea.KeyMsg{Type: tea.KeySpace}, " "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			add(t, db, "a task", globalScope)
			m := newLoaded(t, Config{DB: db, Scopes: allScopes(), CloseKey: tc.key})
			m, _ = press(t, m, "a")
			if m.mode != modeInput {
				t.Fatalf("mode = %v, want modeInput", m.mode)
			}

			next, cmd := m.Update(tc.msg)
			out := next.(Model)
			if out.quitting {
				t.Fatalf("close key %q quit from the input row; in the input row typing is"+
					" the existing action and must win", tc.key)
			}
			if cmd != nil {
				if _, ok := cmd().(tea.QuitMsg); ok {
					t.Fatalf("close key %q produced tea.Quit from the input row", tc.key)
				}
			}
			if out.input.value() != tc.wantVal {
				t.Errorf("input value = %q, want %q — the keystroke was swallowed", out.input.value(), tc.wantVal)
			}
		})
	}
}

// ...but a chord does quit from the input row, which is the other half of DoD 7.
func TestChordCloseKeyQuitsFromInputMode(t *testing.T) {
	for _, tc := range []struct {
		name string
		key  string
		msg  tea.KeyMsg
	}{
		{"ctrl chord", "ctrl+l", tea.KeyMsg{Type: tea.KeyCtrlL}},
		{"alt chord", "alt+t", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'t'}, Alt: true}},
		{"a named key", "f5", tea.KeyMsg{Type: tea.KeyF5}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			add(t, db, "a task", globalScope)
			m := newLoaded(t, Config{DB: db, Scopes: allScopes(), CloseKey: tc.key})
			m, _ = press(t, m, "a")

			next, cmd := m.Update(tc.msg)
			if !next.(Model).quitting {
				t.Errorf("close key %q did not quit from the input row", tc.key)
			}
			if cmd == nil {
				t.Fatal("no command")
			}
			if _, ok := cmd().(tea.QuitMsg); !ok {
				t.Errorf("produced %T, want tea.Quit", cmd())
			}
		})
	}
}

// The collision rule, in normal mode: a close key that is already a popup
// binding keeps doing its old job and does NOT close. Never shadow a feature to
// add a shortcut.
func TestCollidingCloseKeyKeepsItsExistingAction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		key    string
		assert func(t *testing.T, before, after Model)
	}{{
		name: "a opens the input row",
		key:  "a",
		assert: func(t *testing.T, _, after Model) {
			if after.mode != modeInput {
				t.Errorf("mode = %v, want modeInput — `a` stopped adding", after.mode)
			}
		},
	}, {
		name: "d queues a delete",
		key:  "d",
		assert: func(t *testing.T, _, after Model) {
			if len(after.queued) != 1 {
				t.Errorf("queued = %v, want one — `d` stopped queueing", after.queued)
			}
		},
	}, {
		name: "? opens the help overlay",
		key:  "?",
		assert: func(t *testing.T, _, after Model) {
			if after.mode != modeHelp {
				t.Errorf("mode = %v, want modeHelp", after.mode)
			}
		},
	}, {
		name: "j moves the cursor",
		key:  "j",
		assert: func(t *testing.T, before, after Model) {
			if after.cursor == before.cursor {
				t.Errorf("cursor stayed at %d — `j` stopped moving", after.cursor)
			}
		},
	}, {
		name: "1 toggles the session filter",
		key:  "1",
		assert: func(t *testing.T, before, after Model) {
			if after.filter == before.filter {
				t.Error("the filter did not change — `1` stopped filtering")
			}
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			db := openDB(t)
			// Two rows in two *listed* scopes: one row cannot move a cursor, and
			// a scope absent from Config.Scopes is not listed at all — either
			// would make the `j` and `1` legs pass without pressing anything.
			add(t, db, "one", globalScope)
			add(t, db, "two", sessionScope)
			before := newLoaded(t, Config{DB: db, Scopes: allScopes(), CloseKey: tc.key})
			if len(before.rows) < 2 {
				t.Fatalf("only %d rows: the cursor and filter legs would be vacuous", len(before.rows))
			}

			after, _ := press(t, before, tc.key)
			if after.quitting {
				t.Fatalf("close key %q quit the popup; the existing binding must win", tc.key)
			}
			tc.assert(t, before, after)
		})
	}
}

// `q` in normal mode is a collision too, and it is the one that would look fine:
// both actions quit. Asserted anyway, because the path taken must still be the
// switch's — the close check is unreachable for it.
func TestQAsTheCloseKeyStillQuitsThroughTheNormalPath(t *testing.T) {
	m := closeCfg(t, "q")
	next, cmd := press(t, m, "q")
	if !next.quitting {
		t.Error("q did not quit")
	}
	if cmd == nil {
		t.Fatal("no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Errorf("produced %T, want tea.Quit", cmd())
	}
}

// DoD 8. With no close key configured — a hand-run `tdo tui`, or a prefix-table
// install — nothing beyond q/esc/ctrl+c closes the popup.
//
// This is the test that catches the empty-string bug in closesOn: without the
// `CloseKey != ""` guard every unhandled key matches "" and quits.
func TestNoCloseKeyMeansNoExtraKeyCloses(t *testing.T) {
	db := openDB(t)
	add(t, db, "a task", globalScope)
	m := newLoaded(t, Config{DB: db, Scopes: allScopes()}) // CloseKey unset

	for _, key := range []string{"x", "z", "ctrl+l", "f5", "?", "j", "1", " "} {
		next, cmd := m.Update(keyMsgOrRune(key))
		if next.(Model).quitting {
			t.Errorf("%q quit the popup with no CloseKey configured", key)
		}
		if cmd != nil {
			if _, ok := cmd().(tea.QuitMsg); ok {
				t.Errorf("%q produced tea.Quit with no CloseKey configured", key)
			}
		}
	}

	// ...and the three that always closed still do.
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		next, cmd := press(t, m, key)
		if !next.quitting {
			t.Errorf("%q stopped quitting", key)
		}
		if cmd == nil {
			t.Fatalf("%q produced no command", key)
		}
	}
}

// keyMsgOrRune extends the shared keyMsg helper with the few chords these tests
// need, so a plain letter still goes through the same path as everywhere else.
func keyMsgOrRune(key string) tea.KeyMsg {
	switch key {
	case "ctrl+l":
		return tea.KeyMsg{Type: tea.KeyCtrlL}
	case "f5":
		return tea.KeyMsg{Type: tea.KeyF5}
	case " ":
		return tea.KeyMsg{Type: tea.KeySpace}
	}
	return keyMsg(key)
}

// isTextKey has two callers that must agree, so it is pinned directly: the input
// row inserts exactly these, and updateInput declines to quit for exactly these.
func TestIsTextKeyCoversWhatTheInputRowTypes(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  tea.KeyMsg
		want bool
	}{
		{"a rune", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}}, true},
		{"space", tea.KeyMsg{Type: tea.KeySpace}, true},
		{"ctrl chord", tea.KeyMsg{Type: tea.KeyCtrlL}, false},
		{"enter", tea.KeyMsg{Type: tea.KeyEnter}, false},
		{"backspace", tea.KeyMsg{Type: tea.KeyBackspace}, false},
		{"a named key", tea.KeyMsg{Type: tea.KeyF5}, false},
		{"an arrow", tea.KeyMsg{Type: tea.KeyLeft}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isTextKey(tc.msg); got != tc.want {
				t.Errorf("isTextKey(%s) = %v, want %v", tc.msg.String(), got, tc.want)
			}
			// The other half: the field must actually insert exactly when
			// isTextKey says it would.
			f := &field{}
			f.handleKey(tc.msg)
			inserted := f.value() != ""
			if inserted != tc.want {
				t.Errorf("field inserted = %v but isTextKey said %v — the two definitions"+
					" have drifted apart", inserted, tc.want)
			}
		})
	}
}
