# Query A Named Scope: `tdo list --session backend`

**Status:** review
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

## Decisions (executor, 2026-08-21)

**The curator's call on `--dir` is kept as ruled** — a path that no longer exists falls back to
a cleaned absolute path rather than erroring. Nothing found while implementing it argues the
other way, and the alternative would make the flag useless for the stranded case that motivates
it. What was added on top: the documented limitation is **asserted**, not just written down
(`TestListDirOfADeletedSymlinkedPathIsTheDocumentedGap`). A known gap with no test is a silent
regression waiting for someone to "improve" the fallback.

**The dash guard lives in `splitArgs`, and so covers every value-taking flag.** The plan put it
in `internal/cli/env.go` without saying where; the hazard is a property of "this flag consumes
the next token" — exactly the question `takesValue` already answers — not of `--session` and
`--dir` specifically. So `--db --json` and `--scope --json` are now rejected too. That is a
behaviour change beyond the DoD's letter, and it is the *narrower* fix that would have been odd:
`--scope --json` previously failed with `unknown scope "--json"`, which is the right outcome for
the wrong reason and would have started diverging from `--session`'s message. Every existing
`--scope` test passes **unedited** (they all use the `=` form), which is the plan's own guard
that the `filter()` refactor did not change what `--scope` means.

**`selector`'s fields are pointers, and `newSelector` reads them through `fs.Visit`.** The first
implementation used plain strings and treated `""` as "not given" — which made `--session=`
silently fall through to the active merged set: a query about somewhere else, answered with
*here*. `fs.String`'s pointer cannot distinguish absent from present-and-empty; only `fs.Visit`
can. Caught by `TestEmptySelectorValueIsAUsageError`, which was written before the bug and
failed on the first run.

**An empty selector value is a usage error.** Not in the DoD explicitly, but it is the same
silent-wrong-answer family as DoD 3, and "absent beats empty" is the rule every other scope key
in this repo follows. Exit 2, naming the flag.

**Sequencing followed the plan: the unguarded behaviour was measured first.** The flags went in
without the dash guard, the silent failure was captured as real output, and only then was the
guard added. The evidence below is therefore the actual failure rather than a description of it,
and the same mutation is repeated against the finished code.

## Evidence

Verified in `../todo-list-named-scope` on go 1.26 (`/opt/homebrew/bin/go`), darwin/arm64.
Merged into local `main` as **MERGE_HASH** (`git show MERGE_HASH`); the feature commit is
**FEATURE_HASH**.

### The lead finding, re-measured before any change

```
$ tdo list --db X --scope --json
tdo: unknown scope "--json" (want session, dir, global or all)
rc=2
```

`--json` *was* swallowed as the value; only the closed vocabulary produced the error. Confirms
the curator's finding, and that the parser was doing none of the work.

### DoD 3 + 6 — the silent failure, measured with the flags in but the guard out

```
=== UNGUARDED (no dash guard) ===
$ tdo list --db X --session --json
rc=0  <-- exit 0, empty list, --json silently dropped

$ tdo list --db X --dir --json
rc=0

$ tdo count --db X --session --pending
0
rc=0
```

Exit 0, no output, no JSON. `count` printing a bare `0` is the worst of the three: a tmux
statusline would show zero tasks forever.

With the guard, both directions:

```
$ tdo list --session --json
tdo: --session needs a value, but the next argument is another flag (--json). If "--json" really is the value, write --session=--json
rc=2
$ tdo list --dir --json          -> same shape, rc=2
$ tdo count --session --pending  -> same shape, rc=2
$ tdo list --scope --json        -> same shape, rc=2   (was "unknown scope")
$ tdo list --session=-json --json
{"tasks":[]}
rc=0                             <-- the escape works AND --json survived
```

### DoD 3 mutation proof against the finished code

Guard removed (`looksLikeFlag(args[i+1])` check deleted), rebuilt:

```
--- FAIL: TestDashLeadingSelectorValueIsAUsageError
    --- FAIL: .../list_--session_--json
        exit code 0, want 2 (stdout "", stderr "")
        stderr = "", want it to contain "needs a value"
        stderr = "", want it to contain "="
    --- FAIL: .../list_--dir_--json          .../count_--session_--pending
    --- FAIL: .../count_--dir_--pending      .../list_--session_--all
    --- FAIL: .../list_--scope_--json        .../list_--db_--json
```

Seven legs, one per value-taking flag and command pair. Guard restored, all pass.

### DoD 1, 2, 4, 5, 7, 8, 9, 10 — the test suite

`go test ./internal/cli/ -run 'NamedSelectors|SessionByName|SessionName|DashLeading|ListDir|Selectors|EmptySelector|UsageDocuments' -v`:

```
--- PASS: TestListSessionByNameSelectsExactlyThatSession        (DoD 1, list AND count)
--- PASS: TestNamedSelectorsNeverConsultTmux                    (DoD 2)
    --- PASS: /session_with_tasks  /session_with_none  /dir_with_tasks
--- PASS: TestListSessionByNameWithNoTasksIsAnEmptySuccess      (DoD 2)
--- PASS: TestListSessionNameIsUsedVerbatim                     (DoD 1)
    --- PASS: /Backend  /weird:name  /it's_a_"session"  /_leading-space  /trailing-slash/
--- PASS: TestDashLeadingSelectorValueIsAUsageError              (DoD 3, 6 — 7 legs)
--- PASS: TestDashLeadingSelectorValueIsPassedWithEquals         (DoD 3 — the escape)
--- PASS: TestListDirNormalisesLikeAddDoes                       (DoD 4 — 4 spellings)
--- PASS: TestListDirAcceptsARelativeSpelling                    (DoD 4)
--- PASS: TestListDirOfADeletedDirectoryStillQueries             (DoD 5)
--- PASS: TestListDirOfADeletedSymlinkedPathIsTheDocumentedGap   (DoD 5 — the limitation)
--- PASS: TestSelectorsAreMutuallyExclusive                      (DoD 7 — 6 pairs/triples)
--- PASS: TestEmptySelectorValueIsAUsageError
--- PASS: TestNamedSelectorsComposeInAnyOrder                    (DoD 8 — 4 orders + count)
--- PASS: TestUsageDocumentsTheNamedSelectors                    (DoD 9)
ok  github.com/agusarias/tmux-todo/internal/cli
```

`TestNamedSelectorsNeverConsultTmux` installs a context with **no session and no working
directory**, so `Lookup` would fail for both session and dir scope. A named selector that
touched the current context could not pass it.

`TestListDirOfADeletedSymlinkedPathIsTheDocumentedGap` **ran** (it did not skip): `t.TempDir()`
is under `/var -> /private/var` here, so the gap is real and pinned, along with the
`--scope=all` escape the usage text promises.

### DoD 1, 4, 5, 7 — the shell transcript

Two session names, two dir keys (one a git repo with the task filed from a deep subdirectory),
one global, all in one throwaway `--db`:

```
$ tdo list --db X --scope=all                (outside tmux)
4 [ ] session:backend    backend: review PR
3 [ ] session:frontend   frontend: fix nav
2 [ ] session:backend    backend: ship it
6 [ ] dir:.../proj-b     proj-b task
5 [ ] dir:.../proj-a     proj-a task          <- filed from proj-a/src/deep, folded to the root
1 [ ] global             a global note

$ tdo list --db X --session backend           ($TMUX unset)
4 [ ] session:backend  backend: review PR
2 [ ] session:backend  backend: ship it
rc=0
$ tdo list --db X --session frontend
3 [ ] session:frontend  frontend: fix nav
$ tdo list --db X --session no-such-session
rc=0                                          <- empty, exit 0
$ tdo count --db X --session backend          -> 2
$ tdo count --db X --session backend --pending -> 2
```

The `--dir` round trip, five spellings of one directory — the task was filed from
`proj-a/src/deep`:

```
--dir proj-a                    -> proj-a task
--dir proj-a/src/deep           -> proj-a task
--dir link-a                    -> proj-a task     (a symlink to proj-a)
--dir proj-a/src/deep/../..     -> proj-a task
--dir proj-a/./src/deep         -> proj-a task
--dir . (cwd = proj-a/src/deep) -> proj-a task
--dir proj-b                    -> proj-b task     (its own key, not folded in)
--dir proj-a/src/../..          -> <empty>         (correct: that is proj-a's PARENT)
```

That last line is worth keeping: it is a different key, so an empty answer is right. A transcript
line labelled loosely made it look like a bug for a moment.

A deleted directory, DoD 5:

```
proj-c deleted from disk; querying it anyway:
7 [ ] dir:.../xscript/proj-c  proj-c task
rc=0
```

Mutual exclusion, DoD 7:

```
$ tdo list --scope=all --session backend
tdo: --scope and --session are mutually exclusive: they are three spellings of one question
rc=2
$ tdo list --session backend --dir /tmp
tdo: --session and --dir are mutually exclusive: they are three spellings of one question
rc=2
```

### DoD 11 — the JSON golden is untouched

```
--- PASS: TestJSONGoldenBytes
--- PASS: TestJSONEmptyResultIsAnEmptyArray
--- PASS: TestListJSONMatchesGolden

$ git diff --stat internal/cli/testdata/
(no output — the golden file was not modified)
```

### DoD 12 — sweep

```
$ make lint          -> go vet ./...           (clean)
$ make test
?   .../cmd/tdo   [no test files]
ok  .../internal/cli     1.260s
ok  .../internal/scope   1.187s
ok  .../internal/store   0.890s
ok  .../internal/task    0.589s
ok  .../internal/tui     6.508s
$ gofmt -l .         -> (no output)
$ CGO_ENABLED=0 make build && otool -L bin/tdo
bin/tdo:
    /usr/lib/libSystem.B.dylib
    /usr/lib/libresolv.9.dylib                 <- no libsqlite3
$ make test-plugin   -> plugin harness: 140 passed, 0 failed
```

**"CI green on the push" is not done and cannot be by the executor** — pushing is the user's
call via the curator. Everything CI runs (`make lint`, `make test`, `make test-plugin`) is green
locally; the cross-compile matrix is untouched by this change, which is `internal/cli` only.

### Definition of done

1. **`--session <name>` selects exactly that session, on list and count** — done.
   `TestListSessionByNameSelectsExactlyThatSession` asserts both, and that the other scopes do
   not leak. Verbatim keys pinned across five awkward names.
2. **Never consults tmux, never errors, empty is exit 0** — done.
   `TestNamedSelectorsNeverConsultTmux` (with a context where the current session and dir would
   both fail to resolve) and `TestListSessionByNameWithNoTasksIsAnEmptySuccess`.
3. **Dash-leading value is exit 2, `=` is the escape** — done, both directions, and
   mutation-proven with the unguarded output recorded.
4. **`--dir` normalises through `scope.DirKey`** — done. Round trip filed by `add --dir` from a
   deep subdirectory and found from six spellings including a symlink and a relative one.
5. **A deleted path still queries** — done, with the symlink limitation asserted and documented
   in the usage text.
6. **The dash rule applies to `--dir`** — done; the guard is flag-agnostic, so it also covers
   `--scope` and `--db`.
7. **Mutually exclusive** — done. Six combinations, on both commands, each naming the flags
   that clashed.
8. **Composes with `--all`, `--json`, `--pending`, `--db`, any order** — done, four orders on
   list and three on count, including count's opposite `--pending` polarity.
9. **Usage text documents all of it** — done, asserted by `TestUsageDocumentsTheNamedSelectors`
   rather than only written.
10. **Tests drive `cli.Run` against real databases in `t.TempDir()`** — done, every case above.
11. **The JSON golden still matches byte-for-byte** — done, and the file is unmodified.
12. **Suites, gofmt, static build** — done. **CI on the push is outstanding**, and belongs to
    the curator: the executor does not push.
