#!/usr/bin/env bash
# Sidecar worktree setup hook: runs after `git worktree add`, cwd = the new
# worktree, with MAIN_WORKTREE/WORKTREE_PATH/WORKTREE_BRANCH in the env
# (see internal/workspaceops/setup.go).
#
# Gives every new worktree a go.work so Go tooling resolves modules there
# without GOWORK gymnastics. The worktree may be based on a ref that predates
# scripts/worktree-init.sh, so fall back to the main checkout's copy.
set -euo pipefail

init="scripts/worktree-init.sh"
if [ ! -f "$init" ]; then
    init="${MAIN_WORKTREE:?}/scripts/worktree-init.sh"
fi
if [ ! -f "$init" ]; then
    echo "worktree-setup: scripts/worktree-init.sh not found; skipping" >&2
    exit 0
fi
bash "$init"
