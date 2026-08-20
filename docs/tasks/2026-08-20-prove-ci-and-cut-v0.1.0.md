# Prove CI And Cut v0.1.0

**Status:** agreed
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
(Added by the executor.)
