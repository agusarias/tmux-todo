# Prove CI And Cut v0.1.0

**Status:** draft
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
- **The push itself is the user's action**, not the executor's. This brief's first step is a
  human one; the executor's work begins once `main` is on the remote and CI has run.
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

## Plan
(Added at Checkpoint 1.)

## Evidence
(Added by the executor.)
