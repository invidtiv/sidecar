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
# SIDECAR_DRIVE_ARGS is split on whitespace and appended to the sidecar command;
# it is intended for proof-only flags with no embedded spaces.
#
#   ./scripts/tmux-drive.sh start [COLS] [LINES]  - launch sidecar (default 200x50)
#   ./scripts/tmux-drive.sh keys <args...>        - tmux send-keys passthrough
#   ./scripts/tmux-drive.sh type <text>           - send literal text
#   ./scripts/tmux-drive.sh snap [NAME]           - dump pane text (+ PNG if termshot)
#   ./scripts/tmux-drive.sh panes                 - inner sidecar tmux panes + sizes
#   ./scripts/tmux-drive.sh inner-keys PANE KEY... - send keys to one resolved inner pane
#   ./scripts/tmux-drive.sh inner-type PANE TEXT   - send literal text to one inner pane
#   ./scripts/tmux-drive.sh control-clients        - exact control clients below Sidecar
#   ./scripts/tmux-drive.sh control-kill PID       - kill one reverified control client
#   ./scripts/tmux-drive.sh capture-hook-install   - log inner capture-pane commands
#   ./scripts/tmux-drive.sh capture-hook-reset     - truncate the private capture log
#   ./scripts/tmux-drive.sh capture-hook-show      - print the private capture log
#   ./scripts/tmux-drive.sh size                  - outer host pane size
#   ./scripts/tmux-drive.sh paths                 - print the isolated roots in use
#   ./scripts/tmux-drive.sh stop                  - kill the host session + inner server

set -euo pipefail

# Never let the caller's attached tmux client select a server implicitly. Every
# driver operation below uses either the private outer -L name or inner -S path.
unset TMUX

SOCKET="sidecar-drive"
SESSION="host"
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
LAUNCH_REPO="${SIDECAR_DRIVE_REPO:-$REPO_DIR}"
T=(tmux -L "$SOCKET")

# One stable run root per user so start/keys/snap/stop - separate processes -
# all agree on which private tmux server and which state tree they mean.
RAW_RUN_DIR="${SIDECAR_DRIVE_RUN_DIR:-/tmp/sidecar-drive-$(id -u)}"

canonicalize_run_dir() {
    local raw="$RAW_RUN_DIR" parent base canonical_parent canonical_run
    local tmp_root system_tmp_root allowed=0
    case "$raw" in
        /*) ;;
        *)
            echo "refusing RUN_DIR='$raw': SIDECAR_DRIVE_RUN_DIR must be absolute" >&2
            exit 1
            ;;
    esac
    case "$raw" in
        */) echo "refusing RUN_DIR='$raw': trailing slash is not allowed" >&2; exit 1 ;;
    esac
    case "/$raw/" in
        */./*|*/../*)
            echo "refusing RUN_DIR='$raw': dot and dotdot path components are not allowed" >&2
            exit 1
            ;;
    esac
    case "$raw" in
        *[!A-Za-z0-9_./-]*)
            echo "refusing RUN_DIR='$raw': path contains unsupported characters" >&2
            exit 1
            ;;
    esac

    parent=$(dirname "$raw")
    base=$(basename "$raw")
    [ -d "$parent" ] || {
        echo "refusing RUN_DIR='$raw': its immediate parent must already exist" >&2
        exit 1
    }
    canonical_parent=$(cd "$parent" && pwd -P)
    canonical_run="$canonical_parent/$base"
    if [ -e "$raw" ]; then
        [ -d "$raw" ] || { echo "refusing RUN_DIR='$raw': not a directory" >&2; exit 1; }
        [ "$(cd "$raw" && pwd -P)" = "$canonical_run" ] || {
            echo "refusing RUN_DIR='$raw': symlink traversal is not allowed" >&2
            exit 1
        }
    fi

    system_tmp_root=$(cd /tmp && pwd -P)
    tmp_root=$(cd "${TMPDIR:-/tmp}" && pwd -P)
    case "$canonical_run" in
        "$system_tmp_root"/*|"$tmp_root"/*) allowed=1 ;;
    esac
    [ "$allowed" -eq 1 ] || {
        echo "refusing RUN_DIR='$raw': canonical path must be under /tmp or TMPDIR" >&2
        exit 1
    }
    RUN_DIR="$canonical_run"
}

canonicalize_run_dir
OUT_DIR="${SIDECAR_DRIVE_OUT:-$RUN_DIR/out}"
case "$OUT_DIR" in
    "$RUN_DIR"/*)
        out_suffix=${OUT_DIR#"$RUN_DIR"/}
        case "$out_suffix" in
            ""|.|..|*/*)
                echo "refusing output directory outside a direct RUN_DIR child: $OUT_DIR" >&2
                exit 1
                ;;
        esac
        if [ -e "$OUT_DIR" ] && [ "$(cd "$OUT_DIR" && pwd -P)" != "$OUT_DIR" ]; then
            echo "refusing symlink output directory: $OUT_DIR" >&2
            exit 1
        fi
        ;;
    *)
        echo "refusing output directory outside RUN_DIR: $OUT_DIR" >&2
        exit 1
        ;;
esac
export TMUX_TMPDIR="$RUN_DIR/tmux"
export XDG_STATE_HOME="$RUN_DIR/state"
export XDG_CACHE_HOME="$RUN_DIR/cache"
export SIDECAR_ISOLATED_STATE=1
CONFIG="$RUN_DIR/config/config.json"
INNER_SOCKET="$TMUX_TMPDIR/tmux-$(id -u)/default"
CAPTURE_DIR="$RUN_DIR/proof"
CAPTURE_LOG="$CAPTURE_DIR/capture-hooks.log"
CAPTURE_HELPER="$CAPTURE_DIR/capture-hook.sh"

validate_derived_paths() {
    local path
    for path in "$OUT_DIR" "$TMUX_TMPDIR" "$XDG_STATE_HOME" "$XDG_CACHE_HOME" \
        "$CONFIG" "$INNER_SOCKET" "$CAPTURE_DIR" "$CAPTURE_LOG" "$CAPTURE_HELPER"; do
        case "$path" in
            "$RUN_DIR"/*) ;;
            *)
                echo "refusing derived path outside RUN_DIR: $path" >&2
                exit 1
                ;;
        esac
    done
}

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
    local -a command_args=(-config "$CONFIG")
    local -a extra_args
    local launch_cmd arg
    if [ -n "${SIDECAR_DRIVE_ARGS:-}" ]; then
        # Deliberately no eval: proof flags are whitespace-delimited arguments.
        read -r -a extra_args <<<"$SIDECAR_DRIVE_ARGS"
        command_args+=("${extra_args[@]}")
    fi
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
    printf -v launch_cmd 'TERM=xterm-256color exec %q' "${SIDECAR_BIN:-sidecar}"
    for arg in "${command_args[@]}"; do
        printf -v launch_cmd '%s %q' "$launch_cmd" "$arg"
    done
    "${T[@]}" new-session -d -s "$SESSION" -x "$cols" -y "$lines" -c "$LAUNCH_REPO" \
        "$launch_cmd"
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

resolve_inner_pane() {
    local target="${1:-}" pane
    case "$target" in
        ""|*[!A-Za-z0-9_.:%-]*)
            echo "refusing inner target '$target': use a session name or pane id" >&2
            return 1
            ;;
    esac
    pane=$(tmux -S "$INNER_SOCKET" display-message -p -t "$target" '#{pane_id}' 2>/dev/null) || {
        echo "inner target '$target' does not exist on $INNER_SOCKET" >&2
        return 1
    }
    case "$pane" in
        %*) printf '%s\n' "$pane" ;;
        *)
            echo "inner target '$target' did not resolve to a pane" >&2
            return 1
            ;;
    esac
}

inner_keys() {
    local pane
    [ "$#" -ge 2 ] || { echo "usage: $0 inner-keys PANE KEY..." >&2; return 1; }
    pane=$(resolve_inner_pane "$1") || return 1
    shift
    tmux -S "$INNER_SOCKET" send-keys -t "$pane" "$@"
}

inner_type() {
    local pane target="${1:-}"
    [ "$#" -eq 2 ] || { echo "usage: $0 inner-type PANE TEXT" >&2; return 1; }
    pane=$(resolve_inner_pane "$target") || return 1
    tmux -S "$INNER_SOCKET" send-keys -l -t "$pane" "$2"
}

host_descendants() {
    local host_pid
    host_pid=$("${T[@]}" display-message -t "$SESSION" -p '#{pane_pid}' 2>/dev/null) || {
        echo "Sidecar host session is not running" >&2
        return 1
    }
    case "$host_pid" in
        *[!0-9]*|"") echo "invalid Sidecar host pid '$host_pid'" >&2; return 1 ;;
    esac
    ps -axo pid=,ppid=,command= | awk -v root="$host_pid" '
        { pid[NR]=$1; ppid[NR]=$2; $1=""; $2=""; sub(/^[[:space:]]+/, ""); cmd[NR]=$0 }
        END {
            seen[root]=1
            for (pass=1; pass<=NR; pass++)
                for (i=1; i<=NR; i++)
                    if (seen[ppid[i]]) seen[pid[i]]=1
            for (i=1; i<=NR; i++)
                if (pid[i] != root && seen[pid[i]])
                    printf "%s\t%s\n", pid[i], cmd[i]
        }'
}

control_clients() {
    local descendants pid command session process_env process_tmux socket
    descendants=$(host_descendants) || return 1
    while IFS=$'\t' read -r pid command; do
        # Match Sidecar's production shape or the test's stronger explicit-S
        # shape. In both cases the socket identity and target are rechecked.
        if [[ "$command" =~ (^|/)(tmux)[[:space:]]+-S[[:space:]]+([^[:space:]]+)[[:space:]]+-C[[:space:]]+attach-session[[:space:]]+-f[[:space:]]+ignore-size[[:space:]]+-t[[:space:]]+([^[:space:]]+)[[:space:]]*$ ]]; then
            socket="${BASH_REMATCH[3]}"
            session="${BASH_REMATCH[4]}"
            if [ "$socket" = "$INNER_SOCKET" ] && \
                tmux -S "$INNER_SOCKET" has-session -t "=$session" 2>/dev/null; then
                printf '%s\t%s\t%s\n' "$pid" "$session" "$command"
            fi
        elif [[ "$command" =~ (^|/)(tmux)[[:space:]]+-C[[:space:]]+attach-session[[:space:]]+-f[[:space:]]+ignore-size[[:space:]]+-t[[:space:]]+([^[:space:]]+)[[:space:]]*$ ]]; then
            session="${BASH_REMATCH[3]}"
            # The command has no -L/-S override, so its inherited TMUX_TMPDIR
            # determines the socket only when TMUX is unset/empty. If TMUX is
            # present, its first field is the socket identity tmux actually
            # uses and must name the same private inner socket.
            process_env=$(ps eww -p "$pid" -o command= 2>/dev/null || true)
            process_tmux=""
            if [[ "$process_env" =~ (^|[[:space:]])TMUX=([^[:space:]]*) ]]; then
                process_tmux="${BASH_REMATCH[2]}"
            fi
            if [[ " $process_env " == *" TMUX_TMPDIR=$TMUX_TMPDIR "* ]] && \
                { [ -z "$process_tmux" ] || [ "${process_tmux%%,*}" = "$INNER_SOCKET" ]; } && \
                tmux -S "$INNER_SOCKET" has-session -t "=$session" 2>/dev/null; then
                printf '%s\t%s\t%s\n' "$pid" "$session" "$command"
            fi
        fi
    done <<<"$descendants"
}

control_kill() {
    local requested="${1:-}" matches pid session command count=0
    case "$requested" in
        *[!0-9]*|"") echo "usage: $0 control-kill PID" >&2; return 1 ;;
    esac
    matches=$(control_clients) || return 1
    while IFS=$'\t' read -r pid session command; do
        [ -n "$pid" ] || continue
        if [ "$pid" = "$requested" ]; then
            count=$((count + 1))
        fi
    done <<<"$matches"
    if [ "$count" -ne 1 ]; then
        echo "refusing PID $requested: expected exactly one verified descendant control client, found $count" >&2
        return 1
    fi
    kill -TERM "$requested"
    echo "signaled verified control client $requested"
}

capture_hook_install() {
    mkdir -p "$CAPTURE_DIR"
    : > "$CAPTURE_LOG"
    printf '%s\n' '#!/bin/bash' \
        '[[ "${1:-}" =~ ^%[0-9]+$ ]] || exit 1' \
        "printf '%s\\n' \"\$1\" >> '$CAPTURE_LOG'" > "$CAPTURE_HELPER"
    chmod 700 "$CAPTURE_HELPER"
    tmux -S "$INNER_SOCKET" set-hook -g after-capture-pane \
        "run-shell -b '$CAPTURE_HELPER #{pane_id}'"
    echo "$CAPTURE_LOG"
}

capture_hook_reset() {
    tmux -S "$INNER_SOCKET" show-hooks -g after-capture-pane >/dev/null 2>&1 || {
        echo "capture hook is not installed on the inner server" >&2
        return 1
    }
    [ -x "$CAPTURE_HELPER" ] || {
        echo "capture hook helper is missing from $RUN_DIR" >&2
        return 1
    }
    : > "$CAPTURE_LOG"
}

capture_hook_show() {
    [ -f "$CAPTURE_LOG" ] || {
        echo "capture hook log does not exist; run capture-hook-install first" >&2
        return 1
    }
    cat "$CAPTURE_LOG"
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
validate_derived_paths

case "${1:-}" in
    start) shift; start "$@" ;;
    keys)  shift; "${T[@]}" send-keys -t "$SESSION" "$@" ;;
    type)  shift; "${T[@]}" send-keys -t "$SESSION" -l "$*" ;;
    snap)  shift; snap "${1:-}" ;;
    panes) panes ;;
    inner-keys) shift; inner_keys "$@" ;;
    inner-type) shift; inner_type "$@" ;;
    control-clients) control_clients ;;
    control-kill) shift; control_kill "${1:-}" ;;
    capture-hook-install) capture_hook_install ;;
    capture-hook-reset) capture_hook_reset ;;
    capture-hook-show) capture_hook_show ;;
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
