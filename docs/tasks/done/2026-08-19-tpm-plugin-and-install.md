# TPM Plugin And Install

**Status:** done
**Worktree:** none (merged and removed)

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
4. The steady-state run (binary already resolvable at step 1 or 2) does no build, no
   network, and no write, and produces no output at all. Its cost is bounded at ~50ms per
   tmux **server** start — not per session, window, keypress or popup. Measured, with the
   number recorded.

   *(Reworded at Checkpoint 2, 2026-08-20, with the user's sign-off. The original clause said
   "adds no measurable time to tmux startup", which was unachievable by construction: any
   plugin that talks to tmux at all pays a round-trip per call. The curator wrote a clause
   that could not be satisfied; the executor measured ~31ms across four round-trips and
   escalated rather than quietly satisfying or dropping it, which is the correct handling. The
   substance the clause was reaching for — no build, no network, silent — holds and is
   proven.)*

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

### 2026-08-20 (executor) — five `how` revisions, one clause flagged

All within the plan's drift clause; the Goal, Constraints and DoD were not touched.

5. **The body grep is PATH-SPECIFIC, which is what makes Decision 3's own accepted gap
   true.** Decision 3 wrote the guard as `show-hooks -g | grep -q 'tdo session-renamed'` and
   then recorded the accepted gap as "a moved plugin dir can leave one stale hook alongside
   the new one". Those two halves contradict each other: a path-*blind* grep matches the
   stale hook from the old path and therefore **skips installing the new one**, leaving only
   the broken hook — strictly worse than the gap as described. Grepping for the body
   *including this binary's path* is what produces the recorded behaviour: same path
   re-run → no duplicate; moved path → one harmless stale hook beside a working one.
   Implemented that way; the accepted gap stands exactly as written.
6. **The brace form needs no `source-file` and no nested-quoting gymnastics.** The plan
   expected this to be "the fiddly part" and named a temp file as the escape hatch. Measured
   instead: passing the whole `if-shell { … }` block as ONE shell argument to `bind-key`
   hands tmux's own parser exactly the text it wants, and `list-keys` renders it
   byte-identically to `source-file`-ing the same snippet. No temp file, so the steady-state
   path writes nothing to disk. The integration task's handoff note is corrected in
   CLAUDE.md.
7. **The two reads are NOT combined into one tmux call, and that is a measurement, not
   laziness.** `tmux show-option -gqv @x \; show-hooks -g` looked like a free round-trip
   saving. Probed: `show-option -gqv` on an *unset* option prints **nothing at all**, not
   even a newline — so with `@todo-key` unset, line 1 of the combined output is
   `after-bind-key`, the first hook, and would be read as the key. Rejected.
8. **A path containing a quote is a resolution failure, not a keybind.** tmux's
   single-quoted strings have no escape character, so such a path cannot be embedded in
   either command. Rather than install a keybind that breaks when pressed, the script says so
   and binds nothing — the same reasoning as step 4 of the chain, which is why it is folded
   into that outcome rather than being a fifth one.
9. **The step-3 build stamps the version** the same way `make build` does
   (`git describe --tags --always --dirty`, falling back to `dev`), since `Version` is what
   the popup's `?` overlay shows. It costs one `git` call on the build path only, which runs
   at most once, and never on the steady-state path. Confirmed in the end-to-end leg: the
   plugin-built binary reports `41646a6`.

**Also worth recording:** the script is written for **bash 3.2**, because that is the only
bash on macOS (`/bin/bash`, 3.2.57) and a plugin cannot assume a newer one. No associative
arrays, no `${var^^}`.

**FLAGGED FOR CHECKPOINT 2 — DoD 4's "no measurable time" clause is false as written.**
Not a `blocked`, because it needs a *choice* rather than an answer, and because the
`tmux-integration-and-rename-hook` precedent — a DoD clause measurement showed to be false,
completed, recorded, and explicitly left for the curator — was approved as the right handling.
Measured: the plugin adds **~31ms** (median) to a tmux **server** start, once per server
start, not per session, window, or popup. That is four tmux round-trips at ~7.5ms each
(`show-option`, `show-hooks`, `bind-key`, `set-hook`). Everything else in DoD 4 holds: no
build, no network, nothing written to disk, and the run is completely silent.

The floor is not zero for any plugin that talks to tmux at all. Three ways out, none of them
the executor's to pick:
- **accept it and reword the clause** to name the measured number (~31ms/server start), which
  is what the item's own "Measured, with the number recorded" already implies;
- **spend complexity to reach ~23ms** by combining the two *writes* into one invocation
  (`bind-key … \; set-hook …`) — the two reads cannot be combined, see revision 7. This
  breaks the clean three-function shape the plan approved and does not make the clause true;
- **~15ms** would need `@todo-key` read by `display-message -p '#{@todo-key}'` instead of
  `show-option -gqv`, which DoD 5 mandates by name — so it is a DoD change either way.

**SPUN OUT:** release infrastructure — git tags, a CI workflow, and per-platform release
binaries the plugin can download — is the strictly better distribution story and is now its
own brief rather than being smuggled in here.

## Plan
**Approved at Checkpoint 1 on 2026-08-20** as written, including DoD 10's `go.mod` drop to
`go 1.25.0`, the four-step resolution chain, and the curator's call to keep the shell harness
in its own `make test-plugin` target. The plan is disposable — revise the *how* and log why.
Goal, Constraints and Definition of done are not: changing those is a scope event, so set
`blocked` with the question instead.

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

All output below is real, from the worktree `../todo-tpm-plugin-and-install` on tmux 3.7b,
go1.26.6, bash 3.2.57, macOS arm64. Every tmux leg runs on a private socket (`tmux -L`) with
`-f /dev/null`, so the developer's own `~/.tmux.conf` contributed no keybinds or hooks; the
end-to-end leg redirects `XDG_DATA_HOME` to a temp dir, so the real database was never opened.

### `make test-plugin` — 45 assertions, all four resolution outcomes (DoD 2, 9)

```
$ make test-plugin
bash test/plugin_install_test.sh
plugin script : .../tmux-todo.tmux
tmux          : /opt/homebrew/bin/tmux (tmux 3.7b)
bash          : /bin/bash (GNU bash, version 3.2.57(1)-release (arm64-apple-darwin24))
go            : /opt/homebrew/bin/go (usable for the build case: yes)

== resolve-1-path                        [outcome 1 of 4]
    ok   one popup keybind on t
    ok   and it is the only binding on t
    ok   keybind points at the PATH binary
    ok   one tdo session-renamed hook
    ok   hook points at the PATH binary
== resolve-2-plugin-bin                  [outcome 2 of 4]
    ok   one popup keybind on t
    ok   keybind points at the plugin binary
    ok   one tdo session-renamed hook
== resolve-2-plugin-bin-not-executable
    ok   NO popup keybind on t
== resolve-path-beats-plugin-bin
    ok   PATH wins over plugin bin
    ok   plugin bin not used
== resolve-3-go-build                    [outcome 3 of 4]
    ok   precondition: no binary before the run
    ok   go build produced an executable bin/tdo
    ok   one popup keybind on t
    ok   keybind points at the built binary
    ok   one tdo session-renamed hook
    ok   the built binary runs (--version exits 0)
    ok   a second run does not rebuild
== resolve-4-nothing                     [outcome 4 of 4]
    ok   NO popup keybind installed
    ok   no hook installed
    ok   a display-message was issued
    ok   the message names the plugin
    ok   the diagnostic names the binary
== resolve-4-go-too-old
    ok   NO popup keybind installed
    ok   no build attempted
    ok   the diagnostic names the required version
== resolve-4-go-unparseable
    ok   NO popup keybind installed
    ok   no build attempted
== resolve-4-go-build-fails
    ok   NO popup keybind installed
    ok   the diagnostic names the binary
== key-default
    ok   defaults to t when @todo-key is unset
== key-empty-option
    ok   defaults to t when @todo-key is empty
== key-override
    ok   binds w when @todo-key is w
    ok   and installs no popup binding on t
    ok   the override still opens the popup
[idempotence and steady-state cases below]
----------------------------------------
plugin harness: 45 passed, 0 failed
```

Note `resolve-4-go-build-fails`: a `go` new enough to pass the version gate whose *build*
produces nothing. The script's post-build check is `[ -x bin/tdo ]`, not `did go build exit
0`, because the binary existing is the condition the keybind actually depends on.

### Idempotence, as real output rather than an assertion (DoD 7)

Three consecutive runs, printing the greps after each — this is the harness's own output:

```
== idempotence-three-runs
    -- after run 1
       show-hooks -g, tdo hooks   : 1  session-renamed[0] run-shell -b ".../path/tdo session-renamed"
       list-keys -T prefix, key t : 1 popup binding(s), 1 binding(s) total
    -- after run 2
       show-hooks -g, tdo hooks   : 1  session-renamed[0] run-shell -b ".../path/tdo session-renamed"
       list-keys -T prefix, key t : 1 popup binding(s), 1 binding(s) total
    -- after run 3
       show-hooks -g, tdo hooks   : 1  session-renamed[0] run-shell -b ".../path/tdo session-renamed"
       list-keys -T prefix, key t : 1 popup binding(s), 1 binding(s) total
    ok   exactly one tdo hook after three runs
    ok   exactly one popup keybind on t after three runs
    ok   and t carries no second binding
```

And a user's own hooks, after three runs of the install (DoD 8):

```
== user-hook-survives
    -- show-hooks -g after three runs:
       session-renamed[0] display-message "user hook still here"
       session-renamed[1] run-shell "touch .../canary"
       session-renamed[2] run-shell -b ".../path/tdo session-renamed"
    ok   the user's display-message hook survives
    ok   the user's run-shell hook survives
    ok   exactly one tdo hook alongside them
    ok   the user's hook still FIRES after the install
```

The firing is a real `rename-session` on a real server, observed through a `run-shell touch`
canary — `display-message` cannot be observed on a server with no attached client, which is
also why the step-4 message is asserted through tmux's command log.

### The guard is load-bearing: two mutations (DoD 7's verification)

Idempotence tests pass trivially against a script that installs nothing, so the guard was
re-run against mutated copies of the real script (`PLUGIN_SCRIPT=<mutant> make test-plugin`):

| mutation | result |
|---|---|
| the body-grep guard **deleted** | hooks stack `[0] [1] [2]` — `1`, `2`, `3` after runs 1/2/3; **2 assertions fail** |
| the guard greps the hook's **name** instead of its body | the hook is **never installed at all** — 0 hooks on every run; **6 assertions fail** |
| (the real script) | 45 passed, 0 failed |

The second mutation is the one worth keeping: it is Finding 3's trap in code. `show-hooks -g`
prints a bare `session-renamed` line even when zero hooks are installed, so a name grep
matches on a *fresh* server and the install is silently skipped — the failure direction that
leaves the rename broken with no error anywhere. The body grep is what discriminates, in both
directions.

**The harness was also shown able to fail before it was trusted.** Written against a
do-nothing stub script first, per the plan's step 2, which caught two bugs that would have
made the entire suite vacuous:

- `env -i PATH=<stubs only>` left no `bash` on the sandbox PATH, so **the script never ran at
  all** and 22 assertions were "failing" for the wrong reason;
- and no `grep`, so the hook guard silently never executed;
- worse, tmux's **default** prefix table already binds `t` (clock-mode) and `w`
  (choose-tree), so counting bindings for a key counted tmux's own and passed with the plugin
  deleted. Every key assertion now counts bindings whose *command* is the plugin's
  (`display-popup`), plus a total-for-the-key check so a second binding is still caught.

The sandbox now carries the system tool dirs and sandboxes only `tdo` and `go` — and asserts
at startup that neither is reachable in `/usr/bin`, `/bin`, `/usr/sbin`, `/sbin`, so the suite
fails loudly instead of degrading if `tdo` gets installed system-wide later.

### Outcome 4 demonstrated: no binary, no `go`, no keybind (DoD 3)

```
$ env -i HOME=$HOME PATH=<tmux shim>:/usr/bin:/bin ./tmux-todo.tmux
tmux-todo: no usable tdo binary (no 'go' on $PATH to build it with). Install tdo on $PATH,
or run 'make build' in /…/msg/plugin. No keybind installed.

$ tmux -L tdomsg list-keys -T prefix | awk '$4=="t"'
bind-key    -T prefix t       clock-mode
```

`t` still carries tmux's own `clock-mode` — the plugin bound nothing rather than binding
something that would fail on press.

### The Go floor sweep (DoD 10, 13)

```
$ sed -i '' 's/^go 1.26$/go 1.25.0/' go.mod && go mod tidy
$ git diff go.mod
-go 1.26
+go 1.25.0
$ git diff --stat go.sum
                              # empty: byte-identical
$ make test
ok  github.com/agusarias/tmux-todo/internal/cli     0.910s
ok  github.com/agusarias/tmux-todo/internal/scope   0.935s
ok  github.com/agusarias/tmux-todo/internal/store   1.065s
ok  github.com/agusarias/tmux-todo/internal/task    1.116s
ok  github.com/agusarias/tmux-todo/internal/tui     6.073s
$ make lint                   # go vet ./... + gofmt -l .
(clean)
$ gofmt -l .
(empty)
$ CGO_ENABLED=0 make build && otool -L bin/tdo
bin/tdo:
    /usr/lib/libSystem.B.dylib
    /usr/lib/libresolv.9.dylib     # no libsqlite3
```

`go mod tidy` normalised `1.25` to `1.25.0` by itself, as the curator's probe predicted. The
curator's verification reproduced exactly; this item was confirmation, not discovery.

### DoD 4 — the steady-state measurement, and the clause that does not hold

No build, no network, nothing written to disk, and the run is silent (asserted:
`the steady-state run is silent` compares the script's whole stdout+stderr to `""`). The
`steady-state-no-build` case masks `go` from `PATH` entirely, so no build *can* have happened.

The timing, measured the way the DoD asks — a tmux **server** start with and without the
plugin in the config, 25 runs each:

```
without plugin  n=25 median=  14.4ms  p90=  19.0ms  min=  13.6ms
with plugin     n=25 median=  45.1ms  p90=  51.1ms  min=  39.2ms
```

**~31ms added, once per tmux server start** — not per session, window, keypress or popup.
That is four tmux round-trips at ~7.5ms each. So "adds no measurable time to tmux startup"
is **false as written**, and it is flagged for Checkpoint 2 rather than quietly satisfied or
quietly dropped; the three ways out, and why none is the executor's to pick, are in the
Decisions log above. Everything else in DoD 4 holds.

### The whole product, from a config, on a real server (the manual end-to-end leg)

A TPM-style `git clone` of this branch into a plugins dir, a config holding exactly what a
user writes, and a server started with it. The clone has no `bin/tdo` (it is gitignored), so
this exercises **outcome 3 in its realistic setting**:

```
### 1. TPM-style clone
    mode: -rwxr-xr-x   tmux-todo.tmux          # the exec bit survived the clone
    bin/tdo present in the clone? no - so the plugin must build it

### 2. the config
    set -g @todo-key 't'
    run-shell '/…/e2e/plugins/tmux-todo/tmux-todo.tmux'

### 3. what the plugin did, from a cold clone
    binary the plugin built:
       -rwxr-xr-x 7814338 bytes  bin/tdo
      version stamp: 41646a6
      static? /usr/lib/libSystem.B.dylib /usr/lib/libresolv.9.dylib
    keybind:
      bind-key -T prefix t  if-shell -F "#{m:-*,#{e|-|:#{client_width},100}}" { … }
    hook:
      session-renamed[0] run-shell -b "/…/bin/tdo session-renamed"

### 4. tasks seeded from inside the session, so session scope resolves
      1|session:work|rebase onto main
      2|global:|call the dentist
```

`prefix + t`, pressed for real (`send-keys -t outer C-b t`) against a manufactured 80x24
attached client, captured with `capture-pane`:

```
         ┌──────────────────────────────────────────────────────────┐
         │╭────────────────────────────────────────────────────────╮│
         ││  tdo                                                   ││
         ││                                                        ││
         ││  ▸ ⌘ rebase onto main                 (session: work)  ││
         ││    ◉ call the dentist                 (global)         ││
         ││                                                        ││
         ││                                                        ││
         ││                                                        ││
         ││                                                        ││
         ││                                                        ││
         ││                                                        ││
         ││  j/k move · space done · ? keys · q quit               ││
         │╰────────────────────────────────────────────────────────╯│
         └──────────────────────────────────────────────────────────┘
```

The 60 x 15 floor, honoured through the plugin's own keybind: an 80x24 client, so both
`if-shell` branches take the floored leg and the TUI gets 58 columns. `j` then `space` moved
the cursor and completed a row, and the store agreed
(`select id, text, done where done=1` -> `2|call the dentist|1`); `q` closed it.

The rename hook, fired by a real rename:

```
### 7. the rename hook
    before: global: | session:work
    session map: $0 -> work
    == tmux rename-session -t work renamed ==
    after : global: | session:renamed
    session map: $0 -> renamed
```

And three more `run-shell`s of the script against that same live server — the shape a
tmux restart takes:

```
    tdo hooks after three more runs : 1
    bindings on t                   : 1
    user data intact                : 2 tasks
```

### Definition of done

1. ✅ `tmux-todo.tmux` at the repo root, `#!/usr/bin/env bash` + `set -u`, committed
   **mode 100755** (`git ls-files -s` -> `100755 … tmux-todo.tmux`), and the exec bit
   verified to survive a real `git clone` in the end-to-end leg.
2. ✅ the four-step chain, each outcome exercised by the harness, plus three extra
   failure paths (`go` too old, unparseable `go version`, a build that produces nothing) and
   the ordering case that proves `$PATH` beats a stale plugin-local build.
3. ✅ `resolve-4-*` cases assert **no** binding; demonstrated above with `t` still on
   tmux's `clock-mode`.
4. ⚠️ no build, no network, nothing written, silent — all proven. "**No measurable time**"
   is **false**: ~31ms per tmux server start, measured against a baseline. Recorded, and
   flagged above for a curator ruling.
5. ✅ read via `tmux show-option -gqv @todo-key`; defaults to `t` when unset **and** when
   set to empty; prefix table; override tested, including that it leaves no popup binding
   on `t`.
6. ✅ `@todo-key` is the only option in the script (`grep -c '@todo-' tmux-todo.tmux` -> 1
   occurrence, in `install_keybind`). Geometry unchanged from the signed-off branch.
7. ✅ three runs -> exactly one hook, exactly one binding, shown as output, and proven
   load-bearing by two mutations.
8. ✅ a `display-message` user hook and a `run-shell` user hook both survive three runs,
   and the run-shell one still fires on a real rename.
9. ✅ `test/plugin_install_test.sh`, private `tmux -L` server per case, assertions on
   `list-keys` / `show-hooks`, run by `make test-plugin`. `make test` is untouched and needs
   no tmux; the harness skips with a message when tmux is absent.
10. ✅ `go 1.25.0` via `go mod tidy`, `go.sum` byte-identical, suite + vet + gofmt + static
    build all clean.
11. ✅ README rewritten: what it is, TPM install, manual install, `@todo-key`, the popup's
    keys (taken from `helpLines` in the code, not from the design mock), sesh as an optional
    enhancement, **Go 1.25** as the build floor, and where the data lives including the WAL
    sidecars. The "Status: scaffold" claim and the impossible "Go 1.23" requirement are gone.
12. ✅ `design.md`'s Distribution section records the four-step chain, why it ends in a
    source build, the every-server-start constraint, the `bind-key`-replaces /
    `set-hook -ga`-appends asymmetry, and the measured cost.
13. ✅ `make test`, `make test-plugin`, `make lint` clean; `gofmt -l .` empty;
    `CGO_ENABLED=0 make build` links only `libSystem` and `libresolv`.

**Merged into local `main` as e41a85e** (implementation commit `41646a6`, docs and
evidence in `2f980d6`). The worktree `../todo-tpm-plugin-and-install` has been removed, so
read the diff with `git show 41646a6` or `git log -1 -p e41a85e`. **Not pushed.**
`make test`, `make test-plugin` (45/45), `make lint` and `CGO_ENABLED=0 make build` were all
re-run on `main` after the merge and are green there too; `git ls-files -s` on `main` confirms
both shell files at mode **100755**.

## Close-out — 2026-08-20 (curator, Checkpoint 2 approved)

**Approved as merged.** `e41a85e` stays on local `main`; not pushed.

**All 13 DoD items met**, with item 4 reworded above under the user's sign-off. `design.md`'s
**v1 cut line is now complete**: three scopes, merged popup, add/edit/complete/delete/re-scope,
all-tasks view with sesh jump, rename hook, full CLI with `--json`, TPM plugin.

**Independently reproduced by the curator on current `main`**, not read from the Evidence
section: `make test-plugin` → **45 passed, 0 failed**; `go.mod` at `go 1.25.0` with the Go
suite green; `tmux-todo.tmux` committed **mode 100755**; the harness's own steady-state timing
reported 41ms/run over 20 runs, consistent with the ~31ms delta measured against a baseline.

**The mutation was reproduced too, because it is the item that carries the task.** Idempotence
assertions pass trivially against a script that installs nothing, so the guard had to be shown
load-bearing. Rewriting `install_hook`'s body grep as a *name* grep: **39 passed, 6 failed**,
hook count 0 on every run of every case. That is the trap the grill found, in code, failing in
the dangerous direction — a name grep matches tmux's bare `session-renamed` line on a fresh
server, so the install is silently skipped and the rename is broken with no error anywhere.

**The executor improved on this brief in two places, and was right both times.**

1. **The hook grep is path-*specific*, not path-blind.** The brief specified path-blind and
   explicitly "accepted" a stale-hook gap. That was wrong: with a path-blind grep, an install
   at a new path *matches the stale hook from the old path and skips itself*, leaving only the
   broken one. Path-specific inverts the failure into a harmless stale hook beside a working
   one. The curator's "accepted limitation" was worse than the thing it accepted.
2. **The previous task's handoff claim was disproved.** `tmux-integration-and-rename-hook`
   recorded that the `if-shell` brace form "needs `source-file` or careful nested quoting" to
   install from a shell. It does not: passing the whole block as one argument to `bind-key`
   hands tmux's own parser exactly the text it wants, and `list-keys` renders it
   byte-identically. No temp file, no escaping — a simplification the plan had budgeted
   complexity for.

**The harness was proven able to fail before it was trusted** — the plan's step 2 — and that
caught three bugs that would have made the whole suite vacuous. The best of them: **tmux's
default prefix table already binds `t` and `w`**, so counting bindings for a key counted
tmux's own and **passed with the plugin deleted**. Key assertions now count bindings whose
*command* is the plugin's, plus a total-per-key check. This is the third time in this project
that "write the guard, then delete its subject" has caught a guard that proved nothing.

**Outcome 4 is demonstrated rather than asserted**: with no binary and no `go`, `t` still
carries tmux's own `clock-mode`. The plugin bound nothing rather than binding something that
fails on press, which was DoD 3's whole point.

**Accepted limitations, recorded so they are not mistaken for defects.** A plugin path
containing `'` or `"` is treated as a resolution failure rather than a keybind that breaks on
press, because tmux's single-quoted strings have no escape character. A moved plugin dir
leaves one harmless stale hook beside the working one. And the two *reads* cannot be combined
into one round-trip — `show-option -gqv` on an unset option prints nothing at all, so the
response cannot be split by line; only the two writes could be merged, which was declined as
complexity that would not make the old clause true anyway.

**CLAUDE.md:** no curator additions needed. The executor recorded all of it, including the
bare-`session-renamed`-line trap with both mutation directions and the path-specific
requirement, the `t`/`w` default-binding trap, the sandboxed-`PATH`-is-a-vacuous-test-machine
lesson, the `show-option -gqv` empty-output finding, and the correction to the brace-form
handoff.

**Downstream:** `2026-08-20-release-binaries-and-ci.md` (`draft`) is the natural successor —
the plugin's resolution chain ends in a source build only because there is nothing to
download, and that brief carries the OPEN question of whether a downloaded binary should be
preferred over a source build once releases exist.

**Worktree:** `../todo-tpm-plugin-and-install` removed by the executor before this checkpoint.
