#!/bin/bash
# Configuration generator for Sidecar demo environments.
set -euo pipefail

generate_demo_config() {
    local config_path="$1"
    local projects_json="$2"
    local main_theme="${3:-sidecar-modern}"
    local enable_notes="${4:-1}"
    local enable_tasks="${5:-1}"
    local enable_overview="${6:-1}"

    local notes_bool="true"
    local tasks_bool="true"
    local overview_bool="true"

    [ "$enable_notes" -eq 1 ] || notes_bool="false"
    [ "$enable_tasks" -eq 1 ] || tasks_bool="false"
    [ "$enable_overview" -eq 1 ] || overview_bool="false"

    mkdir -p "$(dirname "$config_path")"

    cat > "$config_path" <<JSON
{
  "projects": {
    "mode": "single",
    "root": ".",
    "list": $projects_json
  },
  "plugins": {
    "git-status": {
      "enabled": true,
      "refreshInterval": 1000000000
    },
    "td-monitor": {
      "enabled": true,
      "refreshInterval": 5000000000,
      "dbPath": ".todos/issues.db"
    },
    "file-browser": {
      "enabled": true
    },
    "conversations": {
      "enabled": true
    },
    "workspace": {
      "dirPrefix": true,
      "autoCreateShell": true
    },
    "notes": {
      "defaultEditor": "builtin"
    }
  },
  "ui": {
    "showClock": true,
    "nerdFontsEnabled": true,
    "theme": {
      "name": "$main_theme"
    }
  },
  "features": {
    "flags": {
      "notes_plugin": $notes_bool,
      "tasks_plugin": $tasks_bool,
      "cross_project_overview": $overview_bool,
      "workspace_doc_panes": true,
      "files_auto_refresh": true,
      "tmux_inline_edit": true
    }
  }
}
JSON

    log_success "Generated isolated config at: $config_path"
}
