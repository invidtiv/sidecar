#!/bin/bash
# Tool integration: Tasks plugin and store isolation
set -euo pipefail

setup_tasks_store() {
    local tasks_dir="$1"
    local enabled="${2:-1}"

    if [ "$enabled" -ne 1 ]; then
        return 0
    fi

    log_info "Seeding generic Tasks store for demo..."
    mkdir -p "$tasks_dir"

    cat > "$tasks_dir/tasks.jsonl" <<'EOF'
{"type":"meta","version":2}
{"type":"section","id":"de300001","title":"Inbox","body":"Capture here first. Process into the lists below during review."}
{"type":"task","id":"de300002","parent":"de300001","state":"INBOX","priority":"B","title":"Explore new demo presets and workflow tools","tags":["@sidecar"],"body":"Captured during demo setup."}
{"type":"section","id":"de300003","title":"Next Actions"}
{"type":"task","id":"de300004","parent":"de300003","state":"NEXT","priority":"A","title":"Review Traffic Simulation green-wave controller","tags":["@intersections","important"],"body":"Check intersections signal phase timing."}
{"type":"task","id":"de300005","parent":"de300003","state":"NEXT","priority":"B","title":"Calibrate 3D printer input shaping profile","tags":["@hardware"]}
{"type":"task","id":"de300006","parent":"de300003","state":"TODO","priority":"C","title":"Check wetland acoustic sensor telemetry","tags":["@field"]}
{"type":"task","id":"de300007","parent":"de300003","state":"WAITING","title":"Order high-temp silicone heater for bed leveling","tags":["@waiting"],"body":"Awaiting shipment delivery."}
{"type":"section","id":"de300008","title":"Projects"}
{"type":"section","id":"de300009","parent":"de300008","title":"Synthwave Studio Soundpack","body":"Goal: Complete 80s analog synth preset pack."}
{"type":"task","id":"de30000a","parent":"de300009","state":"NEXT","title":"Design retro bass arpeggiator patch","tags":["@music"]}
{"type":"task","id":"de30000b","parent":"de300009","state":"TODO","title":"Test stereo chorus delay line modulation","tags":["@music"]}
{"type":"section","id":"de30000c","title":"Someday / Maybe"}
{"type":"task","id":"de30000d","parent":"de30000c","state":"TODO","title":"Build DIY sous-vide temperature controller","tags":["@kitchen"]}
EOF

    cat > "$tasks_dir/archive.jsonl" <<'EOF'
{"type":"meta","version":2}
EOF
}
