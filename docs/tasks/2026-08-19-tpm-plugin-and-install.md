# TPM Plugin And Install

**Status:** agreed
**Worktree:** none

## Goal
Ship the TPM plugin that makes `tdo` reachable from a stock tmux config: a `tmux-todo.tmux`
entry point that resolves the `tdo` binary, installs the popup keybind honouring
`@todo-key`, and installs the `session-renamed` hook idempotently — plus a README that
actually gets a new user from zero to a working popup.

**Design:** docs/design.md — "Distribution"

## Why
Everything else in v1 is built and merged, and none of it is reachable. A user who follows
`design.md`'s two-line config today gets nothing: there is no `.tmux` script in the repo, so
TPM clones it and does nothing. This task is the difference between a working program and a
program someone else can use.

The README matters as much as the script. Its current install instruction is **impossible to
follow** — it claims "Requires Go 1.23 or newer" against a `go.mod` that declares 1.26 and
dependencies that need 1.25.0 — and it still says "Status: scaffold … the real features are
not built yet", which is false for every feature except the all-tasks view. The front door
currently misdescribes the house.

## Constraints
- `tpm-plugin-and-install` owns **packaging**; `tmux-integration-and-rename-hook` (now `done`)
  owned **behaviour** and its Evidence section holds the verbatim keybind and hook commands
  this task must install. Use those, do not re-derive them.
- **The script runs on every tmux server start**, not just at install: TPM sources plugin
  scripts each time. So the steady-state path — binary already present — must do no build, no
  network, and no measurable work. A `go build` may happen at most once, when the binary is
  genuinely absent.
- **`make test` must not require a tmux server.** CLAUDE.md documents it as `go test ./...`
  and CI has no tmux. The plugin's shell harness gets its own target.
- The keybind's popup geometry is the floored `if-shell` branch already signed off
  (`design.md:49`, 60x15 floor). This task changes the *path* inside it and nothing else.
- Installing must never clobber a user's own `session-renamed` hook — that is why the
  behaviour task chose `set-hook -ga` over `-g`.
- Out of scope: git tags, CI, and per-platform release binaries. That is the strictly better
  end-state for distribution and it is **its own task** (see Decisions) — this task ships
  what works with no release infrastructure. Also out of scope: a Homebrew formula, shell
  completions, and any `@todo-*` option beyond `@todo-key`.

## Critical surface
1. **The install script mutates the user's live tmux state** — a global keybind and a global
   hook. Not the database, but still *their* environment. It must be idempotent across
   repeated runs (TPM re-runs it) and must leave a user's own hooks intact.
2. **It may execute a Go toolchain on the user's machine** at install time. That must be a
   deliberate last resort with a version check, never a silent surprise, and never on a path
   that runs at every tmux start.
3. **The `go.mod` directive change affects everyone who builds**, not only plugin installers.
   Verified before being written into the DoD below rather than asserted.
4. **The README is the project's front door.** A wrong install instruction is the most
   expensive kind of bug here because it fails before anyone can report it.

No migrations, no database access, no network.

## Definition of done

**The plugin entry point**
1. `tmux-todo.tmux` at the repo root, executable (`chmod +x`, committed with the exec bit),
   `#!/usr/bin/env bash` with `set -u`, and TPM-discoverable (TPM globs `*.tmux` in the
   plugin dir).
2. It resolves the binary through this chain, in order, stopping at the first success:
   1. `tdo` on `$PATH`
   2. `$PLUGIN_DIR/bin/tdo`, if it exists and is executable
   3. `go build` into `$PLUGIN_DIR/bin/tdo`, **only if** `go` is present and its version is
      >= 1.25
   4. otherwise: no keybind is installed, and a `display-message` names the problem and the
      fix
   Each of the four outcomes is exercised by the harness (DoD 9).
3. **A keybind is never installed pointing at a binary that does not exist.** Step 4 binds
   nothing rather than binding something that will fail on press — a dead keybind is a worse
   failure than a clear message, because it looks like the plugin's fault at press time
   rather than at install time.
4. The steady-state run (binary already resolvable at step 1 or 2) does no build and no
   network, and adds no measurable time to tmux startup. Measured, with the number recorded.

**Configuration**
5. `@todo-key` is read via `tmux show-option -gqv @todo-key`, defaults to `t` when unset or
   empty, and binds in the **prefix** table. Both the default and an override are tested.
6. No other `@todo-*` option exists. The popup geometry stays the measured `if-shell` branch,
   because a user-set size can silently breach the frame invariant's asserted range.

**Idempotence**
7. Running the script three times leaves **exactly one** keybind for the configured key and
   **exactly one** `tdo` hook. The hook check greps the hook's **body**, not its name:
   after `set-hook -gu session-renamed`, `show-hooks -g` still prints a bare
   `session-renamed` line with no value, so a name-based grep reports a hook that is not
   there. Proven by the probe recorded in Decisions.
8. A pre-existing user hook (`set-hook -ga session-renamed "display-message hi"`) is still
   present, and still fires, after the script runs three times.

**The harness**
9. A shell harness drives the real script against a **private tmux socket** (`tmux -L`) and
   asserts on `list-keys` and `show-hooks` output. It covers: each of the four resolution
   outcomes, the default and overridden `@todo-key`, three consecutive runs (DoD 7), and
   survival of a user hook (DoD 8). Run by a new `make test-plugin`; `make test` stays
   `go test ./...` and gains no tmux dependency.

**The Go floor**
10. `go.mod`'s directive becomes `go 1.25.0` via `go mod tidy` — the exact floor
    `modernc.org/sqlite` requires — with `go.sum` byte-identical and the full suite, vet,
    gofmt and the static build all still clean. **Already verified by the curator** (see
    Decisions); this item is confirmation, not discovery.

**Documentation**
11. README rewritten: what it is, install via TPM, install manually, `@todo-key`, the keys
    the popup binds, the sesh integration and that sesh is optional, the real Go floor
    (1.25) for building from source, and where the data lives. The "Status: scaffold … the
    real features are not built yet" claim is gone.
12. `design.md`'s Distribution section records the resolution chain, so the design describes
    what ships rather than "resolves the `tdo` binary".
13. `make test`, `make test-plugin`, `make lint` clean; `gofmt -l .` empty; `CGO_ENABLED=0
    make build` still static (no libsqlite3 in `otool -L`).

## Verification
- `make test-plugin` output in Evidence, naming each of the four resolution outcomes and
  showing the `list-keys` / `show-hooks` assertions.
- **The idempotence proof as real output**: `show-hooks -g | grep session-renamed` and
  `list-keys | grep tdo` after run 1, run 2 and run 3, so "exactly one" is visible rather
  than asserted.
- **A mutation proof for DoD 7**: with the body-grep guard removed, the three-run test must
  fail with stacked hooks. Idempotence tests pass trivially against a script that installs
  nothing, so the guard has to be shown to be load-bearing.
- The step-4 path demonstrated with `tdo` absent from `PATH`, no `bin/tdo`, and `go` masked:
  the message, and `list-keys` showing **no** binding.
- The go-floor sweep: suite, vet, gofmt, static build, and `git diff --stat go.sum` empty.
- An end-to-end manual leg: a real tmux server, TPM-style clone path, `prefix + t` opening
  the popup, and a session rename moving a task — the whole product working from a config.

## Decisions

### 2026-08-20 (curator grill) — three probes, four rulings

Explored `design.md`'s Distribution section, the Makefile, `.gitignore`, the README, and the
handoff notes in `tmux-integration-and-rename-hook`'s close-out.

**FINDING 1 — there is nothing to download.** No git tags, no CI, no `.github/`, and `bin/`
is gitignored. So "the plugin fetches a prebuilt binary" — the best end-state — is not an
option available to this task; it presupposes release infrastructure that does not exist.
That is why the resolution chain ends in a source build rather than a download, and why
release infrastructure is explicitly deferred to its own task.

**FINDING 2 — the README's install requirement is impossible.** It says "Requires Go 1.23 or
newer". `go.mod` declares `go 1.26`, and the measured dependency floor is **1.25.0**
(`modernc.org/sqlite`; `bubbles` 1.24.2, `bubbletea` 1.24.0, `lipgloss` 1.18 are all lower).
A user on 1.23 following the README cannot build. This matters more than a doc nit here,
because with a source-build install path the Go floor *is* the install requirement.

**FINDING 3 (probe) — the obvious idempotence check is broken.** Probed on tmux 3.7b: three
`set-hook -ga` calls stack as `session-renamed[0..2]`, as the handoff warned. But after
`set-hook -gu session-renamed` clears them, `show-hooks -g` **still prints a bare
`session-renamed` line with no value** — so `show-hooks -g | grep -c session-renamed`
returns 1 when zero hooks are installed. Any de-dup built on a name grep is wrong in the
direction that silently skips installing the hook. The check must match the body.

1. **Binary resolution is a four-step chain ending in a clear failure, not a build-always or
   a require-preinstalled.** PATH, then the plugin's own `bin/`, then a version-checked
   `go build`, then a message and no keybind. Build-always was rejected because it
   hard-requires the toolchain and CLAUDE.md already documents a stale Go 1.19 shadowing
   homebrew's on this machine — the exact failure mode. Require-preinstalled was rejected
   because `prefix + I` no longer "just works", which is the entire expectation a TPM user
   arrives with. The chain's last step binding *nothing* is the deliberate part: a dead
   keybind misattributes the failure to press time.
2. **`go.mod` drops to the real floor, `go 1.25.0`.** Widens who can build from source by a
   release at zero cost. **Verified before being written down**, not assumed: the directive
   was changed in the real repo, `go mod tidy` normalised `1.25` to `1.25.0`, the full suite
   passed, `go vet` and `gofmt` were clean, `CGO_ENABLED=0` still produced a binary linking
   only `libSystem` and `libresolv`, `go.sum` was byte-identical, and the change was
   reverted. (A first attempt in a `tar` copy showed a scope-test failure that turned out to
   be the copy having no `.git`, so `RepoRoot` found no repo — environmental, not the
   directive. Recorded because it is a trap for the next person who tests in a temp dir.)
   This is a deliberate correction of the process failure in `task-create-edit-rescope`,
   where this brief's author asserted an unverified fact as confirmed and the executor had to
   discover it was wrong.
3. **Idempotence is body-grep-then-append.** `show-hooks -g | grep -q 'tdo session-renamed'
   || set-hook -ga …`. Preserves the user's own hooks, which is the whole reason `-ga` was
   chosen, and is immune to Finding 3's bare-name line. **Known accepted gap:** a hook left
   by a *previous install at a different path* is not removed, so a moved plugin dir can
   leave one stale hook alongside the new one. The stale one fails harmlessly (a
   `run-shell -b` on a missing binary), and the reverse-index removal that would fix it costs
   more script than the failure justifies. Recorded so it is a known limitation rather than a
   surprise; revisit if anyone hits it.
4. **`@todo-key` is the only option**, default `t`, prefix table — exactly `design.md`. The
   popup geometry stays fixed: the 60x15 floor exists because below it the footer truncates
   and the frame invariant is near the edge of its asserted range, so a `@todo-popup-size`
   would let a user silently break the popup.

**CURATOR'S CALL, not asked — flagged at Checkpoint 1.** The shell harness gets its own
`make test-plugin` target rather than joining `make test`. Rationale: `make test` is
`go test ./...` in CLAUDE.md and must stay runnable without a tmux server, especially since
the `tmux-regression-guard` task is currently fixing a different in-tmux suite problem.
This does mean a plugin regression will not be caught by the command everyone runs by habit —
the trade is deliberate, and the alternative (a tmux dependency in the default test target)
is worse.

**SPUN OUT:** release infrastructure — git tags, a CI workflow, and per-platform release
binaries the plugin can download — is the strictly better distribution story and is now its
own brief rather than being smuggled in here.

## Plan
One new shell entry point, one new shell harness, one new Makefile target, and documentation.
No Go source changes except `go.mod`'s directive.

**Approach.** `tmux-todo.tmux` is a thin, fast, side-effect-ordered script. Its whole shape is
dictated by the constraint that tmux sources it on **every server start**, so the common path
must be three cheap checks and two `tmux` calls:

```bash
resolve_binary()   # the 4-step chain; echoes a path or returns non-zero
install_keybind()  # bind-key "$key" <the signed-off if-shell branch, path substituted>
install_hook()     # body-grep, then set-hook -ga only if absent
```

Resolution order matters and is not arbitrary: `$PATH` first so a user's own `go install`ed
or brewed `tdo` wins over a stale plugin-local build, and the plugin-local `bin/tdo` second so
the build happens once rather than on every tmux start. The version gate on step 3 is a
comparison against `1.25`, parsed from `go version`, because building with an older toolchain
fails with a message about the `go` directive that reads like a repo bug rather than a
toolchain problem.

**`bind-key` is re-run every start and that is fine** — `bind-key` *replaces* the binding for
a key, so it is naturally idempotent. Only `set-hook -ga` accumulates, which is why the guard
lives there and only there. Worth stating because "make everything idempotent" would add a
`list-keys` grep that buys nothing.

**Files.**
- `tmux-todo.tmux` — new, executable. The entry point.
- `test/plugin_install_test.sh` — new. The harness: spins a private `tmux -L` server per case,
  runs the real script against it, asserts on `list-keys` / `show-hooks`, tears the server
  down. Cases per DoD 9.
- `Makefile` — a `test-plugin` target, and `.PHONY`. `test` is untouched.
- `go.mod` — directive to `go 1.25.0` via `go mod tidy`.
- `README.md` — rewritten per DoD 11.
- `docs/design.md` — Distribution records the chain.
- `CLAUDE.md` — the bare-`session-renamed`-line trap, and that `make test-plugin` exists and
  needs a tmux binary.

**Sequencing.**
1. **`go mod tidy` to `go 1.25.0` and the sweep.** Independent of everything else, already
   verified by the curator, and doing it first means every later build in this task runs on
   the floor the README will claim.
2. **The harness skeleton first, against a stub script.** Write the private-socket
   setup/teardown and the `list-keys`/`show-hooks` assertions before the real script exists,
   so the harness is proven able to *fail*. A harness written after the script tends to
   assert whatever the script happens to do.
3. **`resolve_binary` and its four outcomes**, each driven by the harness with `PATH`,
   `bin/tdo` and `go` manipulated per case. Step 4 (bind nothing, show a message) is the one
   most likely to be got wrong and gets its assertion written first.
4. **`install_keybind`** with `@todo-key` default and override.
5. **`install_hook`** with the body-grep guard, the three-run test, and the user-hook
   survival test — plus the mutation proof that removing the guard stacks hooks.
6. README, `design.md`, CLAUDE.md, then the manual end-to-end leg: real server, TPM-style
   clone path, `prefix + t`, and a rename moving a task.

**What could go wrong.**
- *Idempotence tests passing against a script that installs nothing.* The single biggest risk
  in this task: every DoD 7/8 assertion is satisfiable by a no-op. Hence the mutation proof,
  and hence step 2 — the harness must be shown able to fail before it is trusted.
- *The `if-shell` brace form not surviving the trip through a shell script.* The behaviour
  task's own note says the brace form needs `source-file` or careful nested quoting to install
  from a shell. Expect this to be the fiddly part; if quoting becomes unreadable, writing the
  branch to a temp file and `source-file`-ing it is the documented escape hatch, not a defeat.
- *`go version` parsing.* Output is `go version go1.25.0 darwin/arm64`, but devel and rc
  builds differ. A parse that cannot read the version must fall through to step 4 (bind
  nothing, explain) rather than optimistically building.
- *`$PLUGIN_DIR` resolution.* The script must locate its own directory without assuming the
  cwd tmux happens to have, and without assuming `~/.tmux/plugins/…` — TPM's path is
  configurable. Derive it from `${BASH_SOURCE[0]}`.
- *Committing the exec bit.* `chmod +x` locally is not enough; `git update-index --chmod=+x`
  may be needed, and a non-executable `.tmux` file makes TPM skip the plugin silently. Verify
  with `git ls-files -s` showing mode `100755`.
- *The harness needing a tmux binary in CI.* It does, and that is why it is not in `make
  test`. The target should skip with a clear message rather than fail when `tmux` is absent.

## Evidence
(Added by the executor.)
