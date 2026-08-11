#!/bin/bash
# prove-bundled-update.sh - drive the real app through the bundled update
# journeys for td-393e81 and retain the evidence.
#
# Isolation: every run uses scripts/tmux-drive.sh (private tmux server + private
# state/config tree) with a fake package-manager world from
# scripts/bundled-update-fixture.sh ahead of PATH. The machine's default tmux
# server, real Homebrew, real products, and live Sidecar state are never touched.
#
#   ./scripts/prove-bundled-update.sh [SCENARIO ...]
#
# Evidence lands in docs/screenshots/bundled-update/.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
EVIDENCE="$REPO_DIR/docs/screenshots/bundled-update"
FIX_ROOT="/tmp/sidecar-bundled-update-fixture"
export SIDECAR_DRIVE_RUN_DIR="/tmp/sidecar-bundled-update-drive"
export SIDECAR_DRIVE_FORCE=1

DRIVE="$REPO_DIR/scripts/tmux-drive.sh"
CONFIG_DIR="$SIDECAR_DRIVE_RUN_DIR/config"
CONFIG="$CONFIG_DIR/config.json"

API_PORT="${BUNDLED_UPDATE_API_PORT:-8731}"
API_PID=""

mkdir -p "$EVIDENCE"

start_release_api() {
    python3 -m http.server "$API_PORT" --bind 127.0.0.1 \
        --directory "$FIX_ROOT/api" >/dev/null 2>&1 &
    API_PID=$!
    sleep 1
}

stop_release_api() {
    [ -n "$API_PID" ] && kill "$API_PID" 2>/dev/null || true
    API_PID=""
}

cleanup() {
    stop_release_api
    "$DRIVE" stop >/dev/null 2>&1 || true
}
trap cleanup EXIT

write_config() {
    local tasks_enabled="$1"
    mkdir -p "$CONFIG_DIR"
    cat > "$CONFIG" <<EOF
{
  "features": { "flags": { "tasks_plugin": $tasks_enabled } }
}
EOF
}

capture() {
    local name="$1"
    "$DRIVE" snap "$name" >/dev/null
    cp "$SIDECAR_DRIVE_RUN_DIR/out/$name.txt" "$EVIDENCE/$name.txt"
    [ -f "$SIDECAR_DRIVE_RUN_DIR/out/$name.png" ] &&
        cp "$SIDECAR_DRIVE_RUN_DIR/out/$name.png" "$EVIDENCE/$name.png"
    echo "  captured $name"
}

run_scenario() {
    local scenario="$1" fixture="$2" tasks_enabled="$3" confirm="$4"

    echo "== $scenario (fixture=$fixture tasks_plugin=$tasks_enabled)"
    "$REPO_DIR/scripts/bundled-update-fixture.sh" build "$FIX_ROOT" "$fixture" >/dev/null
    write_config "$tasks_enabled"
    start_release_api

    SIDECAR_BIN="$FIX_ROOT/wrapper.sh" "$DRIVE" start 120 40 >/dev/null
    sleep 6

    "$DRIVE" keys '!' >/dev/null
    sleep 4
    capture "$scenario-1-diagnostics"

    "$DRIVE" keys u >/dev/null
    sleep 2
    capture "$scenario-2-preview"

    if [ "$confirm" = "confirm" ]; then
        "$DRIVE" keys C-m >/dev/null
        sleep 1
        capture "$scenario-3-progress"
        sleep 6
        capture "$scenario-4-result"
    fi

    "$DRIVE" stop >/dev/null || true
    stop_release_api

    {
        echo "# $scenario - package-manager commands the app actually ran"
        cat "$FIX_ROOT/log/commands.log" 2>/dev/null || echo "(none)"
    } > "$EVIDENCE/$scenario-commands.log"
    echo "  commands: $EVIDENCE/$scenario-commands.log"
}

scenarios=("$@")
if [ ${#scenarios[@]} -eq 0 ]; then
    scenarios=(tasks-disabled all-outdated tasks-current tasks-absent mixed tasks-fails standalone-only)
fi

for s in "${scenarios[@]}"; do
    case "$s" in
        tasks-disabled)  run_scenario tasks-disabled all-outdated false none ;;
        all-outdated)    run_scenario all-outdated all-outdated true confirm ;;
        tasks-current)   run_scenario tasks-current tasks-current true none ;;
        tasks-absent)    run_scenario tasks-absent tasks-absent true none ;;
        mixed)           run_scenario mixed mixed true confirm ;;
        tasks-fails)     run_scenario tasks-fails tasks-fails true confirm ;;
        standalone-only) run_scenario standalone-only standalone-only true confirm ;;
        *) echo "unknown scenario: $s" >&2; exit 1 ;;
    esac
done

echo "evidence in $EVIDENCE"
