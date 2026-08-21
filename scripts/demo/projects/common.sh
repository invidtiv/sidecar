#!/bin/bash
# Shared git repository and worktree generator helpers.
set -euo pipefail

init_git_repo() {
    local dir="$1"
    local name="$2"

    mkdir -p "$dir"
    git -C "$dir" init -q -b main
    git -C "$dir" config user.name "Demo Engineer"
    git -C "$dir" config user.email "engineer@sidecar.demo"
    git -C "$dir" config commit.gpgsign false
}

git_commit_all() {
    local dir="$1"
    local msg="$2"

    git -C "$dir" add -A
    git -C "$dir" commit -q -m "$msg"
}

add_worktree() {
    local repo_dir="$1"
    local wt_dir="$2"
    local branch="$3"

    mkdir -p "$(dirname "$wt_dir")"
    git -C "$repo_dir" worktree add -q -b "$branch" "$wt_dir"
}
