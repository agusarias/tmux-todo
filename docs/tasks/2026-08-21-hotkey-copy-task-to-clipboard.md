# Copy The Selected Task To The Clipboard With `y`

**Status:** review
**Worktree:** none (merged; worktree removed)

## Goal
Pressing `y` in the popup copies the selected task's text to the clipboard, and the popup says
it did. Inside tmux the text also lands in tmux's own paste buffer, so `prefix ]` pastes it.

## Why
A task in the list is usually the thing you are about to type somewhere else — a commit
message, a PR title, a chat message. Today the only way out of the popup is retyping it. The
popup is the one place that already knows the exact text, and `y` is the key the muscle memory
reaches for.

## Constraints
- **No new module in the build graph.** `bubbles/textinput` was rejected for exactly this
  reason (it imports `github.com/atotto/clipboard`, which is in neither `go.sum` nor the module
  cache), and `bubbletea v1.3.10` ships no `SetClipboard`/OSC-52 helper. The copy therefore goes
  through tmux, or through an escape sequence written by hand.
- **`internal/tui` stays environment-blind.** It never shells out and never learns whether tmux
  exists: the copy arrives as a `Copy func(string) error` in `tui.Config`, injected by
  `internal/cli`. This is `jump.go`'s seam — the popup decides, `internal/cli` runs the
  subprocess — with the difference that this one runs *during* the popup rather than at exit,
  because the confirmation is part of the feature.
- **Inside tmux: `tmux load-buffer -w -`**, text on stdin, never interpolated into a shell
  string. A task's text is user data (quotes, `$`, newlines), and `exec.Command` with stdin
  spawns no shell. `-w` is what forwards to the system clipboard over OSC 52; the tmux paste
  buffer is filled either way.
- **Outside tmux: the OSC 52 escape, written by hand to the tty.** ~15 lines, no dependency,
  and it works over SSH. Its honest weakness is that a terminal which ignores OSC 52 is
  indistinguishable from success — so the message says what was attempted, not what a terminal
  did with it.
- **The payload is the task text, verbatim.** No id, no scope prefix, no rendered padding, no
  strikethrough. Paste-ready.
- `y` is normal-mode only. In the input row `y` is literal text (the precedence rule the close
  key already follows) and behind the `?` overlay every mutation key is inert.
- Out of scope: copying multiple tasks or a whole tier; a paste-into-the-input-row key; making
  the copy work when the cursor is on an all-tasks-view group header (it does nothing there).

## Critical surface
None of the classic kinds — no migration, no auth, no store write, no network. Two things
deserve care anyway:
- **The text is user data going into an argv-adjacent place.** stdin, not an argument, and
  never a shell string. The rename hook's injection bug is the precedent.
- **The feedback line is a frame line**, and this repo has shipped three frame bugs, all in the
  arithmetic between correct pieces. The message replaces the title line — already exactly one
  row, already truncated to `contentWidth` — so `chromeHeight` does not change. Anything that
  puts it on a *new* row is a different, riskier change.

## Definition of done
1. `y` on a selected task copies its text verbatim: inside tmux `tmux show-buffer` returns
   exactly that text, byte for byte, including a text with quotes and a `$`.
2. Inside tmux the copy is `tmux load-buffer` with the text on **stdin**, asserted on the argv
   — not `set-buffer <text>` and nothing shell-interpolated.
3. Outside tmux, `y` writes a well-formed OSC 52 sequence (`ESC ] 52 ; c ; <base64> BEL`) whose
   payload base64-decodes to the task text. Asserted against an injected writer, so the test
   needs no terminal.
4. The popup confirms it: the title line shows `copied: <text>`, truncated to `contentWidth`,
   and the frame's height and width are unchanged — `TestFrameNeverExceedsThePane` still holds
   with a message present, and the `clampHeight` backstop is asserted *not* to have fired.
5. The message clears on the **next keypress**, not on a timer. No `tea.Tick`.
6. A failing copy says so (`copy failed: …`) rather than reporting success. Pinned by a test
   with a `Copy` that returns an error.
7. `y` does nothing at all — no call, no message — with an empty list, and with the cursor on
   an all-tasks-view group header.
8. `y` in the input row types a `y`; `y` behind the `?` overlay does nothing.
9. `tui.Config` gains exactly one field for this, and `TestEveryTUIConfigFieldIsAsserted` has a
   real assertion for it: the wiring test proves the injected `Copy` actually loads a tmux
   buffer, not merely that the field is non-nil.
10. The `?` overlay lists `y`. It is a body row, not the footer — fixed length, already
    truncated — which is why this is safe where the close key's footer entry was not.
11. An end-to-end harness case: nested client, popup open, `y` pressed, `tmux show-buffer`
    holds the task text. Proven able to fail.
12. `make test`, `make test-plugin`, `make lint` clean; `gofmt -l .` empty; static build holds;
    the `list --json` golden untouched.

## Verification
- `internal/tui` unit tests over `Update` for DoD 4-8, with a fake `Copy` recording its
  argument.
- `internal/cli` tests for DoD 2 (argv pinned, stdin carries the text) and DoD 3 (OSC 52
  against an injected writer, including a payload with a quote and a newline).
- The DoD 9 wiring assertion inside the substituted program — the only moment the Config is
  valid, per CLAUDE.md's note that `runTUI` defers `closeDB`.
- The DoD 11 harness case, plus a mutation proof: with the `y` binding removed, it must fail.
- A `tmux show-buffer` transcript for DoD 1 with an awkward text (`it's a "$HOME" task`).

## Decisions
- **2026-08-21** — tmux's buffer, not a Go dependency and not pbcopy/xclip. It adds no module
  to a graph deliberately kept at zero new modules, fills tmux's own paste buffer as a side
  benefit, and `-w` covers the system clipboard. The three-platform shell-out was declined as
  three code paths where one suffices inside tmux.
- **2026-08-21** — outside tmux, write OSC 52 by hand rather than declaring the key dead or
  reaching for pbcopy. `tdo tui` from a plain shell is a supported path, and a silent-but-
  standard escape beats a dead key.
- **2026-08-21** — the payload is the text alone. A scope prefix is a format nobody asked for
  and would need its own rule for global scope; the rendered row would carry column padding
  and formatting into someone's paste buffer.
- **2026-08-21** — the confirmation replaces the **title line** and clears on the next
  keypress. The footer was rejected: its width is already `36 + len(Version)` and it is the
  line that shipped the frame-overflow bug. A timer was rejected because it buys nothing a
  keypress does not and costs a `tea.Tick` to test.

- **2026-08-21 (executor)** — the `-w` fallback reports **success**, and says nothing about
  having dropped `-w`. The plan asked it to "say so in the message", which the seam cannot
  carry: `Copy func(string) error` has one channel, and using the error for a warning would
  make DoD 6's "a failing copy says so" mean two different things, while a second channel
  would break DoD 9's "exactly one field". The tmux paste buffer did fill, so from the
  popup's side the copy happened. Documented in `internal/cli/copy.go` and CLAUDE.md
  instead. `set-clipboard off` is the same shape and gets the same treatment.
- **2026-08-21 (executor)** — the whitespace collapse that keeps the notice one row lives in
  `titleLine` (the renderer), not in `copied` (the setter). The plan put it at the setter.
  `TestFrameNeverExceedsThePane`'s new `copied hostile` mode sets `m.notice` directly and
  caught it: the invariant `chromeHeight` depends on is a property of the line that gets
  rendered, so it has to be enforced where the line is built. The payload handed to `Copy`
  is untouched either way.
- **2026-08-21 (executor)** — `y` shares the `?` overlay's existing "a add · e edit ·
  s re-scope" line rather than taking a new one. `helpBody` clips the overlay to
  `listHeight`, so a sixth line would push the version off the bottom at the design's own
  60x15 popup. 35 columns, inside the 39 `footerText` is held to.
- **2026-08-21 (executor)** — DoD 1's "verbatim" is measured against the *stored* text, not
  the typed one: `store.Add` trims its input (`internal/store/tasks.go:56`), so a task
  seeded with a trailing space is stored without it. Asserting the typed string would have
  been asserting that the store does not trim, which is a different and false claim.

## Plan
Approved at Checkpoint 1, 2026-08-21.

**1. `internal/tui` — `Config.Copy`, the `y` binding, and the message.**

```go
// Config
Copy func(string) error   // nil means copying is unavailable; y is then inert

// updateNormal, alongside the other bindings
case "y":
    return m.copySelected()

// copySelected returns a tea.Cmd calling m.cfg.Copy, which sends copiedMsg{text, err}
```

`m.notice` (one string) holds the message; `titleLine()` returns it truncated to
`contentWidth` when set, and the existing title otherwise. Cleared at the top of
`Update`'s `tea.KeyMsg` branch, *before* dispatch, so the keypress that sets it
survives and the next one clears it. `copySelected` returns `m, nil` when there is
no selected task (empty list, group header) or `Copy` is nil.

**2. `internal/cli` — the two copy paths, one seam.**
New `copy.go`. Inside tmux (`resolver.TmuxEnv != ""`):
`exec.Command("tmux", "load-buffer", "-w", "-")` with `c.Stdin` the text. Outside:
write `ESC ] 52 ; c ; <base64> BEL` to a `io.Writer` that defaults to `/dev/tty`
and is injectable for tests. `cfg.Copy` is set to whichever applies.

**3. Sequencing.**
1. `Config.Copy` + `y` + `notice` + the unit tests (DoD 4-8). The frame assertions
   come first because they are the class of bug this repo keeps shipping.
2. `internal/cli/copy.go` + argv/stdin and OSC 52 tests (DoD 2, 3).
3. The wiring assertion (DoD 9) — `TestEveryTUIConfigFieldIsAsserted` fails until it
   exists, which is the forcing function.
4. The `?` overlay row (DoD 10).
5. The harness case + its mutation proof (DoD 11), then the sweep.

**What could go wrong.**
- **`-w` is version-dependent, and that exact class is red on CI today.** `load-buffer
  -w` needs tmux >= 3.2. Probe rather than assume: if the `-w` form fails, retry
  without it (the paste buffer still gets the text; only the system-clipboard hop is
  lost) and say so in the message. Do **not** let the harness assert a `-w`-only
  behaviour the runner's tmux 3.4 might not share.
- **`set-clipboard off` in a user's config** makes `-w` silently not reach the system
  clipboard while the tmux buffer still fills. Nothing to fix; document it, and do not
  write a test that would pass only with the option on.
- **OSC 52 interleaving with the renderer.** Bubble Tea owns the tty; a write from a
  Cmd goroutine can land mid-frame. Use a single `Write` call so the escape itself
  cannot be split, and let the `copiedMsg` repaint clean up. If this proves visibly
  messy, the fallback is to defer the escape to after the program exits, the way
  `jump` already defers — record which was chosen and why.
- **A multi-line task text** in the title line would add rows. Collapse whitespace in
  the *message* (never in the payload).
- **The notice must not survive a reload.** `rowsMsg` arrives on a timer-free path but
  a concurrent pane's write triggers one; the message lives until the next keypress by
  design, so assert that a reload does not clear it.


## Evidence

**Merged into local `main` as `e4da5b9`** (branch `copy-to-clipboard`, work commit `a791b0f`).
Not pushed. Read the diff with `git show e4da5b9` or `git log -1 -p a791b0f`; the worktree is
gone. `go build ./...` and `go test ./...` were re-run on the merge result, green.

### Sweep (DoD 12)

```
$ make lint
go vet ./...

$ gofmt -l .
(empty)

$ make test
?   	github.com/agusarias/tmux-todo/cmd/tdo	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/cli	1.291s
ok  	github.com/agusarias/tmux-todo/internal/scope	1.220s
ok  	github.com/agusarias/tmux-todo/internal/store	(cached)
ok  	github.com/agusarias/tmux-todo/internal/task	(cached)
ok  	github.com/agusarias/tmux-todo/internal/tui	6.943s

$ make test-plugin
plugin harness: 196 passed, 0 failed        # 194 before this task

$ make build && otool -L bin/tdo
bin/tdo:
	/usr/lib/libSystem.B.dylib (compatibility version 0.0.0, current version 0.0.0)
	/usr/lib/libresolv.9.dylib (compatibility version 0.0.0, current version 0.0.0)
# no libsqlite3 — the static build holds

$ git status --short internal/cli/testdata/
(empty — the list --json golden is untouched)
```

### DoD 1 + 11 — the end-to-end case, nothing stubbed

`test/plugin_install_test.sh`'s new `copy-1-y-loads-the-tmux-buffer`: the real plugin
installs the real bind, the real binary runs in a real `display-popup` on a manufactured
nested client, `y` is pressed with `send-keys`, and `tmux show-buffer` is asked what landed.
The text carries a `'`, a `"` and a `$` — the payload that would come back mangled, or would
have executed, if anything on the path went through a shell.

```
== copy-1-y-loads-the-tmux-buffer
    ok   the root C-l binding is installed
    ok   the awkward text survived being seeded
    ok   the buffer stack starts empty
    ok   C-l opens the popup with the task on screen
    ok   y put the task text in the tmux buffer, byte for byte
    ok   the popup confirms the copy on the title line
    ok   the notice replaced the title rather than taking a row
    -- capture with the notice up:
       6:         ││  copied: ZZCOPYZZ it's a "$HOME" task                  ││
    ok   q closes the popup afterwards
```

That capture line is also the DoD 4 evidence: the notice is on the title's row (row 6, where
`closekey-9`'s capture shows `tdo`), and `tdo` is gone from it rather than sharing it.

### Mutation proofs

Every one of these was run, and every one discriminates. The buffer assertion is the one
that matters most, so it is proven first.

| Mutation | Result |
|---|---|
| Remove `case "y"` from `updateNormal` | `copy-1` fails: `y put the task text in the tmux buffer (want 'ZZCOPYZZ it's a "$HOME" task', got '')`, plus both notice assertions. Harness 193/3. **DoD 11's "proven able to fail".** |
| Drop `oneLine` from `titleLine` | `TestFrameNeverExceedsThePane` fails **432** assertions (`the height backstop fired (60 rows -> 30)`) |
| Clear the notice *after* dispatch instead of before | `TestCopyNoticeClearsOnTheNextKeypress` fails on every key: `notice survived "j"` / `"k"` / `"z"` / `"?"` / `"1"` |
| `load-buffer -w -` on stdin → `set-buffer -w <text>` in the argv | `TestLoadBufferPutsTheTextOnStdin` fails on every text: `used set-buffer; the text would be on the command line` / `the text appears in the argv element "it's a \"$HOME\" task"` |
| `Copy: newCopier(...)` → `func(string) error { return nil }` in `runTUI` | `TestTUIConfigWiring/Copy` fails: `Copy made 0 runs, want 1: []`, and `TestTUIConfigWiringOutsideTmux` too |

The second one is worth reading twice: the collapse was originally at the setter, where every
test passed. The `copied hostile` mode — which sets `m.notice` directly rather than pressing
`y` — is what failed, and moving `oneLine` into `titleLine` is what fixed it. See the
Decisions log.

### DoD-by-DoD

1. **Verbatim, quotes and `$` included** — `copy-1` above compares `tmux show-buffer` byte
   for byte. `TestCopyPassesTheTaskTextVerbatim` covers `it's a "$HOME" task`, a newline, a
   tab, `$(rm -rf /)`, a backslash and unicode, against the stored text (see Decisions on
   `store.Add`'s trim).
2. **stdin, not argv** — `TestLoadBufferPutsTheTextOnStdin` pins the argv exactly
   (`tmux load-buffer -w -`), asserts `stdin == text`, and asserts the text appears in **no**
   argv element. `TestLoadBufferFallsBackWithoutW` holds the same for the fallback, which is
   where an injection would go unnoticed. Mutation-proven.
3. **OSC 52 outside tmux** — `TestOSC52IsWellFormed` checks the `ESC ] 52 ; c ;` prefix and
   `BEL` terminator and *base64-decodes* the payload back to the text, over the same awkward
   set; it also asserts the raw text is not in the sequence beside the base64.
   `TestOSC52IsOneWrite` pins the single `Write`. Injected writer — no terminal needed.
4. **Notice on the title line, frame unchanged** — `TestFrameNeverExceedsThePane` gained two
   modes (`copied`, `copied hostile`) and a `Copy` in its Config (without which they would
   press an inert key). It runs 3 versions x 2 views x 9 sizes x 4 filters x 8 modes and
   asserts on the **unclamped** `frame()`, that the `clampHeight` backstop did **not** fire,
   and that no line exceeds the pane width. `TestCopyNoticeReplacesTheTitle` adds that the
   row count is identical before and after, and `TestCopyNoticeIsOneRowAndTruncated` sweeps
   5 widths x 3 hostile texts.
5. **Clears on the next keypress; no `tea.Tick`** — `TestCopyNoticeClearsOnTheNextKeypress`
   over 6 different kinds of key, mutation-proven. `TestCopyNoticeSurvivesAReload` holds the
   other direction: a `rowsMsg` from a concurrent pane must **not** clear it.
   `grep -rn 'tea.Tick' internal/` matches only two *comments* (`tui.go:263`,
   `copy_test.go:166`) explaining why there is no timer — no call site.
6. **A failing copy says so** — `TestCopyFailureSaysSo`: `copy failed: exit status 1`, no
   `copied:` in it, `noticeErr` set, and it fails loudly if `errStyle` and `hintStyle` are
   indistinguishable (so the leg cannot go vacuous). `TestLoadBufferReportsAFailedCopy` and
   `TestOSC52ReportsAnUnopenableTerminal` cover the two producing ends.
7. **Inert with nothing selected** — `TestCopyIsInertWithNothingSelected`: empty list, cursor
   parked on an all-tasks group header, and `Copy == nil`. Each asserts **no call and no
   message**, and that `Update` returned no command at all.
8. **Normal-mode only** — `TestCopyKeyIsNormalModeOnly`: the input row types `yak yoghurt`
   with zero `Copy` calls; behind the `?` overlay `y` copies nothing, sets no notice and
   leaves the overlay up.
9. **One Config field, really asserted** — `TestEveryTUIConfigFieldIsAsserted` failed the
   moment `Copy` was added, exactly as the plan predicted. The `wiringChecks["Copy"]` entry
   is behavioural: it calls `cfg.Copy(...)` and asserts the recorded argv is
   `tmux load-buffer -w -` with the text on stdin, and that nothing was written to the tty.
   `TestTUIConfigWiringOutsideTmux` asserts the mirror — no tmux call, and an escape whose
   payload base64-decodes to the text. Both mutation-proven. The fixture substitutes both
   copy seams in `install`, so no test can reach the developer's real paste buffer.
10. **The `?` overlay lists `y`** — `TestHelpOverlayListsTheCopyKey` checks both views'
    `helpLines` *and* the clipped `helpBody` at the design's 60x15 floor, plus that the
    version line still survives the clip.
11. **End-to-end** — above, with its mutation proof.
12. **Sweep** — above.

### Not verified / known limits

- **`-w` reaching the *system* clipboard is not asserted anywhere**, deliberately. It depends
  on the terminal honouring tmux's OSC 52 forwarding and on the user not having
  `set-clipboard off`; a test for it would pass only on a correctly-configured desktop and
  fail on CI. What is asserted is the tmux buffer, which is what tdo controls.
- **OSC 52 outside tmux is proven only up to the bytes written.** A terminal that silently
  ignores the sequence is indistinguishable from one that honoured it — that is a property of
  OSC 52, recorded in `internal/cli/copy.go` and the brief's Constraints, not a test gap.
- The runner's tmux here is 3.7b, so the `-w` fallback path is exercised only by its unit
  test, not end to end.
