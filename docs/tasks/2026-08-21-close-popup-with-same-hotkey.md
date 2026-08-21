# Close The Popup With The Same Hotkey That Opens It

**Status:** review
**Worktree:** none (merged; worktree removed)

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

## Decisions (executor, 2026-08-21)

**The translation is derived from Bubble Tea, not written out.** The plan flagged that
`msg.String()` might disagree with a hand-written table and named `ctrl+space` as the likely
case. Measured: it does — `C-Space` is `ctrl+@`, because the terminal sends NUL. So `popupKey`
constructs the `tea.KeyMsg` and asks it for its own `String()`, and ctrl+letter is computed as
`tea.KeyType(c-'a'+1)` rather than tabulated. That also makes the terminal-level collisions come
out right for free: `C-i` is `tab`, `C-m` is `enter`. A hand table would have got all three
wrong, and each would have shipped as "the popup just never closes".

**`isTextKey`, not `msg.Type != tea.KeyRunes`.** The plan's input-mode guard is one case short in
each direction, and both were found by tests rather than by reading:

- `tea.KeySpace` is *not* `tea.KeyRunes`, but `field.handleKey` inserts it. With the plan's
  check, a `@todo-key` of `Space` quits the popup instead of typing a space.
- An Alt-modified rune *is* `KeyRunes`, so `alt+t` counted as text and an alt close key silently
  did nothing in the input row. Fixing that also stopped `alt+<rune>` inserting a bare letter,
  which it had been doing since the field was written.

Both live in one predicate in `field.go`, beside the code that inserts, so the two callers cannot
drift; `TestIsTextKeyCoversWhatTheInputRowTypes` asserts the predicate and the field agree row by
row. Both directions are mutation-proven below.

**DoD 3's premise needed a correction, not a scope change.** The first harness leg used
`@todo-key "C-l'x"` and found tmux rejects it: `unknown key`. But tmux rejects `C-lx` too, so the
quote had nothing to do with it — that was an invalid *key name*, not a quoted one. `'`, `"`,
`M-'` and `C-"` are all real, bindable keys, and with those the DoD is satisfied exactly as
written. No `blocked` needed.

**`plugin_keybinds_for` now unquotes field 4.** tmux re-quotes a key name when printing it and
picks the spelling per key (`t` bare, `'` as `\'`, `M-'` as `"M-'"`, `C-"` as `'C-"'`), so the
existing `awk '$4 == k'` answered "not bound" for a key that was bound. That fails in the
*passing* direction, so it was worth fixing in the shared helper rather than around it; it is a
no-op for every pre-existing case.

**A pre-existing flake in the rename cases was fixed on the way past.** Not this task's feature,
but this task's repeated harness runs surfaced it: `seed_session_task` drove an interactive shell
with `send-keys`, and its readiness handshake timed out under load, failing about one run in
three. The cause was a wrong belief recorded in the previous task — that `$TMUX_PANE` is unset
for a pane's initial command. It is set (`%1`); the probe behind that claim ran
`printenv TMUX TMUX_PANE` and got only the first variable back. Seeding is now the pane's own
command, with no shell to race. Four consecutive clean runs, and the rename mutation proof still
discriminates, so determinism was not bought by making those cases vacuous. CLAUDE.md's pitfall
is corrected rather than deleted.

**The `?` overlay is still not updated**, per the brief's own decision.

## Evidence

Verified in `../todo-close-popup-hotkey` on tmux 3.7b, go 1.26, darwin/arm64,
bubbletea v1.3.10. Merged into local `main` as **29f60da**; the feature commit is
**5edffe3**.

### DoD 1 — the whole chain, end to end

`test/plugin_install_test.sh`, case `closekey-9`. The real plugin installs the real bind, the real
binary runs in the popup, and the client is manufactured with the nested-attach trick (a pane
whose own command is `tmux attach` is a real 80x24 client). "The popup is on screen" is a grep for
a task text only the popup can be showing.

```
== closekey-9-the-hotkey-toggles-the-popup
    ok   the root C-l binding is installed
    ok   the popup is not on screen to begin with
    ok   C-l opens the popup
    -- capture with the popup open:
       6:         || tdo                                                   ||
       8:         || > o ZZCANARYZZ                              (global)   ||
    ok   C-l again closes it
    ok   and C-l reopens it, so the key toggles
    ok   q still closes the popup too
```

The same thing by hand, with the frame visible:

```
=== press C-l (open) ===
   |*        +----------------------------------------------------------+
   |         | ,--------------------------------------------------------.|
   |         | |  tdo                                                   ||
   |         | |  > o CANARYTASK                              (global)  ||
popup visible: yes

=== press C-l again (close) ===
   |*
popup visible: no

=== press C-l a third time (reopen) ===
popup visible: yes
```

**Proven able to fail.** With `CloseKey` never wired (`cfg.CloseKey = ""`):

```
### MUTANT A: CloseKey never wired ###
    ok   C-l opens the popup
    FAIL C-l again closes it (want 'no', got 'yes')
--- FAIL: TestTUIConfigWiring/CloseKey
```

That mutation also exposed a weak assertion of my own: the reopen leg reported a cheerful `ok`
next to the failure, because a popup that never closed was still open. It is now guarded on the
close having happened, and prints `-- skipped the reopen check` instead.

### DoD 2 — the four branches carry the key on root, nothing on prefix

```
== closekey-1-root-carries-the-key
    ok   the root binding is installed
    ok   four display-popup size branches, as before
    ok   every branch carries TDO_POPUP_KEY
    ok   and it carries the configured key
== closekey-2-follows-the-option
    ok   TDO_POPUP_KEY follows @todo-key        (M-w, not a hardcoded C-l)
    ok   the key is not hardcoded
== closekey-3-prefix-carries-nothing
    ok   four display-popup branches
    ok   and NONE of them carries TDO_POPUP_KEY
== closekey-4-default-install-carries-nothing
    ok   the default (prefix t) install passes no TDO_POPUP_KEY
== closekey-5-invalid-table-carries-nothing
    ok   falls back to a prefix binding
    ok   and passes no TDO_POPUP_KEY
```

The branch count is asserted against the *measured* number of `display-popup` occurrences rather
than a literal 4, so a fifth size branch cannot silently go unplumbed.

### DoD 3 — a quote in the key name breaks nothing

Four real, bindable quote-containing keys, plus a server started from such a config:

```
== closekey-6-quote-in-key-apostrophe   (and -quote, -alt-apostrophe, -ctrl-quote)
    ok   the script says why the close key is off
    ok   the keybind is still installed
    ok   all four popup branches survive
    ok   and carry no TDO_POPUP_KEY
    ok   the tmux server is still answering

== closekey-7-server-starts-with-a-quoted-key
    ok   a server whose config sets a quoted @todo-key starts
    ok   and the install warns about the key
    ok   and still binds the popup to it
    ok   with no TDO_POPUP_KEY
```

### DoD 4 — one Config field, asserted for real

`TestEveryTUIConfigFieldIsAsserted` failed the moment `CloseKey` was added, exactly as the plan
predicted, and it is now a real assertion rather than a `nilIsCorrect`: the fixture exports a
*tmux* spelling (`C-l`) and the Config must carry the *Bubble Tea* one, so plumbing and
translation are covered at once. It also fails if `CloseKey` ever equals the raw tmux name, which
would be a key the popup never sees. `t.Setenv` gives it something to be wrong about — the
CLAUDE.md pitfall about `os.Getenv` returning `""` on every CI runner. Two extra legs cover the
absent (`TestTUIConfigWiringWithNoPopupKey`) and untranslatable
(`TestTUIConfigWiringIgnoresAnUntranslatableKey`) cases, which `""` alone cannot distinguish from
deleted wiring.

### DoD 5 — the translation, table-tested

`TestPopupKeyTranslation` — 34 rows: the documented four (`C-l`->`ctrl+l`, `M-t`->`alt+t`, bare
rune -> itself, unknown -> `""`), case handling (modifiers and named keys case-insensitive, a
bare rune case-*sensitive*), the named keys in tmux's own spellings (`BSpace`, `DC`, `PPage`),
and nine degrade-to-empty rows (`C-`, `M-`, `C-1`, `S-Tab`, `C-M-x`, a multi-rune name...).

`TestPopupKeyAgreesWithWhatTheModelWouldSee` is the one that matters: for each chord it builds the
real `tea.KeyMsg` and requires `popupKey` to equal its `String()`. That is what pins `C-Space` ->
`ctrl+@` and `C-i` -> `tab` to reality instead of to my expectations.
`TestCtrlOfEveryLetterTranslates` walks a-z and requires all 26 to translate, to be distinct, and
to agree with the KeyMsg — the arithmetic being the part most likely to be quietly off by one.

### DoD 6, 7, 8 and the collision rule — `internal/tui`

```
--- PASS: TestCloseKeyQuitsFromNormalMode
--- PASS: TestCloseKeyQuitsFromHelpMode
--- PASS: TestHelpStillDismissesOnQ
--- PASS: TestCloseKeyQuitsFromInputModeAndCommitsQueuedDeletes
--- PASS: TestPrintableCloseKeyTypesInInputModeInsteadOfQuitting   (letter, digit, space)
--- PASS: TestChordCloseKeyQuitsFromInputMode                      (ctrl, alt, named)
--- PASS: TestCollidingCloseKeyKeepsItsExistingAction              (a, d, ?, j, 1)
--- PASS: TestQAsTheCloseKeyStillQuitsThroughTheNormalPath
--- PASS: TestNoCloseKeyMeansNoExtraKeyCloses
--- PASS: TestIsTextKeyCoversWhatTheInputRowTypes
```

Every quit assertion is on the real exit path — the command returned must be `tea.Quit`, not
merely a `quitting` flag, since a model that set the flag without returning a command would leave
the popup on screen. The input-mode test queues a delete first and asserts the row is actually
gone from the store afterwards, which is what routing through `m.quit()` buys.

`TestNoCloseKeyMeansNoExtraKeyCloses` is the guard on the empty check in `closesOn`: without
`CloseKey != ""` every unhandled key matches `""` and quits the popup.

DoD 8 also has an end-to-end leg, because "the toggle works" and "every key closes the popup"
look identical from `closekey-9` alone:

```
== closekey-10-prefix-install-does-not-toggle
    ok   prefix t opens the popup
    ok   a bare t does not close it (no TDO_POPUP_KEY was passed)
    ok   q closes it
```

And `closekey-8` proves the mechanism itself, independent of the plugin: a popup whose command
records its own environment sees `TDO_POPUP_KEY=C-l`.

**Two more mutation proofs**, both on the input-mode guard:

```
### MUTANT B: isTextKey ignores Alt ###
--- FAIL: TestChordCloseKeyQuitsFromInputMode/alt_chord

### MUTANT C: input guard checks KeyRunes only (the plan as written) ###
--- FAIL: TestPrintableCloseKeyTypesInInputModeInsteadOfQuitting/space
```

Mutant C is the one worth keeping: it shows the deviation from the plan's literal check was
necessary, not stylistic.

### DoD 9 — sweep

```
$ make lint          -> go vet ./...           (clean)
$ make test          -> cli / scope / store / task / tui all ok
$ gofmt -l .         -> (no output)
$ make test-plugin   -> plugin harness: 188 passed, 0 failed   (was 140 before this task)
$ CGO_ENABLED=0 make build && otool -L bin/tdo
    /usr/lib/libSystem.B.dylib
    /usr/lib/libresolv.9.dylib                 <- no libsqlite3
$ git diff --stat internal/cli/testdata/       -> (empty: the JSON golden is untouched)
```

Harness determinism after the seeding fix — four consecutive runs:

```
run 1: plugin harness: 188 passed, 0 failed
run 2: plugin harness: 188 passed, 0 failed
run 3: plugin harness: 188 passed, 0 failed
run 4: plugin harness: 188 passed, 0 failed
```

### Definition of done

1. **`C-l` closes and reopens the popup** — done. `closekey-9`, a real keypress through a real
   client, plus a hand transcript. Mutation-proven able to fail.
2. **Four branches carry `-e TDO_POPUP_KEY` on root, none on prefix** — done, with the branch
   count measured rather than hardcoded. Covers the default install and the invalid-table
   fallback too.
3. **A quoted key name still installs a working keybind, and a server still starts** — done, for
   four different quote-containing keys. The premise needed correcting (tmux rejects malformed key
   names for reasons unrelated to quotes) but not changing.
4. **One `tui.Config` field, with a real assertion** — done. The completeness test forced it, and
   the assertion covers env plumbing *and* translation, with `t.Setenv` so it can fail.
5. **Translation table-tested** — done: 34 rows, plus agreement-with-Bubble-Tea and all 26
   ctrl+letters.
6. **Quits from all three modes, committing queued deletes** — done, asserted through the store.
7. **A printable rune does not quit from input mode; a chord does** — done, both directions, and
   the space case is mutation-proven against the plan's literal check.
8. **No `TDO_POPUP_KEY` means nothing extra closes the popup** — done, in unit tests and end to
   end on a prefix install.
9. **`make test`, `make lint`, `make test-plugin` green** — done, and a pre-existing harness flake
   was fixed rather than left to rot.
