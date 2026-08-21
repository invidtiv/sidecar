# Demo Environments

Sidecar includes a modular, fully isolated demo environment runner ([`scripts/demo.sh`](file:///Users/marcus/code/sidecar/scripts/demo.sh)) designed for presenting, testing, and verifying new features without touching developer state, real project configurations, or host tools.

## How It Works

Every demo run is 100% ephemeral and fail-closed:
- **Two-Axis Isolation**: Isolates the tmux server (`TMUX_TMPDIR`) and state trees (`XDG_STATE_HOME`, `XDG_CACHE_HOME`, `XDG_DATA_HOME`, `TASKS_DIR`, `-config`, `SIDECAR_ISOLATED_STATE=1`).
- **Fresh Working-Tree Build**: Automatically compiles a fresh binary from the current checkout/worktree stamped with development metadata (`devel+<branch>.<commit>`), ensuring new features are tested immediately and update checks are suppressed.
- **Auto-Cleanup**: Tearing down (pressing `q` in Sidecar) kills the private demo tmux server and purges the temporary `/tmp` state tree.

## Available Presets & Options

```bash
# 1. Multi-project demo (Default: 5 themed sample projects with git commits & worktrees)
./scripts/demo.sh

# 2. Single focused project
./scripts/demo.sh single -p intersections       # Traffic simulation engine (sidecar-modern)
./scripts/demo.sh single -p plastic-pieces      # Parametric 3D printing (tokyonight-storm)
./scripts/demo.sh single -p avocet              # Bioacoustic telemetry (kanagawa-wave)
./scripts/demo.sh single -p synthwave-studio    # Retro 80s DAW (synthwave)
./scripts/demo.sh single -p quantum-kitchen     # Molecular gastronomy (catppuccin-mocha)

# 3. New-user onboarding simulations
./scripts/demo.sh fresh --onboarding            # Clean slate, 0 projects, td/tasks masked
./scripts/demo.sh fresh --no-git                # Non-Git directory onboarding
./scripts/demo.sh fresh --no-tasks              # Git repo with td installed, no tasks

# 4. Inspection & Debugging
./scripts/demo.sh multi --keep --dry-run        # Generate files in /tmp without launching
```

## How Agents Demo Features

When an agent completes a feature, it can present the demo in two ways:

### 1. Direct Command for the User
Provide the exact `./scripts/demo.sh` command tailored to the feature in the completion response.

### 2. Live Inception Shell / Split (Recommended when running inside Sidecar)
Agents working inside a Sidecar shell can launch the demo directly inside the user's running Sidecar via the Shell Creation CLI ([`docs/plans/active/agent-shell-create-cli.md`](file:///Users/marcus/code/sidecar/docs/plans/active/agent-shell-create-cli.md) and [`docs/plans/active/terminal-splits-and-windowing.md`](file:///Users/marcus/code/sidecar/docs/plans/active/terminal-splits-and-windowing.md)):

```bash
# Open as a dedicated workspace shell in the current project:
sidecar create shell --name "Demo: <Feature Name>" --run "./scripts/demo.sh [preset] [options]"

# Or open side-by-side beside the agent's current shell as a terminal split:
sidecar create shell --split right --run "./scripts/demo.sh [preset] [options]"
```

The inner demo runs in its own private tmux instance and ephemeral state directory, allowing the user to interactively explore the feature without leaving their outer Sidecar session.
