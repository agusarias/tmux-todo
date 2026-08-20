# Make The Tmux Regression Guard CI-Proof

**Status:** review
**Worktree:** none (merged and removed)

## Goal
`internal/scope`'s regression tests must fail against the tmux-blindness bug on a machine
with **no tmux server** — i.e. on CI — not only on a developer machine inside tmux.

## Why
Split out of `scope-resolution` at its (approved) second Checkpoint 2. That task fixed the
real defect: `scope.Resolve()` used to be `Resolver{}.Resolve()` with an empty `TmuxEnv`,
so session scope was unreachable in the shipped binary. Four tests now guard it — but
`TestNewResolverCarriesTmuxEnv` asserts `NewResolver().TmuxEnv == os.Getenv("TMUX")`, which
outside tmux is `"" == ""`.

Demonstrated during the Checkpoint 2 audit: a copy of the tree with `NewResolver()` reverted
to `return Resolver{}` passes **30/30 with 1 skip** under `env -u TMUX go test ./internal/scope/`.
The entire regression net for a bug that already shipped once is therefore invisible in the
one environment where it will actually run. The brief's Evidence claim that "the default
cannot regress behind a refactor" is an overclaim until this lands.

## Constraints
- No tmux server may be required. `NewResolver` only *reads* the environment, so a faked
  `$TMUX` is sufficient and needs no subprocess.
- Keep the existing live-tmux tests as a separate, skippable leg — they prove the real
  `display-message` call works, which a fake cannot. Do not delete `tmuxAlive()` gating.
- Must not touch the user's real state dir: any test reaching `StickyDefault` sets
  `StateDir` explicitly (`internal/scope/sticky.go:105-117` otherwise falls through to
  `$XDG_STATE_HOME` / `~/.local/state`).
- `internal/scope`, **plus one narrowly-scoped seam in `internal/cli`** — widened by the
  2026-08-20 scope event below, with the user's sign-off. The seam is `var runTUIProgram =
  tui.Run` (or equivalent) and its substitution in `TestTUIWiringSmoke`; nothing else in
  `internal/cli` changes. Asserting what `tui.Config` *contains* is explicitly **out of
  scope** and belongs to `2026-08-20-assert-tui-config-wiring.md`.

## Critical surface
None. Test-only change to a package whose production behaviour is already correct.

## Definition of done
1. `TestNewResolverCarriesTmuxEnv` uses `t.Setenv("TMUX", "/tmp/fake,1,0")` (or similar
   sentinel) and asserts `NewResolver().TmuxEnv` equals that sentinel — so the assertion
   has something to be wrong about regardless of the host environment.
2. A guard also covers the empty case explicitly: with `t.Setenv("TMUX", "")`,
   `NewResolver().TmuxEnv` is `""` and `Resolve()` yields no session scope. The two legs
   together pin both directions of the constructor's behaviour.
3. **The mutation proof runs outside tmux.** With `NewResolver()` reverted to
   `return Resolver{}`, `env -u TMUX go test ./internal/scope/` **fails**. Evidence records
   the actual failure output, and the count of passing-vs-failing tests before and after,
   so the vacuity is demonstrably gone rather than asserted.
4. The live-tmux legs still skip cleanly outside tmux and still pass inside it — the
   existing `tmuxAlive()` gate keeps working, verified both ways.
5. `make test` and `make lint` clean; `gofmt -l .` empty. Full suite green both inside
   tmux and under `env -u TMUX`.

## Verification
Run the suite three ways and record all three: inside tmux, under `env -u TMUX`, and under
`env -u TMUX` against a mutated copy (in a temp dir, never the worktree) with
`NewResolver()` reverted — the third **must** fail. That third run is the whole point of
the task; without it in Evidence this task has not been verified.

## Blocked
**RESOLVED 2026-08-20 by the curator — scope event, user signed off. Status back to `ready`.**

**Answer: option (a), split.** Fix the hang in this task; the coverage half becomes its own
brief. The Constraints above are widened to permit exactly the injection seam and nothing
more, and DoD 5 stands unchanged — the full suite must be green inside tmux, which is now
achievable.

**Why fix it here rather than in its own task.** The hang breaks `make test` — the command
CLAUDE.md documents — for every developer working inside tmux, which for a tmux plugin is
every developer. Leaving that in a queued task means the repo's documented test command stays
broken for as long as the queue takes. The fix is a five-line seam of the same shape as
`newResolver`, and this task is already the one whose subject is "the test suite lies about
the environment it runs in" — so it is thematically the right home, not just the convenient
one.

**Why the coverage half is split out.** The executor also spotted that nothing asserts what
`tui.Config` *contains*, so `DefaultScope` and `SetSticky` — added by
`task-create-edit-rescope` — are wired but unexercised. That is real coverage work with its
own DoD, and it is a textbook instance of CLAUDE.md's standing pitfall about production
wiring never being tested. Folding it in here would grow a brief triaged as a two-line test
change into something needing a rewritten DoD. It is now
`docs/tasks/2026-08-20-assert-tui-config-wiring.md` (`draft`).

**Curator verified the finding independently** before answering, rather than taking the
report at face value: reproduced on current `main` inside a private-socket tmux pane with
stdout redirected to a file. The popup rendered anyway — Bubble Tea opens `/dev/tty`
directly — and blocked until the 20s timeout panic. The captured frame carries the *new*
39-column footer, confirming it reproduces post-merge and is not an artifact of the tree the
executor tested. `TestTUIWiringSmoke`'s doc comment ("Bubble Tea needs a TTY and a test
process has none") is simply false inside tmux and should be corrected as part of the fix.

**Executor guidance for the resumed pass.** The finished `internal/scope` work is on branch
`tmux-regression-guard` at commit `aabfac5`, unmerged, with the worktree
`../todo-tmux-regression-guard` still in place — resume there rather than starting over. Add
the seam, re-run DoD 5's in-tmux leg (it will need a real tmux pane, per CLAUDE.md's recipe;
a private socket keeps it off the user's server), and record the in-tmux `make test`
completing as evidence — that run is the point of the fix.

### Original question, as raised by the executor

**DoD 5 cannot be met, for a reason that has nothing to do with this change, and the fix is
outside this task's Constraints.**

DoD 1–4 are done and verified (Evidence below), including the mutation proof that is the
whole point of the task. DoD 5 also asks for the **full suite green inside tmux**. It is not
green inside tmux — it **hangs forever**, and it did so before this task started.

### What I found

`internal/cli`'s `TestTUIWiringSmoke` is built on this premise, quoted from its own doc
comment:

> It cannot get as far as a rendered popup: Bubble Tea needs a TTY and a test process has
> none, so tui.Run fails at the last step.

That premise is false when `go test` runs inside a tmux pane. Bubble Tea opens `/dev/tty`
directly, so redirecting stdout does not hide the terminal from it — the popup **actually
starts**, renders, and blocks forever waiting for a keystroke that never comes. The frame is
visible in the timeout dump:

```
$ # inside a tmux pane, on cf328ba — before any of this session's work
$ go test ./internal/cli/ -run TestTUIWiringSmoke -timeout 25s
╭──────────────────────────────────────────────────────────────────────╮
│  tdo                                                                 │
│                                                                      │
│  no tasks yet                                                        │
│                                                                      │
│  1/2/3 filter · j/k move · space done · q quit · vdev                │
╰──────────────────────────────────────────────────────────────────────╯panic: test timed out after 25s
	running tests:
		TestTUIWiringSmoke (25s)
...
github.com/charmbracelet/bubbletea.(*Program).eventLoop(...)
FAIL	github.com/agusarias/tmux-todo/internal/cli	25.642s
```

Verified three ways:

- **Pre-existing.** Reproduced at `cf328ba`, the tree as it stood before this session's
  first task. Nothing in `tmux-integration-and-rename-hook` or `task-create-edit-rescope`
  caused it; `runTUI` reached `tui.Run` before those changes exactly as it does now.
- **Environment-specific.** Outside tmux the same test passes in 0.26s. Only `internal/cli`
  hangs; `scope`, `store` and `task` all pass inside tmux, and `internal/tui`'s own
  `TestRun*` legs (which drive a real Bubble Tea loop with a scripted input) pass inside
  tmux too.
- **It is the whole suite, not one test.** `go test ./...` prints package results in
  command-line order, so a hung `internal/cli` means `make test` never finishes. For a tmux
  plugin, "inside tmux" is where a developer always is — `make test` as documented in
  CLAUDE.md hangs for them today.

### The question

DoD 5 says "Full suite green both inside tmux and under `env -u TMUX`". I cannot produce
that without changing `internal/cli`, and this task's Constraints say **`internal/scope`
only** — so this is yours to decide:

- **(a) Fix it here.** Recommended, and cheap. The repo's established answer is an injection
  seam: `var runTUIProgram = tui.Run` in `internal/cli`, substituted by the test, so
  `TestTUIWiringSmoke` asserts the `tui.Config` was built and never starts a program. That
  is the same shape as `newResolver`, and it would close a second gap: nothing currently
  asserts the contents of that Config, so `DefaultScope` and `SetSticky` — added by
  `task-create-edit-rescope` — are wired but untested.
- **(b) Its own task.** Cleanest separation, and it is arguably a bigger finding than this
  task; the cost is that DoD 5 here has to be narrowed anyway in the meantime.
- **(c) Narrow DoD 5** to "`internal/scope` green both ways, full suite green under
  `env -u TMUX`", and leave the hang to (b).

I have not touched `internal/cli`. The branch `tmux-regression-guard` holds the finished
`internal/scope` work as commit **`aabfac5`** and is **not merged**, so whichever way this
goes, nothing has to be undone. Read it with `git show aabfac5`; the worktree
`../todo-tmux-regression-guard` is still in place, so the executor can resume there once
this is answered. The branch touches `internal/scope/scope_test.go` and nothing else, and
the brief lives only on `main`, so a later merge cannot conflict.


## Decisions
- **2026-08-20 (curator, split from `scope-resolution` Checkpoint 2):** approved
  `scope-resolution` rather than bouncing it a second time — its actual defect is fixed and
  verified two ways (in-tmux mutation test plus an end-to-end `capture-pane` showing the
  session tier render). The test-robustness gap is a two-line change and did not justify
  re-opening a twice-merged task, so it lands here.
- **2026-08-20 (curator):** the general lesson is now in CLAUDE.md — "agrees with the
  environment" is vacuous when the environment is empty. Fake the environment so the
  assertion can fail; keep the live check as a skippable leg.

- **2026-08-20 (curator, triage):** **Direct path**, quick-confirmed rather than grilled.
  All four triage axes are low: the brief was written with a full DoD and Verification
  section when it was split out of `scope-resolution`, so there is no ambiguity left to
  grill; it is test-only in one package; and it is trivially revertable. Running a full
  grill on it would be the ceremony-on-a-trivial-task anti-pattern.
- **2026-08-20 (curator, Checkpoint 1 APPROVED):** mini-plan below signed off as written.
  Status `ready`.

- **2026-08-20 (executor, resumed pass):** the branch was 10 commits behind `main` by the
  time the block was answered, so `main` was merged **into** the branch first (`220eced`)
  rather than the fix being written against a stale tree. That mattered more than
  bookkeeping: `all-tasks-view` had changed `tui.Run` to return `(tui.Jump, error)`, so a
  seam written against the old single-return signature would not have compiled — and the
  in-tmux hang had to be re-confirmed post-merge before being fixed, which it was (the
  timeout dump carries the new 39-column footer).
- **2026-08-20 (executor):** the seam's guard is the substituted program's **call count**
  inside `TestTUIWiringSmoke`, not a separate test. A second test asserting "runTUI goes
  through the seam" would have asserted exactly what the count already does. The count is
  what makes the fix self-guarding: a change that inlined `tui.Run` again fails it, and
  fails it **outside** tmux, so CI sees a regression that would otherwise present only as a
  hang — the one failure mode a test suite cannot report on itself.
- **2026-08-20 (executor):** the smoke test's assertions had to invert — exit 0 and empty
  stderr, where it previously wanted exit 1 and a non-empty stderr. That is not a weakened
  test but the removal of a false premise: it was asserting that the popup *failed*, and
  "the popup failed" was never the thing worth covering. Its doc comment, which the curator
  flagged as simply false, is rewritten to say what is actually true.
- **2026-08-20 (executor):** `TestTUIErrorFromTheProgramIsReported` added alongside, because
  the seam replaced the only path by which `tui.Run`'s error reached the exit code. Without
  it the substitution could swallow a failed queued delete and nothing would notice.

## Plan
**Approach:** two-line test change plus a third verification run. No production code moves —
`NewResolver` is already correct; only the test that guards it is vacuous.

**Files:** `internal/scope/scope_test.go` only.

**Steps:**
1. `TestNewResolverCarriesTmuxEnv` sets `t.Setenv("TMUX", "/tmp/fake,1,0")` and asserts
   `NewResolver().TmuxEnv` equals that sentinel — so the assertion has something to be
   wrong about on a tmux-less runner, instead of comparing `"" == ""`.
2. Add the opposite leg: with `t.Setenv("TMUX", "")`, assert `NewResolver().TmuxEnv` is `""`
   **and** that `Resolve()` yields no session scope. Both directions of the constructor's
   contract are then pinned, which is what stops a future refactor from satisfying one by
   breaking the other.
3. Leave the `tmuxAlive()`-gated live legs untouched. A faked `$TMUX` cannot prove the real
   `display-message` call works, so that leg stays as the separate, skippable proof.

**Verification — three runs, all three recorded in Evidence:**
- inside tmux → pass
- `env -u TMUX go test ./internal/scope/` → pass
- `env -u TMUX` against a **mutated copy in a temp dir** (never the worktree) with
  `NewResolver()` reverted to `return Resolver{}` → **must fail**

The third run is the entire point of the task. Today it passes 30/30 with 1 skip, which is
the vacuity being fixed; Evidence must record the before-and-after counts and the actual
failure output, or this task has not been verified.

**What could go wrong:**
- **`t.Setenv` and parallel tests are incompatible** — `t.Setenv` forbids `t.Parallel()` in
  the same test. If any of these tests are parallel, drop the parallelism rather than
  reaching for a manual setenv/restore.
- **Mutating the worktree by accident.** The third run must copy the tree to a temp dir; a
  mutation left in the worktree would be committed.
- **A test reaching `StickyDefault` without setting `StateDir`** falls through to the real
  `$XDG_STATE_HOME` (`sticky.go:105-117`) and would touch the user's own state. Already a
  Constraint; worth re-checking for the new leg since `Resolve()` is in play.

## Evidence

Worktree `../todo-tmux-regression-guard`, go1.26.6, tmux 3.7b, macOS arm64. Two commits:
`aabfac5` (the `internal/scope` guard, from the pass that ended blocked) and `38272d4` (the
`internal/cli` seam), with `main` merged in between at `220eced`. All in-tmux runs use a
private socket (`tmux -L`), so nothing touches the user's own server. All mutations run in a
throwaway **clone** under a scratch dir; the worktree was never mutated.

### DoD 3 — the run this task exists for

A real `git clone` of the branch, `NewResolver()` reverted to `return Resolver{}`, outside
tmux. Three runs, so the change is a measurement rather than a claim:

| tree | `env -u TMUX go test ./internal/scope/` | pass | fail | skip |
|---|---|---|---|---|
| **before** — old test + the bug | `ok ... 0.650s` | 48 | **0** | 1 |
| **after** — new test + the bug | `FAIL ... 0.645s` | 51 | **2** | 2 |
| control — new test, no bug | `ok ... 0.587s` | 53 | 0 | 2 |

The "before" row is the vacuity this task was filed for: the entire regression net for a bug
that already shipped once, green against that very bug on the only kind of machine CI has.
(The brief recorded 30/30; the count grew because `tmux-integration-and-rename-hook` and
`all-tasks-view` added tests to this package meanwhile. The vacuity was unchanged.)

The "after" failure:

```
--- FAIL: TestNewResolverCarriesTmuxEnv (0.00s)
    --- FAIL: TestNewResolverCarriesTmuxEnv/carries_a_set_TMUX (0.00s)
        scope_test.go:522: NewResolver().TmuxEnv = "", want the faked $TMUX
        "/tmp/fake-tmux-socket,1,0" — the constructor is not reading the environment,
        so session scope is unreachable in the shipped binary
FAIL	github.com/agusarias/tmux-todo/internal/scope	0.645s
```

*A false start worth recording:* the first mutated copy was a bare `git archive` extract, not
a repo, so `TestResolveAgainstRealEnvironment` also failed — on "Dir key … is the package
directory, not a repo root", nothing to do with the mutation. A mutation run whose extra
failures come from the harness cannot show what the mutation did, so the copy became a real
clone. Two failures in the table above, not three.

### DoD 1, 2 — both directions of the constructor

```
$ go test ./internal/scope/ -run TestNewResolverCarriesTmuxEnv -v
--- PASS: TestNewResolverCarriesTmuxEnv
    --- PASS: TestNewResolverCarriesTmuxEnv/carries_a_set_TMUX
    --- PASS: TestNewResolverCarriesTmuxEnv/carries_an_unset_TMUX
```

The set leg fakes `$TMUX` with a sentinel the host cannot coincidentally match, so the
assertion has something to be wrong about wherever it runs. The unset leg pins the other
direction — `TmuxEnv` empty **and** `Resolve()` yielding no session scope — because a
constructor that hardcoded a non-empty string would satisfy the first leg alone and the zero
value satisfies the second alone. Neither leg dials a socket; `NewResolver` only reads the
variable. Per the Constraints the unset leg reaches `Resolve()` but not `StickyDefault`, so
no state dir is touched, and no test in the file calls `t.Parallel()`.

### DoD 4 — the live legs, both ways

Inside tmux (`TMUX=/private/tmp/tmux-501/live48121,48126,0`), 55 pass / 0 fail / **0 skip**:

```
--- PASS: TestAgreesWithGitBinary
--- PASS: TestResolveAgainstRealEnvironment
--- PASS: TestPackageResolveMatchesTheRealEnvironment
--- PASS: TestNewResolverCarriesTmuxEnv
--- PASS: TestStickyDefaultReachesSessionInTheRealEnvironment
--- PASS: TestLiveSessionsAgainstARealServer
    scope_test.go:421: resolved in 5.280625ms (TMUX="/private/tmp/tmux-501/live48121,48126,0")
    scope_test.go:492: live tmux: Resolve() session = session=scope
```

Outside tmux, 53 pass / 0 fail / **2 skip** — the two live legs, skipping cleanly:

```
--- PASS: TestResolveAgainstRealEnvironment
--- PASS: TestPackageResolveMatchesTheRealEnvironment
--- PASS: TestNewResolverCarriesTmuxEnv
--- SKIP: TestStickyDefaultReachesSessionInTheRealEnvironment
--- SKIP: TestLiveSessionsAgainstARealServer
```

`tmuxAlive()` still gates in both directions, and the faked-`$TMUX` legs run either way.

### DoD 5 — the hang, and the suite green both ways

**The hang, re-confirmed on this tree** before fixing it, in a tmux pane. Note the footer:
this is the post-`all-tasks-view` frame, so it is not an artifact of the older tree the
previous pass tested.

```
$ go test ./internal/cli/ -run TestTUIWiringSmoke -timeout 20s   # inside tmux
╭──────────────────────────────────────────────────────────────────────╮
│  tdo                                                                 │
│  no tasks yet                                                        │
│  j/k move · space done · ? keys · q quit                             │
╰──────────────────────────────────────────────────────────────────────╯panic: test timed out after 20s
	running tests:
		TestTUIWiringSmoke (20s)
...
github.com/charmbracelet/bubbletea.(*Program).eventLoop(...)
FAIL	github.com/agusarias/tmux-todo/internal/cli	20.621s
```

**After the seam — `make test` completes inside tmux.** This run is the point of the fix:

```
$ echo $TMUX
/private/tmp/tmux-501/full47514,47519,0
$ make test
go test ./...
?   	github.com/agusarias/tmux-todo/cmd/tdo	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/cli    0.663s
ok  	github.com/agusarias/tmux-todo/internal/scope  1.058s
ok  	github.com/agusarias/tmux-todo/internal/store  (cached)
ok  	github.com/agusarias/tmux-todo/internal/task   (cached)
ok  	github.com/agusarias/tmux-todo/internal/tui    5.381s
EXIT=0
$ make lint
go vet ./...
LINT_EXIT=0
$ gofmt -l .
(empty)
```

Outside tmux:

```
$ env -u TMUX make test
go test ./...
ok  	github.com/agusarias/tmux-todo/internal/cli    0.569s
ok  	github.com/agusarias/tmux-todo/internal/scope  0.585s
ok  	github.com/agusarias/tmux-todo/internal/store  (cached)
ok  	github.com/agusarias/tmux-todo/internal/task   (cached)
ok  	github.com/agusarias/tmux-todo/internal/tui    (cached)
$ make lint && gofmt -l .
go vet ./...
(empty)
$ CGO_ENABLED=0 make build && otool -L bin/tdo
bin/tdo:
	/usr/lib/libSystem.B.dylib
	/usr/lib/libresolv.9.dylib
```

Still no libsqlite3.

### The seam's own guard discriminates

`runTUI` mutated back to calling `tui.Run` directly, in a throwaway copy, **outside** tmux —
which is the part that matters, because a hang is invisible to CI but this is not:

```
--- FAIL: TestTUIWiringSmoke (0.02s)
    cli_test.go:170: exit code 1, want 0: tdo: run tui: could not open a new TTY:
        open /dev/tty: device not configured
    cli_test.go:173: a clean run wrote to stderr: "tdo: run tui: could not open a new TTY..."
    cli_test.go:176: the substituted program ran 0 times, want 1 — runTUI is not going
        through runTUIProgram, so `go test` inside tmux starts a real popup and hangs
FAIL	github.com/agusarias/tmux-todo/internal/cli	0.240s
```

### Scope kept

`internal/cli` changed in exactly two places: `var runTUIProgram = tui.Run` plus its one call
site, and `cli_test.go`'s substitution. **What `tui.Config` contains is not asserted** — the
Constraints put that in `2026-08-20-assert-tui-config-wiring.md`, and it is still open there.
`git show 38272d4 --stat` is two files.

### Not proven here

- **That the suite can never hang again.** No test can assert the absence of a hang; the
  seam's call count is the closest available proxy, and the in-tmux `make test` above is the
  direct evidence for this tree. A future package that starts a program from a test would
  need its own seam.
- **CI itself.** There is no CI in this repo yet (`2026-08-20-release-binaries-and-ci.md`,
  `draft`). `env -u TMUX` is the stand-in for a runner with no tmux, which is the condition
  the whole task is about.

### Merge

Branch `tmux-regression-guard`, commits `aabfac5` and `38272d4` (with `main` merged in at
`220eced`), merged into local `main` and the worktree removed. Not pushed.

Merge commit: `260d8bc` (`git log -1 -p 260d8bc` for the diff; the worktree is gone).
