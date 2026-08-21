#!/bin/bash
# Project 3: Avocet (Marshland Bioacoustic Telemetry & Migratory Pattern Tracker)
# Theme: kanagawa-wave
set -euo pipefail

create_project_avocet() {
    local base_dir="$1"
    local blank="${2:-0}"
    local project_dir="$base_dir/avocet"

    log_info "Building project: Avocet (Bioacoustic Tracker)..."
    init_git_repo "$project_dir" "Avocet"

    if [ "$blank" -eq 1 ]; then
        cat > "$project_dir/README.md" <<'EOF'
# Avocet

Mysterious bird app (blank demo).
EOF
        git_commit_all "$project_dir" "Initial blank commit"
        echo "$project_dir"
        return 0
    fi

    # Commit 1: Telemetry ingestion pipeline
    mkdir -p "$project_dir/src/telemetry"
    cat > "$project_dir/README.md" <<'EOF'
# Avocet 🪶

Autonomous bioacoustic monitoring array for tidal marshlands and migratory waterfowl sanctuaries.

## Sensor Architecture
- **LoRa Mesh Network**: Sub-GHz radio packets transmitting audio burst spectrogram centroids.
- **Harmonic Filter**: Identifies Recurvirostra avosetta (Pied Avocet) sweeping up-curved whistling calls (2.8 kHz – 4.2 kHz).
- **Solar Sleep Duty**: Adaptive wake-on-acoustic-trigger power management.
EOF

    cat > "$project_dir/src/telemetry/station_mesh.py" <<'EOF'
"""LoRa Mesh Radio Packet Receiver for Wetland Sensor Array."""
import struct
import time

class RadioMeshListener:
    def __init__(self, frequency_mhz: float = 915.0, spread_factor: int = 7):
        self.frequency = frequency_mhz
        self.sf = spread_factor
        self.active_nodes = {}

    def parse_packet(self, raw_bytes: bytes):
        node_id, battery_mv, peak_freq, duration_ms = struct.unpack("<HHfH", raw_bytes[:10])
        self.active_nodes[node_id] = {
            "last_heard": time.time(),
            "battery_mv": battery_mv,
            "peak_freq_hz": peak_freq,
            "duration_ms": duration_ms
        }
        return self.active_nodes[node_id]
EOF
    git_commit_all "$project_dir" "Initial commit: LoRa mesh acoustic sensor ingestion engine"

    # Commit 2: Spectrogram frequency analysis
    mkdir -p "$project_dir/src/acoustics" "$project_dir/data/signatures"
    cat > "$project_dir/src/acoustics/spectrogram.py" <<'EOF'
"""Acoustic Fast Fourier Transform Spectrogram Processor."""
import math

class FastSpectrogram:
    def __init__(self, sample_rate_hz: int = 32000, fft_size: int = 512):
        self.sample_rate = sample_rate_hz
        self.fft_size = fft_size
        self.window = [0.5 * (1 - math.cos(2 * math.pi * i / (fft_size - 1))) for i in range(fft_size)]

    def compute_energy_band(self, audio_frame, min_hz: float = 2800.0, max_hz: float = 4200.0):
        bin_width = self.sample_rate / self.fft_size
        min_bin = int(min_hz / bin_width)
        max_bin = int(max_hz / bin_width)
        return sum(abs(audio_frame[i] * self.window[i]) for i in range(min_bin, min_bin + (max_bin - min_bin)))
EOF

    cat > "$project_dir/data/signatures/recurvirostra_avosetta.json" <<'EOF'
{
  "species": "Recurvirostra avosetta",
  "common_name": "Pied Avocet",
  "call_type": "alarm_whistle",
  "base_freq_hz": 2850,
  "peak_freq_hz": 4120,
  "harmonic_ratio": 1.48,
  "confidence_threshold": 0.82
}
EOF
    git_commit_all "$project_dir" "Implement real-time Hann-windowed FFT spectrogram generation"

    # Commit 3: Field observation notes
    mkdir -p "$project_dir/field_notes"
    cat > "$project_dir/field_notes/wetland_reserve_log.md" <<'EOF'
# Marshland Reserve Field Notes

- **Station 04 (North Mudflats)**: High activity recorded between 05:15 and 06:30 UTC.
- **Flock Count**: Estimated 45-60 individuals foraging along tidal receding line.
- **Hardware Status**: Solar battery array 02 holding steady at 3.92V.
EOF
    git_commit_all "$project_dir" "Log wetland reserve node array telemetry data and field notes"

    # Add worktree
    local wt_doppler="$base_dir/worktrees/avocet-doppler"
    add_worktree "$project_dir" "$wt_doppler" "experiment/doppler-tracking"
    mkdir -p "$wt_doppler/src/acoustics"
    cat > "$wt_doppler/src/acoustics/doppler.py" <<'EOF'
def estimate_flight_speed(observed_freq_hz: float, source_freq_hz: float = 3500.0, speed_of_sound_ms: float = 343.0):
    delta = observed_freq_hz - source_freq_hz
    return (delta * speed_of_sound_ms) / source_freq_hz
EOF
    git_commit_all "$wt_doppler" "Add Doppler shift frequency velocity estimator for flight vectors"

    echo "$project_dir"
}
