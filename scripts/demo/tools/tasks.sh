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
{"type":"section","id":"demo0001","title":"Inbox","body":"Capture here first. Process into the lists below during review."}
{"type":"task","id":"demo0002","parent":"demo0001","state":"INBOX","priority":"B","title":"Explore new demo presets and workflow tools","tags":["@sidecar"],"body":"Captured during demo setup."}
{"type":"section","id":"demo0003","title":"Next Actions"}
{"type":"task","id":"demo0004","parent":"demo0003","state":"NEXT","priority":"A","title":"Review Traffic Simulation green-wave controller","tags":["@intersections","important"],"body":"Check intersections signal phase timing."}
{"type":"task","id":"demo0005","parent":"demo0003","state":"NEXT","priority":"B","title":"Calibrate 3D printer input shaping profile","tags":["@hardware"]}
{"type":"task","id":"demo0006","parent":"demo0003","state":"TODO","priority":"C","title":"Check wetland acoustic sensor telemetry","tags":["@field"]}
{"type":"task","id":"demo0007","parent":"demo0003","state":"WAITING","title":"Order high-temp silicone heater for bed leveling","tags":["@waiting"],"body":"Awaiting shipment delivery."}
{"type":"section","id":"demo0008","title":"Projects"}
{"type":"section","id":"demo0009","parent":"demo0008","title":"Synthwave Studio Soundpack","body":"Goal: Complete 80s analog synth preset pack."}
{"type":"task","id":"demo000a","parent":"demo0009","state":"NEXT","title":"Design retro bass arpeggiator patch","tags":["@music"]}
{"type":"task","id":"demo000b","parent":"demo0009","state":"TODO","title":"Test stereo chorus delay line modulation","tags":["@music"]}
{"type":"section","id":"demo000c","title":"Someday / Maybe"}
{"type":"task","id":"demo000d","parent":"demo000c","state":"TODO","title":"Build DIY sous-vide temperature controller","tags":["@kitchen"]}
EOF

    cat > "$tasks_dir/archive.jsonl" <<'EOF'
{"type":"meta","version":2}
EOF
}
