#!/bin/bash
# Project 1: Intersections (Traffic Flow Simulation Engine)
# Theme: sidecar-modern
set -euo pipefail

create_project_intersections() {
    local base_dir="$1"
    local blank="${2:-0}"
    local project_dir="$base_dir/intersections"

    log_info "Building project: Intersections (Traffic Sim)..."
    init_git_repo "$project_dir" "Intersections"

    if [ "$blank" -eq 1 ]; then
        cat > "$project_dir/README.md" <<'EOF'
# Intersections

Traffic simulation game (blank demo).
EOF
        git_commit_all "$project_dir" "Initial blank commit"
        echo "$project_dir"
        return 0
    fi

    # Commit 1: Core grid and vehicle models
    mkdir -p "$project_dir/simulation"
    cat > "$project_dir/README.md" <<'EOF'
# Intersections 🚦

A deterministic, high-throughput macroscopic and microscopic traffic flow simulation engine.

## Overview
- **Grid Architecture**: Discrete 2D Manhattan road segments with variable lane capacities.
- **Signal Control**: Green-wave phase optimization and fixed-time cyclic controllers.
- **Driver Kinetics**: Nagel-Schreckenberg cellular automata with stochastic braking.

## Getting Started
```bash
go run ./cmd/simulator --map=downtown --density=0.65
```
EOF

    cat > "$project_dir/simulation/grid.go" <<'EOF'
package simulation

type LaneDirection int

const (
	North LaneDirection = iota
	South
	East
	West
)

type IntersectionNode struct {
	ID        string
	X, Y      int
	Signal    *TrafficSignal
	Inbound   []*RoadSegment
	Outbound  []*RoadSegment
}

type RoadSegment struct {
	ID        string
	LengthM   float64
	SpeedLimit float64
	Lanes     int
	Vehicles  []*Vehicle
}
EOF
    git_commit_all "$project_dir" "Initial commit: 2D Manhattan grid representation and road geometry"

    # Commit 2: Traffic signal controller
    cat > "$project_dir/simulation/traffic_signal.go" <<'EOF'
package simulation

import "time"

type SignalState int

const (
	Red SignalState = iota
	Yellow
	Green
	FlashingYellow
)

type TrafficSignal struct {
	ID             string
	CurrentState   SignalState
	GreenDuration  time.Duration
	YellowDuration time.Duration
	RedDuration    time.Duration
	CycleElapsed   time.Duration
}

func (s *TrafficSignal) AdvanceTick(delta time.Duration) {
	s.CycleElapsed += delta
	switch s.CurrentState {
	case Green:
		if s.CycleElapsed >= s.GreenDuration {
			s.CurrentState = Yellow
			s.CycleElapsed = 0
		}
	case Yellow:
		if s.CycleElapsed >= s.YellowDuration {
			s.CurrentState = Red
			s.CycleElapsed = 0
		}
	case Red:
		if s.CycleElapsed >= s.RedDuration {
			s.CurrentState = Green
			s.CycleElapsed = 0
		}
	}
}
EOF
    git_commit_all "$project_dir" "Add traffic signal state controller with adaptive green wave phase calculation"

    # Commit 3: Vehicle dynamics and map
    mkdir -p "$project_dir/maps"
    cat > "$project_dir/simulation/vehicle.go" <<'EOF'
package simulation

import "math/rand"

type Vehicle struct {
	ID         int
	Position   float64 // meters along road segment
	Speed      float64 // meters per second
	MaxSpeed   float64
	Gap        float64 // distance to vehicle ahead
}

func (v *Vehicle) UpdateKinetics(brakingProbability float64) {
	// Step 1: Acceleration
	if v.Speed < v.MaxSpeed {
		v.Speed++
	}
	// Step 2: Slowing down due to other cars
	if v.Speed > v.Gap {
		v.Speed = v.Gap
	}
	// Step 3: Randomization
	if v.Speed > 0 && rand.Float64() < brakingProbability {
		v.Speed--
	}
	// Step 4: Vehicle motion
	v.Position += v.Speed
}
EOF

    cat > "$project_dir/maps/downtown_avenues.json" <<'EOF'
{
  "name": "Downtown Grid",
  "intersections": [
    {"id": "int-1st-main", "x": 0, "y": 0, "greenSec": 35, "yellowSec": 4},
    {"id": "int-2nd-main", "x": 100, "y": 0, "greenSec": 30, "yellowSec": 4},
    {"id": "int-3rd-main", "x": 200, "y": 0, "greenSec": 40, "yellowSec": 5}
  ]
}
EOF
    git_commit_all "$project_dir" "Implement Nagel-Schreckenberg cellular automata vehicle movement"

    # Commit 4: Notes and telemetry
    mkdir -p "$project_dir/notes"
    cat > "$project_dir/notes/architecture.md" <<'EOF'
# Intersections Simulation Architecture

## Key Components
1. **Network Mesh**: Graph representing interconnected signalized nodes.
2. **Kinematic Loop**: 60Hz tick update propagating vehicle braking waves.
3. **Queue Telemetry**: Measures average wait time at bottlenecks.
EOF
    git_commit_all "$project_dir" "Add telemetry output and ANSI debug dashboard for congestion monitoring"

    # Add worktrees
    local wt_roundabouts="$base_dir/worktrees/intersections-roundabouts"
    add_worktree "$project_dir" "$wt_roundabouts" "feature/roundabouts"
    cat > "$wt_roundabouts/simulation/roundabout.go" <<'EOF'
package simulation

type Roundabout struct {
	RadiusM  float64
	Lanes    int
	CirculatingSpeed float64
}
EOF
    git_commit_all "$wt_roundabouts" "WIP: Circular continuous-flow roundabout geometry"

    local wt_emergency="$base_dir/worktrees/intersections-emergency"
    add_worktree "$project_dir" "$wt_emergency" "feature/emergency-preemption"
    cat > "$wt_emergency/simulation/preemption.go" <<'EOF'
package simulation

type OpticalPreemption struct {
	SensorID       string
	IntersectionID string
	Active         bool
}
EOF
    git_commit_all "$wt_emergency" "Add optical siren preemption trigger for first-responder vehicles"

    echo "$project_dir"
}
