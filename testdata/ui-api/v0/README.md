# UI API v0 fixtures

All examples are synthetic and use the Go wire types. `hello.json`, `sessions.json`, `status.json` and `error.json` are plain HTTP bodies. `pairing.json` records two request/response exchanges; codes and tokens are examples only. `terminal.jsonl` records direction-tagged real mobile service envelopes from the deterministic echo adapter, with process-scoped handles normalized. `SHA256SUMS` covers every JSON/JSONL file.

Serve with `sidecar api serve --fixtures testdata/ui-api/v0 --port 0`. Authentication and routing remain real; use `sidecar api open` or `sidecar api pair --origin URL` as usual. The terminal echoes bytes and does not execute shell commands. History is unavailable. Sessions queries use the shared catalog projection.

Regenerate the spec, fixtures, checksums and CLI reference with `./scripts/update-ui-api-contract.sh` from the repository root. Additional stream JSON/JSONL fixtures are automatically included in the manifest. The fixture adapter contract is in [ui-api.md](../../../docs/reference/ui-api.md). Run `scripts/ui-api-fixture-proof.sh` for the isolated CLI/HTTP/WebSocket journey.
