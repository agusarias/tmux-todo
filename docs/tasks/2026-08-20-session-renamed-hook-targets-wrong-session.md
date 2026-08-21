# The session-renamed Hook Resolves The Wrong Session

**Status:** review
**Worktree:** ../todo-session-renamed-hook

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

## Decisions (executor, 2026-08-21)

**The fix is a new seam, `scope.EnvSession`, not a patch inside `renamedSession`.** The plan
suggested checking whether it belongs beside `SessionID` or `jump.go`'s target builder; it
belongs beside `SessionID`, in `internal/scope`, because it answers the same *kind* of question
(ask tmux about one specific session) and shares the trap `SessionID` documents — tmux reports
an unknown target by printing nothing and exiting 0. `internal/cli` keeps no tmux knowledge it
did not already have.

**A malformed `$TMUX` is an error, not a silent no-op.** The plan said "that must be the
existing silent no-op path, not a crash and not a fallback to asking tmux". The existing
behaviour outside tmux is *not* silent — it exits 1 naming the reason, pinned by
`TestSessionRenamedWithNoNameOutsideTmuxFails` — so "existing" and "silent" pointed in opposite
directions. Kept it loud and consistent with the code already there: exit 1, no rewrite, and
never a fallback to the untargeted query. The three DoD 4 no-op paths (unknown id, name already
current, cold map) are about a *working* tmux and are unchanged. `TestSessionRenamedRejectsAnUnusableTmuxEnv`
sets `clientless` to a session that *would* be found, so a fallback would look like success.

**The Go-level guard needed a dishonest fake made honest.** `fakeContext` answered the
untargeted and targeted queries with the same string, so no Go test could tell which question
the command asked — that is a large part of *why* this shipped. It now dispatches on the target
and grows `clientless`/`clientlessID`: the session an untargeted `display-message` resolves to
when there is no client. With that asymmetry the bug is reproducible with no tmux server at all,
which is what puts the regression guard on the tmux-less CI runner rather than only in
`make test-plugin`.

**DoD 4's "name already matches" leg cannot be reached by a real rename.** Measured on 3.7b:
`rename-session -t alpha alpha` does not fire the `session-renamed` hook at all. The path is
reachable only by a second firing, and a second *concurrent* firing is a race, not an assertion.
`rename-4` therefore installs a hook whose body runs the command twice in one child — strictly
ordered, so the second call provably sees the map already current. The Go table test keeps
covering the path in isolation.

**`assert_fallback_is_not` was added after the mutation proof caught a vacuous case.**
`rename-4` passed against the pre-fix binary: with only `harness` and `delta` on the server, the
client-less fallback happened to resolve `delta` — the "lucky fallback" this brief warns about,
reproduced. Every rename case now asserts, before the rename, what a client-less child resolves
to and that it is *not* the session under test. That precondition is what makes these cases
known-able-to-fail rather than assumed to be.

## Evidence

Verified in `../todo-session-renamed-hook` on tmux 3.7b, go 1.26 (`/opt/homebrew/bin/go`),
darwin/arm64. Merge commit recorded at the end of this section.

### DoD 1 + 3 — a test that fires the real hook, with a bystander

`test/plugin_install_test.sh`, new `rename-*` section. It installs the hook by running the real
`tmux-todo.tmux` with the real binary on PATH, seeds a session task in `alpha` and in `bravo`
from inside each session's own interactive shell, then performs a real `rename-session`:

```
== rename-1-hook-moves-tasks
    ok   the plugin installed its hook
    ok   the SERVER's environment carries the sandbox XDG_DATA_HOME
    ok   seeded a session task in alpha and in bravo
    -- before: alpha task session|alpha | bravo task session|bravo
    ok   alpha's task is filed under alpha
    ok   bravo's task is filed under bravo
    -- a client-less child resolves the current session as: bravo
    ok   a client-less child would resolve some OTHER session, so this case can fail
    -- after : alpha task session|alpha2 | bravo task session|bravo
    ok   the hook moved the renamed session's task
    ok   the bystander session's task was left alone
    ok   the hook printed nothing
    -- after second rename: alpha task session|alpha3
    ok   a second rename moves it again (the map was refreshed)
    ok   the bystander is still untouched
```

The bystander assertions are DoD 3. The second rename is how the map refresh is proven through
the hook: it can only find the old name if the first firing wrote it back.

### DoD 2 — resolution from `$TMUX`'s third field, targeted

`scope.EnvSession` splits `$TMUX` on `,`, takes field 3, requires it to be decimal, and asks
`display-message -t "$<n>" -p '#{session_name}'`. The argv is pinned by
`TestEnvSessionTargetsTheIDFromTmuxEnv`; `TestEnvSessionAgainstRealTmux` checks it agrees with
what tmux itself says, and skips where there is no server.

```
=== RUN   TestEnvSessionTargetsTheIDFromTmuxEnv
--- PASS: TestEnvSessionTargetsTheIDFromTmuxEnv (0.00s)
=== RUN   TestEnvSessionReachesANameWithAColon
--- PASS: TestEnvSessionReachesANameWithAColon (0.00s)
=== RUN   TestEnvSessionRefusesAnUnusableEnvironment
    --- PASS: .../outside_tmux            .../no_session_field       .../empty_session_field
    --- PASS: .../socket_path_only         .../session_field_carries_the_$_sigil
    --- PASS: .../session_field_is_a_name  .../session_field_has_trailing_junk
=== RUN   TestEnvSessionErrors
--- PASS: TestEnvSessionErrors (0.00s)
=== RUN   TestEnvSessionAgainstRealTmux
--- PASS: TestEnvSessionAgainstRealTmux (0.02s)
```

Targeting by id also reaches a name the `=name:` form cannot — measured directly:

```
$0 -> [harness]     rc=0
$1 -> [alpha]       rc=0
$2 -> [weird:name]  rc=0      <- ":" is unusable in a =name: target
$9 -> []            rc=0      <- unknown target: empty, exit 0. Hence the empty check.
```

### DoD 4 — the no-op paths are still silent successes

A hook child that writes anything makes tmux open a `[tmux]` window; `assert_no_hook_output`
counts them, so "silent" is asserted rather than assumed.

```
== rename-2-unknown-session-is-a-silent-no-op
    ok   an unknown session renames silently
    ok   the hook opened a database (it opens the store before resolving)
    ok   and filed nothing in it

== rename-3-cold-map-is-a-silent-no-op
    ok   a cold map renames silently
    ok   the global task is still global, not re-filed
    ok   and the database still holds exactly its one task

== rename-4-second-firing-is-a-silent-no-op
    ok   seeded a session task in delta and in the decoy
    -- a client-less child resolves the current session as: decoy
    ok   a client-less child would resolve the decoy, so this case can fail
    -- after : delta task session|delta2
    ok   the first call in the hook moved the task
    ok   the decoy's task was left alone
    ok   the second call found the map already current and said nothing
```

`rename-2` records a behaviour worth knowing rather than changing: `session-renamed` opens the
store *before* it works out which session it is, so a hook firing on a machine that has never
run `tdo` creates an empty database. Left as-is — the brief puts store changes out of scope.

### DoD 5 — mutation proof

`renamedSession`'s no-argument branch reverted to `r.Resolve()` (i.e. exactly the shipped bug),
rebuilt, both suites re-run.

```
########## MUTANT: renamedSession resolves via Resolve() again ##########
== rename-1-hook-moves-tasks
    -- a client-less child resolves the current session as: bravo
    ok   a client-less child would resolve some OTHER session, so this case can fail
    -- after : alpha task session|alpha | bravo task session|bravo
    FAIL the hook moved the renamed session's task (want 'session|alpha2', got 'session|alpha')
    FAIL a second rename moves it again (the map was refreshed) (want 'session|alpha3', got 'session|alpha')

== rename-4-second-firing-is-a-silent-no-op
    -- a client-less child resolves the current session as: decoy
    FAIL the first call in the hook moved the task (want 'session|delta2', got 'session|delta')
```

And in Go, where the third leg shows the *destructive* direction the Critical surface section
warned about — the mutant rewrote a bystander's tasks and its map row:

```
--- FAIL: TestSessionRenamedHookIgnoresTheClientlessSession/the_wrong_session_is_not_in_the_map_at_all
    session keys = [bravo alpha]: the renamed session's task did not move to alpha2 — the
        command resolved the session from the client instead of from $TMUX
--- FAIL: .../the_wrong_session_is_in_the_map_under_its_current_name
    session keys = [bravo alpha]: ... a task is still filed under the old name
--- FAIL: .../the_wrong_session_is_in_the_map_under_an_older_name
    session keys = [bravo alpha]: the bystander's task left "stale-bravo" — an unrelated
        session's tasks were rewritten
    map[$7] = "bravo" (err <nil>), want "stale-bravo"
--- FAIL: TestSessionRenamedRejectsAnUnusableTmuxEnv/{no_session_field,empty_session_field,session_field_is_not_a_number}
    exit code 0, want 1 ... session keys = [bravo]: something was rewritten despite the
        unusable $TMUX
```

An earlier round of this proof is why `assert_fallback_is_not` exists: `rename-4` passed against
the mutant before it grew a decoy session (see Decisions).

### DoD 6 — suites, formatting, static build

```
$ make lint
go vet ./...

$ make test
?   github.com/agusarias/tmux-todo/cmd/tdo   [no test files]
ok  github.com/agusarias/tmux-todo/internal/cli     0.798s
ok  github.com/agusarias/tmux-todo/internal/scope   1.250s
ok  github.com/agusarias/tmux-todo/internal/store   0.421s
ok  github.com/agusarias/tmux-todo/internal/task    0.366s
ok  github.com/agusarias/tmux-todo/internal/tui     5.902s

$ gofmt -l .
(no output)

$ make test-plugin
plugin harness: 140 passed, 0 failed          (was 125 before this task)

$ otool -L bin/tdo
bin/tdo:
    /usr/lib/libSystem.B.dylib
    /usr/lib/libresolv.9.dylib                <- no libsqlite3
```

### Verification — the end-to-end transcript

A private socket, the real `tmux-todo.tmux` doing the install, `XDG_DATA_HOME` in the server's
environment, two sessions each with their own task, one real rename.

```
server XDG_DATA_HOME : XDG_DATA_HOME=/private/var/.../e2e.csHGCG/xdg
installed hook       : session-renamed[0] run-shell -b ".../bin/tdo session-renamed"
sessions             : $0|harness $1|alpha $2|bravo

BEFORE  tdo list --json:
{"tasks":[{"id":2,"text":"bravo task",...,"scope":{"kind":"session","key":"bravo"},...},
          {"id":1,"text":"alpha task",...,"scope":{"kind":"session","key":"alpha"},...}]}

== tmux rename-session -t alpha alpha2   (the hook fires; nothing interpolated) ==

AFTER   tdo list --json:
{"tasks":[{"id":2,"text":"bravo task",...,"scope":{"kind":"session","key":"bravo"},...},
          {"id":1,"text":"alpha task",...,"scope":{"kind":"session","key":"alpha2"},...}]}

sessions             : $0|harness $1|alpha2 $2|bravo
output windows       : 0 [tmux] window(s)
```

Task id 1 moved `alpha` -> `alpha2` keeping its id and timestamp; task id 2 under `bravo` is
byte-identical before and after; nothing was printed.

The first attempt at this transcript failed with `tmux: command not found` — `env -i` left no
tmux on PATH and the plugin needs a socket-pinning shim, or the install lands on the *default*
socket. Recorded because it is the same family as every other vacuous-setup trap here: it looked
like a product failure and was a harness failure, and it announced itself only because the
script echoed the installed hook.

### Definition of done

1. **A test that fires the real hook** — done. `rename-1` through `rename-4` in
   `test/plugin_install_test.sh`, driving `rename-session` on a private server through the hook
   the plugin script installed. Proven able to fail (DoD 5, and `assert_fallback_is_not`).
2. **Resolution from `$TMUX`'s third field, targeted** — done. `scope.EnvSession`; argv pinned;
   the decimal check is what makes interpolating it safe.
3. **The negative test** — done. `bravo` (rename-1) and `decoy` (rename-4) hold their own tasks
   across the rename and are asserted unchanged; the Go table's third leg shows the mutant
   failing it, so it is not decorative.
4. **The no-op paths still no-op silently and exit 0** — done. `rename-2`/`rename-3` through a
   real hook, `rename-4` for the second firing, and the existing Go table for all three in
   isolation. Silence is asserted by counting the `[tmux]` output windows tmux opens for a
   chatty child. Caveat recorded: a same-name rename does not fire the hook at all on 3.7b.
5. **Mutation proof** — done, above, with real output from both suites.
6. **`make test`, `make test-plugin`, `make lint` clean; `gofmt -l .` empty; static build** —
   done, above. Plugin harness 125 -> 140 assertions.
7. **CLAUDE.md folded into its final form** — done. The "so tmux will answer for that session"
   correction now reads as a fixed bug with its guards named; two new pitfalls (the hook child
   inherits the *server's* environment; `$TMUX_PANE` is absent from a pane's initial command,
   and send-keys needs a readiness handshake); the lucky-fallback vacuity trap; and
   `internal/scope`'s three ways of asking tmux about a session.
