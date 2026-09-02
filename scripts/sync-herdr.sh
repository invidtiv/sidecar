#!/usr/bin/env bash
# Refresh the vendored Herdr agent-detection manifests, the lock, the extracted
# alias and authority tables, and the vendored Herdr integration assets.
#
# This is a thin wrapper over `go run ./internal/tools/herdrsync`; every flag is
# passed straight through. Run it from the repository root.
#
#   scripts/sync-herdr.sh                                   # Herdr's default branch + live catalog
#   scripts/sync-herdr.sh --ref v0.8.2                       # pin to a release tag by hand
#   scripts/sync-herdr.sh --source-dir ~/code/herdr --offline # no network at all
#
# With --source-dir every file is read with `git show <ref>:<path>` inside that
# checkout, never off its working tree, and the run fails when --ref does not
# resolve there: the bytes vendored are the bytes of the commit the lock records.
#
# The tool writes under two output roots and nowhere else:
#
#   internal/agentactivity/manifests   detection manifests, their lock, the
#                                      extracted tables, and report.md
#   internal/agentintegration          Herdr's provider integration assets under
#                                      upstream/, with their own lock
#
# Override either with --out and --integration-out. report.md is rendered in the
# first one and covers both. Vendored files in either tree are byte-for-byte
# upstream copies; never edit one. A detection change belongs in an overlay under
# manifests/sidecar/; an integration change belongs in a Sidecar asset under
# internal/agentintegration/assets, with its provenance recorded in
# portedfrom.go.
set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."
exec go run ./internal/tools/herdrsync "$@"
