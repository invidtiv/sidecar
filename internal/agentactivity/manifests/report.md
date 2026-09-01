# Herdr detection sync report

Generated 2026-09-01T20:20:04Z by `go run ./internal/tools/herdrsync`.

| Field | Value |
| --- | --- |
| Herdr repository | https://github.com/herdrdev/herdr |
| Ref vendored | `e2b85c7` |
| Commit | `e2b85c73615b37a483eefa839923d9aff8e629b3` |
| Pinned release for the differential harness | `v0.8.2` |
| Catalog | https://herdr.dev/agent-detection/index.toml |
| Catalog ETag | `W/"d78b183cb570f3f343ba80777bbf579d"` |
| Sidecar manifest engine version | 3 |
| Manifests vendored | 21 |
| Read from local checkout | `/Users/marcus/code/herdr` |

## Version changes

No manifest version changed since the previous lock.

## File changes

0 file(s) changed, 21 unchanged.

## Published versus bundled

Each row is the copy a Herdr client would load, and why.

| Agent | Vendored from | Bundled | Published | Reason |
| --- | --- | --- | --- | --- |
| `agy` | published | 2026.06.24.1 | 2026.06.24.1 | published and bundled are both 2026.06.24.1; a Herdr client prefers the remote copy |
| `amp` | published | 2026.07.09.1 | 2026.07.09.1 | published and bundled are both 2026.07.09.1; a Herdr client prefers the remote copy |
| `claude` | published | 2026.08.29.1 | 2026.08.29.1 | published and bundled are both 2026.08.29.1; a Herdr client prefers the remote copy |
| `cline` | published | 2026.06.10.1 | 2026.06.10.1 | published and bundled are both 2026.06.10.1; a Herdr client prefers the remote copy |
| `codex` | published | 2026.08.28.1 | 2026.08.28.1 | published and bundled are both 2026.08.28.1; a Herdr client prefers the remote copy |
| `copilot` | published | 2026.08.29.1 | 2026.08.29.1 | published and bundled are both 2026.08.29.1; a Herdr client prefers the remote copy |
| `cursor` | published | 2026.08.03.1 | 2026.08.03.1 | published and bundled are both 2026.08.03.1; a Herdr client prefers the remote copy |
| `devin` | published | 2026.06.15.1 | 2026.06.15.1 | published and bundled are both 2026.06.15.1; a Herdr client prefers the remote copy |
| `droid` | published | 2026.06.10.1 | 2026.06.10.1 | published and bundled are both 2026.06.10.1; a Herdr client prefers the remote copy |
| `gemini` | published | 2026.06.10.1 | 2026.06.10.1 | published and bundled are both 2026.06.10.1; a Herdr client prefers the remote copy |
| `grok` | bundled | 2026.07.16.2 | 2026.07.16.1 | bundled 2026.07.16.2 is newer than published 2026.07.16.1; a Herdr client ignores the older remote copy |
| `hermes` | published | 2026.07.24.1 | 2026.07.24.1 | published and bundled are both 2026.07.24.1; a Herdr client prefers the remote copy |
| `kilo` | published | 2026.06.10.1 | 2026.06.10.1 | published and bundled are both 2026.06.10.1; a Herdr client prefers the remote copy |
| `kimi` | published | 2026.06.10.1 | 2026.06.10.1 | published and bundled are both 2026.06.10.1; a Herdr client prefers the remote copy |
| `kiro` | published | 2026.08.01.1 | 2026.08.01.1 | published and bundled are both 2026.08.01.1; a Herdr client prefers the remote copy |
| `maki` | published | 2026.07.09.2 | 2026.07.09.2 | published and bundled are both 2026.07.09.2; a Herdr client prefers the remote copy |
| `muse` | bundled | 2026.08.26.1 | — | bundled only; the published catalog does not list this agent |
| `opencode` | published | 2026.06.10.1 | 2026.06.10.1 | published and bundled are both 2026.06.10.1; a Herdr client prefers the remote copy |
| `pi` | published | 2026.06.10.1 | 2026.06.10.1 | published and bundled are both 2026.06.10.1; a Herdr client prefers the remote copy |
| `qodercli` | published | 2026.06.10.1 | 2026.06.10.1 | published and bundled are both 2026.06.10.1; a Herdr client prefers the remote copy |
| `qwen` | published | 2026.08.14.1 | 2026.08.14.1 | published and bundled are both 2026.08.14.1; a Herdr client prefers the remote copy |

## Regex compatibility

4 pattern(s) that Rust's `regex` crate accepts cannot compile under Go's RE2. The vendored files keep them verbatim; an overlay carries the rewrite. See `docs/reference/herdr-detection-parity.md`.

- `upstream/antigravity.toml` rule `spinner_working` line_regex: `^\s*[\u2800-\u28FF]+\s+\p{Alphabetic}+\w*ing\b`
  - error parsing regexp: invalid character class range: `\p{Alphabetic}`
- `upstream/cursor.toml` rule `spinner_working` line_regex: `^\s*(⬡|⬢|[\u2800-\u28FF]+)\s+\p{Alphabetic}+\w*ing\b`
  - error parsing regexp: invalid character class range: `\p{Alphabetic}`
- `upstream/kiro.toml` rule `tool_spinner_working` line_regex: `^\s*(◔|◑|◕|●)\s+\p{Alphabetic}`
  - error parsing regexp: invalid character class range: `\p{Alphabetic}`
- `upstream/qodercli.toml` rule `spinner_working` line_regex: `^\s*[\u2800-\u28FF]\s+.*\p{Alphabetic}`
  - error parsing regexp: invalid character class range: `\p{Alphabetic}`

## Alias table

23 agents in Herdr's `lookup_agent`; generic runtimes: `bash`, `bun`, `cmd`, `fish`, `node`, `powershell`, `pwsh`, `sh`, `tmux`, `zsh` (plus python, or python<segment>[.<segment>...] where every dot-separated segment after the prefix is a non-empty run of ASCII digits (is_python_runtime)).

Every Herdr alias for a family Sidecar already claims appears literally in `internal/agentactivity/activity.go`.

## Authority gaps

Herdr's published authority is a *target*. Sidecar tiers are earned by traces and are never copied.

| Agent | Herdr authority | Sidecar tier | Below target |
| --- | --- | --- | --- |
| `agy` | session_identity | screen-fallback | yes |
| `amp` | none | screen-fallback |  |
| `claude` | session_identity | session-identity |  |
| `cline` | none | — |  |
| `codex` | session_identity | session-identity |  |
| `copilot` | session_identity | screen-fallback | yes |
| `cursor` | session_identity | screen-fallback | yes |
| `devin` | session_identity | — | yes |
| `droid` | session_identity | — | yes |
| `gemini` | none | — |  |
| `grok` | session_identity | screen-fallback | yes |
| `hermes` | session_identity | — | yes |
| `kilo` | hooks | — | yes |
| `kimi` | hooks | — | yes |
| `kiro` | none | — |  |
| `maki` | none | — |  |
| `mastracode` | hooks | — | yes |
| `muse` | none | screen-fallback |  |
| `omp` | hooks | — | yes |
| `opencode` | hooks | full |  |
| `pi` | hooks | session-identity | yes |
| `qodercli` | session_identity | — | yes |
| `qwen` | session_identity | — | yes |

## Integration asset versions

Vendoring the assets themselves is Phase 3; these are the `HERDR_INTEGRATION_VERSION` values upstream carries today, so a bump is visible before the assets land here.

| Agent | Asset directory | Version |
| --- | --- | --- |
| `agy` | `antigravity_cli` | 3 |
| `claude` | `claude` | 9 |
| `codex` | `codex` | 8 |
| `copilot` | `copilot` | 3 |
| `cursor` | `cursor` | 1 |
| `devin` | `devin` | 2 |
| `droid` | `droid` | 3 |
| `grok` | `grok` | 1 |
| `hermes` | `hermes` | 5 |
| `kilo` | `kilo` | 4 |
| `kimi` | `kimi` | 7 |
| `mastracode` | `mastracode` | 2 |
| `omp` | `omp` | 9 |
| `opencode` | `opencode` | 10 |
| `pi` | `pi` | 8 |
| `qodercli` | `qodercli` | 3 |
| `qwen` | `qwen` | 1 |

## Fixture verdict flips

Engine not yet wired; see Phase 1.
