#!/bin/bash
# Project 5: Quantum Kitchen (Molecular Gastronomy Calculator & Gel Matrix Modeler)
# Theme: catppuccin-mocha
set -euo pipefail

create_project_quantum_kitchen() {
    local base_dir="$1"
    local blank="${2:-0}"
    local project_dir="$base_dir/quantum-kitchen"

    log_info "Building project: Quantum Kitchen (Molecular Gastronomy)..."
    init_git_repo "$project_dir" "Quantum Kitchen"

    if [ "$blank" -eq 1 ]; then
        cat > "$project_dir/README.md" <<'EOF'
# Quantum Kitchen

Molecular gastronomy project (blank demo).
EOF
        git_commit_all "$project_dir" "Initial blank commit"
        echo "$project_dir"
        return 0
    fi

    # Commit 1: Spherification kinetics
    mkdir -p "$project_dir/models"
    cat > "$project_dir/README.md" <<'EOF'
# Quantum Kitchen 🧪 🍳

Precision molecular gastronomy thermodynamics, spherification rate kinetics, and hydrocolloid phase calculator.

## Culinary Domains
- **Direct & Reverse Spherification**: Sodium alginate (0.5% w/w) in calcium lactate gluconate (1.0% w/w) bath.
- **Fluid Gel Rheology**: High-shear agar-agar and gellan gum yield-stress calculations.
- **Cryogenic Shattering**: Liquid nitrogen (-196°C) flash-freezing for aerated mousses and herbal powders.
EOF

    cat > "$project_dir/models/spherification.rs" <<'EOF'
pub struct SpherificationBath {
    pub calcium_concentration_molar: f64,
    pub ph: f64,
    pub bath_temp_celsius: f64,
}

impl SpherificationBath {
    pub fn calculate_membrane_thickness_mm(&self, time_seconds: f64) -> f64 {
        // Fick's second law parabolic diffusion approximation
        let diffusion_coefficient = 1.2e-9 * (1.0 + 0.02 * (self.bath_temp_celsius - 20.0));
        2.0 * (diffusion_coefficient * time_seconds).sqrt() * 1000.0
    }
}
EOF
    git_commit_all "$project_dir" "Initial commit: calcium alginate hydrogel membrane diffusion kinetics"

    # Commit 2: Sous-vide thermal kinetics & fluid gels
    mkdir -p "$project_dir/recipes"
    cat > "$project_dir/models/sous_vide.rs" <<'EOF'
pub struct ThermalBathSolver {
    pub water_temp_c: f64,
    pub thickness_mm: f64,
    pub thermal_diffusivity: f64, // m^2/s
}

impl ThermalBathSolver {
    pub fn time_to_core_pasteurization_mins(&self, target_core_temp_c: f64) -> f64 {
        let half_thickness_m = (self.thickness_mm / 2.0) / 1000.0;
        let fourier_number = 1.85; // Empirical threshold for cylindrical geometry
        (fourier_number * half_thickness_m * half_thickness_m / self.thermal_diffusivity) / 60.0
    }
}
EOF

    cat > "$project_dir/recipes/spherified_mango_caviar.yaml" <<'EOF'
recipe_name: "Spherified Mango Caviar"
ingredients:
  - name: "Alphonso Mango Puree"
    amount_g: 250
  - name: "Sodium Alginate"
    amount_g: 1.25
    concentration_pct: 0.5
  - name: "Water (Low Calcium)"
    amount_g: 100
bath:
  - name: "Calcium Lactate Gluconate"
    amount_g: 10.0
    water_g: 1000.0
    submersion_seconds: 120
EOF
    git_commit_all "$project_dir" "Add unsteady-state heat conduction solver for sous-vide steak core temp"

    # Commit 3: Liquid nitrogen freeze-fracture protocols
    cat > "$project_dir/recipes/cryo_meyer_lemon_meringue.yaml" <<'EOF'
recipe_name: "Flash-Frozen Meyer Lemon Meringue Shards"
equipment:
  - "Liquid Nitrogen Dewar (-196°C)"
  - "Perforated Cryo Ladle"
  - "Thermal Cryo Gloves"
procedure:
  - step: 1
    action: "Pipe 20mm rosettes of Italian meringue directly into liquid nitrogen bath."
  - step: 2
    action: "Submerge for 25 seconds until outer 2mm forms a brittle, vitreous crust."
  - step: 3
    action: "Transfer immediately to serving plate; shatter with dessert spoon."
EOF
    git_commit_all "$project_dir" "Add liquid nitrogen freeze-fracture protocols and safety specs"

    # Add worktree
    local wt_foams="$base_dir/worktrees/quantum-kitchen-foams"
    add_worktree "$project_dir" "$wt_foams" "feature/foams-and-airs"
    mkdir -p "$wt_foams/models"
    cat > "$wt_foams/models/surfactant_foam.rs" <<'EOF'
pub fn calculate_foam_stability_half_life(lecithin_pct: f64, acidity_ph: f64) -> f64 {
    (lecithin_pct * 120.0) * (acidity_ph / 7.0)
}
EOF
    git_commit_all "$wt_foams" "Add soy lecithin surfactant foam aeration half-life calculator"

    echo "$project_dir"
}
