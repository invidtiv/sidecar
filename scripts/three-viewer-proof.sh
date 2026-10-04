#!/usr/bin/env bash
# Real TUI + built sidecar-ui/SDK/element + native v1 SSH-stdio protocol peer.
# All processes, tmux servers, authority and shell bytes belong to this run.
set -euo pipefail
unset TMUX TMUX_PANE SIDECAR_SHELL SIDECAR_SHELL_NAME SIDECAR_MANAGED_SHELL SIDECAR_TMUX_SERVER SIDECAR_HOST SIDECAR_VIEWER_INSTANCE
repo=$(cd "$(dirname "$0")/.." && pwd -P)
ui=${SIDECAR_UI_REPO:-$HOME/code/sidecar-ui}
for tool in go git timeout tmux node pnpm python3; do
    command -v "$tool" >/dev/null || { echo "three-viewer-proof: install $tool first" >&2; exit 2; }
done
test -f "$ui/package.json" || { echo "three-viewer-proof: set SIDECAR_UI_REPO to sidecar-ui main" >&2; exit 2; }
root=$(mktemp -d /tmp/sc-three.XXXXXX)
root=$(cd "$root" && pwd -P)
export SIDECAR_DRIVE_RUN_DIR="$root" SIDECAR_DRIVE_REPO="$root/project" SIDECAR_BIN="$root/sidecar"
unset SIDECAR_DRIVE_OUT SIDECAR_DRIVE_ARGS SIDECAR_DRIVE_COMMAND
source "$repo/scripts/proof-tmux-env.sh"
proof_tmux_env "$root/tmux"
export XDG_STATE_HOME="$root/state" XDG_CACHE_HOME="$root/cache" SIDECAR_ISOLATED_STATE=1
server_pid=""
stop_api() {
    if [ -n "$server_pid" ]; then
        kill -TERM "$server_pid" 2>/dev/null || true
        for _ in $(seq 1 100); do kill -0 "$server_pid" 2>/dev/null || break; sleep .1; done
        if kill -0 "$server_pid" 2>/dev/null; then kill -KILL "$server_pid" 2>/dev/null || true; fi
        wait "$server_pid" 2>/dev/null || true
        server_pid=""
    fi
}
cleanup() {
    status=$?
    trap - EXIT INT TERM
    stop_api
    timeout 15 "$repo/scripts/tmux-drive.sh" stop >/dev/null 2>&1 || true
    if [ -n "${THREE_VIEWER_OUTPUT:-}" ]; then
        mkdir -p "$THREE_VIEWER_OUTPUT"
        cp -R "$root/out/." "$THREE_VIEWER_OUTPUT/" 2>/dev/null || true
        cp "$root/config/debug.log" "$THREE_VIEWER_OUTPUT/debug.log" 2>/dev/null || true
    fi
    rm -rf "$root"
    exit "$status"
}
trap cleanup EXIT INT TERM
mkdir -p "$root/config" "$root/project" "$root/out"
git -C "$root/project" init -q -b main
printf '{"projects":{"list":[{"name":"proof","path":"%s"}]},"plugins":{"td-monitor":{"enabled":false},"git-status":{"enabled":false},"file-browser":{"enabled":false},"notes":{"enabled":false},"workspace":{"autoCreateShell":false}}}\n' "$root/project" > "$root/config/config.json"
config="$root/config/config.json"
timeout 180 go build -o "$root/sidecar" "$repo/cmd/sidecar"
timeout 180 go build -o "$root/ios" "$repo/internal/tools/threeviewer"
test "$(git -C "$ui" branch --show-current)" = main || { echo 'sidecar-ui must be on main' >&2; exit 1; }
if ! (cd "$ui" && timeout 180 pnpm build) > "$root/out/ui-build.log" 2>&1; then
    cat "$root/out/ui-build.log" >&2
    exit 1
fi
timeout 15 "$root/sidecar" -config "$config" create shell --project "$root/project" --name 'Three viewer proof' --run "python3 -u '$repo/scripts/three-viewer/terminal.py' sink '$root/received'" --json --wait 0 > "$root/created.json"
session=$(node -e 'console.log(JSON.parse(require("fs").readFileSync(process.argv[1])).shell.session)' "$root/created.json")
timeout 15 "$repo/scripts/tmux-drive.sh" paths
timeout 15 "$repo/scripts/tmux-drive.sh" start 160 44
socket="$TMUX_TMPDIR/tmux-$(id -u)/default"
outer="$TMUX_TMPDIR/tmux-$(id -u)/sidecar-drive"
env -u TMUX -u TMUX_PANE timeout 5 tmux -S "$outer" set-option -g focus-events on
timeout 240 "$root/sidecar" -config "$config" api serve --port 0 --ui "$ui/apps/sidecar-ui/build" --json > "$root/out/serve.json" 2> "$root/out/serve.err" &
server_pid=$!
for _ in $(seq 1 100); do test -s "$root/out/serve.json" && break; sleep .1; done
test -s "$root/out/serve.json"
timeout 15 "$root/sidecar" -config "$config" api open --print > "$root/pair-url"
timeout 240 node "$repo/scripts/three-viewer/proof.mjs" "$root" "$repo" "$ui" "$session" "$socket" "$outer"
stop_api
test ! -e "$root/state/sidecar/api/endpoint.json"
echo 'three-viewer-proof: PASS'
