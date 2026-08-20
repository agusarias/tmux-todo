#!/usr/bin/env bash
#
# tmux-todo — TPM plugin entry point.
#
# Installs the popup keybind and the session-renamed hook, pointing both at a
# resolved `tdo` binary. TPM sources this file on EVERY tmux server start, not
# just at install, so the steady-state path does no build, no network and no
# more than four tmux calls. A download or a build happens at most once, when no
# binary exists.
#
# Binary resolution, in order, stopping at the first success:
#   1. `tdo` on $PATH                        — a user's own install wins
#   2. $PLUGIN_DIR/bin/tdo, if executable    — a previous download or build
#   3. a release asset, downloaded           — no Go toolchain needed
#   4. `go build` into $PLUGIN_DIR/bin/tdo   — only with go >= 1.25
#   5. nothing: no keybind, and a message naming the problem and the fix
#
# Steps 1 and 2 stay first so a user's own `tdo` still wins and an existing
# install is still reused: nothing that worked before the download step existed
# stops working. Step 4 survives *behind* step 3 as the offline / no-asset
# fallback rather than being replaced by it.
#
# Step 5 binds NOTHING on purpose. A keybind pointing at a missing binary looks
# like the plugin's fault when the key is pressed, which is a worse failure than
# a clear message at install time.
#
# Written for bash 3.2 (the macOS system bash), so no associative arrays and no
# ${var^^}. See test/plugin_install_test.sh, run by `make test-plugin`.

set -u

PLUGIN_NAME='tmux-todo'
GO_MIN_MAJOR=1
GO_MIN_MINOR=25
GO_MIN='1.25'

# Where step 3 fetches from. /releases/latest/download/<asset> redirects to the
# current release's asset, so this needs no GitHub API call: no JSON to parse, no
# `jq` dependency, and not subject to the unauthenticated API rate limit — which
# a script sourced on every tmux server start would be a fine way to hit.
#
# $TDO_RELEASE_BASE_URL overrides it. That is the seam the harness fetches
# fixtures through, and it doubles as the way to point an install at a mirror.
TDO_REPO='agusarias/tmux-todo'
RELEASE_BASE=${TDO_RELEASE_BASE_URL:-"https://github.com/$TDO_REPO/releases/latest/download"}

# Our own directory, without assuming the cwd tmux happens to have and without
# assuming TPM's plugin path (which is configurable).
_src=${BASH_SOURCE[0]}
case $_src in
    */*) PLUGIN_DIR=$(cd -- "${_src%/*}" && pwd -P) ;;
    *)   PLUGIN_DIR=$(pwd -P) ;;
esac

TDO_BIN=''
RESOLVE_NOTE=''
DOWNLOAD_NOTE='not attempted'

# Both channels on purpose: display-message is what the user sees, stderr is
# what ends up in the tmux server log and is what the harness reads.
warn() {
    printf '%s: %s\n' "$PLUGIN_NAME" "$1" >&2
    tmux display-message "$PLUGIN_NAME: $1" 2>/dev/null
}

# ------------------------------------------------------------------- step 3

# The release asset name for this machine, or return 1 for a platform we do not
# ship a binary for.
#
# Four targets: darwin/{arm64,amd64} and linux/{arm64,amd64}. tmux is POSIX-only,
# so there is deliberately no windows asset to name; WSL users are linux/amd64.
#
# The naming is uname's problem, not Go's — macOS says `arm64` where most Linux
# distributions say `aarch64`, and everyone says `x86_64` for what Go calls
# `amd64`. Getting one wrong yields a 404 and a silent fall-through to the source
# build, which WORKS, so it would not look broken. Hence the table test.
asset_name() {
    local os arch
    case $(uname -s 2>/dev/null) in
        Darwin) os=darwin ;;
        Linux)  os=linux ;;
        *)      return 1 ;;
    esac
    case $(uname -m 2>/dev/null) in
        x86_64|amd64)  arch=amd64 ;;
        arm64|aarch64) arch=arm64 ;;
        *)             return 1 ;;
    esac
    printf 'tdo-%s-%s\n' "$os" "$arch"
}

have_downloader() {
    command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1
}

# curl or wget, whichever exists. Neither means the download step is
# *unavailable*, not that anything is wrong.
#
# -f (curl) and -q (wget) are load-bearing: without them a 404 or a rate-limit
# page is written to the output file and the exit status is 0, so bin/tdo becomes
# an HTML document that the keybind then points at. -L follows the
# /releases/latest/download redirect, which is the entire mechanism. The timeouts
# keep an unreachable host from stalling a tmux server start instead of failing
# one.
fetch() { # url dest -> 0 with the body saved at dest
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL --connect-timeout 10 --max-time 300 -o "$2" -- "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -q --timeout=10 --tries=2 -O "$2" -- "$1"
    else
        return 1
    fi
}

# The hex sum of a file, or return 1 when the box has nothing to compute one
# with. sha256sum on Linux, shasum on macOS; both print `<hex>  <name>`.
sha256_of() { # path
    local out
    if command -v sha256sum >/dev/null 2>&1; then
        out=$(sha256sum -- "$1" 2>/dev/null) || return 1
    elif command -v shasum >/dev/null 2>&1; then
        out=$(shasum -a 256 -- "$1" 2>/dev/null) || return 1
    else
        return 1
    fi
    # The name may hold anything, including spaces; the hex may not.
    out=${out%% *}
    [ -n "$out" ] || return 1
    printf '%s\n' "$out"
}

# Tri-state, and the three are not interchangeable:
#   0  checksums.txt names this asset and the sum MATCHES
#   1  checksums.txt names this asset and the sum does NOT match -> refuse it
#   2  there is no usable answer                                 -> best-effort
#
# 2 covers: no sha256 tool on the box, checksums.txt unreachable or 404, and a
# checksums.txt with no line for this asset. The user's ruling is that an install
# is never blocked by a *missing* checksums file, so 2 proceeds — and says so.
# That does not relax 1: a file that says no is fatal, and the caller deletes the
# download rather than executing it.
verify_download() { # binary asset sums-dest
    local want got
    got=$(sha256_of "$1") || return 2
    fetch "$RELEASE_BASE/checksums.txt" "$3" || return 2
    [ -s "$3" ] || return 2
    # sha256sum's own format; some tools mark binary mode with a `*` before the
    # name, so both spellings count as a line for this asset.
    want=$(awk -v a="$2" '$2 == a || $2 == "*" a { print $1; exit }' "$3" 2>/dev/null)
    [ -n "$want" ] || return 2
    [ "$want" = "$got" ] || return 1
    return 0
}

# Step 3. Sets TDO_BIN on success; sets DOWNLOAD_NOTE and returns 1 otherwise.
#
# Every failure returns 1 rather than failing the script, because step 4 still
# deserves its turn: offline, DNS failure, a 404 for this platform, a 403 rate
# limit, a checksum mismatch and no downloader at all are each "fall through",
# never "give up". The source build is what keeps the chain honest on a machine
# with no network.
#
# This runs at most once per install. Step 2 short-circuits ahead of it on every
# later server start, so the steady state touches no network — which matters
# because tmux sources this file every time the server starts.
download_binary() {
    local asset tmpbin dest
    asset=$(asset_name) || {
        DOWNLOAD_NOTE="no released binary for $(uname -s 2>/dev/null)/$(uname -m 2>/dev/null)"
        return 1
    }
    if ! have_downloader; then
        DOWNLOAD_NOTE='no curl or wget to download a released binary with'
        return 1
    fi

    # The temp file lives in the destination directory, so the final move is a
    # rename within one filesystem and therefore atomic. bin/tdo is never a
    # partially written file: step 2 trusts that path on every later server
    # start, so a truncated download there is a permanently broken install that
    # the chain never retries.
    mkdir -p "$PLUGIN_DIR/bin" 2>/dev/null
    tmpbin=$(mktemp "$PLUGIN_DIR/bin/.tdo.XXXXXX" 2>/dev/null) || {
        DOWNLOAD_NOTE="cannot write a download into $PLUGIN_DIR/bin"
        return 1
    }

    printf '%s: no tdo binary found; downloading %s from %s (this happens once)\n' \
        "$PLUGIN_NAME" "$asset" "$RELEASE_BASE" >&2

    if ! fetch "$RELEASE_BASE/$asset" "$tmpbin" || [ ! -s "$tmpbin" ]; then
        rm -f "$tmpbin"
        DOWNLOAD_NOTE="could not download $asset from $RELEASE_BASE"
        return 1
    fi

    verify_download "$tmpbin" "$asset" "$tmpbin.sums"
    case $? in
        0) ;;
        2) warn "could not verify the checksum of the downloaded $asset (no checksums.txt, or no sha256 tool here). Using it UNVERIFIED." ;;
        *)
            rm -f "$tmpbin" "$tmpbin.sums"
            DOWNLOAD_NOTE="the downloaded $asset failed its checksum and was deleted"
            return 1
            ;;
    esac
    rm -f "$tmpbin.sums"

    # 0755 explicitly, not `chmod +x`: mktemp creates the file 0600, and `+x`
    # on that yields 0711 — executable, but not the mode `go build` produces at
    # step 4, and a binary whose permissions depend on which step installed it
    # is a difference waiting to be debugged.
    chmod 0755 "$tmpbin" 2>/dev/null
    dest=$PLUGIN_DIR/bin/tdo
    if ! mv -f "$tmpbin" "$dest" 2>/dev/null || [ ! -x "$dest" ]; then
        rm -f "$tmpbin"
        DOWNLOAD_NOTE="could not install the download at $dest"
        return 1
    fi
    TDO_BIN=$dest
    return 0
}

# ------------------------------------------------------------------- step 4

# Step 4. Sets TDO_BIN on success; sets RESOLVE_NOTE and returns 1 otherwise.
build_binary() {
    local go_bin go_version major minor version
    go_bin=$(command -v go 2>/dev/null || true)
    if [ -z "$go_bin" ]; then
        RESOLVE_NOTE="no 'go' on \$PATH to build it with"
        return 1
    fi

    go_version=$("$go_bin" version 2>/dev/null || true)
    # `go version go1.26.6 darwin/arm64`, but devel and rc builds differ. A
    # version we cannot read falls through to step 5 rather than optimistically
    # building: building with an old toolchain fails with a message about the
    # `go` directive that reads like a bug in this repo.
    if [[ $go_version =~ go([0-9]+)\.([0-9]+) ]]; then
        major=${BASH_REMATCH[1]}
        minor=${BASH_REMATCH[2]}
    else
        RESOLVE_NOTE="cannot read a version out of '$go_version'; go $GO_MIN or newer is needed to build"
        return 1
    fi
    if [ "$major" -lt "$GO_MIN_MAJOR" ] ||
       { [ "$major" -eq "$GO_MIN_MAJOR" ] && [ "$minor" -lt "$GO_MIN_MINOR" ]; }; then
        RESOLVE_NOTE="go $major.$minor is older than the required $GO_MIN"
        return 1
    fi

    printf '%s: no tdo binary found; building one with %s (this happens once)\n' \
        "$PLUGIN_NAME" "$go_bin" >&2

    version='dev'
    if command -v git >/dev/null 2>&1; then
        version=$(git -C "$PLUGIN_DIR" describe --tags --always --dirty 2>/dev/null || true)
        [ -n "$version" ] || version='dev'
    fi

    mkdir -p "$PLUGIN_DIR/bin" 2>/dev/null
    ( cd "$PLUGIN_DIR" 2>/dev/null &&
      CGO_ENABLED=0 "$go_bin" build -trimpath \
          -ldflags "-s -w -X github.com/agusarias/tmux-todo/internal/cli.Version=$version" \
          -o bin/tdo ./cmd/tdo ) >/dev/null 2>&1

    # Not "did go build exit 0" but "is there a binary": that is the condition
    # the keybind depends on.
    if [ -x "$PLUGIN_DIR/bin/tdo" ]; then
        TDO_BIN=$PLUGIN_DIR/bin/tdo
        return 0
    fi
    RESOLVE_NOTE="the build produced no binary; run 'make build' in $PLUGIN_DIR to see why"
    return 1
}

# Sets TDO_BIN, or sets RESOLVE_NOTE and returns 1.
resolve_binary() {
    local on_path
    on_path=$(command -v tdo 2>/dev/null || true)
    if [ -n "$on_path" ]; then
        TDO_BIN=$on_path
        return 0
    fi
    if [ -x "$PLUGIN_DIR/bin/tdo" ]; then
        TDO_BIN=$PLUGIN_DIR/bin/tdo
        return 0
    fi
    download_binary && return 0
    build_binary
}

# The popup keybind: the measured 60%x60%-with-a-60x15-floor branch, verbatim
# from docs/tasks/done/2026-08-19-tmux-integration-and-rename-hook.md, with only
# the binary path substituted.
#
# display-popup does not expand formats in -w/-h, so the floor is a branch and
# not an expression; the comparison is arithmetic because #{>=:x,y} compares
# STRINGS ("80" >= "100" is true). Passing the whole brace block as ONE shell
# argument hands tmux's own parser exactly the text it wants — the result is
# byte-identical to source-file'ing the same snippet, so no temp file is needed.
#
# bind-key REPLACES the binding for a key, so re-running this is idempotent by
# construction. Only set-hook -ga accumulates, which is why the guard lives
# there and only there. What re-running does NOT do is retract a binding made
# under an earlier @todo-key / @todo-key-table: bind-key is keyed on (table,
# key), so changing either leaves the old binding until the server restarts.
# Unbinding the previous pair would mean remembering it, and a plugin that
# unbinds keys it no longer owns is a plugin that eats a user's own rebinding.

# Key and table are read from options, so `set -g @todo-key` must come BEFORE
# this script runs in the user's config (TPM's plugin lines are near the end of
# a tmux.conf, so this is the normal order anyway).
install_keybind() {
    local key table
    key=$(tmux show-option -gqv @todo-key 2>/dev/null || true)
    [ -n "$key" ] || key='t'

    # The table is configurable too, because whether a popup deserves the prefix
    # is a matter of how the user has already spent their keyspace: a config that
    # root-binds C-t/C-p/C-w wants @todo-key-table 'root' and no prefix at all.
    # Two reads, not one combined `show-option -gqv a \; show-option -gqv b`:
    # an unset option prints NOTHING, not an empty line, so a combined read
    # cannot be split back into two values (CLAUDE.md).
    #
    # Only prefix and root are accepted. tmux takes any name here, but a custom
    # table is reachable only via a `switch-client -T` binding the user owns, so
    # honouring one would install a key nothing can press. A typo falls back to
    # prefix WITH a warning rather than binding nothing: the key still works, and
    # the message says why it is not where it was asked for.
    table=$(tmux show-option -gqv @todo-key-table 2>/dev/null || true)
    case $table in
        prefix|root) ;;
        '') table='prefix' ;;
        *)  warn "@todo-key-table is '$table'; only 'prefix' and 'root' are supported. Binding '$key' in prefix instead."
            table='prefix'
            ;;
    esac

    tmux bind-key -T "$table" "$key" "if-shell -F '#{m:-*,#{e|-|:#{client_width},100}}' {
  if-shell -F '#{m:-*,#{e|-|:#{client_height},25}}' {
    display-popup -E -w 60 -h 15 '$TDO_BIN tui'
  } {
    display-popup -E -w 60 -h 60% '$TDO_BIN tui'
  }
} {
  if-shell -F '#{m:-*,#{e|-|:#{client_height},25}}' {
    display-popup -E -w 60% -h 15 '$TDO_BIN tui'
  } {
    display-popup -E -w 60% -h 60% '$TDO_BIN tui'
  }
}"
}

# The rename hook. -ga, not -g, so a user's own session-renamed hook survives;
# no argument and no format, because the child inherits $TMUX whose session
# field is the one the hook fired for (interpolating a session name into a
# run-shell string is a shell injection — see CLAUDE.md).
#
# -ga appends, and TPM re-runs this script, so it needs a guard. The guard
# greps the hook's BODY, not its name: after `set-hook -gu session-renamed`,
# `show-hooks -g` still prints a bare `session-renamed` line with no value, so a
# name grep reports a hook that is not there and the install is silently
# skipped. tmux re-quotes the body with double quotes when printing it.
#
# The grep is path-specific by design. A path-blind grep would match a hook left
# by an install at a DIFFERENT path and then skip installing this one, leaving
# only the stale, broken hook. Matching the path instead means a moved plugin dir
# leaves one harmless stale hook alongside the working one.
install_hook() {
    if tmux show-hooks -g 2>/dev/null |
       grep -qF "run-shell -b \"$TDO_BIN session-renamed\""; then
        return 0
    fi
    tmux set-hook -ga session-renamed "run-shell -b '$TDO_BIN session-renamed'"
}

main() {
    if ! resolve_binary; then
        # Both notes: "no go on \$PATH" alone reads as though a download was never
        # an option, and "could not download" alone hides that a build was tried
        # too. Step 5 is the only place the user sees why, so it says why twice.
        warn "no usable tdo binary (download: $DOWNLOAD_NOTE; build: $RESOLVE_NOTE). Install tdo on \$PATH, or run 'make build' in $PLUGIN_DIR. No keybind installed."
        return 0
    fi

    # tmux's single-quoted strings have no escape character, so a path holding a
    # quote cannot be embedded in the commands above. Treat it as a resolution
    # failure rather than installing a keybind that breaks when pressed.
    case $TDO_BIN in
        *\'*|*\"*)
            warn "the tdo path contains a quote and cannot be bound: $TDO_BIN. Move it somewhere without quotes. No keybind installed."
            return 0
            ;;
    esac

    install_keybind
    install_hook
}

main
