#!/bin/bash
# Tool integration: Tasks plugin and binaries
set -euo pipefail

configure_tasks_environment() {
    local enable_tasks="$1"
    local bin_dir="$2"

    if [ "$enable_tasks" -eq 0 ]; then
        log_info "Tasks disabled: masking tasks suite binaries from PATH for onboarding simulation"
        mkdir -p "$bin_dir"
        for cmd in tasks tasks-tui tasks-api; do
            cat > "$bin_dir/$cmd" <<EOF
#!/bin/sh
echo "$cmd: command not found (demo onboarding mode)" >&2
exit 127
EOF
            chmod +x "$bin_dir/$cmd"
        done
    fi
}
