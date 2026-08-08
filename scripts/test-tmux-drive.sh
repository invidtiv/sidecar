#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
DRIVER="$SCRIPT_DIR/tmux-drive.sh"
TEST_ROOT="$(mktemp -d /tmp/sidecar-tmux-drive-test.XXXXXX)"
RUN_DIR="$TEST_ROOT/drive"
REPO="$TEST_ROOT/repo"
PWD_FILE="$TEST_ROOT/pwd"
ARGS_FILE="$TEST_ROOT/args"
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
sleep 30
SHIM
chmod +x "$FAKE_SIDECAR"

paths=$(SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" "$DRIVER" paths)
grep -F "launch repo:   $REPO" <<<"$paths" >/dev/null
grep -F "run dir:       $RUN_DIR" <<<"$paths" >/dev/null

SIDECAR_TEST_PWD_FILE="$PWD_FILE" SIDECAR_TEST_ARGS_FILE="$ARGS_FILE" \
SIDECAR_DRIVE_RUN_DIR="$RUN_DIR" SIDECAR_DRIVE_REPO="$REPO" \
SIDECAR_BIN="$FAKE_SIDECAR" "$DRIVER" start 80 24 >/dev/null

for _ in 1 2 3 4 5; do
    [ -s "$PWD_FILE" ] && break
    sleep 0.1
done
test "$(cat "$PWD_FILE")" = "$REPO"
grep -Fx -- "-config" "$ARGS_FILE" >/dev/null
grep -Fx -- "$RUN_DIR/config/config.json" "$ARGS_FILE" >/dev/null
test -S "$RUN_DIR/tmux/tmux-$(id -u)/sidecar-drive"

echo "tmux-drive launch repository and isolation checks passed"
