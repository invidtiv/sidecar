#!/bin/bash
# test-loopback-remote.sh - Thin CI for the portable loopback remote.
# No TUI: up --no-drive only. Never touches the default tmux server.

set -euo pipefail
unset TMUX

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
HARNESS="$SCRIPT_DIR/loopback-remote.sh"
TEST_ROOT="$(mktemp -d /tmp/sidecar-loopback-test.XXXXXX)"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
RUN_DIR="$TEST_ROOT/run"
SNAPSHOT_DIR="$TEST_ROOT/isolation"
REAL_STATE="${HOME}/.local/state/sidecar"
REAL_CONFIG="${HOME}/.config/sidecar"

cleanup() {
    LOOPBACK_RUN_DIR="$RUN_DIR" "$HARNESS" down >/dev/null 2>&1 || true
    rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

fail() { echo "FAIL: $*" >&2; exit 1; }

assert_refused() {
    local dir="$1" why="$2" out status=0
    set +e
    out=$(LOOPBACK_RUN_DIR="$dir" "$HARNESS" paths 2>&1)
    status=$?
    set -e
    [ "$status" -ne 0 ] || fail "expected refuse for $why ($dir)"
    printf '%s\n' "$out" | grep -q "refusing LOOPBACK_RUN_DIR" || fail "refuse for $why did not name LOOPBACK_RUN_DIR"
}

real_tree_paths() {
    local root="$1"
    if [ ! -d "$root" ]; then
        echo "ABSENT"
        return
    fi
    find "$root" \( -path '*/requests/*' -o -path '*/viewers/*' -o -name shells.json \) -print 2>/dev/null | LC_ALL=C sort
}

default_tmux_ls() {
    tmux ls 2>/dev/null || true
}

# Refuse-run-dir: trailing slash, empty component, dotdot, wrong prefix.
assert_refused "/tmp/sidecar-loopback-x/" "trailing slash"
assert_refused "/tmp/sidecar-loopback-x//foo" "empty path component"
assert_refused "/tmp/sidecar-loopback-x/../escape" "dotdot"
assert_refused "/tmp/sidecar-not-loopback-$$" "not under /tmp/sidecar-loopback*"
assert_refused "/var/folders/sidecar-loopback-x" "not under /tmp/sidecar-loopback*"

# Fingerprint the developer's default server and real state tree BEFORE up.
mkdir -p "$SNAPSHOT_DIR"
default_tmux_ls > "$SNAPSHOT_DIR/tmux.ls"
real_tree_paths "$REAL_STATE" > "$SNAPSHOT_DIR/state.paths"

echo "recorded default tmux sessions: $(grep -c . "$SNAPSHOT_DIR/tmux.ls" || true)"
echo "recorded real state-tree paths: $(grep -c . "$SNAPSHOT_DIR/state.paths" || true)"

# An agent runs up from a live Sidecar pane. These must not land on the
# private host server (the Go loopback harness already drops them).
export SIDECAR_SHELL=sidecar-sh-LIVE-DO-NOT-TOUCH
export SIDECAR_SHELL_NAME="live caller"
export SIDECAR_MANAGED_SHELL=1
export SIDECAR_NAMESPACE=/tmp/tmux-$(id -u)/default
export SIDECAR_TMUX_SERVER=99999
export SIDECAR_HOST=aerie

LOOPBACK_RUN_DIR="$RUN_DIR" "$HARNESS" up --delay 40ms --no-drive >/dev/null

canon=$(cd "$RUN_DIR" && pwd -P)
paths=$(LOOPBACK_RUN_DIR="$RUN_DIR" "$HARNESS" paths)

printf '%s\n' "$paths" | grep -F "$REAL_STATE" && fail "paths listed $REAL_STATE as a run root"
printf '%s\n' "$paths" | grep -F "$REAL_CONFIG" && fail "paths listed $REAL_CONFIG as a run root"

printed_paths=$(printf '%s\n' "$paths" | grep -oE '/(private/)?tmp/sidecar-loopback[^[:space:]]*' || true)
printf '%s\n' "$printed_paths" | while IFS= read -r p; do
    [ -n "$p" ] || continue
    case "$p" in
        "$canon"|"$canon"/*) ;;
        *) fail "printed path $p is outside $canon" ;;
    esac
done

printf '%s\n' "$paths" | grep -q "run root:" || fail "paths did not print run root"
printf '%s\n' "$paths" | grep -F "$canon" >/dev/null || fail "paths did not name the loopback root"

test -x "$canon/sidecar" || fail "worktree binary missing"
test -x "$canon/viewer/bin/ssh" || fail "fake ssh missing"
grep -q "Welcome to loopback -- stdout banner" "$canon/viewer/bin/ssh" || fail "fake ssh missing stdout banner"
grep -q "Last login: Tue -- stderr banner" "$canon/viewer/bin/ssh" || fail "fake ssh missing stderr banner"

grep -qx REMOTE-MARKER "$canon/host/project/twin.txt" || fail "host REMOTE-MARKER missing"
grep -qx LOCAL-TWIN "$canon/viewer/project/twin.txt" || fail "viewer LOCAL-TWIN missing"
test "$(cat "$canon/delay")" = "40ms" || fail "delay 40ms was not recorded"

if grep -F "$canon/viewer/bin" "$canon/viewer/config/config.json" >/dev/null; then
    fail "host env PATH includes the fake ssh directory"
fi
grep -q '"sidecar_remote_hosts": true' "$canon/viewer/config/config.json" || fail "viewer missing sidecar_remote_hosts"
grep -q '"cross_project_overview": true' "$canon/viewer/config/config.json" || fail "viewer missing cross_project_overview"
grep -q '"id": "loopback"' "$canon/viewer/config/config.json" || fail "viewer missing host id loopback"

host_socket="$canon/host/tmux/tmux-$(id -u)/default"
[ -S "$host_socket" ] || fail "host tmux socket missing"
n=$(TMUX= tmux -S "$host_socket" list-sessions 2>/dev/null | wc -l | tr -d '[:space:]')
[ "$n" -ge 2 ] || fail "host tmux should have at least two sessions, got $n"

host_env=$(TMUX= tmux -S "$host_socket" show-environment -g 2>/dev/null || true)
for leaked in SIDECAR_SHELL=sidecar-sh-LIVE-DO-NOT-TOUCH SIDECAR_MANAGED_SHELL=1 \
    SIDECAR_NAMESPACE=/tmp/tmux-$(id -u)/default SIDECAR_TMUX_SERVER=99999 \
    "SIDECAR_SHELL_NAME=live caller" SIDECAR_HOST=aerie; do
    printf '%s\n' "$host_env" | grep -Fqx "$leaked" && fail "host tmux inherited $leaked"
done

wt_list=$(git -C "$canon/host/project" worktree list --porcelain)
printf '%s\n' "$wt_list" | grep -q "$canon/host/worktrees/feature" || fail "host linked worktree missing"
if command -v td >/dev/null 2>&1; then
    test -d "$canon/host/project/.todos" || fail "host td was not initialized"
    test -d "$canon/viewer/project/.todos" || fail "viewer td was not initialized"
fi

# Viewer TUI must not have been started.
if [ -S "$canon/viewer/tmux/tmux-$(id -u)/sidecar-drive" ]; then
    fail "up --no-drive started a viewer drive session"
fi

LOOPBACK_RUN_DIR="$RUN_DIR" "$HARNESS" down >/dev/null
[ ! -e "$canon" ] || fail "down did not delete the run root"

# down when already down must not touch the default server.
LOOPBACK_RUN_DIR="$RUN_DIR" "$HARNESS" down >/dev/null

now_ls=$(default_tmux_ls)
while IFS= read -r line; do
    [ -z "$line" ] && continue
    if ! printf '%s\n' "$now_ls" | grep -Fqx "$line"; then
        fail "default tmux session missing since snapshot: $line"
    fi
done < "$SNAPSHOT_DIR/tmux.ls"

now_paths=$(real_tree_paths "$REAL_STATE")
while IFS= read -r line; do
    [ -z "$line" ] && continue
    [ "$line" = "ABSENT" ] && continue
    if [ ! -e "$line" ]; then
        fail "real state-tree path was destroyed: $line"
    fi
done < "$SNAPSHOT_DIR/state.paths"

echo "ok"
