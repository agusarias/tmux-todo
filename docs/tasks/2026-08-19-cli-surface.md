# CLI Surface

**Status:** agreed
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
`scope-resolution` must be `done` first: this task calls `scope.Resolve` for the active
scopes and `scope.StickyDefault` for the flagless `tdo add`. Neither exists yet. This brief
is therefore held at `agreed` with an approved plan rather than moved to `ready` — the queue
has no dependency field, and `agreed` already means "not eligible for execution". Flipping
it to `ready` once scope-resolution lands is a curator action.

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
   created task's id.
2. `tdo add --session` outside tmux **fails** (exit 1, message naming the reason) rather than
   silently choosing another scope — matches the scope-resolution decision.
3. `tdo list` with no `--scope` shows the active merged set (session + dir + global for the
   current context) in the popup's tier order: session, dir, global, newest first within
   each. `--scope=session|dir|global` narrows to one; `--scope=all` returns every task in the
   database including scope keys that are not currently active.
4. `tdo list` shows pending tasks only by default; `--all` includes completed ones. Plain
   output carries the id, a done marker, the scope, and the text, column-aligned.
5. `--json` emits exactly:
   `{"tasks":[{"id":1,"text":"…","done":false,"done_at":null,"scope":{"kind":"dir","key":"/p"},"created_at":"2026-08-20T10:00:00Z"}]}`
   — object wrapper, RFC3339 UTC timestamps, `done_at` null when pending, `scope` nested.
   An empty result is `{"tasks":[]}`, never `null`. A golden-file test pins this byte shape.
6. `tdo done <id>` completes and `tdo rm <id>` deletes. An unknown id exits 1 with a message
   naming the id, via `store.ErrNotFound` — never exit 0 on a no-op.
7. `tdo count` prints a bare integer and newline (total); `--pending` counts only pending.
   Honours `--scope` with the same defaulting as `list`.
8. Every command accepts `--db <path>`. Exit codes are uniform: 0 success, 1 runtime error,
   2 usage error — matching the existing `doctor` and dispatch behaviour.
9. Completed tasks are purged opportunistically: every invocation calls
   `store.PurgeDone` with a 24h cutoff **after** its own work, and a purge failure never
   fails the command. The cutoff *policy* still belongs to completed-task-lifecycle; this
   task only wires the call so CLI-only users are not left with rows that accumulate
   forever.
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
- **2026-08-20 (grill):** The CLI purges completed tasks opportunistically with the same 24h
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
- `internal/cli/env.go` — `env`, `openEnv`, `filter`, `addScope`, the opportunistic purge.
- `internal/cli/add.go`, `list.go`, `mutate.go` (`done`/`rm`), `count.go`.
- `internal/cli/json.go` — the wire types, kept separate so the contract is one readable file.
- Tests per file, plus `testdata/list.json` as the golden file.

**Sequencing.** (1) `env`/`openEnv` and the `--db` plumbing. (2) `json.go` wire types and the
golden test — the contract first, since it is the thing we cannot change later. (3) `list`,
then `count` reusing `filter()`. (4) `add` with sticky fallback. (5) `done`/`rm` with the
not-found path. (6) The purge call. (7) Usage string and verification sweep.

**What could go wrong.**
- *`scope-resolution` lands with a different API than the plan assumes.* The plan names
  `scope.Resolve` and `scope.StickyDefault`; if the shipped signatures differ, `env.go`
  absorbs it. That is a `how` change, no scope event.
- *The golden file makes cosmetic churn look like a break.* Accepted deliberately — that
  friction is the point for a published contract. The test's failure message should say so.
- *RFC3339 in local vs UTC.* Must be `.UTC()` before formatting, or the golden file passes
  only in this timezone. Test with `TZ=Asia/Tokyo` to prove it.
- *The opportunistic purge writes on every invocation*, including read-only ones. A no-op
  `DELETE` is cheap, but if the deferred statusline polling ever lands it would mean a write
  lock every few seconds; the note to leave behind is that gating it behind a cheap existence
  check is the fix, not removing it.
- *`--scope=all` plus the active-merged default are easy to conflate* in `filter()`: "no
  flag" and "all" are different, and an empty `store.Filter.Scopes` means *all* in the store's
  API, so the flagless case must populate `Scopes` explicitly. The likeliest bug in this task.
