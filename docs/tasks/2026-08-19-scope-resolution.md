# Scope Resolution

**Status:** agreed
**Worktree:** none

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
- **2026-08-19 (curator, assumption — flag if wrong):** Symlinks are resolved, so
  `/tmp/x` and `/private/tmp/x` key identically; paths are **not** case-folded, since
  lowercasing would be wrong on Linux. Note for tests: `t.TempDir()` on macOS is itself
  under a symlink.
- **2026-08-19 (curator, assumption — flag if wrong):** With no sticky default ever set,
  the fallback kind is `session` when in tmux, else `dir`. Design does not specify one.

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
