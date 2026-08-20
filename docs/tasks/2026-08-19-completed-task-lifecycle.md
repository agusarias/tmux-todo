# Completed Task Lifecycle

**Status:** ready
**Worktree:** none

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
