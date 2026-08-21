#!/bin/bash
# demo.sh - Launch isolated, interactive Sidecar demo environments.
#
# Stand up a 100% isolated, disposable Sidecar environment with custom sample
# projects (Intersections, Plastic Pieces, Avocet, Synthwave Studio, Quantum Kitchen),
# per-project themes, and configurable toolchains (TD, Tasks, Notes).
#
# Usage:
#   ./scripts/demo.sh [PRESET] [OPTIONS]
#
# Presets:
#   multi               (Default) 5 themed projects with project switcher enabled
#   single              1 project (Intersections, or choose with --project=<name>)
#   fresh               Clean slate (0 projects) for testing new-user onboarding
#
# Options:
#   -p, --project=NAME  Specific project for 'single' mode (intersections, plastic-pieces, avocet, synthwave, quantum)
#   --blank             Create repositories without sample commits/files
#   --git / --no-git    In 'fresh' mode, initialize a clean Git repo vs non-Git directory (default: --git)
#   --no-td             Disable TD and mask 'td' binary to simulate missing TD
#   --no-tasks          Disable Tasks plugin and mask 'tasks' binaries
#   --no-notes          Disable Notes plugin
#   --onboarding        Shortcut for --no-td --no-tasks (clean new-user experience)
#   --keep              Preserve the temporary /tmp directory on exit (don't delete)
#   --dry-run           Generate the demo files and print paths without launching Sidecar
#   --bin=PATH          Path to custom sidecar executable (defaults to fresh build from active checkout)
#   -h, --help          Show this help message
#
# Examples:
#   ./scripts/demo.sh                                # 5 projects with project switcher
#   ./scripts/demo.sh single                         # Single traffic sim project
#   ./scripts/demo.sh single -p plastic-pieces       # 3D printing project
#   ./scripts/demo.sh fresh --no-tasks               # Clean Git repo with TD installed, no tasks, 0 projects
#   ./scripts/demo.sh fresh --no-git --onboarding    # Non-Git directory with 0 dependencies

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# Source modular components
# shellcheck source=demo/common.sh
source "$SCRIPT_DIR/demo/common.sh"
# shellcheck source=demo/config.sh
source "$SCRIPT_DIR/demo/config.sh"
# shellcheck source=demo/projects/common.sh
source "$SCRIPT_DIR/demo/projects/common.sh"
# shellcheck source=demo/projects/intersections.sh
source "$SCRIPT_DIR/demo/projects/intersections.sh"
# shellcheck source=demo/projects/plastic_pieces.sh
source "$SCRIPT_DIR/demo/projects/plastic_pieces.sh"
# shellcheck source=demo/projects/avocet.sh
source "$SCRIPT_DIR/demo/projects/avocet.sh"
# shellcheck source=demo/projects/synthwave_studio.sh
source "$SCRIPT_DIR/demo/projects/synthwave_studio.sh"
# shellcheck source=demo/projects/quantum_kitchen.sh
source "$SCRIPT_DIR/demo/projects/quantum_kitchen.sh"
# shellcheck source=demo/presets/fresh.sh
source "$SCRIPT_DIR/demo/presets/fresh.sh"
# shellcheck source=demo/presets/single.sh
source "$SCRIPT_DIR/demo/presets/single.sh"
# shellcheck source=demo/presets/multi.sh
source "$SCRIPT_DIR/demo/presets/multi.sh"
# shellcheck source=demo/tools/td.sh
source "$SCRIPT_DIR/demo/tools/td.sh"
# shellcheck source=demo/tools/tasks.sh
source "$SCRIPT_DIR/demo/tools/tasks.sh"
# shellcheck source=demo/launcher.sh
source "$SCRIPT_DIR/demo/launcher.sh"

show_help() {
    sed -n '2,34p' "$0" | sed 's/^# \?//'
}

# Defaults
PRESET="multi"
SELECTED_PROJECT="intersections"
BLANK=0
ENABLE_GIT=1
ENABLE_TD=1
ENABLE_TASKS=1
ENABLE_NOTES=1
KEEP=0
DRY_RUN=0
CUSTOM_BIN=""

# Parse positional preset and flags
while [ "$#" -gt 0 ]; do
    case "$1" in
        multi|single|fresh)
            PRESET="$1"
            shift
            ;;
        -p|--project)
            [ "$#" -ge 2 ] || die "--project requires a project name"
            SELECTED_PROJECT="$2"
            shift 2
            ;;
        --project=*)
            SELECTED_PROJECT="${1#*=}"
            shift
            ;;
        --blank)
            BLANK=1
            shift
            ;;
        --git)
            ENABLE_GIT=1
            shift
            ;;
        --no-git)
            ENABLE_GIT=0
            shift
            ;;
        --no-td)
            ENABLE_TD=0
            shift
            ;;
        --no-tasks)
            ENABLE_TASKS=0
            shift
            ;;
        --no-notes)
            ENABLE_NOTES=0
            shift
            ;;
        --onboarding)
            ENABLE_TD=0
            ENABLE_TASKS=0
            shift
            ;;
        --keep)
            KEEP=1
            shift
            ;;
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        --bin=*)
            CUSTOM_BIN="${1#*=}"
            shift
            ;;
        -h|--help)
            show_help
            exit 0
            ;;
        *)
            log_error "Unknown argument: $1"
            show_help
            exit 1
            ;;
    esac
done

# Initialize demo scratch directory
create_demo_root "$PRESET"

# Resolve or build Sidecar binary
SIDECAR_BIN="${CUSTOM_BIN:-}"
if [ -z "$SIDECAR_BIN" ]; then
    short_commit=$(git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo "dev")
    branch=$(git -C "$REPO_DIR" branch --show-current 2>/dev/null || echo "main")
    dirty=""
    [ -z "$(git -C "$REPO_DIR" status --porcelain 2>/dev/null)" ] || dirty="+dirty"
    demo_version="devel+${branch}.${short_commit}${dirty}"

    log_info "Building fresh Sidecar binary from active checkout ($demo_version)..."
    (
        cd "$REPO_DIR"
        GOWORK=off go build -ldflags "-s -w -X main.Version=$demo_version" -o "$DEMO_BIN_DIR/sidecar" ./cmd/sidecar
    )
    SIDECAR_BIN="$DEMO_BIN_DIR/sidecar"
fi

if [ ! -x "$SIDECAR_BIN" ]; then
    die "Sidecar binary not executable at: $SIDECAR_BIN"
fi

# Dispatch preset setup
case "$PRESET" in
    fresh)
        setup_preset_fresh "$DEMO_ROOT" "$CONFIG_PATH" \
            "$ENABLE_TD" "$ENABLE_TASKS" "$ENABLE_NOTES" "$BLANK" "$ENABLE_GIT"
        ;;
    single)
        setup_preset_single "$DEMO_ROOT" "$CONFIG_PATH" \
            "$ENABLE_TD" "$ENABLE_TASKS" "$ENABLE_NOTES" "$BLANK" "$SELECTED_PROJECT"
        ;;
    multi)
        setup_preset_multi "$DEMO_ROOT" "$CONFIG_PATH" \
            "$ENABLE_TD" "$ENABLE_TASKS" "$ENABLE_NOTES" "$BLANK"
        ;;
esac

# Launch environment
launch_demo "$SIDECAR_BIN" "$DRY_RUN" "$KEEP" "$ENABLE_TD" "$ENABLE_TASKS"
