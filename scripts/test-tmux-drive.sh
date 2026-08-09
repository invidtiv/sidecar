#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DRIVER="$SCRIPT_DIR/tmux-drive.sh"
TEST_ROOT="$(mktemp -d /tmp/sidecar-tmux-drive-test.XXXXXX)"
RUN_DIR="$TEST_ROOT/drive"
REPO="$TEST_ROOT/repo"
PWD_FILE="$TEST_ROOT/pwd"
ARGS_FILE="$TEST_ROOT/args"
INNER_INPUT_FILE="$TEST_ROOT/inner-input"
mkdir -p "$REPO"
REPO="$(cd "$REPO" && pwd -P)"

cleanup() {
    SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
        "$DRIVER" stop >/dev/null 2>&1 || true
    rm -rf "$TEST_ROOT"
}
trap cleanup EXIT

if SIDECAR_DRIVE_REPO=relative "$DRIVER" paths >/dev/null 2>&1; then
    echo "relative launch repository unexpectedly accepted" >&2
    exit 1
fi
if SIDECAR_DRIVE_REPO="$TEST_ROOT/missing" "$DRIVER" paths >/dev/null 2>&1; then
    echo "missing launch repository unexpectedly accepted" >&2
    exit 1
fi

FAKE_SIDECAR="$TEST_ROOT/fake-sidecar"
cat >"$FAKE_SIDECAR" <<'SHIM'
#!/bin/bash
pwd >"$SIDECAR_TEST_PWD_FILE"
printf '%s\n' "$@" >"$SIDECAR_TEST_ARGS_FILE"
unset TMUX
tmux new-session -d -s proof "read line; printf '%s' \"\$line\" > '$SIDECAR_TEST_INNER_INPUT_FILE'; sleep 30"
tail -f /dev/null | tmux -C attach-session -f ignore-size -t proof >/dev/null 2>&1 &
wait
SHIM
chmod +x "$FAKE_SIDECAR"

paths=$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" "$DRIVER" paths)
grep -F "launch repo:   $REPO" <<<"$paths" >/dev/null
grep -F "run dir:       $RUN_DIR" <<<"$paths" >/dev/null

SIDECAR_TEST_PWD_FILE="$PWD_FILE" SIDECAR_TEST_ARGS_FILE="$ARGS_FILE" \
SIDECAR_TEST_INNER_INPUT_FILE="$INNER_INPUT_FILE" \
SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
SIDECAR_DRIVE_ARGS="--enable-feature=notes_plugin --proof-trace" \
SIDECAR_BIN="$FAKE_SIDECAR" "$DRIVER" start 80 24 >/dev/null

for _ in 1 2 3 4 5; do
    [ -s "$PWD_FILE" ] && break
    sleep 0.1
done
test "$(cat "$PWD_FILE")" = "$REPO"
grep -Fx -- "-config" "$ARGS_FILE" >/dev/null
grep -Fx -- "$RUN_DIR/config/config.json" "$ARGS_FILE" >/dev/null
grep -Fx -- "--enable-feature=notes_plugin" "$ARGS_FILE" >/dev/null
grep -Fx -- "--proof-trace" "$ARGS_FILE" >/dev/null
test -S "$RUN_DIR/tmux/tmux-$(id -u)/sidecar-drive"

for _ in 1 2 3 4 5 6 7 8 9 10; do
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
for _ in 1 2 3 4 5; do
    [ -s "$INNER_INPUT_FILE" ] && break
    sleep 0.1
done
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
for _ in 1 2 3 4 5; do
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
sleep 0.1
test "$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" capture-hook-show)" = "$pane"
SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" capture-hook-reset
test -z "$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" capture-hook-show)"

# A byte-for-byte matching control client outside the Sidecar host process tree
# must not be listed or eligible for control-kill.
tail -f /dev/null | TMUX_TMPDIR="$RUN_DIR/tmux" \
    tmux -C attach-session -f ignore-size -t proof >/dev/null 2>&1 &
outsider_control_pid=$!
for _ in 1 2 3 4 5 6 7 8 9 10; do
    clients=$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
        "$DRIVER" control-clients)
    [ "$(wc -l <<<"$clients" | tr -d ' ')" = "1" ] && [ -n "$clients" ] && break
    sleep 0.1
done
control_pid=$(awk -F '\t' 'NF { print $1 }' <<<"$clients")
control_session=$(awk -F '\t' 'NF { print $2 }' <<<"$clients")
test "$control_session" = "proof"
case "$control_pid" in *[!0-9]*|"") echo "invalid control pid" >&2; exit 1 ;; esac
test "$control_pid" != "$outsider_control_pid"
if SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" control-kill "$outsider_control_pid" >/dev/null 2>&1; then
    echo "non-descendant pid unexpectedly accepted" >&2
    exit 1
fi
kill "$outsider_control_pid" 2>/dev/null || true
SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
    "$DRIVER" control-kill "$control_pid" >/dev/null
for _ in 1 2 3 4 5; do
    kill -0 "$control_pid" 2>/dev/null || break
    sleep 0.1
done
if kill -0 "$control_pid" 2>/dev/null; then
    echo "verified control client was not killed" >&2
    exit 1
fi

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

echo "tmux-drive launch, inner helper, capture hook, and control-client checks passed"
