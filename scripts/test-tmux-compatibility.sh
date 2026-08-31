#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd "$SCRIPT_DIR/.." && pwd)
# shellcheck source=tmux-compat-lib.sh
source "$SCRIPT_DIR/tmux-compat-lib.sh"

usage() {
    cat <<'EOF'
Usage: ./scripts/test-tmux-compatibility.sh ROLE TMUX_BIN [--server-role ROLE --server-tmux TMUX_BIN]

Without --server-tmux, runs the Sidecar compatibility suite with same-version
private servers. With it, runs an upgrade-skew smoke using TMUX_BIN as the
client and --server-tmux as the binary that starts the private server.
EOF
}

if [[ $# -lt 2 ]]; then
    usage >&2
    exit 2
fi
client_role=$1
client_bin=$2
shift 2
server_role=''
server_bin=''
while [[ $# -gt 0 ]]; do
    case "$1" in
        --server-role) server_role=${2:-}; shift 2 ;;
        --server-tmux) server_bin=${2:-}; shift 2 ;;
        *) tmux_compat_error "unknown argument $1"; usage >&2; exit 2 ;;
    esac
done
if [[ -n "$server_role" || -n "$server_bin" ]]; then
    if [[ -z "$server_role" || -z "$server_bin" ]]; then
        tmux_compat_error "--server-role and --server-tmux must be supplied together"
        exit 2
    fi
fi

IFS=$'\t' read -r _ client_release _ < <(tmux_compat_row "$client_role")
expected_client="tmux $client_release"
if [[ ! -x "$client_bin" ]]; then
    tmux_compat_error "tmux binary is not executable: $client_bin"
    exit 1
fi
actual_client=$($client_bin -V)
if [[ "$actual_client" != "$expected_client" ]]; then
    tmux_compat_error "$client_bin reports $actual_client, expected $expected_client"
    exit 1
fi

# macOS per-user TMPDIR values are long enough to exceed the Unix socket path
# limit once tmux adds tmux-$UID/default. /tmp is the conventional short,
# private-parent root used by Sidecar's other tmux integration harnesses.
compat_tmp_root=${SIDECAR_TMUX_COMPAT_TMPDIR:-/tmp}
case "$compat_tmp_root" in
    /*) ;;
    *) tmux_compat_error "SIDECAR_TMUX_COMPAT_TMPDIR must be absolute"; exit 2 ;;
esac
run_root=$(mktemp -d "$compat_tmp_root/sidecar-tmux-compat.XXXXXX")
export TMUX_TMPDIR="$run_root/tmux"
unset TMUX TMUX_PANE
mkdir -p "$TMUX_TMPDIR"
chmod 700 "$TMUX_TMPDIR"
socket="$TMUX_TMPDIR/tmux-$(id -u)/default"
mkdir -p "$(dirname "$socket")"
chmod 700 "$(dirname "$socket")"
cleanup_bin=$client_bin
control_pid=''
if [[ -n "$server_bin" ]]; then
    cleanup_bin=$server_bin
fi

cleanup() {
    if [[ -n "$control_pid" ]] && kill -0 "$control_pid" >/dev/null 2>&1; then
        kill "$control_pid" >/dev/null 2>&1 || true
        wait "$control_pid" >/dev/null 2>&1 || true
    fi
    if [[ -S "$socket" ]]; then
        # In skew mode teardown must not depend on the very client/server
        # negotiation under test. The binary that started the private server
        # can always address its own explicit socket.
        "$cleanup_bin" -S "$socket" kill-server >/dev/null 2>&1 || true
    fi
    case "$run_root" in
        "$compat_tmp_root"/sidecar-tmux-compat.*) rm -rf -- "$run_root" ;;
    esac
}
trap cleanup EXIT INT TERM

client_dir=$(cd "$(dirname "$client_bin")" && pwd)
export PATH="$client_dir:$PATH"
if [[ $(command -v tmux) != "$client_dir/tmux" || $(tmux -V) != "$expected_client" ]]; then
    tmux_compat_error "PATH did not resolve the selected $expected_client binary"
    exit 1
fi

smoke_same_version() {
    local session=sidecar-compat-same
    "$client_bin" -f /dev/null new-session -d -s "$session" -x 90 -y 30 -- sh -c 'printf READY; exec sh'
    local server_version
    server_version=$($client_bin display-message -p -t "$session" '#{version}')
    if [[ "$server_version" != "$client_release" ]]; then
        tmux_compat_error "private server reports $server_version, expected $client_release"
        exit 1
    fi
    "$client_bin" send-keys -t "$session" -l -- 'SIDECAR_COMPAT_LITERAL'
    "$client_bin" send-keys -t "$session" Enter
    local captured=''
    for _ in $(seq 1 100); do
        captured=$($client_bin capture-pane -p -t "$session")
        [[ "$captured" == *SIDECAR_COMPAT_LITERAL* ]] && break
        sleep 0.05
    done
    [[ "$captured" == *SIDECAR_COMPAT_LITERAL* ]] || {
        tmux_compat_error "private pane did not capture literal input"
        exit 1
    }
    "$client_bin" resize-window -t "$session" -x 100 -y 35
    local metadata
    metadata=$($client_bin list-panes -t "$session" -F $'#{pane_id}\t#{pane_width}\t#{pane_height}\t#{cursor_x}\t#{cursor_y}')
    [[ "$metadata" == *$'\t100\t35\t'* ]] || {
        tmux_compat_error "unexpected pane metadata after resize: $metadata"
        exit 1
    }
    printf 'same-version private smoke passed: client=%s server=tmux %s socket=%s\n' \
        "$actual_client" "$server_version" "$socket"
    "$client_bin" -S "$socket" kill-server
}

smoke_upgrade_skew() {
    IFS=$'\t' read -r _ server_release _ < <(tmux_compat_row "$server_role")
    local expected_server="tmux $server_release"
    local actual_server
    actual_server=$($server_bin -V)
    if [[ "$actual_server" != "$expected_server" ]]; then
        tmux_compat_error "$server_bin reports $actual_server, expected $expected_server"
        exit 1
    fi
    if [[ "$client_release" == "$server_release" ]]; then
        tmux_compat_error "upgrade-skew proof requires different client and server releases"
        exit 1
    fi

    local session=sidecar-compat-skew
    "$server_bin" -f /dev/null -S "$socket" new-session -d -s "$session" -x 90 -y 30 -- sh -c 'printf READY; exec sh'
    local observed_server
    observed_server=$($client_bin -S "$socket" display-message -p -t "$session" '#{version}')
    if [[ "$observed_server" != "$server_release" ]]; then
        tmux_compat_error "latest client observed server $observed_server, expected $server_release"
        exit 1
    fi
    "$client_bin" -S "$socket" send-keys -t "$session" -l -- 'SIDECAR_UPGRADE_SKEW_LITERAL'
    "$client_bin" -S "$socket" send-keys -t "$session" Enter
    local captured=''
    for _ in $(seq 1 100); do
        captured=$($client_bin -S "$socket" capture-pane -p -t "$session")
        [[ "$captured" == *SIDECAR_UPGRADE_SKEW_LITERAL* ]] && break
        sleep 0.05
    done
    [[ "$captured" == *SIDECAR_UPGRADE_SKEW_LITERAL* ]] || {
        tmux_compat_error "latest client could not capture literal input from the minimum server"
        exit 1
    }
    "$client_bin" -S "$socket" resize-window -t "$session" -x 100 -y 35
    local metadata
    metadata=$($client_bin -S "$socket" list-panes -t "$session" -F $'#{pane_id}\t#{pane_width}\t#{pane_height}\t#{cursor_x}\t#{cursor_y}')
    [[ "$metadata" == *$'\t100\t35\t'* ]] || {
        tmux_compat_error "unexpected skew pane metadata after resize: $metadata"
        exit 1
    }

    local control_log="$run_root/control.log"
    local control_error="$run_root/control.err"
    local control_input="$run_root/control.input"
    mkfifo "$control_input"
    "$client_bin" -S "$socket" -C attach-session -f ignore-size -t "$session" <"$control_input" >"$control_log" 2>"$control_error" &
    control_pid=$!
    exec 3>"$control_input"
    printf 'display-message -p "#{version}"\n' >&3
    printf 'send-keys -t %s -l -- SIDECAR_SKEW_CONTROL\n' "$session" >&3
    printf 'send-keys -t %s Enter\n' "$session" >&3

    local control_result=''
    for _ in $(seq 1 100); do
        if grep -q '^%begin ' "$control_log" && grep -q '^%output ' "$control_log"; then
            control_result=notifications
            break
        fi
        if grep -q '^%exit' "$control_log"; then
            # tmux may decline a control-mode attach across protocol versions.
            # Sidecar already treats a dead control channel as a signal to use
            # its capture fallback; the CLI capture above proves that degraded
            # path remains available while the old server is still alive.
            control_result=declined
            break
        fi
        if ! kill -0 "$control_pid" >/dev/null 2>&1; then
            break
        fi
        sleep 0.05
    done
    exec 3>&-
    if kill -0 "$control_pid" >/dev/null 2>&1; then
        kill "$control_pid"
    fi
    wait "$control_pid" >/dev/null 2>&1 || true
    control_pid=''
    # Some tmux versions flush the final %exit only while the client process
    # is being reaped, so classify once more after the bounded wait/stop.
    if [[ -z "$control_result" ]]; then
        if grep -q '^%begin ' "$control_log" && grep -q '^%output ' "$control_log"; then
            control_result=notifications
        elif grep -q '^%exit' "$control_log"; then
            control_result=declined
        fi
    fi
    if [[ -z "$control_result" ]]; then
        sed -n '1,80p' "$control_log" >&2
        sed -n '1,80p' "$control_error" >&2
        tmux_compat_error "upgrade-skew control client neither attached nor declined explicitly within 5 seconds"
        exit 1
    fi
    "$client_bin" -S "$socket" send-keys -t "$session" -l -- 'SIDECAR_POST_CONTROL_FALLBACK'
    "$client_bin" -S "$socket" send-keys -t "$session" Enter
    captured=''
    for _ in $(seq 1 100); do
        captured=$($client_bin -S "$socket" capture-pane -p -t "$session")
        [[ "$captured" == *SIDECAR_POST_CONTROL_FALLBACK* ]] && break
        sleep 0.05
    done
    [[ "$captured" == *SIDECAR_POST_CONTROL_FALLBACK* ]] || {
        tmux_compat_error "capture fallback did not recover terminal content after the skewed control attempt"
        exit 1
    }
    printf 'upgrade-skew private smoke passed: client=%s server=%s control=%s socket=%s\n' \
        "$actual_client" "$actual_server" "$control_result" "$socket"
}

if [[ -n "$server_bin" ]]; then
    smoke_upgrade_skew
    exit 0
fi

smoke_same_version
export SIDECAR_REQUIRE_TMUX_COMPAT=1
cd "$REPO_ROOT"

# Keep tmux from reading the developer's ~/.tmux.conf when package tests start
# their own private servers. Preserve Go's already-resolved caches so changing
# HOME does not turn the compatibility proof into a dependency download.
export GOCACHE
GOCACHE=$(go env GOCACHE)
export GOMODCACHE
GOMODCACHE=$(go env GOMODCACHE)
export GOPATH
GOPATH=$(go env GOPATH)
mkdir -p "$run_root/home"
export HOME="$run_root/home"

go test ./internal/tty -run '^(TestControlManagerIsolatedTmuxSessionPool|TestControlBarrierUnsetsLeaseBeforeLastClientCloses|TestModelAttachMidStreamLosesNoBytes|TestModelResizeReseedsAndLosesNoBytes|TestModelPauseContinueForcesReseedAndStaysContinuous|TestModelReconnectFallsBackThenReseeds|TestSeedTransactionHalvesAreNotInterleaved|TestSendPasteToTmuxBracketedAndPlain|TestSendPasteToTmuxCleansUpAfterFailure|TestPrepareServerAdvertisesTruecolor)$' -count=1
go test ./internal/plugins/workspace -run '^TestLiveTerminalCursor' -count=1
go test ./internal/tmuxserver -run '^TestListSessionsPIDFormatResolves$' -count=1
go test ./internal/termnotify -run '^TestTmux' -count=1
go test ./internal/workspaceops -run '^TestDeleteWorktreeKillsTheWorktreeSession$' -count=1

printf 'Sidecar tmux compatibility suite passed with %s\n' "$actual_client"
