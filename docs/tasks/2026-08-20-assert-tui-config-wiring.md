# Assert What `tui.Config` Is Actually Wired With

**Status:** review
**Worktree:** none (merged and removed)

## Goal
`internal/cli`'s TUI wiring test must assert the **contents** of the `tui.Config` it builds,
not merely that `runTUI` was reached. Every field the popup depends on — `DB`, `Scopes`,
`Home`, `Version`, `DefaultScope`, `SetSticky`, `Now` — is checked against what the resolved
environment says it should be.

## Why
Split out of `tmux-regression-guard-ci-proof` on 2026-08-20, where the executor found it
while diagnosing the in-tmux test hang. Nothing currently asserts what goes *into* the
Config. `DefaultScope` and `SetSticky` were added to it by `task-create-edit-rescope` and are
wired but unexercised: `runTUI` could pass `SetSticky: nil` and the sticky default would
silently stop persisting, with the whole suite green.

This is the exact pitfall CLAUDE.md has recorded twice and that has already shipped two bugs
in this repo — `scope.Resolve()` was `Resolver{}.Resolve()` while 28 tests passed, because
every one of them injected `TmuxEnv` by hand. `internal/tui` is comprehensively tested
against an *injected* `Config`; the thing nobody tests is the code that *builds* the real
one. `SetSticky` in particular has exactly one production construction site and no assertion
on it.

**RESOLVED 2026-08-20 (grill).** Yes — and by requiring every field to be *checked*, not
merely non-zero. The test holds a `map[string]func(...)` of field name to assertion and walks
`tui.Config`'s exported fields with `reflect`, failing on any field the map does not mention.
A field added to `Config` then breaks this test until someone either asserts it or records why
it needs none. The weaker "every field non-zero, with an allow-list" was rejected: a
`SetSticky` stub that does nothing is non-nil and would pass, which is the bug shape this task
exists to catch. `Now` is the one legitimately-nil field (documented as defaulting to
`time.Now`) and is entered in the map as such, with its reason.

## Constraints
- `internal/cli` only. `internal/tui`, `internal/scope` and `internal/store` do not change.
- Depends on `tmux-regression-guard-ci-proof` having landed the `runTUIProgram` seam — this
  task asserts through that seam rather than adding a second one. If that task has not
  merged, set `blocked` rather than inventing a parallel mechanism.
- Must not start a Bubble Tea program. The seam exists precisely so the test never reaches
  a terminal, and re-introducing that is the hang this task's parent fixed.
- Must not touch the user's real database or state dir: `t.TempDir()` and an explicit
  `StateDir`/`$XDG_STATE_HOME`, per the repo norm.
- Out of scope: any change to `tui.Config`'s shape, and testing the popup's behaviour (that
  is `internal/tui`'s job and is already covered).

## Critical surface
None. Test-only, in a package whose production behaviour is believed correct — the point is
to find out whether that belief is warranted.

## Definition of done
1. The wiring test captures the `tui.Config` passed through the `runTUIProgram` seam and
   asserts every field against the resolved environment the test set up: `DB` is the opened
   store at the `--db` path, `Scopes` equals `Resolved.Active()` in tier order, `Home` is the
   home dir, `Version` is `cli.Version`, `LiveSessions` is what the resolver reported, and
   `Now` is recorded as legitimately nil with its reason.
1b. **The test fails when `tui.Config` gains a field it does not check.** A
   `map[string]func(...)` of field name to assertion, cross-checked against a `reflect` walk
   of `Config`'s exported fields, with a failure message naming the unchecked field. Proven by
   adding a throwaway field to `Config` and recording the failure — that run *is* the evidence
   for this item, since the guard is otherwise indistinguishable from an ordinary test.
2. **`SetSticky` is asserted to be non-nil *and to work***: calling it persists the kind, and
   a subsequent `scope.Resolver.StickyDefault` (with `StateDir` pointed at a temp dir) reads
   it back. A non-nil check alone would pass against a `func(task.ScopeKind) error { return
   nil }` stub, which is the bug shape worth catching.
3. **`DefaultScope` is asserted to be the resolved sticky default**, including the documented
   degradation: when the stored preference names a scope that is not currently available, it
   falls back to the first entry in `Scopes` rather than seeding a scope the user cannot
   submit to.
4. **Mutation proof, one per field.** For each assertion, break the corresponding line in
   `runTUI` (drop `SetSticky`, hardcode `DefaultScope`, pass `nil` `Scopes`, …) and record
   that the named test fails. A wiring test that passes against unwired code is the thing
   this task exists to eliminate, so the evidence is the mutation table, not the green run.
5. The outside-tmux path is covered too: with `$TMUX` unset, `Scopes` carries no session
   scope and `DefaultScope` does not name one.
6. `TestTUIWiringSmoke`'s stale doc comment — "Bubble Tea needs a TTY and a test process has
   none" — is gone or corrected, if its parent task did not already do so.
7. `make test`, `make lint` clean, `gofmt -l .` empty, and the suite completes **inside
   tmux** as well as outside.

## Verification
- `go test ./internal/cli/ -v` naming each per-field assertion.
- The DoD 4 mutation table with real failure output, one row per field. This is the primary
  evidence; the passing run proves much less.
- The suite run inside a real tmux pane (private socket) showing it completes, so this task
  cannot silently re-introduce its parent's hang.

## Decisions
- **2026-08-20 (curator, split from `tmux-regression-guard-ci-proof`):** raised by the
  executor while diagnosing the in-tmux hang, and split out on the user's instruction rather
  than folded into that task. The parent fixes the hang with a five-line seam; this asserts
  the Config's contents, which is real coverage work needing its own DoD. Splitting keeps a
  brief triaged as a two-line test change from quietly becoming something else.

## Plan
**Approved at Checkpoint 1 on 2026-08-20** as written. `internal/cli` only, test-only except
that `TestTUIWiringSmoke`'s
stale doc comment goes. The plan is disposable; Goal, Constraints and Definition of done are
not — changing those is a scope event, so set `blocked` instead.

**Approach.** `runTUIProgram` already gives the test the `tui.Config` for free — the seam
landed with `tmux-regression-guard-ci-proof` (merge `260d8bc`), so the Constraint that would
have blocked this task is satisfied. The test substitutes the seam, captures the Config, and
returns without starting a program.

The structure is a field-name-to-assertion table checked against a `reflect` walk:

```go
// Every exported field of tui.Config must appear here. The reflect walk below
// fails on any that does not, so adding a field to Config breaks this test
// until someone either asserts it or records why it needs no assertion.
checks := map[string]func(*testing.T, tui.Config, fixture){
    "DB":           func(t *testing.T, c tui.Config, f fixture) { /* c.DB.Path() == f.dbPath */ },
    "Scopes":       ...,  // equals f.resolved.Active(), in tier order
    "Home":         ...,
    "Version":      ...,
    "DefaultScope": ...,  // the sticky default, and the unavailable-fallback case
    "SetSticky":    ...,  // non-nil AND round-trips through the state dir
    "LiveSessions": ...,
    "Now":          nilIsCorrect("the popup defaults it to time.Now"),
}
```

`nilIsCorrect` being a named helper rather than a skipped entry matters: it makes "this field
is deliberately unset" a recorded decision with a reason in the source, which is the whole
point of forcing every field to appear.

**The two assertions with teeth**, per DoD 2 and 3:

- **`SetSticky` must round-trip, not merely be non-nil.** Call it, then read the value back
  through a `scope.Resolver` with `StateDir` pointed at a `t.TempDir()`. A non-nil check
  passes against `func(task.ScopeKind) error { return nil }`, which is exactly the bug shape.
- **`DefaultScope` needs the degradation case too.** Seed the state dir with a preference
  naming a scope the fixture makes unavailable, and assert the result falls back to the first
  entry in `Scopes` rather than seeding a scope the user cannot submit to.

**Files.** `internal/cli/cli_test.go` (or a new `wiring_test.go` if the table makes the
existing file unwieldy — executor's call), plus deleting the stale doc comment on
`TestTUIWiringSmoke` if `tmux-regression-guard` did not already.

**Sequencing.**
1. **The reflect guard first, against the current 8 fields.** Land it and prove it fails by
   adding a throwaway field to `Config` — DoD 1b's evidence. Doing this first means every
   assertion added afterwards is one the guard already demanded.
2. `DB`, `Scopes`, `Home`, `Version`, `LiveSessions` — the straightforward five.
3. `SetSticky`'s round trip.
4. `DefaultScope`, both the normal and the unavailable-fallback case.
5. The outside-tmux leg (DoD 5): no session scope in `Scopes`, and `DefaultScope` does not
   name one.
6. The per-field mutation table (DoD 4), then the sweep including the in-tmux run.

**What could go wrong.**
- *The reflect walk passing vacuously.* If it iterates the map instead of the struct, it
  proves nothing — every entry trivially matches itself. It must walk
  `reflect.TypeOf(tui.Config{})` and look each field up in the map, not the reverse. The
  throwaway-field proof is what distinguishes these two implementations, which is why it is a
  DoD item rather than a nicety.
- *Touching the user's real state dir.* `SetSticky`'s round trip writes for real. `StateDir`
  and `$XDG_STATE_HOME` must both point at temp dirs — `internal/scope/sticky.go` falls
  through to the real one otherwise, and this task's whole subject is a *write* path.
- *Comparing `SetSticky` by value.* Func values are not comparable in Go beyond nil; the
  assertion has to be behavioural. Anything reaching for `==` on a func will not compile,
  which is a helpful failure rather than a silent one.
- *`DB` comparison.* Asserting pointer identity against the `store.DB` the test opened is
  wrong — `runTUI` opens its own. Compare `DB.Path()`, and assert it is usable.
- *Re-introducing the hang.* Any path that reaches the real `tui.Run` puts this task's parent
  bug straight back. The in-tmux sweep leg (DoD 7) is the check that it has not.

## Evidence

**Where the work landed.** One new file, `internal/cli/wiring_test.go` (~400 lines, test-only).
No production file changed: `git diff --stat` against the branch point is empty for everything
outside that file. DoD 6 needed nothing — `tmux-regression-guard-ci-proof` had already
replaced the stale "Bubble Tea needs a TTY" comment (`grep -rn "needs a TTY" internal/` is
empty).

**Tests added.** `TestTUIConfigWiring` (the in-tmux leg, one subtest per field),
`TestEveryTUIConfigFieldIsAsserted` (the reflect guard), `TestTUIConfigWiringOutsideTmux`
(DoD 5) and `TestTUIConfigDefaultScopeDegrades` (DoD 3's fallback case).

```
$ go test ./internal/cli/ -run 'TestTUIConfig|TestEveryTUIConfigField' -v
--- PASS: TestEveryTUIConfigFieldIsAsserted (0.00s)
--- PASS: TestTUIConfigWiring (0.01s)
    --- PASS: TestTUIConfigWiring/DB (0.00s)
    --- PASS: TestTUIConfigWiring/Scopes (0.00s)
    --- PASS: TestTUIConfigWiring/Home (0.00s)
    --- PASS: TestTUIConfigWiring/Version (0.00s)
    --- PASS: TestTUIConfigWiring/DefaultScope (0.00s)
    --- PASS: TestTUIConfigWiring/SetSticky (0.00s)
    --- PASS: TestTUIConfigWiring/LiveSessions (0.00s)
    --- PASS: TestTUIConfigWiring/Now (0.00s)
--- PASS: TestTUIConfigWiringOutsideTmux (0.00s)
--- PASS: TestTUIConfigDefaultScopeDegrades (0.00s)
ok  	github.com/agusarias/tmux-todo/internal/cli	0.235s
```

### The mutation table (DoD 4 — the primary evidence)

Ten mutations, each applied to `runTUI`'s `tui.Config` literal (the last to `tui.Config`
itself), the four tests run, then the file restored byte-for-byte. Every one fails, and the
run after each restore is green. Driven by a throwaway script; nothing in it is committed.

| # | Mutation | Result | Test that caught it | Message |
|---|---|---|---|---|
| 1 | `DB: nil` | FAILS | `TestTUIConfigWiring/DB` | `DB is nil — the popup has no store to read` |
| 2 | `Scopes: nil` | FAILS | `.../Scopes`, `OutsideTmux`, `DefaultScopeDegrades` | `Scopes = [], want [{Kind:session Key:work} {Kind:dir Key:/private/var/…} {Kind:global Key:}]` |
| 3 | `Home: ""` | FAILS | `.../Home` | `Home = "", want "/var/folders/…/003"` |
| 4 | `Version: ""` | FAILS | `.../Version` | `Version = "", want "dev"` |
| 5 | `DefaultScope: "global"` (hardcoded) | FAILS | `.../DefaultScope`, `DefaultScopeDegrades` | `DefaultScope = "global", want the stored sticky default "dir"` |
| 6 | `SetSticky: nil` | FAILS | `.../SetSticky` | `SetSticky is nil — the sticky default would silently stop persisting` |
| 7 | `SetSticky: func(task.ScopeKind) error { return nil }` | FAILS | `.../SetSticky` | `after SetSticky("global") the state dir reads back "dir" — the write did not land` |
| 8 | `LiveSessions: nil` | FAILS | `.../LiveSessions`, `OutsideTmux` | `LiveSessions = map[], want map[spare:true work:true]` |
| 9 | `Now: time.Now` (wiring a clock the popup defaults itself) | FAILS | `.../Now` | `Now = 0x10048b7b0, want nil: the popup defaults it to time.Now` |
| 10 | `tui.Config` gains `Throwaway string` | FAILS | `TestEveryTUIConfigFieldIsAsserted` | `tui.Config.Throwaway (string) has no entry in wiringChecks…` |

Mutation 7 is the one this task exists for: a non-nil `SetSticky` stub passes any
"is it wired" check and fails the round trip. Mutation 10 is DoD 1b — it is what distinguishes
a reflect walk over the struct from one over the map, which would have been vacuous.

```
BASELINE: green
… (ten mutations, all FAILS) …
RESTORED: green
$ git status --short
?? internal/cli/wiring_test.go       # no residue from any mutation
```

### Sweep (DoD 7)

```
$ make test
ok  	github.com/agusarias/tmux-todo/internal/cli	0.920s
ok  	github.com/agusarias/tmux-todo/internal/scope	0.861s
ok  	github.com/agusarias/tmux-todo/internal/store	1.007s
ok  	github.com/agusarias/tmux-todo/internal/task	0.963s
ok  	github.com/agusarias/tmux-todo/internal/tui	6.400s

$ make lint      # go vet ./... + gofmt check — no output, exit 0
$ gofmt -l .     # empty
```

**Inside tmux** (private socket, so the developer's server is untouched) — the check that this
task did not re-introduce its parent's hang:

```
$ tmux -L tdo-wiring new-session -d -c <worktree> 'make test > intmux.log 2>&1; echo EXIT=$? >> …'
$ cat intmux.log
ok  	github.com/agusarias/tmux-todo/internal/cli	0.482s
ok  	github.com/agusarias/tmux-todo/internal/scope	1.455s
ok  	github.com/agusarias/tmux-todo/internal/store	(cached)
ok  	github.com/agusarias/tmux-todo/internal/task	(cached)
ok  	github.com/agusarias/tmux-todo/internal/tui	7.556s
EXIT=0
```

It completed rather than hanging. `store` and `task` came from the cache; `internal/cli` and
`internal/tui` — the two that could reach a terminal — both ran fresh.

### Two things found while doing it

1. **The `tui.Config.DB` is only open while the popup runs.** `runTUI` defers `closeDB`, so an
   assertion made after `Run` returns can only ever see `sql: database is closed`. The first
   version of the DB check failed for exactly that reason; the usability probe now happens
   inside the stub, which is the only moment the handle is live. Added to CLAUDE.md.
2. **The check closures deliberately do not call `t.Helper()`.** With it, every field failure
   was reported at the dispatch line (`wiring_test.go:342`) rather than at the assertion that
   fired — visible in the first mutation run, fixed in the second.

### Not done

`make test-plugin` was not run: it drives the TPM shell harness and this change is test-only
Go in `internal/cli`, which that harness does not touch.

**Merge commit:** `23c91ac` — `git show 23c91ac` / `git log -1 -p 23c91ac` for the diff.
Branch `assert-tui-config-wiring` merged into local `main` with `--no-ff` and deleted; the
worktree is removed. Not pushed.
