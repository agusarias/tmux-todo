# Release Binaries And CI

**Status:** in-progress
**Worktree:** ../todo-release-binaries-and-ci

## Goal
Give the project real release infrastructure: semantic version tags, a CI workflow that runs
the suite on every push, and per-platform `tdo` binaries published as release assets — so the
TPM plugin can download a binary instead of needing a Go toolchain on the user's machine.

## Why
Split out of `tpm-plugin-and-install` on 2026-08-20. That task found the constraint rather
than choosing it: there are **no git tags, no CI, no `.github/`, and `bin/` is gitignored**,
so "download a prebuilt binary" — the best install experience by a wide margin — was not an
option available to it. Its resolution chain therefore ends in a source build gated on
Go >= 1.25, which works but asks every user without a recent toolchain to either install one
or go without.

Two further reasons this is worth its own task rather than a footnote:

- **`VERSION` is currently meaningless.** The Makefile stamps
  `git describe --tags --always --dirty`, and with zero tags that yields a bare commit hash.
  Every build so far has shipped a version like `e91f97b-dirty`. The `-ldflags` machinery is
  in place and correct; it has simply never had a tag to describe.
- **Nothing has ever run the suite outside this machine.** `tmux-regression-guard-ci-proof`
  exists precisely because a regression net was vacuous on a tmux-less runner — and the only
  reason that was a *hypothetical* rather than a caught bug is that there is no CI runner to
  catch it on. CI is what makes that class of finding automatic.

## Constraints
- **The executor must not push, tag, or create a release.** User's Checkpoint 1 ruling. Every
  artifact is written and committed locally; the user pushes. DoD items 1-13 and 15-17 are
  all achievable without a remote. **DoD 14 (`v0.1.0`) and every item needing a real CI run or
  a real release asset move to `2026-08-20-prove-ci-and-cut-v0.1.0.md`** — do not claim them
  here, and do not claim a green CI run that has not happened.
- Must not require CGO. The whole point of `modernc.org/sqlite` is `CGO_ENABLED=0`, so
  cross-compilation is a plain `GOOS`/`GOARCH` matrix with no C toolchain per target.
- CI must run the suite **without a tmux server** — that is the environment the
  `tmux-regression-guard` work is about. The plugin harness (`make test-plugin`) needs tmux
  and should be a separate job that installs it, or be skipped with a clear message.
- Release assets must be verifiable: checksums at minimum.
- Out of scope: a Homebrew formula or tap, publishing to any package registry, and signing or
  notarisation. Each is a real follow-on; none is needed to make the plugin's download path
  work.

## Critical surface
**Residual risk, accepted by the user at Checkpoint 1.** With best-effort verification, a
download proceeds unverified when `checksums.txt` cannot be fetched — so an attacker able to
serve a binary while suppressing the checksums file gets code execution on a tmux server
start. Two things bound it: a positive mismatch still refuses (DoD 8), and the transport is
HTTPS to GitHub, so this requires breaking TLS or compromising the release itself rather than
merely being on the network. Signature verification (minisign/cosign) is the real fix and is
deliberately *not* in this task — it needs key management and belongs in its own brief.
Recorded here so the trade is visible rather than implicit.

Publishing a release is **externally visible and effectively irreversible** — a tag people
have fetched, and assets a plugin may already be downloading. Getting the platform matrix or
the archive layout wrong is a breaking change for anyone who installed in between. The
plugin's download path also introduces a **network dependency at install time**, with the
failure modes (offline, rate-limited, asset missing for the platform) needing deliberate
answers rather than a bare `curl`.

**RESOLVED at grill, 2026-08-20 — see the Decisions log for all four rulings.** Platforms:
`darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`. Precedence: the download goes in
at step 3, ahead of the source build. First tag: `v0.1.0`. CI: the Go suite on ubuntu and
macOS, the plugin harness on ubuntu, and a cross-compile matrix.

## Definition of done

**CI**
1. `.github/workflows/ci.yml` runs on push and pull request with three jobs:
   - **go** on `ubuntu-latest` **and** `macos-latest`: `go test ./...`, `go vet ./...`, and a
     `gofmt -l .` check that fails on any output.
   - **plugin** on `ubuntu-latest`: install tmux, then `make test-plugin`.
   - **build**: cross-compile all four targets, proving the matrix before a tag depends on it.
2. **The go job must run with no tmux on the runner**, and that is asserted rather than
   assumed — a step that fails if `command -v tmux` succeeds. This is the environment
   `tmux-regression-guard-ci-proof` exists for: its guards are the ones that were vacuous on a
   tmux-less machine, and CI is the first time they run on one.
3. Go is pinned to the `go.mod` directive (1.25.0) via `go-version-file: go.mod`, not a
   literal — so the floor the README promises and the floor CI proves cannot drift apart.

**Releases**
4. `.github/workflows/release.yml` triggers on a `v*` tag and publishes, for each of the four
   targets, a binary built `CGO_ENABLED=0` with `-trimpath` and the same
   `-ldflags -X …/internal/cli.Version=<tag>` the Makefile uses — so a released binary's
   `tdo --version` is the tag.
5. **Asset names are a contract the plugin computes**, so they are fixed and documented:
   `tdo-<goos>-<goarch>` (no archive, no version in the name — the URL carries the version).
   A `checksums.txt` of SHA-256 sums is published alongside.
6. A released binary is verified static: no `libsqlite3`, and `tdo doctor` works on a
   throwaway database on the runner for the native target.

**The plugin's download step**
7. `tmux-todo.tmux`'s resolution chain becomes: `$PATH` → `$PLUGIN_DIR/bin/tdo` → **download**
   → `go build` → message-and-no-keybind. The first two steps are unchanged.
8. **Verification is best-effort, per the user's Checkpoint 1 ruling** — with one line the
   ruling does not relax. Fetch `checksums.txt` and SHA-256 the download against it:
   - **sum present and matching** → use the binary.
   - **sum present and NOT matching** → refuse: delete the file, fall through to the source
     build, never execute it. A positive mismatch is still fatal; "best-effort" relaxes what
     happens when the checksums file is *unavailable*, not what happens when it says no.
   - **`checksums.txt` unreachable, 404, or no `sha256sum`/`shasum` on the box** → proceed with
     the download unverified, and say so once via the same `display-message` channel the
     no-binary path uses, so an unverified install is visible rather than silent.

   The curator's recommendation was to make a verified sum mandatory; the user chose
   best-effort so an install is never blocked by a missing checksums file. Recorded as the
   user's call. The residual risk is stated plainly in Critical surface.
9. The download writes to a temp file and moves it into place only after verification, so an
   interrupted or corrupt download can never leave a broken `bin/tdo` that step 2 then trusts
   on the next tmux start.
10. It uses `https://github.com/<owner>/<repo>/releases/latest/download/<asset>`, which
    redirects to the current release — **no GitHub API call**, so no JSON parsing, no `jq`
    dependency and no API rate limit. `curl` or `wget`, whichever exists; neither means the
    step is unavailable, not an error.
11. Every failure mode falls through rather than failing the script: offline, DNS failure,
    404 (no asset for this platform), a 403/rate limit, a checksum mismatch, and no
    downloader present. Each is a harness case.
12. `uname -s`/`uname -m` map to the four asset names, including `arm64` vs `aarch64` and
    `x86_64` vs `amd64`. An unmapped platform skips the step and falls through. Table-tested.
13. The steady-state run makes **no network call at all** — step 2 short-circuits before the
    download is considered. Asserted with the downloader sandboxed to a command that fails
    loudly if invoked, so a regression here is a test failure rather than a silent latency
    cost on every tmux server start.

**The tag**
14. `v0.1.0` is tagged and released once CI is green and the release workflow has been proven.
    `make build` then stamps a real version for the first time — `git describe` has produced a
    bare commit hash for every build in this project's history.

**Docs and sweep**
15. README's install section documents the download path and that a checksum is verified, plus
    manual installation from a release asset for someone not using TPM.
16. `docs/design.md`'s Distribution section records the five-step chain.
17. `make test`, `make test-plugin`, `make lint` clean; `gofmt -l .` empty; `CGO_ENABLED=0
    make build` still static.

## Verification
- **Workflow syntax validated locally** — `actionlint` if available, otherwise a YAML parse
  plus a line-by-line review against the schema. The executor cannot push (see Constraints),
  so a green CI run is **not** available as evidence here and must not be claimed; it moves to
  `2026-08-20-prove-ci-and-cut-v0.1.0.md`.
- `make test-plugin` extended and green, with the new cases named: asset present and verified,
  asset 404, **checksum mismatch**, offline, no downloader, and each `uname` mapping.
- **A mutation proof for DoD 8**: with the checksum check removed, the mismatch case must fail.
  A verification step that is never exercised against a bad file is not a verification step.
- The four cross-compiles run locally with the release workflow's exact flags, and one
  binary's `tdo --version` shown to carry the injected version string — so the ldflags path is
  proven even though no release exists yet.
- A local end-to-end install with **no `go` on `PATH`**, against a **`file://` or local-HTTP
  fixture** standing in for the release asset, reaching a working `prefix + t` purely by
  "download". The same leg against a real GitHub asset moves to the follow-up brief.

## Decisions
- **2026-08-20 (curator, split from `tpm-plugin-and-install`):** deferred out of that task
  deliberately rather than absorbed. Shipping the plugin script does not depend on this, and
  this needs decisions about platforms, cadence and release policy that a packaging task
  should not make on the side.

### 2026-08-20 (curator grill) — one probe, four rulings

**PROBE — cross-compilation is free, so the platform matrix is not a build question.** All six
targets tried (`darwin/arm64`, `darwin/amd64`, `linux/amd64`, `linux/arm64`, `freebsd/amd64`,
`windows/amd64`) build clean with `CGO_ENABLED=0 -trimpath`, at ~11-12MB each. That is
`modernc.org/sqlite` paying off exactly as CLAUDE.md's driver decision predicted — there is no
C toolchain to arrange per target. **So the constraint is tmux's reach, not Go's**: shipping a
`windows/amd64` binary for a tmux plugin would invite bug reports from a configuration that
cannot work, since tmux is POSIX-only and WSL users are `linux/amd64`.

1. **Four targets:** `darwin/arm64` (Apple Silicon), `darwin/amd64` (Intel Macs),
   `linux/amd64` (x86 servers, containers, WSL), `linux/arm64` (Pi, Graviton, ARM runners).
   ~44MB per release, which is nothing. `freebsd/amd64` builds and can be added on request;
   Windows is deliberately never shipped.
2. **The download goes in at step 3**, between the plugin-local binary and the source build.
   The first two steps are untouched, so a user's own `tdo` still wins and an existing plugin
   build is still reused — nothing that works today stops working. A fresh install needs no Go
   toolchain, which is the entire motivation. The source build survives as the offline /
   no-asset fallback rather than being replaced, which is what keeps the chain honest on a
   machine with no network.
3. **First tag is `v0.1.0`, not `v1.0.0`.** `design.md`'s v1 feature cut line closed on
   2026-08-20, so the *features* are complete — but there are zero external users and no
   real-world shakedown. `1.0.0` would commit semver stability on the `--json` contract, the
   scope-key normalisation rules and the CLI surface, which is a real promise to make about
   something nobody has used in anger. `0.x` says "the shape may still move" while finally
   giving `git describe` something real: **every build in this project's history has stamped a
   bare commit hash**, because there has never been a tag.
4. **CI runs the Go suite on ubuntu and macOS, the plugin harness on ubuntu, and a
   cross-compile matrix.** The Go job on a runner with no tmux is the point, not a side
   effect: `tmux-regression-guard-ci-proof` exists because this repo's scope guards were
   vacuous on exactly that machine, and CI is the first time they will ever run there. The
   plugin harness needs tmux, which is one `apt-get` on ubuntu and more trouble on macOS for
   no additional coverage.

**Implementation notes worth not re-deriving.**
- **Use `/releases/latest/download/<asset>`, not the GitHub API.** That URL redirects to the
  current release's asset, so the plugin needs no JSON parsing, no `jq`, and is not subject to
  the API's unauthenticated rate limit — which a plugin sourced on every tmux server start
  would otherwise be a good way to hit.
- **The plugin downloads the *latest* release rather than one pinned to the checkout.** Safe
  here because the script only ever invokes `tdo tui` and `tdo session-renamed`, both stable
  surfaces, and the script is deliberately thin. If the plugin ever depends on a new
  subcommand, this becomes a real compatibility question.
- **Sandbox the downloader in the harness the way `tdo` and `go` already are.** The existing
  harness puts system tool dirs on `PATH` and shadows only the binaries under test, then
  asserts they are genuinely unreachable; the download cases follow that pattern rather than
  inventing a second mechanism.

### 2026-08-20 (executor) — three implementation choices, none touching the brief

- **`$TDO_RELEASE_BASE_URL` is the test seam**, read once inside step 3 so the steady state
  pays nothing for it. An env var rather than a tmux option: an option would be a documented
  user-facing surface to support, and the harness runs the script directly with `env` anyway.
  It doubles as the way to point an install at a mirror, so it is documented in the README as
  that rather than as a test hook.
- **The harness serves fixtures over a real local HTTP server** (`python3 -m http.server` on
  127.0.0.1, kernel-assigned port), falling back to `file://` when python3 is absent. Started
  as `file://` only, which turned out to cost two legs: a missing local file is not an HTTP
  404, and **wget refuses `file://` outright**. The 404 leg is the one that shows `curl -f` is
  load-bearing (see the mutation table), so it was worth a process per fixture.
  Every case gets `$TDO_RELEASE_BASE_URL` pointed at a non-existent path by default, so no
  case can reach github.com — otherwise `resolve-4-nothing` would start failing the day
  `v0.1.0` ships.
- **`chmod 0755`, not `chmod +x`.** `mktemp` creates the temp file `0600` and `+x` on that
  yields `0711`, so the installed binary's mode would have depended on which step installed
  it. Caught by reading the `ls -l` in the end-to-end capture, not by a test.

**CURATOR'S CALL, not asked — flagged at Checkpoint 1.** DoD 8's checksum verification is
non-negotiable and is written as a hard requirement rather than an option. Downloading a
binary over the network and executing it is the single highest-risk thing in this project, and
"verify, or fall through to the source build" is the only shape that does not trade the
project's static-binary discipline for install convenience. If this is thought too strict,
override at Checkpoint 1 — but the alternative is a plugin that runs whatever it fetched.

## Plan
*Awaiting Checkpoint 1.* Two new workflow files, an extension to the existing plugin script and
its harness, one tag, and docs. No Go source change at all.

**Sequencing is forced by a dependency the other orderings hide.** The plugin's download step
cannot be tested against anything real until a release exists, and a release should not be cut
until CI is green. So:

1. **CI first** (`ci.yml`), including DoD 2's assertion that the go job has no tmux — and prove
   that assertion fires by deliberately installing tmux in the job once and watching it fail.
   Cheapest step, and it starts guarding everything after it.
2. **The release workflow** (`release.yml`), proven on a **throwaway pre-release tag** —
   `v0.0.1-test` or similar — so the four assets, the ldflags version stamp and
   `checksums.txt` are all verified before a real tag exists. Delete the test release after.
3. **`v0.1.0`.** Only now, with both workflows proven.
4. **The plugin's step 3**, which can finally be tested against a real published asset as well
   as against harness fixtures.
5. Harness cases, README, `design.md`, sweep.

**Files.**
- `.github/workflows/ci.yml`, `.github/workflows/release.yml` — new.
- `tmux-todo.tmux` — a `download_binary()` between the existing resolve steps. The three
  existing functions are untouched; this is an insertion, not a restructure.
- `test/plugin_install_test.sh` — new cases per DoD 11 and 12, using the existing sandbox
  mechanism.
- `README.md`, `docs/design.md`, `CLAUDE.md`.

**What could go wrong.**
- *The checksum check never being exercised against a bad file.* DoD 8 is the highest-risk item
  and a verification step that only ever sees good input is decoration. The mutation proof —
  remove the check, the mismatch case must fail — is the evidence, not the passing suite.
- *A partial download left in `bin/tdo`.* Step 2 trusts that path on the next tmux start, so a
  truncated file becomes a permanently broken install that the chain never retries. Hence
  download-to-temp-then-move, after verification.
- *A network call sneaking onto the steady-state path.* The plugin runs on **every** tmux server
  start. If the download step is ever reached when `bin/tdo` already exists, every server start
  pays a network round-trip — or hangs offline. DoD 13 sandboxes the downloader to a command
  that fails loudly if invoked, so this is a test failure rather than a slow tmux.
- *`uname -m` naming.* `arm64` on macOS, `aarch64` on most Linux distributions, `x86_64` for
  amd64 everywhere. Getting one wrong means a 404 and a silent fall-through to the source
  build — which *works*, so it would not look broken. Table-test all four.
- *CI's macOS runner being slow or flaky* on the Go job. If it becomes a nuisance, the honest
  fix is to reduce what runs there and say so, not to retry until green.
- *The throwaway pre-release tag leaking into `git describe`.* Delete both the release and the
  tag, locally and remotely, before cutting `v0.1.0`, or every subsequent build stamps a
  version derived from a test tag.
- *This task pushes to a remote for the first time.* Everything so far has been local and
  unpushed; CI cannot run otherwise. That is a step change in blast radius and the point at
  which the 30-odd unpushed commits become public — worth the user's explicit go-ahead as part
  of Checkpoint 1 rather than a side effect of the first workflow file.

## Evidence

Everything below ran on this machine (darwin/arm64, tmux 3.7b, go1.26.6, bash 3.2.57).
**Nothing was pushed, tagged or released**, per the Constraints. Where a DoD item can only
be closed by a real CI run or a real release asset, that is said plainly rather than
implied — those legs belong to `2026-08-20-prove-ci-and-cut-v0.1.0.md`.

### Workflow validation

`actionlint` is not installed on this machine, so it was built into a scratch directory
(`GOBIN=... go install github.com/rhysd/actionlint/cmd/actionlint@latest`, v1.7.12) rather
than skipped:

```
$ actionlint .github/workflows/ci.yml .github/workflows/release.yml
$ echo $?
0
```

**And the check is not vacuous.** A copy with `matrix.os` misspelled and the `setup-go`
ref dropped is rejected, so actionlint really is reading these files:

```
.github/workflows/ci.yml:31:18: property "oss" is not defined in object type {os: string} [expression]
.github/workflows/ci.yml:35:15: specifying action "actions/setup-go" in invalid format because ref is missing [action]
exit=1
```

`shellcheck` is absent, so actionlint skipped the `run:` bodies. Each was extracted and
handed to `bash -n` instead — syntax only, but that is the failure a workflow cannot
recover from:

```
ok   ci.yml       go         Assert the runner has no tmux
ok   ci.yml       go         make lint
ok   ci.yml       go         make test
ok   ci.yml       plugin     Install tmux
ok   ci.yml       plugin     make test-plugin
ok   ci.yml       build      Build ${{ matrix.goos }}/${{ matrix.goarch }}
ok   release.yml  release    Build the four targets
ok   release.yml  release    Verify the linux/amd64 binary is static and works
ok   release.yml  release    checksums.txt
ok   release.yml  release    Publish the release
run: bodies with bash syntax errors: 0
```

**DoD 2's assumption, checked rather than assumed.** The assertion fails the `go` job when
`command -v tmux` succeeds, so it is only useful if the runner images ship without tmux —
otherwise it turns CI red on arrival. Both readmes in `actions/runner-images` were read (Ubuntu 24.04 and macOS 15
arm64, 2026-08-20): **neither lists tmux**; both list `curl` and `wget`. The workflow
comment records this and says the remedy is to *mask* tmux, never to delete the assertion.

### The release build, run locally with release.yml's exact flags

`CGO_ENABLED=0 GOOS=… GOARCH=… go build -trimpath -ldflags "-s -w -X …/internal/cli.Version=v0.1.0-localproof"`:

```
tdo-darwin-arm64       7631 KB  Mach-O 64-bit executable arm64
tdo-darwin-amd64       7816 KB  Mach-O 64-bit executable x86_64
tdo-linux-amd64        7692 KB  ELF 64-bit LSB executable, x86-64, ..., statically linked
tdo-linux-arm64        7488 KB  ELF 64-bit LSB executable, ARM aarch64, ..., statically linked
```

`file` reporting **"statically linked"** for the linux targets is what release.yml's static
check greps for, so that grep is known to match a real build rather than a guess.

`checksums.txt`, written by the same command release.yml uses, and verified:

```
$ sha256sum tdo-darwin-arm64 tdo-darwin-amd64 tdo-linux-amd64 tdo-linux-arm64 > checksums.txt
$ sha256sum -c checksums.txt
tdo-darwin-arm64: OK
tdo-darwin-amd64: OK
tdo-linux-amd64: OK
tdo-linux-arm64: OK
```

**The ldflags path is proven even with no release in existence** — the version comes out the
other end:

```
$ ./tdo-darwin-arm64 --version
v0.1.0-localproof

$ otool -L tdo-darwin-arm64          # CLAUDE.md's driver invariant
  /usr/lib/libSystem.B.dylib
  /usr/lib/libresolv.9.dylib
libsqlite3 lines: 0

$ ./tdo-darwin-arm64 doctor --db "$(mktemp -d)/tasks.db"
tdo      v0.1.0-localproof
runtime  go1.26.6 darwin/arm64
schema   2 (latest 2)
journal  wal
tasks    0 pending, 0 total
ok
```

That is DoD 6's pair of checks (static, and `doctor` on a throwaway database) run against
the **native darwin/arm64 asset** on this machine. The *runner's* leg — the same two checks
on `tdo-linux-amd64` on ubuntu — is written into release.yml and has not run.

### The plugin harness

`make test-plugin`, with the fixture release served over real local HTTP:

```
plugin script : .../tmux-todo.tmux
tmux          : /opt/homebrew/bin/tmux (tmux 3.7b)
bash          : /bin/bash (GNU bash, version 3.2.57(1)-release (arm64-apple-darwin24))
go            : /opt/homebrew/bin/go (usable for the build case: yes)
host asset    : tdo-darwin-arm64
sha256 tool   : sha256sum
downloader    : curl=/usr/bin/curl wget=none
fixture server: /opt/homebrew/bin/python3
...
plugin harness: 118 passed, 0 failed
```

**55 before this task, 118 after** — the pre-change harness was run from
`git show HEAD:test/plugin_install_test.sh` to get that baseline, so 63 assertions are new.

The named cases from the Verification section, all green:

| case | covers |
|---|---|
| `dl-1-verified` | asset present, sum matches, keybind installed, second run does not re-download |
| `dl-2-checksum-mismatch` | **the sum disagrees** → deleted, nothing installed, no keybind |
| `dl-3-asset-404` | a real HTTP 404 for this platform |
| `dl-4-no-checksums-file` | `checksums.txt` absent → used unverified, `display-message` says so |
| `dl-5-asset-absent-from-checksums` | `checksums.txt` present but no line for us → same |
| `dl-6-offline` | downloader exits non-zero; canary proves it was invoked |
| `dl-7-http-error` | curl exit 22 / wget exit 8 — the 403-or-rate-limit shape |
| `dl-8-no-downloader` | neither `curl` nor `wget` on `$PATH` at all |
| `dl-8b-no-downloader-control` | **the same sandbox with curl DOES download** — dl-8 is not vacuous |
| `dl-10-uname-*` (6 rows) | `Darwin`/`Linux` × `arm64`/`aarch64`/`x86_64`/`amd64` → the right asset |
| `dl-11-unmapped-*` (2 rows) | `FreeBSD/amd64`, `Linux/riscv64` → step skipped, falls through |
| `dl-12`, `dl-13` | `$PATH` and an existing `bin/tdo` both beat the download |
| `dl-14` | the download beats `go build` even with a usable go present |
| `dl-15` | a *failed* download still leaves the source build its turn |
| `steady-state-no-network` | booby-trapped curl+wget: **neither is ever invoked** |

`dl-9-wget-only` **SKIPPED — there is no `wget` on this machine.** The wget branch of
`fetch()` is therefore unexercised here; the case is written and will run on CI's ubuntu
runner, whose image lists wget. Recorded as a real gap rather than a passing row.

### Mutation proofs

A green suite proves nothing about a guard that never sees bad input. Each mutation is one
targeted edit to a copy of the script, run through the unmodified harness via
`PLUGIN_SCRIPT=`:

| mutation | result |
|---|---|
| **DoD 8:** delete `[ "$want" = "$got" ] \|\| return 1` | **114 passed, 4 failed** — all four in `dl-2`: the mismatching download is installed, a keybind and a hook point at it. `dl-1` still passes, so the edit hit the guard and nothing else. |
| step 3 never called from `resolve_binary` | 90 passed, **28 failed** — every download case collapses; `dl-14` falls back to a build |
| step 3 moved *ahead* of the `bin/tdo` check | 113 passed, **5 failed** — `dl-13` (the existing binary is overwritten) and `steady-state-no-network` (`curl was never invoked` fails: the canary exists) |
| `curl -fsSL` → `curl -sSL` (no `-f`) | 116 passed, **2 failed** — `dl-3-asset-404`: the 404 **page** is installed as `bin/tdo` and a key is bound to it |

The last one is why the fixture is served over HTTP rather than `file://`: only a real 404
can demonstrate that `-f` is load-bearing.

**DoD 9 is covered as far as a harness can reach, and not further.** `assert_no_temp_leftover`
holds three cases to leaving no `.tdo.*` behind, and mutation 1 is what shows nothing reaches
`bin/tdo` *before* verification — with the comparison gone, it does. Interrupting a download
mid-flight is not something this harness can stage, so the atomicity of the same-filesystem
`mv` rests on the code and its comment, not on a test.

### Local end-to-end: no Go toolchain, downloaded binary, real `prefix + t`

The one leg that ties it together. A sandbox `$PATH` with **no `go` and no `tdo`**, the real
release binary served over local HTTP, and a nested client (`TMUX= tmux -L … attach`) so the
popup can actually be opened and captured:

```
sandbox check (this is the 'no Go toolchain' claim):
  go on PATH  : NONE
  tdo on PATH : NONE
  curl        : /usr/bin/curl

== running the plugin (TPM's job) ==
  tmux-todo: no tdo binary found; downloading tdo-darwin-arm64 from http://127.0.0.1:57799 (this happens once)

downloaded binary:
  -rwx--x--x  1 agusarias  staff  7814338 tdo
  --version -> v0.1.0-localproof
  sha256 matches the release asset: yes

== pressing prefix + t in the nested client ==
  |⌁        ┌──────────────────────────────────────────────────────────┐main ✔
  |         │╭────────────────────────────────────────────────────────╮│
  |         ││  tdo                                                   ││
  |         ││                                                        ││
  |         ││  ▸ ◉ downloaded, not built                   (global)  ││
  |         ││                                                        ││
  |         ││  j/k move · space done · ? keys · q quit               ││
  |         │╰────────────────────────────────────────────────────────╯│
  |         └──────────────────────────────────────────────────────────┘
```

The task in that popup was added through the downloaded binary. The `-rwx--x--x` in that
capture is why the script now does `chmod 0755` rather than `chmod +x`: `mktemp` creates the
file `0600` and `+x` turns that into `0711`, which is not the mode step 4's `go build`
produces. Fixed after this capture; the harness is green either way.

### Sweep (DoD 17)

```
$ make lint          # go vet ./... + gofmt -l . check
go vet ./...
exit=0

$ gofmt -l .
(empty)

$ make test
?   github.com/agusarias/tmux-todo/cmd/tdo  [no test files]
ok  github.com/agusarias/tmux-todo/internal/cli      0.803s
ok  github.com/agusarias/tmux-todo/internal/scope    0.801s
ok  github.com/agusarias/tmux-todo/internal/store    0.474s
ok  github.com/agusarias/tmux-todo/internal/task     0.903s
ok  github.com/agusarias/tmux-todo/internal/tui      6.126s

$ CGO_ENABLED=0 make build
go build -trimpath -ldflags '-s -w -X …/internal/cli.Version=786d22a-dirty' -o bin/tdo ./cmd/tdo
libsqlite3 lines in otool -L: 0
bin/tdo --version: 786d22a-dirty
```

That last line is the brief's own claim confirmed from the other side: with no tags,
`git describe` yields a bare commit hash, so **every build in this project's history has
stamped a version like `786d22a-dirty`.** DoD 14 is what fixes it, and DoD 14 is not this
task's to close.

### Definition of done

| # | | note |
|---|---|---|
| 1 | ✅ written | three jobs as specified; actionlint clean. **Not run** — no push. |
| 2 | ✅ written | assertion present; both runner images confirmed tmux-free from their readmes. **Not run.** |
| 3 | ✅ | `go-version-file: go.mod` in all three jobs and in release.yml |
| 4 | ✅ | flags proven locally on all four targets; `--version` carries the injected string |
| 5 | ✅ | `tdo-<goos>-<goarch>`, no archive, no version in the name; `checksums.txt` verified locally |
| 6 | ⚠️ partial | both checks pass against the **native** asset here; the runner's linux/amd64 leg is written and unrun |
| 7 | ✅ | five-step chain; `dl-12`/`dl-13`/`dl-14`/`dl-15` pin the ordering in both directions |
| 8 | ✅ | all three outcomes cased; **mutation-proven** |
| 9 | ✅ mostly | temp-in-destination + post-verification `mv`; see the DoD 9 note above for what a harness cannot show |
| 10 | ⚠️ partial | curl leg proven; **wget leg unexercised — no wget on this machine** |
| 11 | ✅ | offline, HTTP error, 404, mismatch, no downloader, unmapped platform — six cases, all falling through |
| 12 | ✅ | 6 mapping rows + 2 unmapped rows |
| 13 | ✅ | booby-trapped downloader; **mutation-proven** |
| 14 | ⛔ **not claimed** | moves to `2026-08-20-prove-ci-and-cut-v0.1.0.md` per Constraints |
| 15 | ✅ | README: the five-step chain, the three verification outcomes, and a manual release-asset install whose `shasum -c` one-liner was run (and shown to fail on a corrupted file) |
| 16 | ✅ | `docs/design.md` Distribution: five-step chain, the asset contract, the verification asymmetry, CI, and why `v0.1.0` |
| 17 | ✅ | see Sweep |

**What a reviewer should read closely.** `tmux-todo.tmux` is the only file here that runs on
a user's machine and executes something it fetched from the network: `download_binary`,
`verify_download` and `fetch` deserve line-by-line reading. The two workflow files are
untested-in-anger by construction and are the follow-up task's subject. `README.md`,
`docs/design.md` and `CLAUDE.md` are prose. `test/plugin_install_test.sh` is the largest
diff and the evidence above is what carries it.
