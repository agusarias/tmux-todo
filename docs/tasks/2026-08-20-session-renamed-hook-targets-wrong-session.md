# The session-renamed Hook Resolves The Wrong Session

**Status:** ready
**Worktree:** none

## Goal
`tdo session-renamed`, **when run as a `run-shell` hook child**, must act on the session the
hook fired for. Today it resolves an unrelated session, finds no map entry, and exits 0 having
done nothing — so a renamed session's tasks are silently stranded under the old name.

## Why
This is a shipped, silent data-correctness bug in a headline v1 feature. `design.md:37-45`
gives session renames their own subsection, and the whole v2 `sessions` table exists to serve
this one code path. It does not work.

It is silent in the worst way: exit 0, no output, and the tasks are still in the database — so
nothing looks broken until the user goes looking for a list that has quietly become
unreachable. The all-tasks view can re-home it, which is the mitigation `design.md:41` already
anticipates, but that is a manual recovery for something that was supposed to be automatic.

**Why it shipped, and why Checkpoint 2 approved it.** `tmux-integration-and-rename-hook`'s
evidence showed a real `rename-session` on a real server moving the tasks. That evidence
exercised the **command**, run by hand in a pane — and a pane *has a client*, so tmux resolves
the current session correctly there. A manual test of this command proves nothing about the
hook. The curator read the evidence, verified the mutation table, and missed that the thing
under test was not the thing that ships.

## Constraints
- The fix belongs in whatever resolves the session for `session-renamed`
  (`internal/cli/session.go`, and `internal/scope` if the seam needs to move). No schema
  change: the `sessions` table and its contents are correct — only the lookup key is wrong.
- **`internal/tui` and the store stay untouched.** This is a CLI/scope bug.
- No change to `tmux-todo.tmux`'s installed hook. The hook line is right; passing no argument
  and letting the child inherit `$TMUX` remains correct, and re-introducing an interpolated
  session name would be the injection bug `tmux-integration` already fixed.
- Out of scope: making the popup or any other command more robust to a missing client. Only
  the hook path runs without one.

## Critical surface
**This is a bulk `UPDATE` keyed on a session identity, on the user's real database, triggered
automatically by a tmux event.** Getting the key wrong in the other direction is worse than
today's no-op: resolving the wrong session and *finding* it in the map would rewrite an
unrelated session's tasks onto the renamed name. Today's failure is inert; a careless fix is
destructive. The DoD therefore requires a negative test — an unrelated session's tasks must be
proven untouched.

## Definition of done
1. **A test that fires the real hook.** The guard must install the hook on a private tmux
   server and `rename-session`, then assert the tasks moved. A test that invokes
   `tdo session-renamed` directly cannot fail against this bug and is the reason it shipped.
2. `tdo session-renamed` derives the session from **`$TMUX`'s third field** and targets it
   explicitly (`display-message -t "$<id>" -p …`), rather than asking tmux for "the current
   session". Interpolating *that* value is safe where a session name is not: `$TMUX` yields
   `$<number>`, never user data.
3. **The negative test**: a second, unrelated session with its own tasks is present during the
   rename, and its tasks are asserted unchanged. This is the destructive-fix guard from
   Critical surface.
4. The no-op paths still no-op silently and exit 0: a session absent from the map, a rename
   where the name already matches, and a server where the map is empty.
5. **Mutation proof**: with the fix reverted to the current resolution, the DoD 1 test must
   fail. Recorded with real output.
6. `make test`, `make test-plugin`, `make lint` clean; `gofmt -l .` empty; static build holds.
7. CLAUDE.md's existing note on this is folded into its final form — the uncommitted draft in
   the working tree at the time of writing says "so tmux will answer for that session" was
   wrong, and that correction plus the two probing traps below belong in the committed version.

## Verification
- The DoD 1 hook-fired test, and the DoD 5 mutation proof, both with real output.
- The DoD 3 negative test's before/after for the unrelated session.
- An end-to-end transcript on a private socket: task under `alpha`, a second session with its
  own task, `rename-session alpha alpha2`, and the resulting `tdo list --json`.

## Decisions

### 2026-08-20 (curator) — diagnosed and confirmed before the brief was written

Found as an **uncommitted CLAUDE.md edit in the working tree** referencing
`docs/tasks/2026-08-20-session-renamed-hook-targets-wrong-session.md`, a brief that did not
exist. So the finding was recorded but untracked. The curator reproduced it from scratch rather
than taking the note at face value.

**Confirmed reproduction** (private socket, `tmux -L`, tmux 3.7b):

```
map    BEFORE : $0|alpha          tasks BEFORE : session|alpha
== rename alpha -> alpha2, via the installed hook ==
map    AFTER  : $0|alpha          tasks AFTER  : session|alpha     <- nothing moved
```

**Mechanism, measured.** A wrapper around the hook child captured both its environment and the
binary's own verbose output:

```
TMUX=/private/tmp/tmux-501/bugMECH,52789,0        <- third field 0 = $0 = alpha, CORRECT
session $1 is not in the map: nothing to move     <- but tdo resolved $1 = yankee
rc=0
```

`$TMUX` carries the right session; `tdo` is not using it. It asks tmux for the current session,
and a `run-shell` child has **no client**, so tmux answers an unrelated one. The map lookup
then misses and the command exits 0 silently.

**Proof that the command is fine and only the hook path is broken.** Same server, no hook
installed, `tdo session-renamed` typed into the pane by hand after the rename:

```
tasks AFTER : session|alpha2      map AFTER : $0|alpha2      <- works
```

Command works, hook does not. That is precisely the gap the original task's evidence fell into.

**TWO PROBING TRAPS, both hit by the curator while diagnosing this. Read before testing.**

1. **The hook child inherits the tmux *server's* environment, not the pane's.** Setting
   `XDG_DATA_HOME` in a pane (or in the shell that sends keys) does **not** reach the hook
   child, so `tdo session-renamed` opens `store.DefaultPath()` — **the user's real database**.
   The curator's first three reproduction attempts did exactly this and were invalid; worse,
   they reached the real database (harmlessly, because the private server's low session ids
   were absent from the real map, so the lookup no-op'd). **Export `XDG_DATA_HOME` before
   starting the server** and verify with `tmux show-environment -g XDG_DATA_HOME`. This is a
   safety requirement, not a convenience.
2. **tmux expands `#{...}` in a `run-shell` argument before `sh` sees it.** A canary containing
   a format silently reports the *server's* view and reads as though the child agreed. Any
   probe of the child's view must contain no formats — put the logic in a script file and pass
   only plain arguments.

**An earlier, misleading probe, recorded so it is not repeated.** An untargeted
`display-message -p '#{session_name}'` run from a hook child answered *correctly* on a
three-session server. The client-less fallback evidently lands on the right session sometimes —
which is exactly why this bug survived a real end-to-end test. Do not use that call's success
as evidence of anything.

**Triage: standard path, no grill.** Ambiguity is already low — the diagnosis above is
complete and the fix is named. Blast radius is high (a bulk UPDATE on real data, fired
automatically), which is why the negative test in DoD 3 is mandatory rather than optional.

## Plan
**Approved at Checkpoint 1 on 2026-08-20**, including the ruling that the hook-fired guard
lives in `test/plugin_install_test.sh` rather than in a `tmuxAlive()`-gated Go test. The plan
is disposable; Goal, Constraints and Definition of done are not — changing those is a scope
event, so set `blocked` with the question instead.

**Approach.** Two changes, and the test is the harder half.

1. **Resolution.** Wherever `session-renamed` currently determines "which session am I",
   replace it with: read `$TMUX`, split on `,`, take the third field, build the target `$<n>`,
   and ask tmux with `-t`. `internal/scope` already builds an exact-match target for
   `SessionID` (`=name:`) and `internal/cli/jump.go` builds one for switch targets, so this is
   a third instance of a pattern the repo knows — worth checking whether it belongs beside one
   of them rather than inline.
2. **The test that actually fires a hook.** The repo has no Go test that drives a real tmux
   server, and `internal/cli`'s tests are environment-blind by design. Two options, and the
   executor should pick with this written down: a Go test gated on `tmuxAlive()` in the style
   of `internal/scope`'s live legs, or a case in `test/plugin_install_test.sh`, which already
   spins private servers, installs hooks and asserts on real tmux state. **The shell harness is
   the better fit** — it exists, it already does exactly this shape of work, and `make
   test-plugin` is already separated from `make test` for precisely the reason that it needs a
   tmux binary. The cost is that the assertion lives away from the Go code it guards, which the
   brief accepts in exchange for testing the thing that ships.

**Sequencing.** Write the failing hook-fired test **first** — it must fail against today's
code, which is DoD 5's mutation proof obtained for free and in the right order. Then the
resolution fix. Then the negative test and the no-op paths.

**What could go wrong.**
- *A fix that resolves the wrong session and finds it.* Today's bug is inert because the lookup
  misses. If a fix resolved some *other* real session that IS in the map, it would rewrite that
  session's tasks onto the renamed name — silent data loss rather than a silent no-op. DoD 3
  exists for this and should be written before the fix, not after.
- *Testing against the real database.* Trap 1 above. The harness's existing sandbox discipline
  handles it; a hand-rolled test very likely will not.
- *`$TMUX` absent or malformed.* Outside tmux, or if the format ever changes, the third field
  may be missing. That must be the existing silent no-op path, not a crash and not a fallback
  to asking tmux — a fallback would restore exactly today's bug.
- *The fix appearing to work because of the lucky fallback.* Any manual check must use a server
  with several sessions where the renamed one is *not* whatever the fallback would pick, or it
  proves nothing.

## Evidence
(Added by the executor.)
