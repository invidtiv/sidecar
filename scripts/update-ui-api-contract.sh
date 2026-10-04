#!/bin/sh
# Regenerate the committed OpenAPI, HTTP/terminal examples and SHA256SUMS.
# Terminal examples use the real service with a synthetic adapter; no tmux.
set -eu
cd "$(dirname "$0")/.."
# Catalog fields are shared with the mobile protocol corpus.
UPDATE_MOBILE_CATALOG_FIXTURE=1 go test ./internal/mobile -run '^TestCatalogProtocolFixtureMatchesProducer$' -count=1
(
	cd testdata/mobile-protocol/v0
	shasum -a 256 README.md history-snapshot.json sessions-catalog.json ssh-fresh-reconnect.jsonl terminal-candidates.json > SHA256SUMS
)
# Update resource inputs before recording the real terminal transcript.
UPDATE_UI_API_FIXTURES=1 UPDATE_UI_API_SPEC=1 go test ./internal/uiapi -run '^(TestUIAPIFixtureCorpus|TestSpecMatchesCommittedDocumentAndRoutes)$' -count=1
UPDATE_UI_API_FIXTURES=1 go test ./internal/uiapi -run '^TestFixtureServerTerminalUsesRealOrderingAndGuards$' -count=1
UPDATE_UI_API_FIXTURES=1 UPDATE_UI_API_SPEC=1 go test ./internal/uiapi -run '^(TestUIAPIFixtureCorpus|TestSpecMatchesCommittedDocumentAndRoutes)$' -count=1
REGEN_CLI_DOC=1 go test ./internal/cli -run '^TestRegenerateCLIDoc$' -count=1
go test ./internal/uiapi -run '^(TestUIAPIFixtureCorpus|TestSpecMatchesCommittedDocumentAndRoutes|TestFixtureServerTerminalUsesRealOrderingAndGuards|TestEventsFixtureTranscript)$' -count=1
