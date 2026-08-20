# Task Create, Edit And Re-scope

**Status:** in-progress
**Worktree:** ../todo-task-create-edit-rescope

## Goal
Mutation UX inside the popup: `a` opens an inline input row with `Tab` cycling the scope
before submit, `Enter` saves and makes that scope the sticky default; `e` edits an existing
task's text in the same row; `s` cycles an existing task's scope in place; `d` deletes with
`u` undoing it for the life of the popup; `?` toggles a keymap overlay because the footer
cannot hold these keys.

**Design:** docs/design.md — "Creating and editing"

## Why
This is the popup's whole reason to exist. Today `tdo tui` is read-plus-toggle: everything
that *creates* a task still requires dropping to the shell, which defeats a popup bound to a
keystroke. It is also the task that turns `internal/tui`'s two prepared seams — `mode`
(`tui.go:97`) and the input row they were added for — into working code, so it either
validates that structure or exposes it as wrong while there is still only one view to fix.

Second reason: it is the first task to *write* the sticky default. Until now `scope`'s
`SetStickyDefault` has had no caller and the CLI deliberately only reads it, so the
round trip that makes the default "sticky" has never actually run.

## Constraints
- `internal/tui` and `internal/cli` only. **No `internal/store` change and no migration** —
  every primitive needed already exists (`Add`, `UpdateText`, `Rescope`, `Delete`,
  `SetStickyDefault`). If one turns out to be missing, that is a scope event.
- `internal/tui` stays **environment-blind**: it must not import `internal/scope`, read the
  clock, touch the filesystem, or ask tmux anything. Everything environmental arrives in
  `Config`, as `DB`, `Scopes`, `Home` and `Now` already do.
- **`chromeHeight` stays 6.** Every new piece of UI — the input row, the help overlay, the
  delete hint — renders *inside* the viewport or *replaces* the body, never as an extra
  chrome line. Three frame bugs have shipped in this arithmetic (CLAUDE.md).
- Every line assembled must be truncated to `contentWidth` before styling. lipgloss wraps
  rather than clips, so one over-wide line silently becomes two rows.
- No new module dependency. `bubbles/textinput` is already in the module cache via the
  existing `bubbles v1.0.0` require.
- The popup **stays open** across every action here. The only thing that closes it is
  `q`/`esc`/`ctrl+c` (and, in a later task, jumping to a session).
- Out of scope: the all-tasks view and `g` (its own task), re-home of stale session groups,
  a general undo stack beyond the delete queue below, multi-select or batch mutation,
  mouse support, and any change to the `--json` contract.

## Critical surface
**`d` + `u` is a deferred destructive write, and it is the one thing here that can lose a
user's data.** A queued delete is committed at popup close, so the failure modes must be
deliberate rather than discovered:

- A delete that never commits (popup SIGKILLed, pane closed, machine sleeps) leaves the task
  alive. That is the **safe** direction and is accepted, not fixed.
- A commit that *fails* must not be silent: `Run` returns the error, `internal/cli` prints it
  and exits non-zero. Reporting "deleted" for a row that is still in the database is worse
  than the delete not happening.
- A queued row must be unreachable while queued — filtered out of the rows on **every**
  reload, not just hidden once — or `space`/`e`/`s` could act on a task that is about to be
  deleted.

`SetSticky` writes the XDG *state* dir, which is a real filesystem write outside the
database. It is not critical (a corrupt or missing sticky file already falls back silently by
design) but it is the second write path in this task and gets its own tests.

No migrations, no auth, no network, no external side effects.

## Definition of done

**Add (`a`)**
1. `a` opens an inline input row at the top of the list showing the scope glyph for the
   currently selected scope, seeded from `scope.StickyDefault` as the CLI's `add` already is.
   The row is *inside the viewport* — `chromeHeight` is unchanged and
   `TestFrameNeverExceedsThePane` still passes in this mode.
2. `Tab` cycles the scope **only through scopes present in `Config.Scopes`**, in tier order,
   wrapping. Outside tmux that is dir → global; with no git repo, session → global; with
   only global, `Tab` is a no-op. An unavailable scope is never offered, so `Enter` can never
   fail on scope grounds. Pinned by a test per case, driven off `Config.Scopes` alone.
3. `Enter` with non-empty text trims surrounding whitespace, inserts via `store.Add`, closes
   the input row, reloads, and leaves the cursor **on the new task**. It then calls
   `Config.SetSticky` with the chosen kind.
4. `Enter` with empty or whitespace-only text **cancels**: the row closes and nothing is
   created. `Esc` cancels from any input state. Neither calls `SetSticky`.
5. A `SetSticky` error does **not** fail the add: the task is already saved, so the popup
   shows the task and swallows the sticky failure the same way `scope` treats a corrupt
   sticky file. Pinned by a test with an injected failing `SetSticky`.

**Edit (`e`)**
6. `e` on a row replaces that row in place with the input row, pre-filled with the task's
   current text and cursor at the end. `Enter` saves via `store.UpdateText` and re-anchors
   the cursor to that task id.
7. `Enter` on empty/whitespace-only text in **edit** mode is **rejected**: the input row
   stays open with a hint, and the task is not blanked. `Esc` abandons the edit, leaving the
   text unchanged.
8. `Tab` is inert in edit mode — scope changes go through `s`, so `e` is purely textual.

**Re-scope (`s`)**
9. `s` on a row cycles its scope through the same available-only cycle as `Tab` and applies
   it immediately via `store.Rescope`, re-anchoring to that task id. It does **not** touch
   the sticky default.
10. With a tier filter active (`1`/`2`/`3`), re-scoping a task out of the filtered tier makes
    the row leave the view; the cursor clamps rather than following it. Pinned by a test, so
    the behaviour is a decision rather than an accident.

**Delete (`d`) and undo (`u`)**
11. `d` on a row removes it from the view and pushes it onto a delete queue. **No `DELETE`
    reaches the database yet.** The row's id, text, scope, done state, timestamp and list
    position are untouched, because nothing was written.
12. `u` pops the most recent queued delete (LIFO) and the row returns **at its original
    position with its original id** — asserted explicitly, since preserving the id is the
    entire reason this mechanism was chosen over re-insert. Repeated `u` unwinds repeated
    `d`. `u` with an empty queue is a no-op.
13. Queued ids are filtered out of the rows on **every** reload, so a queued task cannot be
    reached by the cursor or acted on by `space`, `e` or `s`, and does not reappear when
    another pane's insert triggers a re-query.
14. On `q`/`esc`/`ctrl+c` the queued deletes commit via `store.Delete` **before** the program
    exits, and `Run` returns any commit error. An empty queue exits exactly as it does today.
15. A popup that dies without a clean exit commits nothing and the tasks survive — verified
    by a test that drops the model without running the quit path and asserts the rows are
    still in the database.

**Help (`?`) and the footer**
16. `?` toggles a keymap overlay that replaces the list body (like the error and loading
    states already do), listing every key this build binds with its version. It is at most
    `listHeight` rows, each truncated to `contentWidth`. `?`, `esc` or `q` dismisses it;
    while it is up, mutation keys are inert.
17. The footer stays **one row** and short enough to survive at `contentWidth` 42 — the
    ~60%×60% popup on an 80-column terminal (`design.md:47`). It advertises `? keys` rather
    than enumerating the keymap. A test asserts the untruncated footer fits 42 columns with
    a `Version` as long as a `-dirty` `git describe`.
18. `design.md`'s footer mock (`design.md:62`) is updated to match what ships, so the design
    stops specifying a line that cannot fit its own geometry.

**Mode discipline**
19. In input mode, `q`, `j`, `k`, `d`, `space`, `u`, `?` and `1`/`2`/`3` are **literal text**,
    not commands — pinned by a test that types `q` into the input row and asserts the popup
    did not quit. This is what the `mode` seam exists for.

**Sweep**
20. `make test`, `make lint` clean, `gofmt -l .` empty, `CGO_ENABLED=0 make build` still
    static (no libsqlite3 in `otool -L`).
21. A `capture-pane` check in a real tmux pane, per CLAUDE.md's recipe (`$XDG_DATA_HOME` at a
    temp dir, schema via `tdo doctor --db`, rows seeded with the `sqlite3` CLI), showing the
    input row, the help overlay and a struck-through completed row as they actually render.
    This is the only check that catches whole-frame bugs and it has caught three.

## Verification
- `go test ./internal/tui/ -v` in Evidence, naming the Tab-cycle-availability tests, the
  id-and-position-preserving undo test, the queued-row-unreachable test, the
  commit-on-exit test, the die-without-commit test, the literal-`q`-in-input test, and the
  footer-fits-42-columns test.
- `TestFrameNeverExceedsThePane` extended to cover input mode and help mode, asserted on the
  unclamped `frame()` with the `clampHeight` backstop proven not to have fired — per
  CLAUDE.md, an assertion over `View()` would absorb a `chromeHeight` miscount and stay green.
- **A mutation proof for the deferred delete**: with the commit-on-exit call deleted, the
  commit test must fail. Record the failure output. DoD 11–15 are otherwise a set of
  assertions that a no-op implementation could satisfy, since "nothing was written" is the
  normal state.
- `capture-pane -pe` output showing the input row and help overlay, plus the ANSI escapes,
  since lipgloss renders to plain text in a test process and asserting on rendered output
  would pass whatever style was chosen.
- An end-to-end pane transcript: add a task in each available scope via `a`+`Tab`, edit one,
  re-scope one, delete two and undo one, quit, then `tdo list --json` from the shell showing
  exactly the expected final state — including that the undone task kept its original id.

## Decisions

### 2026-08-20 (curator grill) — seven decisions, all folded above

The grill was run after reading `design.md:45-90`, all 545 lines of `internal/tui/tui.go`,
`render.go`'s function list, and the `store`/`scope` public APIs.

**Lead finding: the design's footer does not fit the design's geometry.** `design.md:62`
specifies an 83-column footer; with the `j/k move` and `v<version>` this build carries it is
~93. `design.md:47`'s ~60%×60% popup on an 80-column terminal is 48 columns wide, so
`contentWidth` is **42**. The truncation machinery (already correct, already pinned) would
have silently reduced the footer to `1/2/3 filter · j/k move · a add · e edit · s` — hiding
every key this task adds except two, with no visible failure. A 120-column terminal
(`contentWidth` 66) still loses the tail. **Resolved:** a `?` keymap overlay, with the footer
kept to one short row advertising `? keys`. The overlay replaces the body rather than adding
a row, so `chromeHeight` stays 6, and it scales to the all-tasks view's keys later.
`design.md`'s mock is corrected as part of this task (DoD 18) rather than left contradicting
the build.

1. **`SetSticky func(task.ScopeKind) error` is injected into `Config`** rather than passing a
   `scope.Resolver` in or returning the choice from `Run`. `internal/tui` must not import
   `internal/scope`, and this follows the precedent set when `Now` was injected for the same
   reason. Returning the choice for `internal/cli` to persist on exit was rejected: a
   SIGKILLed popup would silently stop the default being sticky at all.
2. **`Tab` cycles only scopes present in `Config.Scopes`.** "Absent beats empty" is already
   the repo's rule and the CLI already exits 1 on an unavailable scope; offering a scope the
   user cannot pick, or one that fails on submit, contradicts both. The cost is that session
   scope is invisible outside tmux — accepted, since the `?` overlay is where that gets
   explained.
3. **`d` queues the delete and commits at popup close; `u` is an in-memory cancel.** The
   design calls `space` "the only undo the product has", which makes an irreversible `d` one
   keystroke from `s` and `space` uncomfortable. Re-insert via `store.Add` was rejected on a
   concrete defect: `Add` takes no id and no `created_at`, so an undone task would come back
   with a **new id** — breaking any shell holding it — and a **new timestamp**, so it would
   reappear at the top of its tier instead of where it was. Deferring the write preserves
   both for free and needs no store change. A soft-delete column was rejected as scope
   expansion into a migration.
4. **The accepted cost of deferral is stated, not hidden:** a queued row stays visible to a
   concurrent `tdo list` until the popup closes, and an unclean death commits nothing. Both
   fail in the direction of keeping the user's data.
5. **Empty `Enter` cancels on add, is rejected on edit.** On add nothing exists yet, so
   closing the row loses nothing and "I changed my mind" is the common case. On edit the task
   is on screen and blanking it silently would destroy content the user can see. Text is
   trimmed either way.
6. **`s` does not set the sticky default.** `design.md:70` ties stickiness to the add flow.
   Re-scoping an existing task is a correction to that task, not a statement about the next
   one; making it sticky means tidying one old row silently redirects the next add.
7. **The input row lives inside the viewport** — at the top of the list for `a`, in place of
   the edited row for `e` — so `chromeHeight` stays 6. "Inline input row" in `design.md:68`
   is read as settling this. Taken as a `how` decision, recorded because the frame arithmetic
   is where three bugs have already shipped and a 7th chrome row would have been the
   plausible-looking way to build it.

**Confirmed while exploring, so the plan does not have to discover it:** every store
primitive this task needs already exists, `bubbles/textinput` is already in the module cache
under the existing `bubbles v1.0.0` require (no `go.mod` change), and `tui.go:97`'s `mode`
seam plus `render.go`'s pure `glyph`/`scopeLabel`/`columns`/`renderRow` helpers are directly
reusable for the input row.

## Plan
**Approved at Checkpoint 1 on 2026-08-20** as written below, including DoD 18's correction to
`design.md:62`. The plan is disposable: revise the *how* freely and log why in Decisions. The
Goal, Constraints and Definition of done are not — changing those is a scope event, so set
status `blocked` with the question instead.

All work in `internal/tui`, plus ~8 lines of wiring in `internal/cli/cli.go`. No new package,
no `internal/store` change, no `go.mod` change.

**State added to `Model`.**

```go
mode        mode            // modeNormal | modeInput | modeHelp  (seam already exists)
input       textinput.Model
inputKind   inputKind       // inputAdd | inputEdit
inputScope  task.ScopeKind  // the Tab-selected scope; add only
inputTarget int64           // task id under edit; edit only
inputHint   string          // "text cannot be empty" on a rejected edit

queued      []int64         // delete queue: LIFO stack *and* the filter set
commitErr   error           // set by the commit, read by Run
```

`queued` being ids and nothing else is the load-bearing simplification. Because the delete is
deferred, **the rows are still in the store**, so "restore at the original position with the
original id" needs no saved copy: `u` pops the id and issues `reloadAnchoredTo(id)`, and the
row comes back from the store in its natural place. The queue is filtered out in the
`rowsMsg` handler, which is the single point every reload passes through — that is what makes
DoD 13 ("filtered on *every* reload") structural rather than a thing to remember.

**Commit-on-exit, without doing I/O in `Update` and without an ordering race.**

`q`/`esc`/`ctrl+c` sets `m.quitting` and returns a commit command. The command runs the
deletes and returns `deletesCommittedMsg{err}`; `Update` stores the error and *then* returns
`tea.Quit`. So the quit happens in response to the commit finishing, ordered by construction
rather than by trusting `tea.Sequence` to deliver a message before `tea.Quit` takes effect.
`Run` captures the final model (currently discarded as `_`), type-asserts it and returns
`commitErr`. An empty queue makes the command a no-op that returns immediately, so the
exit path is unchanged for anyone who never pressed `d`.

**Files.**
- `internal/tui/tui.go` — the new state, `mode` dispatch (`updateNormal` / `updateInput` /
  `updateHelp`), the `rowsMsg` queue filter, the quit path, the shorter `footer()`, `Run`
  returning `commitErr`.
- `internal/tui/delete.go` — the queue, `u`, and the commit command. Own file because it is
  the critical surface, the same argument that put the CLI's `--json` contract in its own
  `json.go`.
- `internal/tui/input.go` — the input row: `a`/`e` entry, `Tab` cycling off `Config.Scopes`,
  submit and cancel, the empty-text rule, and `s` re-scope (it shares the availability cycle
  with `Tab`, so the cycle function lives here and has exactly one implementation).
- `internal/tui/render.go` — `helpLines()` and the input row's rendering, reusing the existing
  pure `glyph`, `scopeLabel`, `columns` and `renderRow`.
- `internal/cli/cli.go` — `Config.SetSticky`, and `runTUI` switching from `scope.Resolve()`
  to a `scope.NewResolver()` it keeps, since `SetStickyDefault` is a method on the resolver.
- Tests alongside; `lifecycle_test.go` gains the delete-queue legs, `render_test.go` the help
  and footer-width legs.

**`internal/cli` wiring — the one trap.** `runTUI` currently calls the package-level
`scope.Resolve()`. It needs the `Resolver` itself, and writing `scope.Resolver{}` to get one
reintroduces the tmux-blindness bug that got `scope-resolution` rejected and is pinned by
`scope_test.go`. It must be `scope.NewResolver()`, and `SetSticky` becomes
`resolver.SetStickyDefault` — a method value on that same resolver, so there is no second
construction site to get wrong.

**Sequencing.** Deliberately not in design order — the two riskiest pieces go first, while
the file is small enough to reason about.

1. **`mode` dispatch + `?` overlay + the short footer.** The smallest change that touches the
   frame, done first so `TestFrameNeverExceedsThePane` is extended for the new modes before
   there is anything else to blame. `Version` moves from the footer into the overlay — that
   is what gets the footer under 42 columns (39 without it; any `git describe` blows it).
2. **Delete queue, `u`, commit-on-exit, and the mutation proof.** Critical surface, no
   `textinput` dependency, so it lands as a self-contained slice.
3. **`a`**: `textinput`, `Tab` cycling, submit → `store.Add` + `SetSticky`, empty-cancels.
4. **`e`**: reuse the row, pre-fill, empty-rejects, `Tab` inert.
5. **`s`**: `store.Rescope` on the shared availability cycle; the filter-interaction test.
6. `design.md:62` footer mock corrected, `capture-pane` checks, full sweep.

**What could go wrong.**
- *`textinput` swallows the keys we need.* `Tab`, `Enter` and `Esc` must be intercepted in
  `updateInput` **before** the message reaches `textinput.Update`, or `Tab` inserts a tab
  character instead of cycling scope. First thing to check when step 3 misbehaves.
- *The mutation proof is the real test of DoD 11–15.* "Nothing was written" is also what a
  completely unimplemented delete looks like, so a passing queue test proves little on its
  own. Deleting the commit call must make the commit test fail — recorded in Evidence. This
  is the `completed-task-lifecycle` DoD-6 lesson: a green test whose subject is gone is
  evidence of nothing.
- *`d` on the last visible row shows the wrong empty state.* The list is empty but the queue
  is not, so "no tasks yet" is a lie and hides the fact that `u` would bring the row back.
  `emptyText()` gains that case — it already distinguishes "nothing here" from "your filter
  hid everything", so this is a third arm of an existing decision, not a new one.
- *`q` in help mode dismisses instead of quitting* (DoD 16). Defensible, and it means two
  presses of `q` always exits from anywhere, but it is the one keybinding here that could
  read as a bug. Called out so it is reviewed as a choice.
- *`chromeHeight` drifting to 7.* The input row and the overlay both stay inside the body
  precisely to avoid it. If either ever needs its own line, that is a frame-arithmetic change
  and CLAUDE.md's three shipped bugs say it gets its own careful pass, not a `+1`.

## Evidence
(Added by the executor.)
