#!/bin/bash
# Common helpers and environment setup for Sidecar demo environments.
set -euo pipefail

log_info() {
    printf '\033[1;34m==>\033[0m \033[1m%s\033[0m\n' "$*" >&2
}

log_success() {
    printf '\033[1;32m✓\033[0m %s\n' "$*" >&2
}

log_warn() {
    printf '\033[1;33m▲\033[0m %s\n' "$*" >&2
}

log_error() {
    printf '\033[1;31m✗\033[0m %s\n' "$*" >&2
}

die() {
    log_error "$*"
    exit 1
}

# Create a clean, canonicalized temp directory for the demo run
create_demo_root() {
    local preset="${1:-demo}"
    local raw_root
    raw_root=$(mktemp -d "/tmp/sidecar-demo-${preset}.XXXXXX")
    
    # Canonicalize to eliminate symlink traversal
    DEMO_ROOT="$(cd "$raw_root" && pwd -P)"
    
    DEMO_PROJECTS_DIR="$DEMO_ROOT/projects"
    DEMO_STATE_DIR="$DEMO_ROOT/state"
    DEMO_CACHE_DIR="$DEMO_ROOT/cache"
    DEMO_DATA_DIR="$DEMO_ROOT/data"
    DEMO_CONFIG_DIR="$DEMO_ROOT/config"
    DEMO_TMUX_DIR="$DEMO_ROOT/tmux"
    DEMO_TASKS_DIR="$DEMO_ROOT/tasks"
    DEMO_BIN_DIR="$DEMO_ROOT/bin"
    DEMO_LOG_DIR="$DEMO_ROOT/log"

    mkdir -p "$DEMO_PROJECTS_DIR" "$DEMO_STATE_DIR" "$DEMO_CACHE_DIR" "$DEMO_DATA_DIR" \
             "$DEMO_CONFIG_DIR" "$DEMO_TMUX_DIR" "$DEMO_TASKS_DIR" "$DEMO_BIN_DIR" "$DEMO_LOG_DIR"

    CONFIG_PATH="$DEMO_CONFIG_DIR/config.json"
    INNER_TMUX_SOCKET="$DEMO_TMUX_DIR/tmux-$(id -u)/default"
}

# Apply Sidecar's strict isolation variables
export_isolation_env() {
    export TMUX_TMPDIR="$DEMO_TMUX_DIR"
    export XDG_STATE_HOME="$DEMO_STATE_DIR"
    export XDG_CACHE_HOME="$DEMO_CACHE_DIR"
    export XDG_DATA_HOME="$DEMO_DATA_DIR"
    export TASKS_DIR="$DEMO_TASKS_DIR"
    export SIDECAR_ISOLATED_STATE=1
    
    # Clear any outer host TMUX selector so inner sessions are isolated
    unset TMUX
}
