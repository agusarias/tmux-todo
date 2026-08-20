# Tmux Integration And Rename Hook

**Status:** done
**Worktree:** none (merged and removed)

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
- **2026-08-20 (executor — the exact-match target the plan assumed does not exist):**
  `display-message -t '=<name>'` returns **an empty line and exit 0** on tmux 3.7b, because
  `-t` there is a target-*pane* and `=dev` is parsed as a pane name. The working form is
  `-t '=<name>:'`; the trailing colon is what makes tmux read the target as a session, and
  the `=` then forces the exact match the plan wanted. Found only because `SessionID`
  treats empty output as an error instead of an empty id — a silent-empty tmux answer is
  the failure mode to design against here.
- **2026-08-20 (executor — the hook takes NO argument; this closes a command injection):**
  the plan had the hook pass the new name (`run-shell "tdo session-renamed -- '#{hook_session_name}'"`),
  and measured against real tmux that form recovers renames for names containing a space,
  `"`, `$`, `;`, a backtick or `#` — but **not** `'`, and not `:`. The `'` case is not
  merely an unrecovered rename: a session called `x'; touch /tmp/pwned; '` breaks out of the
  sh single quotes and **executes**. No tmux format escapes for a shell (`#{q:}` escapes for
  tmux's own parser), so the fix is to interpolate nothing. `run-shell`'s child inherits
  `$TMUX`, whose third field is the session the hook fired for, so a bare
  `tdo session-renamed` already knows which session it is. Verified: with the argument-free
  hook all nine awkward names — including `a:b` and the injection payload — re-file
  correctly, and the canary file is never created. **DoD 7's named form is unchanged and
  still tested**; the no-argument form is an additional entry point, and it is the one the
  installed hook uses. This is a change to *how* DoD 9's install command is written, not to
  what the task delivers — flagged here because it is the one place the plan was overruled
  by a security finding rather than by a bug.
- **2026-08-20 (executor — `display-popup` cannot compute its own floor):** `-w`/`-h` do
  **not** expand formats (tmux 3.7b: `width invalid`), so `max(60%, 60)` cannot be an
  expression and the keybind branches with `if-shell -F` instead. Two further traps, both
  hit: `#{>=:x,y}` compares **strings**, so `#{>=:80,100}` is *true* and the first version of
  the keybind silently took the 60% branch on an 80-column terminal; and
  `display-message -p` **eats a literal `%`**, so the probe that was supposed to prove the
  condition printed `60` for `60%` and looked correct. The condition is now arithmetic:
  `#{m:-*,#{e|-|:#{client_width},100}}` is "narrower than 100 columns".
- **2026-08-20 (executor — the popup overlay IS assertable, contra CLAUDE.md):** running
  `TMUX= tmux -L <sock> attach -t work` *inside* another session's pane manufactures a real
  attached client, so `display-popup` has somewhere to draw and `capture-pane` on the outer
  pane captures the overlay, border included. DoD 9's popup evidence is therefore the real
  thing — key pressed, overlay captured — not just an installed binding. CLAUDE.md's claim
  to the contrary has been corrected.
- **2026-08-20 (executor — DoD 11 measured, no optimisation):** the added write costs
  nothing measurable (below), so the read-then-write-only-on-change variant was **not**
  written, per Checkpoint 1.
- **2026-08-20 (executor — DoD 10 needs the curator's call):** the 60-column floor and
  "the footer keeps its version tail" cannot both hold for every build. See Evidence.


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

## Evidence

**Merged into local `main` as `683348c`** (implementation commit `0b8f5a4`; the worktree
`../todo-tmux-integration` has been removed, so read the diff with `git show 0b8f5a4` or
`git log -1 -p 683348c`). Not pushed. `make test`, `make lint` and `make build` were re-run
on `main` after the merge and are green there too.

All output below is real, from the worktree (`../todo-tmux-integration`) on tmux 3.7b,
go1.26.6, macOS arm64. Real-tmux legs run on private sockets (`tmux -L …`) with
`XDG_DATA_HOME` pointed at a temp dir, so the user's database was never touched.

### Tests, lint, build (DoD 12, 13)

```
$ make test
ok  github.com/agusarias/tmux-todo/internal/cli     0.758s
ok  github.com/agusarias/tmux-todo/internal/scope   1.623s
ok  github.com/agusarias/tmux-todo/internal/store   1.065s
ok  github.com/agusarias/tmux-todo/internal/task    1.116s
ok  github.com/agusarias/tmux-todo/internal/tui     1.152s
$ make lint            # go vet ./... + gofmt -l .
(clean)
$ go test ./... -count=1 -race
(all ok)               # 315 tests total
$ CGO_ENABLED=0 make build && otool -L bin/tdo
bin/tdo:
    /usr/lib/libSystem.B.dylib
    /usr/lib/libresolv.9.dylib      # no libsqlite3
$ ./bin/tdo doctor --db <temp>
schema   2 (latest 2)
journal  wal
ok
```

Every new test uses a real SQLite file under `t.TempDir()`; every one that resolves a scope
redirects the sticky-default state dir to a temp dir too.

### The guards were checked by deleting the implementation

A green test whose subject is gone proves nothing (CLAUDE.md). Each new guard was re-run
against a mutated implementation and confirmed to fail:

| mutation | test that failed |
|---|---|
| `RenameSession` drops the `scope_kind` predicate | `TestRenameSessionMovesTasks` |
| `002_sessions.sql` deleted | `TestUpgradeFromV1KeepsTasks` |
| `session_id` fetched by a second `display-message` | `TestResolveQueriesTmuxOnceForAllThreeFields` |
| `SessionID` target loses its `=`…`:` wrapper | `TestSessionIDTargetsAnExactName` |
| `openEnv` stops calling `recordSession` | `TestAddRecordsTheSessionMap` |
| a failed map write is swallowed *silently* (no log) | `TestSessionMapFailureDoesNotFailTheCommand` |
| a failed map write is made fatal | `TestSessionMapFailureDoesNotFailTheCommand` |
| `session-renamed` uses `openEnv` instead of `openStore` | `TestSessionRenamedWhenItIsTheCurrentSession` |

That last one is the guard worth knowing about. When the hook fires, the renamed session
*is* the one the process runs in, so refreshing the map before reading it — which is exactly
what `openEnv` does for every other command — records `$3 -> "new"` first, the lookup then
finds the new name, concludes there is nothing to do, and orphans the tasks. Every other
test in the file still passes under that mutation.

### The rename, end to end on a real tmux server (DoD 3, 4, 7, 8, 9)

Hook installed with `-a`, and a user's own hook survives it:

```
$ tmux -L tdorename set-hook -ga session-renamed "run-shell -b '<tdo> session-renamed'"
$ tmux -L tdorename set-hook -ga session-renamed "display-message 'user hook still here'"
$ tmux -L tdorename show-hooks -g | grep session-renamed
session-renamed[0] run-shell -b "<tdo> session-renamed"
session-renamed[1] display-message "user hook still here"
```

```
== before the rename (session is 'oldname') ==
  map:   $0 -> oldname
  tasks: session:oldname=rebase onto main session:oldname=check CI global:=call the dentist

== tmux rename-session -t oldname newname ==
== after the rename ==
  map:   $0 -> newname
  tasks: session:newname=rebase onto main session:newname=check CI global:=call the dentist

== tdo list, run from inside the renamed session ==
  2 [ ] session:newname  check CI
  1 [ ] session:newname  rebase onto main
  3 [ ] global           call the dentist

== renaming onto a name that already has tasks (design.md's accepted merge) ==
  tasks: session:takenname=rebase onto main session:takenname=check CI
         global:=call the dentist session:takenname=already under takenname
```

The merge is intentional (`design.md`: "a new session that reuses an old name inherits that
name's tasks"). Two rows with the *same text* would sit next to each other after such a
merge; `TestRenameSessionOntoExistingKeyMerges` pins that, and it is not a defect.

No-op paths, silent:

```
== renaming a session tdo has never run in ==
  tasks before=4/10 after=4/10  (unchanged: yes)
  $ tdo session-renamed --verbose -- virgin2
    session $2 is not in the map: nothing to move
```

Awkward names, with the argument-free hook (the injection canary is a file the payload
tries to create):

```
  [start ] -> [a b   ] task under [a b   ] OK
  [a b   ] -> [a"b   ] task under [a"b   ] OK
  [a"b   ] -> [a'b   ] task under [a'b   ] OK
  [a'b   ] -> [a$b   ] task under [a$b   ] OK
  [a$b   ] -> [a;b   ] task under [a;b   ] OK
  [a;b   ] -> [a`b   ] task under [a`b   ] OK
  [a`b   ] -> [a#b   ] task under [a#b   ] OK
  [a#b   ] -> [a:b   ] task under [a:b   ] OK
  [a:b   ] -> [x'; touch /…/pwned; '] task under [x'; touch /…/pwned; '] OK
  [x'; touch /…/pwned; '] -> [back to plain] task under [back to plain] OK

  injection canary /…/pwned: absent — no injection
  map: $0 -> back to plain
```

For the record, the *argument-passing* hook the plan specified was measured first and fails
two of those: `a'b` (sh quoting — and that is the injection) and `a:b` (tmux target
parsing). That is what moved the installed hook to the argument-free form.

### The popup, actually opened (DoD 9, 10)

`display-popup` needs an attached client, so one was manufactured: two sessions, and
`TMUX= tmux -L <sock> attach -t work` run *inside* the first session's pane. The outer pane
is then a real 80x24 client, `C-b T` is a real key press, and `capture-pane` sees the
overlay.

80x24 client — both dimensions below the floor, so the popup is 60x15:

```
  client 80x24, floor branch: w<100? yes h<25? yes
         ┌──────────────────────────────────────────────────────────┐
         │╭────────────────────────────────────────────────────────╮│
         ││  tdo                                                   ││
         ││                                                        ││
         ││  ▸ ⌘ check CI                 (session: work)          ││
         ││    ⌘ rebase onto main                                  ││
         ││    · fix auth redirect        (dir: ~/workspace/todo)  ││
         ││    ◉ call the dentist         (global)                 ││
         ││                                                        ││
         ││                                                        ││
         ││                                                        ││
         ││                                                        ││
         ││  1/2/3 filter · j/k move · space done · q quit · vdev  ││
         │╰────────────────────────────────────────────────────────╯│
         └──────────────────────────────────────────────────────────┘
```

`j` then `space` moved the cursor and completed the row, and the store agrees
(`select text from tasks where done=1` -> `check CI`); `q` closed the popup and the shell
was back. The escapes ship — `capture-pane -pe` on the selected row:

```
  ││  ▸ ⌘ ^[[1mrebase onto main^[[0m                 ^[[2m(session: work)^[[0m  ││
```

120x40 client — above the floor, so the percentages govern (72x24 popup):

```
  client 120x40, floor branch: w<100? no h<25? no
    │╭────────────────────────────────────────────────────────────────────╮│
    ││  ▸ ⌘ check CI                             (session: work)          ││
    ││    · fix auth redirect                    (dir: ~/workspace/todo)  ││
    ││  1/2/3 filter · j/k move · space done · q quit · vdev              ││
```

Measured sizes, straight from the popup (`stty size` inside it):

```
  -w 60   -h 15   -> TUI pane 58x13     (the popup border costs 2x2)
  -w 60%  -h 60%  -> TUI pane 46x12     on the same 80x24 client, for comparison
```

Note also the map row the popup wrote by itself — DoD 6 on the path that matters:
`$1 -> work`, recorded by `tdo tui`'s own resolve.

### DoD 10, the one item that needs a decision

The floor holds: 60x15 on an 80x24 terminal, percentages above 100x25. The footer is a
different matter. Its text is `49 + len(Version)` columns and the pane at the floor gives it
52, so:

| `Version` | footer columns | at the 60-col floor |
|---|---|---|
| `dev` (3) | 52 | fits exactly — captured above, `· vdev` intact |
| `e91f97b-dirty` (13) | 62 | tail truncated: `· ve9…` |
| `v0.1.0-3-gcf328ba-dirty` (23) | 72 | needs a 78-column popup |

So "the footer keeps its version tail" is true for a `dev`-length stamp — which is the
58-column minimum the Constraints quote — and false for a `git describe` stamp, which is
what `make build` produces. The frame itself is correct in every case: nothing wraps,
nothing scrolls, the truncation is `footer()`'s own `truncate` doing its job on the one line
CLAUDE.md designates as the right thing to lose the tail of.

Three ways out, none of which the executor should pick unilaterally: raise the floor to 78
(contradicts the signed-off ~60x15 Decision), shorten the footer text, or accept the
truncation and drop the clause from DoD 10. **Left for Checkpoint 2.**

### Cold start with the added write (DoD 11)

Both binaries built the same way, run inside a real tmux pane so session scope resolves and
the write actually happens, 60 runs each, `before` = `main` at the claim commit:

```
tdo-before   count    n=60 median= 13.46ms p90= 16.85ms min= 12.55ms
tdo-after    count    n=60 median= 13.37ms p90= 14.56ms min= 12.57ms
tdo-before   list     n=60 median= 13.34ms p90= 14.07ms min= 12.17ms
tdo-after    list     n=60 median= 13.82ms p90= 15.67ms min= 12.83ms
```

The write is in the noise — the `count` median is fractionally *lower* after. (The absolute
number includes ~5ms of Python subprocess overhead, which is why it is above the repo's
~8ms figure; both binaries carry the same overhead, so the delta is the measurement.) Budget
is ~100ms. No optimisation written.

One thing worth flagging for `tpm-plugin-and-install`: `tdo count` is the natural status-line
command, and a status line re-runs it every few seconds — so this write lands on a much
hotter path than the popup. It is free today; if a status-line integration ever makes it
matter, the fix is `SessionName` first and `RecordSession` only when the name changed.

### The install commands, verbatim (DoD 9)

```tmux
# The popup. ~60% x 60% with a 60x15 floor. display-popup does not expand formats in
# -w/-h, so the floor is a branch; and the branch must use arithmetic, because
# #{>=:x,y} compares strings ("80" >= "100" is true).
bind-key T if-shell -F '#{m:-*,#{e|-|:#{client_width},100}}' {
  if-shell -F '#{m:-*,#{e|-|:#{client_height},25}}' {
    display-popup -E -w 60 -h 15 '/path/to/tdo tui'
  } {
    display-popup -E -w 60 -h 60% '/path/to/tdo tui'
  }
} {
  if-shell -F '#{m:-*,#{e|-|:#{client_height},25}}' {
    display-popup -E -w 60% -h 15 '/path/to/tdo tui'
  } {
    display-popup -E -w 60% -h 60% '/path/to/tdo tui'
  }
}

# The rename hook. -ga, not -g, so a user's own hook survives. No argument and no format:
# the child inherits $TMUX, whose session field is the one the hook fired for.
set-hook -ga session-renamed "run-shell -b '/path/to/tdo session-renamed'"
```

`T` and the key name are `tpm-plugin-and-install`'s business (`@todo-key`), as is resolving
`/path/to/tdo`. Two notes for that task: TPM re-runs the install script, and `-ga` *appends*,
so hooks stack as `session-renamed[0]`, `[1]`, … — harmless, since the hook body is
idempotent, but worth de-duplicating there rather than reverting to `-g`. And the brace form
above needs `source-file` (or careful nested quoting) to install from a shell.

### Definition of done

1. ✅ `002_sessions.sql`, `SchemaVersion()` = 2, `doctor` shows `schema 2 (latest 2)`,
   `TestUpgradeFromV1KeepsTasks` migrates a real v1 database with its tasks intact.
   `TestConcurrentFirstOpenOfAV1Database` races eight *first* opens of that v1 file, which is
   the window CLAUDE.md says matters.
2. ✅ `RecordSession` / `SessionName`, clock-blind, `TestRecordSessionRoundTrip` freezes
   `DB.now` and asserts `updated_at`.
3. ✅ `RenameSession`, one statement in SQLite's own implicit transaction;
   `TestRenameSessionMovesTasks` covers pending + done moving and dir/global/other-session
   rows untouched (including a dir row whose key equals the old session key).
4. ✅ `TestRenameSessionOntoExistingKeyMerges`, duplicate text included.
5. ✅ `Resolved.SessionID` from a third line of the same format;
   `TestResolveQueriesTmuxOnceForAllThreeFields` asserts on the recorded argv, so a second
   subprocess fails it.
6. ✅ `openEnv` records on every resolve — including `tdo tui`, which now goes through
   `openEnv` for exactly that reason. Failure is swallowed and logged; the failure in the
   test is real (the table is dropped), not stubbed.
7. ✅ `tdo session-renamed -- "<name>"`, injected runner, no tmux server.
8. ✅ all three no-op paths silent and 0, plus the real-tmux leg.
9. ✅ commands above, installed and exercised on a real server; the popup was opened by a
   real `C-b T` and captured.
10. ⚠️ floor honoured and the size recorded; the footer clause holds for a `dev` stamp and
    not for a `git describe` one. See above — needs a call.
11. ✅ measured before and after; no regression.
12. ✅ tests, vet, gofmt, static build, `otool -L`.
13. ✅ real SQLite files under `t.TempDir()`; the state dir is redirected too.
14. ✅ doc comments carry the per-package specifics; CLAUDE.md got the cross-cutting rules
    (the `run-shell` interpolation trap, `set-hook -a`, the `display-popup` size traps, and
    the nested-client capture technique that corrects its own earlier claim); `docs/design.md`
    records the size floor and the v2 table.

## Close-out — 2026-08-20 (curator, Checkpoint 2 approved)

**Approved as merged.** `683348c` stays on local `main`; no revert, no fix-forward. `main` is
**unpushed** — pushing remains the user's call.

**DoD 10's open item is resolved as moot, not deferred.** The executor was right not to pick
between raising the popup floor to 78, shortening the footer, or dropping the clause — all
three were the curator's call. It then resolved itself: `task-create-edit-rescope` (merged
`dc5e73c`, approved at the same checkpoint) moved `Version` out of the footer entirely, and
`footerText` is now a version-free 39 columns against the 52 the 60 x 15 floor leaves. So the
floor stands as signed off, `design.md` needs no amendment, and the clause is satisfied for
every build stamp rather than only for `dev`. Verified on `main`, not inferred.

**Independently re-verified by the curator on current `main`:** full suite green across all
five packages, `go vet` silent, `gofmt -l .` empty, `migrations/` holds `001_init.sql` and
`002_sessions.sql`, and `tdo doctor` reports schema 2.

**The two findings worth carrying forward.**

1. **The plan's hook mechanism was wrong, and only measurement caught it.** The approved plan
   passed the session name to the hook as an argument. Measured against real names, that form
   fails on `a'b` — which is not a cosmetic failure but *the injection* — and on `a:b`, which
   cannot be a tmux target at all. The executor changed the mechanism to an argument-free
   hook (the child inherits `$TMUX`, whose session field is the one the hook fired for) and
   proved it with an injection canary across ten adversarial names including
   `x'; touch …/pwned; '`. Canary absent. This is the plan's drift clause working exactly as
   intended: a `how` revised under measurement, with the old form's failure recorded rather
   than quietly dropped.
2. **`TestSessionRenamedWhenItIsTheCurrentSession` is the guard that discriminates.** When
   the hook fires, the renamed session *is* the one the process runs in — so refreshing the
   map before reading it, which is what `openEnv` does for every other command, records
   `$id -> new` first, finds the new name, concludes there is nothing to move, and orphans
   the tasks. **Every other test in the file passes under that mutation.** The `openStore`
   /`openEnv` split is therefore load-bearing and must not be "tidied" into one helper.

**On the evidence method.** Every new guard was verified by deleting its subject, in an
eight-row mutation table. That is the `completed-task-lifecycle` DoD-6 lesson applied without
being asked, and it is why this checkpoint needed no re-litigation.

**Accepted limitations, recorded so they are not mistaken for defects later:** the map only
knows sessions in which `tdo` has run at least once, so tasks created under an older tmux
server (ids reset on server restart) are unrecoverable by the hook and must be re-homed from
the all-tasks view — `design.md:41` calls this best-effort on purpose. Renaming onto a name
that already holds tasks **merges** them, which is `design.md:44`, and two rows with the same
text sitting adjacent afterwards is correct.

**Handed to `tpm-plugin-and-install`** (still `draft`, so nothing was edited there): the
verbatim keybind and hook commands are in the Evidence section; `set-hook -ga` appends, so
TPM re-running the install script stacks `session-renamed[0]`, `[1]`, … — de-duplicate there
rather than reverting to `-g`, which would eat the user's own hook; the brace form needs
`source-file` or careful nested quoting; and `@todo-key` plus binary resolution are that
task's business.

**Handed forward as a watch item:** `tdo count` is the natural status-line command and a
status line re-runs it every few seconds, which is a far hotter path than the popup for the
session-map write this task added. Free today (measured: in the noise, median fractionally
lower after). If it ever matters, the fix is `SessionName` first and `RecordSession` only on
a change.

**Curator fix at close-out:** `docs/design.md`'s footer note quoted 42 content columns, which
was true before this task's own 60 x 15 floor landed. Both tasks were in flight at once, so
each wrote the number correct at its moment. Corrected to name both figures. The code was
right either way — `footerText` is 39 columns and fits both.

**CLAUDE.md:** no curator additions needed. The executor had already recorded the
`run-shell` interpolation trap (both halves: `$0` expansion and the unescapable session
name), `set-hook -g` vs `-ga`, the session-key-is-a-name rationale, and the
manufactured-attached-client recipe for `display-popup` tests.

**Worktree:** `../todo-tmux-integration` removed by the executor before this checkpoint.
