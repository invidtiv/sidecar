#!/bin/bash
# Build one disposable OpenCode-shaped workspace on tmux-drive's private
# socket. Call tmux-drive.sh paths with the same DRIVE_ROOT before this script.
set -euo pipefail

if [ "$#" -ne 2 ]; then
    echo "usage: $0 FIXTURE_ROOT DRIVE_ROOT" >&2
    exit 2
fi

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
FIXTURE_ROOT="$1"
DRIVE_ROOT="$2"

"$REPO_DIR/scripts/cross-project-overview-fixture.sh" "$FIXTURE_ROOT" "$DRIVE_ROOT" 1 >/dev/null

# Match the canonical paths persisted by the base fixture. On macOS /tmp is a
# symlink to /private/tmp; mixing those spellings survives the first inventory
# but can lose the configured project on the next refresh.
FIXTURE_ROOT="$(cd "$FIXTURE_ROOT" && pwd -P)"
DRIVE_ROOT="$(cd "$DRIVE_ROOT" && pwd -P)"

BIN="$FIXTURE_ROOT/bin/opencode"
WORKTREE="$FIXTURE_ROOT/projects/agents-01/agent-01"
SOCKET="$DRIVE_ROOT/tmux/tmux-$(id -u)/default"
SESSION="sidecar-ws-agent-01"

case "$BIN" in "$FIXTURE_ROOT"/*) ;; *) echo "fixture binary escaped fixture root" >&2; exit 1 ;; esac
case "$WORKTREE" in "$FIXTURE_ROOT"/*) ;; *) echo "fixture worktree escaped fixture root" >&2; exit 1 ;; esac
case "$SOCKET" in "$DRIVE_ROOT"/*) ;; *) echo "fixture socket escaped drive root" >&2; exit 1 ;; esac
[ -S "$SOCKET" ] || { echo "private tmux socket not found: $SOCKET" >&2; exit 1; }

agent_files=("$DRIVE_ROOT"/state/sidecar/projects/project-01/worktrees/*/agent)
if [ "${#agent_files[@]}" -ne 1 ] || [ ! -f "${agent_files[0]}" ]; then
    echo "expected exactly one recorded fixture agent" >&2
    exit 1
fi
printf '%s\n' opencode > "${agent_files[0]}"

# Keep unrelated plugins out of the CPU floor and avoid setup prompts. The
# global Sessions projection remains enabled through the existing overview
# feature, with the same one-project inventory the base fixture created.
cat > "$DRIVE_ROOT/config/config.json" <<JSON
{
  "projects": {"mode":"single","root":".","list":[{"name":"OpenCode fixture","path":"$FIXTURE_ROOT/projects/project-01"}]},
  "plugins": {
    "td-monitor":{"enabled":false},
    "git-status":{"enabled":false},
    "file-browser":{"enabled":false},
    "conversations":{"enabled":false},
    "workspace":{"agents":["opencode"],"tmuxCaptureMaxBytes":1048576,"autoCreateShell":false}
  },
  "features":{"flags":{"cross_project_overview":true}},
  "ui":{"showClock":false,"nerdFontsEnabled":false,"theme":{"name":"default"}}
}
JSON

mkdir -p "$WORKTREE/internal/runtime" "$WORKTREE/docs"
printf '%s\n' 'synthetic fixture file' > "$WORKTREE/internal/runtime/frame.go"
printf '%s\n' '# Synthetic terminal performance fixture' > "$WORKTREE/docs/terminal.md"

(
    cd "$REPO_DIR"
    go build -o "$BIN" ./internal/testfixture/terminal/cmd/opencode
)

# The cross-project fixture created this exact session on the explicit private
# socket. Recreate it once so its inner pane ID cannot equal tmux-drive's outer
# host pane ID (%0 on both fresh servers); inventory deliberately filters the
# host ID, and equal IDs across servers would otherwise hide the only fixture.
tmux -S "$SOCKET" has-session -t "=$SESSION"
KEEPER_SESSION="sidecar-terminal-perf-keeper"
cleanup_keeper() {
    tmux -S "$SOCKET" kill-session -t "=$KEEPER_SESSION" 2>/dev/null || true
}
trap cleanup_keeper EXIT
tmux -S "$SOCKET" new-session -d -s "$KEEPER_SESSION" "sleep 300"
tmux -S "$SOCKET" kill-session -t "=$SESSION"
tmux -S "$SOCKET" new-session -d -s "$SESSION" -c "$WORKTREE" "exec '$BIN'"
tmux -S "$SOCKET" select-pane -t "=$SESSION:" -T "OpenCode performance fixture"
cleanup_keeper
trap - EXIT

printf 'fixture root: %s\n' "$FIXTURE_ROOT"
printf 'drive root:   %s\n' "$DRIVE_ROOT"
printf 'launch repo:  %s\n' "$FIXTURE_ROOT/projects/project-01"
printf 'worktree:     %s\n' "$WORKTREE"
printf 'inner socket: %s\n' "$SOCKET"
printf 'session:      %s\n' "$SESSION"
printf 'interval:     8ms\n'
