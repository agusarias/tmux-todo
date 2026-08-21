# Completed Rows Stay Visible For 24h, Grouped At The End Of Their Tier

**Status:** in-progress
**Worktree:** ../todo-done-24h

## Goal
A task completed within the last 24h is visible in the popup whenever you arrive — not only
during the popup session that completed it. Those rows sit struck-through at the **end of their
scope tier**, below that tier's pending rows, in both the merged list and the all-tasks view.

## Why
"I want the done items to be visible for 24hs." Today they are not: `store.DoneRetention` is
already `24 * time.Hour`, but `doneSince()` takes whichever of "this popup opened" and
"now − 24h" is **later**, and `openedAt` is later except for a popup left open more than a day.
So the 24h arm never bites and anything completed before you opened the popup is already gone.
The retention constant exists and is dead code in effect.

Making it real is also what gives the day a shape: a glance at the popup shows what is left
*and* what got done, which is the thing a task list is for at 6pm.

## Constraints
- **`design.md:108-111` is amended as part of this task**, on the user's instruction
  (2026-08-21). The clause "one that was already done when the popup opened … is hidden when you
  arrive" goes; 24h since completion becomes the only rule. CLAUDE.md makes `design.md` win over
  a contradicting brief, so this task is invalid without that edit — and the edit is a
  *deliberate product change*, recorded as such rather than slipped in.
- **The `tdo list` CLI does not change.** Not its default output, not `--all`, not the `--json`
  contract, not the order rows come back in. `internal/cli/testdata/list.json` must still match
  byte-for-byte.
- **Therefore the re-sort happens in `internal/tui`, never in the store's `ORDER BY`.** The
  store's ordering is shared with `tdo list`; changing it there would change a published
  command's output order as a side effect of a popup change.
- `internal/store` and `internal/scope` do not change. No migration: `done_at` already exists.
- The popup stays environment-blind — `doneSince()` keeps reading `cfg.now()`, which tests
  freeze; no new clock access.
- Out of scope: purging (`store.PurgeDone` still has no caller, as `design.md:113-116` records
  deliberately — rows older than 24h stay in the database, invisible, exactly as today); making
  the window configurable via an option; any change to what `space` does.

## Critical surface
None of the classic kinds. Two real hazards, both in territory this repo has been burned in:
- **The frame.** More visible rows means a longer list and, in the all-tasks view, more group
  content. Three shipped bugs have been in the arithmetic between correct pieces, so the frame
  invariants must be re-asserted with done rows present, not assumed.
- **The cursor.** Completing a row now *moves* it (to the end of its tier), which is exactly the
  "rows shift" case `TestCursorReAnchorsWhenRowsShift` exists for. Getting it wrong means
  `space` acts on the wrong task — a silent failure.

## Definition of done
1. `doneSince()` is `cfg.now().Add(-store.DoneRetention)` and nothing else; the `openedAt` field
   is gone from `Model` (it has no other reader).
2. A task completed 3h ago by a *different* popup session is visible, struck-through, on a fresh
   popup. Pinned by a test with a frozen clock.
3. A task completed 25h ago is not visible. Pinned at both edges: 23h59m visible, 24h01m hidden.
4. Within a scope tier, pending rows come first in their existing newest-first order, then that
   tier's done rows. Tier order (session → dir → global) and the tier labels are unchanged.
5. Done rows inside a tier are ordered **`done_at` descending** — most recently completed first.
6. The same grouping applies inside every group of the all-tasks view (`g`).
7. Completing a row moves it to the end of its tier and **the cursor follows that row**, so
   `space` again undoes it and it returns to its old position. Asserted end to end (complete,
   then complete again, and check the row is back where it started with its id intact).
8. There is **no separator row** between a tier's pending and done blocks; strikethrough and
   position are the signal. Rows are scarce (13 body rows at the 60x15 floor) and a separator
   would cost one per tier.
9. `TestFrameNeverExceedsThePane` still holds with a list that is mostly done rows, asserted on
   the unclamped `frame()` with the `clampHeight` backstop proven not to have fired.
10. `TestRowsNeverExceedTheirWidth` still holds; a struck-through row's width budget is
    unchanged.
11. `tdo list` output and order are untouched, and `internal/cli/testdata/list.json` matches
    byte-for-byte.
12. `design.md:108-111` is amended, and the old wording plus the reason for the change is
    recorded in the Decisions log — not silently overwritten.
13. `make test`, `make test-plugin`, `make lint` clean; `gofmt -l .` empty; static build holds.

## Verification
- `internal/tui` unit tests with a frozen clock for DoD 2, 3, 4, 5, 6, 7.
- A `capture-pane` check in a plain tmux pane (the `$XDG_DATA_HOME` + `tdo doctor --db` +
  `sqlite3` seeding recipe from CLAUDE.md) showing a real frame with pending and done rows in
  one tier — the only check that catches whole-frame bugs, and it has caught three.
- The DoD 9 and 10 invariants run over a seeded list where most rows are done.
- `git diff --stat internal/cli/testdata/` empty for DoD 11.
- A mutation proof for DoD 7: with the id re-anchor removed, completing a mid-tier row must be
  shown leaving the cursor on a *different* task.

## Decisions
- **2026-08-21 (curator grill)** — **LEAD FINDING: the request contradicted `design.md`, and the
  24h constant was already there doing nothing.** `doneSince()` returns the *later* of
  `openedAt` and `now − 24h`, so the retention window was unreachable in practice. The user
  ruled: amend `design.md`, drop the `openedAt` arm, 24h is the only rule.
- **2026-08-21 (user)** — Done rows are **grouped at the end of their tier**, not left inline in
  newest-first order. The inline option was offered and declined: with a day's completions
  inline, the top of a 13-row body stops being "what is left", which is the popup's whole job.
- **2026-08-21 (user)** — Applies to the all-tasks view as well, so the two views cannot
  disagree about what "done" means. The CLI keeps its own contract unchanged.
- **2026-08-21 (user)** — Purging stays out of scope; invisible accumulation past 24h is
  pre-existing and `design.md` already records why `PurgeDone` has no caller.
- **2026-08-21 (curator's call, flagged at Checkpoint 1 — override cheaply)** — three details
  the grill did not cover:
  1. Done rows sort by **`done_at` DESC**, not by id: the block is about *recency of
     completion*, which is the same axis the 24h window uses, and `done_at` is non-null for
     every done row.
  2. **The cursor follows the completed row** into the done block (the existing id anchor),
     rather than staying at its index. That keeps `space`-again-to-undo working on the row the
     user just acted on, which is the only undo the product has.
  3. **No separator row.** Rows are the scarcest resource in the frame.

## Plan
Approved at Checkpoint 1, 2026-08-21. `internal/tui` plus one `design.md` edit; no store, CLI or
schema change.

**1. `doneSince()` collapses to one line** and `Model.openedAt` is deleted (`tui.go:190-193`,
`273`, `298-312`). This alone satisfies DoD 1-3 and makes `store.DoneRetention` load-bearing for
the first time.

**2. A pure partition function, applied where rows are built.** In the merged view the store
hands back tier-ordered, id-DESC rows; the TUI re-orders *within* each tier before flattening:

```go
// partitionDone returns tasks with each scope tier's done rows moved to the end
// of that tier, most recently completed first. Stable for the pending part, so
// the store's newest-first order survives untouched.
func partitionDone(tasks []task.Task) []task.Task
```

Called from `rebuildRows` for the merged list, and per group for the all-tasks view (so
`groupTasks`/`visibleGroups` in `alltasks.go` get the same treatment). It is a pure function over
a slice, which is where this package's testable surface lives.

**3. `design.md:108-111` is rewritten** with the old sentence quoted in this brief's Decisions
log rather than lost.

**Sequencing.**
1. `doneSince()` + the `openedAt` removal, with the frozen-clock edge tests (23h59m / 24h01m).
2. `partitionDone` and its unit tests, merged view first, then the all-tasks view.
3. The cursor test for DoD 7, and its mutation proof.
4. The frame and row-width invariants over a mostly-done list (DoD 9, 10).
5. The `capture-pane` frame check.
6. `design.md`, then the sweep and the golden check.

**What could go wrong.**
- *Sorting in the store instead.* It is the obvious place and it is wrong here: `tdo list`
  shares that `ORDER BY`, so the popup's layout choice would silently reorder a published
  command's output. The golden file is the tripwire, and it must stay untouched.
- *Tier detection by scope kind.* The partition needs tier boundaries; derive them from the
  rows' own `Scope.Kind` runs rather than re-deriving the tier order, or a tier with no pending
  rows will merge into its neighbour.
- *The cursor.* Completing a row now reorders, which is the case
  `TestCursorReAnchorsWhenRowsShift` was written for — and CLAUDE.md records that the *other*
  cursor test here is vacuous because completing a row used not to reorder. That vacuous test
  becomes meaningful now; check whether it starts discriminating, and say so either way.
- *Density.* A day of completions makes the list longer, so the viewport does more scrolling.
  That is the accepted cost of the user's placement choice, but the frame invariants are what
  keep it from becoming a rendering bug.
- *`done_at` null on a row marked done by an older code path.* Order by `done_at` must tolerate
  a zero value rather than panicking or silently sorting it first.
