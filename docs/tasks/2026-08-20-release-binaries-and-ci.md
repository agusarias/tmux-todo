# Release Binaries And CI

**Status:** draft
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
Publishing a release is **externally visible and effectively irreversible** — a tag people
have fetched, and assets a plugin may already be downloading. Getting the platform matrix or
the archive layout wrong is a breaking change for anyone who installed in between. The
plugin's download path also introduces a **network dependency at install time**, with the
failure modes (offline, rate-limited, asset missing for the platform) needing deliberate
answers rather than a bare `curl`.

OPEN: which platforms? `darwin/arm64` and `linux/amd64` are the obvious floor;
`darwin/amd64` and `linux/arm64` are cheap to add and probably should be. Anything else
(BSDs, `linux/386`) needs a reason.

OPEN: does the plugin **prefer** a downloaded binary over a source build, or the reverse?
Preferring the download is faster and needs no toolchain, but pins users to release cadence;
preferring source keeps a checkout authoritative. This changes `tpm-plugin-and-install`'s
resolution chain, so it is a cross-brief decision.

OPEN: tag `v0.1.0` as the first release, or wait until the all-tasks view lands so v1 is
feature-complete per `design.md`'s cut line?

## Definition of done
(To be written at grill time — the OPEN items above change its shape.)

## Verification
(To be written at grill time.)

## Decisions
- **2026-08-20 (curator, split from `tpm-plugin-and-install`):** deferred out of that task
  deliberately rather than absorbed. Shipping the plugin script does not depend on this, and
  this needs decisions about platforms, cadence and release policy that a packaging task
  should not make on the side.

## Plan
(Added at Checkpoint 1.)

## Evidence
(Added by the executor.)
