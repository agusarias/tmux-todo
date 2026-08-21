# The All-Tasks View Is Sticky Across Popup Sessions

**Status:** done
**Worktree:** none (merged; worktree removed)

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

- **2026-08-21 (executor)** — the atomic temp-plus-rename write was **extracted** into
  `writeStateFile` rather than copied for the second file. Both preferences live in the same
  directory and both are read by code that treats a malformed file as absent, so "the write is
  atomic" has to be a property of the mechanism, not of each caller remembering it. The plan
  said "same write pattern"; one function is the honest reading of that.
- **2026-08-21 (executor)** — `SetStickyAllTasks(false)` **writes an empty file** rather than
  removing it. Removal would behave identically on read (absent and empty both mean the merged
  list), but a write keeps this one code path and leaves the mtime telling the truth about when
  the choice was last made.
- **2026-08-21 (executor)** — the wiring fixture seeds the view preference as **`true`**. `false`
  is both the field's zero value *and* what an unreadable file degrades to, so an assertion
  against `false` would pass with the wiring line deleted — the same vacuous-environment trap
  `CloseKey` documents for `$TMUX`. The `false` and corrupt-file directions get their own tests
  (`TestTUIConfigWiringWithNoStoredView`, `...WithACorruptStoredView`).
- **2026-08-21 (executor)** — the `SetAllTasks` wiring check reads the neighbouring scope
  preference **before** its write and compares after, rather than against the value the fixture
  seeded. `wiringChecks` runs as subtests over a **map**, so the `SetSticky` entry may already
  have rewritten it; the first version of this check failed on iteration order, which is a
  property of the test rather than of the code.
- **2026-08-21 (executor)** — the harness case sets `XDG_STATE_HOME` on the **server** via
  `case_start`, and asserts it before pressing anything. A `display-popup` child inherits the
  server's environment, so exporting it in a pane would have left the popup rewriting the
  developer's real preference on every run. Same trap the rename cases document for
  `XDG_DATA_HOME`; now recorded in CLAUDE.md for popup cases too.
- **2026-08-21 (executor)** — confirmed rather than assumed, per the plan's last worry: opening
  straight into the all-tasks view needs **no new resolution**. `LiveSessions` is already
  resolved on every popup open (`runTUI` calls `e.resolver.LiveSessions()` unconditionally), so
  the wide view's `(live)` / `(not running)` labels are correct on the first frame. The
  end-to-end capture on reopen shows a correctly rendered group header.

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

## Evidence

**Merged into local `main` as `9234ed6`** (branch `sticky-view`, work commit `92e443b`). Not
pushed. Read the diff with `git show 9234ed6` or `git log -1 -p 92e443b`; the worktree is gone.
`make lint` and `go test ./...` were re-run on the merge result, green.

### DoD 1 — the whole loop, end to end, nothing stubbed

`test/plugin_install_test.sh`'s new `sticky-1-the-all-tasks-view-is-remembered`: the real plugin
installs the real bind, the real binary runs in a real `display-popup` on a manufactured nested
client, and `g`/`q`/reopen are pressed with `send-keys`. The discriminator is the string
`GLOBAL` — the all-tasks view renders a `─ GLOBAL ─` group header, while the merged list writes
the tier as a lowercase `(global)` label, so an uppercase match means the wide view and nothing
else does.

```
== sticky-1-the-all-tasks-view-is-remembered
    ok   the root C-l binding is installed
    ok   the SERVER's environment carries the sandbox XDG_STATE_HOME
    ok   no view preference exists to begin with
    ok   C-l opens the popup
    ok   the first popup opens in the MERGED list
    ok   g switches to the all-tasks view
    ok   q closes it
    ok   quitting wrote a view preference
    ok   and it records the all-tasks view
    ok   C-l reopens the popup
    ok   the SECOND popup opens in the all-tasks view, with no g pressed
    -- capture on reopen:
       8:         ││  ─ GLOBAL ─                                            ││
       9:         ││  ▸ ZZSTICKYZZ                                          ││
    ok   g switches back to the merged list
    ok   q closes it again
    ok   the preference now records the merged list
    ok   C-l opens the popup a third time
    ok   and it opens in the MERGED list again
```

Both directions are in there deliberately: without the reverse leg, "it is sticky" and "it
always opens wide" look identical. The `XDG_STATE_HOME` assertion is a **safety** check, not a
tidiness one — a `display-popup` child inherits the *server's* environment, so without it the
case would have rewritten the developer's own preference file on every run.

### Mutation proofs

Four, all run, all discriminating.

| Mutation | Result |
|---|---|
| **Persist moved below the empty-queue early return** (the plan's named hazard, DoD 3) | `TestQuitPersistsTheView` fails all 4 legs (`SetAllTasks called 0 times, want exactly 1`), `TestQuitPersistsOnBothQuitPaths` fails, **and** the harness fails: `quitting wrote a view preference (… default-view missing)` |
| Constructor ignores `Config.AllTasks` (DoD 9) | `TestConfigAllTasksPicksTheOpeningView` fails: `New gave view 0, want all-tasks=true` and `group headers present = false, want true` |
| `runTUI` stops reading/writing the preference (DoD 7) | `TestTUIConfigWiring/AllTasks` and `/SetAllTasks` both fail: `AllTasks = false with the all-tasks view stored`, `the write did not land` |
| The view shares `default-scope`'s file (DoD 6) | `TestStickyViewAndScopeAreIndependent` fails: `writing the scope default cleared the stored view preference`, `state dir holds [default-scope], want exactly the two preference files` |

The first is the one the plan asked for by name, and it is worth reading twice: the unit tests
*and* the end-to-end case both catch it. A popup with nothing queued is the common path, so that
bug would have shipped as "the view is only remembered if you happened to press `d`".

### DoD-by-DoD

1. **End to end** — above, both directions, with its mutation proof.
2. **Written on quit, not on toggle** — `TestTogglingTheViewPersistsNothing` presses
   `g g g j 1 g` and asserts the setter is untouched after *each* keystroke, then that the
   following `q` writes exactly once (so it is not passing by the setter never being called at
   all). `TestQuitPersistsTheView` covers the four toggle states.
3. **Both quit paths** — `TestQuitPersistsOnBothQuitPaths` asserts the empty-queue leg
   explicitly (fataling if the queue is not actually empty, so the leg cannot drift into
   testing the other path) and the queued leg, checking the delete commit still runs.
   Mutation-proven twice over.
4. **A failing setter still quits and still commits** — `TestPersistFailureStillQuitsAndStillCommits`:
   the model is quitting, the command is the delete commit, the commit reports no error, **the
   rows are actually gone from the database** (`store.ErrNotFound`), nothing about the failure
   reaches `View()`, and `m.err` is untouched.
5. **Missing / empty / junk → merged list** — `TestStickyAllTasksUnreadableMeansMergedList`
   covers 10 cases: missing, empty, whitespace, junk, the *other* file's vocabulary (`global`),
   a near-miss (`alll`), wrong case (`ALL`), binary, a directory where the file goes, and an
   unusable state dir. It ends with a **positive control** — a file holding `all` must read as
   true — so the table cannot pass vacuously, plus the trailing-newline form the setter writes.
   `TestTUIConfigWiringWithACorruptStoredView` asserts the same through the real wiring.
6. **The two files are independent** — `TestStickyViewAndScopeAreIndependent` writes each,
   checks the other is undisturbed in both orders, flips one back, and asserts the state dir
   holds *exactly two* files (which also catches a leaked temp file from a non-atomic write).
   Mutation-proven.
7. **Exactly two Config fields, both really asserted** —
   `TestEveryTUIConfigFieldIsAsserted` failed for both the moment they were added, as the plan
   predicted. `AllTasks` is asserted against a seeded `true` (never `false`, which is the zero
   value); `SetAllTasks` is behavioural — it writes through the injected func and reads back
   with a fresh `Resolver`, and checks the neighbouring scope preference is unchanged. Every
   fixture points `XDG_STATE_HOME` and `StateDir` at temp dirs.
8. **`internal/tui` still does not import `internal/scope`** — `TestTUIDoesNotImportScope`
   parses every `.go` file in the package with `go/parser` and fails on the import. It carries
   its own **positive control** (a sentinel import that *is* present must be found), so a typo
   in the forbidden path cannot make it pass forever, and it fatals if it parsed zero files.
9. **The first frame is the right one** — `TestConfigAllTasksPicksTheOpeningView` checks
   `New`'s view *before* `Init()`, then runs the first query and asserts the groups came back
   (and that they did **not** in the merged case), then that group headers are present in
   `m.rows` exactly when expected. Mutation-proven.
10. **Sweep** — below.

### DoD 10 — sweep

```
$ make lint
go vet ./...

$ gofmt -l .
(empty)

$ make test
?   	github.com/agusarias/tmux-todo/cmd/tdo	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/cli	0.818s
ok  	github.com/agusarias/tmux-todo/internal/scope	0.711s
ok  	github.com/agusarias/tmux-todo/internal/store	1.075s
ok  	github.com/agusarias/tmux-todo/internal/task	0.906s
ok  	github.com/agusarias/tmux-todo/internal/tui	7.332s

$ make test-plugin
plugin harness: 213 passed, 0 failed        # 197 before this task

$ make build && otool -L bin/tdo
bin/tdo:
	/usr/lib/libSystem.B.dylib
	/usr/lib/libresolv.9.dylib
# no libsqlite3 — the static build holds

$ git diff --stat internal/cli/testdata/
(empty — the list --json golden is untouched)
```

### Not verified / limits

- **Two popups open at once: last quit wins.** Anticipated by the plan, nothing to fix, and not
  tested — there is no ordering to assert, only a write that happens to be second. Worth
  knowing rather than worth guarding.
- **A popup killed uncleanly leaves the previous preference.** Deliberate, and the same
  behaviour queued deletes already have: both fail towards not changing the user's state. Not
  separately tested, because "the process died before reaching `quit()`" has no seam.
- **The preference is global**, not per session or per directory — out of scope per the brief.
- **The write is not fsynced.** It is a temp-file-plus-rename, which is atomic against a crash
  mid-write; a power loss immediately after the rename could still lose it. Consistent with the
  existing `default-scope` file, and a preference is the kind of thing the design already says
  the user can lose without losing anything of theirs.

## Close-out (curator, 2026-08-21)

Approved at Checkpoint 2 on 2026-08-21. All ten DoD items check out, re-verified on `main`:
`make lint` clean, `go test ./...` green, `make test-plugin` **213 passed, 0 failed**, and the
`list --json` golden untouched.

**Independently verified, because it was the constraint most likely to be quietly violated:** the
developer's real state dir holds only `default-scope` — no `default-view` — so the suite did not
write a real preference. The harness's assertion that the *server's* environment carries the
sandbox `XDG_STATE_HOME` is what earns that, since a `display-popup` child inherits the server's
environment and would otherwise have rewritten the user's own file on every run.

**The plan's named hazard is caught twice over.** Moving the persist below `quit()`'s empty-queue
early return fails the unit tests *and* the end-to-end case. Since an empty delete queue is the
common path, that bug would have shipped as "the view is remembered only if you happened to press
`d`" — which is why the persist being the first statement in `quit()` is the design and not a
detail.

**Two positive controls worth copying.** The 10-case corrupt-file table ends with a file holding
`all` that must read *true*, so the table cannot pass vacuously; and `TestTUIDoesNotImportScope`
carries a sentinel import that must be found, so a typo in the forbidden import path cannot make
the guard pass forever — plus it fatals if it parsed zero files. Both are answers to the failure
mode this repo keeps meeting: a guard that is green because it is looking at nothing.

**Limits accepted as stated:** two popups open at once means last quit wins (no ordering to
assert, only a second write); a popup killed uncleanly keeps the previous preference, the same way
queued deletes are lost, both failing towards not changing the user's state; the preference is
global rather than per-context, per the brief; and the write is a temp-file-plus-rename without an
fsync, consistent with the neighbouring `default-scope` file.

**Included in `v0.1.0`.**

