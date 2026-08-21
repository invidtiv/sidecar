#!/bin/bash
# Preset: Single Project (Intersections)
set -euo pipefail

setup_preset_single() {
    local demo_root="$1"
    local config_file="$2"
    local enable_td="${3:-1}"
    local enable_tasks="${4:-1}"
    local enable_notes="${5:-1}"
    local blank="${6:-0}"
    local selected_project="${7:-intersections}"

    log_info "Setting up preset: 'single' (project: $selected_project)..."

    local project_dir=""
    local project_name=""
    local project_theme="sidecar-modern"

    case "$selected_project" in
        "intersections")
            project_dir=$(create_project_intersections "$demo_root/projects" "$blank")
            project_name="Intersections"
            project_theme="sidecar-modern"
            ;;
        "plastic-pieces"|"plastic_pieces")
            project_dir=$(create_project_plastic_pieces "$demo_root/projects" "$blank")
            project_name="Plastic Pieces"
            project_theme="tokyonight-storm"
            ;;
        "avocet"|"have-a-set"|"have_a_set")
            project_dir=$(create_project_avocet "$demo_root/projects" "$blank")
            project_name="Avocet"
            project_theme="kanagawa-wave"
            ;;
        "synthwave"|"synthwave-studio"|"synthwave_studio")
            project_dir=$(create_project_synthwave_studio "$demo_root/projects" "$blank")
            project_name="Synthwave Studio"
            project_theme="synthwave"
            ;;
        "quantum"|"quantum-kitchen"|"quantum_kitchen")
            project_dir=$(create_project_quantum_kitchen "$demo_root/projects" "$blank")
            project_name="Quantum Kitchen"
            project_theme="catppuccin-mocha"
            ;;
        *)
            die "Unknown project: $selected_project"
            ;;
    esac

    setup_td_for_project "$project_dir" "$project_name" "$enable_td"

    local projects_json
    projects_json=$(cat <<JSON
[
  {
    "name": "$project_name",
    "path": "$project_dir",
    "theme": { "name": "$project_theme" }
  }
]
JSON
)

    generate_demo_config "$config_file" "$projects_json" "$project_theme" \
        "$enable_notes" "$enable_tasks" "1"

    LAUNCH_PROJECT_DIR="$project_dir"
}
