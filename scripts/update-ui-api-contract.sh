#!/bin/sh
# Regenerate the committed OpenAPI, HTTP/terminal examples and SHA256SUMS.
# Terminal examples use the real service with a synthetic adapter; no tmux.
set -eu
cd "$(dirname "$0")/.."
UPDATE_UI_API_FIXTURES=1 go test ./internal/uiapi -run '^TestFixtureServerTerminalUsesRealOrderingAndGuards$' -count=1
UPDATE_UI_API_FIXTURES=1 UPDATE_UI_API_SPEC=1 go test ./internal/uiapi -run '^(TestUIAPIFixtureCorpus|TestSpecMatchesCommittedDocumentAndRoutes)$' -count=1
REGEN_CLI_DOC=1 go test ./internal/cli -run '^TestRegenerateCLIDoc$' -count=1
go test ./internal/uiapi -run '^(TestUIAPIFixtureCorpus|TestSpecMatchesCommittedDocumentAndRoutes|TestFixtureServerTerminalUsesRealOrderingAndGuards)$' -count=1
