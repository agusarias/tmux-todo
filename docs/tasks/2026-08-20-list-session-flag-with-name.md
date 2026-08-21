# Query A Named Scope: `tdo list --session backend`

**Status:** in-progress
**Worktree:** ../todo-list-named-scope

## Goal
`tdo list --session <name>` and `tdo list --dir <path>` list the tasks filed under a *named*
scope rather than the current context's — and the same flags work on `tdo count`. Querying a
session that is not running, or a directory you are not in, is the point rather than an error.

## Why
Every existing read is anchored to *where you are*: the merged list, `--scope=session`, the
tier filters. So the tasks you most need to find are the ones you cannot get to — a session
that was renamed or killed, or another project's list you want to check without `cd`-ing there.
The all-tasks view (`g`) solves this inside the popup; nothing solves it from a shell, which is
where scripts, aliases and statuslines live.

It is also cheap: `store.Filter` already takes arbitrary `task.Scope` values, so the store and
the TUI need no change at all. This is a flag-parsing and normalisation task.

## Constraints
- `internal/cli` only. `internal/store`, `internal/tui` and `internal/scope` do not change —
  `scope.DirKey` already exists and is the normaliser this needs.
- **The new flags go through the shared `filter()` helper**, so `list` and `count` cannot
  disagree about what they mean. `cli-surface`'s DoD 7 made that helper exist for exactly this
  reason; adding a scope selector to one command only would break the property deliberately.
- **The `--json` shape does not change.** These are filters, not output. `testdata/list.json`
  must still match byte-for-byte.
- Exit codes stay uniform: 0 success, 1 runtime error, 2 usage error.
- Out of scope: `--session`/`--dir` on `add`, `done` or `rm` (filing or mutating by name is a
  different feature with different risks); any TUI change; globs or fuzzy matching on names;
  and a `--global` selector, which would be identical to `--scope=global`.

## Critical surface
None of the classic kinds — no migration, no auth, no network, no destructive write. Two things
still deserve care:

- **This reads by a key the user types**, and scope keys are durable database keys with pinned
  normalisation rules (absolute, cleaned, symlink-resolved, never case-folded, worktrees folded
  into the main repo). A `--dir` that normalises differently from how `add` filed the task
  returns an empty list and looks like data loss. The normalisation must be *the same function*,
  not a reimplementation.
- **`--session <name>` takes an open-ended value**, which is the parsing hazard below. Nothing
  validates a session name, so a mistake is silent rather than rejected.

## Definition of done

**`--session <name>`**
1. `tdo list --session backend` lists exactly the tasks whose scope is `session:backend`, in
   the normal order, and `tdo count --session backend` counts them. The name is used as the
   key **verbatim** — session keys are the tmux session name with no normalisation, which is
   what `internal/scope` already stores.
2. **It never consults tmux and never errors on an unavailable scope.** It works outside tmux,
   and a name that is not running — or does not exist at all — returns an empty result with
   **exit 0**. Ruling A (`--scope=session` exits 1 when the current session is unavailable)
   governs the *current* context; this flag never asks about it. Pinned by a test with `$TMUX`
   unset.
3. **A dash-leading value is a usage error (exit 2)**, naming the problem and the escape:
   `tdo list --session --json` must not set the name to `"--json"`, drop `--json`, and exit 0
   with an empty list. `--session=-json` is how a session genuinely called `-json` is passed.
   This mirrors `task-create-edit-rescope`'s ruling on dash-leading task text. **Both
   directions pinned by tests**, because the failure being prevented is silent.

**`--dir <path>`**
4. `tdo list --dir <path>` normalises the path through **`scope.DirKey`** — the same function
   `add` files with — so a subdirectory of a repo resolves to that repo's list, a worktree
   folds into its main repo, and `.`/relative/symlinked spellings all land on the same key.
   Pinned by a test that files a task with `add --dir` from one spelling and finds it from
   another.
5. **A path that no longer exists still queries.** `scope.DirKey` errors on it (`lstat: no such
   file or directory`), so `--dir` falls back to a cleaned, absolutised path and does **not**
   error — a deleted project's stranded list has to stay reachable, which is the same principle
   as DoD 2. **Documented limitation:** the fallback cannot symlink-resolve, so a key stored
   through a symlinked path (`/tmp` → `/private/tmp` on macOS) will not match; `--scope=all` is
   the reliable way to see stranded dir keys. Recorded in the usage text.
6. The same dash-leading rule as DoD 3 applies to `--dir`.

**Interaction**
7. `--session`, `--dir` and `--scope` are **mutually exclusive**; any two together is a usage
   error (exit 2) naming them. They are three spellings of one question.
8. They compose normally with `--all`, `--json` and `--pending`, and with `--db`. Flag order
   does not matter, including a selector after another flag — `parseArgs` already guarantees
   this and the tests should pin it for the new flags rather than assume it.
9. `tdo help` and both usage strings document the new flags, the mutual exclusion, the `=`
   escape for dash-leading values, and DoD 5's limitation.

**Tests and sweep**
10. Tests drive `cli.Run` with captured output against real databases in `t.TempDir()`,
    covering: a named session with tasks, one with none, one outside tmux, the dash-leading
    rejection and its `=` escape, the `--dir` normalisation round trip, a deleted `--dir`, and
    each mutual-exclusion pair.
11. **The JSON golden still matches byte-for-byte** — proof this task changed filtering only.
12. `make test`, `make test-plugin`, `make lint` clean; `gofmt -l .` empty; `CGO_ENABLED=0
    make build` still static. CI green on the push.

## Verification
- `go test ./internal/cli/ -v` naming the tests above.
- **A mutation proof for DoD 3**: with the dash guard removed, `tdo list --session --json`
  must be shown exiting 0 with an empty list and no JSON — the silent failure, demonstrated,
  since that is the whole reason the guard exists.
- A shell transcript against a throwaway `--db`: tasks seeded under two session names and two
  dir keys, then each selector shown returning exactly its own set, including one query from
  outside tmux and one for a name with no tasks.
- The `--dir` round trip shown with three spellings of one directory (`.`, an absolute path,
  and a path through a symlink) all returning the same tasks.

## Decisions

### 2026-08-20 (curator grill) — four rulings, and one call the curator made

**LEAD FINDING — `--scope` is safe by accident, and `--session` would not be.** Measured:
`tdo list --scope --json` swallows `--json` as the flag's *value* and fails only because
`--json` is not a valid scope name. The closed vocabulary is doing the work, not the parser.
A session name has no vocabulary — any string is legal — so `tdo list --session --json` would
set the name to `"--json"`, silently drop `--json`, and **exit 0 with an empty list**. That is
the same silent-wrong-scope class `tdo add` was fixed for, and it is why DoD 3 exists and is
mutation-proven rather than merely asserted.

1. **`--session <name>` as requested**, not `--scope=session:backend`. `--session` stays a
   *boolean* on `add` and becomes *value-taking* on `list`/`count`. One name with two meanings
   is a real inconsistency, and it is accepted deliberately: the two never appear in the same
   command's flag set, and the readability of `tdo list --session backend` is the point of the
   request. The compound-value alternative was the curator's suggestion and was declined.
2. **Named scopes never error and never consult tmux.** Ruling A exists so that an
   *unavailable current* scope cannot masquerade as an empty list; asking about a name is a
   question about stored rows, and a dead or renamed session is exactly the case worth asking
   about. So: no tmux call, works outside tmux, empty result exits 0.
3. **Dash-leading values are rejected, with `--session=-name` as the escape.** Mirrors the
   existing ruling for dash-leading task text, so the CLI stays consistent with itself.
4. **`--dir <path>` too, and both flags apply to `count`.** `count` shares `filter()` with
   `list` by design; giving one a selector the other lacks would break the property that
   helper exists to guarantee.

**Confirmed by probe, so the plan need not discover it.** `scope.DirKey(".")` returns the
repo root (`/Users/agusarias/workspace/todo`), `DirKey("/tmp/../tmp")` returns `/private/tmp`
(cleaned and symlink-resolved), and `DirKey("/definitely/not/here")` **errors**:
`resolve …: lstat …: no such file or directory`.

**CURATOR'S CALL, not asked — flagged at Checkpoint 1.** That error creates an asymmetry: a
dead *session* is queryable by name, but a deleted *directory* cannot be normalised. Ruled that
`--dir` falls back to a cleaned, absolutised path rather than erroring, because erroring would
make the flag useless for precisely the stranded case that motivates it. The fallback is exact
whenever no symlink was involved and wrong when one was — hence the documented limitation in
DoD 5 and the pointer to `--scope=all`. The alternative (error, and tell the user to use
`--scope=all`) is simpler and defensible; override cheaply if you prefer it.

## Plan
**Approved at Checkpoint 1 on 2026-08-20** as written, including the curator's call that
`--dir` falls back to a cleaned absolute path rather than erroring on a path that no longer
exists. The plan is disposable; Goal, Constraints and Definition of done are not — changing
those is a scope event, so set `blocked` with the question instead.

**Approach.** All in `internal/cli`, and the shape is set by the shared helper: `filter()`
currently takes `(scopeFlag string, includeDone bool)`. It grows into taking a small selector
value instead, built once per command from the three mutually-exclusive flags:

```go
// exactly one of these is set, or none for the active merged set
type selector struct {
    scope   string // --scope: session|dir|global|all
    session string // --session <name>
    dir      string // --dir <path>
}

func (e *env) filter(sel selector, includeDone bool) (store.Filter, error)
```

Both `list` and `count` build a `selector` from their own FlagSets and hand it to the one
`filter()`. That keeps DoD 7's mutual exclusion and the defaulting rule in a single place
rather than duplicated across two commands — which is the same argument that put `filter()`
there to begin with.

**Files.** `internal/cli/env.go` (the selector, `filter()`, the dash guard and the `--dir`
normalisation), `list.go` and `count.go` (two flags each, and building the selector),
`cli.go` (usage text). Tests alongside.

**Sequencing.**
1. **The dash guard and its mutation proof first.** It is the one piece whose absence is
   silent, and writing it first means every later test runs with it in place. Prove the
   unguarded behaviour (exit 0, empty list, `--json` dropped) before fixing it, so the evidence
   is the real failure rather than a description of it.
2. `selector` + `filter()` + the mutual-exclusion errors, with `list` and `count` both
   converted. The existing `--scope` tests must pass **unedited** — that is the guard that this
   refactor did not change what `--scope` means.
3. `--session <name>`, including the outside-tmux leg.
4. `--dir <path>`: the `scope.DirKey` round trip across three spellings, then the
   deleted-path fallback.
5. Usage text, the golden-file check, sweep.

**What could go wrong.**
- *The `filter()` signature change rippling.* It has two callers, so this is small — but the
  existing `--scope` tests passing unedited is the property to protect. If one needs changing,
  the refactor altered behaviour.
- *`--dir` normalising differently from `add`.* The only safe implementation is to call
  `scope.DirKey` — the same function — never to reimplement "absolute, cleaned,
  symlink-resolved". A reimplementation that agrees today will drift, and the failure looks
  like an empty list rather than an error.
- *`t.TempDir()` on macOS is itself under a symlink* (`/var` → `/private/var`), so a `--dir`
  test comparing against a raw temp path will fail for the wrong reason. CLAUDE.md records
  this; compare against the normalised form.
- *The dash guard rejecting a legitimate value.* A session named `-x` is unusual but legal, so
  the `=` escape must actually work and be tested, or the guard trades a silent bug for a
  hard block.
- *Forgetting `count`.* Easy to add the flags to `list` and stop, since that is what was asked
  for. DoD 4's whole point is that the shared helper makes this free; a test on `count` for
  each new flag is what proves it was not skipped.

## Evidence
(Added by the executor.)
