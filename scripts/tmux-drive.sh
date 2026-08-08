#!/bin/bash
# tmux-drive.sh - Drive sidecar headlessly for reproduction + visual verification.
#
# Isolated on BOTH axes, because isolating either alone is not enough (td-8d18de):
#   - tmux: the outer host pane runs on -L sidecar-drive, and TMUX_TMPDIR moves the
#     *inner* sessions sidecar creates onto a private server too. (sidecar unsets
#     TMUX and never passes -L, so TMUX_TMPDIR is the only lever that works on it.)
#   - state: XDG_STATE_HOME plus -config move the shell manifest, state.json and
#     debug.log out of the developer's live tree. SIDECAR_ISOLATED_STATE=1 makes
#     sidecar fail closed if anything still resolves back into it.
#
# One run root per driver. Two agents driving proofs at once would otherwise
# share a tmux server AND a state tree, and the second start would kill the
# first mid-capture - the very cross-instance contention this isolates against.
# Set SIDECAR_DRIVE_RUN_DIR per agent when running concurrently.
# Set SIDECAR_DRIVE_REPO to an absolute existing fixture directory when the
# proof must launch outside this Sidecar checkout.
#
#   ./scripts/tmux-drive.sh start [COLS] [LINES]  - launch sidecar (default 200x50)
#   ./scripts/tmux-drive.sh keys <args...>        - tmux send-keys passthrough
#   ./scripts/tmux-drive.sh type <text>           - send literal text
#   ./scripts/tmux-drive.sh snap [NAME]           - dump pane text (+ PNG if termshot)
#   ./scripts/tmux-drive.sh panes                 - inner sidecar tmux panes + sizes
#   ./scripts/tmux-drive.sh size                  - outer host pane size
#   ./scripts/tmux-drive.sh paths                 - print the isolated roots in use
#   ./scripts/tmux-drive.sh stop                  - kill the host session + inner server

set -euo pipefail

SOCKET="sidecar-drive"
SESSION="host"
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
LAUNCH_REPO="${SIDECAR_DRIVE_REPO:-$REPO_DIR}"
OUT_DIR="${SIDECAR_DRIVE_OUT:-/tmp/sidecar-drive}"
T=(tmux -L "$SOCKET")

# One stable run root per user so start/keys/snap/stop - separate processes -
# all agree on which private tmux server and which state tree they mean.
RUN_DIR="${SIDECAR_DRIVE_RUN_DIR:-/tmp/sidecar-drive-$(id -u)}"
export TMUX_TMPDIR="$RUN_DIR/tmux"
export XDG_STATE_HOME="$RUN_DIR/state"
export XDG_CACHE_HOME="$RUN_DIR/cache"
export SIDECAR_ISOLATED_STATE=1
CONFIG="$RUN_DIR/config/config.json"
INNER_SOCKET="$TMUX_TMPDIR/tmux-$(id -u)/default"

validate_launch_repo() {
    case "$LAUNCH_REPO" in
        /*) ;;
        *)
            echo "refusing launch repo '$LAUNCH_REPO': SIDECAR_DRIVE_REPO must be an absolute path" >&2
            exit 1
            ;;
    esac
    if [ ! -d "$LAUNCH_REPO" ]; then
        echo "refusing launch repo '$LAUNCH_REPO': directory does not exist" >&2
        exit 1
    fi
    LAUNCH_REPO="$(cd "$LAUNCH_REPO" && pwd -P)"
}

start() {
    local cols="${1:-200}" lines="${2:-50}"
    case "$RUN_DIR" in
        ""|"/"|"$HOME")
            echo "refusing to use RUN_DIR='$RUN_DIR' - set SIDECAR_DRIVE_RUN_DIR to a scratch dir" >&2
            exit 1
            ;;
    esac
    if "${T[@]}" has-session -t "$SESSION" 2>/dev/null; then
        if [ "${SIDECAR_DRIVE_FORCE:-0}" != "1" ]; then
            echo "a drive session is already running in $RUN_DIR." >&2
            echo "another agent may be mid-capture. Set SIDECAR_DRIVE_RUN_DIR to your own" >&2
            echo "scratch dir, or SIDECAR_DRIVE_FORCE=1 to take this one over." >&2
            exit 1
        fi
        "${T[@]}" kill-session -t "$SESSION" 2>/dev/null || true
    fi
    mkdir -p "$OUT_DIR" "$TMUX_TMPDIR" "$XDG_STATE_HOME" "$XDG_CACHE_HOME" "$(dirname "$CONFIG")"
    "${T[@]}" new-session -d -s "$SESSION" -x "$cols" -y "$lines" -c "$LAUNCH_REPO" \
        "TERM=xterm-256color ${SIDECAR_BIN:-sidecar} -config $CONFIG"
    "${T[@]}" set-option -t "$SESSION" status off
    echo "started ${cols}x${lines} in $LAUNCH_REPO (run dir $RUN_DIR)"
}

snap() {
    local name="${1:-snap-$(date +%H%M%S)}"
    local txt="$OUT_DIR/$name.txt"
    mkdir -p "$OUT_DIR"
    "${T[@]}" capture-pane -t "$SESSION" -e -p > "$txt"
    if command -v termshot &>/dev/null; then
        local cols
        cols=$("${T[@]}" display-message -t "$SESSION" -p '#{pane_width}')
        termshot --raw-read "$txt" --columns "$cols" --filename "$OUT_DIR/$name.png" &>/dev/null
        echo "$OUT_DIR/$name.png"
    fi
    echo "$txt"
}

# Inner panes: the sessions sidecar itself created. They live on the private
# server named by INNER_SOCKET, never on the user's default one - query it by
# explicit path so this can never report (or touch) the developer's sessions.
panes() {
    local out
    out=$(tmux -S "$INNER_SOCKET" list-panes -a -F \
        '#{session_name}  #{pane_id}  #{pane_width}x#{pane_height}  win=#{window_width}x#{window_height}  #{pane_current_command}  alt=#{alternate_on}  cur=#{cursor_x},#{cursor_y}  hist=#{history_size}' \
        2>/dev/null || true)
    if [ -n "$out" ]; then
        echo "$out"
    else
        echo "(no sidecar-created panes)"
    fi
}

paths() {
    echo "launch repo:   $LAUNCH_REPO"
    echo "run dir:       $RUN_DIR"
    echo "inner socket:  $INNER_SOCKET"
    echo "state home:    $XDG_STATE_HOME"
    echo "cache home:    $XDG_CACHE_HOME"
    echo "config:        $CONFIG"
    echo "manifest:      $XDG_STATE_HOME/sidecar/projects/<slug>/shells.json"
}

validate_launch_repo

case "${1:-}" in
    start) shift; start "$@" ;;
    keys)  shift; "${T[@]}" send-keys -t "$SESSION" "$@" ;;
    type)  shift; "${T[@]}" send-keys -t "$SESSION" -l "$*" ;;
    snap)  shift; snap "${1:-}" ;;
    panes) panes ;;
    paths) paths ;;
    size)  "${T[@]}" display-message -t "$SESSION" -p '#{pane_width}x#{pane_height}' ;;
    stop)
        "${T[@]}" kill-session -t "$SESSION" 2>/dev/null || true
        # Safe because -S names a socket file inside RUN_DIR. Never a bare
        # kill-server: that would take down the developer's default server.
        tmux -S "$INNER_SOCKET" kill-server 2>/dev/null || true
        echo stopped
        ;;
    *)     sed -n '2,30p' "$0"; exit 1 ;;
esac
