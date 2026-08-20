# Scope Resolution

**Status:** review
**Worktree:** /Users/agusarias/workspace/todo-scope-resolution

## Goal
Resolve the three independent scopes for the current context: `global`, `dir` (active
pane's cwd normalized to its git repo root, with git worktrees folding into the main repo,
literal path when not a repo), and `session` (tmux session name alone). Must degrade
gracefully outside tmux, where session scope is unavailable. Also owns the **sticky scope
default** — the persisted kind that `tdo add` and the popup's `a` row fall back to.

**Design:** docs/design.md — "Scope model"

## Why
The scope key is the partition key for every row in the database. If normalization is
wrong — a worktree keyed separately from its parent, a symlinked path keyed twice, an
empty-string session key — the fix is a data migration over the user's real tasks, not a
refactor. Every other task (CLI, merged popup, all-tasks view, rename hook) reads scopes
from this package, so its API shape decides how small those tasks can be. The package
boundary already exists as a doc-only stub for exactly this reason.

## Constraints
- Implements the existing `internal/scope` stub; does not restructure the package layout.
- Depends only on `internal/task` and the stdlib. No SQLite, no Bubble Tea, no new module
  requirements — in particular **no dependency on the `git` binary at runtime**.
- Never writes task rows. Scope resolution is read-only with respect to the store; the one
  thing it persists is the sticky default kind.
- Resolution must be injectable (tmux env, command runner, cwd, state dir) so tests never
  require a live tmux server or a git checkout.
- Adds no more than ~10ms to cold start; the 100ms popup budget is the product.
- Out of scope: the tmux `session-renamed` hook and scope-key rewriting (owned by
  tmux-integration-and-rename-hook), re-homing stale session scopes (owned by
  all-tasks-view-with-sesh-jump), and any store queries.

## Critical surface
**None** in the taskflow sense — no migrations, no auth, no prod data, no external side
effects, no network, no public API contract. The elevated risk is different in kind: the
strings this package produces become durable database keys, so a normalization change
after v1 ships is a migration. That risk is addressed by pinning normalization rules in
the Decisions log below and testing them directly.

## Definition of done
1. `Resolve()` returns a value carrying all three axes, with `Session` and `Dir` absent
   (nil) rather than empty-keyed when they cannot be determined; `Global` is always present
   with an empty key.
2. Dir path source: `tmux display-message -p '#{pane_current_path}'`, falling back to
   `os.Getwd()` when the tmux query fails or `$TMUX` is unset. A test covers both paths and
   the case where neither is available (Dir absent).
3. Session key is the tmux session name alone, from `#{session_name}`. Absent when `$TMUX`
   is unset or the query fails. Never an empty-string key.
4. Git root resolution is pure Go: walk up from the starting path for a `.git` entry. A
   `.git` **directory** means that dir is the root. A `.git` **file** is parsed for its
   `gitdir:` line and, when that path contains a `worktrees/` segment, the **main repo
   root** is derived from it — so `../todo-sqlite-store` resolves to `.../workspace/todo`.
5. A path with no `.git` anywhere up to the filesystem root is used **literally** as the
   dir key.
6. Keys are absolute, `filepath.Clean`ed, with symlinks resolved (`filepath.EvalSymlinks`),
   no trailing separator, and no case folding. Tested: two symlinked paths to one repo
   produce one key.
7. An agreement test proves the pure-Go walker matches the real `git` binary
   (`rev-parse --show-toplevel` / `--git-common-dir`) on a fixture repo plus a linked
   worktree, skipped via `exec.LookPath` when git is absent. The unit tests for the walker
   itself use hand-built `.git` fixtures and need no git.
8. Sticky default: read/write a single **kind** (never a key — the key is re-resolved at
   use time) in `$XDG_STATE_HOME/tmux-todo/`, falling back to `~/.local/state/tmux-todo/`.
   Writes are atomic (temp file + rename). A missing, empty, or unparseable file yields the
   fallback kind without an error, and a set→read round trip through a fresh process-level
   read returns the stored kind.
9. The sticky default degrades: if the stored kind is `session` but session scope is
   unavailable, the effective default is `dir`; if `dir` is also unavailable, `global`.
10. `go test ./...` and `go vet ./...` clean, `gofmt -l .` empty, and
    `CGO_ENABLED=0 make build` still produces a binary with no libsqlite3 in `otool -L`.
11. A benchmark or timed run shows resolution (including the tmux subprocess) inside the
    ~10ms budget, with the number recorded in Evidence.

## Verification
- `go test ./internal/scope/ -v` output pasted into Evidence, showing by name: the
  worktree-folding test, the symlink-identity test, the no-repo literal-path test, the
  outside-tmux absence test, and the sticky-default round trip and degradation tests.
- The git-agreement test (DoD 7) shown running, not skipped, on this machine.
- Timed resolution (DoD 11) with the actual measured number.
- A manual check from a real tmux pane in this repo's `../todo-*` worktree, showing the
  resolved dir key is the parent repo root and the session key is the session name.

## Decisions
- **2026-08-19 (grill):** Dir path comes from tmux (`#{pane_current_path}`) with an
  `os.Getwd()` fallback. Rejected cwd-only: `display-popup` is not guaranteed to start in
  the pane's path, and that bug would stay invisible until a user hit it. Rejected
  tmux-only: it would break `tdo add --dir` in a plain terminal, which the design requires.
- **2026-08-19 (grill):** Git root resolution is pure Go, not a `git` subprocess. Keeps
  `tdo` working where git is not installed and stays sub-millisecond; the cost is
  reimplementing a slice of git, which DoD 7's agreement test contains.
- **2026-08-19 (grill):** Outside tmux, session scope is **absent** in the API and
  `tdo add --session` **errors** rather than silently falling back. Rejected empty-key
  session scopes: `""` would become a real, un-nameable key in the user's database.
- **2026-08-19 (grill):** The sticky scope default is folded into **this** task rather than
  a new brief or the store's schema. The store task is in-progress, so adding a settings
  table would mean bouncing it to `blocked`; a small file in the XDG state dir keeps this
  self-contained. Consequence accepted: this package is no longer purely stateless.
- **2026-08-19 (Checkpoint 1, confirmed):** Symlinks are resolved, so
  `/tmp/x` and `/private/tmp/x` key identically; paths are **not** case-folded, since
  lowercasing would be wrong on Linux. Note for tests: `t.TempDir()` on macOS is itself
  under a symlink.
- **2026-08-19 (Checkpoint 1, confirmed):** With no sticky default ever set,
  the fallback kind is `session` when in tmux, else `dir`. Design does not specify one;
  confirmed explicitly at Checkpoint 1 over `dir`-always and `global`-always.

- **2026-08-20 (executor):** No wall-clock assertion in the timing test. The plan's DoD 11
  wanted the number recorded, and a `> 10ms` failure would fire on roughly one run in ten
  (measured tail: 12.07ms) — the scaffold task already set the precedent that a flaky timing
  threshold trains everyone to ignore the suite. The test logs the number and the budget is
  audited from Evidence; only a >1s result fails it, since that is a hang, not latency.
- **2026-08-20 (executor):** Measured the timing across 12 fresh processes rather than once.
  The first development sample read 14.7ms — over budget — and turned out to be a cold-page
  first `exec`, not the steady cold cost (median 5.7ms). One sample would have either failed
  the DoD wrongly or, taken warm, passed it wrongly.
- **2026-08-20 (executor):** The tmux subprocess is ~4.8ms of the ~5.7ms; the git walk is
  0.02–0.1ms. The plan's stated remedy if over budget (one `display-message` instead of two)
  was already implemented, so the *next* lever — noted for
  tmux-integration-and-rename-hook, not done here — is having tmux expand `#{session_name}`
  and `#{pane_current_path}` straight into the `display-popup` command line, removing the
  subprocess entirely. Deliberately not built now: it needs the keybind that task owns, and
  building the env-var half early would ship unused API on a guess.
- **2026-08-20 (executor):** Extended the agreement test with a real submodule leg beyond
  DoD 7's letter, because the plan listed submodule behaviour as an assumption to confirm.
  Git agrees a submodule is its own repository, so the rule stands as designed.
- **2026-08-20 (executor):** Added `Resolved.Has(kind)` alongside `Lookup` — the degradation
  logic needs an availability check and reading it off an error was worse at the call site.
- **2026-08-20 (executor, flag for the curator):** `CLAUDE.md` is now 98 lines against
  taskflow's 60-line aim, after 72 at the store task and 58 at the scaffold. My additions are
  compressed as far as they usefully go; the trend is structural, since each task legitimately
  contributes a hard-won pitfall. Worth a curator decision before the remaining six tasks land:
  either accept a longer file, or move per-package detail into the package doc comments (which
  already carry most of it) and keep CLAUDE.md to commands, layout and cross-cutting rules.
  Not restructuring it unilaterally — it is not this task's scope.

- **2026-08-20 (curator, Checkpoint 2 — REJECTED, fix forward):** `03d96c4` stays on `main`;
  this task returns to `ready` for a scoped follow-up. 10 of 11 DoD items are satisfied and
  the evidence is the most honest in the queue (it volunteers its own 14.7ms outlier and the
  12ms tail). Re-verified independently: 28 tests pass, `make lint` silent, `otool -L` shows
  no libsqlite3, `TestAgreesWithGitBinary` really runs rather than skipping, and
  `git show --stat 03d96c4` confirms **zero** `internal/store` files touched — the store is
  still environment-blind. The rejection is one code bug, not an evidence problem.
- **2026-08-20 (curator, Checkpoint 2 — DoD 3 FAILS IN PRODUCTION):** `Resolve()` can never
  see tmux. `scope.go:63` is `func Resolve() (Resolved, error) { return Resolver{}.Resolve() }`,
  and `Resolver{}` has `TmuxEnv == ""`, which is exactly what `queryTmux` short-circuits on.
  **No non-test code in the package reads `os.Getenv("TMUX")`** — the only `Getenv` is
  `XDG_STATE_HOME` in `sticky.go:109`. Verified from a live tmux pane with `$TMUX` set:

  ```
  package-level Resolve(): Session=<nil>  Dir=&{dir /Users/agusarias/workspace/todo}
  explicit TmuxEnv:        Session=&{session probe-chk}
  ```

  User-visible consequence: `tdo add --session` returns `ErrUnavailable` from inside tmux, and
  `StickyDefault` silently degrades `session` → `dir` for every user, because it keys off
  `rs.Session != nil` and `rs.Session` is always nil in production. Session scope — the
  product's main axis — is unreachable through the documented API.
- **2026-08-20 (curator):** This is the DoD-9 pattern from `sqlite-store-and-migrations`
  repeating in a new package, and it is worth naming as a recurring failure mode: **all 28
  tests pass because every one of them injects the thing production forgets.**
  `scope_test.go:230` and `:278` both construct `Resolver{TmuxEnv: os.Getenv("TMUX")}` — the
  test does the wiring the shipped entry point omits. The Evidence's live tmux check ran that
  same test binary, so it validated the injected path, not `Resolve()`. A test that injects a
  dependency cannot prove the production default is wired.

## Fix-forward scope (2026-08-20, curator)

Only these three items. Nothing else about the implementation is in question, and the Goal,
Constraints and DoD are unchanged — this is a `how` fix, not a scope event.

1. **Wire the real environment into `Resolve()`.** The package-level entry point must default
   `TmuxEnv` from `os.Getenv("TMUX")` (and `Run`/`Getwd` are already defaulted). Keep
   `Resolver` fully injectable for tests — the fix is about the *default*, not the seam.
   Correct the package doc comment, which currently claims the zero value talks to the real
   environment.
2. **Test the package-level entry point, not just the injected one.** At least one test must
   exercise `scope.Resolve()` itself and assert session presence tracks the real `$TMUX`.
   Without it this bug reappears the moment someone refactors the default.
3. **Decouple `TestResolveAgainstRealEnvironment` from bare `$TMUX`.** It currently fails on a
   machine where `$TMUX` is set but the server is gone:
   `TMUX="/tmp/fake,1,0" go test ./internal/scope/` →
   `scope_test.go:258: Session is absent despite $TMUX being set`, while
   `env -u TMUX go test ./internal/scope/` passes. Gate it on a live-server check
   (`tmux display-message` succeeding) rather than on `$TMUX` being non-empty, so it survives
   CI and SSH-into-a-detached-session. DoD 11's timing number comes only from this test, so
   keep the probe — just make its precondition honest.

## Decisions (continued)
- **2026-08-20 (curator, downstream API note):** `cli-surface`'s plan assumed a package-level
  `scope.StickyDefault`; it shipped as a **method** on `Resolver`, and the field is `TmuxEnv`
  rather than the plan's `TmuxSocket`. Both fall inside that brief's own pre-authorized
  absorption clause, so **no scope event** — but its `env` struct must hold a
  `scope.Resolver`, not just a `scope.Resolved`, since it needs the receiver for both
  `StickyDefault` and `SetStickyDefault`. Noted on that brief too.
- **2026-08-20 (curator, repo-wide):** CLAUDE.md length question answered, per the executor's
  own suggestion: **CLAUDE.md keeps commands, layout and cross-cutting rules; per-package
  specifics move into package doc comments** where the code is. Applies to the six remaining
  tasks, so nobody re-raises it per-task.

## Decisions (fix-forward)
- **2026-08-20 (executor):** The fix is a `NewResolver()` constructor that captures the
  environment, with `Resolve()` built on it — not a bare `os.Getenv` inside `Resolve`. The
  curator's item 1 only required the package-level entry point to be wired, but
  `StickyDefault` and `SetStickyDefault` are *methods*, so a caller needing the receiver had
  no correct way to build one and would have re-created the identical bug. The curator's own
  downstream note says `cli-surface`'s `env` struct must hold a `scope.Resolver`; that task
  now has `NewResolver()` to hold instead of `Resolver{}`.
- **2026-08-20 (executor):** The zero `Resolver` deliberately still does **not** read `$TMUX`.
  Making it do so would have been the other obvious fix and it is wrong: `TmuxEnv: ""` is
  load-bearing in the tests as "not inside tmux" (`scope_test.go:75,144`), so a fallback to
  the ambient environment would make those tests read whatever the developer's shell happens
  to be. The seam stays; only the default moved. The `Resolver` doc comment now states the
  asymmetry — `Run`/`Getwd` default to the real thing when nil, `TmuxEnv` cannot, because ""
  is meaningful there — which is the sentence whose absence caused the bug.
- **2026-08-20 (executor):** `TestStickyDefaultReachesSessionInTheRealEnvironment` sets
  `StateDir` to a `t.TempDir()`. The production sticky default lives in the user's real XDG
  state dir, and a test that writes `session` into it would silently change the user's
  configured default.

## Plan
Single package, `internal/scope`, replacing the doc-only stub. No other package changes —
the CLI and TUI wire-up belongs to their own tasks.

**Approach.** One exported entry point plus an injectable resolver behind it:

```go
type Resolver struct {          // zero value = real environment
    TmuxSocket string                                        // $TMUX; "" = not in tmux
    Run        func(string, ...string) ([]byte, error)       // defaults to exec
    Getwd      func() (string, error)
    StateDir   string                                        // "" = XDG lookup
}

type Resolved struct {
    Session *task.Scope   // nil outside tmux
    Dir     *task.Scope   // nil when no path is determinable
    Global  task.Scope    // always present, empty key
}

func Resolve() (Resolved, error)            // package-level convenience over Resolver
func (r Resolver) Resolve() (Resolved, error)
func (rs Resolved) Active() []task.Scope    // session, dir, global — merged-list tier order
func (rs Resolved) Lookup(task.ScopeKind) (task.Scope, error)

func (r Resolver) StickyDefault(rs Resolved) task.ScopeKind  // degrades per DoD 9
func (r Resolver) SetStickyDefault(task.ScopeKind) error
```

`Active()` returning tier order here is deliberate: the merged popup and `store.List` both
need session→dir→global, and putting it in one place stops three tasks from re-deriving it.

**Files.**
- `internal/scope/scope.go` — `Resolver`, `Resolved`, `Resolve`, `Active`, `Lookup`;
  replaces `doc.go`'s stub note with the real package doc.
- `internal/scope/git.go` — the walk-up root finder and `gitdir:`/`worktrees/` parsing.
- `internal/scope/sticky.go` — XDG state dir, atomic write, tolerant read.
- `internal/scope/scope_test.go`, `git_test.go`, `sticky_test.go` — plus the
  git-agreement test guarded by `exec.LookPath`.

**Sequencing.** (1) git walker against hand-built `.git` fixtures — the riskiest logic, and
testable with zero environment. (2) The git-agreement test, which is what makes step 1
trustworthy. (3) `Resolver`/`Resolved` with a fake `Run` for tmux. (4) Sticky default.
(5) Timing number. Each step lands with its tests before the next starts.

**What could go wrong.**
- *Submodules.* `.git` is a file pointing at `.git/modules/<name>`, which has no
  `worktrees/` segment, so the walker keeps the submodule's own dir as the root — the
  submodule gets its own list. That is defensible (it is a separate repo) and is what the
  agreement test will confirm or refute; if git disagrees, the test fails loudly rather
  than shipping a silent divergence.
- *Bare repos / detached `$GIT_DIR`.* Not a pane cwd anyone runs `tdo` from in practice;
  the walker falls through to the literal-path rule. Explicitly not handled.
- *EvalSymlinks on a deleted or unreadable cwd* returns an error; Dir goes absent rather
  than keying a task under a broken path.
- *tmux query latency* — one subprocess, measured in DoD 11. If it lands over budget, the
  fix is to read `#{session_name}` and `#{pane_current_path}` in a **single**
  `display-message` call, which the plan already does.

**Verification mapping.** DoD 1–3 → `scope_test.go` with fake `Run`/`Getwd`. DoD 4–6 →
`git_test.go` fixtures. DoD 7 → agreement test. DoD 8–9 → `sticky_test.go` against
`t.TempDir()`. DoD 10 → `make test`, `make lint`, `make build` + `otool -L`. DoD 11 →
benchmark output. Plus the manual tmux check from a `../todo-*` worktree.

## Evidence

Executed 2026-08-20 in worktree `/Users/agusarias/workspace/todo-scope`
(branch `scope-resolution`, rebased onto `main`, merged and removed).

**Merge commit: `03d96c4`** — fast-forward onto `main`, 7 files, `doc.go` replaced
by `scope.go` / `git.go` / `sticky.go` plus three test files. Not pushed. Review
with `git show 03d96c4`.

Rebased rather than merged with a commit: the curator landed the store task's
close-out on `main` mid-flight, and a fast-forward keeps `git show <hash>` honest.

### `go test ./internal/scope/ -v` — 28 tests, all passing

```
--- PASS: TestDirKeyFromGitDirectory                      --- PASS: TestResolveInsideTmux
--- PASS: TestDirKeyFoldsWorktreeIntoMainRepo   <- DoD 4   --- PASS: TestResolveOutsideTmuxHasNoSession   <- DoD 1,3
--- PASS: TestDirKeyHandlesRelativeGitdir                  --- PASS: TestResolveFallsBackWhenTmuxQueryFails <- DoD 2
--- PASS: TestDirKeySubmoduleKeepsItsOwnRoot               --- PASS: TestResolveEmptySessionNameStaysAbsent <- DoD 3
--- PASS: TestDirKeyIgnoresWorktreesSegmentOutsideGitDir   --- PASS: TestResolveWithNoPathAtAll           <- DoD 2
--- PASS: TestDirKeyWithoutRepoIsLiteral        <- DoD 5   --- PASS: TestResolveIgnoresUnresolvablePanePath
--- PASS: TestDirKeyIsSymlinkStable             <- DoD 6   --- PASS: TestActiveIsInTierOrder
--- PASS: TestDirKeyRejectsMissingPath                     --- PASS: TestLookupUnknownKind
--- PASS: TestDirKeyHasNoTrailingSeparator      <- DoD 6   --- PASS: TestResolveAgainstRealEnvironment    <- DoD 11
--- PASS: TestParseGitFileErrors                           --- PASS: TestStickyDefaultRoundTrip           <- DoD 8
--- PASS: TestAgreesWithGitBinary (0.33s)       <- DoD 7   --- PASS: TestStickyDefaultFallbackWithNothingStored
--- PASS: TestStickyDefaultDegrades             <- DoD 9   --- PASS: TestStickyDefaultToleratesBadFiles   <- DoD 8
    (6 subtests, one per degradation row)                  --- PASS: TestStickyDefaultMissingDirectoryIsNotAnError
--- PASS: TestSetStickyDefaultIsAtomic          <- DoD 8   --- PASS: TestSetStickyDefaultRejectsUnknownKind
--- PASS: TestStateDirHonoursXDG                           ok  internal/scope  0.537s
```

Every test the Verification section named by name is present and passing:
worktree-folding, symlink-identity, no-repo-literal-path, outside-tmux absence,
sticky round trip and sticky degradation.

### DoD 7 — the git agreement test ran, it did not skip

`TestAgreesWithGitBinary` took 0.33s, which is the `git init` / `commit` /
`worktree add` / `submodule add` fixture doing real work; a skip is 0.00s. It
builds a repo, a linked worktree and a submodule, then compares `DirKey` against
the real binary:

- inside the repo and a nested dir → `git rev-parse --show-toplevel`
- inside the linked worktree → `dirname` of `git rev-parse --git-common-dir`,
  and asserts we do **not** return the worktree's own `--show-toplevel`
- inside the submodule → `git rev-parse --show-toplevel`, and asserts we do
  **not** fold it into the parent

The submodule leg goes beyond DoD 7's letter on purpose: the plan flagged
submodule behaviour as an assumption to confirm or refute against real git. It is
confirmed — git also treats a submodule as its own repository, so "the submodule
gets its own list" is agreement, not divergence.

### Manual check from a real tmux pane (Verification, last bullet)

A fresh session named `scope-check`, started in the worktree:

```
$ tmux display-message -p -t scope-check '#{session_name} :: #{pane_current_path}'
scope-check :: /Users/agusarias/workspace/todo-scope

$ /tmp/scope.test -test.run TestResolveAgainstRealEnvironment -test.v
    resolved in 10.314334ms (TMUX="/private/tmp/tmux-501/default,37572,43")
      session: session=scope-check
      dir:     dir=/Users/agusarias/workspace/todo
--- PASS
```

tmux reports the pane inside `todo-scope`; resolution returns the **parent repo**
`/Users/agusarias/workspace/todo` and the session name alone. That is DoD 4's
headline case verified against the live repository rather than a fixture.

### DoD 11 — timing, and what it actually costs

Cold resolution, 12 fresh processes (first `Resolve` in each, so the subprocess is
included and nothing is warm):

```
4.86  4.88  4.99  5.02  5.11  5.25  6.20  6.31  6.35  8.26  9.08  12.07   (ms)
median 5.7ms   ·   11 of 12 under 10ms   ·   one outlier at 12.07ms
```

Steady state and the breakdown:

```
BenchmarkResolve-8   30   4.62–6.25 ms/op      (dominated by the tmux subprocess)
BenchmarkDirKey-8    30   0.021–0.107 ms/op    (the pure-Go walker)
tmux display-message, 20 runs: median 4.8ms, min 4.3, max 7.1
```

So the ~10ms budget is met at the median with ~4ms of headroom, and the honest
caveat is that the tail touches 12ms on a busy machine. Essentially all of it is
the one `tmux display-message`; the git walk is 0.02–0.1ms. The first measurement
taken during development read 14.7ms, which was a cold-page-cache first `exec` and
is not representative — it is recorded here because it is what prompted measuring
the distribution instead of trusting one sample.

### Whole repo

```
$ gofmt -l .            (no output)
$ go vet ./...          (no output)
$ go test ./... -count=3
?   	github.com/agusarias/tmux-todo/cmd/tdo	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/cli	0.561s
ok  	github.com/agusarias/tmux-todo/internal/scope	1.280s
ok  	github.com/agusarias/tmux-todo/internal/store	0.649s
ok  	github.com/agusarias/tmux-todo/internal/task	0.869s
ok  	github.com/agusarias/tmux-todo/internal/tui	0.697s

$ CGO_ENABLED=0 make build && otool -L bin/tdo
	/usr/lib/libSystem.B.dylib
	/usr/lib/libresolv.9.dylib          (7.1M, unchanged — no new deps)
```

`internal/scope` was the last package with no test files; only `cmd/tdo` remains,
and it is three lines delegating to `internal/cli`.

### Definition of done

| # | Item | Status |
|---|---|---|
| 1 | Three axes, absent rather than empty-keyed, Global always present | met |
| 2 | Pane path from tmux, `os.Getwd` fallback, absent when neither works | met — three tests |
| 3 | Session from `#{session_name}`, absent outside tmux, never empty | met |
| 4 | Pure-Go walker; `.git` dir, `.git` file, `worktrees/` → main repo | met — and verified live from a real worktree |
| 5 | No repo above → literal path | met |
| 6 | Absolute, cleaned, symlink-resolved, no trailing sep, no case folding | met — symlink-identity test |
| 7 | Agreement test vs the git binary, skipped only if git is absent | met — ran, plus a submodule leg |
| 8 | Sticky *kind* in XDG state dir, atomic write, tolerant read, round trip | met |
| 9 | Degradation session → dir → global | met — 6-row table test |
| 10 | `go test` / `go vet` / `gofmt` clean; no libsqlite3 | met |
| 11 | Timed resolution recorded, within ~10ms | met at the median (5.7ms); tail to 12ms disclosed above |

---

## Evidence (fix-forward, 2026-08-20)

Second execution, addressing only the three items in **Fix-forward scope**. The original
implementation commit `03d96c4` stays on `main` untouched; this is a follow-up on top.

Toolchain `go1.26.6 darwin/arm64`. Test count in `internal/scope`: **28 → 31**, zero skips.

### The bug is fixed, and the test that proves it fails without the fix

The whole point of this pass is that the previous 28 tests could not see the defect, so
each new guard was run against the broken code before being trusted.

**Restoring the exact shipped bug** (`func Resolve() ... { return Resolver{}.Resolve() }`),
from inside a live tmux session:

```
--- FAIL: TestPackageResolveMatchesTheRealEnvironment
    Resolve() session = <absent>, but Resolver{TmuxEnv: $TMUX} sees session=scopemut
      — the package entry point is not wired to the real environment
    Resolve() reports no session scope from inside a live tmux session
```

**Breaking the new default** (`NewResolver()` returning `Resolver{}`) fails all four
real-environment guards, including the user-visible consequence the curator named:

```
--- FAIL: TestResolveAgainstRealEnvironment
    Session is absent despite a reachable tmux server
--- FAIL: TestPackageResolveMatchesTheRealEnvironment
    Resolve() session = <absent>, but Resolver{TmuxEnv: $TMUX} sees session=scopemut2
--- FAIL: TestNewResolverCarriesTmuxEnv
    NewResolver().TmuxEnv = "", want $TMUX "/private/tmp/tmux-501/default,37572,65"
--- FAIL: TestStickyDefaultReachesSessionInTheRealEnvironment
    StickyDefault = "dir" inside tmux with session stored, want "session"
      — session scope is unreachable through the production path
```

With the fix in place, from inside tmux:

```
=== RUN   TestResolveAgainstRealEnvironment
    resolved in 5.641125ms (TMUX="/private/tmp/tmux-501/default,37572,63")
      session: session=scoperun
      dir:     dir=/Users/agusarias/workspace/todo
--- PASS: TestResolveAgainstRealEnvironment
=== RUN   TestPackageResolveMatchesTheRealEnvironment
    live tmux: Resolve() session = session=scoperun
--- PASS: TestPackageResolveMatchesTheRealEnvironment
--- PASS: TestNewResolverCarriesTmuxEnv
--- PASS: TestStickyDefaultReachesSessionInTheRealEnvironment
```

### Item 3 — the stale-`$TMUX` failure, before and after

The old gate keyed off `$TMUX` being non-empty. Reproducing the curator's exact command
against the old gate, and then against the new one:

```
# old gate
$ TMUX="/tmp/fake,1,0" go test ./internal/scope/ -run TestResolveAgainstRealEnvironment
    scope_test.go:275: Session is absent despite $TMUX being set
FAIL

# new gate (tmuxAlive: $TMUX set AND `tmux display-message` succeeds)
$ TMUX="/tmp/fake,1,0" go test ./internal/scope/
ok  	github.com/agusarias/tmux-todo/internal/scope	0.688s

$ env -u TMUX go test ./internal/scope/
ok  	github.com/agusarias/tmux-todo/internal/scope	0.659s
```

The timing probe DoD 11 depends on is kept — only its precondition changed.

### Full suite inside a live tmux session

31 tests, **0 skips**, including `TestAgreesWithGitBinary` actually running (DoD 7):

```
--- PASS: TestAgreesWithGitBinary (0.42s)
...
ok  	github.com/agusarias/tmux-todo/internal/scope	0.682s
EXIT=0
```

Whole repo: `make test` green across all packages, `make lint` (`go vet ./...`) silent,
`gofmt -l .` empty, and `CGO_ENABLED=0 make build` still links no libsqlite3:

```
bin/tdo:
	/usr/lib/libSystem.B.dylib
	/usr/lib/libresolv.9.dylib
```

### DoD 11 — timing re-measured

12 fresh processes inside tmux, following the precedent this task set of never trusting one
sample:

| n | min | median | max |
|---|---|---|---|
| 12 | 4.24 ms | **4.53 ms** | 9.07 ms |

Within the ~10ms budget. The 9.07ms max is the first `exec` of the run — the cold-page
outlier already documented in the original Evidence — and every subsequent sample sits
between 4.2 and 5.3ms. Adding `os.Getenv("TMUX")` cost nothing measurable.

### End-to-end: the product actually works now

The user-visible payoff, and the strongest evidence that the fix reaches production rather
than just the test binary. The real `bin/tdo` binary in a live tmux session, against a
seeded throwaway database (`$XDG_DATA_HOME`), showing the session tier present for the
first time:

```
╭──────────────────────────────────────────────────────────────────────────────╮
│  tdo                                                                         │
│                                                                              │
│  ▸ ⌘ rebase onto main           (session: tdolive)                           │
│    ⌘ check CI                                                                │
│    · fix auth redirect          (dir: ~/.claude/jobs/a9c0f4db/tmp/xdg/repo)  │
│    · write migration                                                         │
│    ◉ call the dentist           (global)                                     │
│                                                                              │
│  1/2/3 filter · j/k move · q quit · vacf815b-dirty                           │
╰──────────────────────────────────────────────────────────────────────────────╯
```

That is `docs/design.md`'s default-view mock, produced by the shipped binary. Before this
fix the same command rendered only the dir and global rows. `1` now filters to the session
tier and `1` again returns to the merged list, both confirmed in the same pane.

### Fix-forward Definition of done

| Item | Status |
|---|---|
| 1 — `Resolve()` wired to the real environment; doc comment corrected | met — `NewResolver()`, and the `Resolver` comment now names the `TmuxEnv` asymmetry |
| 2 — a test exercising `scope.Resolve()` itself | met — `TestPackageResolveMatchesTheRealEnvironment`, plus `TestNewResolverCarriesTmuxEnv` and the `StickyDefault` guard; all three shown failing against the bug above |
| 3 — `TestResolveAgainstRealEnvironment` gated on a live server | met — `tmuxAlive()`, with the before/after commands above |

DoD 3 (session key from `#{session_name}`, the item the curator marked as failing in
production) is now met **through the documented API**, not only through an injected
resolver. Items 1–2 and 4–11 were already met and are unchanged by this pass.

### Not verified

- Behaviour when `$TMUX` is set and the server is reachable but `display-message` returns
  an unexpected format — still the existing "any failure yields empty strings" path,
  unchanged here.
- The `display-popup` overlay itself, which needs an attached client (CLAUDE.md). The
  end-to-end check above runs the TUI in a plain tmux pane instead.

