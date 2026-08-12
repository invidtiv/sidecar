#!/bin/bash
set -euo pipefail
unset TMUX

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DRIVER="$SCRIPT_DIR/tmux-drive.sh"
TEST_ROOT="$(mktemp -d /tmp/sidecar-tmux-drive-test.XXXXXX)"
TEST_ROOT="$(cd "$TEST_ROOT" && pwd -P)"
RUN_DIR="$TEST_ROOT/drive"
unset SIDECAR_DRIVE_OUT
REPO="$TEST_ROOT/repo"
PWD_FILE="$TEST_ROOT/pwd"
ARGS_FILE="$TEST_ROOT/args"
INNER_INPUT_FILE="$TEST_ROOT/inner-input"
LAUNCH_TMUX_FILE="$TEST_ROOT/launch-tmux"
REAL_CONTROL_PID_FILE="$TEST_ROOT/real-control.pid"
DECOY_CONTROL_PID_FILE="$TEST_ROOT/decoy-control.pid"
DECOY_TMUX_TMPDIR="$TEST_ROOT/decoy-tmux"
DECOY_SOCKET="$DECOY_TMUX_TMPDIR/tmux-$(id -u)/default"
TEST_INNER_SOCKET="$RUN_DIR/tmux/tmux-$(id -u)/default"
host_pid=""
mkdir -p "$REPO"
REPO="$(cd "$REPO" && pwd -P)"

cleanup() {
    SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
        "$DRIVER" stop >/dev/null 2>&1 || true
    tmux -S "$DECOY_SOCKET" kill-server >/dev/null 2>&1 || true
    real_pid=""
    decoy_pid=""
    [ ! -s "$REAL_CONTROL_PID_FILE" ] || real_pid=$(cat "$REAL_CONTROL_PID_FILE")
    [ ! -s "$DECOY_CONTROL_PID_FILE" ] || decoy_pid=$(cat "$DECOY_CONTROL_PID_FILE")
    for pid in "${host_pid:-}" "$real_pid" "$decoy_pid"; do
        [ -n "$pid" ] || continue
        for _ in $(seq 1 100); do
            kill -0 "$pid" 2>/dev/null || break
            sleep 0.1
        done
        if kill -0 "$pid" 2>/dev/null; then
            kill -TERM "$pid" 2>/dev/null || true
            for _ in $(seq 1 100); do
                kill -0 "$pid" 2>/dev/null || break
                sleep 0.1
            done
        fi
    done
    rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

wait_for_file() {
    local file="$1"
    for _ in $(seq 1 150); do
        [ -s "$file" ] && return 0
        sleep 0.1
    done
    echo "timed out waiting for $file" >&2
    return 1
}

wait_for_pid_exit() {
    local pid="$1"
    for _ in $(seq 1 150); do
        kill -0 "$pid" 2>/dev/null || return 0
        sleep 0.1
    done
    echo "timed out waiting for pid $pid to exit" >&2
    return 1
}

wait_for_server_exit() {
    local socket="$1"
    for _ in $(seq 1 150); do
        tmux -S "$socket" list-sessions >/dev/null 2>&1 || return 0
        sleep 0.1
    done
    echo "timed out waiting for private tmux server exit: $socket" >&2
    return 1
}

if SIDECAR_DRIVE_REPO=relative "$DRIVER" paths >/dev/null 2>&1; then
    echo "relative launch repository unexpectedly accepted" >&2
    exit 1
fi
if SIDECAR_DRIVE_REPO="$TEST_ROOT/missing" "$DRIVER" paths >/dev/null 2>&1; then
    echo "missing launch repository unexpectedly accepted" >&2
    exit 1
fi
if SIDECAR_DRIVE_RUN_DIR="$HOME/./sidecar-proof" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" paths >/dev/null 2>&1; then
    echo "dot-spelled HOME run directory unexpectedly accepted" >&2
    exit 1
fi
mkdir -p "$TEST_ROOT/path-parent"
if SIDECAR_DRIVE_RUN_DIR="$TEST_ROOT/path-parent/../escape" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" paths >/dev/null 2>&1; then
    echo "dotdot-spelled run directory unexpectedly accepted" >&2
    exit 1
fi
ln -s "$HOME" "$TEST_ROOT/home-link"
if SIDECAR_DRIVE_RUN_DIR="$TEST_ROOT/home-link/proof" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" paths >/dev/null 2>&1; then
    echo "symlink-parent traversal into HOME unexpectedly accepted" >&2
    exit 1
fi
ln -s "$HOME" "$TEST_ROOT/root-link"
if SIDECAR_DRIVE_RUN_DIR="$TEST_ROOT/root-link" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" paths >/dev/null 2>&1; then
    echo "symlink run root into HOME unexpectedly accepted" >&2
    exit 1
fi
if SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_OUT="$RUN_DIR/../outside" \
    SIDECAR_DRIVE_REPO="$REPO" "$DRIVER" paths >/dev/null 2>&1; then
    echo "dotdot output directory unexpectedly accepted" >&2
    exit 1
fi

FAKE_SIDECAR="$TEST_ROOT/fake-sidecar"
cat >"$FAKE_SIDECAR" <<'SHIM'
#!/bin/bash
pwd >"$SIDECAR_TEST_PWD_FILE"
printf '%s\n' "$@" >"$SIDECAR_TEST_ARGS_FILE"
printf '%s\n' "${TMUX-}" >"$SIDECAR_TEST_LAUNCH_TMUX_FILE"
unset TMUX
tmux -S "$SIDECAR_TEST_INNER_SOCKET" new-session -d -s proof \
    "read line; printf '%s' \"\$line\" > '$SIDECAR_TEST_INNER_INPUT_FILE'; sleep 30"
mkdir -p "$(dirname "$SIDECAR_TEST_DECOY_SOCKET")"
chmod 700 "$(dirname "$SIDECAR_TEST_DECOY_SOCKET")"
tmux -S "$SIDECAR_TEST_DECOY_SOCKET" new-session -d -s proof 'sleep 30'
real_fifo="$SIDECAR_TEST_PWD_FILE.real.fifo"
decoy_fifo="$SIDECAR_TEST_PWD_FILE.decoy.fifo"
mkfifo "$real_fifo" "$decoy_fifo"
exec 8<>"$real_fifo"
exec 9<>"$decoy_fifo"
# Production shape: no -L/-S and TMUX unset, so expected TMUX_TMPDIR selects
# the real private server.
TMUX_TMPDIR="$(dirname "$(dirname "$SIDECAR_TEST_INNER_SOCKET")")" \
    tmux -C attach-session -f ignore-size -t proof <&8 >/dev/null 2>&1 &
real_control_pid=$!
# Adversarial production shape: the expected TMUX_TMPDIR is retained, but a
# nonempty TMUX selects the same-session decoy on another explicit private
# socket. control-clients must reject this descendant.
decoy_server_pid=$(tmux -S "$SIDECAR_TEST_DECOY_SOCKET" display-message -p '#{pid}')
TMUX="$SIDECAR_TEST_DECOY_SOCKET,$decoy_server_pid,0" \
TMUX_TMPDIR="$(dirname "$(dirname "$SIDECAR_TEST_INNER_SOCKET")")" \
    tmux -C attach-session -f ignore-size -t proof <&9 >/dev/null 2>&1 &
decoy_control_pid=$!
printf '%s\n' "$real_control_pid" >"$SIDECAR_TEST_REAL_CONTROL_PID_FILE"
printf '%s\n' "$decoy_control_pid" >"$SIDECAR_TEST_DECOY_CONTROL_PID_FILE"
cleanup_children() {
    trap - EXIT TERM HUP
    exec 8>&-
    exec 9>&-
    kill "$real_control_pid" "$decoy_control_pid" 2>/dev/null || true
    wait "$real_control_pid" "$decoy_control_pid" 2>/dev/null || true
}
trap cleanup_children EXIT TERM HUP
wait
SHIM
chmod +x "$FAKE_SIDECAR"

paths=$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" "$DRIVER" paths)
grep -F "launch repo:   $REPO" <<<"$paths" >/dev/null
grep -F "run dir:       $RUN_DIR" <<<"$paths" >/dev/null

SIDECAR_TEST_PWD_FILE="$PWD_FILE" SIDECAR_TEST_ARGS_FILE="$ARGS_FILE" \
SIDECAR_TEST_LAUNCH_TMUX_FILE="$LAUNCH_TMUX_FILE" \
SIDECAR_TEST_INNER_INPUT_FILE="$INNER_INPUT_FILE" \
SIDECAR_TEST_REAL_CONTROL_PID_FILE="$REAL_CONTROL_PID_FILE" \
SIDECAR_TEST_DECOY_CONTROL_PID_FILE="$DECOY_CONTROL_PID_FILE" \
SIDECAR_TEST_DECOY_TMUX_TMPDIR="$DECOY_TMUX_TMPDIR" \
SIDECAR_TEST_DECOY_SOCKET="$DECOY_SOCKET" \
SIDECAR_TEST_INNER_SOCKET="$TEST_INNER_SOCKET" \
SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
SIDECAR_DRIVE_ARGS="--enable-feature=notes_plugin --proof-trace" \
SIDECAR_BIN="$FAKE_SIDECAR" \
TMUX="$TEST_ROOT/forbidden-default,999999,0" \
    "$DRIVER" start 80 24 >/dev/null

host_pid=$(TMUX_TMPDIR="$RUN_DIR/tmux" tmux -L sidecar-drive \
    display-message -t host -p '#{pane_pid}')

wait_for_file "$PWD_FILE"
wait_for_file "$LAUNCH_TMUX_FILE"
wait_for_file "$REAL_CONTROL_PID_FILE"
wait_for_file "$DECOY_CONTROL_PID_FILE"
test "$(cat "$PWD_FILE")" = "$REPO"
case "$(cat "$LAUNCH_TMUX_FILE")" in
    "$RUN_DIR/tmux/tmux-$(id -u)/sidecar-drive,"*) ;;
    *) echo "launched Sidecar did not inherit only the private host socket" >&2; exit 1 ;;
esac
grep -Fx -- "-config" "$ARGS_FILE" >/dev/null
grep -Fx -- "$RUN_DIR/config/config.json" "$ARGS_FILE" >/dev/null
grep -Fx -- "--enable-feature=notes_plugin" "$ARGS_FILE" >/dev/null
grep -Fx -- "--proof-trace" "$ARGS_FILE" >/dev/null
test -S "$RUN_DIR/tmux/tmux-$(id -u)/sidecar-drive"

# A poisoned inherited selector cannot redirect helpers: the driver unsets
# TMUX and every operation names its private server explicitly.
poisoned_panes=$(TMUX="$TEST_ROOT/forbidden-default,999999,0" \
    SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" "$DRIVER" panes)
grep -F "proof" <<<"$poisoned_panes" >/dev/null

for _ in $(seq 1 150); do
    panes=$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" "$DRIVER" panes)
    grep -F "proof" <<<"$panes" >/dev/null && break
    sleep 0.1
done
pane=$(awk '$1 == "proof" { print $2; exit }' <<<"$panes")
case "$pane" in %*) ;; *) echo "failed to discover isolated proof pane" >&2; exit 1 ;; esac

if SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" capture-hook-reset >/dev/null 2>&1; then
    echo "capture-hook-reset unexpectedly succeeded before installation" >&2
    exit 1
fi

SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" inner-type "$pane" "INNER_MARKER" >/dev/null
SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" inner-keys "$pane" Enter >/dev/null
wait_for_file "$INNER_INPUT_FILE"
test "$(cat "$INNER_INPUT_FILE")" = "INNER_MARKER"
if SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" inner-keys missing Enter >/dev/null 2>&1; then
    echo "missing inner pane unexpectedly accepted" >&2
    exit 1
fi

hook_log=$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" capture-hook-install)
test "$hook_log" = "$RUN_DIR/proof/capture-hooks.log"
tmux -S "$RUN_DIR/tmux/tmux-$(id -u)/default" capture-pane -p -t "$pane" >/dev/null
for _ in $(seq 1 150); do
    hook_output=$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
        "$DRIVER" capture-hook-show)
    [ "$hook_output" = "$pane" ] && break
    sleep 0.1
done
test "$hook_output" = "$pane"
# Capturing the outer host pane must not invoke the hook installed on the inner
# server. This assertion also guards against accidentally using the default
# socket in capture-hook-install.
TMUX_TMPDIR="$RUN_DIR/tmux" tmux -L sidecar-drive capture-pane -p -t host >/dev/null
test "$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" capture-hook-show)" = "$pane"
SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" capture-hook-reset
test -z "$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" capture-hook-show)"

real_control_pid=$(cat "$REAL_CONTROL_PID_FILE")
decoy_control_pid=$(cat "$DECOY_CONTROL_PID_FILE")
kill -0 "$real_control_pid"
kill -0 "$decoy_control_pid"
for _ in $(seq 1 150); do
    clients=$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
        "$DRIVER" control-clients)
    [ "$(wc -l <<<"$clients" | tr -d ' ')" = "1" ] && [ -n "$clients" ] && break
    sleep 0.1
done
control_pid=$(awk -F '\t' 'NF { print $1 }' <<<"$clients")
control_session=$(awk -F '\t' 'NF { print $2 }' <<<"$clients")
test "$control_session" = "proof"
case "$control_pid" in *[!0-9]*|"") echo "invalid control pid" >&2; exit 1 ;; esac
test "$control_pid" = "$real_control_pid"
if SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" control-kill "$decoy_control_pid" >/dev/null 2>&1; then
    echo "wrong-socket descendant control pid unexpectedly accepted" >&2
    exit 1
fi
kill -0 "$decoy_control_pid"
SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" control-kill "$control_pid" >/dev/null
wait_for_pid_exit "$control_pid"

if SIDECAR_DRIVE_RUN_DIR="$HOME/unsafe-proof" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" paths >/dev/null 2>&1; then
    echo "HOME-contained run directory unexpectedly accepted" >&2
    exit 1
fi

FIXTURE_ROOT="$TEST_ROOT/cutover-fixture"
"$SCRIPT_DIR/terminal-cutover-fixture.sh" "$FIXTURE_ROOT" >/dev/null
test -f "$FIXTURE_ROOT/config/config.json"
test -x "$FIXTURE_ROOT/editors/nvim-proof"
test "$(git -C "$FIXTURE_ROOT/main" worktree list --porcelain | grep -c '^worktree ')" = "3"
grep -F '"notes_plugin": true' "$FIXTURE_ROOT/config/config.json" >/dev/null
if "$SCRIPT_DIR/terminal-cutover-fixture.sh" "$FIXTURE_ROOT" >/dev/null 2>&1; then
    echo "nonempty fixture root unexpectedly accepted" >&2
    exit 1
fi
if "$SCRIPT_DIR/terminal-cutover-fixture.sh" "$TEST_ROOT/home-link/fixture" >/dev/null 2>&1; then
    echo "fixture symlink traversal into HOME unexpectedly accepted" >&2
    exit 1
fi

SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" stop >/dev/null
tmux -S "$DECOY_SOCKET" kill-server >/dev/null 2>&1 || true
wait_for_pid_exit "$host_pid"
wait_for_pid_exit "$decoy_control_pid"
wait_for_server_exit "$DECOY_SOCKET"

echo "tmux-drive launch, inner helper, capture hook, and control-client checks passed"
