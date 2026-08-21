# Copy The Selected Task To The Clipboard With `y`

**Status:** in-progress
**Worktree:** ../todo-copy-to-clipboard

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

