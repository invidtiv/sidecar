#!/bin/bash
# Project 2: Plastic Pieces (Parametric 3D Print Generator & Slicer Manager)
# Theme: tokyonight-storm
set -euo pipefail

create_project_plastic_pieces() {
    local base_dir="$1"
    local blank="${2:-0}"
    local project_dir="$base_dir/plastic-pieces"

    log_info "Building project: Plastic Pieces (3D Printing)..."
    init_git_repo "$project_dir" "Plastic Pieces"

    if [ "$blank" -eq 1 ]; then
        cat > "$project_dir/README.md" <<'EOF'
# Plastic Pieces

3D printing project (blank demo).
EOF
        git_commit_all "$project_dir" "Initial blank commit"
        echo "$project_dir"
        return 0
    fi

    # Commit 1: OpenSCAD planetary gear model
    mkdir -p "$project_dir/models"
    cat > "$project_dir/README.md" <<'EOF'
# Plastic Pieces ⚙️

Parametric OpenSCAD models, custom Voron DIN rail mounts, and tuned high-speed slicer profiles.

## Printing Guidelines
- **PLA High-Flow**: 220°C nozzle, 60°C PEI bed, 300mm/s outer perimeter.
- **PETG Structural**: 245°C nozzle, 80°C bed, 40% fan, 0.4mm layer clearance for captive bearings.
EOF

    cat > "$project_dir/models/planetary_bearing.scad" <<'EOF'
// Parametric Herringbone Planetary Gear Bearing
$fn = 80;

teeth_sun = 12;
teeth_planets = 8;
teeth_ring = 28;
module_pitch = 2.0;
pressure_angle = 20;
clearance = 0.25; // Slicer tolerance offset in mm

module gear_sun() {
    cylinder(r = teeth_sun * module_pitch / 2 - clearance, h = 10, center = true);
}

module planetary_assembly() {
    gear_sun();
    for (i = [0:5]) {
        rotate([0, 0, i * 60])
            translate([24, 0, 0])
                cylinder(r = teeth_planets * module_pitch / 2 - clearance, h = 10, center = true);
    }
}

planetary_assembly();
EOF
    git_commit_all "$project_dir" "Initial commit: OpenSCAD planetary gear model with tolerance offsets"

    # Commit 2: Voron DIN rail mount and slicer profile
    mkdir -p "$project_dir/slicer/profiles"
    cat > "$project_dir/models/din_rail_clip.scad" <<'EOF'
// DIN Rail Clip (EN 50022 35mm) with M3 Brass Heat-Set Insert Bosses
rail_width = 35.0;
clip_depth = 8.0;
insert_hole_dia = 4.0; // Sized for M3x4x5 brass heat-set inserts

difference() {
    cube([42, clip_depth, 14], center = true);
    translate([0, 0, 2])
        cube([rail_width, clip_depth + 1, 10], center = true);
    // Mounting insert holes
    translate([16, 0, 0]) cylinder(d = insert_hole_dia, h = 20, center = true);
    translate([-16, 0, 0]) cylinder(d = insert_hole_dia, h = 20, center = true);
}
EOF

    cat > "$project_dir/slicer/profiles/pla_rapid_016.json" <<'EOF'
{
  "name": "PLA Rapid 0.16mm High Detail",
  "layer_height": 0.16,
  "first_layer_height": 0.20,
  "perimeters": 4,
  "infill_density": "25%",
  "infill_pattern": "gyroid",
  "print_speed": 280,
  "travel_speed": 450,
  "acceleration": 12000
}
EOF
    git_commit_all "$project_dir" "Add Voron 2.4 DIN rail mounting bracket and slicer profile"

    # Commit 3: Klipper input shaping macros
    mkdir -p "$project_dir/firmware"
    cat > "$project_dir/firmware/klipper_macros.cfg" <<'EOF'
[input_shaper]
shaper_freq_x: 62.4
shaper_type_x: mzv
shaper_freq_y: 48.2
shaper_type_y: ei

[gcode_macro NOZZLE_PURGE]
gcode:
    G90
    G1 X10 Y10 Z0.3 F6000
    G92 E0
    G1 X90 E12 F1200
    G1 X100 E14 F1200
    G92 E0
EOF
    git_commit_all "$project_dir" "Tune input shaping resonance compensation macros in Klipper"

    # Add worktree
    local wt_dual="$base_dir/worktrees/plastic-pieces-dual-nozzle"
    add_worktree "$project_dir" "$wt_dual" "feature/dual-extrusion"
    mkdir -p "$wt_dual/slicer"
    cat > "$wt_dual/slicer/idex_toolchange.gcode" <<'EOF'
; IDEX Toolchange Prime
T[next_extruder]
G1 X[wipe_tower_x] Y[wipe_tower_y] F12000
G1 E1.5 F1800
EOF
    git_commit_all "$wt_dual" "Add IDEX dual-extruder prime pillar & tool change wipe logic"

    echo "$project_dir"
}
