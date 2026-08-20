# All-tasks View With Sesh Jump

**Status:** agreed
**Worktree:** none

## Goal
`g` toggles a wide view of every task in the database grouped by scope — including scopes
that are not currently active — with session groups labelled live or not running. `Enter` on
a session-scoped row switches to that session and closes the popup. Stale session groups can
be re-homed (`r`) or deleted as a group.

**Design:** docs/design.md — "All-tasks view"

## Why
This is the only view that can see a task the user has stranded. Every other surface —
merged popup, `tdo list`, the filters — is scoped to the *current* context by construction,
so a task filed under a session that has since been renamed or killed is invisible and
unrecoverable everywhere else. `design.md:41-43` makes the rename hook explicitly
best-effort *because* this view is the backstop; without it, "best-effort" means "sometimes
loses your list".

The jump is the other half: it turns the task list into navigation. Seeing `fix flaky test`
under a dead `api` session and pressing `Enter` to be back in that session is the feature the
product is actually for, and it is the reason the popup is allowed to close at all.

## Constraints
- **Sequencing: this task executes *after* `task-create-edit-rescope`.** It builds on that
  task's `viewKind`/`mode` dispatch, its delete queue and `u`, its scope-availability cycle,
  and its `?` overlay. Executing first would mean building all of that twice and then
  reconciling. The status is deliberately held at `agreed` until that task reaches `done` —
  see the gate note under Decisions.
- `internal/tui` stays **environment-blind**: no tmux calls, no `sesh`, no `exec`, no clock,
  no filesystem. Liveness arrives in `Config`; the jump leaves as an *intent* that
  `internal/cli` executes.
- `internal/store` needs **no change and no migration**. `ListGrouped`/`Group` already exist
  for exactly this view, and re-home is `Rescope` per task.
- `chromeHeight` stays 6 and every assembled line is truncated to `contentWidth` before
  styling. Minimum `contentWidth` is **52** (`design.md:49,57`: the 60×15 floor gives the TUI
  a 58-wide pane, less `chromeWidth` 6).
- The popup **stays open** for every action here except the jump, which is the one action
  `design.md:51` allows to close it.
- `sesh` is an **optional enhancement, never a hard dependency**. `tdo` must work fully on a
  machine without it.
- Out of scope: the rename hook and the `sessions` table (`tmux-integration-and-rename-hook`
  owns those), fuzzy search over the wide list, a done/history view, and any `--json` change.
  A `tdo` subcommand mirroring the jump is also out of scope: `tdo list --scope=all` already
  exposes the data, and the jump only means something from inside tmux.

## Critical surface
1. **The jump shells out to tmux and `sesh`, with a session name as an argument.** A session
   name can contain spaces, quotes and shell metacharacters. Every invocation must pass the
   name as a distinct `exec` argument, never interpolated into a shell string — the
   in-progress `tmux-integration` task already found the live version of this class of bug
   (`$0` expanding through `run-shell`), which is proof the class is real here.
2. **Group delete is a multi-task destructive action** — one keypress against a group that
   may hold a dozen tasks. It goes through `task-create-edit-rescope`'s delete queue so `u`
   restores the whole group and nothing is written until the popup closes.
3. **Re-home is a bulk `Rescope`** across every task in a group. It is not destructive (no
   row is lost) but it is bulk and not covered by the delete queue's undo — re-homing again
   is the only way back, which the brief accepts and states rather than discovering.
4. **`Enter` closes the popup.** Any queued deletes must commit *before* the jump command
   runs, or a jump silently discards them.

No migrations, no auth, no network beyond the local tmux server.

## Definition of done

**The view (`g`)**
1. `g` toggles between the merged view and the all-tasks view; `g` again returns. The cursor
   position in each view is remembered independently for the life of the popup.
2. The all-tasks view lists **every** scope in the database via `ListGrouped` with a nil
   `Filter.Scopes`, groups in tier order (session, dir, global), newest first within a group.
   Scopes with no tasks are absent, not empty-headed. Includes scope keys that are not
   currently active — the whole point of the view.
3. It is **wide**: rows carry no scope column, because the group header carries the scope.
   That is the entire meaning of "wide" here — `display-popup` cannot resize once open, so
   nothing about the frame's dimensions changes.
4. Session group headers are labelled `(live)` or `(not running)` from
   `Config.LiveSessions`, which `internal/cli` resolves once before the popup opens. A
   session with tasks that is not in that set is `(not running)`. `internal/tui` never asks
   tmux anything — pinned by a test that drives the view off `Config` alone.
5. **The jump affordance lives in the group header, once** — not on every row. The header
   shows what `Enter` will do (`↵ switch` when live, `↵ sesh` when not), right-aligned, and
   is truncated to `contentWidth` like every other line. Rows keep their full width for task
   text.
6. Group headers are **not selectable**: `j`/`k` moves between task rows and skips headers,
   and the cursor can never land on one. Pinned by a test that walks the full list top to
   bottom and asserts every stop is a task.
7. Completed-row visibility follows the merged view's existing rule (`openedAt` / 24h),
   reusing `doneSince()` unchanged.

**The jump (`Enter`)**
8. `Enter` on a session-scoped row in a **live** session runs `tmux switch-client -t <name>`.
   Proven to work from inside a `display-popup -E` command — see Decisions.
9. `Enter` on a session-scoped row in a **non-running** session runs `sesh connect -s <name>`
   (the `-s`/`--switch` flag, *not* bare `connect`, because the client is already attached);
   if `sesh` is absent or exits non-zero, it falls back to `tmux new-session -d -s <name>`
   followed by `tmux switch-client -t <name>`. The fallback is pinned by a test with an
   injected runner that fails the `sesh` call.
10. `Enter` on a **dir** or **global** row is a no-op — no jump, no close, no error. There is
    nothing to switch to.
11. The jump leaves `internal/tui` as a returned **intent**, not a subprocess: `Run` returns
    it and `internal/cli` executes it after the Bubble Tea program exits. `internal/tui`
    imports no `exec`.
12. **Queued deletes commit before the jump command runs.** Pinned by a test: queue a delete,
    jump, and assert the row is gone from the database.
13. Session names are passed as separate `exec` arguments. A test uses a name containing a
    space, a single quote and a `$` and asserts the argv the runner received, so the name is
    never re-parsed by a shell.

**Re-home (`r`) and group delete**
14. `r` on a session group cycles the whole group to another scope through the **currently
    active** scopes (the same availability cycle `Tab` uses) and applies it as a bulk
    `Rescope`. It works on live groups too — a stale group is the motivating case, not the
    only legal one. It does not touch the sticky default.
15. Group delete pushes **every task in the group** onto the delete queue; one `u` restores
    the whole group with every id and position intact, since nothing was written. Nothing
    reaches the database until the popup closes or a jump commits.
16. A group whose every task is queued for deletion disappears entirely, header included,
    rather than leaving an empty header behind.

**Keys**
17. Every merged-view key works in the all-tasks view: `space`, `a`, `e`, `s`, `d`, `u`, `?`,
    `1`/`2`/`3`, `q`. Plus `Enter` (jump), `r` (re-home) and `g` (back).
18. `a` files into **the group under the cursor** — the input row opens under that header and
    `Tab` is inert, because the group *is* the scope choice. It may target a non-running
    session, which is legitimate: queueing work for a session you will recreate. It does
    **not** change the sticky default — this is a placement, not a preference.
19. `?` lists the all-tasks view's keys when that view is on screen, so the overlay describes
    the view you are actually in.

**Design and sweep**
20. `design.md:107-120` updated: the jump affordance moves to the group header, `sesh connect`
    becomes `sesh connect -s` with its fallback, and re-home's target is specified.
21. `make test`, `make lint` clean, `gofmt -l .` empty, `CGO_ENABLED=0 make build` still
    static (no libsqlite3 in `otool -L`).
22. `capture-pane` evidence from a real tmux pane showing the all-tasks view with a live
    group, a not-running group, a dir group and a global group, per CLAUDE.md's recipe.
23. A real end-to-end jump in a private tmux server (`tmux -L`): a live switch and a
    not-running connect, each with the client's session asserted before and after.

## Verification
- `go test ./internal/tui/ -v` naming: the headers-are-not-selectable walk, the
  liveness-from-Config test, the dir/global-Enter-is-a-no-op test, the sesh-fallback test
  with an injected failing runner, the argv test for a name with shell metacharacters, and
  the deletes-commit-before-jump test.
- `TestFrameNeverExceedsThePane` extended to the all-tasks view, asserted on the unclamped
  `frame()` with the `clampHeight` backstop proven not to have fired.
- `TestRowsNeverExceedTheirWidth` extended to group headers, including a header whose session
  name is long enough to collide with the right-aligned jump hint.
- **A real-tmux jump transcript** on a private socket: `list-clients -F '#{client_session}'`
  before and after, for both the live and the not-running case. The switch-client mechanism
  is already proven (Decisions); this proves *`tdo`* does it.
- A `sesh`-absent run (PATH without it) showing the tmux fallback taking over.
- `capture-pane -pe` for the ANSI escapes, since lipgloss renders plain in a test process.

## Decisions

### 2026-08-20 (curator grill) — two probes and six rulings

Explored `design.md:102-120`, `internal/tui/tui.go`, `store.ListGrouped`/`Group`, and the
in-progress `tmux-integration-and-rename-hook` brief (for the `sessions` table and what
"stale" means). Two mechanisms in the draft were unproven, so both were probed on a private
tmux server rather than grilled on a guess.

**PROBE 1 — `switch-client` from inside `display-popup -E` works.** This was the design's
biggest unvalidated assumption and it holds. On tmux 3.7b, with a real client attached
(obtained headlessly by attaching from inside another pane's pty — `TMUX=` is required or
tmux refuses to nest), a popup command running `tmux switch-client -t beta` moved the client
**immediately**, while the popup command was still running, and the client stayed on `beta`
after the command exited and the popup closed. So no deferred-execution gymnastics are
needed: the jump can run before `tdo` exits. Recorded because a future session would
otherwise re-derive it, and getting an attached client headlessly is the fiddly part.
*(Also learned: `display-popup` needs `-c <client>`; `-t <session>` fails with
"no current client".)*

**PROBE 2 — the design's `sesh connect` invocation is wrong for this context.** `sesh connect
--help` documents `-s`/`--switch`: "Switch the session (rather than attach). This is useful
for actions triggered outside the terminal." Bare `connect` *attaches*, which is wrong when
the client is already attached — which it always is, inside a popup. **And `sesh` may not
know the name at all**: `sesh list` returns a blend of live tmux sessions, zoxide
directories and config entries, while a stale tdo scope key is a *dead tmux session name*
that need not appear in any of them. The design specified no fallback for the exact case the
feature exists to serve.

1. **Jump = `switch-client` when live; `sesh connect -s` then a tmux fallback when not.** If
   `sesh` is absent or non-zero, `tmux new-session -d -s <name>` + `switch-client`. sesh
   stays an optional enhancement that restores directory and startup command when it can,
   and `tdo` works fully without it — the right shape for a tmux plugin whose users may not
   have a third-party tool installed.
2. **Liveness is injected as `Config.LiveSessions`, resolved once by `internal/cli`** before
   the popup opens, in the same spirit as `Scopes` and `Now`. Accepted staleness: a session
   killed while the popup is open still reads `(live)`, and `Enter` then falls through to the
   create-and-switch path anyway — so the wrong label costs nothing. A per-keypress
   `LiveSessions func()` was rejected: it puts a ~5ms subprocess on a keypress inside the
   popup and makes rendering depend on a call that can fail.
3. **Group delete goes through `task-create-edit-rescope`'s delete queue.** One `u` restores
   the whole group with ids and positions intact, and there is exactly one destructive path
   in the product rather than a queued one and an immediate one. This is what creates the
   sequencing constraint.
4. **Every merged-view key stays live in the all-tasks view** (user's call, against the
   curator's narrower recommendation). The two views then differ only in layout, which is one
   less rule to learn. The `a`-target problem this creates is resolved in 6.
5. **The jump affordance moves to the group header.** Measured: `↵ switch there` is 14
   columns and `↵ sesh connect <name>` 18–35, against a minimum `contentWidth` of **52** —
   26% to 67% of every session row, repeated to say something identical for every row in the
   group. The header already carries the scope and the liveness label, so it is where "what
   Enter does" belongs. Task text gets the full width.
6. **`a` files into the group under the cursor**, with `Tab` inert — the group *is* the scope
   choice, so offering a second one would be two answers to the same question. It may target
   a non-running session (queueing work for a session you will recreate: legitimate, and
   arguably the point of the view). It does **not** set the sticky default, consistent with
   `task-create-edit-rescope`'s ruling that only the merged view's add flow does.

**CURATOR'S CALL, not asked — flagged at Checkpoint 1.** `design.md:120` gives stale groups
"re-home" but never says re-home *to what*. Ruled: `r` cycles the group through the
**currently active** scopes — the same availability cycle `Tab` uses — and bulk-`Rescope`s
it. Rationale: re-home exists to make stranded tasks visible again, and "visible again" means
a scope the user is actually in; cycling through every dir key in the database would be a long
unordered list. Not restricted to stale groups, because there is no reason to forbid it on a
live one. Override cheaply if this is wrong.

**Confirmed while exploring:** `store.ListGrouped` and `store.Group` already exist with the
exact shape this view needs ("groups in tier order; scopes with no matching tasks are absent
rather than empty"), so the store needs no change. No dependency on the in-progress
`tmux-integration` task: liveness comes from `tmux list-sessions`, not the `sessions` table,
and re-home uses per-task `Rescope` rather than its `RenameSession`.

**GATE — why this brief is `agreed` and not `ready`.** It depends on
`task-create-edit-rescope`'s modes, delete queue, `u`, availability cycle and `?` overlay.
Both briefs share the `2026-08-19` date prefix and the executor claims the oldest `ready`
task, which would sort `all-tasks-view…` **first** alphabetically — exactly the wrong order.
Holding this at `agreed` is the curator's lever to prevent that.

**Gate status:** `task-create-edit-rescope` reached `review` on 2026-08-20 (merge `dc5e73c`)
while this brief was being planned, so the dependency is built and merged into local `main`.
Flip this brief to `ready` as soon as that Checkpoint 2 is approved. The plan below was
written against the merged code, not against the plan for it.

## Plan
`internal/tui` plus a new jump executor in `internal/cli`. No `internal/store` change, no
migration, no new module dependency.

**The one structural change: a shared row model.** The merged view's cursor is an index into
`[]task.Task`. The all-tasks view's list is *heterogeneous* — group headers interleaved with
task rows, headers not selectable — so that model does not stretch. Both views become:

```go
type rowKind int
const (rowTask rowKind = iota; rowHeader; rowInput)

type row struct {
    kind  rowKind
    task  task.Task    // rowTask
    group store.Group  // rowHeader: scope + liveness
}
```

`m.rows []row` with the cursor indexing into it and `moveCursor` skipping non-selectable
kinds. The merged view is then the degenerate case where every row is a `rowTask`, and
`anchorCursor`, the viewport scroll math and the input row all have exactly one
implementation instead of two. This is the riskiest part of the task precisely because it
edits code that already works, so it goes first and its guard is stated below.

**The jump leaves as a value, not a subprocess.** `Run` grows a result:

```go
// Jump is what internal/cli should do after the popup closes.
// The zero value means "no jump".
type Jump struct {
    Session string
    Live    bool
}
func Run(cfg Config) (Jump, error)
```

`internal/tui` still imports no `os/exec`. `internal/cli` owns the invocation table, behind an
injected runner so every branch is testable without tmux or sesh:

| context | live | command |
|---|---|---|
| inside tmux (`$TMUX` set) | yes | `tmux switch-client -t <name>` |
| inside tmux | no | `sesh connect -s <name>`, else `tmux new-session -d -s <name>` + `switch-client -t <name>` |
| outside tmux | either | `tmux attach -t <name>`, creating it first if absent |

The outside-tmux row is a `how` detail the grill did not cover: `switch-client` and
`sesh connect -s` both need an attached client, and `tdo tui` can be run from a plain shell.
`internal/cli` already knows whether it is inside tmux (it holds the `scope.Resolver`), so it
picks attach-vs-switch and `internal/tui` stays ignorant of the distinction.

**Files.**
- `internal/tui/rows.go` — new. The `row` model, the flatten functions for both views, and
  cursor movement over selectable rows.
- `internal/tui/alltasks.go` — new. `g`, the `ListGrouped` query, group headers with the
  liveness label and the right-aligned jump hint, `r`, and group delete.
- `internal/tui/tui.go` — `viewKind` dispatch (the seam is already there, with a comment naming this task),
  `Config.LiveSessions`, per-view cursor memory, `Run`'s new signature.
- `internal/tui/render.go` — `renderHeader`, and a width budget for the header that guarantees
  the jump hint its columns the way `columns()` already does for tier labels.
- `internal/cli/jump.go` — new. The invocation table above, the injected runner, and the
  `sesh`-absent fallback. This is where `os/exec` lives.
- `internal/cli/cli.go` — resolve `LiveSessions` once via `tmux list-sessions`, pass it in,
  and act on the returned `Jump`.

**Sequencing.**
1. **The row-model refactor, with the merged view's existing tests as the guard.** Land
   `[]row` and make every current `internal/tui` test pass **without editing a single one of
   them**. If a merged-view test needs changing, the refactor changed behaviour and is wrong.
   That constraint is the whole safety argument for touching working code.
2. **`g` and the all-tasks view, read-only**: `ListGrouped` with nil `Scopes` and the merged
   view's `DoneSince`, headers, liveness from `Config`, non-selectable headers, the header
   jump hint, and the width tests. No mutation, no jump — the view is provable on its own.
3. **The jump.** `Run`'s `Jump` result, `internal/cli/jump.go` and its table, the argv test
   for metacharacters, the sesh-failure fallback, and deletes-commit-before-jump.
4. **`r` re-home and group delete**: bulk `Rescope`, the group push onto the delete queue,
   and the empty-group-disappears case.
5. **`a` into the cursor's group** and the per-view `?` overlay.
6. `design.md:107-120`, `capture-pane` evidence, the real-tmux jump on a private socket
   (`tmux -L`, reusing the probe harness recorded in Decisions), then the full sweep.

**What could go wrong.**
- *The row refactor silently regresses the merged view.* The mitigation is the rule in step 1:
  existing tests pass unedited. If that rule has to be broken, stop and treat it as a scope
  event rather than "fixing" the test.
- *A long session name collides with the right-aligned header hint.* This is precisely the
  class that made tier labels vanish for real dir keys — "a row wider than the viewport is
  silently clipped, not wrapped" (CLAUDE.md). The header needs a real width budget, and the
  hint must win its columns while the *name* left-truncates, because a session name's tail is
  what identifies it. Extend `TestRowsNeverExceedTheirWidth` to headers.
- *A group whose every task is queued for deletion leaves an empty header behind.* Filter the
  queued ids out of each group's `Tasks` **and then drop groups that came out empty** — two
  steps, and skipping the second is the plausible bug (DoD 16).
- *`ListGrouped` with a nil `Scopes` also returns done rows.* It must be passed the same
  `DoneSince` the merged view computes, or the wide view becomes a graveyard while the
  merged view stays clean — and the inconsistency would look like a store bug.
- *`Run`'s signature churns twice.* `task-create-edit-rescope` has just made it return a
  commit error; this makes it `(Jump, error)`. Accepted: one caller, and the alternative is
  smuggling the jump out through a pointer field.
- *A detached-but-running session reads `(live)` and `switch-client` then has no client.*
  Covered by the invocation table's outside-tmux row; called out because `tmux list-sessions`
  reporting a session says nothing about whether *this* process has a client.

## Evidence
(Added by the executor.)
