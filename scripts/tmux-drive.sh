#!/bin/bash
# tmux-drive.sh - Drive sidecar headlessly for reproduction + visual verification.
#
# Runs sidecar on a dedicated tmux socket (-L sidecar-drive) so the outer host
# pane is never confused with the inner shell/agent sessions sidecar creates on
# the user's default tmux server.
#
#   ./scripts/tmux-drive.sh start [COLS] [LINES]  - launch sidecar (default 200x50)
#   ./scripts/tmux-drive.sh keys <args...>        - tmux send-keys passthrough
#   ./scripts/tmux-drive.sh type <text>           - send literal text
#   ./scripts/tmux-drive.sh snap [NAME]           - dump pane text (+ PNG if termshot)
#   ./scripts/tmux-drive.sh panes                 - inner sidecar tmux panes + sizes
#   ./scripts/tmux-drive.sh size                  - outer host pane size
#   ./scripts/tmux-drive.sh stop                  - kill the host session

set -euo pipefail

SOCKET="sidecar-drive"
SESSION="host"
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="${SIDECAR_DRIVE_OUT:-/tmp/sidecar-drive}"
T=(tmux -L "$SOCKET")

start() {
    local cols="${1:-200}" lines="${2:-50}"
    "${T[@]}" kill-session -t "$SESSION" 2>/dev/null || true
    mkdir -p "$OUT_DIR"
    "${T[@]}" new-session -d -s "$SESSION" -x "$cols" -y "$lines" -c "$REPO_DIR" \
        "TERM=xterm-256color ${SIDECAR_BIN:-sidecar}"
    "${T[@]}" set-option -t "$SESSION" status off
    echo "started ${cols}x${lines} in $REPO_DIR"
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

# Inner panes: the sessions sidecar itself created, on the default tmux server.
panes() {
    tmux list-panes -a -F \
        '#{session_name}  #{pane_id}  #{pane_width}x#{pane_height}  win=#{window_width}x#{window_height}  #{pane_current_command}  alt=#{alternate_on}  cur=#{cursor_x},#{cursor_y}  hist=#{history_size}' \
        2>/dev/null | grep -E 'sidecar|shell|agent|term' || echo "(no sidecar-created panes)"
}

case "${1:-}" in
    start) shift; start "$@" ;;
    keys)  shift; "${T[@]}" send-keys -t "$SESSION" "$@" ;;
    type)  shift; "${T[@]}" send-keys -t "$SESSION" -l "$*" ;;
    snap)  shift; snap "${1:-}" ;;
    panes) panes ;;
    size)  "${T[@]}" display-message -t "$SESSION" -p '#{pane_width}x#{pane_height}' ;;
    stop)  "${T[@]}" kill-session -t "$SESSION" 2>/dev/null || true; echo stopped ;;
    *)     sed -n '2,20p' "$0"; exit 1 ;;
esac
