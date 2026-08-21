# Prove CI And Cut v0.1.0

**Status:** done
**Worktree:** none

## Goal
With the workflows written and merged, actually run them: push `main`, get CI green, prove the
release workflow on a throwaway pre-release tag, then cut `v0.1.0` — the project's first real
version — and confirm the plugin can install by download against a genuine release asset.

## Why
Split out of `release-binaries-and-ci` at its Checkpoint 1, on the user's instruction that the
executor must not push, tag, or publish. That task can write and locally validate both
workflows, the plugin's download step and its harness cases; it cannot prove any of it, because
**a workflow that has never run is not evidence** and a download step tested only against a
local fixture has never met GitHub's redirect, its rate limits, or a real asset name.

This is also the point at which the project becomes public: ~37 local commits, unpushed since
the first task. That is a deliberate, user-owned step change in blast radius, which is exactly
why it is not a side effect of writing a YAML file.

## Constraints
- Depends on `release-binaries-and-ci` being `done`. If it is not, set `blocked`.
- **This is a curator-run task and must never be set `ready`.** Ruled at Checkpoint 1,
  2026-08-20. Every DoD item here is a push, a tag or a publish, and all three are forbidden
  to the executor — so an executor claiming this brief could do nothing but immediately
  `blocked`. taskflow already puts a remote push with "the user's call, via the curator", so
  this is the framework's own shape rather than an exception to it. The curator proposes each
  publish action in an interactive pass, the user approves it, the curator runs it and records
  the result. **If you are an executor reading this: do not claim this task.**
- The throwaway pre-release tag and its release must be deleted — locally **and** remotely —
  before `v0.1.0` is cut, or every subsequent `git describe` derives a version from a test tag.
- No change to the `--json` contract, the scope-key rules, or the CLI surface. Cutting `0.1.0`
  rather than `1.0.0` was chosen precisely so those are not yet frozen — but this task is not
  the place to move them either.

## Critical surface
**Everything here is externally visible and effectively irreversible.** A pushed commit, a tag
someone has fetched, and a published release asset the plugin may already be downloading. Two
specific hazards: a wrong asset name breaks every install until a new release fixes it, and a
deleted-then-reused tag is the classic way to make `git describe` and everyone's local clone
disagree permanently.

## Definition of done
1. `main` pushed to the remote (user's action), and the CI workflow's **first real run green**,
   with the run URL recorded.
2. **DoD 2 of the parent task proven to fire**: a deliberate run with tmux installed in the
   `go` job, showing that job **fail**. The tmux-less assertion is otherwise itself unverified
   — the vacuous-guard trap this project has now hit four times, and the reason
   `tmux-regression-guard-ci-proof` existed at all.
3. The plugin harness job green on `ubuntu-latest`, with tmux installed from apt.
4. The release workflow proven on a **throwaway pre-release tag**: four assets named
   `tdo-<goos>-<goarch>` plus `checksums.txt`, one asset downloaded on a clean machine and run,
   and its `tdo --version` shown to equal the tag.
5. The throwaway tag and release deleted, locally and remotely, and `git describe` confirmed
   not to reference it.
6. **`v0.1.0` tagged and released.** `make build` then stamps a real version for the first time
   in the project's history — verified by running `./bin/tdo --version`.
7. **An end-to-end install by download**: a TPM-style clone on a machine (or container) with no
   `bin/tdo` and **no `go` on `PATH`**, reaching a working `prefix + t` purely via the release
   asset. Includes the checksum path being exercised against the real `checksums.txt`.
8. README's install instructions followed verbatim by someone who has not read the code — or
   failing that, in a clean container — and any step that does not work as written corrected.
9. `make test`, `make test-plugin`, `make lint` clean on the tagged commit.

## Verification
- The CI run URL and the failing-run URL for DoD 2, both recorded.
- The release page's asset list, and a transcript of downloading one asset on a clean machine,
  verifying its checksum by hand, and running `tdo --version` and `tdo doctor`.
- `git describe --tags` before and after the throwaway tag's deletion, proving DoD 5.
- The DoD 7 install transcript, run in a container so "no `go` on `PATH`" is real rather than
  arranged.

## Decisions
- **2026-08-20 (curator, split from `release-binaries-and-ci` at Checkpoint 1):** the user
  ruled that the executor writes the workflows but must not push, tag, or publish. Rather than
  leave the parent task with unprovable DoD items — or worse, invite an executor to claim a
  green CI run it could not have observed — every item requiring a remote moved here.

## Pre-flight audit — 2026-08-20 (curator, before any push)

Done to de-risk step 2: the plugin harness has only ever run on **macOS, bash 3.2, BSD
userland**, and CI's `plugin` job runs it on **Ubuntu, bash 5, GNU coreutils**. A red first run
would be indistinguishable from a broken workflow, so the portability questions were answered
before pushing rather than after.

- **No BSD-only constructs** in `tmux-todo.tmux` or `test/plugin_install_test.sh` — checked for
  `sed -i ''`, `stat -f`, `base64 -D`, `readlink -f`, `date -r`, `grep -P`, `tail -r`,
  `mktemp -t`. None present.
- **The sha256 tool is resolved, not assumed**, on both sides: `sha256sum` then `shasum -a 256`.
  Ubuntu has the first, macOS the second.
- **The host asset is computed from `uname -s`/`uname -m`**, independently of the script under
  test, so on the runner it becomes `tdo-linux-amd64` rather than hunting for a darwin binary.
  A platform that maps to nothing aborts loudly instead of testing nothing.
- **`python3` absence degrades, it does not fail**: the fixture server falls back to `file://`
  with a printed note. ubuntu-latest ships python3, so the HTTP path should hold. *Coverage
  note:* on the `file://` fallback `dl-3-asset-404` loses its teeth, since only a real server
  can return a 404 — the harness header states which mode ran, so a reviewer can tell.
- **tmux version**: the script needs `display-popup`, `#{e|-|:…}` arithmetic, `#{m:…}` and
  `set-hook -ga` — all ≥ 3.2. Ubuntu 24.04 ships 3.4 and the workflow prints `tmux -V`, so the
  actual version is on the record rather than assumed.
- **`dl-9-wget-only` should finally run.** It is `SKIP`ped on this machine for want of `wget`,
  which `release-binaries-and-ci` recorded as a genuine untested branch of `fetch()`. Ubuntu
  has `wget`, so CI closes that gap — one of the concrete things this task buys.

**Conclusion:** no blocker found. If the first run is red, the likeliest causes are runner
image drift (the `go` job's no-tmux assertion) or something inside `apt-get install tmux`,
not a portability defect in the harness.

## Plan
**Approved at Checkpoint 1 on 2026-08-20.** Curator-run, approval per step, status stays
`agreed`. The user also ruled that `docs/tasks/` publishes as-is — the rationale trail is an
asset for an open-source plugin, not a liability.

**Preconditions, established at Checkpoint 1 so they are not discovered mid-task.**
- `gh` 
 is installed and authenticated as `agusarias`; git protocol **ssh**; token scopes
  `gist, read:org, repo`. `repo` covers creating a release.
- **`workflow` scope is absent.** That scope gates pushing `.github/workflows/*` over **HTTPS**
  with a token; pushes here go over SSH, where the key governs and the gate does not apply. If
  a push of the workflow files is ever rejected for scope, that is the cause and the fix is
  either an SSH remote (already the case) or adding the scope — **not** deleting the workflow.
- `origin/main` is at `bf9fed1` (the `cli-surface` review commit), so the repo already exists
  publicly and this push is ~47 commits, not a first publication.
- **No tags exist on the remote**, confirming `git describe` has never had one to describe.

**Step order, each one a propose/approve/run cycle.**

1. **Push `main`.** Nothing irreversible in itself — the branch already exists publicly — but
   it is what makes ~47 commits and the whole `docs/tasks/` trail visible. Report the range
   pushed.
2. **Watch CI's first real run.** `gh run watch` / `gh run view`. Record the URL. If it fails,
   that is not a setback: a first CI run on a repo that has never had one is expected to
   surface something, and fixing it is part of this task rather than a scope event.
3. **Prove the tmux-less assertion fires** (DoD 2). Deliberately add tmux to the `go` job on a
   throwaway branch and confirm that job **fails**. This is the item most likely to be skipped
   as "obviously fine", and it is the whole reason
   `tmux-regression-guard-ci-proof` existed — an assertion nobody has watched fail is an
   assertion nobody has tested. Delete the branch after.
4. **Prove the release workflow on a throwaway pre-release tag** (`v0.0.1-test`). Four assets
   plus `checksums.txt`, one asset downloaded and its `--version` shown to equal the tag.
5. **Delete the throwaway tag and its release, locally and remotely**, and confirm with
   `git describe --tags` that nothing references it. Doing this *before* step 6 is not
   cosmetic: a lingering `v0.0.1-test` would make every subsequent `git describe` derive from
   it, so every build would stamp a version derived from a test tag.
6. **Cut `v0.1.0`** — annotated tag, pushed, release published. Then `make build` and show
   `./bin/tdo --version` reporting a real version for the first time in the project's history.
7. **Verify install-by-download** (DoD 7): a container or clean environment with no `go` on
   `PATH`, a TPM-style clone, reaching a working `prefix + t` purely from the release asset.
   This is the user this whole release exists for, and it is the only step that proves the
   parent task's download path against a real GitHub asset rather than a fixture.
8. **Walk the README's install instructions verbatim** (DoD 8) and fix anything that does not
   work as written.

**What could go wrong.**
- *Step 3 being skipped.* It is the one step with no user-visible payoff, and it is the one
  guarding against the exact vacuity this project has hit four times. Do it before the tag,
  while there is still appetite for it.
- *The throwaway tag surviving.* Covered by step 5, called out again because `git describe`
  failures are silent and permanent-feeling: every build stamps the wrong thing and nobody
  notices until a release.
- *`v0.1.0` cut while the hook bug is open.* `session-renamed-hook-targets-wrong-session` is
  `ready` and unfixed — the rename hook silently does nothing. Cutting a release with a
  headline feature known-broken is a judgment call, not a technicality: either the fix lands
  first, or the release notes say plainly that rename-following does not work yet and re-home
  from the all-tasks view is the workaround. **Raise this with the user at step 6 rather than
  deciding it here** — it is a product call about what `0.1.0` claims.
- *CI cost and flakiness on the macOS runner.* If it is a nuisance, reduce what runs there and
  say so; do not retry until green.
- *Doing steps 1-2 before the parent task merges.* There are no workflow files yet:
  `release-binaries-and-ci` is `in-progress`. Step 1 can happen any time; step 2 cannot happen
  until the workflows exist on `main`.

## Evidence

### Steps 1-3 complete: pushed, and CI is green (DoD 1, 2, 3)

**Step 1 — pushed.** `bf9fed1..0335701`, 57 commits, 52 files, +13323/-322. The repo was
already public at `bf9fed1`, so this published the backlog rather than the project.

**CI's first run ever failed, twice, and both failures were this repo's own anti-vacuity
guards refusing to run tests that would have proved nothing.** Neither was a defect:

| run | job | what happened |
|---|---|---|
| `32409675065` | `go (ubuntu-latest)` | `tmux is on this runner at /usr/bin/tmux` — the assertion fired |
| `32409675065` | `plugin harness` | `/usr/bin/go exists, so the sandbox cannot control whether go resolves. Aborting.` |

`go (macos-latest)` and all four cross-compiles passed on that first run.

**This satisfies DoD 2 with a real experiment rather than a contrived one.** The item asked for
a deliberate run with tmux installed in the `go` job, showing that job fail. Run `32409675065`
*is* that run — `actions/runner-images`' README does not list tmux, so the image supplied the
condition unprompted and the assertion caught it. An assertion nobody has watched fail is an
assertion nobody has tested; this one has now been watched failing, on the real runner, for
the real reason.

**Fix 1 — mask, never delete** (`22ccf0d`). Both failing steps' own comments prescribed
masking. The `go` job now masks tmux (bounded loop, re-checks, errors if it cannot) and the
`plugin` job masks `tdo`/`go` out of `/usr/bin`, `/bin`, `/usr/sbin`, `/sbin` — then *proves
setup-go's toolchain still resolves*, because the go-build resolution case needs a usable `go`
on the normal PATH at the same time as no `go` in the system dirs. Result: `go (ubuntu-latest)`
green, and the harness got past its abort into the tests themselves.

**Fix 2 — a version-dependent assertion mechanism** (`26c8ab8`). Run `32410008830` left the
harness at **118 passed, 3 failed**, all three reading `tmux show-messages` to prove the
`display-message` channel fired. tmux **3.4** (ubuntu's apt tmux) cannot answer that on a
client-less server; **3.7b** (the dev machine) can. The plugin was never at fault — `warn()`
writes to stderr *and* `display-message`, and the stderr half was already asserted beside each
of the three and already passing on Linux. So the harness now detects the capability at
startup, reports it in the header beside `wget` and `python3`, and skips loudly where the
question is unanswerable. **Both branches were exercised before pushing** — observable → 118/0
unchanged, probe forced off → 115/0 with both SKIP notices — so the skip path is not untested
code.

**Run `32410474435`: all seven jobs green.**

```
✓ go (ubuntu-latest)          ✓ cross-compile (darwin, arm64)
✓ go (macos-latest)           ✓ cross-compile (darwin, amd64)
✓ plugin harness              ✓ cross-compile (linux, amd64)
                              ✓ cross-compile (linux, arm64)
```

### What CI bought immediately

The runner's own header, which is the portability audit above turning out to be right:

```
tmux          : /usr/bin/tmux (tmux 3.4)
bash          : /usr/bin/bash (GNU bash, version 5.2.21(1)-release (x86_64-pc-linux-gnu))
host asset    : tdo-linux-amd64
downloader    : curl=/usr/bin/curl wget=/usr/bin/wget
show-messages : NOT observable on this tmux; message-log assertions skip
```

- **The suite has now run with no tmux at all**, on `go (ubuntu-latest)`. That is the
  environment `tmux-regression-guard-ci-proof` was written for and had never actually met:
  every scope, wiring and TUI guard in this repo had only ever run on a developer's machine
  inside tmux.
- **`dl-9-wget-only` executed and passed** — `ok wget alone is enough to download`. That branch
  of `fetch()` had never run: `release-binaries-and-ci` recorded it as a genuine untested gap
  because this machine has no `wget`. First CI run, gap closed.
- **The harness ran on GNU userland and bash 5** for the first time, against BSD/bash-3.2
  provenance, and needed no portability fix at all — the sha256 tool resolution and the
  `uname`-computed host asset both held.

### Steps 4-5 complete: the release workflow is proven, and the rehearsal is gone (DoD 4, 5)

**A footgun was closed before any tag existed.** `release.yml` called `gh release create` with
no `--prerelease`, and GitHub's "latest" is the most recent release that is *not* a prerelease
— while `tmux-todo.tmux` installs from `/releases/latest/download/<asset>`. So cutting any
release candidate would silently have become the binary every installed plugin downloads at its
next tmux server start. Latent rather than live (no release existed;
`/releases/latest/download/tdo-darwin-arm64` returned **HTTP 404**), but permanent. Fixed in
`c0cd1fe`: semver's own rule decides — a hyphen after the version core means pre-release —
exercised across `v0.1.0`/`v2.3.4` (final) and `v0.0.1-test`/`v1.0.0-rc.1` (prerelease).
Closing it first is what made the rehearsal below safe.

**Release run `32487438564`: green on its first attempt**, unlike CI's. All five assets, with
the contract names exactly as `release.yml`'s header pins them:

```
checksums.txt        330 bytes
tdo-darwin-amd64   7877472
tdo-darwin-arm64   7744898
tdo-linux-amd64    7753912
tdo-linux-arm64    7667896
```

**The `--prerelease` safeguard demonstrated rather than asserted.** With the throwaway release
published, `/releases/latest/download/tdo-darwin-arm64` **still returned HTTP 404** — the
release did not become "latest", which is the entire behaviour the fix exists for.
`isPrerelease` is `true`.

**One asset downloaded and verified by hand**, off the release page:

```
published: 8f613a14e16ef7bfab628db8e780222543022e04548fad66e0f80cd57ce6ed89
computed : 8f613a14e16ef7bfab628db8e780222543022e04548fad66e0f80cd57ce6ed89   MATCH

$ ./tdo-darwin-arm64 --version
v0.0.1-test
$ ./tdo-darwin-arm64 doctor --db <throwaway>
tdo      v0.0.1-test
runtime  go1.25.0 darwin/arm64
schema   2 (latest 2)
journal  wal
ok
$ otool -L ./tdo-darwin-arm64
  /usr/lib/libSystem.B.dylib
  /usr/lib/libresolv.9.dylib          # no libsqlite3
```

So the ldflags stamp, the migrations, and `CGO_ENABLED=0` all hold on a binary that came off
GitHub rather than out of a local build. `runtime go1.25.0` also confirms
`go-version-file: go.mod` did what it claims: CI built on the exact floor the README promises.

*Method note:* the first verification attempt printed `MATCH` while both sides were **empty** —
the download had silently failed and `"" = ""` compared equal. The comparison now requires both
values to be non-empty before claiming a match. Recorded because it is the same vacuous-guard
trap this project has caught four times in its own tests, written here by the curator.

**Step 5 — the rehearsal is fully removed.** `gh release delete --cleanup-tag` took the release
and the remote tag, and the local tag with it:

```
before:  git describe -> v0.0.1-test
after:   local tags [] · remote tags [] · releases []
         git describe -> fatal: No names found, cannot describe anything
```

Back to the pre-tag state, so nothing derives a version from a test tag. That mattered enough
to be its own DoD item: a lingering `v0.0.1-test` would have made every later `git describe`
descend from it.

### Remaining: steps 6-8

The release machinery is now proven end to end; what is left is the real tag and the install
check. **Step 6 is a product decision, not a technical one**, and it is the open item:
`session-renamed-hook-targets-wrong-session` is still `in-progress`, so `v0.1.0` today would
ship a release whose rename-following silently does nothing. Either the fix lands first, or the
release notes say so plainly and point at the all-tasks view's re-home as the workaround.

### Step 6 complete: v0.1.0 is cut (DoD 6), and CI is green on the tagged commit (DoD 1, 9)

**The product blocker cleared first.** Step 6 was held because
`session-renamed-hook-targets-wrong-session` was open, and cutting a release whose headline
rename feature silently did nothing would have needed a release note admitting it. That fix
landed and was approved at Checkpoint 2 (merge `174f829`), so `v0.1.0` ships it working. No
caveat was needed.

**The tag is `b8dbf9e`, and it was chosen rather than defaulted to.** At the moment of the
decision `main` carried an unreviewed feature (the sticky all-tasks view) on top of a CI-green
commit, so the options were "tag the green reviewed commit and leave the feature for v0.1.1" or
"review it, push, re-run CI, tag everything". The user chose the second. Both CI runs are
recorded because the first one is what proved the harness fix:

| run | commit | result |
|---|---|---|
| `32519409733` | `f8f054d` | all seven jobs green — the tmux 3.4 quote-key fix proven on the runner |
| `32521344469` | `b8dbf9e` | all seven jobs green — the tagged commit |

```
✓ go (ubuntu-latest)          ✓ cross-compile (darwin, arm64)
✓ go (macos-latest)           ✓ cross-compile (darwin, amd64)
✓ plugin harness              ✓ cross-compile (linux, amd64)
                              ✓ cross-compile (linux, arm64)
```

**Release run `32521504370`: green.** Five assets, the contract names unchanged from the
rehearsal, and this time `isPrerelease` is **false** — which is the whole point of the
`c0cd1fe` safeguard being exercised in both directions:

```
tag=v0.1.0 prerelease=false draft=false
checksums.txt        330
tdo-darwin-amd64  7906736
tdo-darwin-arm64  7761986
tdo-linux-amd64   7786680
tdo-linux-arm64   7667896
```

**`/releases/latest/download/` resolves for the first time in the project's history.** It
returned HTTP 404 before any release existed and *still* 404'd with the `v0.0.1-test`
prerelease published — the two measurements that made the prerelease fix believable. Now:

```
$ curl -sIL -o /dev/null -w '%{http_code}' \
    https://github.com/agusarias/tmux-todo/releases/latest/download/tdo-darwin-arm64
200
```

That URL is the plugin's step-3 download path, so this is the first moment
`tmux-todo.tmux` can install by download from a real release.

**One asset downloaded and verified by hand, off the published release:**

```
published: 10800ddf87d9bb89f149683af28e60cb55fa6205eac560b3aa2569c4e0540403
computed : 10800ddf87d9bb89f149683af28e60cb55fa6205eac560b3aa2569c4e0540403   MATCH

$ ./tdo-darwin-arm64 --version
v0.1.0
$ ./tdo-darwin-arm64 doctor --db <throwaway>
tdo      v0.1.0
runtime  go1.25.0 darwin/arm64
schema   2 (latest 2)
journal  wal
tasks    0 pending, 0 total
ok
$ otool -L ./tdo-darwin-arm64
  /usr/lib/libSystem.B.dylib
  /usr/lib/libresolv.9.dylib          # no libsqlite3
```

Both sides were checked non-empty before the match was claimed — the vacuous-comparison trap
recorded at step 4, where an empty download once compared equal to an empty checksum.

**DoD 6's local half:** `make build` now stamps a real version for the first time in the
project's history.

```
$ make build && ./bin/tdo --version
v0.1.0
$ git describe --tags
v0.1.0
```

### Remaining: steps 7-8

- **Step 7 / DoD 7** — the end-to-end install by download on a machine with **no `go` on
  `PATH`**, reaching a working keybind purely from the release asset, with the checksum path
  exercised against the real `checksums.txt`. This is now possible for the first time (the
  asset exists and `latest` resolves) and it is the user this whole release is for.
- **Step 8 / DoD 8** — walk the README's install instructions verbatim and fix whatever does
  not work as written.

### Step 8 (DoD 8): the README's install instructions, walked as written

Every claim in `README.md`'s Install section was executed or checked against the code, rather
than read. Nothing needed correcting.

**The release-binary block, run verbatim** (the one most likely to be subtly wrong, since it
pipes a grepped line into a checksum tool whose accepted format is stricter than it looks):

```sh
base=https://github.com/agusarias/tmux-todo/releases/latest/download
asset=tdo-darwin-arm64
curl -fsSLO "$base/$asset"
curl -fsSLO "$base/checksums.txt"
grep " $asset$" checksums.txt | shasum -a 256 -c -
```
```
downloads: ok
tdo-darwin-arm64: OK
rc=0
```

So the `checksums.txt` the workflow writes and the `grep`/`shasum -c` pair the README documents
really do line up. `tdo --version` printing the release tag (`v0.1.0`) was confirmed at step 6.

**The from-source block, on a fresh clone of the public repo:**

```
$ git clone https://github.com/agusarias/tmux-todo && cd tmux-todo && make build
$ ./bin/tdo --version
v0.1.0-1-g7366878
```

Works with no local state — the pushed tree builds standalone. The version is a `git describe`
rather than a bare tag because `main` is one commit past `v0.1.0`; the README only promises the
tag for the *release binary* path, so this is correct rather than a defect.

**Claims checked against the code instead of the prose:**

- `TDO_RELEASE_BASE_URL` is real, not aspirational documentation — `tmux-todo.tmux:45`
  (`RELEASE_BASE=${TDO_RELEASE_BASE_URL:-...}`).
- "`go` 1.25 or newer" matches `go.mod`'s `go 1.25.0`.
- The four documented asset names are exactly the four the release published.
- The two option defaults the comments claim (`@todo-key` = `t`, `@todo-key-table` = `prefix`)
  match `install_keybind`.

### Step 7 (DoD 7): in progress, and how it is being done

Docker is available on this machine, so the "no `go` on `PATH`" condition is being made **real**
rather than arranged: a `debian:stable-slim` container with tmux, curl and git and no Go
toolchain at all, doing a TPM-style `git clone` of the public repo and letting the plugin's
step-3 download install the binary from the real release. The script asserts the sandbox first
(`command -v go` must fail, `command -v tdo` must fail, `sha256sum` must exist so the checksum
path can actually run) — otherwise the whole case would pass for the wrong reason, which is the
trap this harness has hit before with a shadowed PATH.

It then asserts: a fresh clone has no `bin/tdo`; a server started from a config that
`run-shell`s the plugin installs one; `prefix t` is bound to `display-popup`; the binary reports
`v0.1.0`; its SHA-256 equals the published `checksums.txt` entry for `tdo-linux-arm64`
(**byte-for-byte proof it is the release asset and not a local build**); `add`/`list` work; and
the popup opens on `prefix t` with a manufactured nested client.

**Not yet run:** the first attempt timed out at 10 minutes doing `apt-get install` inside the
container, and pre-building the image has been stalled pulling `debian:stable-slim` from Docker
Hub. Nothing about the plugin is implicated — this is image-pull throughput. If Docker Hub stays
unreachable the fallback is the CI plugin job's approach (mask `go` out of the system directories
on this machine and assert it is really absent), which loses "the sandbox is intrinsic" but keeps
the real download, the real checksum and the real keybind.

### Step 7 (DoD 7): install by download, proven — 12 passed, 0 failed

Run on this machine with a **masked PATH**, since the container attempt could not get an image
(see the caveat below). The sandbox PATH is built by symlinking every binary out of the system
directories **by wildcard** and skipping only `go`, `gofmt` and `tdo` — a curated allow-list is
how a missing tool makes a case pass for the wrong reason, which this harness has been bitten by
before. `HOME`, `XDG_DATA_HOME` and `XDG_STATE_HOME` all point into a throwaway root, so nothing
touched the real database or preferences.

```
== the sandbox is real (asserted, not assumed)
    ok   no go on PATH
    ok   no tdo on PATH
    ok   tmux is present (tmux 3.7b)
    ok   curl is present, so step 3 can download
    ok   a sha256 tool exists, so the checksum path really runs
== TPM-style clone of the public repo
    ok   cloned the public repo the way TPM would
    ok   the fresh clone has no bin/tdo
== a server start sources the plugin, with no toolchain available
    ok   the plugin installed bin/tdo with no go on PATH (so: by download)
    ok   prefix t is bound to display-popup, exactly once
== the installed binary IS the published asset
    version   : v0.1.0
    asset     : tdo-darwin-arm64
    published : 10800ddf87d9bb89f149683af28e60cb55fa6205eac560b3aa2569c4e0540403
    computed  : 10800ddf87d9bb89f149683af28e60cb55fa6205eac560b3aa2569c4e0540403
    ok   byte-for-byte the release asset, not a local build
== the binary works against a sandboxed database
    ok   add and list work
    tdo      v0.1.0   runtime go1.25.0 darwin/arm64   schema 2 (latest 2)   journal wal   ok
== prefix t opens the popup, with a manufactured client
    ok   prefix t opened the popup and the task is on screen
    -- 8:  ││  ▸ ◉ ZZSTEP7ZZ installed by download         (global)  ││
----------------------------------------
step 7 (masked PATH): 12 passed, 0 failed
```

**What this proves that nothing before it did.** The plugin's step-3 download had only ever run
against a local fixture server; here it fetched from the real
`/releases/latest/download/`, past GitHub's redirect, and the file it installed hashes to the
**published** `checksums.txt` entry — so the download, the redirect, the asset name the plugin
computes from `uname`, the checksum verification and the keybind are all proven against a genuine
release. The five assertions at the top are what keep the run from being vacuous: with `go` on
the PATH the install could have come from a source build and every later assertion would still
have passed.

**Caveat, and it is the one the Verification section asked to avoid.** The brief's DoD says "a
machine (or container)", which this satisfies; its Verification section preferred a container so
that "no `go` on `PATH`" is *intrinsic* rather than arranged. Docker is installed and its daemon
is up here, but no image could be pulled — `debian:stable-slim` from Docker Hub produced no layer
in ~13 minutes and a `public.ecr.aws` mirror fared no better, so registry egress looks blocked in
this environment. A masked PATH cannot prove the absence of things nobody thought to mask: a
container would also have shown whether the install needs anything beyond tmux, curl and git.
That leg is unrun, and the run above is what stands in its place.

**Update, same day: the container leg DID run.** The image finished building after the caveat
above was written (registry egress was slow, not blocked), so both legs exist and the caveat is
retained only as the record of the order things happened in.

### Step 7, container leg: intrinsic sandbox — 11 passed, 0 failed

`debian:stable-slim` (Debian 13 trixie, linux/aarch64) with tmux, curl and git and **no Go
toolchain of any kind** — asserted three ways, including that no Go install exists off-PATH,
which a masked PATH cannot show.

```
== container facts
    kernel    : Linux/aarch64
    distro    : Debian GNU/Linux 13 (trixie)
    tmux      : tmux 3.5a
== the sandbox is INTRINSIC: this image never had a Go toolchain
    ok   no go anywhere on PATH
    ok   no tdo anywhere on PATH
    ok   no Go install on the filesystem at all
    ok   sha256sum exists, so the checksum path really runs
    ok   cloned the public repo the way TPM would
    ok   the fresh clone has no bin/tdo
    ok   the plugin installed bin/tdo after 0s — by download, since no build is possible here
    ok   prefix t is bound to display-popup, exactly once
    version   : v0.1.0
    file      : ELF 64-bit LSB executable, ARM aarch64, statically linked
    published : ad9e23fc3034defc5a7a6cde51c888bd0b6e165a42db1dbd33a0fc2101c4137b
    computed  : ad9e23fc3034defc5a7a6cde51c888bd0b6e165a42db1dbd33a0fc2101c4137b
    ok   byte-for-byte the published tdo-linux-arm64
    ok   add and list work
    tdo v0.1.0 · runtime go1.25.0 linux/arm64 · schema 2 (latest 2) · journal wal · ok
    ok   prefix t opened the popup and the task is on screen
    -- 8:  ││  ▸ ◉ ZZCONTAINERZZ from the release asset    (global)  ││
----------------------------------------
step 7 (container): 11 passed, 0 failed
```

**Three things this leg proves that the masked-PATH run could not.**

1. **The `tdo-linux-arm64` asset had never been executed by anyone.** The release workflow
   cross-compiled it and `verify` only ran the *native* one; CI's own jobs are amd64. This is the
   first time that binary has run, and `doctor` reports `runtime go1.25.0 linux/arm64`, schema 2
   and WAL — so the migrations and the pure-Go SQLite driver work on that platform rather than
   merely compiling for it. `file` confirms **statically linked**, which is the
   `CGO_ENABLED=0` promise checked on a Linux binary instead of via `otool`.
2. **A third tmux version.** 3.7b on the dev machine, 3.4 on CI, **3.5a** here — and the
   keybind, the `display-popup` branch and the nested-client capture all behave the same on all
   three. Given that this project has now been bitten twice by version-dependent tmux behaviour
   (`show-messages` on 3.4, the `C-"` key name), a third data point is worth more than the
   intrinsic sandbox was.
3. **The install needs nothing beyond tmux, curl and git.** A slim image with no Go, no build
   tools and no `tdo` reached a working `prefix t` from a clone in under a second of plugin time.
   That is the actual user this release exists for, and it is now a measured claim.

## Close-out (curator, 2026-08-21)

Closed on 2026-08-21. All nine DoD items are satisfied and evidenced above, including **both**
legs of step 7 — the masked-PATH run (12/0) and the intrinsic-sandbox container run (11/0).

**What this task was actually for.** It exists because a workflow that has never run is not
evidence, and it earned that framing twice over. CI's first run ever failed, and both failures
were this repo's own anti-vacuity guards refusing to run tests that would prove nothing — the
tmux-less assertion firing on a runner that ships tmux (which is how DoD 2 got a *real*
experiment instead of a contrived one), and the plugin sandbox refusing to pretend it controlled
`go` resolution. The release workflow then turned out to publish every release as "latest",
including release candidates, which would have silently made any RC the binary every installed
plugin downloads at its next tmux server start. All three were latent and permanent, and none
would have been found by reading the YAML.

**The tag point was chosen, not defaulted to.** When step 6 came due, `main` carried an
unreviewed feature on top of a CI-green commit. The user chose to review it, push, re-run CI and
tag everything, so `v0.1.0` = `b8dbf9e` with six features each through a Checkpoint 2.

**Lessons folded into CLAUDE.md**: three tmux versions now have evidence and each has moved
behaviour (probe the running tmux, never compare `tmux -V`); a cross-compiled asset nothing has
executed is not evidence (`tdo-linux-arm64` shipped never having run); and a masked PATH cannot
prove the absence of what nobody thought to mask.

**Follow-ups deliberately not taken here:** nothing purges completed rows (`store.PurgeDone`
still has no caller, per `design.md`), and `session-renamed` opens the store before resolving the
session, so a hook firing on a machine that has never run `tdo` creates an empty database. Both
are recorded, neither is a release blocker.

**v0.1.0 is public**: https://github.com/agusarias/tmux-todo/releases/tag/v0.1.0

