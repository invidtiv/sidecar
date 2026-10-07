#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
unset TMUX TMUX_PANE
SIDECAR_OWNER_SSH_PROOF=1 go test ./internal/cli -run '^TestMobileOwnerSSHControlMasterTransportLoss$' -count=1 -v -timeout 2m
