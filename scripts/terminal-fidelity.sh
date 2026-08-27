#!/bin/bash
# Inspect embedded-terminal background decisions from a live pane or sweep a
# real TUI across adjacent widths on a throwaway tmux server.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"

usage() {
    cat <<'EOF'
Usage:
  ./scripts/terminal-fidelity.sh live <tmux-target>
  ./scripts/terminal-fidelity.sh sweep --command <command> [options]

Modes:
  live TARGET          Capture TARGET read-only and print Sidecar's canvas
                       decision before and after the screen-model seam.

  sweep                Launch a real TUI on an isolated tmux server, resize it
                       through adjacent widths, retain raw ANSI captures, and
                       analyze every capture with Sidecar's production logic.

Sweep options:
  --command COMMAND    TUI command to launch (required).
  --widths LIST        Comma-separated widths (default: 119,120,121).
  --height ROWS        Pane height (default: 40).
  --settle SECONDS     Redraw wait after launch/resize (default: 2; max: 60).
  --out DIRECTORY      Capture directory (default: a new directory under /tmp).

Examples:
  ./scripts/terminal-fidelity.sh live %42
  ./scripts/terminal-fidelity.sh sweep --command codex --widths 144,145,146 --height 53
  ./scripts/terminal-fidelity.sh sweep --command cursor-agent --widths 99,100,101

Safety:
  live never sends input or resizes its target. sweep always uses an explicit
  private tmux socket and kills only that server. The launched command keeps
  your normal environment and HOME so installed TUIs can use their config and
  credentials; it may therefore perform whatever work the command normally
  performs. No prompt text is sent automatically.
EOF
}

run_probe() {
    cd "$REPO_DIR"
    go test ./internal/termpreview -run CanvasProbeLive -count=1 -v
}

if [[ $# -lt 1 ]]; then
    usage >&2
    exit 2
fi

MODE="$1"
shift

if [[ "$MODE" == "-h" || "$MODE" == "--help" ]]; then
    usage
    exit
fi

if [[ "$MODE" == "live" ]]; then
    if [[ $# -ne 1 ]]; then
        usage >&2
        exit 2
    fi
    CANVAS_PROBE_TARGET="$1" run_probe
    exit
fi

if [[ "$MODE" != "sweep" ]]; then
    usage >&2
    exit 2
fi

COMMAND=""
WIDTHS="119,120,121"
HEIGHT=40
SETTLE=2
OUT_DIR=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --command)
            COMMAND="${2:-}"
            shift 2
            ;;
        --widths)
            WIDTHS="${2:-}"
            shift 2
            ;;
        --height)
            HEIGHT="${2:-}"
            shift 2
            ;;
        --settle)
            SETTLE="${2:-}"
            shift 2
            ;;
        --out)
            OUT_DIR="${2:-}"
            shift 2
            ;;
        -h|--help)
            usage
            exit
            ;;
        *)
            printf 'unknown option: %s\n' "$1" >&2
            usage >&2
            exit 2
            ;;
    esac
done

if [[ -z "$COMMAND" ]]; then
    printf 'sweep requires --command\n' >&2
    exit 2
fi
if [[ ! "$HEIGHT" =~ ^[1-9][0-9]*$ ]]; then
    printf 'invalid height: %s\n' "$HEIGHT" >&2
    exit 2
fi
if [[ ! "$SETTLE" =~ ^([0-9]+([.][0-9]+)?|[.][0-9]+)$ ]] || ! awk -v n="$SETTLE" 'BEGIN { exit !(n >= 0 && n <= 60) }'; then
    printf 'settle must be between 0 and 60 seconds: %s\n' "$SETTLE" >&2
    exit 2
fi

IFS=',' read -r -a WIDTH_LIST <<< "$WIDTHS"
if [[ ${#WIDTH_LIST[@]} -eq 0 ]]; then
    printf 'width list is empty\n' >&2
    exit 2
fi
for width in "${WIDTH_LIST[@]}"; do
    if [[ ! "$width" =~ ^[1-9][0-9]*$ ]]; then
        printf 'invalid width: %s\n' "$width" >&2
        exit 2
    fi
done

RUNTIME_DIR="$(mktemp -d "${TMPDIR:-/tmp}/sidecar-terminal-fidelity.XXXXXX")"
SOCKET="$RUNTIME_DIR/tmux.sock"
SESSION="fidelity"
cleanup() {
    env -u TMUX tmux -S "$SOCKET" -f /dev/null kill-server >/dev/null 2>&1 || true
    rm -rf "$RUNTIME_DIR"
}
trap cleanup EXIT INT TERM

if [[ -z "$OUT_DIR" ]]; then
    OUT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/sidecar-terminal-fidelity-captures.XXXXXX")"
else
    mkdir -p "$OUT_DIR"
    shopt -s nullglob
    existing=("$OUT_DIR"/cap-*.txt)
    if [[ ${#existing[@]} -gt 0 ]]; then
        printf 'refusing to overwrite existing captures in %s\n' "$OUT_DIR" >&2
        exit 2
    fi
fi

TMUX=(env -u TMUX tmux -S "$SOCKET" -f /dev/null)
FIRST_WIDTH="${WIDTH_LIST[0]}"
"${TMUX[@]}" new-session -d -s "$SESSION" -x "$FIRST_WIDTH" -y "$HEIGHT" \
    -e "TERM=xterm-256color" "${SHELL:-/bin/zsh}" -lc "$COMMAND"
"${TMUX[@]}" set-option -t "$SESSION" status off
sleep "$SETTLE"

MANIFEST="$OUT_DIR/manifest.tsv"
printf 'requested_width\tactual_width\theight\tcommand\tpane_dead\tcapture\n' > "$MANIFEST"
for width in "${WIDTH_LIST[@]}"; do
    "${TMUX[@]}" resize-window -t "$SESSION" -x "$width" -y "$HEIGHT"
    sleep "$SETTLE"
    actual_width="$("${TMUX[@]}" display-message -p -t "$SESSION" '#{pane_width}')"
    current_command="$("${TMUX[@]}" display-message -p -t "$SESSION" '#{pane_current_command}')"
    pane_dead="$("${TMUX[@]}" display-message -p -t "$SESSION" '#{pane_dead}')"
    capture="cap-${width}x${HEIGHT}.txt"
    "${TMUX[@]}" capture-pane -p -e -t "$SESSION" > "$OUT_DIR/$capture"
    printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$width" "$actual_width" "$HEIGHT" "$current_command" "$pane_dead" "$capture" >> "$MANIFEST"
done

printf 'captures: %s\n' "$OUT_DIR"
CANVAS_PROBE_DIR="$OUT_DIR" CANVAS_PROBE_REQUIRE_STABLE=1 run_probe | tee "$OUT_DIR/analysis.log"
