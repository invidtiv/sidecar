# Herdr detection parity: run their manifests, sync automatically, keep our edge

**Status:** draft for review, 2026-09-01. Nothing here is implemented. **Research baseline:** Sidecar `main` at the head of `claude/tui-lifecycle-herdr-parity-cu2d9t`; Herdr `e2b85c7` (`preview-2026-08-31`, ten commits past v0.8.2), the live catalog at `https://herdr.dev/agent-detection/index.toml`, and Herdr's Apache-2.0 `LICENSE`. **Open questions for the user are collected at the end; the phases are written so that Phases 0–3 do not depend on their answers.**

Related plans:

- [Deterministic agent lifecycle hooks](notification-agent-lifecycle-hooks.md) owns provider hooks, the report contract, authority arbitration, and the capability matrix. This plan does not touch the resolver or the tiers; it changes what the *screen* lane feeds into them, and it adds a Herdr-relative target for the *hooks* lane.
- [Herdr agent control and session restore](herdr-agent-control-and-session-restore.md) owns `sidecar agent start/prompt/wait/read`. Those verbs consume `agentactivity.Result`; they inherit anything this plan improves without change.
- [Workspace agent activity status (td-48ecf2)](../implemented/td-48ecf2-workspace-agent-activity-status.md) built `internal/agentactivity` by hand-porting four Herdr manifests. It said: "A Go data table is acceptable for v1; use embedded TOML only if re-harvested rule churn proves that data updates are materially easier than code review." This plan is the finding that the churn has proved it.
- [Adding new agent CLIs](../../guides/active/adding-new-agent-clis.md) is the seven-subsystem guide. Phase 4 changes Step 2 of that guide from "write a Go rule table" to "vendor a manifest and register an alias".

## One sentence

**Sidecar should execute Herdr's published detection manifests directly instead of hand-translating them, pull them on a schedule with a review gate that shows exactly which of our own fixtures changed verdict, layer Sidecar-only improvements on top as data in the same grammar, and measure the hooks lane against Herdr's per-agent authority table so falling behind is a failing check rather than a feeling.**

## Why now

Sidecar detects agent state in two lanes. The hooks lane is deterministic where a provider ships a complete lifecycle contract; today that is OpenCode only, with Codex, Claude, and Pi at session-identity. The screen lane, `internal/agentactivity`, covers everything else and is the permanent fallback. It was built by reading Herdr's TOML manifests and re-expressing each rule as a Go `Rule` literal. That worked for a one-time port and has been losing ground since:

- **Herdr's manifests are data and ours are code.** Herdr ships 21 screen manifests (144 rules) and republishes them to `herdr.dev` so released binaries pick up rule fixes without a release. Sidecar has 10 provider files, each a hand-written approximation of a Herdr manifest pinned at a date in a comment (`amp 2026.07.09.1`, `copilot 2026.07.07.1`, `cursor 2026.08.03.1`, `grok 2026.07.16.2`, `opencode 2026.06.10.1`, `pi 2026.06.10.1`). Herdr's `claude.toml` is at `2026.08.29.1`, `codex.toml` at `2026.08.28.1`, `github-copilot.toml` at `2026.08.29.1`. Every one of those bumps is a fix we have to notice, read, translate, and test by hand.
- **Concrete drift exists today.** Claude Code 2.1.228 switched its busy title spinner from braille to half-circle glyphs (U+25D0–U+25D3). Herdr's `osc_title_working` matches both; Sidecar's `claudeTitleWorking` matches only braille, so on a current Claude the title rule falls through and idle detection has to carry the turn. Herdr also has rules Sidecar has no equivalent for: Claude's MCP elicitation dialog, `/btw` overlay, `Waiting for N background agents`, `N MCP tasks still running`; Codex's trust-directory prompt (a top-of-buffer region), prompt-marker-relative regions that stop stale scrollback from producing false blockers, and a working fallback that survives customised static titles.
- **Herdr covers 23 agents; Sidecar covers 10.** Missing on our side: Gemini CLI, Cline, Devin, Droid, Kimi, Kiro, Kilo, Hermes, Qoder, Qwen, Maki, and OMP. Adding one today costs a Go file, a process-name case, a dispatch case, a `Supports` case, a catalog family, theme colours in every curated theme, and fixtures. Most of that cost is not detection.
- **Their grammar is small and closed.** Four states, integer priority with first-match-wins on ties, fifteen named regions, three matcher kinds (`contains`, `regex`, `line_regex`), three gate kinds (`all`, `any`, `not`) with a depth cap of eight, three `visible_*` flags, `skip_state_update`, and a validator with hard limits (128 rules per manifest, 512 gates, 1024 matchers, 512 chars per matcher). Sidecar's `Rule` already models a subset of it; the missing pieces are regions and nested gates. Executing the grammar is less code than we already spend approximating it.

The trend outside both projects points the same way. Prime Agent ships a built-in Herdr state reporter; ClawTab publishes a dated per-provider hook compatibility matrix and says outright that "hook support changes faster than tmux itself". Agents and the tools around them are converging on Herdr's vocabulary. Riding that is cheaper than racing it.

## Decision first

1. **Execute Herdr manifests verbatim.** Add a manifest engine in Go that implements Herdr's manifest grammar at engine version 3 with identical semantics, validates with the same limits, and produces an `explain` record with the same fields Herdr's does. It replaces the per-provider `Rule` tables as the screen-lane classifier. The engine is state-free; the `Tracker` (debounce, `done`, `IdleInferred`, skip retention cap) and the `agentlifecycle` resolver are untouched.
2. **Vendor, do not fetch, by default.** Herdr's manifests are committed under a Sidecar path with a lock file recording upstream commit, catalog ETag, per-file digest, and manifest version. A sync tool refreshes them from both the Herdr repository and the live catalog. Runtime network fetch is not part of this plan's default behaviour.
3. **Sidecar improvements are overlays in the same grammar.** Anything Sidecar knows that Herdr does not lives in `sidecar/<agent>.toml`, merged by rule id over the vendored manifest. An overlay can add rules, replace a rule, raise or lower a priority, or disable a rule. The vendored files are never edited, so a re-sync is a clean file replacement and the overlay's diff against upstream is the exact list of things we believe we do better.
4. **The fixture corpus is the parity gate.** Sidecar has real, sanitized screen fixtures with expected verdicts for ten providers; Herdr deliberately keeps none (its `AGENTS.md` says to tune rules against live panes, not fixture suites). Every sync runs the corpus against the old and new manifests and the review shows only the verdict flips. That corpus, plus a differential run against a real Herdr binary, is what "parity" means here.
5. **Measure the hooks lane against Herdr's authority table.** Herdr publishes which agents have lifecycle authority through hooks (Pi, OMP, Kimi, OpenCode, Kilo, MastraCode) and which are session-identity only (eleven more). Record that table beside `capabilities.json` as a *target*, and let a test list every provider where Sidecar's proved tier is below Herdr's. The existing plan's rule stands: a tier is earned by traces, never copied. The target only makes the gap visible.
6. **Process identity stays code, tracked by extraction.** Like Herdr, adding a brand-new agent still needs a binary change for process names and labels. The sync tool extracts Herdr's alias table from `src/detect/mod.rs` and its generic-runtime list, and a test asserts Sidecar's `identifyProcessName` recognises every alias for every family Sidecar claims.

What this plan does **not** do: it does not remove screen detection's role as the permanent fallback, does not change the hooks contract or the resolver, does not add a daemon or a listening socket, does not fetch from the network at runtime unless a later opt-in phase is approved, and does not copy Herdr's hook assets (they speak Herdr's socket API; ours speak `sidecar agent report`). It also does not adopt Herdr's positional pane ids, `done` semantics, or aggregate rollups; Sidecar's are already at or ahead of parity there.

## What Herdr actually has (research record)

Recorded so the later phases have a fixed reference. Paths are in the Herdr checkout at `e2b85c7`.

**Manifest grammar** (`src/detect/manifest.rs`, `scripts/agent_detection_manifest_check.py`):

| Element | Semantics |
| --- | --- |
| `id`, `version`, `min_engine_version`, `updated_at`, `aliases`, `rules` | Only these top-level keys; unknown keys reject the file. `version` is dotted numeric, compared segment-wise. Remote manifests must declare `min_engine_version`; a file requiring a newer engine is ignored. |
| `rules[].state` | `idle`, `working`, `blocked`, `unknown`. `skip_state_update` is only valid with `unknown` and no `visible_*` flag. |
| `rules[].priority` | Integer. Every rule is evaluated; the highest priority match wins; on a tie the earlier rule keeps it. No match on a known agent falls back to `idle` with reason `default_known_agent_idle_fallback`. |
| `rules[].region` | `whole_recent`, `whole_recent_without_current_prompt_marker`, `after_last_prompt_marker`, `before_current_prompt_marker`, `current_prompt_block_marker`, `after_current_prompt_block_marker`, `prompt_box_body`, `above_prompt_box`, `last_non_empty_above_prompt_box`, `after_last_horizontal_rule`, `osc_title`, `osc_progress`, `bottom_lines(N)`, `bottom_non_empty_lines(N)`, `top_non_empty_lines(N)` (engine 3). The prompt-marker regions are Codex-shaped (`›` composer line); the prompt-box regions are Claude-shaped (horizontal-rule box). |
| Matchers | `contains` is case-insensitive over the region; `regex` matches the region text; `line_regex` matches if any single line matches. All matchers in a gate must hold. |
| Gates | `all` (every nested gate), `any` (at least one), `not` (none). Nest to depth 8. |
| Limits | 128 rules, 512 gates, 32 matchers per gate, 1024 matchers, 512 chars per matcher. |

Usage across the 21 bundled manifests: 43 rules on `whole_recent`, 16 on `osc_title`, 4 on `osc_progress`, 2 on prompt-marker regions, 3 on prompt-box regions, 2 on `after_last_horizontal_rule`, 2 on `top_non_empty_lines`, the rest on `bottom_non_empty_lines(N)` for N in 1–30. Engine version: 14 manifests at 1, 5 at 2 (OSC regions), 2 at 3.

**Distribution** (`distribution/agent-detection/`, `src/detect/manifest_update.rs`): a `schema_version = 1` `index.toml` lists `{id, path}` per agent; clients fetch each file (256 KiB cap), validate, and cache under the state directory. Precedence is local override (`~/.config/herdr/agent-detection/<agent>.toml`) over the newer of cached-remote and bundled. Published and bundled copies may intentionally differ behind an exact version-and-digest exception (today: `grok` published at `2026.07.16.2` versus bundled `2026.07.16.1`; `muse` bundled but unpublished until a stable release ships its process identity). Their validator enforces that these exceptions are explicit.

**Process identity** (`src/detect/mod.rs`): a single `lookup_agent` alias table (for example `"claude" | "claude-code"`, `"cursor" | "cursor-agent"`, `"opencode" | "opencode2" | "open-code"`, `"qodercli" | "qoderclicn" | "qoder" | "qodercn"`, `muse-bin-<digit>…`), a generic-runtime list (`sh bash zsh fish tmux node bun cmd powershell pwsh` plus `python[3[.N]]`), argv-based unwrapping of runtimes, and a foreground-process-group scan that prefers the group leader and scores non-runtime matches higher. `HERDR_AGENT=<agent>` on a wrapper command names the manifest to use when a sandbox hides the process.

**Hooks and authority** (`docs/preview/website/src/content/docs/agents.mdx`, `integrations.mdx`, `src/integration/registry.rs`): 17 installable integrations. Lifecycle authority through hooks: Pi, OMP, Kimi, OpenCode, Kilo, MastraCode. Session identity only, state from screen: Claude, Codex, Copilot, Devin, Droid, Qoder, Qwen, Cursor, Hermes, Antigravity, Grok. No integration: Amp, Kiro, Maki, Muse, Gemini, Cline. Integration assets carry `HERDR_INTEGRATION_VERSION` (Claude 6, Codex 8 in-repo, and so on); the changelog shows those bump several times a month as providers change their hook payloads. Reports go over a Unix socket as `pane.report_agent` / `pane.report_agent_session` / `pane.release_agent` with a per-source `seq`; the pane learns its identity from `HERDR_ENV`, `HERDR_PANE_ID`, `HERDR_BIN_PATH`, `HERDR_SOCKET_PATH`.

**Diagnostics**: `herdr agent explain <target>` and `herdr agent explain --file screen.txt --agent codex --json` print the manifest source and version, matched rule, every evaluated rule with region evidence, visible flags, skip reason, and fallback reason. `herdr agent read <pane> --source detection --format text` prints exactly the text detection saw. Both are what make manifest tuning a five-minute loop in their `AGENTS.md`.

**Churn**: six commits touched `src/detect/manifests/` between 2026-08-20 and 2026-08-31, and the 0.8.x changelogs list a detection or hook fix in nearly every release (Claude half-circle spinner, Qwen locale-independent titles, Cursor "Run Everything" false blocker, Codex reasoning-summary headers, Kiro positive idle, Copilot `ask_user`, versioned Python wrappers). That is the rate a hand-port has to keep up with.

**What Sidecar already does better, and keeps**: a real fixture corpus with provenance; `Tracker` semantics (`done = idle && !seen`, `IdleInferred` so inferred idle never announces completion, a two-minute cap on overlay retention); the `agentlifecycle` capability matrix with per-source tested version ranges and evidence tiers (stricter than Herdr's per-agent table); host-scoped identity in the report contract; and `sidecar agent explain` already reporting the authoritative source and fallback reason. None of that sits in the manifest layer, so none of it is at risk from this plan.

## Terminal constraints the engine must respect

- **`osc_title` maps to tmux `#{pane_title}`.** Sidecar already captures it atomically with the screen. Rules on `osc_title` work unchanged.
- **`osc_progress` is empty under tmux.** tmux consumes OSC 9;4 and exposes no payload, so the region always resolves to `""`. Four upstream rules (Claude `osc_progress_idle` and three others) can never match in Sidecar; the engine records that as region evidence rather than pretending. This is a known, permanent gap, already noted in `grok.go`.
- **Screen text is the SGR-stripped capture.** Herdr evaluates plain text from its own terminal model; Sidecar evaluates `capture-pane -e` output after `ansi.Strip`. Both engines see what a human sees. Phase 0 confirms there is no width, wrapping, or trailing-whitespace difference that flips a verdict, using the differential harness.
- **`whole_recent` needs a defined window.** Herdr reads the recent bottom of the buffer, not the user's viewport. Sidecar's capture includes up to 600 lines of history. Phase 0 measures Herdr's detection read window on a live pane and the engine trims trailing blank rows then bounds the tail to that many lines. `top_non_empty_lines(N)` (Codex trust prompt) reads from the top of that same window, so the window has to be the same one Herdr uses or the trust prompt will scroll out of it at a different moment.
- **`HERDR_AGENT` has a Sidecar equivalent.** Managed shells already know their launch agent kind; a `SIDECAR_AGENT` hint on a wrapper command covers the sandbox case the same way, and it is a process-identity hint only, never a lifecycle claim.

## The journeys this plan must make real

### 1. A Herdr rule fix lands in Sidecar without anyone translating it

Herdr publishes `claude.toml 2026.09.03.1` adding a new permission-prompt shape. The Monday sync run opens a review with three things in it: the manifest diff, the lock-file bump, and a table of Sidecar fixtures whose verdict changed under the new file (expected: none, or a listed fixture that was wrong before). A maintainer reads the diff, sees no flips, merges. The next Sidecar release detects the new prompt. Nobody wrote a regex.

### 2. A Sidecar-only improvement survives the next sync

We notice Claude's `AskUserQuestion` renders with a ☐ glyph Herdr does not gate on and add a higher-priority rule in `sidecar/claude.toml` with a fixture proving it. Two weeks later upstream reworks `live_blocked_form`. The sync replaces the vendored file; the overlay is untouched; the corpus run shows the fixture still passes and shows whether upstream's rework made our overlay redundant (same verdict with the overlay disabled). If it is redundant, the review says so and we delete it. If Herdr later adopts the same idea, the overlay disappears on its own.

### 3. A wrong badge is explained in one command, offline

A pane shows `idle` while Grok is clearly working. `sidecar agent explain --current --json` reports the screen lane's manifest source (`upstream grok 2026.07.16.2 + sidecar overlay`), every evaluated rule with the region text it saw, the matched rule or the fallback reason, and whether a hook source was consulted. `sidecar agent explain --file screen.txt --agent grok` reproduces it from a saved capture, which is also exactly how a new fixture is minted.

### 4. Twelve more agents show a real state badge

A user launches Gemini CLI in a Sidecar shell. `identifyProcessName` knows `gemini`; the engine loads the vendored `gemini.toml`; the pane shows working and blocked states with no Sidecar-specific code beyond the alias and a badge colour. Launch/resume support and a conversation adapter remain separate, optional work per the existing guide.

### 5. Falling behind on hooks is a failing check, not a surprise

`go test ./internal/agentlifecycle/` includes a report listing each provider where Herdr has lifecycle authority through hooks and Sidecar does not (today: Pi, Kimi, Kilo, MastraCode, OMP). The test does not fail the build; it fails only when the recorded target is stale against the vendored Herdr table, so the gap is always current and visible.

## Settled architecture

### Package layout

```
internal/agentactivity/
  manifest/                 engine: parse, validate, regions, gates, evaluate, explain
  manifests/
    upstream/<agent>.toml   vendored verbatim from Herdr (never edited)
    upstream/index.toml     vendored catalog index
    upstream/NOTICE         Apache-2.0 attribution for the vendored files
    upstream.lock.json      herdr commit, catalog ETag, per-file sha256 + version, sync time
    sidecar/<agent>.toml    Sidecar overlays in the same grammar
    aliases.upstream.json   extracted from Herdr src/detect/mod.rs by the sync tool
    authority.upstream.json extracted per-agent authority table (hooks vs screen)
  activity.go               Observation, Result, Tracker unchanged; Detect dispatches to the engine
  <provider>.go             shrinks to process gating + FallbackIdle wrapper, then is deleted per provider in Phase 2
scripts/sync-herdr.sh       thin wrapper over `go run ./internal/tools/herdrsync`
internal/tools/herdrsync/   fetch, verify, extract, write lock, render the review report
.github/workflows/herdr-sync.yml   weekly + manual dispatch
```

Manifests are embedded with `embed.FS`; the engine compiles them once at first use, not in `Init()`, per the startup-latency rule. A compile error in an overlay is a startup-visible diagnostic and that overlay is skipped; a compile error in a vendored file cannot happen because the lock test compiles every vendored file in CI.

### Engine contract

- Input is `agentactivity.Observation` plus the resolved agent id. Output is `Result` (unchanged fields) plus an `Explain` value with `ManifestSource`, `ManifestVersion`, `OverlayApplied`, `MatchedRule{ID, Priority, Region, State}`, `EvaluatedRules[]{ID, Priority, Region, State, Matched, RegionBytes, RegionPreview}`, `FallbackReason`, and `SkippedUpdateReason`. Field names follow Herdr's `explain` JSON so the differential harness can diff them structurally.
- Semantics are Herdr's, not Sidecar's current `Rule`: `contains` is case-insensitive; every rule is evaluated; highest priority wins; ties keep the earlier rule; `skip_state_update` yields `Result.SkipStateUpdate` with `State == StateUnknown`; `visible_*` flags come from the matched rule, not from the state alone. This is the single largest behavioural difference from today's `Evaluate`, which stops at the first match in file order and derives visibility from state. The characterization tests in `compat_test.go` pin today's behaviour; Phase 2 changes them deliberately, per fixture, with the reason recorded.
- Regexes compile with Go's `regexp` (RE2). Herdr uses Rust's `regex` crate; both are RE2-family with no lookaround or backreferences, and the syntax used in the 144 upstream rules (`\x{2800}`, `(?i)`, `(?m)`, `(?s)`, `\A`, `\b`, Unicode classes) is common to both. The lock test compiles every regex in every vendored manifest so an incompatible pattern fails CI on the sync PR, not in a user's pane. If one ever appears, the overlay mechanism can carry a rewritten equivalent for that rule id and the report names it.
- `min_engine_version` above Sidecar's declared engine version rejects the file at sync time, not at runtime; a rule using `top_non_empty_lines` requires engine 3 exactly as in Herdr's validator.
- Fallback: a positively identified live process with no matching rule returns `StateIdle` with `FallbackIdle: true` and evidence `default_known_agent_idle_fallback`, which is what `<provider>.known-live-fallback` means today.
- Process gating stays outside the engine: `Detect` still refuses to evaluate `claude.toml` against a pane whose foreground process is not Claude or a permitted runtime wrapper. That refusal is Sidecar's and is stricter than Herdr's; it stays.

### Overlay merge

An overlay file has the same shape as a manifest with two additions per rule: `disable = true` removes the upstream rule with that id, and a rule whose id matches an upstream id replaces it. Any other rule is appended. Overlay ids are prefixed `sidecar.` so an upstream rule can never collide with a Sidecar addition by accident. The merged result is validated with the same limits as a plain manifest. The review report renders, for each overlay rule, whether it changed any fixture verdict with the overlay on versus off; an overlay rule that changes nothing is flagged as a deletion candidate.

### Sync tool

`herdrsync` does one thing per invocation and writes only under `internal/agentactivity/manifests/`:

1. Fetch `distribution/agent-detection/*.toml` and `src/detect/manifests/*.toml` at a pinned Herdr ref (default: the newest tag, override with `--ref`), and the live catalog `index.toml` plus every listed file from `herdr.dev` with its ETag. Cap at 256 KiB per file like Herdr does.
2. Validate every file with the ported validator. Refuse the whole sync if any file fails.
3. Per agent, choose the newer of bundled and published, exactly as a Herdr client would, and record which won and why. Where the two differ and the published one is older (the `grok` case), keep the bundled one and say so.
4. Extract `lookup_agent` and `is_generic_runtime_or_shell` from `src/detect/mod.rs` into `aliases.upstream.json`, and the authority table from `agents.mdx` into `authority.upstream.json`. Extraction is a regex over stable Rust match-arm shapes; if the shape changes the tool fails loudly and the previous JSON stands.
5. Also fetch `src/integration/assets/**` and `src/integration/registry.rs` into a scratch tree and diff `HERDR_INTEGRATION_VERSION` per target against the last lock; report bumps for any provider Sidecar has an `agentintegration` adapter for. This is a heads-up that a provider's hook payload changed, not a code change.
6. Write `upstream.lock.json` and render `report.md`: version bumps, per-file diffs, alias table additions Sidecar lacks, authority gaps, integration version bumps, and the fixture-corpus verdict flips.

A `TestVendoredManifestsMatchLock` test hashes the vendored files against the lock so a hand edit to a vendored file fails CI; edits belong in overlays.

### Workflow

`.github/workflows/herdr-sync.yml` runs weekly and on dispatch, runs `herdrsync`, and if anything changed opens a pull request from `bot/herdr-sync` with `report.md` as the body. A second job in that workflow, not in ordinary CI, builds or downloads a Herdr binary and runs the differential harness (below). Ordinary Go CI never needs Rust or the network.

### Differential harness

For every fixture in `internal/agentactivity/testdata/**` with a `screen:` block, run `herdr agent explain --file <screen> --agent <agent> --json` and Sidecar's `sidecar agent explain --file <screen> --agent <agent> --json` against the *same* vendored manifest (Herdr's local override directory is pointed at our vendored copy so both engines read one file), and diff the `state`, `matched_rule.id`, and `fallback_reason`. Disagreements are engine bugs by definition and fail the sync workflow. Because it uses Herdr's own `--file` mode, no pane, tmux, or agent binary is needed.

### Local overrides

`~/.config/sidecar/agent-detection/<agent>.toml`, same precedence as Herdr: a valid local override wins over vendored+overlay for that agent; an invalid one is ignored with a warning in `explain`. This gives a user the same five-minute tuning loop Herdr's `AGENTS.md` describes, and it is how a fix gets proved before it becomes an overlay or an upstream contribution. No hot reload in v1; `explain` reports the loaded source so a stale process is obvious.

### Contributing upstream

When a Sidecar overlay fixes something Herdr also gets wrong, the preferred outcome is a Herdr pull request carrying the rule and a sanitized fixture, so the overlay can be deleted on the next sync. The report's "overlay changes nothing" flag is the signal that this has happened.

## Work sequence

### Phase 0 — Vendor and measure (no behaviour change)

- Build `herdrsync` fetch/validate/lock/report. Vendor the 21 manifests, `index.toml`, `NOTICE`, `aliases.upstream.json`, `authority.upstream.json`.
- Port Herdr's validator limits and rules as `manifest.Validate`; `TestAllVendoredManifestsParseAndValidate` and `TestVendoredManifestsMatchLock`.
- Compile every upstream regex under Go `regexp`; record any incompatibility.
- Measure Herdr's detection read window on a live pane (`herdr agent read --source detection`) and record the line count for the engine's `whole_recent` bound.
- Alias parity report: which upstream aliases `identifyProcessName` does not recognise for families Sidecar already claims. Fix the ones for existing families in this phase (they are one-line cases).
- **Exit gate:** vendored tree committed, lock test green, regex compatibility report attached, read window recorded.

### Phase 1 — Engine, explain, conformance, shadow mode

- Implement regions, gates, evaluation, fallback, and `Explain` per the contract above.
- Port Herdr's 45 inline manifest tests from `src/detect/manifest/tests.rs` as a Go table; they are the grammar's executable specification.
- Run the engine over every Sidecar fixture and record verdicts beside today's `Rule` verdicts. Every disagreement is triaged into: engine bug (fix), upstream rule better (accept, update fixture expectation with reason), Sidecar rule better (write an overlay with the fixture proving it).
- Add `--file PATH --agent KIND` to `sidecar agent explain`, and have the live explain carry the screen-lane `Explain` alongside the existing authority fields.
- Ship the differential harness in the sync workflow.
- **Shadow mode** behind `features.ManifestDetection` (default off): production polls run both classifiers and log disagreements with evidence ids to the lifecycle diagnostics; nothing user-visible changes. Run it on the maintainer's machines for at least a week across the providers actually in use.
- **Exit gate:** ported conformance suite green, differential harness green over the whole corpus, shadow disagreements triaged to zero or to an overlay with a fixture.

### Phase 2 — Cutover per provider

- One provider at a time, in the order of live use (Claude, Codex, OpenCode, Cursor, Grok, Pi, Copilot, Amp, Antigravity, Muse): flip `Detect` to the engine for that provider, delete its Go rule table, keep the process-gating wrapper, update the `compat_test.go` characterization fixtures deliberately with the reason for each change.
- Overlays written in Phase 1 are the only Sidecar-owned rules left. Each has a fixture.
- Remove the feature flag once all ten are cut over.
- **Exit gate:** `internal/agentactivity/<provider>.go` contains no `Rule` literals; corpus green; live matrix (`SIDECAR_LIVE_AGENT_MATRIX`) passes for the providers it passed before.

### Phase 3 — Automation and overrides

- Land the weekly workflow with pull-request creation and the report body.
- Local override directory and its `explain` reporting.
- Integration-version bump detection in the report for OpenCode, Codex, Claude.
- A staleness check in the workflow (not in Go CI): if the live catalog's version for any agent is newer than the lock for more than 14 days with no open sync PR, the run fails so it shows up.
- **Exit gate:** one real sync PR produced by the workflow, reviewed and merged, with a verdict-flip table in its body (possibly empty).

### Phase 4 — Coverage expansion

- Add a `DetectionOnly bool` to `agentcatalog.Family` (or a parallel `DetectionFamilies` list; decide during implementation, the vocabulary tests decide which is less invasive). A detection-only family needs an id, aliases, a display name, and a badge colour. It is not offered in creation pickers and has no resume or adapter.
- Register Gemini, Cline, Devin, Droid, Kimi, Kiro, Kilo, Hermes, Qoder, Qwen, Maki as detection-only, each with its vendored manifest. OMP is hooks-only upstream and is out of scope until a Sidecar integration exists.
- Update the "Adding new agent CLIs" guide: Step 2 becomes "sync or vendor the manifest, add the alias, add a fixture from `explain --file`". The seven-subsystem picture stays for full families.
- Authority target test: `TestHerdrAuthorityGaps` lists providers where `authority.upstream.json` says lifecycle-through-hooks and `capabilities.json` says below `full`. It fails only if `authority.upstream.json` and the lock disagree, so the list is always current.
- **Exit gate:** every agent in Herdr's `SCREEN_MANIFEST_AGENTS` (21) has a Sidecar identity and a manifest; a fresh fixture per new agent is not required for the cutover but the guide says how to mint one.

### Phase 5 — Opt-in extensions (each gated on a user answer below)

- **Runtime catalog fetch.** `detection.remoteManifests: "off" | "herdr.dev" | "<url>"` with Herdr's precedence rules, 256 KiB cap, cache under the state directory, and `explain` reporting the remote version. Off by default.
- **Herdr reporter compatibility.** A `sidecar herdr-compat` shim and, in managed shells, `HERDR_ENV=1`, `HERDR_PANE_ID=<managed target>`, `HERDR_BIN_PATH=<shim>`, and a socket path the shim serves, translating `pane.report_agent` / `pane.report_agent_session` / `pane.release_agent` into `sidecar agent report` / `end` / `release` with source prefix `herdr-compat:`. Reports enter as `advisory` tier by the existing rules; nothing here grants authority. This would make agents that ship native Herdr reporters (Prime Agent today) light up in Sidecar, and would let Herdr's own hook assets work unmodified. It also means a Sidecar shell claims to be Herdr to any process that checks, which is wrong if the user runs Sidecar inside Herdr or vice versa. Default off; the shim refuses to start if a real `HERDR_SOCKET_PATH` is already in the environment.

## Test matrix

- **Grammar:** the 45 ported Herdr tests; region resolution for every named region against synthetic screens; `contains` case-insensitivity; tie-break keeps earlier rule; `skip_state_update` validation; every limit rejects at limit+1.
- **Vendoring:** lock digest test; every vendored regex compiles; `min_engine_version` above ours rejects; published-vs-bundled choice recorded in the lock matches what the tool decided.
- **Corpus:** every fixture in `testdata/**` has an expected `state`, `evidence` (rule id), and visible flags; the engine matches all of them after Phase 2; the differential harness agrees with Herdr on `state`, `matched_rule.id`, `fallback_reason`.
- **Overlays:** merge by id, `disable`, append, prefix enforcement, overlay-off comparison per rule.
- **Identity:** every alias in `aliases.upstream.json` for a Sidecar family resolves in `identifyProcessName`; the generic-runtime list is a superset of Herdr's on Linux and Darwin.
- **Tracker unchanged:** `compat_test.go` `Tracker` fixtures pass byte-identical before and after Phase 2; only the `Detect` verdict fixtures change, each with a recorded reason.
- **Explain:** `--file` reproduces a live verdict from its saved screen; JSON field names match Herdr's for the shared fields.
- **Workflow:** a dry run against a pinned older Herdr ref produces a report whose verdict-flip table matches a checked-in expectation.

## Acceptance evidence

- A sync PR opened by the workflow, with the report body, merged with no manual manifest edits.
- `git grep 'Rule{' internal/agentactivity/*.go` returns nothing.
- `sidecar agent explain --file internal/agentactivity/testdata/claude/blocked.txt --agent claude --json` and the Herdr equivalent produce the same `state` and `matched_rule.id`.
- A Claude Code 2.1.228 or newer pane shows `working` from its half-circle title spinner with no Sidecar rule authored for it.
- A Gemini CLI pane shows `blocked` on its confirmation prompt with no Sidecar regex authored for it.

## Risks and how the plan bounds them

- **Semantics drift between the two engines.** Bounded by the ported conformance tests and the differential harness over real fixtures, run on every sync. If Herdr adds a region or matcher kind, the validator rejects the file at sync time and the report says which rule needs engine work before it can be vendored.
- **Regex dialect.** Bounded by compiling every upstream pattern in CI; the overlay carries a rewritten rule if one ever fails.
- **Losing Sidecar behaviour in the cutover.** Bounded by the characterization fixtures: every verdict change is a deliberate, commented edit. The `Tracker` and resolver are outside the change.
- **Trusting upstream data.** Manifests are regex and literals, not code; the validator caps size and depth; Go's RE2 has no pathological backtracking. Vendored files are reviewed in a PR, never fetched at runtime by default.
- **Herdr changes course.** If manifests stop being published, the vendored tree keeps working and overlays become ordinary rules. Nothing in Sidecar's runtime depends on herdr.dev being up.
- **Attribution.** Apache-2.0 requires the licence and notice to travel with the vendored files; `NOTICE` under `manifests/upstream/` and a line in the repository's third-party notices cover it.

## Other projects surveyed

None publishes a machine-readable, versioned detection catalog the way Herdr does, which confirms the premise that Herdr is the right upstream. Worth knowing anyway:

- **ClawTab** (Tauri, tmux status bar): hooks-first for Claude Code, Codex, OpenCode with terminal parsing as fallback, three states, and a *dated* per-provider compatibility matrix with the explicit caveat that hook support changes faster than tmux. The dated matrix is a practice worth keeping in our capability matrix, which already records `testedProviderRange`.
- **agent-deck**: Claude via hook plus transcript reading, Codex via its `notify` hook, everything else by output parsing marked "untested". Nothing to borrow beyond confirmation that Codex `notify` is the community's session-identity path.
- **claude-code-kanban**, **Claude-Code-Agent-Monitor**: hooks-only, Claude-only, web dashboards. Their "awaiting reason" tooltips (`Needs input`, `Turn done`, `At prompt`, `Interrupted`) are a nice presentation of the same four-state model.
- **claude-squad**, **amux**, **dmux**, **repomon**, **thurbox**, **agterm**, **cmux**: worktree and session managers over tmux or a native terminal. They show attention state coarsely and do not publish rules.
- **vibe-kanban**: drives agents through their non-interactive JSON/stream modes rather than reading TUIs, so it sidesteps the problem instead of solving it. Not applicable to embedded interactive panes.
- **Prime Agent**: ships a built-in Herdr reporter that activates on `HERDR_ENV=1`. The first concrete sign that agents will report to whichever protocol is dominant, and the motivation for the Phase 5 compatibility question.

## Open questions

1. **Authority of vendored manifests.** This plan makes Herdr's manifests the screen-lane classifier and demotes Sidecar's own rules to overlays. Confirm that is the intended trade: fewer hand-written rules and a review gate on every upstream change, in exchange for accepting upstream's judgement by default.
2. **Runtime fetch.** Phase 5 proposes an opt-in remote catalog. Should the default stay "vendored only", or is opt-in-at-install acceptable so users get rule fixes between Sidecar releases the way Herdr users do?
3. **Herdr reporter compatibility.** Pursue the `HERDR_*` compatibility shim so agents with native Herdr reporters (and Herdr's hook assets) work in Sidecar shells, accepting that a Sidecar pane then claims to be Herdr? Or leave it out and keep Sidecar's own env vocabulary only?
4. **Which new agents deserve full families.** Detection-only covers all eleven cheaply. Which of Gemini, Cline, Devin, Droid, Kimi, Kiro, Kilo, Hermes, Qoder, Qwen, Maki do you actually launch, and so want in creation pickers with resume and a conversation adapter?
5. **Sync cadence and PR automation.** Weekly with an automatic pull request needs the workflow token to open PRs. Acceptable, or should the workflow open an issue and a `td` task and leave the PR to a person?
6. **Herdr binary in the differential harness.** Build from source in the weekly workflow (Rust toolchain, cacheable, slow the first time) or download their release binary? Either is fine; source tracks `main`, releases track what users run.
7. **Hooks-lane targets.** Should Phase 4's authority-gap report become a tracked goal (trace Pi, Kimi, Kilo, MastraCode hooks to `full` per the lifecycle plan's rules), or stay informational until a user asks for one of those providers?
