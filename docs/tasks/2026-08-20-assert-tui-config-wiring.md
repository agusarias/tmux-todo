# Assert What `tui.Config` Is Actually Wired With

**Status:** draft
**Worktree:** none

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

OPEN: does this also want a guard on the reverse direction — that a field *added* to
`tui.Config` in future fails the test until `runTUI` populates it? A `reflect`-based
"every exported field is non-zero" check would do it and would have caught this class at the
moment `DefaultScope` was introduced, but it fights fields whose zero value is legitimate
(`Now` nil means `time.Now`, and `SetSticky` nil is documented as "do not remember"). Worth
a ruling at grill time.

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
   home dir, `Version` is `cli.Version`, and `Now` is either non-nil or documented as
   deliberately nil.
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
(Added at Checkpoint 1.)

## Evidence
(Added by the executor.)
