# Make The Tmux Regression Guard CI-Proof

**Status:** ready
**Worktree:** none

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
- `internal/scope` only. No production code change is expected — if one turns out to be
  needed, that is a scope event.

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
