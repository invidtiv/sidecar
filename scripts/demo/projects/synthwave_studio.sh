#!/bin/bash
# Project 4: Synthwave Studio (Polyphonic Analog Synthesizer & 80s Retro DAW)
# Theme: synthwave
set -euo pipefail

create_project_synthwave_studio() {
    local base_dir="$1"
    local blank="${2:-0}"
    local project_dir="$base_dir/synthwave-studio"

    log_info "Building project: Synthwave Studio (Retro Synth DAW)..."
    init_git_repo "$project_dir" "Synthwave Studio"

    if [ "$blank" -eq 1 ]; then
        cat > "$project_dir/README.md" <<'EOF'
# Synthwave Studio

Retro synth audio engine (blank demo).
EOF
        git_commit_all "$project_dir" "Initial blank commit"
        echo "$project_dir"
        return 0
    fi

    # Commit 1: Polyphonic PolyBLEP oscillator
    mkdir -p "$project_dir/dsp"
    cat > "$project_dir/README.md" <<'EOF'
# Synthwave Studio 🎛️ 🌆

High-performance C99 audio DSP synthesis engine for 80s retrowave basslines, neon brass leads, and lush analog chorus.

## Signal Chain
1. **PolyBLEP Oscillators**: Saw, Square with PWM, Sub-Oscillator (-1 Oct).
2. **Moog Transistor Ladder**: 4-pole 24dB/oct resonant lowpass filter with diode saturation.
3. **BBD Stereo Chorus**: Roland Juno-style analog bucket brigade delay emulation.
EOF

    cat > "$project_dir/dsp/saw_oscillator.c" <<'EOF'
#include <math.h>

// PolyBLEP anti-aliasing residual polynomial
static inline double polyblep(double t, double dt) {
    if (t < dt) {
        t /= dt;
        return t + t - t * t - 1.0;
    } else if (t > 1.0 - dt) {
        t = (t - 1.0) / dt;
        return t * t + t + t + 1.0;
    }
    return 0.0;
}

double generate_sawtooth(double *phase, double freq_hz, double sample_rate) {
    double dt = freq_hz / sample_rate;
    double sample = (2.0 * (*phase)) - 1.0;
    sample -= polyblep(*phase, dt);
    *phase += dt;
    if (*phase >= 1.0) *phase -= 1.0;
    return sample;
}
EOF
    git_commit_all "$project_dir" "Initial commit: PolyBLEP bandlimited oscillator core"

    # Commit 2: Moog ladder filter & Juno chorus
    mkdir -p "$project_dir/patches"
    cat > "$project_dir/dsp/moog_ladder.c" <<'EOF'
#include <math.h>

typedef struct {
    double state[4];
    double cutoff_hz;
    double resonance; // 0.0 to 4.0
} MoogLadder;

double moog_process(MoogLadder *f, double input, double sample_rate) {
    double g = tan(M_PI * f->cutoff_hz / sample_rate);
    double feedback = f->resonance * f->state[3];
    double u = tanh(input - feedback); // Soft-clipping saturation

    f->state[0] += g * (u - f->state[0]);
    f->state[1] += g * (f->state[0] - f->state[1]);
    f->state[2] += g * (f->state[1] - f->state[2]);
    f->state[3] += g * (f->state[2] - f->state[3]);

    return f->state[3];
}
EOF

    cat > "$project_dir/patches/neon_sunset_lead.json" <<'EOF'
{
  "patch_name": "Neon Sunset Lead",
  "osc1_wave": "sawtooth",
  "osc2_wave": "pulse_pwm",
  "osc2_detune_cents": 12.5,
  "filter_cutoff_hz": 1850.0,
  "filter_resonance": 2.4,
  "chorus_mode": "juno_II",
  "reverb_decay_sec": 3.8
}
EOF
    git_commit_all "$project_dir" "Implement 4-pole Moog ladder filter with non-linear saturation and Juno chorus"

    # Commit 3: Arpeggiator engine
    mkdir -p "$project_dir/midi"
    cat > "$project_dir/midi/arpeggiator.c" <<'EOF'
#include <stdint.h>

typedef enum { UP, DOWN, UP_DOWN, RANDOM } ArpPattern;

typedef struct {
    uint8_t note_buffer[16];
    uint8_t count;
    uint8_t step_index;
    ArpPattern pattern;
    double swing_factor; // 0.5 = straight, 0.66 = triplet swing
} Arpeggiator;
EOF
    git_commit_all "$project_dir" "Add 16-step retro arpeggiator with latch and swing timing"

    # Add worktree
    local wt_tape="$base_dir/worktrees/synthwave-tape-fx"
    add_worktree "$project_dir" "$wt_tape" "feature/tape-saturation"
    mkdir -p "$wt_tape/dsp"
    cat > "$wt_tape/dsp/tape_delay.c" <<'EOF'
double apply_tape_flutter(double sample, double flutter_lfo) {
    return sample * (1.0 + (0.003 * flutter_lfo));
}
EOF
    git_commit_all "$wt_tape" "Add vintage reel-to-reel magnetic tape hysteresis and wow/flutter modulation"

    echo "$project_dir"
}
