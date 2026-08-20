#!/usr/bin/env bash
#
# Harness for tmux-todo.tmux, the TPM plugin entry point.
#
# Drives the real script against a private tmux server per case (`tmux -L`, plus
# `-f /dev/null` so the developer's own ~/.tmux.conf cannot contribute keybinds
# or hooks to the assertions) and asserts on `list-keys` / `show-hooks` output.
#
# Run by `make test-plugin`. Deliberately NOT part of `make test`, which stays
# `go test ./...` and must not need a tmux server (CLAUDE.md).
#
# Two traps this harness was written around, both found by running it against a
# do-nothing stub first:
#
#   * tmux's DEFAULT prefix table already binds `t` (clock-mode) and `w`
#     (choose-tree). Counting bindings for a key therefore counts tmux's own
#     binding and passes with the plugin deleted. Every key assertion here
#     counts bindings whose command contains `display-popup` — the plugin's.
#   * `env -i` leaves no `bash` on PATH, so the script silently never ran and
#     22 assertions "failed for the wrong reason". The sandbox PATH now carries
#     a bash symlink and the script is executed through its own shebang, which
#     tests the exec bit too.
#
# PLUGIN_SCRIPT may point at a mutated copy of the script; that is how the
# body-grep guard is shown to be load-bearing rather than trivially satisfied.

set -uo pipefail

REPO_ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
PLUGIN_SCRIPT=${PLUGIN_SCRIPT:-$REPO_ROOT/tmux-todo.tmux}

if ! command -v tmux >/dev/null 2>&1; then
    echo "SKIP: no tmux on PATH. The plugin harness drives a real tmux server;"
    echo "      install tmux to run it. (make test does not need tmux.)"
    exit 0
fi
if [ ! -f "$PLUGIN_SCRIPT" ]; then
    echo "FAIL: no plugin script at $PLUGIN_SCRIPT"
    exit 1
fi

REAL_TMUX=$(command -v tmux)
REAL_BASH=${BASH:-$(command -v bash)}
REAL_GO=$(command -v go || true)
GO_OK=no
if [ -n "$REAL_GO" ]; then
    gv=$("$REAL_GO" version 2>/dev/null)
    if [[ $gv =~ go([0-9]+)\.([0-9]+) ]]; then
        if [ "${BASH_REMATCH[1]}" -gt 1 ] ||
           { [ "${BASH_REMATCH[1]}" -eq 1 ] && [ "${BASH_REMATCH[2]}" -ge 25 ]; }; then
            GO_OK=yes
        fi
    fi
fi

TMPROOT=$(mktemp -d "${TMPDIR:-/tmp}/tdo-plugin-harness.XXXXXX")
# ...resolved, because on macOS mktemp hands back /var/... while the script's own
# `pwd -P` resolves it to /private/var/... and every path assertion would miss.
TMPROOT=$(cd -- "$TMPROOT" && pwd -P)

# The sandbox PATH carries the system tool dirs, so the script finds grep, mkdir
# and git exactly as it would in a real tmux; what the sandbox controls is only
# whether `tdo` and `go` are reachable. That has to be true to begin with, or the
# whole resolution suite quietly tests nothing.
SYS_PATH=/usr/bin:/bin:/usr/sbin:/sbin
for d in /usr/bin /bin /usr/sbin /sbin; do
    for t in tdo go; do
        if [ -e "$d/$t" ]; then
            echo "FAIL: $d/$t exists, so the sandbox cannot control whether $t resolves."
            echo "      The resolution cases would be vacuous. Aborting."
            exit 1
        fi
    done
done
SOCKETS=()
PASS=0
FAIL=0

cleanup() {
    local s
    for s in ${SOCKETS[@]+"${SOCKETS[@]}"}; do
        "$REAL_TMUX" -L "$s" kill-server >/dev/null 2>&1
    done
    rm -rf "$TMPROOT"
}
trap cleanup EXIT

echo "plugin script : $PLUGIN_SCRIPT"
echo "tmux          : $REAL_TMUX ($("$REAL_TMUX" -V))"
echo "bash          : $REAL_BASH ($("$REAL_BASH" --version | head -1))"
echo "go            : ${REAL_GO:-none} (usable for the build case: $GO_OK)"

# ---------------------------------------------------------------- assertions

ok()  { PASS=$((PASS + 1)); printf '    ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf '    FAIL %s\n' "$1"; }

assert_eq() { # want got label
    if [ "$1" = "$2" ]; then ok "$3"; else bad "$3 (want '$1', got '$2')"; fi
}
assert_contains() { # haystack needle label
    case "$1" in *"$2"*) ok "$3" ;; *) bad "$3 (no '$2' in: $1)" ;; esac
}
assert_absent() { # haystack needle label
    case "$1" in *"$2"*) bad "$3 (unexpected '$2' in: $1)" ;; *) ok "$3" ;; esac
}
assert_no_file() { # path label
    if [ -e "$1" ]; then bad "$2 ($1 exists)"; else ok "$2"; fi
}

# ---------------------------------------------------------------- case setup

# case_start <name> — a private server, plus a sandbox PATH whose front holds:
#   tmux  : a shim pinning every call the script makes to this case's socket
#   bash  : so the script's own #!/usr/bin/env bash shebang resolves
# followed by the system tool dirs. `tdo` and `go` live in neither, so they are
# reachable only when a case puts them there.
case_start() {
    CASE=$1
    CASEDIR=$TMPROOT/$CASE
    PLUGINDIR=$CASEDIR/plugin
    PATHDIR=$CASEDIR/path
    SOCK=tdoplug-$$-$CASE
    mkdir -p "$PLUGINDIR" "$PATHDIR"
    cp "$PLUGIN_SCRIPT" "$PLUGINDIR/tmux-todo.tmux"
    chmod +x "$PLUGINDIR/tmux-todo.tmux"

    printf '#!/bin/sh\nexec %s -L %s "$@"\n' "$REAL_TMUX" "$SOCK" >"$PATHDIR/tmux"
    chmod +x "$PATHDIR/tmux"
    ln -s "$REAL_BASH" "$PATHDIR/bash"

    SOCKETS+=("$SOCK")
    "$REAL_TMUX" -L "$SOCK" -f /dev/null new-session -d -s harness >/dev/null 2>&1
    printf '\n== %s\n' "$CASE"
}

case_end() { "$REAL_TMUX" -L "$SOCK" kill-server >/dev/null 2>&1; }

stub() { # path [stdout-line]
    mkdir -p "$(dirname -- "$1")"
    if [ $# -gt 1 ]; then
        printf '#!/bin/sh\necho "%s"\n' "$2" >"$1"
    else
        printf '#!/bin/sh\nexit 0\n' >"$1"
    fi
    chmod +x "$1"
}

# Runs the real script the way tmux does: executed directly, so the shebang and
# the exec bit are both exercised. Captures stdout+stderr for diagnostics.
plugin_run() {
    env -i HOME="$HOME" PATH="$PATHDIR:$SYS_PATH" TERM=dumb TMPDIR="${TMPDIR:-/tmp}" \
        "$PLUGINDIR/tmux-todo.tmux" 2>&1
}

tm() { "$REAL_TMUX" -L "$SOCK" "$@"; }

# THE PLUGIN's bindings for a key, not tmux's. list-keys prints
# `bind-key -T prefix <key> <command>`, so field 4 is the key; the plugin's
# command is the only one that opens a popup. Counting bare bindings for the key
# would count tmux's default clock-mode binding and pass with the plugin gone.
plugin_keybinds_for() {
    tm list-keys -T prefix 2>/dev/null | awk -v k="$1" '$4 == k' | grep 'display-popup'
}
count_plugin_keybinds_for() { plugin_keybinds_for "$1" | grep -c . ; }
count_all_keybinds_for() {
    tm list-keys -T prefix 2>/dev/null | awk -v k="$1" '$4 == k' | grep -c .
}

# tdo hooks, counted by BODY not name: after `set-hook -gu session-renamed`,
# show-hooks still prints a bare `session-renamed` line with no value, so a name
# grep reports a hook that is not there. Requiring `session-renamed[` and the
# command body is what makes the count real.
tdo_hooks() {
    tm show-hooks -g 2>/dev/null | grep 'session-renamed\[.*tdo session-renamed'
}
count_tdo_hooks() { tdo_hooks | grep -c . ; }

# ================================================================ resolution

# Outcome 1 of 4: tdo on $PATH wins.
case_start resolve-1-path
stub "$PATHDIR/tdo"
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for t)" "one popup keybind on t"
assert_eq 1 "$(count_all_keybinds_for t)" "and it is the only binding on t"
assert_contains "$(plugin_keybinds_for t)" "$PATHDIR/tdo tui" "keybind points at the PATH binary"
assert_eq 1 "$(count_tdo_hooks)" "one tdo session-renamed hook"
assert_contains "$(tdo_hooks)" "$PATHDIR/tdo session-renamed" "hook points at the PATH binary"
case_end

# Outcome 2 of 4: no tdo on PATH, but the plugin's own bin/tdo is executable.
case_start resolve-2-plugin-bin
stub "$PLUGINDIR/bin/tdo"
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for t)" "one popup keybind on t"
assert_contains "$(plugin_keybinds_for t)" "$PLUGINDIR/bin/tdo tui" "keybind points at the plugin binary"
assert_eq 1 "$(count_tdo_hooks)" "one tdo session-renamed hook"
case_end

# A non-executable bin/tdo is not a resolution: step 2 tests -x, not -e.
case_start resolve-2-plugin-bin-not-executable
mkdir -p "$PLUGINDIR/bin"
printf 'not a binary\n' >"$PLUGINDIR/bin/tdo"
chmod -x "$PLUGINDIR/bin/tdo"
out=$(plugin_run)
assert_eq 0 "$(count_plugin_keybinds_for t)" "NO popup keybind on t"
case_end

# Ordering is not arbitrary: a user's own tdo on PATH must beat a stale
# plugin-local build, so step 1 has to win when both exist.
case_start resolve-path-beats-plugin-bin
stub "$PATHDIR/tdo"
stub "$PLUGINDIR/bin/tdo"
out=$(plugin_run)
assert_contains "$(plugin_keybinds_for t)" "$PATHDIR/tdo tui" "PATH wins over plugin bin"
assert_absent "$(plugin_keybinds_for t)" "$PLUGINDIR/bin/tdo" "plugin bin not used"
case_end

# Outcome 3 of 4: nothing to resolve, but a new-enough go — build it, once.
if [ "$GO_OK" = yes ]; then
    case_start resolve-3-go-build
    cp -R "$REPO_ROOT/go.mod" "$REPO_ROOT/go.sum" "$REPO_ROOT/cmd" "$REPO_ROOT/internal" "$PLUGINDIR/"
    ln -s "$REAL_GO" "$PATHDIR/go"
    assert_no_file "$PLUGINDIR/bin/tdo" "precondition: no binary before the run"
    out=$(plugin_run)
    if [ -x "$PLUGINDIR/bin/tdo" ]; then
        ok "go build produced an executable bin/tdo"
    else
        bad "go build produced no bin/tdo (output: $out)"
    fi
    assert_eq 1 "$(count_plugin_keybinds_for t)" "one popup keybind on t"
    assert_contains "$(plugin_keybinds_for t)" "$PLUGINDIR/bin/tdo tui" "keybind points at the built binary"
    assert_eq 1 "$(count_tdo_hooks)" "one tdo session-renamed hook"
    # The built binary must actually run: a keybind onto a broken build is
    # exactly the dead keybind DoD 3 exists to prevent.
    if "$PLUGINDIR/bin/tdo" --version >/dev/null 2>&1; then
        ok "the built binary runs (--version exits 0)"
    else
        bad "the built binary does not run"
    fi
    # ...and a second run must NOT rebuild: it resolves at step 2 now.
    stamp_before=$(ls -l "$PLUGINDIR/bin/tdo")
    plugin_run >/dev/null
    assert_eq "$stamp_before" "$(ls -l "$PLUGINDIR/bin/tdo")" "a second run does not rebuild"
    case_end
else
    printf '\n== resolve-3-go-build\n    SKIP no go >= 1.25 on PATH (found: %s)\n' "${REAL_GO:-none}"
fi

# Outcome 4 of 4, the one most likely to be got wrong: nothing resolves, so
# NOTHING is bound, and the user is told. A dead keybind would misattribute the
# failure to press time rather than install time.
case_start resolve-4-nothing
out=$(plugin_run)
assert_eq 0 "$(count_plugin_keybinds_for t)" "NO popup keybind installed"
assert_eq 0 "$(count_tdo_hooks)" "no hook installed"
msgs=$(tm show-messages 2>/dev/null)
assert_contains "$msgs" "display-message" "a display-message was issued"
assert_contains "$msgs" "tmux-todo" "the message names the plugin"
assert_contains "$out" "tdo" "the diagnostic names the binary"
case_end

# Step 3 must not fire on an old toolchain: building with one fails with a
# message about the go directive that reads like a repo bug, not a toolchain one.
case_start resolve-4-go-too-old
cp -R "$REPO_ROOT/go.mod" "$REPO_ROOT/go.sum" "$REPO_ROOT/cmd" "$REPO_ROOT/internal" "$PLUGINDIR/"
stub "$PATHDIR/go" "go version go1.19.13 darwin/arm64"
out=$(plugin_run)
assert_eq 0 "$(count_plugin_keybinds_for t)" "NO popup keybind installed"
assert_no_file "$PLUGINDIR/bin/tdo" "no build attempted"
assert_contains "$out" "1.25" "the diagnostic names the required version"
case_end

# A version string it cannot parse must fall through to outcome 4, not
# optimistically build — devel and rc builds print differently.
case_start resolve-4-go-unparseable
cp -R "$REPO_ROOT/go.mod" "$REPO_ROOT/go.sum" "$REPO_ROOT/cmd" "$REPO_ROOT/internal" "$PLUGINDIR/"
stub "$PATHDIR/go" "go version weird-custom-build"
out=$(plugin_run)
assert_eq 0 "$(count_plugin_keybinds_for t)" "NO popup keybind installed"
assert_no_file "$PLUGINDIR/bin/tdo" "no build attempted"
case_end

# A go that IS new enough but whose build fails must also bind nothing.
case_start resolve-4-go-build-fails
stub "$PATHDIR/go" "go version go1.26.0 darwin/arm64"   # a stub: `go build` is a no-op
out=$(plugin_run)
assert_eq 0 "$(count_plugin_keybinds_for t)" "NO popup keybind installed"
assert_contains "$out" "tdo" "the diagnostic names the binary"
case_end

# ============================================================= @todo-key

case_start key-default
stub "$PATHDIR/tdo"
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for t)" "defaults to t when @todo-key is unset"
case_end

case_start key-empty-option
stub "$PATHDIR/tdo"
tm set-option -g @todo-key "" >/dev/null 2>&1
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for t)" "defaults to t when @todo-key is empty"
case_end

case_start key-override
stub "$PATHDIR/tdo"
tm set-option -g @todo-key w >/dev/null 2>&1
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for w)" "binds w when @todo-key is w"
assert_eq 0 "$(count_plugin_keybinds_for t)" "and installs no popup binding on t"
assert_contains "$(plugin_keybinds_for w)" "display-popup" "the override still opens the popup"
case_end

# ============================================================= idempotence

# TPM sources plugin scripts on every server start, so three runs is the real
# steady state rather than a pathological case.
case_start idempotence-three-runs
stub "$PATHDIR/tdo"
for run in 1 2 3; do
    plugin_run >/dev/null
    printf '    -- after run %s\n' "$run"
    printf '       show-hooks -g, tdo hooks   : %s  %s\n' \
        "$(count_tdo_hooks)" "$(tdo_hooks | sed 's/^/                                     /;1s/^ *//')"
    printf '       list-keys -T prefix, key t : %s popup binding(s), %s binding(s) total\n' \
        "$(count_plugin_keybinds_for t)" "$(count_all_keybinds_for t)"
done
assert_eq 1 "$(count_tdo_hooks)" "exactly one tdo hook after three runs"
assert_eq 1 "$(count_plugin_keybinds_for t)" "exactly one popup keybind on t after three runs"
assert_eq 1 "$(count_all_keybinds_for t)" "and t carries no second binding"
case_end

# A user's own session-renamed hooks must survive the install, three times over
# — that is the whole reason the behaviour task chose set-hook -ga over -g.
case_start user-hook-survives
stub "$PATHDIR/tdo"
canary=$CASEDIR/canary
tm set-hook -ga session-renamed "display-message 'user hook still here'" >/dev/null 2>&1
tm set-hook -ga session-renamed "run-shell 'touch $canary'" >/dev/null 2>&1
for run in 1 2 3; do plugin_run >/dev/null; done
hooks=$(tm show-hooks -g | grep 'session-renamed\[')
printf '    -- show-hooks -g after three runs:\n%s\n' "$(echo "$hooks" | sed 's/^/       /')"
assert_contains "$hooks" "user hook still here" "the user's display-message hook survives"
assert_contains "$hooks" "touch $canary" "the user's run-shell hook survives"
assert_eq 1 "$(count_tdo_hooks)" "exactly one tdo hook alongside them"
# ...and still fires: a real rename on a real server.
tm rename-session -t harness renamed >/dev/null 2>&1
for _ in 1 2 3 4 5 6 7 8 9 10; do [ -e "$canary" ] && break; sleep 0.2; done
if [ -e "$canary" ]; then
    ok "the user's hook still FIRES after the install"
else
    bad "the user's hook did not fire after the install"
fi
case_end

# ============================================================= steady state

# The constraint that shapes the whole script: tmux sources it on every server
# start, so the resolved path must do no build and touch no network. Masking go
# entirely proves no build can have happened.
case_start steady-state-no-build
stub "$PLUGINDIR/bin/tdo"
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for t)" "keybind installed with no go on PATH at all"
assert_absent "$out" "building" "no build announced"
assert_eq "" "$out" "the steady-state run is silent"
runs=20
start=$(python3 -c 'import time;print(int(time.time()*1000))' 2>/dev/null || echo 0)
for _ in $(seq $runs); do plugin_run >/dev/null; done
end=$(python3 -c 'import time;print(int(time.time()*1000))' 2>/dev/null || echo 0)
if [ "$start" != 0 ] && [ "$end" != 0 ]; then
    printf '    -- steady state: %s ms/run over %s runs (whole script: 4 tmux calls)\n' \
        "$(( (end - start) / runs ))" "$runs"
fi
case_end

# ================================================================== summary

printf '\n%s\n' "----------------------------------------"
printf 'plugin harness: %s passed, %s failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
