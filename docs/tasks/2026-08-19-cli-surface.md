# CLI Surface

**Status:** ready
**Worktree:** none

## Goal
Build the non-interactive surface over the shared core: `tdo add` with scope flags,
`tdo list` with `--scope` and `--json`, `tdo done`, `tdo rm`, `tdo count --pending`.
Scope flags fall back to the same sticky default the TUI uses.

**Design:** docs/design.md — "CLI surface"

## Why
Two reasons beyond "the design lists these commands". First, `--json` is the tool's only
scripting contract — statusline counts, shell aliases and anything a user wires up later
parse it, so its shape is a promise we keep. Second, this is the first consumer that
exercises store + scope together end to end, so it is where a wrong assumption in either
package surfaces cheaply, before the TUI's event loop is in the way.

## Blocked on
**RESOLVED 2026-08-20.** `scope-resolution` landed its fix-forward as merge `d69dd4d`:
`scope.Resolve()` now goes through `NewResolver()`, which reads `os.Getenv("TMUX")`
(`internal/scope/scope.go:64-69`), so session scope reaches the real binary. Verified by
curator with an end-to-end tmux capture — a session-scoped task renders with its tier
label. `scope.StickyDefault` also exists (`internal/scope/sticky.go:26`). This brief is
unparked to `ready`.

**Inherited hazard — read before writing `openEnv`.** `Resolver`'s *zero value* is
deliberately still tmux-blind: `Resolver{}` has `TmuxEnv == ""`, which `queryTmux` treats
as "not inside tmux" and short-circuits. `scope_test.go:341-345` pins that on purpose.
This task is the first new caller, and writing `scope.Resolver{}` instead of
`scope.NewResolver()` reintroduces the exact bug that got scope-resolution rejected.
Nothing structural prevents it — only the doc comment at `scope.go:27-36`. Always
construct via `NewResolver()`, or take a `Resolver` that came from it.

## Constraints
- Extends `internal/cli`: stdlib `flag` with manual dispatch, no cobra (docs/design.md).
- All five commands go through `internal/store` and `internal/scope`. No SQL in `internal/cli`
  and no re-deriving scope keys locally.
- `--db <path>` works on every command, as `doctor` already does, so tests drive the real
  binary against a throwaway database.
- Cold start stays under the ~100ms budget; `tdo count` is the one plausibly hot path.
- Out of scope: the TUI (`internal/tui` stays a placeholder), the tmux keybind and hook,
  `tdo purge` as its own command, batch ids (`tdo done 1 2 3` — the design says `<id>`),
  shell completions, and colour/TTY detection.

## Critical surface
**`--json` output is a published API contract.** Once a user's script parses it, changing a
field name or a timestamp format breaks them silently. Nothing else here qualifies: no
migrations (the store owns those), no auth, no network, no external side effects. One
adjacent risk to keep in view: `tdo done` and `tdo rm` mutate the user's real database from
a one-line command, so id handling and the not-found path must be exact.

## Definition of done
1. `tdo add "text" [--session|--dir|--global]` inserts one task. With no scope flag it uses
   `scope.StickyDefault`; more than one scope flag is a usage error (exit 2). Prints the
   created task's id. **Exactly one positional argument** — zero or two-or-more is a usage
   error (exit 2), per Ruling B. **The scope flag is honoured whether it precedes or follows
   the text**, since `design.md:107` writes it after; a flag that stdlib `flag` would have
   dropped into `fs.Args()` must not be silently ignored. Both orders are pinned by tests.
2. `tdo add --session` outside tmux **fails** (exit 1, message naming the reason) rather than
   silently choosing another scope — matches the scope-resolution decision.
3. `tdo list` with no `--scope` shows the active merged set (session + dir + global for the
   current context) in the popup's tier order: session, dir, global, newest first within
   each. `--scope=session|dir|global` narrows to one; `--scope=all` returns every task in the
   database including scope keys that are not currently active. `--scope=session` outside
   tmux (or any scope that resolves unavailable) **exits 1** with the `ErrUnavailable`
   reason, per Ruling A — never an empty list with exit 0. An unrecognised `--scope` value
   is a usage error (exit 2).
4. `tdo list` shows pending tasks only by default; `--all` includes completed ones. Plain
   output carries the id, a done marker, the scope, and the text, column-aligned.
5. `--json` emits exactly:
   `{"tasks":[{"id":1,"text":"…","done":false,"done_at":null,"scope":{"kind":"dir","key":"/p"},"created_at":"2026-08-20T10:00:00Z"}]}`
   — object wrapper, RFC3339 UTC timestamps, `done_at` null when pending, `scope` nested.
   An empty result is `{"tasks":[]}`, never `null`. A golden-file test pins this byte shape.
6. `tdo done <id>` completes and `tdo rm <id>` deletes. An unknown id exits 1 with a message
   naming the id, via `store.ErrNotFound` — never exit 0 on a no-op.
7. `tdo count` prints a bare integer and newline (total); `--pending` counts only pending.
   Honours `--scope` with the same defaulting **and the same Ruling A failure** as `list` —
   the two share one `filter()` helper rather than duplicating the logic.
8. Every command accepts `--db <path>`. Exit codes are uniform: 0 success, 1 runtime error,
   2 usage error — matching the existing `doctor` and dispatch behaviour.
9. ~~Completed tasks are purged opportunistically.~~ **STRUCK** by the 2026-08-20 scope
   event (see Decisions) — retention belongs entirely to `completed-task-lifecycle`, and
   `design.md:79-81` keeps the done row in the database anyway. **This task wires no purge
   call.** `store.PurgeDone` stays an uncalled store primitive.
10. Tests drive `cli.Run` with captured stdout/stderr against real databases in
    `t.TempDir()`, asserting exit codes, the JSON golden file, and tier ordering. Outside-tmux
    behaviour is tested by unsetting `$TMUX`.
11. `go test ./...`, `go vet ./...` clean, `gofmt -l .` empty, `CGO_ENABLED=0 make build`
    still static (no libsqlite3 in `otool -L`).
12. `tdo help` and the usage string list all five new commands with their flags.

## Verification
- `go test ./internal/cli/ -v` output in Evidence, naming the JSON golden test, the
  tier-ordering test, the unknown-id exit-code test and the outside-tmux `--session` test.
- A real end-to-end shell transcript against a throwaway `--db`: add three tasks in
  different scopes, `list`, `list --json | jq`, `done`, `rm`, `count --pending`, with the
  actual output pasted — not paraphrased.
- `tdo add --session` run with `$TMUX` unset, showing the non-zero exit and the message.
- Timed `tdo count` on a 500-row database, confirming the budget still holds.

## Decisions
- **2026-08-20 (grill):** `--json` is an **object wrapper** (`{"tasks":[…]}`) with **RFC3339
  UTC** timestamps and a nested `scope` object. Rejected a bare array: it can never gain a
  sibling field, so any later metadata (counts, schema version) would be a breaking change.
  Rejected unix seconds: unambiguous but unreadable in `jq` output. Rejected NDJSON: streaming
  buys nothing at a few hundred rows.
- **2026-08-20 (grill):** Flagless `tdo list` shows the **active merged set**, not everything,
  so `tdo list` and the popup answer the same question and the CLI works as a debugging tool
  for the TUI. `--scope=all` is the escape hatch.
- **2026-08-20 (grill):** The CLI **reads** the sticky default but never **writes** it — only
  the TUI's `Enter` sets it. Rejected symmetric behaviour: a cron job or shell alias running
  `tdo add --global` would silently change what the next popup add defaults to, which is
  action at a distance the user cannot see.
- **2026-08-20 (grill):** ~~SUPERSEDED by the scope event below — see DoD 9.~~ The CLI purges completed tasks opportunistically with the same 24h
  cutoff the popup uses. The design's trigger ("popup close or after 24h") leaves a
  CLI-only user's done rows accumulating forever; this closes that gap without moving the
  policy out of completed-task-lifecycle.
- **2026-08-20 (curator):** Plain (non-JSON) output is human-facing and explicitly **not** a
  compatibility promise; `--json` is the contract. Stated so a later formatting change is not
  mistaken for a breaking one.
- **2026-08-20 (curator):** No colour and no TTY detection in v1. Scriptability first, and the
  popup is where presentation effort belongs.

- **2026-08-20 (curator):** `scope-resolution` has landed on `main` (`03d96c4`) but was
  **rejected at Checkpoint 2** and is back at `ready` for a one-line tmux-wiring fix, so this
  task stays parked at `agreed`. Two API facts to absorb when it unparks, both inside this
  brief's own pre-authorized drift clause: there is **no package-level `scope.StickyDefault`**
  (it is a method on `Resolver`), and the field is `TmuxEnv`, not `TmuxSocket`. Consequence
  for the plan: `env` must hold a `scope.Resolver`, not just a `scope.Resolved` — it needs the
  receiver for both `StickyDefault` and `SetStickyDefault`. Also note `Resolved.Has` was added
  post-plan and `Resolved.Active()` is what feeds `store.Filter.Scopes`.
- **2026-08-20 (curator, SCOPE EVENT — user signed off):** **DoD 9 is struck.** It required
  every invocation to call `store.PurgeDone` with a 24h cutoff. `completed-task-lifecycle`
  resolved its own self-contradictory brief as **hide-only: done rows are bounded in the view
  and never deleted**, so an automatic 24h delete here would destroy rows before the view rule
  ever mattered. `PurgeDone` keeps no caller; it stays a store primitive for a possible
  explicit `tdo purge`. Do not reintroduce a purge call in this task.
### 2026-08-20 (curator, Checkpoint 1 — approved, unparked to `ready`)

**Ruling A — reads of an unavailable scope fail like writes do.** `tdo list --scope=session`
and `tdo count --scope=session` outside tmux **exit 1** with the `scope.ErrUnavailable`
message, exactly as `add` does (DoD 2). Rejected the alternative (empty list, exit 0):
"absent beats empty" is already this repo's rule, and an empty list is indistinguishable
from a scope that genuinely has no tasks. Applies to `--scope=dir` too if `Dir` is ever nil.

**Ruling B — `tdo add` takes exactly one positional.** `tdo add fix flaky test` is a usage
error (**exit 2**), not a silent join. `design.md:107` shows the text quoted. Rejected the
join because it weakens Refinement 1 below: if stray positionals are always an error, then
a mistyped flag (`-sesion`) is caught instead of being absorbed into the task text.

### 2026-08-20 (curator) — six refinements the shipped code forces

The plan's shape is unchanged; these are corrections found by reading the code as it now
stands, not new scope.

1. **The `add` argument-order trap — the highest-value bug this task can prevent.**
   `design.md:107` is `tdo add "fix flaky test" [--session|--dir|--global]` — text *first*.
   stdlib `flag` **stops parsing at the first non-flag argument**, so a trailing `--global`
   never reaches the FlagSet: it lands in `fs.Args()` and is silently ignored, filing the
   task under the sticky default and **exiting 0**. A wrong-scope task with no error is the
   worst failure this CLI has available. Honour the design's order — hoist leading
   positionals past the flags before `Parse` — and pin **both** orders with tests. With
   Ruling B, any remaining positional after that is exit 2, which also catches typo'd flags.
2. **`{"tasks":[]}` needs an explicitly non-nil slice.** `store.query` returns a nil slice
   on no rows (`internal/store/tasks.go:188`), and a nil slice marshals to `null` — a direct
   DoD 5 violation. Build the wire slice with `make([]wireTask, 0, len(tasks))`.
3. **Golden-file timestamps need two layers**, because `db.now` is unexported
   (`internal/store/store.go:39`) so `cli` tests cannot freeze the clock. (a) A unit test
   calling the unexported marshal function over hand-built `task.Task` values — byte-exact
   and timezone-proof, run under `TZ=Asia/Tokyo`. (b) An end-to-end test driving `Run`
   against a real DB whose `created_at` was pinned with a raw `UPDATE` (`store.DB` embeds
   `*sql.DB`), compared against the same golden file.
4. **A resolution seam in `internal/cli`.** `runTUI` calls `scope.Resolve()` directly, which
   makes tests depend on the test process's cwd and a live tmux server. DoD 10 needs
   determinism, so `openEnv` should resolve through a package-level
   `var resolveScopes = scope.Resolve` that tests replace. `t.Setenv("TMUX", "")` covers the
   outside-tmux legs and `t.Setenv("XDG_STATE_HOME", t.TempDir())` controls the sticky
   default — never let a test touch the user's real state dir.
5. **`env` carries the `Resolver`, not just `Resolved`** — `StickyDefault` is a method on it
   (`internal/scope/sticky.go:26`). Construct it with `scope.NewResolver()`, never
   `scope.Resolver{}` (see the hazard note under "Blocked on"). The CLI reads the sticky
   default and never writes it (already decided).
6. **`filter()` must keep "no flag" and `--scope=all` distinct.** No flag →
   `Scopes: resolved.Active()` populated *explicitly*; `--scope=all` → leave `Scopes` nil,
   since the store reads empty as "every scope" (`internal/store/tasks.go:29-34`);
   `--scope=<kind>` → `resolved.Lookup(kind)`, surfacing `ErrUnavailable` per Ruling A;
   anything else → exit 2. Shared verbatim by `list` and `count` (DoD 3 + 7).

**Free from work already shipped** (do not rebuild): tier ordering is in `store.List`'s SQL
(`internal/store/tasks.go:19-23`), so DoD 3's ordering needs only a test. `store.ErrNotFound`
already arrives wrapped with the id and verb — `"delete task 7: task not found"`
(`internal/store/tasks.go:204-216`) — so DoD 6 is nearly free. The `--db` flag and the
0/1/2 exit-code convention already exist in `runDoctor` and are the template to copy
(`internal/cli/cli.go:118-135`, `:52-54`).

**Sequencing** (revised; old step 6, the purge, is struck): (1) `env`/`openEnv`/`--db`;
(2) `json.go` + golden test; (3) `list`, then `count` on the same `filter()`; (4) `add`,
including the arg-order handling and the sticky fallback; (5) `done`/`rm` via
`errors.Is(err, store.ErrNotFound)`; (6) usage string + the DoD 11 verification sweep.

## Plan
All work in `internal/cli`; no new packages, no changes to `store` or `scope`.

**Approach.** The existing `Run` switch gains five cases. Each command is one function with
its own `flag.FlagSet`, and three pieces of shared machinery underneath so the commands stay
thin and consistent:

```go
// resolved once per invocation, threaded into each command
type env struct {
    db    *store.DB
    scope scope.Resolved
    out   io.Writer
    err   io.Writer
}

func openEnv(dbFlag string, out, err io.Writer) (*env, func(), error)  // --db + Open + Resolve
func (e *env) filter(scopeFlag string, includeDone bool) (store.Filter, error)  // shared by list and count
func (e *env) addScope(session, dir, global bool) (task.Scope, error)  // flags -> scope, sticky fallback
```

`filter()` existing once is the point: `list` and `count` must agree on what `--scope`
means, and DoD 3 and 7 are otherwise two implementations of the same defaulting rule.

**Files.**
- `internal/cli/cli.go` — dispatch cases, usage string.
- `internal/cli/env.go` — `env`, `openEnv`, `filter`, `addScope`. (No purge — DoD 9 struck.)
- `internal/cli/add.go`, `list.go`, `mutate.go` (`done`/`rm`), `count.go`.
- `internal/cli/json.go` — the wire types, kept separate so the contract is one readable file.
- Tests per file, plus `testdata/list.json` as the golden file.

**Sequencing.** (1) `env`/`openEnv` and the `--db` plumbing. (2) `json.go` wire types and the
golden test — the contract first, since it is the thing we cannot change later. (3) `list`,
then `count` reusing `filter()`. (4) `add` with sticky fallback. (5) `done`/`rm` with the
not-found path. (6) ~~The purge call.~~ struck. (7) Usage string and verification sweep.

**What could go wrong.**
- *`scope-resolution` lands with a different API than the plan assumes.* The plan names
  `scope.Resolve` and `scope.StickyDefault`; if the shipped signatures differ, `env.go`
  absorbs it. That is a `how` change, no scope event.
- *The golden file makes cosmetic churn look like a break.* Accepted deliberately — that
  friction is the point for a published contract. The test's failure message should say so.
- *RFC3339 in local vs UTC.* Must be `.UTC()` before formatting, or the golden file passes
  only in this timezone. Test with `TZ=Asia/Tokyo` to prove it.
- *`--scope=all` plus the active-merged default are easy to conflate* in `filter()`: "no
  flag" and "all" are different, and an empty `store.Filter.Scopes` means *all* in the store's
  API, so the flagless case must populate `Scopes` explicitly. The likeliest bug in this task.
