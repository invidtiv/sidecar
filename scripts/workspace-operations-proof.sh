#!/bin/bash
# Prove the shared mutation core through CLI, project Workspaces, and Sessions.
# Both tmux and Sidecar state are private; tmux-drive owns cleanup of each.
set -euo pipefail
unset TMUX TMUX_PANE SIDECAR_SHELL SIDECAR_SHELL_NAME SIDECAR_TMUX_SERVER
repo=$(cd "$(dirname "$0")/.." && pwd -P)
root=$(mktemp -d /tmp/sc-ops.XXXXXX)
root=$(cd "$root" && pwd -P)
export SIDECAR_DRIVE_RUN_DIR="$root"
export SIDECAR_DRIVE_REPO="$root/project"
export SIDECAR_BIN="$root/sidecar"
unset SIDECAR_DRIVE_OUT SIDECAR_DRIVE_ARGS SIDECAR_DRIVE_COMMAND
driver="$repo/scripts/tmux-drive.sh"
cleanup() {
    status=$?
    "$driver" stop >/dev/null 2>&1 || true
    if [ -n "${WORKSPACE_PROOF_OUTPUT:-}" ] && [ -d "$root/out" ]; then
        mkdir -p "$WORKSPACE_PROOF_OUTPUT"
        cp "$root/out/"* "$WORKSPACE_PROOF_OUTPUT/"
    fi
    rm -rf "$root"
    exit "$status"
}
trap cleanup EXIT INT TERM
mkdir -p "$root/config" "$root/project"
git -C "$root/project" init -q -b main
git -C "$root/project" -c user.name=Proof -c user.email=proof@local commit --allow-empty -qm seed
printf '{"projects":{"list":[{"name":"proof","path":"%s"}]},"plugins":{"td-monitor":{"enabled":false},"git-status":{"enabled":false},"file-browser":{"enabled":false},"notes":{"enabled":false},"workspace":{"autoCreateShell":false,"worktreeSetup":{"copyEnvFiles":false,"runHook":false}}}}\n' "$root/project" > "$root/config/config.json"
printf '{"showIdleWorktrees":true}\n' > "$root/config/state.json"
go build -o "$SIDECAR_BIN" "$repo/cmd/sidecar"
"$driver" paths
"$driver" start 160 44
wait_screen() {
    local name=$1 expected=$2
    for _ in $(seq 1 40); do
        "$driver" snap "$name" >/dev/null
        if grep -Fq "$expected" "$root/out/$name.txt"; then return; fi
        sleep 0.1
    done
    cat "$root/out/$name.txt" >&2
    echo "workspace-operations-proof: screen did not show $expected" >&2
    exit 1
}
wait_screen initial 'Workspaces'
"$driver" keys 1
"$driver" keys C-n
wait_screen project-created 'Shell 1'
"$driver" cli shell list --project project --json > "$root/shells.json"
session=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert len(d["shells"])==1; print(d["shells"][0]["shell"])' "$root/shells.json")
"$driver" cli shell rename --project project --target "$session" 'Shared core proof' --json
wait_screen project-renamed 'Shared core proof'
"$driver" keys 8
wait_screen sessions-renamed 'Shared core proof'
"$driver" keys C-n
wait_screen sessions-form 'Create Workspace'
"$driver" keys Enter
wait_screen sessions-created 'Shell 2'
"$driver" cli shell list --project project --json > "$root/shells.json"
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert len(d["shells"])==2, d' "$root/shells.json"
"$driver" cli shell delete --project project --target "$session" --json
"$driver" cli shell restore --project project "$session" --json > "$root/restored.json"
python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["status"]=="restored", d' "$root/restored.json"
"$driver" cli create worktree u2a-feature --project project --plan --json > "$root/plan.json"
"$driver" cli create worktree u2a-feature --project project --no-launch --json --wait 0 > "$root/created.json"
worktree=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["path"])' "$root/created.json")
oid=$(git -C "$worktree" rev-parse HEAD)
"$driver" cli worktree delete "$worktree" --project project --plan --json > "$root/delete-plan.json"
"$driver" cli worktree delete "$worktree" --project project --expect-branch u2a-feature --expect-head-oid "$oid" --yes --json
test ! -d "$worktree"
echo 'workspace-operations-proof: PASS (project create, CLI rename, Sessions create, shell delete/restore, confirmed worktree create/delete)'
