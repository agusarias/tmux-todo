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
#   * The download step would otherwise reach the REAL GitHub release from every
#     case that resolves nothing — a network call per case, and an assertion that
#     starts failing the day v0.1.0 ships. Every run therefore gets
#     $TDO_RELEASE_BASE_URL pointed at a local fixture, defaulting to one that
#     does not exist, so a case downloads only what it opted into.
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
# `curl` and `wget` cannot be sandboxed by shadowing: "no downloader on this box"
# means `command -v curl` FAILS, and a stub makes it succeed. So one PATH is
# built with everything the script needs except those two — by wildcard rather
# than a curated list, because a tool missing from a hand-written list would make
# the case pass for the wrong reason (`grep` absent = the guard never runs).
SYSBIN_NO_DL=$TMPROOT/sysbin-no-downloader
mkdir -p "$SYSBIN_NO_DL"
for d in /usr/bin /bin /usr/sbin /sbin; do
    [ -d "$d" ] || continue
    for f in "$d"/*; do
        n=${f##*/}
        case $n in curl|wget) continue ;; esac
        [ -e "$SYSBIN_NO_DL/$n" ] || ln -s "$f" "$SYSBIN_NO_DL/$n" 2>/dev/null
    done
done
for t in curl wget; do
    if ( PATH=$SYSBIN_NO_DL; export PATH; command -v "$t" >/dev/null 2>&1 ); then
        echo "FAIL: $t is still reachable in the no-downloader sandbox. Aborting."
        exit 1
    fi
done
# ...and the same sandbox must NOT be degenerate: a PATH missing grep or mktemp
# would make the download cases pass without the script ever getting that far.
for t in grep mktemp mkdir mv rm chmod awk uname; do
    if ! ( PATH=$SYSBIN_NO_DL; export PATH; command -v "$t" >/dev/null 2>&1 ); then
        echo "FAIL: the no-downloader sandbox has no $t, so its cases would be"
        echo "      vacuous rather than failing loudly. Aborting."
        exit 1
    fi
done

# The base URL every case gets unless it sets RELEASE_BASE itself. It must not
# exist: a case that has not built a fixture must get a failed download, never a
# request to github.com.
NO_RELEASE_URL="file://$TMPROOT/no-such-release"
if [ -e "$TMPROOT/no-such-release" ]; then
    echo "FAIL: $TMPROOT/no-such-release exists; the default fixture must be absent."
    exit 1
fi

# The asset this machine's uname maps to, computed independently of the script so
# it is a real expectation rather than an echo of the code under test.
case $(uname -s) in
    Darwin) HOST_OS=darwin ;;
    Linux)  HOST_OS=linux ;;
    *)      HOST_OS='' ;;
esac
case $(uname -m) in
    x86_64|amd64)  HOST_ARCH=amd64 ;;
    arm64|aarch64) HOST_ARCH=arm64 ;;
    *)             HOST_ARCH='' ;;
esac
HOST_ASSET=''
[ -z "$HOST_OS$HOST_ARCH" ] || HOST_ASSET="tdo-$HOST_OS-$HOST_ARCH"

SHA_TOOL=''
if command -v sha256sum >/dev/null 2>&1; then SHA_TOOL='sha256sum'
elif command -v shasum >/dev/null 2>&1; then SHA_TOOL='shasum -a 256'
fi

# A fixture release is served over real HTTP when python3 is here, and over
# file:// otherwise. HTTP is worth the process: it is the only way a case gets a
# genuine 404 rather than a missing local file, and `curl -f` is precisely what
# turns that 404 into a failure instead of a 9-byte HTML page installed as
# bin/tdo. wget also refuses file:// URLs outright, so the wget leg needs it.
PY3=$(command -v python3 || true)

# Can `show-messages` observe a display-message on a server with no attached
# client? On tmux 3.7b it can; on 3.4 — ubuntu-latest's apt tmux, and the first
# CI run that surfaced this — it cannot, so three assertions that read the
# message log are simply unanswerable there.
#
# This is a capability of the observation mechanism, not of the plugin. The
# *content* of every one of those diagnostics is already asserted against the
# script's own stderr, which is version-independent; what the message-log
# assertions add on top is that the tmux channel fired at all. So detect, and
# skip loudly where it cannot be seen, rather than dropping the assertions
# everywhere or failing on a tmux that cannot answer the question.
# The rename-hook cases need the REAL binary, not a stub: what they assert is
# what `tdo session-renamed` does about the session it was fired for, and a shell
# stub has no opinion about that. Built once, into TMPROOT, from this checkout.
#
# Every other case in this file deliberately uses stubs — they assert on what the
# *plugin script* installed, and a stub makes "which binary got resolved"
# observable. These cases are the opposite: the plugin script is incidental and
# the binary is the subject.
TDO_BIN=''
if [ "$GO_OK" = yes ]; then
    _bin=$TMPROOT/real/tdo
    mkdir -p "$TMPROOT/real"
    if ( cd "$REPO_ROOT" && CGO_ENABLED=0 "$REAL_GO" build -o "$_bin" ./cmd/tdo ) 2>"$TMPROOT/real/build.log"; then
        TDO_BIN=$_bin
    else
        printf 'WARN: could not build tdo for the rename-hook cases:\n%s\n' \
            "$(cat "$TMPROOT/real/build.log")"
    fi
    unset _bin
fi

CAN_SEE_MSGS=''
_msgsock="tdo-msgprobe-$$"
if tmux -L "$_msgsock" new-session -d -s probe 2>/dev/null; then
    tmux -L "$_msgsock" display-message 'tdo-msgprobe-canary' 2>/dev/null
    case $(tmux -L "$_msgsock" show-messages 2>/dev/null) in
        *tdo-msgprobe-canary*) CAN_SEE_MSGS=yes ;;
    esac
    tmux -L "$_msgsock" kill-server 2>/dev/null
fi
unset _msgsock
FIXTURE_PIDS=()

SOCKETS=()
PASS=0
FAIL=0

cleanup() {
    local s pid
    for s in ${SOCKETS[@]+"${SOCKETS[@]}"}; do
        "$REAL_TMUX" -L "$s" kill-server >/dev/null 2>&1
    done
    for pid in ${FIXTURE_PIDS[@]+"${FIXTURE_PIDS[@]}"}; do
        kill "$pid" >/dev/null 2>&1
    done
    rm -rf "$TMPROOT"
}
trap cleanup EXIT

# ------------------------------------------------------- key-name bindability
#
# Whether tmux accepts a given *key name* is a property of the tmux running the
# suite, not of the plugin. ubuntu-latest's 3.4 rejects at least one of the
# quote-containing names that 3.7b binds happily, which made
# closekey-6-quote-in-key-* red in CI for a reason that has nothing to do with
# the code under test.
#
# So ask, rather than guess. A `tmux -V` comparison would encode today's belief
# about which release changed the key parser and would go stale silently; a real
# bind-key answers the actual question on whatever tmux is present, including
# ones that do not exist yet.
#
# The probe gets its OWN server and its OWN key table. Nothing else touches
# `tdo-keyprobe`, and the bindings it makes are `display-message` rather than
# `display-popup`, so even a leaked one could not be counted by
# count_plugin_keybinds_for. It is a separate server rather than each case's own
# so that one definition answers both the header line and the loop below — a
# header that could disagree with what actually ran would be worse than no
# header.
QUOTE_KEY_SPECS=("apostrophe:'" 'quote:"' "alt-apostrophe:M-'" 'ctrl-quote:C-"')

KEYPROBE_SOCK="tdo-keyprobe-$$"
KEYPROBE_OK=''
if "$REAL_TMUX" -L "$KEYPROBE_SOCK" -f /dev/null new-session -d -s probe >/dev/null 2>&1; then
    KEYPROBE_OK=yes
    SOCKETS+=("$KEYPROBE_SOCK")
fi

# tmux_can_bind <key> — 0 if this tmux accepts the key name.
#
# **Fails OPEN.** With no usable probe server the answer is "bindable", so every
# case runs exactly as it did before this guard existed. That direction is
# deliberate: an undetermined probe must never be a reason to assert less, and a
# case that runs either passes or reports a real failure. Failing closed would
# turn a broken probe into a silently empty suite.
tmux_can_bind() {
    [ -n "$KEYPROBE_OK" ] || return 0
    "$REAL_TMUX" -L "$KEYPROBE_SOCK" bind-key -T tdo-keyprobe "$1" display-message ok >/dev/null 2>&1 || return 1
    "$REAL_TMUX" -L "$KEYPROBE_SOCK" unbind-key -T tdo-keyprobe "$1" >/dev/null 2>&1
    return 0
}

# bindable_quote_keys — the subset of QUOTE_KEY_SPECS' keys this tmux accepts,
# space separated, for the header line.
bindable_quote_keys() {
    local spec out=''
    for spec in "${QUOTE_KEY_SPECS[@]}"; do
        tmux_can_bind "${spec#*:}" && out="$out ${spec#*:}"
    done
    printf '%s' "${out# }"
}

echo "plugin script : $PLUGIN_SCRIPT"
echo "tmux          : $REAL_TMUX ($("$REAL_TMUX" -V))"
echo "bash          : $REAL_BASH ($("$REAL_BASH" --version | head -1))"
echo "go            : ${REAL_GO:-none} (usable for the build case: $GO_OK)"
echo "host asset    : ${HOST_ASSET:-<unmapped platform>}"
echo "sha256 tool   : ${SHA_TOOL:-none}"
echo "downloader    : curl=$(command -v curl || echo none) wget=$(command -v wget || echo none)"
echo "fixture server: ${PY3:-none (fixtures fall back to file://)}"
echo "real tdo      : ${TDO_BIN:-none (rename-hook cases skip)}"
if [ -n "$CAN_SEE_MSGS" ]; then
    echo "show-messages : observable"
else
    echo "show-messages : NOT observable on this tmux; message-log assertions skip"
fi
if [ -z "$KEYPROBE_OK" ]; then
    echo "quote keys    : probe server unavailable; every quote-key case runs unguarded"
else
    _bindable=$(bindable_quote_keys)
    echo "quote keys    : this tmux binds [${_bindable:-none}]; unbindable names skip"
    unset _bindable
fi

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
assert_file() { # path label
    if [ -e "$1" ]; then ok "$2"; else bad "$2 ($1 missing)"; fi
}
assert_exec() { # path label
    if [ -x "$1" ]; then ok "$2"; else bad "$2 ($1 not executable)"; fi
}
# The temp file the download writes through must never survive a run: a
# leftover .tdo.XXXXXX is a half-written binary sitting next to the real one.
assert_no_temp_leftover() { # bindir label
    local n
    n=$(ls -a "$1" 2>/dev/null | grep -c '^\.tdo\.')
    if [ "$n" = 0 ]; then ok "$2"; else bad "$2 ($n leftover .tdo.* in $1)"; fi
}

# ---------------------------------------------------------------- case setup

# case_start <name> [VAR=VALUE ...] — a private server, plus a sandbox PATH whose
# front holds:
#   tmux  : a shim pinning every call the script makes to this case's socket
#   bash  : so the script's own #!/usr/bin/env bash shebang resolves
# followed by the system tool dirs. `tdo` and `go` live in neither, so they are
# reachable only when a case puts them there.
#
# Any VAR=VALUE arguments go into the environment of the *server*, and they have
# to be set here rather than by a case afterwards: a run-shell child inherits the
# server's environment, not a pane's and not the harness's, so XDG_DATA_HOME
# exported anywhere later would leave a hook child opening the developer's real
# database. See the rename-hook section.
case_start() {
    CASE=$1
    shift
    CASEDIR=$TMPROOT/$CASE
    # Opt-in knobs, reset per case: RELEASE_BASE points the download step at a
    # fixture, RUN_PATH replaces the sandbox PATH wholesale.
    RELEASE_BASE=''
    RUN_PATH=''
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
    env "$@" "$REAL_TMUX" -L "$SOCK" -f /dev/null new-session -d -s harness >/dev/null 2>&1
    printf '\n== %s\n' "$CASE"
}

case_end() {
    local pid
    "$REAL_TMUX" -L "$SOCK" kill-server >/dev/null 2>&1
    for pid in ${FIXTURE_PIDS[@]+"${FIXTURE_PIDS[@]}"}; do
        kill "$pid" >/dev/null 2>&1
    done
    FIXTURE_PIDS=()
}

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
    env -i HOME="$HOME" PATH="${RUN_PATH:-$PATHDIR:$SYS_PATH}" TERM=dumb \
        TMPDIR="${TMPDIR:-/tmp}" \
        TDO_RELEASE_BASE_URL="${RELEASE_BASE:-$NO_RELEASE_URL}" \
        "$PLUGINDIR/tmux-todo.tmux" 2>&1
}

# A stand-in release, served over file://. The URL construction, the fetch, the
# checksum comparison and the atomic move are all the real code paths; only the
# transport is local. The same leg against a real GitHub asset belongs to
# docs/tasks/2026-08-20-prove-ci-and-cut-v0.1.0.md, which owns the first tag.
#
# Every asset is a runnable stub whose body names itself, so an assertion can
# tell WHICH asset was installed — that is what makes the uname table real.
fixture_release() { # <dir> <asset>...
    local dir=$1 a
    shift
    mkdir -p "$dir"
    for a in "$@"; do
        printf '#!/bin/sh\necho "%s"\n' "$a" >"$dir/$a"
        chmod +x "$dir/$a"
    done
    ( cd "$dir" && $SHA_TOOL "$@" >checksums.txt )
    serve_fixture "$dir"
}

# Sets RELEASE_BASE to a URL serving <dir>. One short-lived python3 http.server
# on 127.0.0.1 per fixture, on a kernel-assigned port so parallel runs cannot
# collide; file:// is the fallback when python3 is absent, at the cost of the
# 404 and wget legs.
serve_fixture() { # dir
    local log port i
    if [ -z "$PY3" ]; then
        RELEASE_BASE="file://$1"
        return 0
    fi
    log=$TMPROOT/httpd-$CASE.log
    # -u, or python buffers the "Serving HTTP on ... port NNNNN" line when stdout
    # is a file and the port is never read — which does not fail, it silently
    # falls back to file:// and takes the real-404 and wget legs with it.
    # disown, or the shell announces every teardown with a "Terminated: 15" line.
    "$PY3" -u -m http.server --bind 127.0.0.1 --directory "$1" 0 >"$log" 2>&1 &
    FIXTURE_PIDS+=($!)
    disown %% 2>/dev/null || true
    port=''
    for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15; do
        port=$(sed -n 's/.*port \([0-9][0-9]*\).*/\1/p' "$log" 2>/dev/null | head -1)
        [ -n "$port" ] && break
        sleep 0.2
    done
    if [ -z "$port" ]; then
        printf '    -- NO fixture server (%s); falling back to file://, so this\n' \
            "$(head -1 "$log" 2>/dev/null || true)"
        printf '       case no longer covers a real HTTP 404\n'
        RELEASE_BASE="file://$1"
        return 0
    fi
    RELEASE_BASE="http://127.0.0.1:$port"
}

# The same, with one asset's recorded sum corrupted. Not a truncated file and not
# a missing line: a checksums.txt that positively DISAGREES, which is the one
# case DoD 8 says must be fatal.
fixture_release_bad_sum() { # <dir> <asset>...
    fixture_release "$@"
    local dir=$1 asset=$2
    awk -v a="$asset" '{ if ($2 == a) print "0000000000000000000000000000000000000000000000000000000000000000  " a; else print }' \
        "$dir/checksums.txt" >"$dir/checksums.txt.new"
    mv "$dir/checksums.txt.new" "$dir/checksums.txt"
}

# uname, table-tested: the script asks for -s and -m separately, so the stub
# answers per flag.
stub_uname() { # <path> <uname -s> <uname -m>
    printf '#!/bin/sh\ncase $1 in\n-s) echo "%s" ;;\n-m) echo "%s" ;;\n*) echo "%s" ;;\nesac\n' \
        "$2" "$3" "$2" >"$1"
    chmod +x "$1"
}

# A downloader that must never run. Touching a canary rather than merely failing
# is what turns "the steady state does no network call" into an assertion instead
# of a hope.
stub_loud_downloader() { # <path> <canary>
    printf '#!/bin/sh\ntouch %s\nexit 1\n' "$2" >"$1"
    chmod +x "$1"
}

tm() { "$REAL_TMUX" -L "$SOCK" "$@"; }

# THE PLUGIN's bindings for a key, not tmux's. list-keys prints
# `bind-key -T prefix <key> <command>`, so field 4 is the key; the plugin's
# command is the only one that opens a popup. Counting bare bindings for the key
# would count tmux's default clock-mode binding and pass with the plugin gone.
# The optional second argument is the key table, defaulting to prefix so the
# resolution cases read as before; the root-table cases pass it explicitly.
# `list-keys -T <table> <key>` would be shorter than awk over the whole table,
# but on tmux 3.7b combining -T with a key argument prints NOTHING and exits 0 —
# for a key that IS bound. An assertion of 0 built on it passes while the binding
# sits right there, which is the wrong direction to be wrong in.
# The awk unquotes field 4 first, because tmux re-quotes a key name when it
# prints it and the spelling it picks depends on the key: `t` comes back bare,
# `'` as `\'`, `M-'` as `"M-'"`, and `C-"` as `'C-"'`. A bare `$4 == k` therefore
# answers "not bound" for a key that is sitting right there — the wrong direction
# to be wrong in, since an assertion of zero passes. Bare keys are unaffected, so
# this is a no-op for every case that predates the close-key section.
KEY_AWK='
function unq(s) {
    sub(/^\\/, "", s)
    if (s ~ /^".*"$/ || s ~ /^\047.*\047$/) s = substr(s, 2, length(s) - 2)
    return s
}
unq($4) == k'
plugin_keybinds_for() {
    tm list-keys -T "${2:-prefix}" 2>/dev/null | awk -v k="$1" "$KEY_AWK" | grep 'display-popup'
}
count_plugin_keybinds_for() { plugin_keybinds_for "$1" "${2:-prefix}" | grep -c . ; }
count_all_keybinds_for() {
    tm list-keys -T "${2:-prefix}" 2>/dev/null | awk -v k="$1" "$KEY_AWK" | grep -c .
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
if [ -n "$CAN_SEE_MSGS" ]; then
    msgs=$(tm show-messages 2>/dev/null)
    assert_contains "$msgs" "display-message" "a display-message was issued"
    assert_contains "$msgs" "tmux-todo" "the message names the plugin"
else
    printf '    SKIP show-messages cannot observe a display-message on this tmux\n'
fi
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

# ================================================== download (step 3)

# Everything here needs a name to fetch and a way to check a sum. Without either,
# SKIP loudly rather than pass quietly.
if [ -z "$HOST_ASSET" ] || [ -z "$SHA_TOOL" ]; then
    printf '\n== download\n    SKIP asset=%s sha=%s on %s/%s\n' \
        "${HOST_ASSET:-none}" "${SHA_TOOL:-none}" "$(uname -s)" "$(uname -m)"
else

# The happy path: an asset for this platform whose sum is in checksums.txt.
case_start dl-1-verified
fixture_release "$CASEDIR/release" "$HOST_ASSET"
assert_no_file "$PLUGINDIR/bin/tdo" "precondition: no binary before the run"
out=$(plugin_run)
assert_exec "$PLUGINDIR/bin/tdo" "the download landed an executable bin/tdo"
assert_eq "$HOST_ASSET" "$("$PLUGINDIR/bin/tdo" 2>/dev/null)" "and it is the asset for THIS platform"
assert_eq 1 "$(count_plugin_keybinds_for t)" "one popup keybind on t"
assert_contains "$(plugin_keybinds_for t)" "$PLUGINDIR/bin/tdo tui" "keybind points at the downloaded binary"
assert_eq 1 "$(count_tdo_hooks)" "one tdo session-renamed hook"
assert_contains "$out" "downloading" "the one-off download is announced"
assert_absent "$out" "UNVERIFIED" "no unverified warning when the sum checks out"
assert_no_temp_leftover "$PLUGINDIR/bin" "no leftover .tdo.* temp file"
# ...and the next server start resolves at step 2, so it must not download again.
stamp_before=$(ls -l "$PLUGINDIR/bin/tdo")
out2=$(plugin_run)
assert_eq "$stamp_before" "$(ls -l "$PLUGINDIR/bin/tdo")" "a second run does not re-download"
assert_eq "" "$out2" "and is silent"
case_end

# DoD 8's fatal case, and the highest-risk line in the whole plugin: a
# checksums.txt that positively DISAGREES with what arrived. Not a missing file
# and not a missing line — those are the best-effort cases below. The mutation
# proof for this assertion lives in the task brief's Evidence: with the checksum
# comparison deleted, these four assertions must fail.
case_start dl-2-checksum-mismatch
fixture_release_bad_sum "$CASEDIR/release" "$HOST_ASSET"
out=$(plugin_run)
assert_no_file "$PLUGINDIR/bin/tdo" "a mismatching download is NOT installed"
assert_no_temp_leftover "$PLUGINDIR/bin" "and the temp copy is deleted, not left behind"
assert_eq 0 "$(count_plugin_keybinds_for t)" "NO popup keybind installed"
assert_eq 0 "$(count_tdo_hooks)" "no hook installed"
assert_contains "$out" "checksum" "the diagnostic names the checksum"
case_end

# A release that exists but has no asset for this platform. Over HTTP this is a
# real 404, which is what makes `curl -f` load-bearing: without it curl saves the
# error page and exits 0, so bin/tdo becomes an HTML document the keybind then
# points at.
case_start dl-3-asset-404
fixture_release "$CASEDIR/release" tdo-linux-riscv64
out=$(plugin_run)
assert_no_file "$PLUGINDIR/bin/tdo" "a 404 installs nothing"
assert_eq 0 "$(count_plugin_keybinds_for t)" "NO popup keybind installed"
assert_no_temp_leftover "$PLUGINDIR/bin" "no leftover .tdo.* temp file"
assert_contains "$out" "$HOST_ASSET" "the diagnostic names the asset it wanted"
case_end

# Best-effort verification, case 1: no checksums.txt at all. The user's ruling is
# that this must not block an install — but it must not be silent either, so the
# warning goes through the same display-message channel as the no-binary path.
case_start dl-4-no-checksums-file
fixture_release "$CASEDIR/release" "$HOST_ASSET"
rm -f "$CASEDIR/release/checksums.txt"
out=$(plugin_run)
assert_exec "$PLUGINDIR/bin/tdo" "best-effort: the download is used anyway"
assert_eq 1 "$(count_plugin_keybinds_for t)" "and the keybind is installed"
assert_contains "$out" "UNVERIFIED" "the diagnostic says it is unverified"
if [ -n "$CAN_SEE_MSGS" ]; then
    assert_contains "$(tm show-messages 2>/dev/null)" "UNVERIFIED" "and the user is told via display-message"
else
    printf '    SKIP show-messages cannot observe a display-message on this tmux\n'
fi
case_end

# Best-effort verification, case 2: a checksums.txt that simply has no line for
# this asset. Distinct from case 1 in the code and worth its own case — reading
# "no line" as a mismatch would block installs on any release whose checksums
# file lags a platform.
case_start dl-5-asset-absent-from-checksums
fixture_release "$CASEDIR/release" "$HOST_ASSET" tdo-linux-riscv64
awk -v a="$HOST_ASSET" '$2 != a' "$CASEDIR/release/checksums.txt" >"$CASEDIR/sums.new"
mv "$CASEDIR/sums.new" "$CASEDIR/release/checksums.txt"
assert_absent "$(cat "$CASEDIR/release/checksums.txt")" "$HOST_ASSET" "precondition: our asset is not in checksums.txt"
out=$(plugin_run)
assert_exec "$PLUGINDIR/bin/tdo" "best-effort: the download is used anyway"
assert_contains "$out" "UNVERIFIED" "the diagnostic says it is unverified"
case_end

# Offline / DNS failure. The stub is what a real curl does on an unreachable
# host: nothing written, non-zero exit. The canary makes "the downloader ran" an
# assertion rather than an inference.
case_start dl-6-offline
fixture_release "$CASEDIR/release" "$HOST_ASSET"
stub_loud_downloader "$PATHDIR/curl" "$CASEDIR/curl-ran"
stub_loud_downloader "$PATHDIR/wget" "$CASEDIR/wget-ran"
out=$(plugin_run)
assert_file "$CASEDIR/curl-ran" "the downloader was invoked"
assert_no_file "$PLUGINDIR/bin/tdo" "a failing downloader installs nothing"
assert_eq 0 "$(count_plugin_keybinds_for t)" "NO popup keybind installed"
assert_no_temp_leftover "$PLUGINDIR/bin" "no leftover .tdo.* temp file"
case_end

# A 403 / rate limit, which `curl -f` reports as exit 22 with no body saved. The
# script's contract is the same for every non-zero downloader exit, and this
# pins that the contract is "fall through", not "fail the tmux server start".
case_start dl-7-http-error
fixture_release "$CASEDIR/release" "$HOST_ASSET"
printf '#!/bin/sh\nexit 22\n' >"$PATHDIR/curl"
chmod +x "$PATHDIR/curl"
printf '#!/bin/sh\nexit 8\n' >"$PATHDIR/wget"
chmod +x "$PATHDIR/wget"
out=$(plugin_run)
assert_no_file "$PLUGINDIR/bin/tdo" "an HTTP error installs nothing"
assert_eq 0 "$(count_plugin_keybinds_for t)" "NO popup keybind installed"
case_end

# No downloader on the box at all — `command -v curl` must FAIL, which a stub
# cannot express, so this case swaps the whole PATH for the no-downloader
# sandbox. That sandbox is asserted non-degenerate at startup, and the control
# case below closes the loop.
case_start dl-8-no-downloader
fixture_release "$CASEDIR/release" "$HOST_ASSET"
RUN_PATH="$PATHDIR:$SYSBIN_NO_DL"
out=$(plugin_run)
assert_no_file "$PLUGINDIR/bin/tdo" "nothing downloaded with neither curl nor wget"
assert_eq 0 "$(count_plugin_keybinds_for t)" "NO popup keybind installed"
assert_contains "$out" "curl" "the diagnostic names what is missing"
case_end

# The control for dl-8: the SAME sandbox, plus curl, must succeed. Without this,
# dl-8 would pass just as green if the sandbox were missing `mktemp` or `awk`.
if [ -n "$(command -v curl || true)" ]; then
    case_start dl-8b-no-downloader-control
    fixture_release "$CASEDIR/release" "$HOST_ASSET"
    ln -s "$(command -v curl)" "$PATHDIR/curl"
    RUN_PATH="$PATHDIR:$SYSBIN_NO_DL"
    out=$(plugin_run)
    assert_exec "$PLUGINDIR/bin/tdo" "the same sandbox WITH curl does download, so dl-8 is not vacuous"
    assert_eq "$HOST_ASSET" "$("$PLUGINDIR/bin/tdo" 2>/dev/null)" "and installed the right asset"
    case_end
fi

# wget is the other half of DoD 10 and takes a different argument spelling, so it
# gets its own leg rather than being assumed equivalent. It cannot be tested
# against a file:// fixture: wget rejects that scheme outright.
if [ -n "$(command -v wget || true)" ] && [ -n "$PY3" ]; then
    case_start dl-9-wget-only
    fixture_release "$CASEDIR/release" "$HOST_ASSET"
    ln -s "$(command -v wget)" "$PATHDIR/wget"
    RUN_PATH="$PATHDIR:$SYSBIN_NO_DL"
    out=$(plugin_run)
    assert_exec "$PLUGINDIR/bin/tdo" "wget alone is enough to download"
    assert_eq "$HOST_ASSET" "$("$PLUGINDIR/bin/tdo" 2>/dev/null)" "and it installed the right asset"
    assert_eq 1 "$(count_plugin_keybinds_for t)" "one popup keybind on t"
    case_end
else
    printf '\n== dl-9-wget-only\n    SKIP wget=%s python3=%s\n' \
        "$(command -v wget || echo none)" "${PY3:-none}"
fi

# uname, table-tested. Four platforms, one asset each, all four served at once —
# so a mapping that is merely *plausible* installs the wrong file and the
# assertion says which. `arm64` vs `aarch64` and `x86_64` vs `amd64` are the two
# places this goes wrong, and both directions get a row.
printf '\n== dl-10-uname-table\n'
# NOT `... | while read`: a pipeline runs its right-hand side in a SUBSHELL, so
# every ok/bad inside would increment PASS and FAIL in a child process and the
# summary would never see them — six rows of assertions that cannot fail the
# suite. Iterating in the current shell is the whole point here.
for row in Darwin:arm64:tdo-darwin-arm64 \
           Darwin:x86_64:tdo-darwin-amd64 \
           Darwin:amd64:tdo-darwin-amd64 \
           Linux:x86_64:tdo-linux-amd64 \
           Linux:aarch64:tdo-linux-arm64 \
           Linux:arm64:tdo-linux-arm64; do
    u_s=${row%%:*}
    u_m=${row#*:}; u_m=${u_m%%:*}
    want=${row##*:}
    case_start "dl-10-uname-$u_s-$u_m"
    fixture_release "$CASEDIR/release" \
        tdo-darwin-arm64 tdo-darwin-amd64 tdo-linux-amd64 tdo-linux-arm64
    stub_uname "$PATHDIR/uname" "$u_s" "$u_m"
    out=$(plugin_run)
    if [ -x "$PLUGINDIR/bin/tdo" ]; then
        assert_eq "$want" "$("$PLUGINDIR/bin/tdo" 2>/dev/null)" "uname -s=$u_s -m=$u_m installs $want"
    else
        bad "uname -s=$u_s -m=$u_m installed nothing (wanted $want; output: $out)"
    fi
    case_end
done

# ...and a platform we ship no binary for skips the step entirely and falls
# through. Two rows: an unmapped OS and an unmapped machine.
for row in FreeBSD:amd64 Linux:riscv64; do
    u_s=${row%%:*}
    u_m=${row##*:}
    case_start "dl-11-unmapped-$u_s-$u_m"
    fixture_release "$CASEDIR/release" \
        tdo-darwin-arm64 tdo-darwin-amd64 tdo-linux-amd64 tdo-linux-arm64
    stub_uname "$PATHDIR/uname" "$u_s" "$u_m"
    out=$(plugin_run)
    assert_no_file "$PLUGINDIR/bin/tdo" "uname -s=$u_s -m=$u_m downloads nothing"
    assert_eq 0 "$(count_plugin_keybinds_for t)" "and installs NO popup keybind"
    assert_contains "$out" "no released binary" "the diagnostic says the platform is unsupported"
    case_end
done

# Precedence, the two directions that matter. A user's own tdo and an existing
# plugin-local binary must both beat the download, or an install that works today
# starts making a network call at every tmux server start.
case_start dl-12-path-beats-download
fixture_release "$CASEDIR/release" "$HOST_ASSET"
stub "$PATHDIR/tdo"
out=$(plugin_run)
assert_contains "$(plugin_keybinds_for t)" "$PATHDIR/tdo tui" "PATH wins over the download"
assert_no_file "$PLUGINDIR/bin/tdo" "and nothing was downloaded at all"
case_end

case_start dl-13-plugin-bin-beats-download
fixture_release "$CASEDIR/release" "$HOST_ASSET"
stub "$PLUGINDIR/bin/tdo" existing-build
out=$(plugin_run)
assert_eq existing-build "$("$PLUGINDIR/bin/tdo" 2>/dev/null)" "the existing plugin binary is untouched"
assert_contains "$(plugin_keybinds_for t)" "$PLUGINDIR/bin/tdo tui" "and is what the keybind points at"
case_end

# ...and the download must beat the source build, which is the ordering ruling
# this task exists for: a fresh install on a machine WITH go must still not spend
# a compile.
if [ "$GO_OK" = yes ]; then
    case_start dl-14-download-beats-go-build
    fixture_release "$CASEDIR/release" "$HOST_ASSET"
    cp -R "$REPO_ROOT/go.mod" "$REPO_ROOT/go.sum" "$REPO_ROOT/cmd" "$REPO_ROOT/internal" "$PLUGINDIR/"
    ln -s "$REAL_GO" "$PATHDIR/go"
    out=$(plugin_run)
    assert_eq "$HOST_ASSET" "$("$PLUGINDIR/bin/tdo" 2>/dev/null)" "the DOWNLOAD won, not the build"
    assert_absent "$out" "building" "no build was announced"
    case_end
else
    printf '\n== dl-14-download-beats-go-build\n    SKIP no go >= 1.25 on PATH\n'
fi

# ...but a failed download must still leave the source build its turn, or the new
# step 3 has silently replaced step 4 instead of preceding it.
if [ "$GO_OK" = yes ]; then
    case_start dl-15-failed-download-falls-through-to-build
    cp -R "$REPO_ROOT/go.mod" "$REPO_ROOT/go.sum" "$REPO_ROOT/cmd" "$REPO_ROOT/internal" "$PLUGINDIR/"
    ln -s "$REAL_GO" "$PATHDIR/go"
    # No fixture at all: RELEASE_BASE stays the default, which does not exist.
    out=$(plugin_run)
    assert_exec "$PLUGINDIR/bin/tdo" "the source build still ran after the download failed"
    assert_contains "$out" "building" "and announced itself"
    if "$PLUGINDIR/bin/tdo" --version >/dev/null 2>&1; then
        ok "the built binary runs (--version exits 0)"
    else
        bad "the built binary does not run"
    fi
    case_end
else
    printf '\n== dl-15-failed-download-falls-through-to-build\n    SKIP no go >= 1.25 on PATH\n'
fi

fi # HOST_ASSET / SHA_TOOL guard

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

# ======================================================= @todo-key-table

# The default table has to be asserted in the NEGATIVE too: `$4 == k` over
# list-keys -T root matches nothing when the table is empty, so "1 in prefix"
# alone would also pass for a plugin that bound both tables.
case_start table-default
stub "$PATHDIR/tdo"
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for t prefix)" "binds in prefix when @todo-key-table is unset"
assert_eq 0 "$(count_plugin_keybinds_for t root)" "and nothing in root"
case_end

# The case this option exists for: a config that has already spent its C-<key>
# space on tmux wants the popup with no prefix at all.
case_start table-root
stub "$PATHDIR/tdo"
tm set-option -g @todo-key-table root >/dev/null 2>&1
tm set-option -g @todo-key C-l >/dev/null 2>&1
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for C-l root)" "binds C-l in root when @todo-key-table is root"
assert_eq 0 "$(count_plugin_keybinds_for C-l prefix)" "and NOT in prefix"
assert_eq 0 "$(count_plugin_keybinds_for t prefix)" "and leaves prefix t alone"
assert_eq 1 "$(count_all_keybinds_for C-l root)" "exactly one binding on root C-l, so a second is still caught"
assert_contains "$(plugin_keybinds_for C-l root)" "display-popup" "the root binding still opens the popup"
case_end

# A typo must not cost the user their keybind: fall back to prefix and say so.
# `tmux bind-key -T <nonsense>` would otherwise create a table nothing can reach.
case_start table-invalid
stub "$PATHDIR/tdo"
tm set-option -g @todo-key-table prefx >/dev/null 2>&1
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for t prefix)" "falls back to prefix on an unsupported table"
assert_eq 0 "$(count_plugin_keybinds_for t prefx)" "and creates no binding in the bogus table"
assert_contains "$out" "@todo-key-table" "the diagnostic names the option"
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

# ============================================== TDO_POPUP_KEY (close key)

# The popup closes on the key that opened it, which needs @todo-key to reach the
# program *inside* the popup. tmux cannot help: while a display-popup has focus
# the outer client's binding never fires and there is no popup key table to bind
# a closer in. So the plugin puts the key in the popup's environment with
# `display-popup -e` and tdo acts on the second press.
#
# What these cases guard is the bind body, which tmux parses on every server
# start. A malformed argument here does not degrade the feature — it stops the
# popup opening for every existing install.

# popup_env_count <key> <table> — how many of the plugin's four display-popup
# branches carry TDO_POPUP_KEY. Four is "all of them"; zero is "none".
# list-keys prints the whole brace block on one line, so this counts occurrences
# within it rather than lines.
popup_env_count() {
    plugin_keybinds_for "$1" "${2:-prefix}" |
        grep -o 'TDO_POPUP_KEY' | grep -c .
}

# The number of display-popup branches, so "every branch carries it" is asserted
# against the real count rather than a literal 4 that silently drifts if a fifth
# size branch is ever added.
popup_branch_count() {
    plugin_keybinds_for "$1" "${2:-prefix}" | grep -o 'display-popup' | grep -c .
}

# DoD 2, the root case: every branch carries the key, and it is the *configured*
# key rather than a hardcoded one.
case_start closekey-1-root-carries-the-key
stub "$PATHDIR/tdo"
tm set-option -g @todo-key-table root >/dev/null 2>&1
tm set-option -g @todo-key C-l >/dev/null 2>&1
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for C-l root)" "the root binding is installed"
branches=$(popup_branch_count C-l root)
assert_eq 4 "$branches" "four display-popup size branches, as before"
assert_eq "$branches" "$(popup_env_count C-l root)" "every branch carries TDO_POPUP_KEY"
# tmux re-quotes the bind body when printing it and drops quotes it does not
# need, so the value comes back as TDO_POPUP_KEY=C-l rather than the
# TDO_POPUP_KEY='C-l' the script wrote.
assert_contains "$(plugin_keybinds_for C-l root)" "TDO_POPUP_KEY=C-l" "and it carries the configured key"
case_end

# ...and it follows @todo-key rather than being hardcoded. A different key must
# produce a different environment value, which is what a hardcoded 'C-l' would
# fail.
case_start closekey-2-follows-the-option
stub "$PATHDIR/tdo"
tm set-option -g @todo-key-table root >/dev/null 2>&1
tm set-option -g @todo-key M-w >/dev/null 2>&1
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for M-w root)" "the root binding is installed on M-w"
assert_contains "$(plugin_keybinds_for M-w root)" "TDO_POPUP_KEY=M-w" "TDO_POPUP_KEY follows @todo-key"
assert_absent "$(plugin_keybinds_for M-w root)" "TDO_POPUP_KEY=C-l" "the key is not hardcoded"
case_end

# DoD 2, the prefix case: no TDO_POPUP_KEY at all. In a prefix table the opening
# chord is prefix+key, the prefix cannot reach a focused popup, and a bare-key
# closer would be a different key from the opener.
case_start closekey-3-prefix-carries-nothing
stub "$PATHDIR/tdo"
tm set-option -g @todo-key t >/dev/null 2>&1
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for t prefix)" "the prefix binding is installed"
assert_eq 4 "$(popup_branch_count t prefix)" "four display-popup branches"
assert_eq 0 "$(popup_env_count t prefix)" "and NONE of them carries TDO_POPUP_KEY"
case_end

# The default install is a prefix install, so the common case must also be clean.
case_start closekey-4-default-install-carries-nothing
stub "$PATHDIR/tdo"
out=$(plugin_run)
assert_eq 0 "$(popup_env_count t prefix)" "the default (prefix t) install passes no TDO_POPUP_KEY"
case_end

# An invalid @todo-key-table falls back to prefix, so it must also fall back to
# no close key — the fallback has to carry the whole prefix decision, not half.
case_start closekey-5-invalid-table-carries-nothing
stub "$PATHDIR/tdo"
tm set-option -g @todo-key-table nonsense >/dev/null 2>&1
tm set-option -g @todo-key C-l >/dev/null 2>&1
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for C-l prefix)" "falls back to a prefix binding"
assert_eq 0 "$(popup_env_count C-l prefix)" "and passes no TDO_POPUP_KEY"
case_end

# DoD 3, and the one that protects every existing install: a key name containing
# a quote. tmux's single-quoted strings have no escape character, so embedding it
# in the bind body would make the whole bind-key argument unparseable and the
# popup would stop opening. The key must still be bound, the popup must still
# open, and no TDO_POPUP_KEY may be present.
#
# The key names here are ones tmux actually accepts. The first attempt used
# "C-l'x", which tmux rejects with "unknown key" — but so does "C-lx", so the
# quote had nothing to do with it and the case was testing an invalid key name
# rather than a quoted one. `'`, `"`, `M-'` and `C-"` all bind for real on 3.7b.
#
# ...but not on every tmux: 3.4 rejects at least one of them as a key NAME, and
# that is what made this group red in CI. So each key is probed first and the
# case skips only when tmux itself refuses the name.
#
# The skip is deliberately the narrowest thing that closes the CI failure. "tmux
# will not bind this name" is the only excuse; "the plugin failed to bind it" is
# the bug this group exists for and still fails. Both directions are
# mutation-proven — see the task's Evidence section — because a skip is the one
# change that can make a suite pass by asking less.
quote_keys_ran=0
for spec in "${QUOTE_KEY_SPECS[@]}"; do
    label=${spec%%:*}
    badkey=${spec#*:}
    if ! tmux_can_bind "$badkey"; then
        printf '\n== closekey-6-quote-in-key-%s\n' "$label"
        printf '    SKIP this tmux cannot bind the key name "%s" at all, so the plugin\n' "$badkey"
        printf '         cannot be asked to bind it. Not a plugin failure.\n'
        continue
    fi
    quote_keys_ran=$((quote_keys_ran + 1))
    case_start "closekey-6-quote-in-key-$label"
    stub "$PATHDIR/tdo"
    tm set-option -g @todo-key-table root >/dev/null 2>&1
    tm set-option -g @todo-key "$badkey" >/dev/null 2>&1
    out=$(plugin_run)
    # The warning is the user-facing half; the script's own stderr is asserted
    # rather than the tmux message log, which older tmux cannot show.
    assert_contains "$out" "cannot be passed to the popup" "the script says why the close key is off"
    assert_eq 1 "$(count_plugin_keybinds_for "$badkey" root)" "the keybind is still installed"
    assert_eq 4 "$(popup_branch_count "$badkey" root)" "all four popup branches survive"
    assert_eq 0 "$(popup_env_count "$badkey" root)" "and carry no TDO_POPUP_KEY"
    # The server must still be healthy: a broken bind-key argument would have
    # made tmux reject the whole command, and a broken *config* would show up as
    # a server that cannot answer.
    assert_eq "ok" "$(tm display-message -p ok 2>/dev/null)" "the tmux server is still answering"
    case_end
done

# The guard on the guard. If the probe skipped every key, this group asserted
# nothing at all — and a suite that reports success for zero coverage is worse
# than the red build this fix replaces. There is no tmux where this should fire:
# `'` is an ordinary key name.
printf '\n== closekey-6-quote-key-coverage\n'
if [ "$quote_keys_ran" -eq 0 ]; then
    bad "quote-key coverage was empty: this tmux bound none of the quote key names, so the whole group was vacuous"
else
    ok "$quote_keys_ran of ${#QUOTE_KEY_SPECS[@]} quote key names were bindable and actually ran"
fi

# The other half of DoD 3: a tmux server *started* with such a config comes up.
# The cases above set the option on a running server; this one puts it in the
# config file the server reads at startup, which is how a real user would hit it,
# and the failure mode there is a server that never comes up at all.
#
# Probed like the loop above, and for the same reason: its key is `'`, so a tmux
# that cannot bind that name would fail this case for a reason that is not the
# plugin's. It is one key rather than a list, so a plain `if` rather than a
# counter — and no vacuity guard is needed, because a skip here is visible as a
# missing case in a group of one.
if ! tmux_can_bind "'"; then
    printf '\n== closekey-7-server-starts-with-a-quoted-key\n'
    printf '    SKIP this tmux cannot bind the key name "%s" at all. Not a plugin failure.\n' "'"
else
case_start closekey-7-server-starts-with-a-quoted-key
stub "$PATHDIR/tdo"
conf=$CASEDIR/tmux.conf
printf "set-option -g @todo-key-table root\nset-option -g @todo-key \"'\"\n" >"$conf"
sock2=$SOCK-startup
SOCKETS+=("$sock2")
if "$REAL_TMUX" -L "$sock2" -f "$conf" new-session -d -s startup >/dev/null 2>&1; then
    ok "a server whose config sets a quoted @todo-key starts"
    # ...and the plugin can then install into it. The shim points at $SOCK, so
    # this leg drives the script with a shim pointing at the second server.
    printf '#!/bin/sh\nexec %s -L %s "$@"\n' "$REAL_TMUX" "$sock2" >"$PATHDIR/tmux"
    chmod +x "$PATHDIR/tmux"
    out=$(plugin_run)
    assert_contains "$out" "cannot be passed to the popup" "and the install warns about the key"
    keys=$("$REAL_TMUX" -L "$sock2" list-keys -T root 2>/dev/null | awk -v k="'" "$KEY_AWK")
    assert_eq 1 "$(printf '%s' "$keys" | grep -c 'display-popup')" "and still binds the popup to it"
    assert_eq 0 "$(printf '%s' "$keys" | grep -o 'TDO_POPUP_KEY' | grep -c .)" "with no TDO_POPUP_KEY"
else
    bad "a server whose config sets a quoted @todo-key failed to start"
fi
"$REAL_TMUX" -L "$sock2" kill-server >/dev/null 2>&1
case_end
fi

# nested_client — manufactures an attached client on this case's server and
# echoes its name, or nothing.
#
# display-popup refuses to run with "no current client", and a detached `tmux -L`
# server has none. One can be built: a pane whose own command is `tmux attach`
# becomes a real 80x24 client for the session it attached to. $TMUX must be
# cleared or tmux refuses to nest.
#
# The attach is the pane's INITIAL COMMAND rather than something sent with
# send-keys, so there is no shell to wait for and no startup rc to race — the
# outer pane exists only to host a client, and it never needs to type.
nested_client() {
    tm new-session -d -s inner >/dev/null 2>&1
    tm new-session -d -s outer -x 80 -y 24 "TMUX= $REAL_TMUX -L $SOCK attach -t inner" >/dev/null 2>&1
    local i name
    for i in $(seq 40); do
        name=$(tm list-clients -F '#{client_name}' 2>/dev/null | head -1)
        [ -n "$name" ] && { printf '%s' "$name"; return 0; }
        sleep 0.25
    done
    return 1
}

# The env var must actually reach the program. `display-popup -e` is the whole
# mechanism, so it is worth one direct check independent of the plugin: run a
# popup whose command records its own environment.
case_start closekey-8-display-popup-e-reaches-the-program
stub "$PATHDIR/tdo"
probe=$CASEDIR/env-probe
outfile=$CASEDIR/env-answer
printf '#!/bin/sh\nprintf "%%s" "${TDO_POPUP_KEY-<unset>}" > "%s"\n' "$outfile" >"$probe"
chmod +x "$probe"
client=$(nested_client)
if [ -z "$client" ]; then
    bad "could not manufacture an attached client, so -e was not proven"
else
    tm display-popup -c "$client" -E -e "TDO_POPUP_KEY=C-l" -w 40 -h 5 "$probe" >/dev/null 2>&1
    for _ in 1 2 3 4 5 6 7 8; do [ -s "$outfile" ] && break; sleep 0.25; done
    assert_eq "C-l" "$(cat "$outfile" 2>/dev/null)" "display-popup -e reaches the program inside the popup"
fi
case_end

# DoD 1, the whole chain: the real plugin installs the real bind, the real binary
# runs in the popup, and the SAME chord opens it, closes it, and opens it again.
# Nothing below this line is a stub — it is the only case that proves the plugin,
# the translation and Update agree with each other.
if [ -z "$TDO_BIN" ]; then
    printf '\n== closekey-9 SKIPPED: no usable go toolchain to build tdo with.\n'
    printf '   This is the only case that presses the key for real.\n'
else
case_start closekey-9-the-hotkey-toggles-the-popup \
    XDG_DATA_HOME=$TMPROOT/closekey-9-the-hotkey-toggles-the-popup/xdg \
    XDG_STATE_HOME=$TMPROOT/closekey-9-the-hotkey-toggles-the-popup/state
DB=$CASEDIR/xdg/tmux-todo/tasks.db
cp "$TDO_BIN" "$PATHDIR/tdo"
tm set-option -g @todo-key-table root >/dev/null 2>&1
tm set-option -g @todo-key C-l >/dev/null 2>&1
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for C-l root)" "the root C-l binding is installed"

# A task with a distinctive text, so "the popup is on screen" is a grep for
# something only the popup can be showing. Seeded outside tmux so the harness's
# own session never enters the sandbox database.
mkdir -p "$(dirname "$DB")"
env -u TMUX "$TDO_BIN" add --db "$DB" --global 'ZZCANARYZZ' >/dev/null 2>&1

client=$(nested_client)
if [ -z "$client" ]; then
    bad "could not manufacture an attached client, so the toggle is unproven"
else
    # popup_state polls the capture for the canary, so the assertions wait for the
    # popup rather than sleeping a guessed interval. It answers as soon as the
    # state it is waiting for arrives, and reports whatever it last saw.
    popup_state() { # want  -> echoes yes|no
        local i seen
        for i in $(seq 40); do
            if tm capture-pane -p -t outer 2>/dev/null | grep -q 'ZZCANARYZZ'; then
                seen=yes
            else
                seen=no
            fi
            [ "$seen" = "$1" ] && break
            sleep 0.25
        done
        printf '%s' "$seen"
    }

    assert_eq "no" "$(popup_state no)" "the popup is not on screen to begin with"

    tm send-keys -t outer C-l >/dev/null 2>&1
    assert_eq "yes" "$(popup_state yes)" "C-l opens the popup"
    printf '    -- capture with the popup open:\n'
    tm capture-pane -p -t outer 2>/dev/null | grep -n 'ZZCANARYZZ\|tdo' | head -3 | sed 's/^/       /'

    # THE assertion: the same chord, now delivered to the program inside the
    # popup rather than to the outer client, closes it.
    tm send-keys -t outer C-l >/dev/null 2>&1
    closed=$(popup_state no)
    assert_eq "no" "$closed" "C-l again closes it"

    # The reopen leg only means anything if the close happened. Guarded, because
    # against a build with the wiring removed it otherwise reported a cheerful
    # "ok" for a popup that had simply never closed — a reassuring pass sitting
    # next to the real failure.
    if [ "$closed" = "no" ]; then
        tm send-keys -t outer C-l >/dev/null 2>&1
        assert_eq "yes" "$(popup_state yes)" "and C-l reopens it, so the key toggles"
    else
        printf '    -- skipped the reopen check: it never closed, so it cannot reopen\n'
    fi

    # q must still close it, from whatever state we are in.
    tm send-keys -t outer q >/dev/null 2>&1
    assert_eq "no" "$(popup_state no)" "q still closes the popup too"
fi
case_end
fi

# The other side of DoD 8, end to end: a PREFIX install passes no TDO_POPUP_KEY,
# so the bare key must not close the popup. Without this, "the toggle works" and
# "every key closes the popup" look identical from closekey-9 alone.
if [ -z "$TDO_BIN" ]; then
    printf '\n== closekey-10 SKIPPED: no usable go toolchain.\n'
else
case_start closekey-10-prefix-install-does-not-toggle \
    XDG_DATA_HOME=$TMPROOT/closekey-10-prefix-install-does-not-toggle/xdg \
    XDG_STATE_HOME=$TMPROOT/closekey-10-prefix-install-does-not-toggle/state
DB=$CASEDIR/xdg/tmux-todo/tasks.db
cp "$TDO_BIN" "$PATHDIR/tdo"
tm set-option -g @todo-key t >/dev/null 2>&1
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for t prefix)" "the prefix t binding is installed"
mkdir -p "$(dirname "$DB")"
env -u TMUX "$TDO_BIN" add --db "$DB" --global 'ZZCANARYZZ' >/dev/null 2>&1

client=$(nested_client)
if [ -z "$client" ]; then
    bad "could not manufacture an attached client"
else
    popup_state() {
        local i seen
        for i in $(seq 40); do
            if tm capture-pane -p -t outer 2>/dev/null | grep -q 'ZZCANARYZZ'; then seen=yes; else seen=no; fi
            [ "$seen" = "$1" ] && break
            sleep 0.25
        done
        printf '%s' "$seen"
    }
    # prefix + t opens it.
    tm send-keys -t outer C-b >/dev/null 2>&1
    tm send-keys -t outer t >/dev/null 2>&1
    assert_eq "yes" "$(popup_state yes)" "prefix t opens the popup"
    # A bare `t` inside it must NOT close it: with no TDO_POPUP_KEY there is no
    # close key, and `t` is not one of q/esc/ctrl+c.
    tm send-keys -t outer t >/dev/null 2>&1
    sleep 1
    assert_eq "yes" "$(popup_state yes)" "a bare t does not close it (no TDO_POPUP_KEY was passed)"
    tm send-keys -t outer q >/dev/null 2>&1
    assert_eq "no" "$(popup_state no)" "q closes it"
fi
case_end
fi

# ================================================================ y copies

# DoD 11 for the clipboard task: the whole chain, nothing stubbed. The real
# plugin installs the real bind, the real binary runs inside a real popup on a
# real client, `y` is pressed for real, and `tmux show-buffer` is asked what
# landed in the buffer.
#
# Why this case cannot be replaced by the Go tests: internal/tui is tested
# against an injected Copy and internal/cli against an injected runner, so
# between them nothing proves the two halves are connected to each other or that
# the tmux the popup inherits is the tmux that answers show-buffer. The popup
# runs in a `display-popup` child of THIS server, so its `tmux load-buffer` lands
# in this server's buffer stack — which is the fact under test.
#
# The text is deliberately awkward: a quote, a double quote and a `$`. It is the
# payload that would come back mangled — or would have executed — if anything on
# the path put it through a shell or into an argv. `show-buffer` is compared
# byte for byte against it.
if [ -z "$TDO_BIN" ]; then
    printf '\n== copy-1 SKIPPED: no usable go toolchain to build tdo with.\n'
    printf '   This is the only case that presses y for real.\n'
else
case_start copy-1-y-loads-the-tmux-buffer \
    XDG_DATA_HOME=$TMPROOT/copy-1-y-loads-the-tmux-buffer/xdg \
    XDG_STATE_HOME=$TMPROOT/copy-1-y-loads-the-tmux-buffer/state
DB=$CASEDIR/xdg/tmux-todo/tasks.db
cp "$TDO_BIN" "$PATHDIR/tdo"
tm set-option -g @todo-key-table root >/dev/null 2>&1
tm set-option -g @todo-key C-l >/dev/null 2>&1
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for C-l root)" "the root C-l binding is installed"

# One task, and its text is both the canary the popup is grepped for and the
# payload show-buffer is compared against. Seeded outside tmux so the harness's
# own session never enters the sandbox database.
COPY_TEXT="ZZCOPYZZ it's a \"\$HOME\" task"
mkdir -p "$(dirname "$DB")"
env -u TMUX "$TDO_BIN" add --db "$DB" --global "$COPY_TEXT" >/dev/null 2>&1
assert_eq "1" "$(env -u TMUX "$TDO_BIN" count --db "$DB" --scope global 2>/dev/null)" \
    "the awkward text survived being seeded"

# The buffer stack must start empty, or a pre-existing buffer could be mistaken
# for the copy. An empty stack makes show-buffer fail, which is the state the
# assertion below needs to be able to distinguish from success.
tm delete-buffer >/dev/null 2>&1
assert_eq "" "$(tm show-buffer 2>/dev/null)" "the buffer stack starts empty"

client=$(nested_client)
if [ -z "$client" ]; then
    bad "could not manufacture an attached client, so y is unproven"
else
    popup_state() { # want -> echoes yes|no
        local i seen
        for i in $(seq 40); do
            if tm capture-pane -p -t outer 2>/dev/null | grep -q 'ZZCOPYZZ'; then seen=yes; else seen=no; fi
            [ "$seen" = "$1" ] && break
            sleep 0.25
        done
        printf '%s' "$seen"
    }

    tm send-keys -t outer C-l >/dev/null 2>&1
    opened=$(popup_state yes)
    assert_eq "yes" "$opened" "C-l opens the popup with the task on screen"

    if [ "$opened" != "yes" ]; then
        # Guarded for the same reason closekey-9's reopen leg is: against a build
        # where y does nothing, an unopened popup would otherwise report a
        # cheerful pass for an assertion that never ran.
        printf '    -- skipped the y check: the popup never opened\n'
    else
        tm send-keys -t outer y >/dev/null 2>&1

        # Poll for the buffer rather than sleeping a guessed interval: the copy is
        # a subprocess spawned from inside the popup's event loop.
        got=''
        for _ in $(seq 40); do
            got=$(tm show-buffer 2>/dev/null)
            [ -n "$got" ] && break
            sleep 0.25
        done
        assert_eq "$COPY_TEXT" "$got" "y put the task text in the tmux buffer, byte for byte"

        # The confirmation is on screen too, and on the title's row. Grepped for
        # one word: capture-pane -pe interleaves escapes between words, so a
        # multi-word grep finds nothing even when the phrase is visible.
        #
        # POLLED, not captured once. The loop above breaks the moment the tmux
        # buffer fills, and the buffer is filled by the copy subprocess — which
        # returns *before* Bubble Tea has handled the resulting message and
        # repainted. Capturing at that instant is a race, and it lost: the first
        # version of this case failed here with the buffer already correct.
        cap=''
        for _ in $(seq 40); do
            cap=$(tm capture-pane -p -t outer 2>/dev/null)
            case "$cap" in *"copied:"*) break ;; esac
            sleep 0.25
        done
        assert_contains "$cap" "copied:" "the popup confirms the copy on the title line"
        # The notice REPLACES the title, so the title must be gone. Without this
        # the assertion above would also pass for a notice rendered on a new row
        # — which is the frame-overflow shape this repo has shipped three times.
        assert_absent "$cap" "│  tdo " "the notice replaced the title rather than taking a row"
        printf '    -- capture with the notice up:\n'
        printf '%s\n' "$cap" | grep -n 'copied:' | head -2 | sed 's/^/       /'

        tm send-keys -t outer q >/dev/null 2>&1
        assert_eq "no" "$(popup_state no)" "q closes the popup afterwards"
    fi
fi
case_end
fi

# ============================================================== rename hook

# The section that exists because everything above it can pass while the shipped
# hook does nothing.
#
# `tdo session-renamed` was proven once by typing it into a pane after a rename.
# That proves nothing about the hook: a pane has a CLIENT, and tmux answers
# "which session is current" from the client. A `run-shell` hook child has no
# client, so tmux falls back to an unrelated session — the command then looked the
# wrong id up in the map, missed, and exited 0 with the tasks stranded under the
# old name. See
# docs/tasks/2026-08-20-session-renamed-hook-targets-wrong-session.md.
#
# So these cases fire a REAL rename on a REAL server through the hook the plugin
# script itself installed, and assert on the database afterwards. Three traps
# shape how:
#
#   * The hook child inherits the tmux SERVER's environment — not a pane's, not
#     this harness's. XDG_DATA_HOME therefore has to be set when the server is
#     started (case_start takes it as an argument for exactly this), or the child
#     opens the developer's real database. That is a safety requirement.
#   * tmux expands #{...} inside a run-shell argument before sh sees it, so a
#     probe containing a format silently reports the server's view and reads as
#     though the child agreed. Nothing here interpolates a format into a hook.
#   * A harness-side `tdo` read would record ITS OWN session into the map, since
#     every command that resolves a scope refreshes id -> name. If the harness's
#     real session id collided with a private server's, that write would clobber
#     the row under test. Every read here goes through tdo_read, which unsets
#     $TMUX so the read resolves no session at all.

# tdo_read runs the real binary with no tmux context: no session resolved, so no
# map row written, so the assertion cannot perturb what it is measuring.
tdo_read() { env -u TMUX "$TDO_BIN" "$@"; }

# scope_of <db> <task text> — "kind|key" for the task with that text, read
# through `tdo list --json`, which is a published contract and so a fair thing to
# parse. Splitting on "}" leaves the text and its nested scope object in one
# chunk; task texts here contain no quote or regex metacharacter.
scope_of() {
    tdo_read list --db "$1" --scope=all --all --json 2>/dev/null |
        tr '}' '\n' |
        sed -n "s/.*\"text\":\"$2\",.*\"kind\":\"\([a-z]*\)\",\"key\":\"\([^\"]*\)\".*/\1|\2/p" |
        tr '\n' ' ' | sed 's/ *$//'
}

# wait_scope <db> <text> <want> — poll until scope_of answers <want>, then echo
# whatever it last said. `run-shell -b` is asynchronous, so the rename returns
# before the hook child has finished; polling for the expected value keeps the
# happy path fast and still reports the real answer on failure.
#
# It must only ever be used for the POSITIVE assertion. A bystander that must not
# move is read once, after the positive one has settled — polling for "unchanged"
# would pass instantly every time, including on a fix that moves it a moment
# later.
wait_scope() {
    local i got=''
    for i in $(seq 40); do
        got=$(scope_of "$1" "$2")
        [ "$got" = "$3" ] && break
        sleep 0.25
    done
    printf '%s' "$got"
}

# seed_session_task <session> <text> — CREATE the session with the add as its
# pane's initial command, so the task is filed through the production path with
# no shell and no keystrokes involved. `exec sleep` keeps the session alive
# afterwards; a pane whose command exits takes the session with it, and there
# would be nothing left to rename.
#
# An earlier version drove an interactive shell with send-keys, on the belief that
# tmux sets $TMUX_PANE only for a shell and not for a pane's initial command.
# **That was wrong** — measured: a pane whose command is `sh -c 'printenv
# TMUX_PANE'` prints `%1`. The probe that said otherwise ran `printenv TMUX
# TMUX_PANE`, which reported only the first variable, so TMUX_PANE looked absent.
#
# The send-keys version also flaked, which is what sent someone back to check: it
# needed a readiness handshake (keystrokes sent before the shell reads them are
# buffered, and polling-and-resending seeded four copies of one task), and then
# the handshake itself timed out under load because the shell's rc files are
# slow. The pane-command form has no shell to wait for and cannot race.
seed_session_task() {
    local i
    tm new-session -d -s "$1" "$TDO_BIN add --session '$2'; exec sleep 300" >/dev/null 2>&1
    for i in $(seq 60); do
        [ -n "$(scope_of "$DB" "$2")" ] && return 0
        sleep 0.25
    done
    return 1
}

# clientless_session — the session name an UNTARGETED display-message resolves to
# from a client-less child, which is the wrong answer the whole bug is made of.
#
# It is here as a PRECONDITION, not as a curiosity. The client-less fallback lands
# on whichever session tmux considers most recent, and sometimes that IS the
# session being renamed — on a two-session server it usually is. A rename case set
# up that way passes with the bug fully present: proven, by running these cases
# against the pre-fix binary, where rename-4 went green until it grew a decoy.
# So every case asserts that the fallback names something else, and is therefore
# known to be able to fail.
#
# The probe lives in a FILE. tmux expands #{...} in a run-shell argument before sh
# sees it, so a format written inline would report the server's own view and read
# as though the child had agreed with it.
clientless_session() {
    local probe=$CASEDIR/fallback-probe out=$CASEDIR/fallback-answer i
    rm -f "$out"
    printf '#!/bin/sh\ntmux display-message -p "#{session_name}" > "$1" 2>&1\n' >"$probe"
    chmod +x "$probe"
    tm run-shell -b "$probe $out" >/dev/null 2>&1
    for i in $(seq 40); do
        [ -s "$out" ] && break
        sleep 0.25
    done
    tr -d '\n' <"$out" 2>/dev/null
}

# assert_fallback_is_not <session> <label> — the anti-vacuity guard above.
assert_fallback_is_not() {
    local got
    got=$(clientless_session)
    printf '    -- a client-less child resolves the current session as: %s\n' "${got:-<nothing>}"
    if [ -z "$got" ]; then
        bad "$2 (the fallback probe answered nothing, so the case is unproven)"
    elif [ "$got" = "$1" ]; then
        bad "$2 (the fallback also names '$1', so this case would pass with the bug present)"
    else
        ok "$2"
    fi
}

# A hook child that writes anything makes tmux open a "[tmux]" window to show it.
# That is the concrete cost the silent no-op paths exist to avoid, and it is
# assertable: no such window means nothing was printed.
assert_no_hook_output() { # label
    local w
    w=$(tm list-windows -a -F '#{window_name}' 2>/dev/null | grep -c '^\[tmux\]$')
    if [ "$w" = 0 ]; then ok "$1"; else bad "$1 ($w [tmux] output window(s))"; fi
}

if [ -z "$TDO_BIN" ]; then
    printf '\n== rename-hook cases SKIPPED: no usable go toolchain to build tdo with.\n'
    printf '   These are the only cases that fire a real hook, so a green run\n'
    printf '   without them says nothing about the rename path.\n'
else

# The case the bug lived in. One rename, through the plugin's own installed hook,
# with a bystander session present the whole time.
case_start rename-1-hook-moves-tasks \
    XDG_DATA_HOME=$TMPROOT/rename-1-hook-moves-tasks/xdg \
    XDG_STATE_HOME=$TMPROOT/rename-1-hook-moves-tasks/state
DB=$CASEDIR/xdg/tmux-todo/tasks.db
cp "$TDO_BIN" "$PATHDIR/tdo"
out=$(plugin_run)
assert_eq 1 "$(count_tdo_hooks)" "the plugin installed its hook"
# The safety check, not a convenience: without this the hook child opens the
# developer's real database.
assert_contains "$(tm show-environment -g XDG_DATA_HOME)" "$CASEDIR/xdg" \
    "the SERVER's environment carries the sandbox XDG_DATA_HOME"

# Seeded in this order so bravo is the most recently created session, which is
# what makes it — and not alpha — the one a client-less child resolves to. The
# precondition below asserts that rather than trusting it.
if seed_session_task alpha 'alpha task' && seed_session_task bravo 'bravo task'; then
    ok "seeded a session task in alpha and in bravo"
else
    bad "could not seed the session tasks (db: $DB)"
fi
printf '    -- before: alpha task %s | bravo task %s\n' \
    "$(scope_of "$DB" 'alpha task')" "$(scope_of "$DB" 'bravo task')"
assert_eq "session|alpha" "$(scope_of "$DB" 'alpha task')" "alpha's task is filed under alpha"
assert_eq "session|bravo" "$(scope_of "$DB" 'bravo task')" "bravo's task is filed under bravo"

assert_fallback_is_not alpha \
    "a client-less child would resolve some OTHER session, so this case can fail"

# THE rename. Nothing is interpolated into the hook; the child reads $TMUX.
tm rename-session -t alpha alpha2 >/dev/null 2>&1
got=$(wait_scope "$DB" 'alpha task' 'session|alpha2')
printf '    -- after : alpha task %s | bravo task %s\n' \
    "$got" "$(scope_of "$DB" 'bravo task')"
assert_eq "session|alpha2" "$got" "the hook moved the renamed session's task"
# The destructive-fix guard. Resolving the wrong session and *finding* it is
# worse than today's no-op, because it rewrites somebody else's list.
assert_eq "session|bravo" "$(scope_of "$DB" 'bravo task')" \
    "the bystander session's task was left alone"
assert_no_hook_output "the hook printed nothing"

# ...and again, which is the only way to prove the map was refreshed: the second
# rename can only find the old name if the first one wrote it back.
tm rename-session -t alpha2 alpha3 >/dev/null 2>&1
got=$(wait_scope "$DB" 'alpha task' 'session|alpha3')
printf '    -- after second rename: alpha task %s\n' "$got"
assert_eq "session|alpha3" "$got" "a second rename moves it again (the map was refreshed)"
assert_eq "session|bravo" "$(scope_of "$DB" 'bravo task')" "the bystander is still untouched"
case_end

# A session tdo has never run in owns no tasks under any name. The hook fires,
# finds nothing in the map, and must exit 0 without a word — it runs on every
# rename in the user's tmux, so a chatty no-op would flash a window each time.
case_start rename-2-unknown-session-is-a-silent-no-op \
    XDG_DATA_HOME=$TMPROOT/rename-2-unknown-session-is-a-silent-no-op/xdg \
    XDG_STATE_HOME=$TMPROOT/rename-2-unknown-session-is-a-silent-no-op/state
DB=$CASEDIR/xdg/tmux-todo/tasks.db
cp "$TDO_BIN" "$PATHDIR/tdo"
plugin_run >/dev/null
tm new-session -d -s lonely >/dev/null 2>&1
tm rename-session -t lonely lonely2 >/dev/null 2>&1
sleep 1
assert_no_hook_output "an unknown session renames silently"
# A database file IS expected: session-renamed opens the store before it works
# out which session it is, so the hook creates an empty one on a fresh machine.
# What must be true is that it holds nothing.
assert_file "$DB" "the hook opened a database (it opens the store before resolving)"
assert_eq 0 "$(tdo_read count --db "$DB" --scope=all)" "and filed nothing in it"
case_end

# The same, with a map that exists but does not know this session — the "empty
# map" and "unknown id" halves are different code paths, and only one of them
# gets to skip opening the store.
case_start rename-3-cold-map-is-a-silent-no-op \
    XDG_DATA_HOME=$TMPROOT/rename-3-cold-map-is-a-silent-no-op/xdg \
    XDG_STATE_HOME=$TMPROOT/rename-3-cold-map-is-a-silent-no-op/state
DB=$CASEDIR/xdg/tmux-todo/tasks.db
cp "$TDO_BIN" "$PATHDIR/tdo"
plugin_run >/dev/null
mkdir -p "$(dirname "$DB")"
tdo_read add --db "$DB" --global 'a global task' >/dev/null
tm new-session -d -s lonely >/dev/null 2>&1
tm rename-session -t lonely lonely2 >/dev/null 2>&1
sleep 1
assert_no_hook_output "a cold map renames silently"
assert_eq "global|" "$(scope_of "$DB" 'a global task')" "the global task is still global, not re-filed"
assert_eq 1 "$(tdo_read count --db "$DB" --scope=all)" "and the database still holds exactly its one task"
case_end

# The "already current" path, reached the only way a real hook can reach it: a
# hook body that runs the command twice in one child, so the second call is
# ordered strictly after the first rather than racing it. The first moves the
# task; the second must find the map already correct and say nothing.
#
# The move assertion is what keeps this non-vacuous — if the first call no-ops,
# the second one no-ops too and a silence-only assertion would pass.
case_start rename-4-second-firing-is-a-silent-no-op \
    XDG_DATA_HOME=$TMPROOT/rename-4-second-firing-is-a-silent-no-op/xdg \
    XDG_STATE_HOME=$TMPROOT/rename-4-second-firing-is-a-silent-no-op/state
DB=$CASEDIR/xdg/tmux-todo/tasks.db
cp "$TDO_BIN" "$PATHDIR/tdo"
plugin_run >/dev/null
tm set-hook -gu session-renamed >/dev/null 2>&1
tm set-hook -ga session-renamed "run-shell -b '$PATHDIR/tdo session-renamed; $PATHDIR/tdo session-renamed'" >/dev/null 2>&1
if seed_session_task delta 'delta task' && seed_session_task decoy 'decoy task'; then
    ok "seeded a session task in delta and in the decoy"
else
    bad "could not seed the session tasks"
fi
# The decoy is seeded LAST on purpose: it makes it, and not delta, the session a
# client-less child resolves to. Without it this case passed against the pre-fix
# binary.
assert_fallback_is_not delta \
    "a client-less child would resolve the decoy, so this case can fail"
tm rename-session -t delta delta2 >/dev/null 2>&1
got=$(wait_scope "$DB" 'delta task' 'session|delta2')
printf '    -- after : delta task %s\n' "$got"
assert_eq "session|delta2" "$got" "the first call in the hook moved the task"
assert_eq "session|decoy" "$(scope_of "$DB" 'decoy task')" "the decoy's task was left alone"
assert_no_hook_output "the second call found the map already current and said nothing"
case_end

fi

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

# The other half of the steady-state promise, and the one the download step put
# at risk: tmux sources this script on EVERY server start, so a network call on
# the resolved path would cost a round-trip — or a hang, offline — every time.
# The downloader is present but booby-trapped: invoking it leaves a canary.
case_start steady-state-no-network
fixture_release "$CASEDIR/release" "${HOST_ASSET:-tdo-linux-amd64}"
stub "$PLUGINDIR/bin/tdo"
stub_loud_downloader "$PATHDIR/curl" "$CASEDIR/curl-ran"
stub_loud_downloader "$PATHDIR/wget" "$CASEDIR/wget-ran"
out=$(plugin_run)
assert_eq 1 "$(count_plugin_keybinds_for t)" "keybind installed"
assert_no_file "$CASEDIR/curl-ran" "curl was never invoked"
assert_no_file "$CASEDIR/wget-ran" "wget was never invoked"
assert_eq "" "$out" "and the steady-state run is silent"
case_end

# ================================================================== summary

printf '\n%s\n' "----------------------------------------"
printf 'plugin harness: %s passed, %s failed\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ] || exit 1
