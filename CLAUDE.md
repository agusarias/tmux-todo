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
- `internal/scope` — pane → scope resolution (`Resolver`/`Resolved`), the pure-Go
  git root walker, and the sticky default kind. Injectable: tests need neither a
  tmux server nor a git checkout.
- `internal/tui` — Bubble Tea popup: the merged task list. Environment-blind by
  design — it takes a `Config` (store, resolved scopes, home dir, version) from
  `internal/cli` and never resolves a scope or reads the clock itself, so
  `Update`/`View` are testable without tmux. Row formatting lives in `render.go`
  as pure functions.
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
- **Scope keys are durable database keys**: absolute, cleaned, symlink-resolved,
  never case-folded. Changing a rule after v1 ships is a data migration, so the
  rules are pinned by tests. Git worktrees fold into their main repo (so
  `../todo-<task>` shares the parent's list) while submodules keep their own —
  `TestAgreesWithGitBinary` holds both to what real git reports.
- **Absent beats empty.** Outside tmux there is no session scope rather than a
  `""` key, and `--session` errors; the sticky default stores a *kind*, never a
  key, in the XDG *state* dir, and a corrupt file falls back silently.
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
- **A test that injects a dependency cannot prove the production default is
  wired.** This has now shipped two bugs. `scope.Resolve()` was
  `Resolver{}.Resolve()` with an empty `TmuxEnv`, so session scope was
  unreachable in the binary while all 28 tests passed — every one of them set
  `TmuxEnv` by hand. Whenever a seam has a zero value that *means* something
  (here `""` = "not inside tmux"), the constructor is the thing to test: exercise
  the real entry point and assert it agrees with an explicitly-wired one.
- **A multi-statement `Exec` applies statements up to the first failure** and
  leaves them there — hence the transaction around each migration. That per-file
  unit is a `SAVEPOINT` *inside* the one outer `BEGIN IMMEDIATE`, so a failed
  migration rolls back to its savepoint and the outer transaction still
  **commits** — earlier migrations survive and the version tracks the last
  success. Looks wrong at a glance; it is what makes partial upgrades sane.
- **WAL means sidecars.** `tasks.db-wal` / `tasks.db-shm` sit beside the database
  while a connection is open; backup or sync work must account for them. A clean
  `Close` checkpoints them away.
- Raw `sqlite3` output shows integer timestamps: read them with
  `datetime(created_at, 'unixepoch')`.
- The popup overlay cannot be asserted headlessly — `display-popup` needs an
  attached client. Automated checks run the TUI in a plain tmux pane
  (`tmux new-session -d … 'bin/tdo tui'` + `capture-pane`) instead. `tdo tui` has
  no `--db` flag, but `store.DefaultPath` honours **`$XDG_DATA_HOME`**, so point
  that at a temp dir, create the schema with `tdo doctor --db …`, seed rows with
  the `sqlite3` CLI, and the capture is reproducible without touching the real
  database. Worth the trouble: this is the only check that catches whole-frame
  bugs, and it has caught three the unit tests could not see.
- **Unit tests over `renderRows` do not test the assembled frame.** Both frame
  bugs found so far were in the *arithmetic between* correct pieces: `chromeHeight`
  must count the box's two border rows and both blank lines (not just title and
  footer), and `View` must not append a trailing newline — a frame taller than the
  pane makes the terminal **scroll**, so the rows that disappear are the top ones
  (session tier, tier labels), not the bottom.
- **A row wider than the viewport is silently clipped, not wrapped.** That is how
  tier labels vanished for real dir keys: a dir scope key is an absolute path, and
  a label sized from "whatever width is left" gets no width at all. `columns()`
  guarantees labels a share and left-truncates them (the path *tail* identifies
  it); `TestRowsNeverExceedTheirWidth` holds every row to its budget.
- **lipgloss styles render to plain text in tests** — a test process has no colour
  profile, so asserting on rendered ANSI passes whatever style was chosen. Assert
  on the style object instead (`textStyle(...).GetStrikethrough()`) and prove the
  escapes ship with `capture-pane -pe`.

- **`t.TempDir()` is itself under a symlink on macOS** (`/var` -> `/private/var`),
  so scope tests compare against `normalizePath(tmp)`, never the raw temp path.
- **Resolution costs one `tmux display-message`** (~5ms of a ~5.7ms cold median),
  one call for both formats. If the popup ever needs those milliseconds back, the
  lever is tmux expanding `#{...}` straight into the `display-popup` command.

## Worktrees

Work happens in `git worktree` checkouts (`../todo-<task-slug>`). No env files or
bootstrap scripts: `make build` and `make test` work in a fresh worktree once the
module cache is warm. `bin/` is gitignored per worktree.
