#!/bin/bash
# remote-content-proof.sh - Isolated recipe for remote Sessions content clicks.
#
# Wraps remote-spike.sh (host) and tmux-drive.sh (viewer). Isolates BOTH tmux
# and Sidecar state on both ends. Does not kill the default tmux server and
# does not rewrite ~/.local/state/sidecar.
#
# SPIKE_HOST is required for any command that talks to a second machine. There
# is no default: a live workstation Sidecar/state tree is not a proof target.
#
#   ./scripts/remote-content-proof.sh check-isolation
#   ./scripts/remote-content-proof.sh paths
#   SPIKE_HOST=proof-box ./scripts/remote-content-proof.sh setup
#   SPIKE_HOST=proof-box ./scripts/remote-content-proof.sh teardown
#
# Full click sequence: docs/guides/active/remote-content-pane-proof.md

set -euo pipefail
unset TMUX

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
SPIKE="$REPO_DIR/scripts/remote-spike.sh"
DRIVE="$REPO_DIR/scripts/tmux-drive.sh"

VIEW_RUN_DIR="${CONTENT_PROOF_RUN_DIR:-/tmp/sidecar-content-proof-$(id -u)}"
SPIKE_RUN_DIR="${SPIKE_RUN_DIR:-/tmp/sidecar-spike-$(id -un)}"
SNAPSHOT_DIR="/tmp/sidecar-content-proof-$(id -u).isolation"

refuse_run_dir() {
    echo "refusing run dir '$1': $2" >&2
    exit 1
}

assert_proof_run_dir() {
    local dir="$1" allow="$2"
    case "$dir" in
        */)     refuse_run_dir "$dir" "a trailing slash is not allowed" ;;
        *//*)   refuse_run_dir "$dir" "empty path component" ;;
    esac
    case "/$dir/" in
        */../*|*/./*) refuse_run_dir "$dir" "dot and dotdot components are not allowed" ;;
    esac
    case "$dir" in
        $allow) ;;
        *) refuse_run_dir "$dir" "must match $allow" ;;
    esac
}

assert_proof_run_dir "$VIEW_RUN_DIR" "/tmp/sidecar-content-proof*"
assert_proof_run_dir "$SPIKE_RUN_DIR" "/tmp/sidecar-spike*"

require_host() {
    if [ -z "${SPIKE_HOST:-}" ]; then
        echo "SPIKE_HOST is required (no default). Do not point this at a live workstation Sidecar or its real state tree." >&2
        exit 2
    fi
}

real_state_root() { printf '%s' "${HOME}/.local/state/sidecar"; }
real_config_root() { printf '%s' "${HOME}/.config/sidecar"; }

path_is_under() {
    local path="$1" root="$2"
    case "$path/" in
        "$root"|"$root"/*) return 0 ;;
    esac
    return 1
}

default_tmux_ls() {
    tmux ls 2>/dev/null || true
}

cmd_check_isolation() {
    mkdir -p "$SNAPSHOT_DIR"
    local state_root config_root sessions
    state_root="$(real_state_root)"
    config_root="$(real_config_root)"
    sessions="$(default_tmux_ls)"

    if path_is_under "$VIEW_RUN_DIR" "$state_root" || path_is_under "$VIEW_RUN_DIR" "$config_root"; then
        echo "FAIL: viewer run dir resolves inside the real Sidecar tree" >&2
        exit 1
    fi
    if path_is_under "$SPIKE_RUN_DIR" "$state_root" || path_is_under "$SPIKE_RUN_DIR" "$config_root"; then
        echo "FAIL: spike run dir resolves inside the real Sidecar tree" >&2
        exit 1
    fi
    case "$VIEW_RUN_DIR" in
        "$HOME"/.local/*|"$HOME"/.config/*) echo "FAIL: viewer run dir is under HOME state/config" >&2; exit 1 ;;
    esac

    if [ ! -f "$SNAPSHOT_DIR/tmux.ls" ]; then
        printf '%s\n' "$sessions" > "$SNAPSHOT_DIR/tmux.ls"
        echo "recorded default tmux session list in $SNAPSHOT_DIR/tmux.ls"
    else
        echo "default tmux sessions at start:"
        cat "$SNAPSHOT_DIR/tmux.ls" | sed 's/^/  /'
        echo "default tmux sessions now:"
        printf '%s\n' "$sessions" | sed 's/^/  /'
        # A live user session may appear; a proof must not *remove* one.
        while IFS= read -r line; do
            [ -z "$line" ] && continue
            if ! printf '%s\n' "$sessions" | grep -Fqx "$line"; then
                echo "FAIL: default tmux session missing since snapshot: $line" >&2
                exit 1
            fi
        done < "$SNAPSHOT_DIR/tmux.ls"
        echo "  all snapshotted default sessions still present"
    fi

    echo "viewer run dir      $VIEW_RUN_DIR"
    echo "spike run dir       $SPIKE_RUN_DIR"
    echo "real state          $state_root"
    echo "real config         $config_root"
    echo "SIDECAR_ISOLATED_STATE will be 1 on both ends"
    echo "these must NOT appear as run roots: $state_root, $config_root"
}

cmd_paths() {
    echo "viewer (tmux-drive):"
    SIDECAR_DRIVE_RUN_DIR="$VIEW_RUN_DIR" "$DRIVE" paths
    echo
    echo "proof helper:"
    echo "  CONTENT_PROOF_RUN_DIR $VIEW_RUN_DIR"
    echo "  SPIKE_RUN_DIR         $SPIKE_RUN_DIR"
    echo "  SPIKE_HOST            ${SPIKE_HOST:-<unset, required for setup>}"
    echo
    if [ -n "${SPIKE_HOST:-}" ]; then
        echo "host (remote-spike):"
        SPIKE_RUN_DIR="$SPIKE_RUN_DIR" "$SPIKE" paths
    else
        echo "host (remote-spike): skipped (set SPIKE_HOST to print remote paths)"
    fi
}

plant_viewer_markers() {
    local project="$VIEW_RUN_DIR/project"
    mkdir -p "$project"
    printf 'LOCAL-TWIN\n' > "$project/twin.txt"
    if [ ! -d "$project/.git" ]; then
        git -C "$project" init -q
        git -C "$project" config user.email proof@example.com
        git -C "$project" config user.name Proof
        git -C "$project" add twin.txt
        git -C "$project" commit -qm 'viewer local twin'
    fi
    mkdir -p "$VIEW_RUN_DIR/tmux/tmux-$(id -u)"
    chmod 700 "$VIEW_RUN_DIR/tmux/tmux-$(id -u)"
    local socket="$VIEW_RUN_DIR/tmux/tmux-$(id -u)/default"
    if ! TMUX= tmux -S "$socket" has-session -t sidecar-sh-twin-1 2>/dev/null; then
        TMUX= tmux -S "$socket" new-session -d -s sidecar-sh-twin-1 -c "$project" -x 80 -y 24 'read _hold'
    fi
    echo "planted viewer markers in $project (LOCAL-TWIN, session sidecar-sh-twin-1 on $socket)"
}

plant_host_markers() {
    require_host
    SPIKE_RUN_DIR="$SPIKE_RUN_DIR" "$SPIKE" ssh "set -e
        project=$SPIKE_RUN_DIR/project
        mkdir -p \"\$project\"
        printf 'REMOTE-MARKER\nsee twin.txt:20\n' > \"\$project/twin.txt\"
        cd \"\$project\"
        git add twin.txt || true
        git -c user.email=proof@example.com -c user.name=Proof commit -qm 'host remote marker' || true
        printf 'host-only\n' >> \"\$project/dirty.txt\"
        socket=$SPIKE_RUN_DIR/tmux/tmux-\$(id -u)/default
        mkdir -p \"\$(dirname \"\$socket\")\"
        chmod 700 \"\$(dirname \"\$socket\")\"
        export TMUX=
        tmux -S \"\$socket\" has-session -t sidecar-sh-twin-1 2>/dev/null || \
          tmux -S \"\$socket\" new-session -d -s sidecar-sh-twin-1 -c \"\$project\" -x 80 -y 24 'printf \"%s\\n\" \"see twin.txt:20 sidecar-sh-twin-1 https://example.com\"; read _hold'
    "
    echo "planted host markers (REMOTE-MARKER, session sidecar-sh-twin-1)"
}

cmd_setup() {
    require_host
    cmd_check_isolation
    plant_viewer_markers
    SPIKE_RUN_DIR="$SPIKE_RUN_DIR" "$SPIKE" deploy
    SPIKE_RUN_DIR="$SPIKE_RUN_DIR" "$SPIKE" fixture
    plant_host_markers
    echo
    echo "next: SPIKE_HOST=$SPIKE_HOST SPIKE_RUN_DIR=$SPIKE_RUN_DIR ./scripts/remote-spike.sh probe"
    echo "then register the host in the viewer's isolated config and drive clicks;"
    echo "see docs/guides/active/remote-content-pane-proof.md"
}

cmd_teardown() {
    if [ -n "${SPIKE_HOST:-}" ]; then
        SPIKE_RUN_DIR="$SPIKE_RUN_DIR" "$SPIKE" teardown || true
    fi
    local socket="$VIEW_RUN_DIR/tmux/tmux-$(id -u)/default"
    if [ -S "$socket" ]; then
        case "$socket" in
            "$VIEW_RUN_DIR"/*)
                echo "killing ONLY the private viewer server at $socket"
                TMUX= tmux -S "$socket" kill-server 2>/dev/null || true
                ;;
            *) echo "refusing teardown: viewer socket '$socket' is outside $VIEW_RUN_DIR" >&2; exit 1 ;;
        esac
    fi
    SIDECAR_DRIVE_RUN_DIR="$VIEW_RUN_DIR" "$DRIVE" stop >/dev/null 2>&1 || true
    if [ -d "$VIEW_RUN_DIR" ]; then
        case "$VIEW_RUN_DIR" in
            /tmp/sidecar-content-proof*) rm -rf "$VIEW_RUN_DIR" ;;
            *) echo "refusing to delete $VIEW_RUN_DIR" >&2; exit 1 ;;
        esac
    fi
    echo "default server left untouched"
    default_tmux_ls | sed 's/^/  /'
}

usage() {
    sed -n '2,18p' "$0"
    exit 2
}

case "${1:-}" in
    check-isolation) shift; cmd_check_isolation "$@" ;;
    paths)           shift; cmd_paths "$@" ;;
    setup)           shift; cmd_setup "$@" ;;
    teardown)        shift; cmd_teardown "$@" ;;
    *) usage ;;
esac
