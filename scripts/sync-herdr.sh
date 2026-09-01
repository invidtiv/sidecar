#!/usr/bin/env bash
# Refresh the vendored Herdr agent-detection manifests, the lock, and the
# extracted alias and authority tables.
#
# This is a thin wrapper over `go run ./internal/tools/herdrsync`; every flag is
# passed straight through. Run it from the repository root.
#
#   scripts/sync-herdr.sh                                   # newest Herdr release + live catalog
#   scripts/sync-herdr.sh --ref main                         # track main instead
#   scripts/sync-herdr.sh --source-dir ~/code/herdr --offline # no network at all
#
# The tool writes only under internal/agentactivity/manifests and renders
# report.md there for review. Vendored files are byte-for-byte upstream copies;
# never edit one, put the change in an overlay under manifests/sidecar/.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
exec go run ./internal/tools/herdrsync "$@"
