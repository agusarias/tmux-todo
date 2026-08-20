# Popup TUI Merged List

**Status:** in-progress
**Worktree:** /Users/agusarias/workspace/todo-popup-fix

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
20. **The assembled frame never exceeds the pane in either dimension.** A committed
    table test over `View()` across several widths and heights — including narrow ones
    (40, 44, 48, 64 columns) and a long `Version` string — asserts that the rendered
    frame's line count is <= the model's height and that no line's display width
    exceeds the model's width. Truncating `footer()` (and `titleLine()`) to the content
    width is the fix; the test is what closes the class. `chromeHeight`'s assumption
    that the footer is exactly one row must either hold by construction or be counted.

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

- **2026-08-20 (executor):** `Config.Scopes` is fed from `scope.Resolved.Active()` in
  `internal/cli`, not from the hardcoded global-only placeholder the plan's step 1 called
  for. The plan wrote that placeholder while `scope-resolution` was `in-progress`; that code
  is now merged on `main`, and `Active()` already returns exactly the tier-ordered
  `[]task.Scope` this task's `Config` wants. Using it satisfies DoD 1's "internal/cli
  resolves and injects" instead of deferring it, and leaves the `internal/tui` →
  `internal/scope` boundary untouched (resolution stays in `cli`). Revising the *how*, not
  the *what*.
- **2026-08-20 (executor):** The tier label triggers on a change of **scope** (kind *and*
  key), not merely of tier. With one scope per tier — what `cli` injects — this is identical
  to DoD 5. It differs only if two scopes of the same kind are ever active, where reusing
  the first one's label would attribute tasks to the wrong repo. `TestTierLabelOnFirstRowOf
  EachTierOnly` pins the DoD case; `TestLabelRepeatsForANewScopeInTheSameTier` pins the
  extension.
- **2026-08-20 (executor):** Strike-through is asserted on the **style object**
  (`textStyle(...).GetStrikethrough()`), not on rendered output. A test process has no
  colour profile, so lipgloss renders every style down to plain text and an ANSI-level
  assertion passes whatever style is chosen — the first version of that test was green
  against a deliberately wrong implementation. The real-terminal capture below is what
  proves the escapes actually ship.
- **2026-08-20 (executor):** A cursor marker (`▸ `, with a two-column blank standing in for
  it) was added, which the `design.md` mock does not show. Colour alone would have been
  invisible both in a no-profile test and to the assertion above; the mark keeps the glyph
  column stable whether or not a row is selected.
- **2026-08-20 (executor):** Real-terminal verification **was** run, contrary to the
  Verification section's expectation that it could not be. That section ruled it out because
  `tdo tui` has no `--db` flag, but `store.DefaultPath` honours `$XDG_DATA_HOME`, which makes
  a throwaway database reproducible without one. It earned its keep: it caught three defects
  the headless tests could not see (see Evidence).

### 2026-08-20 — Checkpoint 2 rejected (1st): footer overflows the frame. Fix forward.
The 19 DoD items were all met and the evidence held up under audit (mutation-testing the
`len(scopes)==0` short circuit and the `renderRows` ordering guard reproduced the quoted
failures exactly; `otool -L` clean; 38 tests real). Rejected anyway for a defect the DoD
did not cover:

`footer()` (`tui.go:368`) is never truncated to the content width, while `chromeHeight`
(`tui.go:40`) is a constant `6` that assumes the footer occupies exactly one row. When the
footer wraps, the frame is one row taller than the pane, and per this repo's own pitfall an
over-tall frame makes the terminal **scroll** — so the rows lost are the *top* ones (box
border, then session tier and tier labels), not the bottom.

Footer width is `36 + len(Version)`, and `Makefile:3` stamps `Version` from
`git describe --tags --always --dirty`. So the minimum usable popup width **depends on the
build's git state**: `dev` wraps below 45 columns, this repo's current `ec132f9` below 49,
a `-dirty` describe (23 chars) below 65. Reproduced in a tmux pane at 48x12 — which is
`design.md:47`'s `~60%x60%` on a standard 80x20 terminal — with the top border already
scrolled off. `design.md:71`'s end-state footer is 92 columns, so every follow-on UI task
raises the threshold.

Chosen: **fix forward** (the merge stays; no revert). The change is additive and the rest of
the task is sound.

Root cause is the missing test, not the missing truncate: none of the 38 tests asserts
anything about the *assembled frame's* dimensions. All three frame bugs found in this task
were caught by a capture at a single fixed 80x20 size. Hence new **DoD 20** — a frame
invariant test across widths — which is what actually closes the class. Truncating the
footer alone would leave the next added key free to reintroduce it.

Also folded in from the audit, non-blocking:
- `render.go:246` — `textStyle`'s godoc opens with a copy-pasted `truncateLeft` sentence.
  Fix while in there.
- `internal/cli/cli.go`'s 43 new lines (`runTUI`: store open, scope resolve, Config
  assembly) have **zero** test coverage. Not a new DoD item, but worth a smoke test if
  cheap.
- `go.mod` bumped 5 transitive deps and added 3 new ones beyond `bubbles`, unremarked in
  Evidence. `clipperhouse/displaywidth` now backs `lipgloss.Width` — the primitive the
  accepted ambiguous-width risk depends on. The risk was accepted against a different
  implementation than the one now in the tree; note it in Evidence next time, and DoD 20's
  width assertions now exercise it.
- The blocker recorded under "Blocking finding for another task" is **resolved**:
  `scope-resolution`'s fix-forward merged as `d69dd4d`, and a session-scoped task now
  renders with its tier label in a real tmux pane. That section is history, not a live issue.

### Plan delta for the fix-forward pass
Scope is small and additive; the approved approach below still stands.
1. Truncate `footer()` and `titleLine()` to the content width (`m.width - chromeWidth`),
   reusing the existing `truncate`/`ellipsis` helpers in `render.go` rather than a new one.
2. Add the DoD 20 table test in `tui_test.go`: for each of several `(width, height)` pairs
   and both a short and a long `Version`, build the model, feed a
   `tea.WindowSizeMsg`, render `View()`, and assert `len(lines) <= height` and
   `lipgloss.Width(line) <= width` for every line. Seed enough rows to fill the viewport,
   since the bug only shows with a full list.
3. Decide explicitly whether `chromeHeight` stays a constant (safe once both chrome lines
   are truncated) or becomes computed; if it stays constant, say why in a comment, because
   the constant is what made this bug invisible.
4. Fix the `render.go:246` godoc while in the file.
5. Re-run the full verification sweep from DoD 18, and re-run the tmux `capture-pane`
   check at 48x12 and 64x14 specifically — the sizes that failed.

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

## Evidence

**Merge commit:** `a0155d9` on local `main` (unpushed) — `git show a0155d9`,
`git log -1 -p 816b027` for the implementation commit itself.

Toolchain: `go1.26.6 darwin/arm64` from `/opt/homebrew/bin/go`.

### Tests

`make test` — all packages pass; `internal/tui` goes from 3 placeholder tests to 38.

```
$ make test
go test ./...
?   	github.com/agusarias/tmux-todo/cmd/tdo	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/cli	0.236s
ok  	github.com/agusarias/tmux-todo/internal/scope	(cached)
ok  	github.com/agusarias/tmux-todo/internal/store	(cached)
ok  	github.com/agusarias/tmux-todo/internal/task	(cached)
ok  	github.com/agusarias/tmux-todo/internal/tui	0.521s
```

`make lint` (`go vet ./...`) clean; `gofmt -l .` empty.

### The two traps the brief named, mutation-tested

A passing test proves nothing until it has been seen to fail, so each guard was
re-run against a deliberately broken implementation.

**DoD 2 — the `internal/tui` → `internal/scope` boundary.** Adding that import:

```
--- FAIL: TestDoesNotImportScope
    tui_test.go:143: internal/tui reaches internal/scope:
      github.com/agusarias/tmux-todo/internal/tui -> github.com/agusarias/tmux-todo/internal/scope
```

The test walks the module-local import graph transitively (`go/parser`, no subprocess),
so a *transitive* reintroduction fails too, and it self-checks that the walk actually
reached `internal/store` rather than silently finding nothing.

**DoD 3 — `Filter.Scopes` left empty meaning "every scope in the database".** Removing
the empty-set short circuit:

```
--- FAIL: TestEmptyConfigScopesShowsNothing
    rows = ["session task" "global task"], want none for an empty scope set
--- FAIL: TestFilterOnUnavailableTierIsEmpty
    rows = ["a session task" "global task"], want none: no session scope is active
```

And dropping `Scopes` from the filter entirely:

```
--- FAIL: TestQueryIsScopedToConfig
    rows = ["someone else's session" "mine" "another repo"], want only [mine]: the filter leaked to inactive scopes
--- FAIL: TestFilterKeysNarrowToOneTier
    "1" filtered to ["session task" "dir task" "global task"], want only [session task]
```

**DoD 4 — no re-sorting in the view.** Adding an alphabetical sort to `renderRows`:

```
--- FAIL: TestRenderOrderMatchesStore
    row 0 = "▸ · dir newer …", want the text "session only" from List position 0
```

### Real-terminal capture — and the three defects it caught

The brief expected this to be impossible. It is not: `store.DefaultPath` honours
`$XDG_DATA_HOME`, so a seeded throwaway database plus `tmux new-session` +
`capture-pane` gives a reproducible check with no `--db` flag and without touching the
user's real database. **This found three defects that every headless test missed**, which
is the most useful thing in this report:

1. **The frame was taller than the pane.** `chromeHeight` counted the title, footer and
   blank lines but not the box's two border rows, and `View` added a trailing newline on
   top. The frame overflowed by three rows, and because a terminal scrolls rather than
   clips, the rows that vanished were the *top* ones — the session tier and every tier
   label, i.e. exactly what the merged view exists to show. Headless tests asserted on
   `renderRows`, which was correct throughout; nothing measured the assembled frame.
2. **Tier labels vanished entirely for real dir keys.** A dir scope key is an absolute
   path, and labels were given only the width left over after the text. A long path made
   that budget negative, the row overflowed, and the viewport clipped the label away —
   silently. The headless test used `~/ws/pulsar`, which fits. Fixed by giving labels a
   guaranteed share (`columns`), left-truncating them so the identifying tail survives,
   and holding every row to its width. Regression tests: `TestLabelSurvivesALongDirKey`,
   `TestRowsNeverExceedTheirWidth`.
3. **The box resized as content changed**, so filtering visibly redrew the border at a
   different width. The box is now pinned to the popup width.

After the fixes, at 80x20 with tasks seeded across all three scopes:

```
╭──────────────────────────────────────────────────────────────────────────────╮
│  tdo                                                                         │
│                                                                              │
│  ▸ · fix auth redirect  …8-b114-4afa-8794-06f48a8fdc60/scratchpad/xdg/repo)  │
│    · write migration                                                         │
│    ◉ call the dentist   (global)                                             │
│                                                                              │
│  1/2/3 filter · j/k move · q quit · vb05f98e-dirty                           │
╰──────────────────────────────────────────────────────────────────────────────╯
```

Live key checks in that pane:

- `3` → `filter: global` in the title, one row; `3` again → merged list back.
- `1` (no session scope active) → `no session tasks — press 1 again for all scopes`,
  the filtered empty state, distinct from the unfiltered `no tasks yet`.
- `j`/`k` move the `▸` marker and clamp at the last row (`j` at the end is inert).
- `x` does nothing and does not quit; `q` exits and the pane closes.
- Strike-through is real in the terminal, not just in the style object — raw escapes on
  the completed row (`capture-pane -pe`), where `SGR 9` is strikethrough:

```
· ^[[2;9mwrite^[[0;9m ^[[2mmigration^[[0m
```

### Static binary (DoD 18)

```
$ CGO_ENABLED=0 make build
$ otool -L bin/tdo
bin/tdo:
	/usr/lib/libSystem.B.dylib
	/usr/lib/libresolv.9.dylib
```

No `libsqlite3` — the pure-Go driver claim still holds.

### Cold start (~100ms budget)

| path | measured |
|---|---|
| `tdo version` — bare process start, 30 runs | 8.42 ms/run |
| `tdo doctor --db` — process + `store.Open` + migrate check + 2 counts, 30 runs | 8.49 ms/run |
| popup first frame (`New` + `Init`'s query + `View`) over **200** tasks, 200 runs | 1.913 ms/run |

Roughly **10ms** to a drawn first frame, against a 100ms budget. Adding `bubbles` and real
querying cost nothing measurable. The first-frame figure came from a temporary probe, not a
committed test — a wall-clock assertion would be flaky in CI.

### Not verified

- **Ambiguous-width terminals.** `⌘` (U+2318) and `◉` (U+25C9) are East-Asian *ambiguous*
  width; under `ambiguous-width=double` they take two cells and the columns shift. The
  capture above ran in a normal-width terminal, so this remains the accepted risk the brief
  recorded, now with a `capture-pane` harness available to check it whenever
  `tmux-integration-and-rename-hook` wants to.
- **The `display-popup` overlay itself**, which needs an attached client (CLAUDE.md). The
  checks above run the TUI in a plain tmux pane instead.

## Blocking finding for another task — `internal/scope`

Not this task's code, and deliberately **not fixed here**: `internal/scope` is the
`scope-resolution` task, currently sitting in `review`, and editing code mid-audit would
muddy that task's Checkpoint 2 diff. Flagging it for the curator to fold into that review.

**Session scope never resolves in the shipped binary.** `scope.Resolve()` — the production
entry point — is `Resolver{}.Resolve()`, a zero value, so `TmuxEnv` is `""`; `queryTmux`
treats empty `TmuxEnv` as "not inside tmux" and returns before ever running
`tmux display-message`. The doc comment on `Resolver` says "Its zero value talks to the real
one", which is what the code does not do. Only the *tests* populate the field, from
`os.Getenv("TMUX")` (`scope_test.go:230,278`) — so the suite is green while production is
broken.

Probed from inside a real tmux session:

```
TMUX="/private/tmp/tmux-501/default,37572,54"
Resolve().Session                   = <nil>
Active()                            = [{dir /Users/agusarias/workspace/todo} {global }]
Resolver{TmuxEnv: $TMUX}.Session    = &{session tdoprobe}
```

Consequence for this task: the popup renders the session tier correctly (headless tests
cover it), but on a real machine no session task can ever appear, because `Active()` never
returns a session scope. The one-line fix belongs in `internal/scope`:

```go
func Resolve() (Resolved, error) { return Resolver{TmuxEnv: os.Getenv("TMUX")}.Resolve() }
```

That also wants a test which fails against the zero value — the present suite cannot catch
this class of bug, since every case supplies `TmuxEnv` by hand.

