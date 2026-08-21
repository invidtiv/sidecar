#!/bin/bash
# Preset: Multi-Project (All 5 Projects with Per-Project Themes)
set -euo pipefail

setup_preset_multi() {
    local demo_root="$1"
    local config_file="$2"
    local enable_td="${3:-1}"
    local enable_tasks="${4:-1}"
    local enable_notes="${5:-1}"
    local blank="${6:-0}"

    log_info "Setting up preset: 'multi' (5 themed projects with project switcher)..."

    # Build all 5 sample projects
    local p1_dir p2_dir p3_dir p4_dir p5_dir
    p1_dir=$(create_project_intersections "$demo_root/projects" "$blank")
    p2_dir=$(create_project_plastic_pieces "$demo_root/projects" "$blank")
    p3_dir=$(create_project_avocet "$demo_root/projects" "$blank")
    p4_dir=$(create_project_synthwave_studio "$demo_root/projects" "$blank")
    p5_dir=$(create_project_quantum_kitchen "$demo_root/projects" "$blank")

    # Set up TD in each project if enabled
    setup_td_for_project "$p1_dir" "Intersections" "$enable_td"
    setup_td_for_project "$p2_dir" "Plastic Pieces" "$enable_td"
    setup_td_for_project "$p3_dir" "Avocet" "$enable_td"
    setup_td_for_project "$p4_dir" "Synthwave Studio" "$enable_td"
    setup_td_for_project "$p5_dir" "Quantum Kitchen" "$enable_td"

    # Generate projects list with per-project themes
    local projects_json
    projects_json=$(cat <<JSON
[
  {
    "name": "Intersections",
    "path": "$p1_dir",
    "theme": { "name": "sidecar-modern" }
  },
  {
    "name": "Plastic Pieces",
    "path": "$p2_dir",
    "theme": { "name": "tokyonight-storm" }
  },
  {
    "name": "Avocet",
    "path": "$p3_dir",
    "theme": { "name": "kanagawa-wave" }
  },
  {
    "name": "Synthwave Studio",
    "path": "$p4_dir",
    "theme": { "name": "synthwave" }
  },
  {
    "name": "Quantum Kitchen",
    "path": "$p5_dir",
    "theme": { "name": "catppuccin-mocha" }
  }
]
JSON
)

    # Main default theme is sidecar-modern
    generate_demo_config "$config_file" "$projects_json" "sidecar-modern" \
        "$enable_notes" "$enable_tasks" "1"

    # Default landing project is Intersections
    LAUNCH_PROJECT_DIR="$p1_dir"
}
