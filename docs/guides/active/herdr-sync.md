# Maintaining the Herdr sync

The weekly `Herdr sync` GitHub Actions workflow imports detection manifests, alias and authority metadata, and integration source assets from Herdr's default branch. It pins a released Herdr binary separately for differential verification. Run it manually with `gh workflow run herdr-sync.yml`; reproduce the import with `scripts/sync-herdr.sh`.

The workflow opens or updates `bot/herdr-sync`. Its Go checks and both differential modes are posted as commit statuses on the exact published commit. An import or push failure does not validate an older sync branch. A failed check leaves the proposed changes available for review with a failing status. Merge only when both statuses pass.

Staleness compares current upstream versions with the committed lock. An open sync PR exempts a stale bump only when its own pinned lock actually contains that bump. An old PR does not hide newer missing rules.

The importer accepts the legacy authority table and the current integration/notes table in Herdr's agent documentation. Unknown table schemas, agent names, or note meanings fail before vendoring. Native self-reporting is recorded separately from installable lifecycle integrations. Imported authority metadata never grants a Sidecar integration tier; ported assets require their own proof.

A sync can require changes to overlays or the engine. Remove a disable when upstream removes its target. Retire regex rewrites when upstream removes the rule, and replace obsolete synthetic fixtures with current upstream shapes. The Codex no-match fallback is `unknown` (`codex_state_ambiguous`), including through Sidecar's live detection path; lack of working evidence cannot announce completion. Explicit matched rules still determine their declared state.

Validation: `GOWORK=off SIDECAR_REQUIRE_NODE=1 go test ./...`, `GOWORK=off go build ./...`, and `GOWORK=off golangci-lint run ./...`. With the pinned binary available, run `HERDR_BIN=/path/to/herdr scripts/herdr-diff.sh` and the same command with `--merged`. The default comparison permits named Sidecar overlay differences; merged mode must agree on every compared fixture.

Vendoring a new agent does not register it as a Sidecar provider or install its hooks. Letta's source assets and manifest are available for a later deliberate port; existing integration ports remain independently versioned.
