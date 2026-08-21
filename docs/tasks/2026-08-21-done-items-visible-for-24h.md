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

- **2026-08-21 (executor)** — an **all-done tier must not short-circuit the sort**. The first
  `appendTierPartitioned` returned early when a tier had nothing to move (`pending == 0 ||
  pending == len(tier)`), which left an all-done tier in the store's id order instead of
  `done_at` DESC. Caught by `TestPartitionDoneWithAnAllDoneTier`. Only the *no done rows* case
  short-circuits now.
- **2026-08-21 (executor)** — three existing tests asserted the behaviour this task changes and
  were rewritten rather than deleted, with the old name and reason in each doc comment:
  `TestDoneRowCompletedBeforeOpenIsHidden` → `...IsVisible` (the inverted product rule),
  `TestDoneSinceIsTheLaterBoundary` → `TestDoneSinceIsTheRetentionWindow` (the `max()` is gone),
  and `TestSpaceKeepsTheRowVisibleAndInPlace` → `TestSpaceMovesTheRowToTheEndOfItsTierAndThe
  CursorFollows` (the row now *does* jump; what survives is that it stays on screen and the
  cursor goes with it).
- **2026-08-21 (executor)** — **the plan's question answered: `TestCursorReAnchorsOnTaskID` is
  no longer vacuous.** CLAUDE.md recorded it as passing with the id anchor deleted, because
  completing a row did not reorder. It now fails under that mutation, because completing a row
  moves it. Both it and `TestCursorReAnchorsWhenRowsShift` stay; the generalisable point ("a
  test can acquire or lose its teeth when the code around it changes, without being edited") is
  now in CLAUDE.md.
- **2026-08-21 (executor)** — `done_at`-order assertions through the model needed a
  `stampDone` test helper writing the column via SQL. `store.Complete` stamps the store's
  private clock, so two completions in one test land in the same second, `done_at` DESC becomes
  a tie, and the stable sort resolves it by the store's id order — an assertion built that way
  would have passed with the comparison deleted. The all-tasks test now completes rows in an
  order that **contradicts** the id order, so the two cannot agree by accident.
- **2026-08-21 (executor)** — CLAUDE.md's note that `[9m` is the strikethrough escape is wrong
  and was corrected. SGR 9 arrives combined (`[2;9m` / `[0;9m`), so that grep answers zero for a
  row that *is* struck through — which briefly looked like a real rendering bug in the
  `capture-pane` check. The working pattern is `;9m`.

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

## Evidence

### The real frame (the check that has caught three bugs)

`bin/tdo tui` in a plain 80x20 tmux pane, `$XDG_DATA_HOME` at a temp dir, schema from
`tdo doctor --db`, rows seeded with `sqlite3` — the recipe from CLAUDE.md. Nine rows across
three tiers: five pending, three done inside the window, one done 25h ago.

```
╭──────────────────────────────────────────────────────────────────────────────╮
│  tdo                                                                         │
│                                                                              │
│  ▸ ⌘ check CI is green     (session: pulsar)                                 │
│    ⌘ rebase onto main                                                        │
│    ⌘ wrote the brief                                                         │
│    ⌘ reviewed the PR                                                         │
│    · update README         …_0n9sln4pn_2z50xz6w5nfh0000gn/T/tmp.Dj6LiuvRv8)  │
│    · fixed the flaky test                                                    │
│    ◉ call the dentist      (global)                                          │
│    ◉ paid the invoice                                                        │
│                                                                              │
│  j/k move · space done · ? keys · q quit                                     │
╰──────────────────────────────────────────────────────────────────────────────╯
   (blank body rows elided; the real capture is 20 rows)

rows carrying SGR 9 (strikethrough):  4
the 25h-old row ('aged out yesterday'): absent, correct
frame rows: 20 (pane is 20)   <- fits exactly, no scroll
```

Everything the task asks for is visible in that one capture:

- **Pending first, done at the end of each tier.** Session: `check CI`, `rebase` (pending),
  then `wrote the brief`, `reviewed the PR` (done). Dir: `update README`, then
  `fixed the flaky test`. Global: `call the dentist`, then `paid the invoice`.
- **`done_at` DESC inside the done block.** `wrote the brief` (1h ago) sits above
  `reviewed the PR` (2h ago), which is the *opposite* of their id order — so the store's
  ordering is not what produced this.
- **Tier labels and tier order intact** — the rows that vanish first when a frame overflows.
- **No separator row** between the pending and done blocks in any tier (DoD 8).
- **The 25h row is gone from the view** (DoD 3) and still in the database.
- **Struck through**: 4 done rows, 4 SGR-9 runs. The escape is `[2;9m`/`[0;9m`, not `[9m` —
  see the Decisions log; the bare-`[9m` grep in CLAUDE.md reported 0 and briefly looked like a
  rendering bug.

### DoD 11 — the CLI is untouched, asserted two ways

```
$ git status --short internal/cli/ internal/store/ internal/scope/
(empty)

$ git diff --stat internal/cli/testdata/
(empty — list.json byte-identical)
```

And behaviourally, which is the assertion that would catch a leak the file check cannot:

```
$ tdo list --all --scope global
3 [ ] global  pending newer
2 [x] global  done recently      <- still interleaved, NOT moved to the end
1 [ ] global  pending
```

The popup groups; the CLI does not. That is the whole reason `partitionDone` lives in
`internal/tui` and not in the store's `ORDER BY`.

### DoD 13 — sweep

```
$ make lint
go vet ./...

$ gofmt -l .
(empty)

$ make test
?   	github.com/agusarias/tmux-todo/cmd/tdo	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/cli	1.251s
ok  	github.com/agusarias/tmux-todo/internal/scope	1.202s
ok  	github.com/agusarias/tmux-todo/internal/store	1.577s
ok  	github.com/agusarias/tmux-todo/internal/task	0.926s
ok  	github.com/agusarias/tmux-todo/internal/tui	9.371s

$ make test-plugin
plugin harness: 197 passed, 0 failed

$ make build && otool -L bin/tdo
bin/tdo:
	/usr/lib/libSystem.B.dylib
	/usr/lib/libresolv.9.dylib
# no libsqlite3 — static build holds
```

### Mutation proofs

Five, all run, all discriminating.

| Mutation | Result |
|---|---|
| `anchorCursor` reduced to `clampCursor()` (the id anchor removed) | **3 tests fail**, including the DoD 7 round trip: `cursor is on "first" (id 1), want it to follow the completed row "second" (id 2)` |
| `partitionDone` not wired into the merged view | `TestMergedViewPutsDoneAtTheEndOfItsTier` and `TestSpaceMovesTheRow...` fail |
| `partitionDone` not wired into the all-tasks view | `TestAllTasksViewPutsDoneAtTheEndOfEachGroup` fails: `group rows = [added last, finished first / added first, finished last / still to do]` — done rows still above pending |
| `completedAfter` neutered to `return false` (done block keeps store order) | 3 tests fail, incl. `partitionDone = [s-done-old s-done-new …], want [s-done-new s-done-old …]` |
| `doneSince` restored to the `openedAt` max() | **6 tests fail**, incl. `rows = ["still to do"], want both the pending and the recently-done row` |

The first one is the plan's required DoD 7 proof, and it also settles the plan's open question:
`TestCursorReAnchorsOnTaskID` — which CLAUDE.md recorded as **vacuous**, since completing a row
used not to reorder — now fails under that mutation. It has become a real guard because the
behaviour around it changed. Both it and `TestCursorReAnchorsWhenRowsShift` stay.

The fourth mutation is worth a note: the *first* attempt at it (`_ = completedAfter`) left the
`sort` import unused, so nothing compiled and the run produced no failures at all — which reads
exactly like "no test covers this". A mutation that does not build is not evidence; it was
redone so it compiles.

### DoD-by-DoD

1. **`doneSince` is one line; `openedAt` is gone** — `grep -rn openedAt internal/` matches
   only comments explaining the removal (7 hits, all prose); the struct field and every reader
   are gone, which is why the four tests that set it had to be edited to compile. `TestDoneSinceIsTheRetentionWindow` asserts it both on a bare `Model` and after
   `New`, so a doneSince that still consulted an open time would differ on the second leg.
2. **Completed 3h ago by another session, visible and struck through** —
   `TestDoneRowCompletedBeforeOpenIsVisible`. The row is completed *before* the model is built,
   which is the case the old rule could not show. Strikethrough asserted on the style object
   (`textStyle(...).GetStrikethrough()`), since a test process has no colour profile.
3. **Both edges** — `TestDoneVisibilityEdges`: 0h, 3h, 23h59m visible; 24h01m, 25h hidden. Each
   leg also asserts the row is still in the database.
4. **Pending first, then done, per tier; tier order unchanged** —
   `TestPartitionDoneGroupsDoneAtTheEndOfEachTier` (pure) and
   `TestMergedViewPutsDoneAtTheEndOfItsTier` (through the model, asserting the session tier's
   done row sits *above* the global tier's pending one, so done rows are not swept into one
   block at the bottom). `TestPartitionDoneKeepsTierOrderAndMembership` adds that nothing is
   dropped, duplicated or moved across a tier boundary.
5. **`done_at` DESC** — the pure test uses timestamps that contradict the id order;
   `TestPartitionDoneWithAnAllDoneTier` covers the tier where nothing moves but everything still
   sorts; the all-tasks test completes rows in id-contradicting order. Mutation-proven.
6. **Every all-tasks group** — `TestAllTasksViewPutsDoneAtTheEndOfEachGroup`, plus the capture
   above. Applied inside `visibleGroups`, so a group *is* one scope and no tier detection is
   needed there.
7. **The row moves, the cursor follows, and the round trip restores it** —
   `TestSpaceMovesTheRowToTheEndOfItsTierAndTheCursorFollows`: after `space` the row is last in
   its tier, the pending rows above keep their order, the cursor index equals that last
   position, the row is still in `View()`; after a second `space` the row is back at its
   original index with its original id.
8. **No separator row** — `TestNoSeparatorRowBetweenPendingAndDone` asserts
   `len(m.rows) == len(m.tasks)` and that every merged-view row is a `rowTask`.
9. **Frame invariant with a mostly-done list** — `TestFrameNeverExceedsThePane`'s fixture now
   seeds **27 of 40 rows done**, stamped inside the window, with a frozen clock; it fatals if
   fewer than 40 rows load, so the done rows cannot silently age out and leave the size sweep
   proving nothing. Still asserted on the unclamped `frame()` with the `clampHeight` backstop
   asserted *not* to have fired. 3 versions x 2 views x 9 sizes x 4 filters x 8 modes.
10. **Row width** — `TestRowsNeverExceedTheirWidth` gained done rows (long and short) and now
    sweeps every cursor position, so done-and-selected is covered.
    `TestStrikethroughCostsNoColumns` is the new mechanism check: a done row and an identical
    pending one must render to the same width, which is what would break if the marker ever
    became a character instead of a style.
11. **CLI untouched** — above, file-level and behavioural.
12. **`design.md` amended** — the "Completion vs deletion" section now states the 24h-only rule
    and the end-of-tier placement, with the **old sentence quoted verbatim** in a parenthetical
    that also records why it changed. Nothing was silently overwritten.
13. **Sweep** — above.

### Not verified / limits

- **Rows older than 24h still accumulate invisibly.** `store.PurgeDone` still has no caller,
  exactly as before and as `design.md` records deliberately. Out of scope per the brief.
- **The capture check is one size (80x20) and one seeding.** It is a real-frame smoke test, not
  a sweep; the sweep is `TestFrameNeverExceedsThePane`, which is where the 9 sizes live.
- **`done_at` ties are resolved by the store's order, not by anything this task chose.** Two
  rows completed in the same second keep their newest-first sequence (the sort is stable). That
  is deliberate and is why the tests stamp distinct timestamps rather than relying on it.
