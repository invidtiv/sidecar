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

start() {
    local cols="${1:-200}" lines="${2:-50}"
    case "$RUN_DIR" in
        ""|"/"|"$HOME")
            echo "refusing to use RUN_DIR='$RUN_DIR' - set SIDECAR_DRIVE_RUN_DIR to a scratch dir" >&2
            exit 1
            ;;
    esac
    "${T[@]}" kill-session -t "$SESSION" 2>/dev/null || true
    mkdir -p "$OUT_DIR" "$TMUX_TMPDIR" "$XDG_STATE_HOME" "$XDG_CACHE_HOME" "$(dirname "$CONFIG")"
    "${T[@]}" new-session -d -s "$SESSION" -x "$cols" -y "$lines" -c "$REPO_DIR" \
        "TERM=xterm-256color ${SIDECAR_BIN:-sidecar} -config $CONFIG"
    "${T[@]}" set-option -t "$SESSION" status off
    echo "started ${cols}x${lines} in $REPO_DIR (run dir $RUN_DIR)"
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
    echo "run dir:       $RUN_DIR"
    echo "inner socket:  $INNER_SOCKET"
    echo "state home:    $XDG_STATE_HOME"
    echo "cache home:    $XDG_CACHE_HOME"
    echo "config:        $CONFIG"
    echo "manifest:      $XDG_STATE_HOME/sidecar/projects/<slug>/shells.json"
}

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
