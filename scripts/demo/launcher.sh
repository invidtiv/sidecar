#!/bin/bash
# Demo environment launcher and lifecycle manager.
set -euo pipefail

setup_sanitized_path() {
    local enable_td="$1"
    local enable_tasks="$2"
    local demo_root="$3"

    if [ "$enable_td" -eq 1 ] && [ "$enable_tasks" -eq 1 ]; then
        return 0
    fi

    local mirror_dir="$demo_root/pathmirror"
    mkdir -p "$mirror_dir"

    IFS=':' read -r -a path_dirs <<<"$PATH"
    for d in "${path_dirs[@]}"; do
        [ -d "$d" ] || continue
        for f in "$d"/*; do
            [ -x "$f" ] && [ ! -d "$f" ] || continue
            base="$(basename "$f")"
            if [ "$enable_td" -eq 0 ] && [ "$base" = "td" ]; then
                continue
            fi
            if [ "$enable_tasks" -eq 0 ]; then
                case "$base" in
                    tasks|tasks-tui|tasks-api) continue ;;
                esac
            fi
            [ -e "$mirror_dir/$base" ] || ln -sfn "$f" "$mirror_dir/$base" 2>/dev/null || true
        done
    done

    export PATH="$mirror_dir"
}

launch_demo() {
    local sidecar_bin="$1"
    local dry_run="${2:-0}"
    local keep="${3:-0}"
    local enable_td="${4:-1}"
    local enable_tasks="${5:-1}"

    # Global tracking variables for cleanup trap
    KEEP_DEMO_ROOT="$keep"
    ACTIVE_DEMO_ROOT="$DEMO_ROOT"
    ACTIVE_TMUX_SOCKET="$INNER_TMUX_SOCKET"

    # Sanitize PATH by mirroring and omitting disabled tools (so LookPath genuinely returns ErrNotFound)
    setup_sanitized_path "$enable_td" "$enable_tasks" "$DEMO_ROOT"

    # Export two-axis isolation
    export_isolation_env

    # Cleanup handler
    cleanup() {
        local exit_code=$?
        trap - EXIT INT TERM HUP

        log_info "Tearing down demo environment..."

        # Kill private inner tmux server if active
        if [ -n "${ACTIVE_TMUX_SOCKET:-}" ] && [ -S "$ACTIVE_TMUX_SOCKET" ]; then
            tmux -S "$ACTIVE_TMUX_SOCKET" kill-server >/dev/null 2>&1 || true
        fi

        if [ "${KEEP_DEMO_ROOT:-0}" -eq 1 ]; then
            log_warn "Demo directory preserved at: $ACTIVE_DEMO_ROOT"
        else
            if [ -n "${ACTIVE_DEMO_ROOT:-}" ] && [ -d "$ACTIVE_DEMO_ROOT" ]; then
                rm -rf "$ACTIVE_DEMO_ROOT"
                log_success "Cleaned up temporary demo state."
            fi
        fi

        exit "$exit_code"
    }

    trap cleanup EXIT INT TERM HUP

    # Print summary
    echo ""
    printf "\033[1;36m=====================================================\033[0m\n"
    printf "\033[1;36m              SIDECAR DEMO ENVIRONMENT               \033[0m\n"
    printf "\033[1;36m=====================================================\033[0m\n"
    printf " \033[1mRoot Directory:\033[0m   %s\n" "$DEMO_ROOT"
    printf " \033[1mConfig File:\033[0m      %s\n" "$CONFIG_PATH"
    printf " \033[1mState Tree:\033[0m       %s\n" "$DEMO_STATE_DIR"
    printf " \033[1mPrivate Tmux:\033[0m     %s\n" "$INNER_TMUX_SOCKET"
    printf " \033[1mLaunch Project:\033[0m   %s\n" "$LAUNCH_PROJECT_DIR"
    printf " \033[1mTD Enabled:\033[0m       %s\n" "$([ "$enable_td" -eq 1 ] && echo "yes" || echo "no (omitted from PATH)")"
    printf " \033[1mTasks Plugin:\033[0m     %s\n" "$([ "$enable_tasks" -eq 1 ] && echo "yes" || echo "no (omitted from PATH)")"
    printf " \033[1mKeep on Exit:\033[0m     %s\n" "$([ "$keep" -eq 1 ] && echo "yes" || echo "no (auto-purge)")"
    printf "\033[1;36m=====================================================\033[0m\n"
    echo ""

    if [ "$dry_run" -eq 1 ]; then
        log_success "Dry run complete. Skipping interactive Sidecar launch."
        return 0
    fi

    log_info "Launching Sidecar... (Press 'q' inside Sidecar to exit and tear down)"
    sleep 1

    # Launch Sidecar interactively in current terminal
    (
        cd "$LAUNCH_PROJECT_DIR"
        TERM=xterm-256color "$sidecar_bin" -config "$CONFIG_PATH" -project "$LAUNCH_PROJECT_DIR"
    )
}
