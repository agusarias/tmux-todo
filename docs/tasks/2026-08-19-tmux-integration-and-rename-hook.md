# Tmux Integration And Rename Hook

**Status:** ready
**Worktree:** none

## Goal
Wire `tdo` into tmux so the popup is reachable and session-scoped tasks survive a session
rename:

- A popup keybind invoking `tmux display-popup -E`, centered, ~60%x60% but never narrower
  than the tested range (see Constraints), configurable via `@todo-key`.
- A `session-renamed` hook that rewrites the session scope key so tasks follow the rename.
  Because tmux does not tell the hook the *old* name, this requires a durable
  `session_id -> name` map (see Decisions) — a new migration.

The hook stays **best-effort**: stale session scopes must remain reachable from the
all-tasks view, which owns re-home and delete.

**Design:** docs/design.md — "Session renames and stale names", "Popup UX"

## Why
Nothing can open the popup today. `internal/tui` is finished and polished — the merged list,
the completion toggle, and a frame invariant across nine pane sizes — but no keybind exists,
so none of it has ever been used the way a user would use it. This task is what turns three
merged tasks into a product you can actually press a key to see.

The rename half matters because the session scope key *is* the session name. Renaming a
session silently orphans its list, and renaming is common (`sesh` and friends rename freely).
`design.md:39` already concedes the hook is best-effort, which is why the all-tasks view's
re-home exists — but best-effort should still work in the common case.

## Constraints
- **Popup sizing has a floor.** `design.md:47`'s ~60%x60% is 48x12 on a standard 80x20
  terminal. `popup-tui-merged-list` measured the footer's minimum at 58 columns (`dev`
  stamp) to 78 (`-dirty` describe), and its frame invariant is only asserted from 40 columns
  up. Use percentages with an absolute floor of ~60x15 so the popup stays inside the tested
  range and the footer keeps its version tail.
- **Do not pass `#{session_id}` through `run-shell`.** It expands to `$0`, and `run-shell`
  hands the string to `sh`, which expands `$0` to `sh`. Verified twice on tmux 3.7b,
  including with single quotes at the tmux level — the format is expanded before `sh` sees
  it. The hook passes the *new name*; the binary asks tmux for the id itself.
- **`set-hook -g` replaces an existing hook.** Use `-a` (append) so the plugin does not
  clobber a user's own `session-renamed` hook.
- **The store stays environment-blind.** `internal/store` must not learn what tmux is. The
  id->name map is a table it stores and a method it exposes; deciding *what* the current
  session id is belongs to `internal/scope`, and writing the row belongs to `internal/cli`.
- **`internal/scope` keeps its one-subprocess discipline.** `queryTmux` reads
  `#{session_name}` and `#{pane_current_path}` in a single `display-message`
  (`scope.go:107`); `#{session_id}` becomes a third line of the same format string, not a
  second call.
- Out of scope: the TPM plugin script, `@todo-key` *parsing* from tmux options, binary
  resolution, and the README — all `tpm-plugin-and-install`. This task owns the *behaviour*
  and the tmux invocations, proven by hand; that task owns *packaging* them.
- Out of scope: the all-tasks view's re-home and delete of stale session groups
  (`all-tasks-view-with-sesh-jump`), and anything about non-running session groups.

## Critical surface
**Yes — a migration.** `002_sessions.sql` adds the `session_id -> name` table. Per CLAUDE.md
the migration must be additive and never edited once applied, and the runner's concurrent
first-open window is the dangerous one. Also a **write on the popup's hot path**: recording
the current session's name on every invocation is a new write where there was only a read.

No auth, no prod data, no external side effects beyond the tmux commands this task installs.

## Definition of done
1. `002_sessions.sql` creates `sessions(session_id TEXT PRIMARY KEY, name TEXT NOT NULL,
   updated_at INTEGER NOT NULL)`. `store.SchemaVersion()` reports 2 and `tdo doctor` shows
   it. A test opens a v1 database and asserts it migrates to v2 without losing tasks.
2. `store.RecordSession(ctx, id, name string) error` upserts the mapping;
   `store.SessionName(ctx, id string) (string, error)` reads it. Both clock-blind — the
   `updated_at` stamp comes from `DB.now`, which tests freeze.
3. `store.RenameSession(ctx, oldKey, newKey string) (int, error)` bulk-rewrites
   `scope_kind='session' AND scope_key=oldKey` to `newKey` and reports rows affected. One
   statement, one transaction. A test proves pending and done rows both move and that
   `dir`/`global` rows are untouched.
4. **Rename onto an existing key merges, and a test pins it.** If tasks already exist under
   the new name, the rewritten rows join them. This is `design.md:42-43`'s accepted
   behaviour ("a new session that reuses an old name inherits that name's tasks"), so the
   test asserts the merge rather than guarding against it.
5. `scope.Resolved` gains `SessionID string`, populated from `#{session_id}` as a third line
   of the existing `queryTmux` format — no additional subprocess. A test asserts the format
   string still produces exactly one `display-message` call.
6. `internal/cli` records the mapping after a successful resolve, whenever `Session != nil`
   and `SessionID != ""`. A failed record must **not** fail the command — the popup opening
   matters more than the map being fresh. A test proves a record failure is swallowed and
   surfaced no further than a log.
7. A subcommand performs the rewrite from the new name alone: it asks tmux for that
   session's id (`display-message -t <new-name> -p '#{session_id}'`), looks the old name up
   in the map, rewrites if they differ, and refreshes the map. A test drives it with an
   injected command runner — no tmux server required.
8. **Every no-op path is a silent success, not an error:** unknown session id (tdo never ran
   in that session), map name equal to the new name (nothing to do), and no tasks under the
   old key. The hook fires on every rename in the user's tmux, so noise here is a bug.
9. The keybind and the hook are installed by commands recorded verbatim in the brief and
   proven by hand in a real tmux server: `bind-key` invoking `display-popup -E` with the
   floored size, and `set-hook -ga session-renamed`. Evidence shows the popup opening and a
   rename moving tasks, captured from a real server.
10. The popup size honours the floor: at least ~60x15, percentage-based above that. Evidence
    records the computed size on an 80x24 terminal and confirms the footer is not truncated
    there.
11. Cold start stays under the ~100ms budget with the added write. Evidence records the
    measured median before and after, since DoD 6 puts a write on a path that previously
    only read.
12. `go test ./...`, `go vet ./...` clean, `gofmt -l .` empty; `CGO_ENABLED=0 make build`
    yields a binary with no libsqlite3 in `otool -L`.
13. Tests use real SQLite files under `t.TempDir()`, and no test touches the user's real
    database or state dir.
14. Per-package specifics land in doc comments; CLAUDE.md gets a line only for cross-cutting
    rules (the `$0` expansion trap and `set-hook -a` are strong candidates).

## Verification
Headless Go tests for everything below the tmux boundary: the migration (v1 -> v2 with tasks
preserved), the three store methods including the merge case and the untouched-other-scopes
case, `SessionID` populated from a faked `display-message`, the one-subprocess assertion, the
subcommand driven through an injected runner, and each no-op path returning success.

Above the tmux boundary, a real server on a private socket (`tmux -L`), which is how the
findings in Decisions were established in the first place: create a session, run the
binary, rename the session, assert the tasks moved. This is genuinely runnable — unlike
`display-popup`, `set-hook` and `rename-session` need no attached client.

**Not verifiable here:** the `display-popup` overlay itself, which needs an attached client
(CLAUDE.md). DoD 9's popup evidence is a `bind-key` shown to be installed and the same
command run in a plain pane, not the overlay rendering. Say so rather than implying the
overlay was seen.

## Decisions
- **2026-08-20 (curator, grill — the brief's mechanism does not exist):** the draft said a
  `session-renamed` hook "rewrites the session scope key", but tmux does not give the hook
  the old name. Probed on tmux 3.7b with a throwaway server: on renaming `oldname` ->
  `newname`, the hook sees `#{hook_session_name}` = `newname` and `#{session_name}` =
  `newname`. The old name — the key the tasks are filed under — is already gone. So the
  task as drafted could not be implemented at all.
- **2026-08-20 (curator, grill):** `session_id` **is** stable across a rename (`$0` before
  and after, same probe), so an id -> name map is the only way to recover the old key.
  **Chosen: the map lives in SQLite**, as a new migration, rather than in the XDG state dir.
  The user's call. It costs a migration (critical surface) but keeps the map transactionally
  consistent with the rewrite it drives; the state-dir alternative would let a corrupt file
  silently miss a rename, and unlike the sticky default a missed rename loses a task list
  rather than a preference.
- **2026-08-20 (curator, grill — ACCEPTED LIMITATION):** the map only knows sessions in
  which `tdo` has run at least once. A session renamed before its first `tdo` invocation
  has no tasks to move, so the limitation is mostly self-cancelling — but a session whose
  tasks were created under an *older* server (ids reset on server restart) is genuinely
  unrecoverable by the hook and must be re-homed from the all-tasks view. This is exactly
  the case `design.md:39` calls best-effort.
- **2026-08-20 (curator, grill — rejected alternative):** filing session tasks under
  `session_id` instead of the name would make renames free, but tmux ids reset when the
  server restarts, so every reboot would orphan every session-scoped task. Worse than the
  problem being solved.
- **2026-08-20 (curator, grill — TASK BOUNDARY with `tpm-plugin-and-install`):** both briefs
  claimed installing the keybind and the hook. Resolved: **this task owns behaviour** — the
  store methods, the subcommand, the popup sizing decision, and the exact tmux invocations
  proven by hand — and **`tpm-plugin-and-install` owns packaging**: the plugin script,
  reading `@todo-key` from tmux options, binary resolution, and the README. The mechanism is
  shippable and testable on its own; the packaging is not, which is why the split falls
  here. `tpm-plugin-and-install` is still `draft`, so no cross-brief edit was needed.
- **2026-08-20 (curator, grill — popup size floored):** `design.md:47`'s plain ~60%x60% is
  48x12 on an 80x20 terminal, below the footer's 58-column threshold for every build stamp
  and near the bottom of the frame invariant's asserted range. Chosen: percentages with an
  absolute floor of ~60x15. This is a deliberate, measured refinement of `design.md:47`
  rather than a contradiction of it — the percentage still governs on any reasonable
  terminal.
- **2026-08-20 (curator, grill — implementation trap, verified):** `#{session_id}` expands
  to `$0`, and `run-shell` passes its argument to `sh`, which expands `$0` to the shell's
  own name. Observed as `sid=sh` twice, including with single quotes at the tmux level,
  because tmux expands the format before `sh` is invoked. **Mitigation: never pass the id
  through the hook.** The hook passes the new name; the binary asks tmux for that session's
  id itself. `#{q:session_id}` may also work but was not verified and is not the plan.
- **2026-08-20 (curator, grill):** `set-hook -g` **replaces** any existing hook of that
  name. The install must use `-a` so a user's own `session-renamed` hook survives.
- **2026-08-20 (curator, Checkpoint 1 APPROVED):** plan signed off as written — migration
  first, store methods, `SessionID` folded into the existing `display-message` call, `cli`
  record plus the rename subcommand, real tmux last. The migration was **not** split into
  its own task: it is small, and auditing it next to the code that first uses it is worth
  more than a separate checkpoint. DoD 11's hot-path write stays a **measurement** rather
  than a pre-emptive optimisation — read-then-write-only-on-change is the known fix if the
  number says so, and guessing at it now would add a read to every invocation to avoid a
  cost nobody has observed. Status `ready`.

## Plan
**Approach:** three layers, bottom-up, each fully testable before the layer above it exists.
The migration goes first because it is the only irreversible piece; the tmux-facing work
goes last because it is the only part that needs a real server. Everything in between is
driven by an injected command runner, so the bulk of the task never shells out.

**Files:**
- `internal/store/migrations/002_sessions.sql` — new. Additive; never edited once applied.
- `internal/store/sessions.go` — new. `RecordSession`, `SessionName`, `RenameSession`. A
  separate file rather than growing `tasks.go`, since this is a different table.
- `internal/store/sessions_test.go` — new, including the v1 -> v2 upgrade test.
- `internal/scope/scope.go` — `Resolved.SessionID`, `#{session_id}` as a third line of the
  existing `queryTmux` format string.
- `internal/scope/scope_test.go` — the one-subprocess assertion.
- `internal/cli/cli.go` — record-on-resolve, and the rename subcommand.
- `internal/cli/cli_test.go` — the subcommand and its no-op paths, injected runner.
- `docs/design.md` — note the popup size floor at `:47`.
- `CLAUDE.md` — the `$0`-through-`run-shell` trap and `set-hook -a`.

**Sequencing:**
1. **Migration 002 and its upgrade test first.** Critical surface, and everything else
   depends on the table existing. Prove a v1 database reaches v2 with its tasks intact
   before writing a single method against the new table.
2. `RecordSession` / `SessionName` / `RenameSession` with tests: the bulk rewrite, the merge
   case (DoD 4), and the proof that `dir` and `global` rows are untouched. No tmux involved.
3. `Resolved.SessionID` from the third format line, plus the assertion that `queryTmux`
   still makes exactly one `display-message` call.
4. `internal/cli` records the mapping after a successful resolve, with DoD 6's test that a
   record failure is swallowed rather than failing the command.
5. The rename subcommand and all three no-op paths (DoD 8), driven by an injected runner.
6. **Real tmux server on a private socket** (`tmux -L`): install the hook with `-a`, rename a
   session, assert the tasks moved. Then the keybind and the floored popup size.
7. Cold-start measurement before and after (DoD 11), the `design.md` size note, the CLAUDE.md
   lines, then `make test`, `make lint`, `CGO_ENABLED=0 make build`, `otool -L`.

**What could go wrong:**
- **The migration-concurrency test proving nothing.** CLAUDE.md is explicit: racing an
  *already-migrated* database says nothing about the dangerous window. The new test must
  race two **first** opens of a v1 database, so the losing process waits on the outer
  `BEGIN IMMEDIATE` and then finds the version already current.
- **The hot-path write (DoD 11).** Recording the session on every invocation puts a write on
  a path that only read before, against a ~100ms budget currently met at ~8ms. Measure
  before optimising. If it costs anything, the cheap fix is to read the map first and write
  only when the name actually changed — a read is not a WAL append.
- **`set-hook -a` stacking on every plugin reload.** TPM re-runs the install script, and
  `-a` appends, so the hook can accumulate copies. Harmless in effect because the hook body
  is idempotent (a no-op when the names already match) but wasteful and confusing in
  `show-hooks`. Note it for `tpm-plugin-and-install`, which owns the install script; do not
  solve it here by reverting to `-g`, which would clobber the user's own hook.
- **Session names containing shell metacharacters** passed through `run-shell`. Quote at the
  tmux level and include a test with a space and a quote in the name — this is the same
  class of bug as the `$0` trap, and the trap proves the class is live.
- **The merge in DoD 4 producing visually duplicate rows** when both sessions held a task
  with the same text. Correct per `design.md:42-43` and not to be "fixed"; worth a line in
  the Evidence so it does not read as a defect at Checkpoint 2.
- **Deciding what the current session id is inside `internal/store`.** It must not: the
  store takes an id string and knows nothing about tmux. The seam is the same one the repo
  already keeps for scopes.
