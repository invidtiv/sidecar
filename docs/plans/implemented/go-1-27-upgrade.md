# Plan: Upgrade to Go 1.27

## Goal

Move sidecar from Go 1.26.0 to Go 1.27.0 with no behavioral regressions: bump the module and workspace directives, verify the app end to end under the new runtime, and confirm the release path still builds pinned dependencies cleanly.

## Current state

| Concern | Today |
|---|---|
| go.mod directive | `go 1.26.0` |
| go.work directive | `go 1.26.0`, spanning `./`, `../tasks`, `../td` |
| Local toolchain | Homebrew go1.27.0 already builds everything — this mismatch is what forced golangci-lint v2.13.1 (c027481d) |
| CI | `setup-go` uses `go-version-file: go.mod`, so the directive is the single lever; lint action pinned at v2.13.1 (Go 1.27-capable) |
| Release deps | Pins `td v0.62.0` / `tasks v1.12.0` from the module proxy for release builds; dev installs compile siblings from source via go.work |

## What Go 1.27 changes that touch this repo

- **encoding/json/v2 is now the default implementation** of `encoding/json` (escape hatch: `GOEXPERIMENT=nojsonv2`). Error strings and some edge behaviors differ from v1. Sidecar parses provider JSONL streams in every adapter, plus config/state round-trips — tests asserting exact error text are the likely casualties.
- `go test` runs the **stdversion vet** check by default (stdlib symbols newer than the effective directive). After the bump this is satisfied by definition.
- Generic methods and embedded-field-selector struct literals are legal syntax — additive; only matters to tooling, which v2.13.1 handles.
- `compress/flate` output bytes changed — relevant only if any test asserts exact gzip bytes.
- macOS 13+ remains the darwin floor (unchanged from 1.26).

## Work sequence

1. **Directives**: set `go 1.27.0` in `go.work` and `go.mod` in the same change. The workspace directive must be ≥ each member's; both siblings currently declare ≤ 1.26, so landing order versus their own upgrades is flexible either way.
2. **Tidy**: `go mod tidy` (expect no drift; toolchain-only change).
3. **JSON pass**: run the full suite watching adapter parsing, config loading, and state persistence tests; on any failure, bisect with `GOEXPERIMENT=nojsonv2` to confirm a json/v2 cause before changing code. The escape hatch is a diagnostic, not a fix.
4. **Startup proof**: `SIDECAR_STARTUP_TRACE=stderr sidecar` before/after — no startup-path work exists in this change, so traces should match within noise.
5. **Headless proof**: `./scripts/tmux-drive.sh paths` then an isolated start/keys/snap run per AGENTS.md (both tmux socket and state tree isolated).
6. **Release path**: `SIDECAR_INSTALL_PINNED=1 make install-local` (pinned td/tasks compile under 1.27), `make goreleaser-snapshot`, `make install-status`.

## Coordination

td and tasks carry their own upgrade plans. Recommended landing order is td → tasks → sidecar so the trio moves together, but nothing here blocks on them: 1.27 ≥ both current sibling directives, and future pin bumps that inherit a 1.27 requirement land after this plan by construction.

## Verification & acceptance evidence

- `go build ./...`, `go test ./...`, `GOOS=linux GOWORK=off golangci-lint run ./...` all clean.
- Pre-commit hook green on a real commit.
- Startup trace deltas within noise; tmux-drive proof shows unchanged behavior.
- Pinned install and goreleaser snapshot succeed.

Out of scope: dependency version bumps beyond tidy, json/v2-specific code adoption, td/tasks upgrades themselves.
