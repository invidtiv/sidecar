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

    log_info "Setting up preset: 'fresh' (clean onboarding slate)..."

    # In fresh mode, projects list is empty
    local projects_json="[]"

    generate_demo_config "$config_file" "$projects_json" "sidecar-modern" \
        "$enable_notes" "$enable_tasks" "1"

    # Target directory to launch Sidecar in (empty demo root)
    LAUNCH_PROJECT_DIR="$demo_root"
}
