# Popup TUI Merged List

**Status:** ready
**Worktree:** none

## Goal
The default popup view: session, dir and global tasks merged into one list with a per-row
scope glyph, ordered by scope tier then newest first, with navigation, `1/2/3` scope
filters, and `q`/`Esc` to close. The popup stays open across actions.

**Design:** docs/design.md — "Popup UX / Default view"

## Why
This is the product. Everything else built so far — the store, scope resolution, the CLI —
exists to make this list appear in under 100ms. It is also the foundation the other three
UI tasks (`task-create-edit-rescope`, `completed-task-lifecycle`,
`all-tasks-view-with-sesh-jump`) extend, so the seams it leaves determine whether those
tasks are additions or refactors.

## Constraints
- **The TUI must not sort or group.** `internal/store/tasks.go:17-23` already implements
  `tierOrder` + `listOrder` (`CASE scope_kind …, created_at DESC, id DESC`), and `List`
  returns exactly the merged order the popup needs. Re-sorting in the view would be a
  second source of truth for ordering.
- **`internal/tui` must not import `internal/scope`.** Resolved scopes are injected. This
  keeps the model unit-testable without tmux and unblocks this task while `scope-resolution`
  is still in flight.
- **`Filter.Scopes` must always be populated explicitly.** Empty means *every scope in the
  database* (`tasks.go:28-35`), not the active merged set — see Decisions.
- `internal/cli` already imports `internal/tui` (`internal/cli/cli.go:14`), so the TUI
  cannot reuse `cli-surface`'s `env` / `filter` / `addScope` helpers. The active-set →
  `store.Filter` mapping will exist in both packages unless a later task pushes a shared
  helper down into `store` or `scope`.
- Out of scope: `a` / `e` / `s` / `d` keybindings (owned by `task-create-edit-rescope`),
  the `space` completion action and the 24h purge policy (owned by
  `completed-task-lifecycle`), the `g` all-tasks view itself (owned by
  `all-tasks-view-with-sesh-jump`), and the `display-popup` keybind and sizing (owned by
  `tmux-integration-and-rename-hook`).

## Critical surface
**None.** This task is read-only against the database (`List` only), adds no migration,
touches no auth, sends nothing externally, and publishes no API beyond the internal
`tui.Run` signature. Review depth is "shape", not line-by-line.

## Definition of done
1. `tui.Run(Config) error` and `New(Config) Model`, where `Config` carries `*store.DB`,
   `Scopes []task.Scope` (resolved active scopes, tier-ordered), `Home string` (for the
   display transform) and `Version string`. `internal/cli` resolves and injects.
2. `internal/tui` does not import `internal/scope` — asserted by a test reading
   `go list -deps` or equivalent, not just by inspection.
3. The model queries `store.List` with `Filter{Scopes: cfg.Scopes, IncludeDone: true}`.
   A test seeds a task in a scope that is **not** in `cfg.Scopes` and asserts it does not
   appear — this is the trap named in Decisions.
4. Render order equals `List` order exactly; the TUI performs no sort. Proven by a test
   comparing rendered row order against the slice `List` returned.
5. Per-row glyph: `⌘` session, `·` dir, `◉` global. The tier label
   (`(session: pulsar)` / `(dir: ~/ws/pulsar)` / `(global)`) appears on the **first row of
   each tier only**, per `docs/design.md:63-67`.
6. Dir labels are home-abbreviated for display (`/Users/x/ws/p` → `~/ws/p`) via a
   display-only helper. Stored scope keys stay absolute and symlink-resolved — the
   transform never reaches the database.
7. `1` / `2` / `3` filter the list to a single tier; pressing the same digit again returns
   to the merged view. Cursor resets to the top on any filter change.
8. Done tasks render struck-through in place (lipgloss `Strikethrough`), in their normal
   position in the order.
9. `q`, `Esc` and `Ctrl-C` quit. Unhandled keys in normal mode do nothing and do not quit.
10. Navigation: `j`/`k` and up/down arrows, cursor clamped at both ends.
11. Scrolling via `charmbracelet/bubbles/viewport`; task text longer than the available
    width is truncated with an ellipsis rather than wrapped.
12. **Seam — modal key dispatch:** a `mode` field on the model with the key handler
    dispatching on it, so `task-create-edit-rescope` can add an input mode in which `q` is
    a literal keystroke rather than quit. Only normal mode exists now.
13. **Seam — reload:** a `reloadCmd` `tea.Cmd` that re-runs `store.List` and replaces the
    rows, used on filter change. This is what "the popup stays open across actions" means
    in a task that has no mutations yet.
14. **Seam — view mode:** a view-mode field with a single `viewMerged` value, so `g` is a
    new case rather than a restructure. `g` is **not** bound in this task.
15. Empty states: distinct text for an empty database and for a filter that matched nothing.
16. The footer hint line lists **only the keys this task implements** — see Decisions on the
    `design.md:71` mock.
17. Version remains visible in the view (the existing `TestViewMentionsVersion` contract
    survives the placeholder's removal).
18. `go test ./...`, `go vet ./...` clean, `gofmt -l .` empty; `CGO_ENABLED=0 make build`
    still yields a binary with no libsqlite3 in `otool -L`.
19. Tests use real SQLite files under `t.TempDir()`, per repo convention.

## Verification
Headless Go tests only, against real SQLite files in `t.TempDir()`. `Update` and `View`
are pure functions, so row order, glyph and label placement, the `~` transform, filter
toggling, cursor movement and clamping, strike-through, empty states, and
"unhandled keys don't quit" are all directly assertable.

**Explicitly NOT verified by this task, and must be stated as such in Evidence:**
real-terminal rendering. No `tmux new-session` + `capture-pane` check runs here, because
`tdo tui` has no `--db` flag and pointing a capture at the user's real database would not
be reproducible. Column alignment and glyph width in a live terminal are therefore
reasoned about, not proven — see the accepted risk in Decisions. The `display-popup`
overlay remains unverifiable headlessly regardless (CLAUDE.md), and lands with
`tmux-integration-and-rename-hook`.

## Decisions
- **2026-08-20 (curator, grill):** Ordering is the store's job, not the view's. The draft's
  "ordered by scope tier then newest first" described work `listOrder` already does; an
  implementer reading the brief alone would have built a redundant sort layer. Constraint
  added, and DoD 4 now tests that the view preserves `List` order rather than producing it.
- **2026-08-20 (curator, grill):** `Filter.Scopes` empty means every scope in the database,
  not the active merged set. `cli-surface`'s plan already flags this as the likeliest bug in
  that task; the popup has the identical trap and the draft was silent on it. DoD 3 makes it
  a test with a negative assertion rather than a note.
- **2026-08-20 (curator, grill):** Scopes are **injected**, not resolved by the TUI:
  `Config.Scopes []task.Scope` populated by `internal/cli`. This unblocks the task while
  `scope-resolution` is `in-progress` (unlike `cli-surface`, which stays parked at `agreed`
  because it genuinely calls `scope.Resolve`), and makes the model testable with fakes.
  Refinement at plan time: the approved shape was `tui.Run(db, resolved, version)`; it
  became a `Config` struct so `Home` has somewhere to live without the TUI calling
  `os.UserHomeDir` itself. Same injection boundary, one extra field.
- **2026-08-20 (curator, grill):** This task owns the **seams** as well as the list — modal
  key dispatch, `reloadCmd`, and a view-mode field (DoD 12-14). The three follow-on UI tasks
  then add cases instead of restructuring the key handler. Chosen over a strictly read-only
  flat switch precisely because `task-create-edit-rescope` would otherwise have to refactor
  `q` handling to type a literal `q` into an input row.
- **2026-08-20 (curator, grill):** Done rows are **included and struck-through here**
  (`IncludeDone: true`), not deferred. **Consequence: `completed-task-lifecycle` narrows** —
  it keeps the `space` keybinding, the purge-on-close behaviour and the 24h retention policy
  feeding `store.PurgeDone`, but no longer owns strike-through rendering. Its brief should be
  amended when the curator next picks it up.
- **2026-08-20 (curator, grill):** Filter clearing is **same-digit toggle**. `design.md:71`
  advertises `1/2/3 filter` with no un-filter key, and `Esc` is already bound to close;
  making `Esc` clear-then-close would contradict `design.md:70` and make the popup feel
  sticky. No new key, no design-doc edit.
- **2026-08-20 (curator, grill):** Row format follows the mock exactly — glyph on every row,
  tier label on the first row of each tier, dir path home-abbreviated. The prose "per-row
  scope glyph" and the mock's per-tier label are not in conflict once read as glyph-per-row
  plus label-once. The `~` form requires a display transform that did not exist anywhere,
  because `scope-resolution` DoD 6 commits to absolute symlink-resolved keys; DoD 6 here
  confines that transform to rendering.
- **2026-08-20 (curator, grill):** `charmbracelet/bubbles` is added now for `viewport`.
  `task-create-edit-rescope` will want `textinput` from the same module, so the dependency
  is paid once. Silently capping the list was rejected: hiding tasks is the worst failure
  mode a TODO manager has.
- **2026-08-20 (curator, grill — ACCEPTED RISK):** The glyphs `⌘` (U+2318) and `◉` (U+25C9)
  are East-Asian **ambiguous** width. Under `ambiguous-width=double` they occupy two cells
  and every column shifts. Keeping them as-is was chosen over `runewidth` padding or ASCII
  substitutes; combined with the headless-only verification above, this means misalignment
  in such terminals would first surface as a user report. Revisit if
  `tmux-integration-and-rename-hook` adds a real `capture-pane` check.
- **2026-08-20 (curator):** `design.md:71`'s footer shows
  `1/2/3 filter · a add · e edit · space done · d delete · s re-scope · g all · q quit`, but
  `a e space d s g` are owned by the three follow-on tasks. Reading that mock as the **end
  state** rather than this task's state: the footer lists only implemented keys and grows as
  those tasks land. Shipping a hint line of dead keys was not a real option.

- **2026-08-20 (curator, Checkpoint 1 APPROVED):** Plan signed off as written, including the
  accepted glyph risk and the footer reading of `design.md:71`. Status `ready`.

## Plan
**Approach:** replace the placeholder model with a real list model that is a pure function
of injected data. No tmux, no scope resolution and no clock inside `internal/tui` — the
package takes a `*store.DB`, a scope set, a home path and a version string, and everything
else is `Update`/`View` over that. This is what makes DoD's headless verification honest
rather than a concession.

**Files:**
- `internal/tui/tui.go` — `Config`, `Model`, `New`, `Run`, `Init`, `Update`, `View`. The
  placeholder model and its version-only view are deleted.
- `internal/tui/render.go` — row formatting: glyph, first-row-of-tier label, `~`
  abbreviation, truncation, strike-through. Split out because this is the bulk of the
  testable surface.
- `internal/tui/tui_test.go` — extended; `TestQuitKeys` and `TestUnhandledKeyKeepsRunning`
  survive, `TestViewMentionsVersion` is rewritten against the new footer.
- `internal/tui/render_test.go` — new.
- `internal/cli/cli.go` — `tdo tui` builds the `Config` and injects. Placeholder scope set
  until `scope-resolution` lands (see step 1).
- `go.mod` / `go.sum` — add `charmbracelet/bubbles`.

**Sequencing:**
1. `Config` + injection boundary first, with `cli` passing a hardcoded global-only scope
   set. Everything downstream is then testable, and swapping in `scope.Resolve` once
   `scope-resolution` is `done` is a one-line change at the call site.
2. `reloadCmd` + `Init` loading rows via `store.List` with `Scopes` populated and
   `IncludeDone: true`. Write DoD 3's negative test here, before any rendering exists.
3. `render.go`: glyph, tier label, `~`, truncation, strike-through — pure string functions
   with table tests. No Bubble Tea involved.
4. `viewport` + navigation + clamping.
5. Modal dispatch, filter toggling (cursor reset), view-mode field, empty states.
6. Footer with implemented keys only; rewrite the version test.
7. `make test`, `make lint`, `CGO_ENABLED=0 make build`, `otool -L`, cold-start check
   against the ~100ms budget.

**What could go wrong:**
- **`Filter.Scopes` left empty** — the trap in DoD 3. Mitigated by writing that test in
  step 2, before rendering can distract from it.
- **Re-sorting in the view.** Easy to do accidentally when grouping rows by tier for the
  label logic. Step 3's row formatter must consume the slice in the order it is given;
  DoD 4 tests exactly this.
- **Column alignment** under ambiguous-width terminals — accepted risk, unverifiable here.
- **`bubbles/viewport` fighting a fixed popup size.** Popups do not resize once open, so
  `WindowSizeMsg` handling may be effectively dead code; wire it anyway rather than
  hardcoding 60%x60%, since sizing belongs to `tmux-integration-and-rename-hook`.
- **Cold start regression.** Adding `bubbles` and real querying to a path currently
  budgeted at ~8ms against ~100ms; step 7 measures rather than assumes.
