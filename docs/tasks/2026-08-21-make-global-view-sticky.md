# The All-Tasks View Is Sticky Across Popup Sessions

**Status:** ready
**Worktree:** none

## Goal
Close the popup while the all-tasks view (`g`) is up, and the next popup opens in it. Close it
from the merged list, and the next one opens there. The preference survives a tmux server
restart and a reboot, because it is a file rather than process state.

## Why
`g` is a mode, not a momentary action: someone who works across several projects lives in the
wide view, and today every popup drops them back into the merged list so `g` becomes a reflex
keystroke on every open. The project already treats "which scope do I mean" as worth persisting
(`internal/scope/sticky.go` stores the add-default kind in the XDG *state* dir); this is the same
argument for "which view do I want".

## Constraints
- **The all-tasks view only.** Tier filters (`1`/`2`/`3`) stay per-popup — ruled 2026-08-21. So
  the persisted value is one boolean's worth of state, not a view/filter enum.
- **`internal/tui` must not import `internal/scope`**, and does not today. The preference arrives
  and departs through `tui.Config`, mirroring the existing `DefaultScope` + `SetSticky` pair:
  a value in, a setter func out.
- **Written on quit, not on toggle.** One write per popup session, on the `m.quit()` funnel that
  already commits queued deletes. A popup that dies uncleanly leaves the previous preference,
  exactly as it leaves queued deletes uncommitted — both fail towards not changing the user's
  state.
- **A write failure must never fail the quit, and must print nothing.** Same rule
  `addCmd` already follows for `SetSticky`: the popup's job is not to trade the user's exit for
  a preference file.
- **A missing, empty or unparseable file means the merged list**, silently. `internal/scope`
  already treats the sticky file that way and the reason is the same: a corrupt one-word file
  must never stop the popup from opening.
- Reuse `Resolver.stateDir()` — `$XDG_STATE_HOME/tmux-todo`, with the documented fallback. A
  second state directory would be a second thing to back up and a second thing to get wrong.
- **Tests must not touch the developer's real state file.** `t.Setenv("XDG_STATE_HOME", t.TempDir())`
  in every test that exercises a real read or write, and the TUI tests inject fakes.
- Out of scope: a CLI flag or option to set the default view; persisting the cursor position, the
  filter, or the scroll offset; per-session or per-directory preferences (one global preference,
  like the sticky default kind).

## Critical surface
None. No schema, no network, no destructive write. The one thing worth care is that this adds a
**second consumer of the state dir**, so a bug there could plausibly damage the add-default
file that already lives beside it — hence a separate file, never a shared one, and a test that
proves the two round-trip independently.

## Definition of done
1. Press `g`, quit, reopen: the popup is in the all-tasks view. Press `g` again, quit, reopen:
   it is in the merged list. Proven end to end with a real popup and a `capture-pane`, not only
   in unit tests.
2. The write happens on **quit**, not on toggle: after pressing `g` the stored value is still the
   old one; after quitting it is the new one.
3. The write happens on **both** quit paths — with an empty delete queue and with a non-empty
   one. (`m.quit()` returns early when nothing is queued, so a persist bolted onto the
   delete-commit command would save the preference only for users who pressed `d`.)
4. A `SetStickyView` that returns an error still quits, still commits queued deletes, and prints
   nothing.
5. A missing state file, an empty one, and a file holding junk all open the merged list, and the
   popup opens normally in every case.
6. The add-default sticky file and the view file round-trip independently: writing one does not
   read as the other, and neither is disturbed by the other's absence.
7. `tui.Config` gains exactly two fields (the initial view, and the setter), each with a real
   assertion in `wiringChecks` — behavioural, not `nilIsCorrect`, and with `XDG_STATE_HOME`
   pointed at a temp dir so the assertion can fail without touching real state.
8. `internal/tui` still does not import `internal/scope` (asserted, since the whole seam exists
   to keep that true).
9. Opening straight into the all-tasks view queries the *groups*, not the merged list — i.e. the
   view is set before `Init()` runs, and the first frame is correct rather than the merged list
   for one frame.
10. `make test`, `make test-plugin`, `make lint` clean; `gofmt -l .` empty; static build holds;
    the `list --json` golden untouched.

## Verification
- `internal/scope`: round-trip test for the new file, plus missing/empty/junk cases, plus DoD 6's
  independence test. All with `XDG_STATE_HOME` in a temp dir.
- `internal/tui`: `quit()` calls the setter with `true` from the all-tasks view and `false` from
  the merged list, on both queue paths (DoD 2, 3); an erroring setter still quits and still
  commits (DoD 4); the initial view comes from Config (DoD 9).
- `internal/cli`: the two wiring assertions (DoD 7).
- End-to-end (DoD 1): the nested-client trick — open the popup, `g`, `q`, reopen, and
  `capture-pane` shows a group header (a string only the all-tasks view renders). Then the
  reverse.
- A mutation proof for DoD 3: move the persist into the delete-commit path and show the
  end-to-end case failing for a popup with nothing queued.

## Decisions
- **2026-08-21 (curator grill)** — "Global view" was ambiguous between the all-tasks view (`g`)
  and the global *tier filter* (`3`); the product uses the word "global" for the tier, and `g`'s
  view is called the all-tasks view in `design.md`. The user ruled: the **all-tasks view**, and
  tier filters stay per-popup.
- **2026-08-21 (user)** — Written on quit rather than on each toggle. One write per session, and
  an accidental `g` that is immediately undone never moves the preference.
- **2026-08-21 (user)** — An unusable stored preference falls back to the merged list rather than
  degrading through tiers or showing an empty filtered view. "Absent beats empty", and an empty
  popup at open time reads as a bug.
- **2026-08-21 (curator's call, flagged at Checkpoint 1 — override cheaply)** — the persist is a
  **synchronous call at the top of `m.quit()`**, before the queued-delete branch, rather than a
  `tea.Cmd`. It is one small file write on a path that is already exiting, and doing it in one
  place is what makes DoD 3 structural instead of a rule to remember. A command would have to be
  sequenced with `tea.Quit` and with `commitDeletesCmd`, which is where the "only saved when you
  pressed `d`" bug lives.

## Plan
Approved at Checkpoint 1, 2026-08-21.

**1. `internal/scope/sticky.go` — a second one-word file beside the first.**

```go
const stickyViewFile = "default-view"   // holds "all"; anything else means merged

func (r Resolver) StickyAllTasks() bool          // missing/empty/junk -> false
func (r Resolver) SetStickyAllTasks(bool) error
```

Same `stateDir()`, same silent-fallback rule, same write pattern as the existing pair. A separate
file rather than a second word in `default-scope`, so a parse change to one can never eat the
other (DoD 6).

**2. `internal/tui` — two Config fields and one call.**

```go
// Config
AllTasks    bool                 // the view to open in
SetAllTasks func(bool) error     // persisted on quit; error dropped

// the constructor picks the view before Init() runs
if cfg.AllTasks { m.view = viewAll }

// quit(), first statement, before the queued-delete branch
if m.cfg.SetAllTasks != nil { _ = m.cfg.SetAllTasks(m.view == viewAll) }
```

**3. `internal/cli/cli.go` — wire both from the resolver** that `openEnv` already built, next to
`DefaultScope`/`SetSticky`. `TestEveryTUIConfigFieldIsAsserted` fails until `wiringChecks` has
an entry for each, which is the forcing function.

**Sequencing.**
1. `internal/scope`: the file, its round trip, and the missing/empty/junk and independence tests.
2. `internal/tui`: the Config fields, the constructor branch, the `quit()` call, and the unit
   tests for both queue paths and the erroring setter.
3. `internal/cli`: the wiring assertions.
4. The end-to-end harness case and its mutation proof.
5. Sweep.

**What could go wrong.**
- *Persisting only on one quit path.* The named hazard: `quit()` returns early when nothing is
  queued. DoD 3 exists for it and the mutation proof is what shows the test discriminates.
- *A test writing the developer's real preference.* Every `internal/scope` test needs
  `XDG_STATE_HOME` in a temp dir; the TUI tests must inject a fake setter rather than the real
  one. A test that silently flips the developer's own view is a bad afternoon.
- *The first frame being the merged list.* `Init()` issues the first query, so the view has to be
  set in the constructor, not after. DoD 9 pins it.
- *Two popups open at once.* Last quit wins. Nothing to fix; write it down.
- *The all-tasks view needing more Config than the merged list.* `LiveSessions` is resolved on
  every open already, so opening straight into the wide view needs no new resolution — confirm
  rather than assume, since a missing liveness map would show every session as dead.
