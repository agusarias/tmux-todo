# tmux-todo (`tdo`)

tmux-native TODO manager: a `display-popup` TUI over a local SQLite store, tasks
scoped `session` / `dir` / `global`. `docs/design.md` is the agreed design and
wins over any task brief that contradicts it; `docs/tasks/` is the work queue.

## Commands

```sh
make build      # CGO_ENABLED=0 static binary -> ./bin/tdo
make test       # go test ./...
make lint       # go vet ./... + gofmt check
make install    # go install ./cmd/tdo
./bin/tdo doctor   # end-to-end check; --db X targets a throwaway database
```

Go must resolve from `/opt/homebrew/bin/go` (1.26.x). A Go 1.19 tarball also
sits at `/usr/local/go/bin/go`; it is too old for the Charm and sqlite deps and
predates `GOTOOLCHAIN` auto-upgrade, so it cannot self-resolve. If a shell picks
it up, fix PATH rather than downgrading dependencies.

## Layout

- `cmd/tdo` — thin `main`, delegates to `internal/cli` so commands stay testable.
- `internal/task` — domain types (`Task`, `ScopeKind`). No I/O.
- `internal/store` — SQLite: `Open`, `schema_version`, `DefaultPath` (XDG).
- `internal/scope` — pane → scope resolution. Doc-only stub, owned by its task.
- `internal/tui` — Bubble Tea popup. Currently a placeholder model.
- `internal/cli` — stdlib `flag` with manual subcommand dispatch.

## Decisions worth knowing

- **`modernc.org/sqlite`, not `mattn/go-sqlite3`.** Pure Go buys `CGO_ENABLED=0`,
  a genuinely static binary and easy cross-compilation. It costs ~2-3x query
  time — irrelevant for a few hundred rows. `otool -L bin/tdo` must never show
  libsqlite3.
- **Bubble Tea + lipgloss** for the TUI: the popup stays open across actions, so
  the Elm-style model/update/view fits better than one-shot prompts.
- **No cobra.** Five commands do not justify the dependency on a hot path;
  cold start is ~7ms and should stay under 100ms (popup latency is the product).
- **Tests use real SQLite files in `t.TempDir()`**, never a mocked store — the
  store's whole job is talking to SQLite correctly.
- **Version** is stamped via `-ldflags -X .../internal/cli.Version` (`dev` by hand).

## Pitfalls

- The popup overlay cannot be asserted headlessly. `tmux display-popup` needs an
  attached client, so automated checks run the TUI in a plain tmux pane
  (`tmux new-session -d ... 'bin/tdo tui'` + `capture-pane`); the popup framing
  itself is a manual check.
- The first build after `go mod tidy` takes 1-2 minutes — `modernc.org/sqlite`
  has a large generated tree. Slow, not hung.

## Worktrees

Work happens in `git worktree` checkouts (`../todo-<task-slug>`). No env files
or bootstrap scripts: `make build` and `make test` work in a fresh worktree as
soon as the module cache is warm. `bin/` is gitignored per worktree.
