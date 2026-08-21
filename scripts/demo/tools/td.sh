#!/bin/bash
# Tool integration: TD (Task & Note management)
set -euo pipefail

# setup_td_for_project PROJECT_DIR PROJECT_NAME
setup_td_for_project() {
    local project_dir="$1"
    local project_name="$2"
    local enabled="${3:-1}"

    if [ "$enabled" -ne 1 ]; then
        return 0
    fi

    # Only run td commands if td is actually installed on the host
    if command -v td >/dev/null 2>&1; then
        (
            cd "$project_dir"
            # Initialize td in this project root if not already initialized
            td init -q 2>/dev/null || true

            case "$project_name" in
                "Intersections")
                    td create "Tune yellow light timing for 45mph arterials" -q 2>/dev/null || true
                    td create "Fix vehicle deadlock at 4-way unsignalized intersection" -q 2>/dev/null || true
                    td note add "Simulation Run Notes" -q 2>/dev/null || true
                    ;;
                "Plastic Pieces")
                    td create "Verify clearance tolerances for PETG shrinkage on M3 threaded inserts" -q 2>/dev/null || true
                    td create "Calibrate pressure advance K-factor for 0.6mm nozzle" -q 2>/dev/null || true
                    td note add "Filament Calibration Notes" -q 2>/dev/null || true
                    ;;
                "Avocet")
                    td create "Filter low-frequency marsh wind noise in 100-300Hz band" -q 2>/dev/null || true
                    td create "Optimize battery sleep cycle for solar listener array 07" -q 2>/dev/null || true
                    td note add "Station 4 Observations" -q 2>/dev/null || true
                    ;;
                "Synthwave Studio")
                    td create "SIMD vectorize ladder filter inner loop for ARM NEON" -q 2>/dev/null || true
                    td create "Fix voice stealing click on fast 16th-note arpeggiations" -q 2>/dev/null || true
                    td note add "Synth Patch Architecture" -q 2>/dev/null || true
                    ;;
                "Quantum Kitchen")
                    td create "Calculate calcium bath pH buffer for high-acidity passionfruit sphere" -q 2>/dev/null || true
                    td create "Benchmark thermal diffusivity constant for wagyu striploin" -q 2>/dev/null || true
                    td note add "Lab Prep Protocol" -q 2>/dev/null || true
                    ;;
            esac
        )
    fi
}
