#!/bin/bash
# loopback-remote.sh - Portable loopback remote for agents.
#
# Two isolated Sidecar trees on this machine, joined by scripts/loopback-ssh.sh.
# No named workstation, no live ~/.local/state/sidecar, no default tmux server.
#
#   ./scripts/loopback-remote.sh up [--delay 40ms] [--no-drive]
#   ./scripts/loopback-remote.sh paths
#   ./scripts/loopback-remote.sh status
#   ./scripts/loopback-remote.sh down
#
# Environment:
#   LOOPBACK_RUN_DIR  run root (default: /tmp/sidecar-loopback-$USER)
#                     must match /tmp/sidecar-loopback* — teardown deletes it

set -euo pipefail

# Never let an attached tmux client select a server implicitly.
unset TMUX

# An agent runs this from a live Sidecar pane. Those identity variables name
# the CALLER's shell and tmux server; leaking them into the private host
# server or the viewer TUI is how a fixture would talk to the wrong pane.
# Keep SIDECAR_BIN / SIDECAR_ISOLATED_STATE / SIDECAR_DRIVE_* /
# SIDECAR_LOOPBACK_* — those are harness levers we set below.
unset SIDECAR_SHELL SIDECAR_SHELL_NAME SIDECAR_MANAGED_SHELL \
    SIDECAR_NAMESPACE SIDECAR_TMUX_SERVER SIDECAR_HOST SIDECAR_PANE

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
DRIVE="$REPO_DIR/scripts/tmux-drive.sh"
LOOPBACK_SSH="$REPO_DIR/scripts/loopback-ssh.sh"
BASE_PATH="$PATH"
UID_NUM="$(id -u)"
RAW_ROOT="${LOOPBACK_RUN_DIR:-/tmp/sidecar-loopback-$(id -un)}"

refuse_run_dir() {
    echo "refusing LOOPBACK_RUN_DIR='$RAW_ROOT': $1" >&2
    exit 1
}

assert_loopback_prefix() {
    local dir="$1"
    case "$dir" in
        /tmp/sidecar-loopback*|/private/tmp/sidecar-loopback*) ;;
        *) refuse_run_dir "must be /tmp/sidecar-loopback* — teardown deletes this path recursively" ;;
    esac
}

assert_run_dir_shape() {
    local dir="$1"
    case "$dir" in
        /*) ;;
        *) refuse_run_dir "must be an absolute path" ;;
    esac
    case "$dir" in
        */)     refuse_run_dir "a trailing slash makes the temp root itself the target" ;;
        *//*)   refuse_run_dir "empty path component" ;;
    esac
    case "/$dir/" in
        */../*|*/./*) refuse_run_dir "dot and dotdot components are not allowed" ;;
    esac
    case "$dir" in
        *[!A-Za-z0-9_./-]*) refuse_run_dir "unsupported characters" ;;
    esac
    assert_loopback_prefix "$dir"
}

canonicalize_run_dir() {
    local parent base canonical_parent canonical_run
    assert_run_dir_shape "$RAW_ROOT"
    parent=$(dirname "$RAW_ROOT")
    base=$(basename "$RAW_ROOT")
    if [ -d "$parent" ]; then
        canonical_parent=$(cd "$parent" && pwd -P)
        canonical_run="$canonical_parent/$base"
    else
        canonical_run="$RAW_ROOT"
    fi
    if [ -e "$RAW_ROOT" ]; then
        [ -d "$RAW_ROOT" ] || refuse_run_dir "not a directory"
        canonical_run=$(cd "$RAW_ROOT" && pwd -P)
        [ "$canonical_run" = "$canonical_parent/$base" ] || refuse_run_dir "symlink traversal is not allowed"
    fi
    assert_run_dir_shape "$canonical_run"
    ROOT="$canonical_run"
}

canonicalize_run_dir

HOST="$ROOT/host"
VIEWER="$ROOT/viewer"
BIN="$ROOT/sidecar"
HOST_HOME="$HOST/home"
HOST_STATE="$HOST/state"
HOST_CONFIG_DIR="$HOST/config"
HOST_CONFIG="$HOST_CONFIG_DIR/config.json"
HOST_TMUX_TMPDIR="$HOST/tmux"
HOST_SOCKET="$HOST_TMUX_TMPDIR/tmux-$UID_NUM/default"
HOST_PROJECT="$HOST/project"
VIEWER_CONFIG="$VIEWER/config/config.json"
VIEWER_STATE="$VIEWER/state"
VIEWER_TMUX_TMPDIR="$VIEWER/tmux"
VIEWER_INNER_SOCKET="$VIEWER_TMUX_TMPDIR/tmux-$UID_NUM/default"
VIEWER_DRIVE_SOCKET="$VIEWER_TMUX_TMPDIR/tmux-$UID_NUM/sidecar-drive"
VIEWER_PROJECT="$VIEWER/project"
FAKE_SSH="$VIEWER/bin/ssh"
HOLD_SESSION="sidecar-sh-loopback-hold"
PLAIN_SESSION="sidecar-sh-loopback-plain"

real_state_root() { printf '%s' "${HOME}/.local/state/sidecar"; }
real_config_root() { printf '%s' "${HOME}/.config/sidecar"; }

path_is_under() {
    local path="$1" root="$2"
    case "$path/" in
        "$root"|"$root"/*) return 0 ;;
    esac
    return 1
}

assert_isolated_roots() {
    local real_state real_config p
    real_state="$(real_state_root)"
    real_config="$(real_config_root)"
    for p in "$ROOT" "$BIN" "$FAKE_SSH" \
        "$HOST" "$HOST_HOME" "$HOST_STATE" "$HOST_CONFIG" "$HOST_TMUX_TMPDIR" "$HOST_SOCKET" "$HOST_PROJECT" \
        "$VIEWER" "$VIEWER_CONFIG" "$VIEWER_STATE" "$VIEWER_TMUX_TMPDIR" "$VIEWER_INNER_SOCKET" "$VIEWER_DRIVE_SOCKET" "$VIEWER_PROJECT"; do
        if path_is_under "$p" "$real_state" || path_is_under "$p" "$real_config"; then
            echo "refusing: path $p is under the real Sidecar tree" >&2
            exit 1
        fi
        case "$p" in
            "$ROOT"|"$ROOT"/*) ;;
            *)
                echo "refusing: path $p is outside the loopback run root $ROOT" >&2
                exit 1
                ;;
        esac
    done
}

parse_go_duration_seconds() {
    local d="$1" out
    out=$(awk -v s="$d" 'BEGIN {
        if (s == "") { print "0"; exit 0 }
        if (s ~ /^[0-9]+(\.[0-9]+)?$/) { print s; exit 0 }
        u["ns"]=1e-9; u["us"]=1e-6; u["µs"]=1e-6; u["μs"]=1e-6
        u["ms"]=1e-3; u["s"]=1; u["m"]=60; u["h"]=3600
        total=0
        while (length(s) > 0) {
            if (match(s, /^[0-9]+(\.[0-9]+)?(ns|us|µs|μs|ms|s|m|h)/) == 0) {
                exit 2
            }
            tok = substr(s, RSTART, RLENGTH)
            s = substr(s, RSTART+RLENGTH)
            match(tok, /^[0-9]+(\.[0-9]+)?/)
            n = substr(tok, RSTART, RLENGTH) + 0
            unit = substr(tok, RSTART+RLENGTH)
            total += n * u[unit]
        }
        printf "%.9f\n", total
    }') || {
        echo "invalid --delay '$d' (want a Go duration such as 40ms or 1s)" >&2
        exit 2
    }
    printf '%s\n' "$out"
}

host_path() {
    local tmux_bin git_bin
    tmux_bin=$(command -v tmux) || { echo "tmux is required on PATH" >&2; exit 1; }
    git_bin=$(command -v git) || { echo "git is required on PATH" >&2; exit 1; }
    printf '%s:%s:/usr/bin:/bin:%s' "$(dirname "$tmux_bin")" "$(dirname "$git_bin")" "$BASE_PATH"
}

json_write() {
    python3 - "$@" <<'PY'
import json, sys
kind = sys.argv[1]
if kind == "host-config":
    path, project = sys.argv[2], sys.argv[3]
    cfg = {
        "features": {"flags": {}},
        "projects": {
            "mode": "single",
            "root": ".",
            "list": [{"name": "Loopback", "path": project}],
        },
    }
elif kind == "viewer-config":
    path, binary, host_config, home, state, tmux_tmpdir, host_path, viewer_project = sys.argv[2:]
    cfg = {
        "features": {"flags": {"sidecar_remote_hosts": True, "cross_project_overview": True}},
        "hosts": {"list": [{
            "id": "loopback",
            "target": "loopback",
            "binary": binary,
            "config": host_config,
            "env": [
                "HOME=" + home,
                "XDG_STATE_HOME=" + state,
                "TMUX_TMPDIR=" + tmux_tmpdir,
                "TMUX=",
                "TMUX_PANE=",
                "SIDECAR_ISOLATED_STATE=1",
                "PATH=" + host_path,
            ],
        }]},
        "projects": {
            "mode": "single",
            "root": ".",
            "list": [{"name": "Loopback", "path": viewer_project}],
        },
    }
elif kind == "meta":
    path, project = sys.argv[2], sys.argv[3]
    cfg = {"path": project}
elif kind == "shells":
    path, hold, plain, namespace, workdir = sys.argv[2:]
    cfg = {
        "version": 2,
        "shells": [
            {"tmuxName": hold, "displayName": "loopback hold", "namespace": namespace, "workDir": workdir},
            {"tmuxName": plain, "displayName": "loopback plain", "namespace": namespace, "workDir": workdir},
        ],
    }
else:
    raise SystemExit("unknown json kind %s" % kind)
open(path, "w").write(json.dumps(cfg, indent=2) + "\n")
PY
}

plant_git_project() {
    local dir="$1" marker="$2" message="$3"
    mkdir -p "$dir"
    printf '%s\n' "$marker" > "$dir/twin.txt"
    if [ ! -d "$dir/.git" ]; then
        git -C "$dir" init -q
        git -C "$dir" config user.email loopback@example.com
        git -C "$dir" config user.name Loopback
        git -C "$dir" add twin.txt
        git -C "$dir" commit -qm "$message"
    fi
}

# td on PATH with no .todos opens the "Set up td" modal on the first TUI
# frame, which blocks @ / W. Match demo.sh: init when td exists, skip if not.
init_td_if_available() {
    local dir="$1"
    command -v td >/dev/null 2>&1 || return 0
    (
        cd "$dir"
        printf '\n' | td init >/dev/null 2>&1
    ) || true
}

plant_host_worktree() {
    local main="$1" linked="$2"
    mkdir -p "$(dirname "$linked")"
    if [ -d "$linked/.git" ] || [ -f "$linked/.git" ]; then
        return 0
    fi
    git -C "$main" worktree add -b feature "$linked" >/dev/null 2>&1
}

create_host_session() {
    local name="$1" out
    if TMUX= tmux -S "$HOST_SOCKET" has-session -t "$name" 2>/dev/null; then
        return 0
    fi
    # The client's environment becomes the server's. Address the private
    # socket by path so an empty TMUX_TMPDIR cannot fall back to default.
    set +e
    out=$(TMUX= HOME="$HOST_HOME" XDG_STATE_HOME="$HOST_STATE" \
        TMUX_TMPDIR="$HOST_TMUX_TMPDIR" PATH="$HOST_PATH" \
        SIDECAR_ISOLATED_STATE=1 TMUX_PANE= \
        tmux -S "$HOST_SOCKET" new-session -d -s "$name" -c "$HOST_PROJECT_CANON" \
        -x 80 -y 24 'read _hold' 2>&1)
    status=$?
    set -e
    case "$out" in
        *error*) status=1 ;;
    esac
    if [ "$status" -ne 0 ]; then
        echo "create host session $name: $out" >&2
        exit 1
    fi
}

write_viewer_env() {
    local delay_sec="${1:-}"
    {
        printf 'SIDECAR_DRIVE_RUN_DIR=%q\n' "$VIEWER"
        printf 'SIDECAR_BIN=%q\n' "$BIN"
        printf 'SIDECAR_DRIVE_REPO=%q\n' "$VIEWER_PROJECT_CANON"
        printf 'PATH=%q\n' "$FAKE_SSH_PATH"
        printf 'SIDECAR_ISOLATED_STATE=1\n'
        if [ -n "$delay_sec" ]; then
            printf 'SIDECAR_LOOPBACK_SSH_DELAY=%q\n' "$delay_sec"
        fi
    } > "$ROOT/viewer.env"
}

drive_prefix() {
    local delay_sec=""
    if [ -f "$ROOT/delay.seconds" ]; then
        delay_sec=$(cat "$ROOT/delay.seconds")
    fi
    printf 'SIDECAR_DRIVE_RUN_DIR=%q SIDECAR_BIN=%q SIDECAR_DRIVE_REPO=%q PATH=%q SIDECAR_ISOLATED_STATE=1' \
        "$VIEWER" "$BIN" "${VIEWER_PROJECT_CANON:-$VIEWER_PROJECT}" "$VIEWER/bin:$BASE_PATH"
    if [ -n "$delay_sec" ]; then
        printf ' SIDECAR_LOOPBACK_SSH_DELAY=%q' "$delay_sec"
    fi
}

cmd_paths() {
    assert_isolated_roots
    local host_project="$HOST_PROJECT"
    local viewer_project="$VIEWER_PROJECT"
    if [ -d "$HOST_PROJECT" ]; then
        host_project=$(cd "$HOST_PROJECT" && pwd -P)
    fi
    if [ -d "$VIEWER_PROJECT" ]; then
        viewer_project=$(cd "$VIEWER_PROJECT" && pwd -P)
    fi
    echo "run root:          $ROOT"
    echo "binary:            $BIN"
    echo "fake ssh:          $FAKE_SSH"
    echo
    echo "host:"
    echo "  home:            $HOST_HOME"
    echo "  config:          $HOST_CONFIG"
    echo "  XDG_STATE_HOME:  $HOST_STATE"
    echo "  TMUX_TMPDIR:     $HOST_TMUX_TMPDIR"
    echo "  tmux socket:     $HOST_SOCKET"
    echo "  project:         $host_project"
    echo
    echo "viewer:"
    echo "  config:          $VIEWER_CONFIG"
    echo "  XDG_STATE_HOME:  $VIEWER_STATE"
    echo "  TMUX_TMPDIR:     $VIEWER_TMUX_TMPDIR"
    echo "  inner socket:    $VIEWER_INNER_SOCKET"
    echo "  drive socket:    $VIEWER_DRIVE_SOCKET"
    echo "  project:         $viewer_project"
    echo
    echo "these must NOT appear as run roots: ~/.local/state/sidecar, ~/.config/sidecar"
    if [ -d "$VIEWER" ] && [ -d "$viewer_project" ]; then
        echo
        echo "viewer (tmux-drive):"
        SIDECAR_DRIVE_RUN_DIR="$VIEWER" SIDECAR_DRIVE_REPO="$viewer_project" \
            SIDECAR_BIN="$BIN" "$DRIVE" paths | sed 's/^/  /'
    fi
}

cmd_status() {
    assert_isolated_roots
    local host_state="down" viewer_state="not running" n=0
    if [ -S "$HOST_SOCKET" ]; then
        n=$(TMUX= tmux -S "$HOST_SOCKET" list-sessions 2>/dev/null | wc -l | tr -d '[:space:]')
        if [ "${n:-0}" -gt 0 ]; then
            host_state="up ($n sessions)"
        else
            host_state="socket present, no sessions"
        fi
    fi
    if [ -S "$VIEWER_DRIVE_SOCKET" ] && TMUX= tmux -S "$VIEWER_DRIVE_SOCKET" has-session -t host 2>/dev/null; then
        viewer_state="running"
    elif [ -f "$ROOT/no-drive" ]; then
        viewer_state="not running (--no-drive)"
    fi
    echo "host tmux:     $host_state"
    echo "viewer drive:  $viewer_state"
    echo "run root:      $ROOT"
    echo
    echo "drive commands:"
    echo "  $(drive_prefix) $DRIVE keys"
    echo "  $(drive_prefix) $DRIVE snap"
    if [ -S "$HOST_SOCKET" ]; then
        echo
        echo "host sessions:"
        TMUX= tmux -S "$HOST_SOCKET" list-sessions 2>/dev/null | sed 's/^/  /' || true
    fi
}

cmd_down() {
    # Stop the viewer drive first, while the viewer tree still exists.
    # tmux-drive stop addresses only sockets under SIDECAR_DRIVE_RUN_DIR.
    # Skip when --no-drive never started one: `tmux -S <missing> kill-server`
    # is the command we must not issue against a path we do not own.
    if [ -d "$VIEWER" ] && { [ -S "$VIEWER_DRIVE_SOCKET" ] || [ ! -f "$ROOT/no-drive" ]; }; then
        SIDECAR_DRIVE_RUN_DIR="$VIEWER" SIDECAR_BIN="${BIN:-sidecar}" \
            SIDECAR_DRIVE_REPO="${REPO_DIR}" \
            "$DRIVE" stop >/dev/null 2>&1 || true
    fi
    if [ -S "$HOST_SOCKET" ]; then
        case "$HOST_SOCKET" in
            "$ROOT"/*)
                echo "killing ONLY the private host server at $HOST_SOCKET"
                TMUX= tmux -S "$HOST_SOCKET" kill-server 2>/dev/null || true
                ;;
            *)
                echo "refusing down: host socket '$HOST_SOCKET' is outside $ROOT" >&2
                exit 1
                ;;
        esac
    fi
    if [ -d "$ROOT" ]; then
        case "$ROOT" in
            /tmp/sidecar-loopback*|/private/tmp/sidecar-loopback*)
                rm -rf "$ROOT"
                ;;
            *)
                echo "refusing to delete $ROOT" >&2
                exit 1
                ;;
        esac
    fi
    echo "loopback down"
}

cmd_up() {
    local delay="" delay_sec="" no_drive=0
    while [ $# -gt 0 ]; do
        case "$1" in
            --delay)
                delay="${2:?--delay requires a Go duration such as 40ms or 1s}"
                shift 2
                ;;
            --delay=*)
                delay="${1#--delay=}"
                shift
                ;;
            --no-drive)
                no_drive=1
                shift
                ;;
            *)
                echo "unknown flag: $1" >&2
                echo "usage: $0 up [--delay 40ms] [--no-drive]" >&2
                exit 2
                ;;
        esac
    done
    if [ -n "$delay" ]; then
        delay_sec=$(parse_go_duration_seconds "$delay")
    fi

    command -v tmux >/dev/null || { echo "tmux is required on PATH" >&2; exit 1; }
    command -v git >/dev/null || { echo "git is required on PATH" >&2; exit 1; }
    command -v python3 >/dev/null || { echo "python3 is required to write JSON configs" >&2; exit 1; }
    [ -f "$LOOPBACK_SSH" ] || { echo "missing $LOOPBACK_SSH" >&2; exit 1; }

    if [ -d "$ROOT" ]; then
        echo "replacing existing loopback at $ROOT"
        cmd_down
    fi

    assert_isolated_roots
    mkdir -p "$HOST_HOME" "$HOST_STATE" "$HOST_CONFIG_DIR" "$HOST_TMUX_TMPDIR" \
        "$(dirname "$HOST_SOCKET")" "$(dirname "$VIEWER_CONFIG")" "$VIEWER/bin"
    chmod 700 "$HOST_TMUX_TMPDIR" "$(dirname "$HOST_SOCKET")"

    echo "building sidecar into $BIN"
    (cd "$REPO_DIR" && go build -o "$BIN" ./cmd/sidecar)
    chmod +x "$BIN"

    plant_git_project "$HOST_PROJECT" "REMOTE-MARKER" "host remote marker"
    plant_git_project "$VIEWER_PROJECT" "LOCAL-TWIN" "viewer local twin"
    HOST_PROJECT_CANON=$(cd "$HOST_PROJECT" && pwd -P)
    VIEWER_PROJECT_CANON=$(cd "$VIEWER_PROJECT" && pwd -P)
    plant_host_worktree "$HOST_PROJECT_CANON" "$HOST/worktrees/feature"
    init_td_if_available "$HOST_PROJECT_CANON"
    init_td_if_available "$VIEWER_PROJECT_CANON"

    HOST_PATH=$(host_path)
    FAKE_SSH_PATH="$VIEWER/bin:$BASE_PATH"

    json_write host-config "$HOST_CONFIG" "$HOST_PROJECT_CANON"
    json_write viewer-config "$VIEWER_CONFIG" "$BIN" "$HOST_CONFIG" \
        "$HOST_HOME" "$HOST_STATE" "$HOST_TMUX_TMPDIR" "$HOST_PATH" "$VIEWER_PROJECT_CANON"

    local state_project="$HOST_STATE/sidecar/projects/loopback"
    mkdir -p "$HOST_STATE/sidecar/projects"
    find "$HOST_STATE/sidecar/projects" -mindepth 1 -maxdepth 1 -type d ! -name loopback -exec rm -rf {} + 2>/dev/null || true
    mkdir -p "$state_project"
    json_write meta "$state_project/meta.json" "$HOST_PROJECT_CANON"
    json_write shells "$state_project/shells.json" "$HOLD_SESSION" "$PLAIN_SESSION" \
        "$HOST_SOCKET" "$HOST_PROJECT_CANON"

    cp "$LOOPBACK_SSH" "$FAKE_SSH"
    chmod 700 "$FAKE_SSH"
    if [ -n "$delay" ]; then
        printf '%s\n' "$delay" > "$ROOT/delay"
        printf '%s\n' "$delay_sec" > "$ROOT/delay.seconds"
    fi
    write_viewer_env "$delay_sec"

    create_host_session "$HOLD_SESSION"
    create_host_session "$PLAIN_SESSION"

    if [ "$no_drive" -eq 1 ]; then
        touch "$ROOT/no-drive"
        echo "loopback up --no-drive at $ROOT"
    else
        PATH="$FAKE_SSH_PATH" \
        SIDECAR_DRIVE_RUN_DIR="$VIEWER" \
        SIDECAR_BIN="$BIN" \
        SIDECAR_DRIVE_REPO="$VIEWER_PROJECT_CANON" \
        SIDECAR_LOOPBACK_SSH_DELAY="${delay_sec:-}" \
        SIDECAR_ISOLATED_STATE=1 \
            "$DRIVE" start
        echo "loopback up at $ROOT"
    fi
    cmd_paths
}

case "${1:-}" in
    up)     shift; cmd_up "$@" ;;
    paths)  shift; cmd_paths "$@" ;;
    status) shift; cmd_status "$@" ;;
    down)   shift; cmd_down "$@" ;;
    *) sed -n '2,16p' "$0"; exit 2 ;;
esac
