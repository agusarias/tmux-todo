# SQLite Store And Migrations

**Status:** ready
**Worktree:** none

## Goal
Implement the SQLite store at `~/.local/share/tmux-todo/tasks.db` with the v1 `tasks`
schema, a `schema_version` table, and versioned migrations from day one so later fields
(priority, tags, due dates) are an ALTER rather than a rewrite. Handle concurrent access
from multiple open popups.

**Design:** docs/design.md — "Storage"

## Why
This is the shared core every other task consumes. The CLI, the merged popup list, the
all-tasks view and the completed-task lifecycle are all presentation over these queries —
if the API is right, those tasks stay small and can be built in any order. Getting the
schema or the timestamp representation wrong here means a migration plus four call-site
rewrites later.

## Constraints
- Extends the scaffold's `internal/store`; do not restructure the package layout.
- `CGO_ENABLED=0` must keep working — `modernc.org/sqlite` only, no cgo driver.
- The v1 column set is exactly what docs/design.md specifies. Priority, due dates, tags,
  notes and subtasks are explicitly **out of scope** — the schema must merely tolerate
  adding them later.
- Scope *resolution* (pane cwd -> git root, tmux session name) belongs to the
  scope-resolution task. This task accepts `task.Scope` values and never inspects the
  environment.
- No TUI and no CLI wiring here. Those tasks consume this API.
- Cold start budget stands: opening the DB and listing must not push `tdo` past ~100ms.

## Critical surface
**Migrations** — this task establishes the migration runner and v1 schema that all later
schema changes build on, against a database holding the user's real data. A broken runner
or a non-idempotent migration corrupts a live store. No auth, no prod data, no external
side effects, no network.

## Definition of done
1. `migrations/001_init.sql` embedded via `go:embed`, creating
   `tasks(id, text, done, done_at, scope_kind, scope_key, created_at)` per docs/design.md.
2. A migration runner applies every migration above the recorded version, each in a single
   transaction, in filename order; a failed migration rolls back and leaves the recorded
   version untouched.
3. Running the runner twice is a no-op — proven by a test that opens, migrates, closes,
   reopens and asserts the version and row count are unchanged.
4. `schema_version` holds exactly one row, enforced structurally, so the scaffold's
   check-then-insert race cannot produce duplicates (see Decisions).
5. Pragmas applied on every `Open`: `journal_mode=WAL`, `busy_timeout=5000`,
   `foreign_keys=ON`. A test asserts WAL is actually in effect after open.
6. Store API, all methods taking `context.Context` and domain types from `internal/task`:
   `Add`, `List`, `Get`, `Complete`, `Uncomplete`, `UpdateText`, `Rescope`, `Delete`,
   `Count`, `PurgeDone`.
7. `List` takes a set of scopes and returns tasks ordered scope tier (session, dir, global)
   then newest first, matching the merged popup's order. It also supports "all scopes,
   grouped" for the all-tasks view, including scope keys that are not currently active.
8. Timestamps stored as INTEGER unix seconds; round-tripping a `time.Time` through
   `Add` -> `List` preserves the second and the `DoneAt` nil/non-nil distinction.
9. Concurrency proven, not asserted: a test opens two independent `*DB` handles against one
   file and interleaves writes and reads without `SQLITE_BUSY`.
10. `go test ./...`, `go vet ./...` clean, `gofmt -l .` empty; `CGO_ENABLED=0 make build`
    still yields a binary with no libsqlite3 in `otool -L`.
11. `tdo doctor` reports the migrated schema version (1, not 0) and still exits 0.
12. Tests use real SQLite files under `t.TempDir()` — no mocks over the driver.
13. **Concurrent *first* open is safe** (added at Checkpoint 2, 2026-08-20). Two processes
    opening the same brand-new database must both succeed: neither `SQLITE_BUSY` from the
    `schema_version` inspection nor `table tasks already exists` from a doubly-applied
    migration. Proven by a test that runs the real migration path from N concurrent
    goroutines against one fresh file, and by the two-process binary check in Verification.
    DoD 9 does not cover this — it exercises a database that is already migrated.

## Verification
- `go test ./...` with the store package's own output shown, including the idempotent-reopen
  test (DoD 3) and the two-handle concurrency test (DoD 9).
- `sqlite3 <tmpdb> '.schema'` and `PRAGMA journal_mode;` output pasted into Evidence, from a
  database the binary created — not from a test fixture.
- `bin/tdo doctor` output showing `schema 1`.
- A deliberate failing-migration test proving rollback: version stays at the prior value and
  the partial DDL is absent.
- **Concurrent first open (DoD 13):** 25 iterations of two `tdo doctor` processes racing on
  one fresh `XDG_DATA_HOME`, plus a 3-way run, all exiting 0. The curator's reproduction
  command and its before-numbers are in the Checkpoint 2 decision below; re-run it and paste
  the after-numbers.

## Decisions
- **2026-08-19 (grill):** This task ships the **full CRUD API**, not just schema and
  migrations. Alternative was leaving queries to each consumer, which scatters SQL across
  four tasks and duplicates it. Making the core complete is what keeps the CLI and TUI tasks
  small and order-independent.
- **2026-08-19 (grill):** Migrations are `go:embed`-ed `.sql` files (`001_init.sql`, ...)
  applied by a small hand-rolled runner. No goose/golang-migrate — a dependency and a second
  CLI surface for a tool that will apply a handful of migrations in its life. SQL stays
  readable and diffable rather than buried in Go string literals.
- **2026-08-19 (grill):** Timestamps are **INTEGER unix seconds**. Sortable, timezone-free,
  trivial `time.Time` round-trip. Accepted cost: raw `sqlite3` output shows integers; use
  `datetime(created_at,'unixepoch')` when eyeballing. Milliseconds rejected — the `id`
  tiebreak already gives a stable newest-first order within a second.
- **2026-08-19 (grill):** `journal_mode=WAL` with `busy_timeout=5000`, so readers never
  block the writer across multiple open popups. Accepted cost: `tasks.db-wal` and
  `tasks.db-shm` sidecars in the XDG data dir. `synchronous` stays at the default — the
  faster NORMAL setting trades real durability for a write latency nobody will perceive at
  this scale.
- **2026-08-19 (curator, from scaffold Checkpoint 2):** The scaffold's `ensureSchemaVersion`
  has a benign check-then-insert race — two processes opening a fresh database can both read
  `count(*) = 0` and both INSERT. It self-heals today (`Version()` reads `LIMIT 1`,
  `SetVersion` updates all rows) but the table shape is wrong. Replace it with a structurally
  single-row table, e.g. `schema_version(id INTEGER PRIMARY KEY CHECK (id = 1), version
  INTEGER NOT NULL)` seeded with `INSERT OR IGNORE`. Existing databases are all version 0
  and disposable, so a clean redefinition is fine — no data migration needed for this fix.
- **2026-08-19 (curator, from scaffold Checkpoint 2):** WAL and `busy_timeout` were
  deliberately deferred from the scaffold to this task; they are DoD 5 here.
- **2026-08-19 (curator):** Done rows are never hard-deleted by normal use. `PurgeDone`
  exists for the completed-task-lifecycle task's 24h rule and takes an explicit cutoff, so
  the retention policy lives with the task that owns those semantics, not in the store.
  docs/design.md keeps done rows so history and stats stay possible later.

- **2026-08-19 (executor):** `DB.now` is an unexported clock field rather than every
  mutating method taking a `time.Time`. Tests freeze it; the four consumer tasks get
  `Add(ctx, text, scope)` and `Complete(ctx, id)` instead of threading `time.Now()` through
  every call site. `PurgeDone` still takes an explicit cutoff, per the curator decision.
- **2026-08-19 (executor):** Added `ListGrouped` returning `[]Group` alongside `List`. DoD 7
  asks for "all scopes, grouped" and grouping in SQL-adjacent code beats making the
  all-tasks-view task re-derive group boundaries from a flat slice. Scopes with no matching
  tasks are absent rather than empty.
- **2026-08-19 (executor):** Two CHECK constraints beyond the design's column list: a global
  task must have an empty `scope_key` (and non-global must not), and `done` must agree with
  `done_at`. These are consistency guards on the agreed columns, not new fields; `validScope`
  turns them into readable Go errors before SQLite raises them.
- **2026-08-19 (executor):** `SetVersion` (scaffold, public) is gone — version writes now
  belong to the migration runner alone, inside the migration transaction. `Version` gained a
  `context.Context`. Only `doctor` consumed either.
- **2026-08-19 (executor):** The legacy id-less `schema_version` table is dropped and
  recreated only when it reports version 0; above that, `Open` fails with "refusing to
  rewrite it" rather than destroying a database this code does not understand. The curator
  decision authorised discarding version-0 databases, not arbitrary ones.
- **2026-08-19 (executor):** `doctor` grew `journal`, `tasks N pending, M total` and a
  `schema X (latest Y)` line, and exits 1 if the recorded version is behind the embedded
  migrations. Same rationale as the scaffold's `doctor`: the shipped binary should exercise
  the query path, not just link it.
- **2026-08-19 (executor):** CLAUDE.md is now 72 lines, above taskflow's 60-line aim. The
  overflow is the driver-behaviour pitfalls (DSN pragmas, partial multi-statement Exec, WAL
  sidecars) — each cost real debugging time here and would cost it again. Flagging rather
  than silently trimming; the curator may cut it.
- **2026-08-20 (Checkpoint 2, fix forward):** The merge `fda2791` was audited and kept on
  `main` — schema, runner, CRUD and evidence all held up — but it was bounced back to
  `ready` for one defect the DoD did not cover: **concurrent first open fails**. Two
  `tdo doctor` processes against one fresh `XDG_DATA_HOME` failed **11 of 25** runs with
  `inspect schema_version: database is locked (5) (SQLITE_BUSY)`, and a 3-way run also hit
  `migration 001_init: SQL logic error: table tasks already exists (1)`. The same test
  against an already-migrated database failed **0 of 25**, so the window is strictly first
  creation — the first popup after install, and the first opens after any release that adds
  `002_*.sql`. Reverting was rejected: the implementation is otherwise sound and the fix is
  small. Reproduce with:

  ```sh
  for i in $(seq 1 25); do rm -rf /tmp/race$i
    XDG_DATA_HOME=/tmp/race$i ./bin/tdo doctor >/dev/null 2>&1 & p1=$!
    XDG_DATA_HOME=/tmp/race$i ./bin/tdo doctor >/dev/null 2>&1 & p2=$!
    wait $p1; r1=$?; wait $p2; r2=$?
    [ $r1 -ne 0 ] || [ $r2 -ne 0 ] && echo "run $i: $r1/$r2"
  done
  ```

  Two independent causes, both in `migrate.go`/`store.go`; the fix is the executor's call,
  but the diagnosis is: (1) `applyMigrations` reads the current version *outside* the
  transaction, so both processes decide to apply 001 and the loser's DDL collides — the
  version read and the apply loop want to be in one `BEGIN IMMEDIATE` transaction, with the
  version re-checked inside it so the loser sees it already current and skips; (2) the DSN
  sets `journal_mode` before `busy_timeout`, so the lock wait is not yet in force while the
  database is being created — hence `SQLITE_BUSY` rather than a 5s wait. Ordering
  `busy_timeout` first is the suspected fix, to be confirmed by the DoD 13 test.
- **2026-08-20 (Checkpoint 2):** Everything else in the merge was accepted as-is,
  explicitly including the two extra CHECK constraints in `001_init.sql` (global tasks
  carry no scope key; `done` and `done_at` agree). They are consistency guards over the
  design's v1 column set, not new fields, so they do not contradict docs/design.md.

## Plan
Approved at Checkpoint 1, 2026-08-19.

**Approach:** replace the scaffold's placeholder `schema_version` bookkeeping with a real
migration runner, then build the full query surface on top. `internal/store` keeps its
current shape — this widens the package, it does not restructure it.

**Sequencing**
1. `internal/store/migrations/001_init.sql`, embedded with `go:embed`. Creates the v1
   `tasks` table plus indices on `(scope_kind, scope_key, done)` and `created_at`.
2. Redefine `schema_version` as a structurally single-row table
   (`id INTEGER PRIMARY KEY CHECK (id = 1)`), seeded with `INSERT OR IGNORE`. Removes the
   scaffold's check-then-insert race. Existing version-0 databases are disposable.
3. `migrate.go` — the runner: read the current version, apply every higher migration in
   filename order, each inside one transaction that also bumps the version, so a failure
   rolls back DDL and version together.
4. Pragmas applied in `Open`, per connection, before migrating: `journal_mode=WAL`,
   `busy_timeout=5000`, `foreign_keys=ON`. Read `journal_mode` back with `QueryRow`.
5. `tasks.go` — `Add`, `Get`, `List`, `Count`, `Complete`, `Uncomplete`, `UpdateText`,
   `Rescope`, `Delete`, `PurgeDone`. Every method takes `context.Context` and speaks
   `internal/task` domain types.
6. Ordering: `ORDER BY <tier CASE on scope_kind>, created_at DESC, id DESC` — one SQL
   ordering serving both the merged popup list and the grouped all-tasks view.
7. Tests: field round-trip, idempotent re-migration, failing-migration rollback, two-handle
   concurrency, tier ordering, `DoneAt` nil vs set. Real files under `t.TempDir()`.
8. Verification sweep; confirm `tdo doctor` reports `schema 1`.

**Files:** new `internal/store/migrate.go`, `internal/store/tasks.go`,
`internal/store/migrations/001_init.sql`, `internal/store/tasks_test.go`,
`internal/store/migrate_test.go`; modified `internal/store/store.go`,
`internal/store/store_test.go`, `internal/cli/cli.go` (doctor line), `CLAUDE.md`.

**What could go wrong**
- The rollback test needs a migration that fails midway. SQLite is transactional for DDL so
  this works, but the test must assert the partial table is genuinely absent, not merely
  that an error was returned.
- `PRAGMA journal_mode=WAL` returns a row and must be read with `QueryRow`, not `Exec`, or
  the driver may skip it silently. DoD 5 exists to catch exactly that.
- WAL sidecars (`tasks.db-wal`, `tasks.db-shm`) change what "the database file" means for
  any later backup or sync work. Note it in CLAUDE.md.
- A `CASE`-based tier ordering is easy to get subtly wrong (dir sorting before session). The
  ordering test pins all three tiers explicitly.

**Revision after Checkpoint 2 (2026-08-20).** Step 9: make concurrent first open safe per
DoD 13 — one `BEGIN IMMEDIATE` transaction around the version read and the apply loop, and
`busy_timeout` ahead of `journal_mode` on the DSN. Add the N-goroutine fresh-file test
alongside the existing two-handle test, and re-run the two-process binary check. The
existing `## Evidence` section below is the *previous* pass's and must be replaced, not
appended to, so the numbers in it are never mistaken for the fixed build's.

**Out of scope:** scope resolution, any TUI work, any CLI command beyond the `doctor`
version line, and the 24h purge *policy* — `PurgeDone` takes an explicit cutoff and the
completed-task-lifecycle task decides it.

## Evidence

Executed 2026-08-19 in worktree `/Users/agusarias/workspace/todo-sqlite-store`
(branch `sqlite-store-and-migrations`, now merged and removed).

**Merge commit: `fda2791`** — fast-forward onto `main`, 10 files, +1488/-123.
Not pushed. Review with `git show fda2791`.

### Store test suite (all 23 tests, real SQLite files under `t.TempDir()`)

```
$ go test ./internal/store/ -v -count=1
--- PASS: TestOpenMigratesToLatest (0.01s)
--- PASS: TestReopenIsANoOp (0.00s)                       <- DoD 3
--- PASS: TestFailedMigrationRollsBack (0.00s)            <- DoD 2
--- PASS: TestSchemaVersionIsStructurallySingleRow (0.00s) <- DoD 4
--- PASS: TestLegacyVersionTableIsReplaced (0.00s)
--- PASS: TestLegacyVersionTableWithDataIsRefused (0.00s)
--- PASS: TestParseMigrationName (0.00s)
--- PASS: TestLoadMigrationsIsOrdered (0.00s)
--- PASS: TestOpenAppliesPragmas (0.00s)                  <- DoD 5
--- PASS: TestOpenCreatesMissingDirectories (0.00s)
--- PASS: TestOpenRejectsEmptyPath (0.00s)
--- PASS: TestDefaultPathHonoursXDG (0.00s)
--- PASS: TestAddListRoundTrip (0.00s)                    <- DoD 8
--- PASS: TestCompleteUncompleteDoneAt (0.00s)            <- DoD 8
--- PASS: TestListOrdering (0.00s)                        <- DoD 7
--- PASS: TestListSameSecondTiebreak (0.00s)
--- PASS: TestListFiltersByScope (0.00s)
--- PASS: TestListGroupedIncludesInactiveScopes (0.00s)   <- DoD 7
--- PASS: TestUpdateTextRescopeDelete (0.00s)
--- PASS: TestMissingIDReportsNotFound (0.00s)
--- PASS: TestValidationRejectsBadInput (0.00s)
--- PASS: TestPurgeDoneUsesCallerCutoff (0.00s)
--- PASS: TestConcurrentHandles (0.02s)                   <- DoD 9
ok  	github.com/agusarias/tmux-todo/internal/store	0.368s
```

Full sweep, clean:

```
$ gofmt -l .            (no output)
$ go vet ./...          (no output)
$ go test ./... -count=1
?   	github.com/agusarias/tmux-todo/cmd/tdo	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/cli	1.183s
?   	github.com/agusarias/tmux-todo/internal/scope	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/store	0.725s
ok  	github.com/agusarias/tmux-todo/internal/task	0.803s
ok  	github.com/agusarias/tmux-todo/internal/tui	0.965s
```

### The database the binary created (not a test fixture)

```
$ XDG_DATA_HOME=…/xdg2 ./bin/tdo doctor
tdo      5bba1c0-dirty
runtime  go1.26.6 darwin/arm64
database …/xdg2/tmux-todo/tasks.db
schema   1 (latest 1)
journal  wal
tasks    0 pending, 0 total
ok
$ echo $?
0

$ sqlite3 …/xdg2/tmux-todo/tasks.db '.schema'
CREATE TABLE schema_version (
		id      INTEGER PRIMARY KEY CHECK (id = 1),
		version INTEGER NOT NULL
	);
CREATE TABLE tasks (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    text       TEXT    NOT NULL,
    done       INTEGER NOT NULL DEFAULT 0 CHECK (done IN (0, 1)),
    done_at    INTEGER,
    scope_kind TEXT    NOT NULL CHECK (scope_kind IN ('session', 'dir', 'global')),
    scope_key  TEXT    NOT NULL DEFAULT '',
    created_at INTEGER NOT NULL,

    -- A global task has no key; session and dir tasks must have one.
    CHECK ((scope_kind = 'global' AND scope_key = '') OR
           (scope_kind <> 'global' AND scope_key <> '')),
    -- done and done_at agree: either both set or neither.
    CHECK ((done = 1 AND done_at IS NOT NULL) OR (done = 0 AND done_at IS NULL))
);
CREATE TABLE sqlite_sequence(name,seq);
CREATE INDEX idx_tasks_scope ON tasks (scope_kind, scope_key, done);
CREATE INDEX idx_tasks_created_at ON tasks (created_at DESC, id DESC);

$ sqlite3 …/tasks.db 'PRAGMA journal_mode; SELECT * FROM schema_version;'
wal
1|1
```

The two CHECK constraints beyond the design's column list are consistency
guards, not new fields: they stop a global task carrying a scope key and stop
`done` disagreeing with `done_at`. Column set is exactly docs/design.md v1.

### Build and linkage

```
$ CGO_ENABLED=0 make build
$ otool -L bin/tdo
bin/tdo:
	/usr/lib/libSystem.B.dylib (compatibility version 0.0.0, current version 0.0.0)
	/usr/lib/libresolv.9.dylib (compatibility version 0.0.0, current version 0.0.0)
```

### Cold start with a populated database

500 rows inserted with `sqlite3`, then 10 runs each (`time.perf_counter()` around
`subprocess.run`):

```
tdo --version:                                            median=7.9ms  min=6.9  max=19.4
tdo doctor (500 rows, open + migrate check + 2 counts):   median=8.3ms  min=7.8  max=16.4
```

Opening the database, checking migrations and running two aggregate queries costs
~0.4ms on top of process start, against a ~100ms budget. Note this measures
`Count`, not `List` — there is no CLI list command until the cli-surface task, so
full-list latency gets measured there.

### Driver behaviour probed before writing the runner

Two facts were established empirically rather than assumed, both of which shaped
the implementation:

1. `?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)`
   on the DSN reaches pooled connections — read back as `wal` / `5000` / `1`.
   A post-open `Exec` would have applied to one connection only.
2. A multi-statement `Exec` returns the failure but **leaves earlier statements
   applied**: `CREATE TABLE a (x); CREATE TABLE bad (((` errored and left `a`
   behind. Outside a transaction the runner would strand partial DDL, so the
   per-migration transaction is doing real work — `TestFailedMigrationRollsBack`
   asserts the partial table is genuinely absent, not merely that an error came back.

### Definition of done

| # | Item | Status |
|---|---|---|
| 1 | `001_init.sql` embedded, v1 columns | met |
| 2 | Runner applies above-version migrations in one transaction; failure rolls back | met — `TestFailedMigrationRollsBack` |
| 3 | Second run is a no-op | met — `TestReopenIsANoOp` (version and 3 rows unchanged) |
| 4 | `schema_version` structurally single-row | met — second row and duplicate id both rejected |
| 5 | WAL / busy_timeout / foreign_keys on every Open, WAL asserted | met — `TestOpenAppliesPragmas` |
| 6 | Full ctx-taking API on domain types | met — plus `ListGrouped` for the all-tasks view |
| 7 | `List` scope-filtered + tier ordering; grouped listing incl. inactive scopes | met |
| 8 | Unix-second timestamps, `DoneAt` nil/non-nil round-trip | met |
| 9 | Two handles, interleaved writes and reads, no SQLITE_BUSY | met — 4 goroutines × 40 rounds |
| 10 | `go test` / `go vet` / `gofmt` clean; no libsqlite3 | met |
| 11 | `doctor` reports schema 1, exits 0 | met |
| 12 | Real SQLite files in `t.TempDir()`, no mocks | met |
