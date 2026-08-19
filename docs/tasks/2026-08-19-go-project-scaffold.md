# Go Project Scaffold

**Status:** review
**Worktree:** /Users/agusarias/workspace/todo-go-project-scaffold

## Goal
Set up the `tmux-todo` Go project: module, repo layout separating the core task package
from the CLI and TUI entrypoints, `tdo` binary build, and a test harness. Cold start has
to stay imperceptible when launched from `tmux display-popup`.

**Design:** docs/design.md

## Why
This task fixes the toolchain, dependency set and package boundaries that the other nine
queued tasks all import. Getting the driver and TUI choices wrong here means rewriting
store, scope and three TUI tasks later. It also bootstraps a repo that currently has zero
commits and no CLAUDE.md.

## Constraints
- Module path `github.com/agusarias/tmux-todo`; binary name `tdo`.
- `CGO_ENABLED=0` must produce a working static binary — no C toolchain at build time.
- Deps limited to the Charm TUI stack + pure-Go SQLite. CLI parsing uses the stdlib.
- Scope resolution, real store queries, and real TUI behaviour are **out of scope** — this
  task only proves each layer compiles, links and runs. Downstream tasks fill them in.
- Do not invent schema beyond what docs/design.md v1 specifies.

## Critical surface
None. Greenfield repo, no migrations against existing data, no auth, no external side
effects, no published API. The one machine-level action is upgrading the Go toolchain
(user-approved during the grill).

## Definition of done
1. `go version` reports Go 1.23 or newer, resolved from `/opt/homebrew/bin/go`.
2. `go.mod` declares module `github.com/agusarias/tmux-todo` and a modern Go directive.
3. Layout exists: `cmd/tdo/`, `internal/task/`, `internal/store/`, `internal/scope/`,
   `internal/tui/`, `internal/cli/`.
4. `CGO_ENABLED=0 make build` produces `./bin/tdo`; `file bin/tdo` shows a Mach-O binary
   and `otool -L` shows no libsqlite3 linkage.
5. `bin/tdo --version` prints a version string and exits 0.
6. `bin/tdo tui` opens a placeholder Bubble Tea view that quits on `q` (exercises the full
   TUI stack, not just compilation).
7. `internal/store` opens a real SQLite file, creates a `schema_version` table, and reads
   it back — proving modernc.org/sqlite works with CGO off.
8. `go test ./...` passes with at least one real test per non-placeholder package, using
   `t.TempDir()` for DB fixtures (no mocks over SQLite).
9. `go vet ./...` clean; `gofmt -l .` empty.
10. Measured cold start of `bin/tdo --version` recorded in Evidence, under 100ms.
11. `CLAUDE.md` exists at repo root: build/test commands, layout, worktree notes, the
    driver/TUI decisions and why. Under 60 lines.
12. `.gitignore` covers `bin/`, and a README stub names the project and its status.

## Verification
- `go test ./...`, `go vet ./...`, `gofmt -l .` — real output pasted into Evidence.
- `CGO_ENABLED=0 make build` then `otool -L bin/tdo` to prove no dynamic SQLite linkage.
- Cold start: `for i in $(seq 10); do /usr/bin/time -p bin/tdo --version; done` (or
  equivalent), report the median.
- `bin/tdo tui` cannot be asserted in a headless test; exercise it manually inside tmux and
  state in Evidence that it was a manual check.

## Decisions
- **2026-08-19 (grill):** Upgrade the Go toolchain to latest stable via `brew install go`
  rather than pinning dependencies to Go 1.19-compatible releases. Installed Go was 1.19.2
  from a `/usr/local/go` tarball; current bubbletea/lipgloss/modernc.org/sqlite all require
  Go >= 1.23, and 1.19 predates `GOTOOLCHAIN` auto-upgrade so it cannot self-resolve.
  `/opt/homebrew/bin` precedes `/usr/local/go/bin` in PATH, so the brew install shadows the
  tarball with no cleanup. If `brew install go` fails, that is a blocked event, not a
  workaround-it event.
- **2026-08-19 (grill):** SQLite driver is `modernc.org/sqlite` (pure Go) over
  `mattn/go-sqlite3` (cgo). Buys `CGO_ENABLED=0`, a genuinely static binary and painless
  cross-compilation later; costs ~2-3x query time and a larger binary, both irrelevant for
  a few hundred rows in a popup.
- **2026-08-19 (grill):** TUI is Bubble Tea + lipgloss. Matches sesh's ergonomics, and the
  Elm-style model/update/view fits a popup that stays open across actions, toggles between
  merged and all-tasks views, and hosts an inline input row.
- **2026-08-19 (grill):** The TPM plugin builds from source (`go build` when `tdo` is
  absent) rather than downloading release assets. No goreleaser or CI release pipeline in
  v1; a follow-up task can add prebuilt binaries once v1 works.
- **2026-08-19 (curator):** CLI parsing uses stdlib `flag` with manual subcommand dispatch,
  not cobra — five commands do not justify the dependency or its init cost on a hot path.
- **2026-08-19 (curator):** Tests run against real SQLite files in `t.TempDir()`, never a
  mocked store interface. The store's whole job is talking to SQLite correctly.
- **2026-08-19 (curator):** Cold start is verified by measurement recorded in Evidence, not
  by an automated timing assertion — a wall-clock threshold in `go test` is flaky and would
  train everyone to ignore it.

- **2026-08-19 (executor):** Added a `tdo doctor` command beyond the plan's `--version`
  and `tui`. Without it the shipped binary never touches SQLite, so `otool -L` would prove
  only that an unrelated binary lacks libsqlite3. `doctor` resolves the XDG path, opens the
  database and reads `schema_version`, making the CGO_ENABLED=0 claim testable from the
  artifact users actually run. Revision of the how, not the what.
- **2026-08-19 (executor):** `store.DefaultPath()` (XDG data dir) lands in this task rather
  than the store-and-migrations task, because `doctor` needs it. Still no task queries.
- **2026-08-19 (executor):** DoD 6 was verified by scripting a real tmux session
  (`new-session` + `capture-pane` + `send-keys q`) instead of a human watching a popup. The
  `display-popup` overlay renders client-side and cannot be captured, so the popup framing
  itself remains unverified — flagged in Evidence rather than claimed.

## Plan
Approved at Checkpoint 1, 2026-08-19.

**Approach:** one thin vertical slice through every layer the other nine tasks import, so
the stack is proven rather than merely present. Each package gets its real dependency wired
and exercised; no domain logic that belongs to a downstream task.

**Sequencing**
0. (Done by curator before this task went `ready`: initial commit on `main` carrying
   `docs/`, so `git worktree add` has a HEAD to branch from.)
1. `brew install go`; confirm `go version` >= 1.23 resolving from `/opt/homebrew/bin/go`.
   If brew fails, set status `blocked` — do not fall back to Go 1.19.
2. `go mod init github.com/agusarias/tmux-todo`; create the package skeleton.
3. `internal/task` — `Task` struct and `ScopeKind` enum only.
4. `internal/store` — `Open(path)`, create `schema_version`, read it back. Real SQLite.
5. `internal/cli` — stdlib `flag` with manual subcommand dispatch: `--version`, `tui`.
6. `internal/tui` — placeholder Bubble Tea model that quits on `q`/`Esc`.
7. `internal/scope` — `doc.go` only, noting the package is owned by the scope-resolution
   task. Do not implement resolution here.
8. `Makefile` (build/test/vet/fmt/install), `.gitignore` (covers `bin/`), README stub,
   `CLAUDE.md`.
9. Verification sweep against the Definition of done.

**Files created:** `go.mod`, `go.sum`, `cmd/tdo/main.go`, `internal/task/task.go`,
`internal/store/store.go`, `internal/store/store_test.go`, `internal/cli/cli.go`,
`internal/cli/cli_test.go`, `internal/tui/tui.go`, `internal/scope/doc.go`, `Makefile`,
`.gitignore`, `README.md`, `CLAUDE.md`.

**What could go wrong**
- `modernc.org/sqlite` has a large generated dep tree; the first build takes 1-2 minutes.
  Slow, not hung.
- Bubble Tea inside `display-popup -E` is the one thing no headless test covers. Manual
  check in tmux, labelled as manual in Evidence.
- Binary lands ~12-15MB with the pure-Go driver. Expected, not a regression.
- `brew install go` is the only step touching the machine outside the repo.

**Out of scope:** task CRUD, scope resolution, migrations beyond `schema_version`, real
popup UI.

## Evidence

Executed 2026-08-19 in worktree `/Users/agusarias/workspace/todo-go-project-scaffold`
(branch `go-project-scaffold`, now merged and removed).

**Merge commit: `abbc3e3`** — fast-forward onto `main`, 16 files, 851 insertions.
Not pushed. Review with `git show abbc3e3`.

### Toolchain, format, vet, tests

```
$ go version
go version go1.26.6 darwin/arm64        # /opt/homebrew/bin/go, shadowing the 1.19.2 tarball

$ gofmt -l .
(no output)

$ go vet ./...
(no output)

$ go test ./... -count=1
?   	github.com/agusarias/tmux-todo/cmd/tdo	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/cli	0.545s
?   	github.com/agusarias/tmux-todo/internal/scope	[no test files]
ok  	github.com/agusarias/tmux-todo/internal/store	0.692s
ok  	github.com/agusarias/tmux-todo/internal/task	0.820s
ok  	github.com/agusarias/tmux-todo/internal/tui	0.961s
```

`cmd/tdo` and `internal/scope` are the two packages without tests: `main` is three
lines delegating to `internal/cli`, and `scope` is a `doc.go` stub owned by the
scope-resolution task. Store tests use real SQLite files under `t.TempDir()` —
no mocks — and cover fresh open, version round-trip, reopen persistence, nested
directory creation and the empty-path error.

### Build and linkage

```
$ CGO_ENABLED=0 make build
go build -trimpath -ldflags '-s -w -X github.com/agusarias/tmux-todo/internal/cli.Version=2d57bb7' -o bin/tdo ./cmd/tdo

$ ls -lh bin/tdo
-rwxr-xr-x  1 agusarias  staff   7.1M bin/tdo

$ file bin/tdo
bin/tdo: Mach-O 64-bit executable arm64

$ otool -L bin/tdo
bin/tdo:
	/usr/lib/libSystem.B.dylib (compatibility version 0.0.0, current version 0.0.0)
	/usr/lib/libresolv.9.dylib (compatibility version 0.0.0, current version 0.0.0)
```

No libsqlite3 linkage. 7.1MB, below the 12-15MB the plan expected.

### Binary behaviour

```
$ ./bin/tdo --version
2d57bb7
$ echo $?
0

$ ./bin/tdo doctor
tdo      2d57bb7
runtime  go1.26.6 darwin/arm64
database /…/scratchpad/xdg/tmux-todo/tasks.db
schema   0
ok
$ echo $?
0
```

`doctor` opens a real SQLite database from the *shipped* binary — this is what
makes the CGO_ENABLED=0 claim testable outside the test binary, since without it
`otool -L` would only prove that a binary which never touches SQLite fails to
link it.

### Cold start (DoD 10)

10 runs each, measured with `time.perf_counter()` around `subprocess.run` (more
precise than `/usr/bin/time -p`, which quantises to 10ms):

```
tdo --version: n=10 median=7.4ms min=6.9ms max=8.3ms
tdo doctor:    n=10 median=7.7ms min=7.4ms max=12.3ms
```

Median 7.4ms against a 100ms budget — 13x headroom, and `doctor` shows that
opening SQLite costs ~0.3ms on top.

### TUI (DoD 6) — scripted, not human-eyeballed

Run in a real tmux 3.7b session, not a headless harness:

```
$ tmux new-session -d -s tdo-check -x 80 -y 24 "$PWD/bin/tdo tui"
$ tmux capture-pane -p -t tdo-check
╭───────────────────────────────────────╮
│  tdo                                  │
│                                       │
│  scaffold placeholder — no tasks yet  │
│                                       │
│  version 2d57bb7 · q quit             │
╰───────────────────────────────────────╯
$ tmux send-keys -t tdo-check q          # session exited on q
```

**Not verified:** the `display-popup -E` overlay itself. `display-popup` needs an
attached client and renders client-side, so `capture-pane` cannot see it — a
detached-session attempt showed only the typed command. The Bubble Tea stack,
render and quit path are proven above; the popup *framing* (60%×60%, centering,
Esc handling inside the overlay) still wants one human look before the popup-TUI
task builds on it. Flagging rather than claiming it.

`internal/tui` also has headless tests asserting `q`/`Esc`/`ctrl+c` return
`tea.Quit` and that unhandled keys do not.

### Definition of done

| # | Item | Status |
|---|---|---|
| 1 | Go ≥ 1.23 from `/opt/homebrew/bin/go` | met — 1.26.6 |
| 2 | `go.mod` module path + modern directive | met — `go 1.26` |
| 3 | Package layout | met — all six directories |
| 4 | `CGO_ENABLED=0 make build`, no libsqlite3 | met |
| 5 | `--version` prints, exit 0 | met |
| 6 | `tdo tui` placeholder quits on `q` | met in a tmux pane; popup overlay unverified (above) |
| 7 | Store opens real SQLite, `schema_version` round-trip | met — tests + `doctor` |
| 8 | `go test ./...` green, real test per non-placeholder package | met |
| 9 | `go vet` clean, `gofmt -l` empty | met |
| 10 | Cold start recorded, < 100ms | met — 7.4ms median |
| 11 | `CLAUDE.md` under 60 lines | met — 58 lines |
| 12 | `.gitignore` covers `bin/`, README stub | met |
