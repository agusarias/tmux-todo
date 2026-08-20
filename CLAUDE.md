# tmux-todo (`tdo`)

tmux-native TODO manager: a `display-popup` TUI over a local SQLite store, tasks
scoped `session` / `dir` / `global`. `docs/design.md` is the agreed design and
wins over any task brief that contradicts it; `docs/tasks/` is the work queue.

## Commands

```sh
make build      # CGO_ENABLED=0 static binary -> ./bin/tdo
make test       # go test ./...
make lint       # go vet ./... + gofmt check
./bin/tdo doctor          # schema version, journal mode, task counts
./bin/tdo doctor --db X   # ...against a throwaway database
```

Go must resolve from `/opt/homebrew/bin/go` (1.26.x). The Go 1.19 tarball at
`/usr/local/go/bin/go` is too old for these deps and cannot self-upgrade: if a
shell picks it up, fix PATH rather than downgrading dependencies.

## Layout

- `cmd/tdo` — thin `main`, delegates to `internal/cli` so commands stay testable.
- `internal/task` — domain types (`Task`, `ScopeKind`). No I/O.
- `internal/store` — SQLite: `Open` (pragmas + migrate), task CRUD, and
  `migrations/NNN_*.sql` run by `migrate.go`. Environment-blind by design: it
  takes `task.Scope` values and never asks tmux or the filesystem anything.
- `internal/scope` — pane → scope resolution. Doc-only stub, owned by its task.
- `internal/tui` — Bubble Tea popup. Currently a placeholder model.
- `internal/cli` — stdlib `flag` with manual subcommand dispatch.

## Decisions worth knowing

- **`modernc.org/sqlite`, not `mattn/go-sqlite3`.** Pure Go buys `CGO_ENABLED=0`
  and a genuinely static binary, at ~2-3x query time — irrelevant here.
  `otool -L bin/tdo` must never show libsqlite3.
- **Bubble Tea + lipgloss**: the popup stays open across actions.
- **No cobra.** A handful of commands does not justify it on a hot path; cold
  start is ~8ms and should stay under 100ms — popup latency is the product.
- **Migrations are embedded `.sql` files run by a hand-rolled runner.** To add
  one, drop in `internal/store/migrations/002_*.sql`; filename order decides, and
  `store.SchemaVersion()` follows. Each file runs in one transaction that also
  bumps `schema_version`. Never edit an applied migration — add the next one.
- **Timestamps are INTEGER unix seconds.** The `id DESC` tiebreak keeps
  newest-first stable within a second, so milliseconds were unnecessary.
- **The store never reads the clock**: `DB.now` is a field tests freeze.
  `PurgeDone` takes an explicit cutoff — the 24h retention *policy* belongs to
  the completed-task-lifecycle task.
- **Tests use real SQLite files in `t.TempDir()`**, never a mocked store.
- **Version** is stamped via `-ldflags -X .../internal/cli.Version`.

## Pitfalls

- **Per-connection pragmas ride on the DSN** (`?_pragma=busy_timeout(5000)&...`),
  not a post-open `Exec`: `database/sql` pools connections, so an `Exec`-applied
  pragma reaches one connection and silently misses the rest.
- **`journal_mode` is the exception and must NOT be a DSN pragma.** It is a
  property of the file, and SQLite refuses to change it while another connection
  is reading — answering `SQLITE_BUSY` *without* consulting `busy_timeout`. On
  the DSN it fires on every new pooled connection, so concurrent first opens
  fail outright. `Open` calls `ensureJournalMode` once, with a retry.
- **Concurrent first open is the dangerous window**, not steady state: the
  migration runner holds one `BEGIN IMMEDIATE` transaction across the version
  read and the apply loop, so the losing process waits and then sees the version
  already current. Tests that race an *already-migrated* database prove nothing
  about this — that was the DoD-9 gap that shipped a bug.
- **A multi-statement `Exec` applies statements up to the first failure** and
  leaves them there — hence the transaction around each migration.
- **WAL means sidecars.** `tasks.db-wal` / `tasks.db-shm` sit beside the database
  while a connection is open; backup or sync work must account for them. A clean
  `Close` checkpoints them away.
- Raw `sqlite3` output shows integer timestamps: read them with
  `datetime(created_at, 'unixepoch')`.
- The popup overlay cannot be asserted headlessly — `display-popup` needs an
  attached client. Automated checks run the TUI in a plain tmux pane
  (`tmux new-session -d … 'bin/tdo tui'` + `capture-pane`) instead.

## Worktrees

Work happens in `git worktree` checkouts (`../todo-<task-slug>`). No env files or
bootstrap scripts: `make build` and `make test` work in a fresh worktree once the
module cache is warm. `bin/` is gitignored per worktree.
