#!/usr/bin/env bash
# Make a git worktree self-sufficient for Go tooling.
#
# The main checkout may carry an untracked go.work (multi-module dev against
# sibling checkouts). Go finds the nearest go.work walking UP from the current
# directory, so a worktree under .claude/worktrees/ inherits the main
# checkout's file — which does not list the worktree, and every go command
# fails with "directory ... does not contain modules listed in go.work".
#
# Fix: write a go.work inside the worktree that shadows the parent's. `.`
# becomes the worktree itself; every other entry from the parent file is
# resolved to an absolute path so sibling modules keep working. go.work is
# gitignored, so the generated file never dirties the tree.
#
# Idempotent; run from anywhere inside a worktree (any harness, or by hand):
#   make worktree-init
set -euo pipefail

root=$(git rev-parse --show-toplevel)
common=$(cd "$(git rev-parse --git-common-dir)" && pwd)
main=$(dirname "$common")

if [ "$root" = "$main" ]; then
    echo "worktree-init: $root is the main checkout; nothing to do"
    exit 0
fi

parent_work="$main/go.work"
if [ ! -f "$parent_work" ]; then
    echo "worktree-init: no go.work at $main; module resolution already correct"
    exit 0
fi

target="$root/go.work"
tmp=$(mktemp)
trap 'rm -f "$tmp"' EXIT

{
    grep '^go ' "$parent_work"
    echo
    echo "use ("
    echo $'\t.'
    # Re-emit the parent's use entries (both block and single-line forms),
    # resolving each against the main checkout; `.` is already covered above.
    grep -oE '(^use[[:space:]]+|^[[:space:]]+)(\.\.?/[^[:space:])]+|/[^[:space:])]+)' "$parent_work" \
        | awk '{print $NF}' \
        | while IFS= read -r entry; do
            abs=$(cd "$main" && cd "$entry" 2>/dev/null && pwd) || {
                echo "worktree-init: skipping missing module $entry (from $parent_work)" >&2
                continue
            }
            [ "$abs" = "$main" ] && continue
            printf '\t%s\n' "$abs"
        done
    echo ")"
} > "$tmp"

if [ -f "$target" ] && cmp -s "$tmp" "$target"; then
    echo "worktree-init: $target already up to date"
    exit 0
fi

cp "$tmp" "$target"
echo "worktree-init: wrote $target"
sed 's/^/  /' "$target"
