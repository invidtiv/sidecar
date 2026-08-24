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
            # Initialize td in this project root (provide newline for non-interactive agent instructions prompt)
            printf "\n" | td init >/dev/null 2>&1 || true

            local id_in_prog id_open id_closed

            case "$project_name" in
                "Intersections")
                    id_in_prog=$(td create "Fix vehicle deadlock at 4-way unsignalized intersection" --type bug --priority P0 2>/dev/null | awk '/CREATED/ {print $2}')
                    id_open=$(td create "Implement adaptive green wave timing for arterial corridors" --type feature --priority P1 2>/dev/null | awk '/CREATED/ {print $2}')
                    id_closed=$(td create "Add 2D Manhattan grid representation and road geometry" --type chore --priority P2 2>/dev/null | awk '/CREATED/ {print $2}')
                    [ -n "$id_in_prog" ] && td start "$id_in_prog" >/dev/null 2>&1 || true
                    [ -n "$id_closed" ] && td close "$id_closed" -m "Geometry primitives merged" >/dev/null 2>&1 || true
                    td note add "Simulation Benchmark Notes" --content "Downtown grid benchmark runs steady at 60 FPS with 500 active vehicles." >/dev/null 2>&1 || true
                    ;;
                "Plastic Pieces")
                    id_in_prog=$(td create "Verify clearance tolerances for PETG shrinkage on M3 inserts" --type bug --priority P0 2>/dev/null | awk '/CREATED/ {print $2}')
                    id_open=$(td create "Calibrate pressure advance K-factor for 0.6mm high-flow nozzle" --type feature --priority P1 2>/dev/null | awk '/CREATED/ {print $2}')
                    id_closed=$(td create "Add OpenSCAD planetary gear model with tolerance offsets" --type task --priority P2 2>/dev/null | awk '/CREATED/ {print $2}')
                    [ -n "$id_in_prog" ] && td start "$id_in_prog" >/dev/null 2>&1 || true
                    [ -n "$id_closed" ] && td close "$id_closed" -m "Initial model committed" >/dev/null 2>&1 || true
                    td note add "Filament Calibration Notes" --content "Polymaker PLA Pro printed best at 215C with 0.16mm layer height." >/dev/null 2>&1 || true
                    ;;
                "Avocet")
                    id_in_prog=$(td create "Optimize battery sleep cycle for solar listener array 07" --type task --priority P1 2>/dev/null | awk '/CREATED/ {print $2}')
                    id_open=$(td create "Filter low-frequency marsh wind noise in 100-300Hz band" --type feature --priority P1 2>/dev/null | awk '/CREATED/ {print $2}')
                    id_closed=$(td create "Implement real-time Hann-windowed FFT spectrogram generation" --type chore --priority P2 2>/dev/null | awk '/CREATED/ {print $2}')
                    [ -n "$id_in_prog" ] && td start "$id_in_prog" >/dev/null 2>&1 || true
                    [ -n "$id_closed" ] && td close "$id_closed" -m "FFT implementation validated against recorded calls" >/dev/null 2>&1 || true
                    td note add "Station 4 Observations" --content "High migratory bird activity observed near north tidal inlet between 05:15 and 06:30 UTC." >/dev/null 2>&1 || true
                    ;;
                "Synthwave Studio")
                    id_in_prog=$(td create "SIMD vectorize ladder filter inner loop for ARM NEON" --type task --priority P0 2>/dev/null | awk '/CREATED/ {print $2}')
                    id_open=$(td create "Add dual chorus mode with stereo panning LFO modulation" --type feature --priority P1 2>/dev/null | awk '/CREATED/ {print $2}')
                    id_closed=$(td create "Fix voice stealing click on fast 16th-note arpeggiations" --type bug --priority P2 2>/dev/null | awk '/CREATED/ {print $2}')
                    [ -n "$id_in_prog" ] && td start "$id_in_prog" >/dev/null 2>&1 || true
                    [ -n "$id_closed" ] && td close "$id_closed" -m "Added smooth release envelope on stolen voices" >/dev/null 2>&1 || true
                    td note add "Synth Patch Architecture" --content "Roland Juno-style analog bucket brigade delay emulation signal chain verified." >/dev/null 2>&1 || true
                    ;;
                "Quantum Kitchen")
                    id_in_prog=$(td create "Calculate calcium bath pH buffer for high-acidity passionfruit sphere" --type task --priority P0 2>/dev/null | awk '/CREATED/ {print $2}')
                    id_open=$(td create "Benchmark thermal diffusivity constant for wagyu striploin" --type feature --priority P1 2>/dev/null | awk '/CREATED/ {print $2}')
                    id_closed=$(td create "Add liquid nitrogen freeze-fracture protocols and safety specs" --type chore --priority P2 2>/dev/null | awk '/CREATED/ {print $2}')
                    [ -n "$id_in_prog" ] && td start "$id_in_prog" >/dev/null 2>&1 || true
                    [ -n "$id_closed" ] && td close "$id_closed" -m "Safety specifications documented" >/dev/null 2>&1 || true
                    td note add "Lab Prep Protocol" --content "Reverse spherification with sodium alginate (0.5% w/w) in calcium lactate gluconate (1.0% w/w) bath." >/dev/null 2>&1 || true
                    ;;
            esac

            write_content_links_sample "$project_dir" "$id_open"
        )
    fi
}

# write_content_links_sample drops one Markdown file whose prose exercises every
# content-link kind that works without an external resource provider. It is
# written here rather than in the project fixtures because the td id has to be a
# real one the demo just created — a made-up id opens a card that errors.
write_content_links_sample() {
    local project_dir="$1"
    local issue_id="${2:-}"

    mkdir -p "$project_dir/notes"
    {
        cat <<'EOF'
# Content Links

Every reference below is clickable, and clicking one opens a pane beside Files.
Press `m` to toggle the rendered view — the same references stay live in both,
including inside the document panes this file opens.

EOF
        if [ -n "$issue_id" ]; then
            printf -- '- Issue: %s\n' "$issue_id"
        fi
        cat <<'EOF'
- Source file with a line: README.md:1
- Bare source file: notes/content-links.md
- Diff range: main..HEAD
- URL: https://github.com/marcus/sidecar
- Markdown link: [the Sidecar repository](https://github.com/marcus/sidecar)

## Provider keys inside Markdown links

A resource provider whose config lists the destination's host in `claimHosts`
can take a link's *label* back from the browser, which is how a Jira key
normally appears in a brief:

    [ZMS-37161](https://your-site.atlassian.net/browse/ZMS-37161)

That one needs a provider configured, so it stays an ordinary browser link
here. See `docs/reference/terminal-resource-provider-protocol.md`.
EOF
    } > "$project_dir/notes/content-links.md"
}
