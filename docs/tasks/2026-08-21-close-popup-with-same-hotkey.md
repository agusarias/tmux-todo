# Close The Popup With The Same Hotkey That Opens It

**Status:** in-progress
**Worktree:** ../todo-close-popup-hotkey

## Goal
Pressing the popup's own hotkey while the popup is open closes it. With the
default-ish root-table config (`@todo-key 'C-l'`, `@todo-key-table 'root'`),
`C-l` toggles the popup: same chord open and closed.

## Why
The popup is a glance surface — open it, read it, dismiss it. Reaching for `q`
or `esc` to close what one key opened is a break in the muscle memory, and the
hotkey is the key already under the finger. tmux cannot do this on its own:
while a `display-popup` has focus, keystrokes go to the program inside it, the
outer client's binding never fires, and tmux has no popup key table to bind a
closer in. Only `tdo` can act on the second press.

## Constraints
- The close key **follows `@todo-key`**, never a hardcoded `ctrl+l`. It is
  plumbed from the plugin into the popup's environment
  (`display-popup -E -e TDO_POPUP_KEY='<key>'`), so it costs no extra tmux
  round-trip on the popup's hot path (cold start stays well under 100ms).
- `internal/tui` stays environment-blind: the key arrives as a `tui.Config`
  field from `internal/cli`, which is the only place that reads the env.
- **Only plumbed when `@todo-key-table` is `root`.** With a `prefix` table the
  opening chord is `prefix`+key and the prefix cannot reach the popup, so the
  only available closer would be the bare key — a *different* key from the one
  that opened it, which is explicitly not wanted. Prefix installs keep `q`/`esc`
  and are otherwise unchanged.
- **Existing popup bindings win.** If `@todo-key` is a key the popup already
  uses (`a`, `d`, `e`, `s`, `g`, `r`, `u`, `j`, `k`, `1`-`3`, space), that key
  keeps doing its job and simply does not close the popup. Never shadow a
  feature to add a shortcut.
- `q`, `esc` and `ctrl+c` keep working exactly as they do today, in every mode.
- Nothing in the plugin's install path may fail a tmux server start. A key name
  that cannot be safely embedded (contains `'` or `"` — tmux single-quoted
  strings have no escape character) or cannot be translated to a Bubble Tea key
  string yields *no* close key rather than a broken keybind.
- Out of scope: documenting the close key in the `?` overlay (the overlay and
  footer have a proven width trap; adding a variable-length key name to them is
  its own task), and any two-key/prefix-chord state machine in `Update`.

## Critical surface
None in the taskflow sense — no migrations, no auth, no prod data, no external
side effects, no `tdo list --json` change. The one contract-ish edge is the
**plugin keybind body**: it is installed on every tmux server start, so a
malformed `bind-key` argument breaks the popup for every existing install. That
is what the "unsafe key name → omit the env" rule and the shell-harness
assertions are for.

## Definition of done
1. With `@todo-key 'C-l'` / `@todo-key-table 'root'`, pressing `C-l` inside the
   open popup closes it; pressing `C-l` again reopens it.
2. The four `display-popup` branches in `install_keybind` carry
   `-e TDO_POPUP_KEY='<key>'` when the table is `root`, and carry no
   `TDO_POPUP_KEY` at all when it is `prefix`.
3. A key name containing a quote installs a working keybind with no
   `TDO_POPUP_KEY`, and a tmux server start with such a config still succeeds.
4. `tui.Config` gains exactly one field for this, wired in `internal/cli` from
   `TDO_POPUP_KEY`, and `TestEveryTUIConfigFieldIsAsserted` has a real
   assertion for it (not a `nilIsCorrect`).
5. Translation is table-tested: `C-l` → `ctrl+l`, `M-t` → `alt+t`, a bare rune
   → itself, an unknown/unmappable name → `""` (no close key).
6. With the close key set, it quits from **all three modes** — normal, input and
   help — committing any queued deletes on the way out, exactly as `q` does.
7. A close key that is a plain printable rune does **not** quit from input mode:
   in the input row that keystroke is literal text (the "existing action wins"
   rule applied to typing). A chord (`ctrl+`/`alt+`/named key) does quit.
8. With no `TDO_POPUP_KEY` in the environment (a hand-run `tdo tui`, or a prefix
   install), no key beyond today's `q`/`esc`/`ctrl+c` closes the popup.
9. `make test`, `make lint` and `make test-plugin` are green.

## Verification
- `internal/tui` unit tests over `Update` for DoD 6, 7, 8 and the collision rule
  (a colliding close key still performs its normal-mode action and does not
  quit), including the queued-delete commit path out of input mode.
- `internal/cli`: table test for the translation (DoD 5) plus a wiring
  assertion that the field really comes from the environment — `t.Setenv` so the
  assertion has something to be wrong about, per the CLAUDE.md pitfall about
  vacuous environment guards.
- `test/plugin_install_test.sh`: assertions for DoD 2 and 3 against private
  `tmux -L` servers, in the style of the existing keybind assertions (awk over
  the whole key table, grep the bind body).
- End-to-end (DoD 1), the only check that proves the whole chain: the nested
  client trick from CLAUDE.md — attach a client inside a pane, send the hotkey,
  `capture-pane` shows the popup, send it again, `capture-pane` shows it gone.
  Captured output goes in the Evidence section.

## Plan
Approved at Checkpoint 1, 2026-08-21. The key travels plugin -> popup env ->
`tui.Config` -> `Update`; `internal/tui` learns nothing about tmux.

**1. `tmux-todo.tmux`, `install_keybind`.** After key/table resolution compute one
variable and interpolate it into all four `display-popup` branches:

```sh
popup_env=''
case $table:$key in
    root:*\'*|root:*\"*) warn "@todo-key '$key' cannot be passed to the popup; close-with-the-hotkey is off (q/esc still work)." ;;
    root:*) popup_env="-e TDO_POPUP_KEY='$key' " ;;
esac
# ... display-popup -E ${popup_env}-w 60 -h 15 '$TDO_BIN tui'
```

Empty for `prefix` tables and for unsafe names, so in those cases the bind body
stays byte-identical to today's.

**2. `internal/cli`: new `popupkey.go`, one line at `cli.go:151`.**
`popupKey(name string) string` translates a tmux key name to a Bubble Tea key
string: `C-x` -> `ctrl+x`, `M-x` -> `alt+x`, named keys through a small map
(`Enter`, `Escape`, `Tab`, `Space`, `BSpace`, `F1`-`F12`, arrows), a bare single
rune -> itself, anything else -> `""`. Then
`cfg.CloseKey = popupKey(os.Getenv("TDO_POPUP_KEY"))`.

**3. `internal/tui`: `Config.CloseKey` plus three call sites.** The check runs
*after* each mode's key switch, which is what makes "existing action wins"
structural rather than something each call site has to remember:

```go
// updateNormal, updateHelp: after the switch
if m.closesOn(msg) { return m.quit() }

// updateInput: after esc/tab/enter, before m.input.handleKey(msg)
if msg.Type != tea.KeyRunes && m.closesOn(msg) { return m.quit() }
```

`closesOn` is `m.cfg.CloseKey != "" && msg.String() == m.cfg.CloseKey`. Routing
through the existing `m.quit()` is what makes queued deletes commit on this path
for free.

**Sequencing.** Config field + `closesOn` and its unit tests -> `internal/cli`
translation + wiring assertion (`TestEveryTUIConfigFieldIsAsserted` fails until
that assertion exists, which is the forcing function) -> plugin `-e` + shell
harness assertions -> the nested-client end-to-end capture last, since it is the
only leg that needs all three pieces.

**What could go wrong.**
- The bind body is on the tmux-server-start path: a quote in `$key` would make
  the `bind-key` argument unparseable. Hence the `case` guard, plus a harness leg
  that starts a server with such a config and asserts it comes up with a working
  (env-less) bind.
- `msg.String()` may disagree with the translation table (e.g. Bubble Tea
  rendering `ctrl+space` as `ctrl+@`). Drive the real `Update` with a real
  `tea.KeyMsg` in the unit tests rather than asserting on strings alone;
  unmappable or mismatched names degrade to no close key, never to a wrong one.
- A vacuous env guard: `os.Getenv("TDO_POPUP_KEY")` is `""` on any CI runner, so
  the wiring assertion must `t.Setenv` to give itself something to be wrong
  about - the exact pitfall CLAUDE.md records for `TMUX`.
- `-e` support: tmux 3.7b here has it, and the plugin already assumes
  3.2-era `display-popup` features, so this adds no new version floor.

## Decisions
- **2026-08-21** — Close key follows `@todo-key`, plumbed via
  `display-popup -e TDO_POPUP_KEY`, not hardcoded and not re-read from tmux by
  `tdo`. Hardcoding drifts the moment the option changes; asking tmux costs a
  second ~5ms round-trip on the popup's hot path and would still be wrong for a
  hand-written bind.
- **2026-08-21** — Plumbed only for `@todo-key-table root`. In a `prefix` table
  the identical chord is unreachable inside the popup, and a bare-key closer is
  a different key from the opener — rejected by the user as not what "same
  hotkey" means.
- **2026-08-21** — Existing popup bindings take precedence over the close key;
  the close check runs *after* each mode's key switch. A colliding `@todo-key`
  loses the toggle, never the feature.
- **2026-08-21** — The close key quits from all three modes, discarding any
  half-typed input text: "the same key always closes" beats preserving a draft
  line. Its one exception is the printable-rune case in input mode (DoD 7),
  which follows from the precedence rule above — in the input row, typing *is*
  the existing action.
- **2026-08-21** — The `?` overlay is not updated to mention the close key. The
  footer/overlay width trap (CLAUDE.md) makes a variable-length key name there a
  real risk of silently eating the keymap, and it is not needed to make the
  feature work.
