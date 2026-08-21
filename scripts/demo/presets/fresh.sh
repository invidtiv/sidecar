#!/bin/bash
# Preset: Fresh Onboarding (Clean slate, zero projects, first-run state)
set -euo pipefail

setup_preset_fresh() {
    local demo_root="$1"
    local config_file="$2"
    local enable_td="${3:-1}"
    local enable_tasks="${4:-1}"
    local enable_notes="${5:-1}"
    local blank="${6:-0}"
    local enable_git="${7:-1}"

    log_info "Setting up preset: 'fresh' (clean onboarding slate)..."

    # In fresh mode, projects list is empty
    local projects_json="[]"

    generate_demo_config "$config_file" "$projects_json" "sidecar-modern" \
        "$enable_notes" "$enable_tasks" "1"

    if [ "$enable_git" -eq 1 ]; then
        local repo_dir="$demo_root/repo"
        init_git_repo "$repo_dir" "fresh-repo"
        if [ "$blank" -eq 0 ]; then
            cat > "$repo_dir/README.md" <<'EOF'
# Sample Project

A newly initialized Git repository without Sidecar configuration.
EOF
            git_commit_all "$repo_dir" "Initial commit"
        fi
        LAUNCH_PROJECT_DIR="$repo_dir"
    else
        # Plain non-git directory (for testing non-git onboarding)
        LAUNCH_PROJECT_DIR="$demo_root"
    fi
}
