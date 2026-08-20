#!/usr/bin/env bash
#
# tmux-todo — TPM plugin entry point.
#
# Installs the popup keybind and the session-renamed hook, pointing both at a
# resolved `tdo` binary. TPM sources this file on EVERY tmux server start, not
# just at install, so the steady-state path does no build, no network and no
# more than four tmux calls. A build happens at most once, when no binary exists.
#
# Binary resolution, in order, stopping at the first success:
#   1. `tdo` on $PATH                        — a user's own install wins
#   2. $PLUGIN_DIR/bin/tdo, if executable    — a previous build of ours
#   3. `go build` into $PLUGIN_DIR/bin/tdo   — only with go >= 1.25
#   4. nothing: no keybind, and a message naming the problem and the fix
#
# Step 4 binds NOTHING on purpose. A keybind pointing at a missing binary looks
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

# Our own directory, without assuming the cwd tmux happens to have and without
# assuming TPM's plugin path (which is configurable).
_src=${BASH_SOURCE[0]}
case $_src in
    */*) PLUGIN_DIR=$(cd -- "${_src%/*}" && pwd -P) ;;
    *)   PLUGIN_DIR=$(pwd -P) ;;
esac

TDO_BIN=''
RESOLVE_NOTE=''

# Both channels on purpose: display-message is what the user sees, stderr is
# what ends up in the tmux server log and is what the harness reads.
warn() {
    printf '%s: %s\n' "$PLUGIN_NAME" "$1" >&2
    tmux display-message "$PLUGIN_NAME: $1" 2>/dev/null
}

# Step 3. Sets TDO_BIN on success; sets RESOLVE_NOTE and returns 1 otherwise.
build_binary() {
    local go_bin go_version major minor version
    go_bin=$(command -v go 2>/dev/null || true)
    if [ -z "$go_bin" ]; then
        RESOLVE_NOTE="no 'go' on \$PATH to build it with"
        return 1
    fi

    go_version=$("$go_bin" version 2>/dev/null || true)
    # `go version go1.26.6 darwin/arm64`, but devel and rc builds differ. A
    # version we cannot read falls through to step 4 rather than optimistically
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
# there and only there.
install_keybind() {
    local key
    key=$(tmux show-option -gqv @todo-key 2>/dev/null || true)
    [ -n "$key" ] || key='t'

    tmux bind-key -T prefix "$key" "if-shell -F '#{m:-*,#{e|-|:#{client_width},100}}' {
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
        warn "no usable tdo binary ($RESOLVE_NOTE). Install tdo on \$PATH, or run 'make build' in $PLUGIN_DIR. No keybind installed."
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
