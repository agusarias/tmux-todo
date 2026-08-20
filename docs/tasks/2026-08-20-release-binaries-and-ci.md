# Release Binaries And CI

**Status:** ready
**Worktree:** none

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
(Added by the executor.)
