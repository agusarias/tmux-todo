# Completed Task Lifecycle

**Status:** done
**Worktree:** none (removed after merge)

## Goal
`space` toggles completion on the cursor row. A row completed during this popup session
stays visible and struck-through so the action is legible and reversible in the moment.
The view hides done rows completed before this popup opened, or more than 24h ago,
whichever boundary is later. **Rows are never deleted.**

**Design:** docs/design.md — "Completion vs deletion"

## Why
Completion is the most-pressed key in the product, so its feel matters more than its code:
the row must not vanish under the cursor, and a mis-press must be undoable without an undo
stack (`design.md:149` defers one). Keeping rows forever also preserves the option of
history and stats later, which is the reason the store marks `done` rather than deleting.

## Constraints
- **`store.PurgeDone` is not called by normal use.** Hiding is a *view* concern; deletion
  is not part of this task or any other. `PurgeDone` remains a store primitive with no
  caller, available for an explicit `tdo purge` if one is ever wanted.
- **The store stays clock-blind.** `DB.now` is unexported and tests freeze it; the retention
  cutoff is computed by the caller and passed in, never read inside `internal/store`.
- Completion is a toggle, not a one-way action — `store.Uncomplete` already exists.
- Out of scope: `d` delete (`task-create-edit-rescope`), strike-through *rendering* and the
  modal/reload seams (already owned by `popup-tui-merged-list`), deletion of any kind, an
  undo stack, and history/stats features themselves.

## Critical surface
**None** in taskflow's formal sense — no migration, no auth, no external side effects, no
prod data. Worth one caveat: this task is the one that decides tasks are **never reaped**,
so the `tasks` table grows without bound. That is an accepted product decision (see
Decisions), not an oversight.

## Definition of done
1. `space` in **normal mode only** toggles the cursor row: `store.Complete` on a pending
   row, `store.Uncomplete` on a done one. A test proves both directions round-trip.
2. `store.Filter` grows `DoneSince time.Time`. When non-zero it bounds done rows to
   `done_at >= DoneSince`; pending rows are unaffected. Zero value keeps current behaviour,
   so the field is additive and `popup-tui-merged-list`'s existing call site stays valid
   until updated.
3. The TUI passes `DoneSince = max(popupOpenTime, now-24h)`, recomputed on every reload.
   A test freezes `Now`, ages a `done_at` past both boundaries, and asserts the row
   disappears from the rendered view while remaining in the database
   (`Count`/`Get` still finds it).
4. `Now func() time.Time` is injected via `tui.Config`, defaulting to `time.Now`. No
   `time.Now()` call sites inside `internal/tui` outside that default. **This revises
   `popup-tui-merged-list`'s plan note that the TUI holds no clock** — see Decisions.
5. Retention lives in exactly one place: an exported `store.DoneRetention = 24 * time.Hour`
   (or equivalent single definition). No duplicated `24 * time.Hour` literal in `cli` or
   `tui`.
6. After a toggle, the cursor **re-anchors on the same task id**, not on the same index.
   `reloadCmd` gains a preserve-selection parameter. A test completes a mid-list row and
   asserts the cursor still points at that task.
7. A row toggled during this session stays visible and struck-through in its existing
   position — completing does not reorder the list (`listOrder` sorts on `scope_kind` and
   `created_at`, not `done`, so this should hold for free; the test pins it).
8. `Complete`'s doc comment at `internal/store/tasks.go:123-124` is corrected: it currently
   documents a re-stamp-extends-the-purge-window assumption that this task's toggle
   semantics and no-deletion rule both invalidate.
9. The footer hint line gains `space done`, per `popup-tui-merged-list`'s decision that the
   footer grows as tasks land.
10. Filters and empty states account for struck rows: a tier whose only rows are done
    renders those struck rows, not the "matched nothing" text.
11. `go test ./...`, `go vet ./...` clean, `gofmt -l .` empty; `CGO_ENABLED=0 make build`
    yields a binary with no libsqlite3 in `otool -L`.
12. Tests use real SQLite files under `t.TempDir()`.
13. Per the repo-wide decision on CLAUDE.md length, any per-package specifics from this task
    go in package doc comments; CLAUDE.md gets at most a line if a cross-cutting rule
    changed.

## Verification
Headless Go tests with a frozen clock, against real SQLite files in `t.TempDir()`. Because
`Now` is injected (DoD 4), "hidden after 24h" is genuinely provable rather than asserted:
a test can advance the clock 25h and assert the row leaves the view while `Get` still
returns it. That is the whole reason the clock is injected rather than read.

Directly assertable: both toggle directions, the `DoneSince` SQL bound, the
`max(openTime, now-24h)` arithmetic as a pure function, cursor re-anchoring by id,
row position stability across a toggle, filter/empty-state behaviour with struck rows,
and single-definition retention.

**Not verified here:** real-terminal rendering of the strike-through, for the same reason
`popup-tui-merged-list` declares — `tdo tui` has no `--db` flag and the `display-popup`
overlay cannot be asserted headlessly (CLAUDE.md). Evidence must say so rather than imply
the strike-through was seen.

## Decisions
- **2026-08-20 (curator, grill — the brief was self-contradictory):** `design.md:79-80` and
  the original draft both claimed done rows are "purged from the view" *and* that "the row
  remains in the DB" so history stays possible. The only store primitive for this is a hard
  `DELETE FROM tasks WHERE done = 1 AND done_at < ?` (`tasks.go:161-175`), so the two halves
  could not both hold: if it ran on close, "remains in the DB" was false; if it never ran,
  "purges after 24h" was false. **Resolved as hide-only: the view bounds done rows, nothing
  is ever deleted.**
- **2026-08-20 (curator, grill):** This resolution *preserves* rather than supersedes the
  promise in `sqlite-store-and-migrations`' Decisions log — "done rows are never hard-deleted
  by normal use. `PurgeDone` exists for the completed-task-lifecycle task's 24h rule." The
  24h rule turned out to be a visibility rule, so `PurgeDone` simply has no caller. Had we
  chosen deletion, that line would have needed striking.
- **2026-08-20 (curator, grill — ACCEPTED CONSEQUENCE):** With no reaper, the `tasks` table
  grows without bound. Accepted deliberately: rows are tiny, the store is a local SQLite
  file, and unbounded growth is cheaper than losing a user's history. If it ever matters, the
  fix is an explicit `tdo purge` over the existing primitive, not an automatic delete.
- **2026-08-20 (curator, grill — SCOPE EVENT on another brief):** `cli-surface` **DoD 9 is
  struck**. It required every invocation to call `store.PurgeDone` with a 24h cutoff, which
  under hide-only would delete rows before the view rule ever mattered. Signed off by the
  user; `cli-surface` is still `agreed`, so the edit costs nothing. Recorded on that brief
  too.
- **2026-08-20 (curator, grill):** `space` is a **toggle** (`Complete` / `Uncomplete`), which
  matches `design.md:77-78`'s "reversible in the moment" and is the only undo available since
  `design.md:149` defers an undo stack. This contradicts `Complete`'s doc comment, written
  assuming re-completion re-stamps `done_at` to extend a purge window — a window that no
  longer exists. DoD 8 corrects the comment rather than leaving two readings in the tree.
- **2026-08-20 (curator, grill):** The clock question resolved itself once deletion was off
  the table: with no purge call there is no purge *trigger*, so the open-vs-close-vs-both
  question is moot, and the only clock need is the per-reload visibility cutoff. Hence
  injected `tui.Config.Now` (DoD 4) rather than `internal/cli` computing a cutoff once.
  This is a deliberate revision of `popup-tui-merged-list`'s plan note that the TUI holds no
  clock; an *injected* clock keeps that note's intent — the model stays a pure function of
  its inputs — and matches the repo's `DB.now` discipline.
- **2026-08-20 (curator, grill — FOLLOW-UP, do not edit the other brief):**
  `popup-tui-merged-list` is `ready`/in-flight with the executor and its DoD 3 pins
  `Filter{Scopes, IncludeDone: true}` with no `done_at` bound. Per the user's call, it lands
  as approved and **this** task adds `Filter.DoneSince` and updates the call site. DoD 2
  makes the field additive so the two never conflict. The curator must not edit a brief the
  executor owns.
- **2026-08-20 (curator, grill):** Cursor **re-anchors by task id** after a toggle. Index-based
  re-anchoring drifts when the row set changes, and resetting to top makes a toggle unusable
  — you could not press `space` twice on the same row. Requires a preserve-selection
  parameter on the `reloadCmd` seam `popup-tui-merged-list` is shipping.
- **2026-08-20 (curator, grill — left open deliberately):** Whether done rows appear in the
  `g` all-tasks view is **not** decided here. `design.md:88-96`'s mock shows only `[ ]` boxes
  and never `[x]`, and `all-tasks-view-with-sesh-jump` is silent. Flagged for that brief's
  grill rather than pre-empted from here.

- **2026-08-20 (curator, Checkpoint 1 APPROVED):** Plan signed off as written, including the
  near-dead 24h arm (kept to match `design.md`'s "whichever comes first", and DoD 3 must test
  it explicitly or it will never execute). Status `ready`.

## Close-out

### 2026-08-20 — Checkpoint 2 APPROVED (curator)

Merge `03eb37a` stands on local `main`, unpushed. Every Evidence claim re-verified
independently before approval, not taken on trust:

- `make test` green across all five packages, `go vet ./...` silent, `gofmt -l .` empty,
  `CGO_ENABLED=0 make build` linking only `libSystem` + `libresolv` (toolchain
  `go1.26.6 darwin/arm64`).
- All 15 guards this task adds pass on a clean `-count=1` run.
- The three claims a self-report can most easily get wrong, each checked by grep:
  the only `time.Now()` in `internal/tui` is `tui.go:85`'s `Config.now()` fallback;
  `DoneRetention` is defined once at `internal/store/tasks.go:32` with every other hit
  merely reading it; `PurgeDone` has no caller outside its own test.

**Definition of done — all 13 met**, with one item qualified rather than waved through:

- **DoD 6 is met by a different test than the one it names.** The DoD's wording
  ("complete a mid-list row, assert the cursor still points at that task") is
  **vacuous**: completing a row does not reorder the list, so the cursor's index lands
  on the same task whether or not `anchorCursor`'s id lookup exists — the test passes
  with the implementation deleted. The guard that actually discriminates is
  `TestCursorReAnchorsWhenRowsShift`, where another pane inserting rows shifts every
  index. Both tests remain in the tree; **`TestCursorReAnchorsWhenRowsShift` is the one
  that protects the behaviour**, and any future change to id-anchoring should be judged
  against it, not the DoD-worded one. The executor surfaced this itself with the failing
  mutation to prove it — the right call, and the reason it is recorded here rather than
  quietly counted as clean. Generalised into CLAUDE.md as a repo-wide verification rule.
- **DoD 4's revision of `popup-tui-merged-list`'s "the TUI holds no clock" note stands
  as approved.** An injected `Config.Now` preserves that note's intent: the model is
  still a pure function of its inputs.

**Beyond the brief:** Verification declared real-terminal strike-through unprovable. It
was proved anyway — `store.DefaultPath` honours `$XDG_DATA_HOME`, so a seeded throwaway
database yields a reproducible `capture-pane` without a `--db` flag. The capture shows
the two-day-old done row absent on open but still in the database, the toggled row
holding its position, `done_at` cleared on undo, and exactly one row carrying `ESC[2;9m`.
That closes a stated verification gap instead of accepting it.

**Known regression, deliberately not fixed here — accepted, and since resolved:** adding
`space done` widened the footer, pushing the minimum popup width to 58 (`dev`) / 62
(`ec132f9`) / 78 (`-dirty`) columns, all above `design.md:47`'s ~48-column popup on an
80x20 terminal. The executor deferred the fix to `popup-tui-merged-list`'s DoD 20 rather
than absorbing another task's scope, which was the right call and is why this task was
approved with the regression open.

**It did not stay open.** `popup-tui-merged-list`'s fix-forward pass landed as `e831b42`
minutes after this task's `03eb37a`, truncating `footer()` and `titleLine()` to the
content width and adding `TestFrameNeverExceedsThePane` — 108 subtests over pane sizes x
version stamps x filter states. So the wider footer this task introduced is now covered by
an invariant that fails 56 subtests if the truncation is removed, which is a stronger
outcome than fixing it here would have been: the *class* is closed, not just the instance.
No user was ever exposed — the `display-popup` keybind lives in
`tmux-integration-and-rename-hook`, which does not exist yet.

**CLAUDE.md:** one cross-cutting entry added — *"A DoD can specify a vacuous test"* —
sitting with the existing pitfalls about tests that cannot fail. Per DoD 13, no
per-package detail was added; that lives in the doc comments on `Filter.DoneSince`,
`Complete`, and `Config.Now`.

**Still open, by design:** `store.PurgeDone` keeps no caller (Constraints); whether done
rows appear in the `g` all-tasks view is left to `all-tasks-view-with-sesh-jump`'s grill.

**Worktree:** already removed by the executor before this checkpoint. `main` is unpushed —
pushing remains the user's call.

## Decisions (execution)
- **2026-08-20 (executor):** The toggle write and the re-query are **one** `tea.Cmd`, not a
  `tea.Sequence` of two. Sequence was the first shape and it is untestable: it produces an
  unexported `sequenceMsg` that only the Bubble Tea runtime can unwrap, so a test could not
  drive the toggle at all. One command also keeps the store the single source of truth —
  the model never patches its own copy of a row to reflect a write, it re-reads.
- **2026-08-20 (executor):** `writeThenReload` generalises the `reloadCmd` seam rather than
  adding a parallel path: `reloadCmd` is now `writeThenReload(0, nil)`. The plan asked for a
  "preserve-selection parameter"; making the anchor a field on `rowsMsg` puts it where the
  rows arrive, so the cursor is re-anchored in exactly one place regardless of what
  triggered the reload.
- **2026-08-20 (executor):** The view now renders the store's error verbatim instead of
  prefixing "cannot read tasks". With writes flowing through the same error field that
  prefix would have been wrong half the time; the store already wraps its errors with the
  operation ("complete task 3: …", "list tasks: …").
- **2026-08-20 (executor):** DoD 6's test as worded is **vacuous on its own** and is kept
  only because the DoD names it. Completing a row does not reorder the list, so the cursor's
  index still lands on the same task whether or not the id lookup exists — the test passes
  with `anchorCursor`'s body deleted. `TestCursorReAnchorsWhenRowsShift` is the one that
  actually discriminates: another pane adding tasks shifts every index, which is the real
  case id-anchoring exists for. Both are in the tree, and the mutation results below show
  which one earns its keep.

## Plan
**Approach:** the whole task is one store field, one injected clock, one key binding, and a
cursor-anchoring fix. The design intent — "the row must not vanish under your cursor" —
falls out of doing those four things in that order, with the `DoneSince` bound proved in SQL
before any TUI code exists.

**Files:**
- `internal/store/tasks.go` — add `DoneSince` to `Filter`, apply it in the `List` predicate,
  fix `Complete`'s doc comment (DoD 8), add the exported retention constant.
- `internal/store/tasks_test.go` — `DoneSince` bound tests, including the pending-rows-
  unaffected case and the zero-value-is-current-behaviour case.
- `internal/tui/tui.go` — `Config.Now`, the `space` binding in normal mode, the cutoff
  computation, `reloadCmd` preserve-selection parameter.
- `internal/tui/tui_test.go` — toggle both directions, frozen-clock hiding, cursor
  re-anchoring, position stability, struck rows vs empty state.
- `internal/tui/render.go` — footer gains `space done`.
- `docs/tasks/2026-08-19-cli-surface.md` — strike DoD 9 (already done by the curator).
- `docs/design.md` — rewrite lines 79-80 to state hide-only and drop the deletion reading.

**Sequencing:**
1. `Filter.DoneSince` in the store, with tests, before touching the TUI. The SQL predicate is
   the only part that can be subtly wrong (`>=` vs `>`, and not catching pending rows).
2. Retention constant + `Complete` doc comment fix — small, and gets the store's story
   consistent before callers arrive.
3. `Config.Now` + the cutoff function as a pure `max(openTime, now-retention)` helper with a
   table test. No Bubble Tea involved.
4. `space` toggle wired to the store, then `reloadCmd` preserve-selection, then the cursor
   test. Toggle first so the cursor test has something to act on.
5. Footer, filter/empty-state interaction with struck rows.
6. `docs/design.md` rewrite — last, so it describes what actually shipped.
7. `make test`, `make lint`, `CGO_ENABLED=0 make build`, `otool -L`.

**What could go wrong:**
- **`DoneSince` accidentally filtering pending rows.** The predicate must apply only where
  `done = 1`; a naive `AND done_at >= ?` drops every pending row, whose `done_at` is NULL.
  Step 1's test for this is the single most important one in the task.
- **`popup-tui-merged-list` landing differently than its brief says**, changing the call site
  or the `reloadCmd` shape this task extends. Mitigated by DoD 2's additive field; if the seam
  arrived in a different shape, revise the *how* and log it rather than treating it as a
  scope event.
- **The 24h boundary being effectively dead code.** Since `popupOpenTime` is almost always
  later than `now-24h`, the 24h arm only fires for a popup left open more than a day. It is
  kept because `design.md` specifies "whichever comes first", and it is cheap — but a test
  must cover it explicitly or it will never execute.
- **Cursor re-anchoring when the anchored task leaves the view** — e.g. toggling a row that
  was already outside the done window. Clamp to the nearest surviving index rather than
  leaving the cursor pointing at a missing id.
- **Two sources of truth for visibility** if the TUI also filters after `List`. It must not:
  the bound belongs in SQL, per `popup-tui-merged-list` DoD 4 ("render order equals `List`
  order, the TUI performs no sort").

## Evidence

**Merge commit:** `03eb37a` on local `main` (unpushed) — `git show 03eb37a`,
`git log -1 -p 4e1ced1` for the implementation commit itself.

Toolchain `go1.26.6 darwin/arm64`.

### Tests

`make test` green across every package; `go vet ./...` silent; `gofmt -l .` empty.

```
$ make test
go test ./...
?   	github.com/agusarias/tmux-todo/cmd/tdo	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/cli	0.534s
ok  	github.com/agusarias/tmux-todo/internal/scope	1.080s
ok  	github.com/agusarias/tmux-todo/internal/store	0.990s
ok  	github.com/agusarias/tmux-todo/internal/task	1.025s
ok  	github.com/agusarias/tmux-todo/internal/tui	1.318s
```

`internal/store` + `internal/tui` now hold 83 passing tests; this task adds 7 store tests
and 13 TUI tests (`internal/tui/lifecycle_test.go`).

### Mutation results — every guard shown failing first

**The predicate the plan called the most dangerous** (DoD 2). Replacing
`(done = 0 OR done_at >= ?)` with the naive `done_at >= ?`:

```
--- FAIL: TestDoneSinceBoundsOnlyDoneRows
    the pending task was filtered out by DoneSince; the predicate is catching NULL done_at
```

That is the failure mode that would empty the popup: a pending row's `done_at` is NULL and
no comparison against NULL is true. Changing `>=` to `>`:

```
--- FAIL: TestDoneSinceIsInclusive
    a row completed exactly at the cutoff was hidden; the bound is exclusive
```

**The retention arithmetic** (DoD 3). Making `doneSince()` return `openedAt` unconditionally,
which drops the 24h arm the plan warned would otherwise never execute:

```
--- FAIL: TestDoneRowHiddenAfterRetentionWindow
    rows = ["completed in this session"] past the retention window, want it hidden
--- FAIL: TestDoneSinceIsTheLaterBoundary
    with a long-open popup doneSince = ...-10-06..., want the retention cutoff ...-10-08...
```

**The toggle** (DoD 1). Making `space` complete-only:

```
--- FAIL: TestSpaceTogglesBothDirections
    space did not uncomplete an already-done row
```

**The TUI actually passing the bound.** Dropping `DoneSince` from the filter:

```
--- FAIL: TestDoneRowCompletedBeforeOpenIsHidden
    rows = ["still to do" "finished earlier"], want only the pending task
--- FAIL: TestDoneRowHiddenAfterRetentionWindow
```

**Cursor anchoring** (DoD 6) — and the one negative result worth reading. Deleting the id
lookup from `anchorCursor`:

```
$ go test -run CursorReAnchors        # with anchorCursor's body deleted
ok  	github.com/agusarias/tmux-todo/internal/tui
```

DoD 6's test as specified does not fail, because completing a row does not reorder the list,
so the index lands on the same task either way. The added
`TestCursorReAnchorsWhenRowsShift` does fail, which is why it exists:

```
--- FAIL: TestCursorReAnchorsWhenRowsShift
    cursor landed on "from another pane", want to stay anchored to "second"
    cursor index is still 1; the rows did not shift, so this test proves nothing
```

### Real-terminal check

The brief said this could not be verified. It can: `store.DefaultPath` honours
`$XDG_DATA_HOME`, so a seeded throwaway database gives a reproducible `capture-pane` with
no `--db` flag (now recorded in CLAUDE.md). Seeded across all three tiers plus one row
completed two days ago, at 78x14:

```
│  ▸ ⌘ rebase onto main        (session: tdodone)                            │
│    ⌘ check CI                                                              │
│    · fix auth redirect       (dir: ~/.claude/jobs/a9c0f4db/tmp/xdg2/repo)  │
│    ◉ call the dentist       (global)                                       │
```

The two-day-old completed row is absent on open, as the rule requires — and still in the
database. Then `j`, `space`:

- the row stayed put under the cursor and did not reorder;
- `sqlite3` shows `2|1|check CI`, and after a second `space`, `2|0||check CI` — the toggle
  round-trips through the real binary, and `done_at` is cleared on undo;
- exactly one row in the frame carries the strike escape (`ESC[2;9m`, SGR 9 =
  strikethrough, 2 = faint):

```
│  ▸ · ESC[2;9mfixESC[0;9m ESC[2mauthESC[0;9m ESC[2mredirectESC[0m       ESC[2m(dir: …/repo)ESC[0m  │
```

A second run gave incidental confirmation of the open-boundary rule: `check CI`, completed
in the *previous* popup, was hidden when the next popup opened, so `j` landed on a
different row.

Footer (DoD 9):

```
│  1/2/3 filter · j/k move · space done · q quit · v70a4619-dirty            │
```

### Static binary and retention single-definition

`CGO_ENABLED=0 make build` links no libsqlite3 (`libSystem`, `libresolv` only). Grepping the
tree for a duplicated 24h literal finds only the definition itself and two test references
(one an unrelated `PurgeDone` test, one the assertion pinning the constant):

```
internal/store/tasks.go:32:const DoneRetention = 24 * time.Hour
internal/store/tasks_test.go:672:	if DoneRetention != 24*time.Hour {
```

### Definition of done

| # | Item | Status |
|---|---|---|
| 1 | `space` toggles both directions in normal mode | met — round-trip test, mutation-checked |
| 2 | `Filter.DoneSince`, additive, bounds done rows only | met — 5 store tests, both mutations caught |
| 3 | TUI passes `max(openTime, now-24h)`, recomputed per reload | met — frozen clock, row hidden while `Get` still finds it |
| 4 | `Config.Now` injected, defaulting to `time.Now` | met — no other `time.Now()` in `internal/tui` |
| 5 | Retention defined once | met — `store.DoneRetention`, grep above |
| 6 | Cursor re-anchors by task id | met — with the caveat above: the DoD-worded test is vacuous, `TestCursorReAnchorsWhenRowsShift` is the real guard |
| 7 | Toggled row stays visible, struck, in position | met — headless and in a real terminal |
| 8 | `Complete`'s doc comment corrected | met |
| 9 | Footer gains `space done` | met — **see the regression below** |
| 10 | A tier of only-done rows is not an empty state | met — `TestStruckRowsAreNotAnEmptyState` |
| 11 | tests / vet / gofmt / static build | met |
| 12 | Real SQLite under `t.TempDir()` | met |
| 13 | Per-package detail in doc comments, not CLAUDE.md | met — no CLAUDE.md change in this task |

### Regression this task introduces into a known live defect

DoD 9 required the footer to grow, and it does — which makes
`popup-tui-merged-list`'s **rejected** Checkpoint 2 defect materially worse. That task's
`footer()` is never truncated to the content width while `chromeHeight` assumes it occupies
one row, so a wrapped footer scrolls the top of the frame away. Measured minimum popup width
before and after adding `space done`:

| `Version` stamp | before | after |
|---|---|---|
| `dev` | 45 | **58** |
| `ec132f9` | 49 | **62** |
| `v0.1.0-3-gec132f9-dirty` | 65 | **78** |

`docs/design.md:47` sizes the popup at ~60%x60%, i.e. 48 columns on a standard 80x20
terminal — now below the threshold for every build stamp, where before only a `-dirty`
describe crossed it.

**Deliberately not fixed here.** Truncating `footer()` and adding the frame-invariant test
is `popup-tui-merged-list`'s DoD 20 and its fix-forward plan delta; that task is `ready` with
a written plan, and absorbing its scope would duplicate the work and muddy both diffs. Flagged
so the curator can sequence it next — it is the natural next pick, and this task raises its
urgency rather than creating it. No user is exposed yet: the `display-popup` keybind and
sizing belong to `tmux-integration-and-rename-hook` and do not exist, so nothing runs the
popup at 48 columns today.

### Not verified

- `store.PurgeDone` still has no caller, by design (Constraints). Nothing in this task
  deletes a row, and `TestDoneSinceBoundsOnlyDoneRows` asserts the aged-out row is still
  retrievable via `Get` and still counted by `Count`.
- Whether done rows appear in the `g` all-tasks view — left open by the brief for
  `all-tasks-view-with-sesh-jump`, and untouched here.
- Behaviour of a popup left open across a real 24h boundary was proven with a frozen clock,
  not with a real one; that is what injecting the clock bought.

