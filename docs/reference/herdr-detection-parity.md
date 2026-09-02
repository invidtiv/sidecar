# Herdr detection parity: vendored tree, lock, and regex compatibility

Reference for the vendored Herdr agent-detection manifests and what was measured about them. The plan is `docs/plans/active/herdr-detection-parity.md`; this document records facts, not intent.

## Vendored tree and lock

`internal/agentactivity/manifests/upstream/` holds byte-for-byte copies of Herdr's 21 agent-detection manifests plus the published catalog `index.toml`, an Apache-2.0 `NOTICE`, and Herdr's `LICENSE`. They come from Herdr commit `e2b85c73615b37a483eefa839923d9aff8e629b3` (`e2b85c7`) at `https://github.com/herdrdev/herdr`, cross-checked against the live catalog at `https://herdr.dev/agent-detection/index.toml`. The differential harness in later phases runs against Herdr release `v0.8.2`, which is recorded in the lock as `pinned_release_tag`.

`upstream.lock.json` pins every file: sha256, byte length, manifest version, `min_engine_version`, rule count, alias list, whether the bundled or the published copy won and why, and any regex pattern in that file which Go's RE2 cannot compile. Its `files` section pins the two vendored files that are not manifests, `upstream/LICENSE` and the generated `upstream/NOTICE`, by the same sha256 and byte length. `TestVendoredManifestsMatchLock` hashes each embedded manifest against the lock and `TestVendoredAttributionFilesMatchLock` hashes those two, so a hand edit to any vendored file fails CI. Edits belong in an overlay under `internal/agentactivity/manifests/sidecar/`; see the README there.

Per-agent the tool chooses the copy a Herdr client would load: the published one unless it is older than the bundled one. Nineteen agents have identical published and bundled versions and the published copy is vendored. Two are exceptions, both of them deliberate upstream:

- `grok` — bundled `2026.07.16.2` (engine 3) is newer than published `2026.07.16.1` (engine 2), so the bundled copy is vendored. Herdr stages the older published copy on purpose to keep engine-2 clients working; its own checker carries an explicit exception for it.
- `muse` — bundled only at `2026.08.26.1`. Herdr does not publish it until a stable binary can identify the process.

`aliases.upstream.json` carries Herdr's `lookup_agent` table (23 agents), the `is_generic_runtime_or_shell` list, the `python[N[.N]]` rule, the `muse-bin-<digit>` versioned-binary prefix, and the suffixes `normalized_agent_lookup_name` strips. `authority.upstream.json` carries Herdr's published per-agent authority table with each provider's `HERDR_INTEGRATION_VERSION`: six agents where hooks are the state authority (`kilo`, `kimi`, `mastracode`, `omp`, `opencode`, `pi`), eleven where the integration supplies session identity only, and six with no integration. Both are extracted by regex over stable Rust and MDX shapes, and the extractor fails loudly rather than writing an empty table if a shape changes.

### Re-syncing

```bash
scripts/sync-herdr.sh                                        # newest Herdr release tag + live catalog
scripts/sync-herdr.sh --ref main                              # track main instead
scripts/sync-herdr.sh --source-dir ~/code/herdr --ref e2b85c7 # a local checkout, live catalog
scripts/sync-herdr.sh --source-dir ~/code/herdr --offline     # no network at all
```

The wrapper is a thin shell over `go run ./internal/tools/herdrsync`. With `--source-dir` it reads every file with `git show <ref>:<path>` inside that checkout rather than off the working tree, and refuses to run when `--ref` does not resolve there, so the bytes vendored are always the bytes of the commit the lock and the `NOTICE` record. It writes only under `internal/agentactivity/manifests`, caps every fetched file at 256 KiB the way Herdr does, validates every file before writing anything, and refuses the whole sync if a file fails validation or declares a `min_engine_version` above `manifest.EngineVersion` (3). It renders `report.md` next to the lock; that file is committed, because it is the review surface for a sync and its diff is the fastest way to see what upstream changed.

With `--offline` the published copies come from `<source-dir>/distribution/agent-detection` rather than herdr.dev, the ETag is recorded as `unknown`, and the lock carries a note saying so. That keeps the published-versus-bundled decision reproducible without a network. Vendoring the integration assets under `internal/agentintegration/upstream/` is Phase 3 and is marked TODO in the tool; the asset versions themselves are already extracted, so a bump shows up in the report before the assets land.

## Regex compatibility

The 21 vendored manifests carry **122 rules** and **83 regex patterns** (`regex` plus `line_regex`, counted across every nested gate).

**79 of 83 compile under Go's `regexp` (RE2). 4 do not.** All four failures are the same feature.

| File | Rule | Field | Pattern | Error |
| --- | --- | --- | --- | --- |
| `upstream/antigravity.toml` | `spinner_working` | `line_regex` | `^\s*[\u2800-\u28FF]+\s+\p{Alphabetic}+\w*ing\b` | invalid character class range: `\p{Alphabetic}` |
| `upstream/cursor.toml` | `spinner_working` | `line_regex` | `^\s*(⬡\|⬢\|[\u2800-\u28FF]+)\s+\p{Alphabetic}+\w*ing\b` | invalid character class range: `\p{Alphabetic}` |
| `upstream/kiro.toml` | `tool_spinner_working` | `line_regex` | `^\s*(◔\|◑\|◕\|●)\s+\p{Alphabetic}` | invalid character class range: `\p{Alphabetic}` |
| `upstream/qodercli.toml` | `spinner_working` | `line_regex` | `^\s*[\u2800-\u28FF]\s+.*\p{Alphabetic}` | invalid character class range: `\p{Alphabetic}` |

**Is there an RE2-equivalent rewrite?** Not an exact one, but a sound one. Rust's `regex` crate supports Unicode *binary properties* by name; RE2 supports only general categories (`\p{L}`, `\p{Nd}`) and scripts (`\p{Greek}`). Unicode `Alphabetic` is `L + Nl + Other_Alphabetic`, and RE2 has no `Other_Alphabetic`, whose members are combining marks and a handful of symbols. Every one of these four rules uses `\p{Alphabetic}` to mean "a spinner glyph followed by a word", so `[\p{L}\p{Nl}]` (or plainly `\p{L}`) matches every screen these rules are aimed at. The two spellings diverge only where an `Other_Alphabetic` character — in practice a combining mark — falls inside the run being matched. Where the pattern is `\p{Alphabetic}+` (`antigravity.toml`, `cursor.toml`) that means a mark anywhere in the word, not just at its start: a decomposed accented word such as `de` + U+0301 + `marrage` ends the RE2 run at the mark, where the Rust pattern runs through it. Where the pattern matches a single `\p{Alphabetic}` (`kiro.toml`, `qodercli.toml`) the divergence needs a mark in that one position: a status word that *starts* with one, or, for `qodercli.toml`'s trailing `.*\p{Alphabetic}`, a line carrying such a mark and no letter at all. Neither shape is how an agent renders a status word: terminals emit precomposed text, and no status word begins with a combining mark. The rewrite belongs in an overlay carrying the same rule id, not in the vendored file; the overlay mechanism arrives in Phase 1.

**One syntactic translation is applied, and it is lossless.** Rust accepts `\uHHHH` and `\u{H...}` as code-point escapes; RE2 spells the same thing `\x{H...}` and rejects `\u` outright. `manifest.TranslateRustRegex` rewrites those escapes before compiling. `\u2800` and `\x{2800}` denote the same single code point, so nothing about what a pattern matches changes. Eight upstream patterns use `\u`; another 17 already use `\x{...}` directly, which is why both spellings appear in the same corpus. The translation happens in the engine, never in the vendored bytes: `TestNoVendoredFileIsRewritten` asserts the `\u` escapes are still in the files.

**Syntax features present in the corpus, and whether the two engines agree.** Counts are patterns containing the feature, not occurrences.

| Feature | Patterns | Rust `regex` versus Go RE2 |
| --- | --- | --- |
| `\d` `\w` `\s` | 59 | Identical. Both are ASCII-only by default in Rust and always ASCII in RE2. |
| Literal non-ASCII characters | 38 | Identical. Both operate on UTF-8 and match by code point. |
| Character classes and ranges | 35 | Identical. |
| Alternation | 29 | Identical, but leftmost-first versus leftmost-longest differs for *submatch* extraction only. The engine asks whether a pattern matches, never which alternative won, so this cannot change a verdict. |
| `(?:...)` | 21 | Identical. |
| `(?i)` | 20 | Identical: Unicode-aware simple case folding in both. |
| `\x{...}` | 17 | Identical. |
| `\b` | 14 | **Differs at the edges.** Rust's `\b` is Unicode-aware (`\w` there is Unicode word characters for boundary purposes); RE2's `\b` is ASCII-only. The 14 patterns use it to close a Latin word such as `...ing\b`, where both agree. A boundary between two non-ASCII letters would differ; none of the corpus does that. `\b` behaviour is worth re-checking on any sync that adds a pattern applying `\b` to non-Latin text. |
| `\u{...}` / `\uHHHH` | 8 | Translated, losslessly, as described above. |
| Bounded repeats `{m,n}` | 5 | Identical. |
| `\p{...}` | 4 | **Incompatible**; the four rows in the table above. |
| `(?m)` | 2 | Identical: `^` and `$` match at line boundaries. |
| `(?s)` | 2 | Identical: `.` matches a newline. |
| `\A` | 1 | Identical. |
| Lazy quantifiers | 1 | Identical in what they match. |

No pattern uses lookaround or a backreference, which neither engine supports, so there is nothing to check there. `TestEveryVendoredRegexCompilesUnderGoRegexp` pins the four known failures by file, rule id, and field, so a *new* incompatibility fails CI on the sync pull request rather than surfacing as a silently dead rule in a user's pane.

## Read window

Measured against Herdr source at `e2b85c7` and confirmed on the 0.8.2 binary running headless on an isolated config/state directory and socket (a pane printing 2000 numbered lines in a 39-row pane returned lines 1963–2000: 38 rows, the bottom cursor row trimmed).

- The detection text is the tail of the terminal buffer, anchored at the bottom regardless of where the viewport is scrolled (`src/pane/terminal.rs:2801-2813`, `ghostty_recent_read_range`).
- N is the pane's own row count (`src/pane/terminal.rs:2616-2623`), falling back to 24 when rows are unreadable (`DEFAULT_DETECTION_ROWS`, `src/pane/terminal.rs:41`).
- Rows are wrapped physical rows (not unwrapped) and each row is right-trimmed (`ghostty_screen_row`, `src/pane/terminal.rs:2841-2865`).
- Trailing blank rows are trimmed after the window is selected; the window is not extended upward to compensate. Interior blank lines are preserved. Rows join with `\n` plus one trailing newline (`src/pane/terminal.rs:2767-2768`, `:3358-3372`).
- `whole_recent` is that text verbatim (`src/detect/manifest.rs:1297`); every other screen region derives from it. `top_non_empty_lines(N)` reads the same window from the top and returns the text up to the end of the Nth non-empty line, interleaved blanks included (`src/detect/manifest.rs:1372-1384`); it requires engine 3.
- `osc_title` is the last OSC 0 or 2 payload, capped at 256 chars, cleared by an empty payload; `osc_progress` is the last OSC 9 payload after `9;` (`src/pane/osc.rs:448-517`). Both are cleared when the pane's foreground agent changes. Under tmux `osc_progress` is always empty.
- The live detection loop and `herdr agent read --source detection` read the same function (`src/pane.rs:896`, `:2470`), so the CLI is not an approximation.

Sidecar consequence: `Observation` carries `PaneHeight` from tmux `#{pane_height}`; the engine's window is `ansi.Strip` → last `PaneHeight` rows (24 when unknown) → right-trim each row → drop trailing blank rows. Sidecar's `capture-pane` still fetches up to 600 lines of scrollback; the engine bounds the tail.

That order is load-bearing and it is upstream's. Trimming *before* windowing reaches further up the buffer than the pane can display, which is how a resolved historical prompt gets back into view; upstream trims after and never backfills, so a blank row inside the window costs a row of the budget. The empty final piece a terminating newline leaves is a real grid row — the pane's cursor row — and it is counted, which is why the measured case returns 38 rows rather than 39. `TestReadWindowIsTheTailOfTheBufferAtThePaneHeight` and `TestReadWindowTrimsTrailingBlanksWithoutExtendingUpward` pin both halves.

## Alias parity

Upstream `lookup_agent` (`src/detect/mod.rs:193-222`) normalises before lookup: lower-case, trim, strip one of `.exe .cmd .bat .ps1 .js`, take the path basename across `/` or `\` (`src/detect/mod.rs:668-683`). Sidecar's `identifyProcessName` now does the same (`normalizeProcessName` in `activity.go`), with the Claude version-string argv0 checked on the un-normalised name so `1.2.3.js` cannot read as Claude.

| Family | Upstream aliases | Added to Sidecar |
| --- | --- | --- |
| claude | `claude`, `claude-code` | `claude-code` |
| antigravity | `agy`, `antigravity`, `antigravity-cli` | `antigravity-cli` |
| opencode | `opencode`, `opencode2`, `open-code` | `opencode2` |
| codex, grok, pi, copilot, cursor, amp, muse | as upstream | none needed; Sidecar is a superset for codex (`codex-cli`), grok (`grok-*`), muse (`muse-*`) |

Generic runtimes: Herdr's list is `sh bash zsh fish tmux node bun cmd powershell pwsh python[N[.N]]`. Sidecar returns `shell` for `sh bash zsh fish nu pwsh` and `""` for the rest; nothing on Herdr's list resolves to a Sidecar family (`TestHerdrGenericRuntimesNeverResolveToAProvider`). Sidecar's `shell` bucket is a launch-readiness gate, not Herdr's process-scoring predicate, so it is deliberately not widened to `node`, `tmux`, or `python`; a separate generic-runtime predicate belongs with the argv-unwrapping work in Phase 4.

## Engine (Phase 1)

`internal/agentactivity/manifest` executes the vendored manifests at engine version 3: `ReadWindow` builds the detection window measured above, `resolve.go` implements all fifteen regions with a citation to the Herdr line each helper ports, `compile.go` compiles every regex once (after `TranslateRustRegex`) and marks an uncompilable rule as never matching with a `regex_incompatible` note in its explain evidence, `evaluate.go` runs every rule and keeps the highest priority with ties to the earlier rule, and `merge.go` applies a `sidecar/<agent>.toml` overlay by rule id. `internal/agentactivity/manifests.Load` compiles upstream plus overlay once per agent behind a `sync.Once`; a broken overlay becomes a diagnostic on the returned `Source` and upstream is used alone.

Three engine details are Sidecar's own and have no upstream counterpart, because Sidecar's inputs are not Herdr's:

- **The `osc_title` region is ANSI-stripped.** Herdr's `osc_title` is a decoded OSC 0/2 payload and carries no SGR by construction; Sidecar's is tmux `#{pane_title}`, which hands back whatever bytes the program wrote. Every upstream `osc_title` rule is anchored at the start of the title, so an unstripped escape ahead of a coloured spinner glyph turns the rule into a permanent no-match and a working pane reads as idle. On a title with no escapes the strip is identity.
- **`contains` folds with the full Unicode lowercase algorithm**, not `strings.ToLower`. Herdr uses Rust's `str::to_lowercase`, which applies SpecialCasing (U+0130 lowers to two runes) and the Final_Sigma condition (a Σ ending a word lowers to ς); Go's simple per-rune fold does neither, so a needle and a screen Herdr separates would match here. `golang.org/x/text/cases` with `language.Und` is used for both the needle and the region. No vendored needle diverges today; `TestContainsFoldsCaseTheWayRustDoes` is what keeps a sync from introducing one silently.
- **An uncompilable regex kills its whole rule, not just the pattern.** Inside a `not` gate that is the opposite of Herdr, which would evaluate the pattern, fail to match it, satisfy the `not` and usually fire the rule. Skipping is the safer direction and it is unreachable today: all four incompatible patterns are positive `line_regex` matchers with an overlay rewrite each, which `TestOverlaysMakeEveryRuleCompilable` enforces.

An overlay must declare the id of the manifest it amends; a mismatch is refused rather than merged, because the loader keys on the file name and the merged manifest keeps upstream's id, so nothing else would notice a misfiled overlay.

`conformance_test.go` ports 36 of Herdr's 45 inline manifest tests under their Rust names. The nine not ported exercise Herdr's remote-manifest cache and override loader (six, no Sidecar equivalent until Phase 5), duplicate cases covered elsewhere (two), and one loader-failure test whose analogue is `TestOverlayFailureFallsBackToUpstream`.

Six overlays exist today. Four are one per `\p{Alphabetic}` rule (antigravity, cursor, kiro, qodercli), each replacing the upstream rule id with `[\p{L}\p{Nl}]`; `TestOverlaysMakeEveryRuleCompilable` proves no merged manifest carries a dead rule. The other two are the Sidecar-owned detection rules the Phase 2 cutover kept, one for claude and one for codex; see "Phase 2 cutover" below.

`sidecar agent explain --file PATH --agent KIND [--title T] [--rows N] [--print-window] [--json]` evaluates a saved screen (testdata header format or raw text) with no tmux and no store, printing Herdr's text layout or the JSON explain record. `--print-window` prints the exact window the engine saw, which is Herdr's `agent read --source detection` offline.

### Differential harness

`scripts/herdr-diff.sh` runs every fixture with a `screen:` block through `herdr agent explain --file --json` (the 0.8.2 release binary, with `XDG_CONFIG_HOME` pointed at a throwaway override directory holding the vendored bytes) and through `sidecar agent explain --file --json`, and diffs `state`, `matched_rule.id`, and `fallback_reason`.

Result at `e2b85c7` against herdr 0.8.2, after the Phase 2 cutover: 47 fixtures compared, 45 agree, 0 disagree, 2 overlay divergences, 0 redundant overlay rules; 5 proof transcripts skipped. Two limitations are forced by Herdr's `--file` mode: it passes an empty `osc_title` (`explain_for_label`), so the comparison is screen-only with the title stripped on both sides, and it applies no read window, so the harness feeds both engines the window Sidecar prints with `--print-window`.

A fixture whose Sidecar verdict comes from a rule id beginning `sidecar.` is reported as an **overlay divergence**, not a disagreement, and does not fail the run. Herdr is being run against the vendored file alone, so a Sidecar-owned rule firing is the divergence the overlay exists to create and the fixture beside it is the evidence. The inverse is a failure: a `sidecar.` rule that reaches the *same* verdict Herdr reaches without it is reported as **redundant**, which is the plan's "overlay changes nothing" signal that the rule should be deleted. An overlay rule carrying an *upstream* id — the four `\p{Alphabetic}` rewrites — is not in either bucket: those rules are live on Herdr's side too, so they must agree.

Read the 45 as a floor, not a proof of full parity. Of the 45 agreements, 21 are fallback-versus-fallback — no rule matched on either side, so what agrees is the fallback, not a rule — which leaves 24 rows that actually exercise a rule. And because the title is blanked on both sides, all 16 `osc_title` rules no-match on both engines, so a title-driven disagreement cannot show up here at all: `grok/stale_working_scrollback.txt`, a census disagreement decided entirely by a stale braille title, prints as an agreement in this harness. The census below, which runs with titles intact, is what covers that half.

### Census (Phase 1 baseline)

`go test ./internal/agentactivity -run TestManifestCensus -v` prints, for every fixture, the Go rule-table verdict beside the manifest verdict. At Phase 1's end: 45 fixtures, 35 agree, 10 disagree. Every disagreement is a rule Sidecar has and upstream lacks, or a case where Sidecar's narrower rule is right; none is an engine bug, which the 45/45 harness result confirms independently. Phase 2 resolves each by overlay or by accepting upstream, and records the decision per fixture.

| Fixture | Go verdict | Manifest verdict | Triage |
| --- | --- | --- | --- |
| `antigravity/blocked.txt` | blocked | idle (fallback) | Upstream `antigravity.toml` has three rules and no trust-prompt rule. Overlay. |
| `antigravity/working.txt` | working | idle (fallback) | Upstream has no `Generating…` / `esc to cancel` rule. Overlay. |
| `claude/overlay.txt` | unknown, skip | idle | Upstream has no model-picker retain rule. Overlay. |
| `codex/background_terminal.txt` | working | idle | Herdr treats background terminals as not busy; Sidecar's rule requires the waiting line, a running count, and the `/ps` `/stop` hints, and is right for a main turn blocked on one. Overlay. |
| `cursor/blocked_decision.txt` | blocked | idle (fallback) | Upstream has no `Waiting for decision (y/n/p)` rule. Overlay. |
| `cursor/false_positive_finished_background.txt` | idle | working | Upstream `background_task_status_working` matches a finished background task. Overlay; candidate for an upstream pull request. |
| `cursor/working_background.txt` | working | idle (fallback) | Upstream has no `(background)` suffix rule. Overlay. |
| `grok/background_subagent.txt` | working | idle | Upstream has no "N subagent still running" rule; this is the parked-turn case that produced false completions. Overlay. |
| `grok/overlay.txt` | unknown, skip | idle | Upstream has no resume-overlay retain rule. Overlay. |
| `grok/stale_working_scrollback.txt` | idle | working | Stale braille title; Sidecar lets a clear idle footer beat the title, Herdr trusts the title. Overlay. |

### Shadow mode

`features.ManifestDetection` (default off) installs a shadow sink at startup; while it is installed, `Detect` runs the manifest lane beside the Go rule tables and appends one JSON line per disagreement in state, skip, or fallback to `<state dir>/agent-detection-shadow.jsonl`, carrying both verdicts and the manifest explain record. A difference in evidence spelling alone is not logged, because `claude.title.working` and `osc_title_working` name the same finding. The returned result is still the Go rule tables' verdict for any provider not yet cut over.

The log is bounded three ways, because a steady disagreement is the normal case rather than the rare one — an idle Codex pane disagrees on every poll, at 200ms while the pane is active — and an unbounded diagnostic would be a worse bug than the one it is looking for. A record is written only when a pane's verdict pair changes, where a pane is keyed by its agent plus a hash of its title and foreground command (`Detect` is given no pane identity). The explain payload rides only on the first record of each verdict pair for that pane; a repeat carries the verdicts alone. And the file rotates to a single `.jsonl.1` generation once it would pass 5 MiB, so the whole footprint is capped at twice that.

Writes happen on a pump goroutine behind a bounded queue, so a slow disk delays no poll; records the queue could not take are dropped and counted, and the count rides on the next record written as `dropped`.

What reaches the file is not screen-free. A record carries the engine's region previews: up to 240 characters of screen text per evaluated rule, which for a `whole_recent` rule is the top of the read window. That is bounded and it is a great deal less than the pane, but a shadow log should be read before it is attached to an issue.

## Phase 2 cutover: claude, codex

`Detect` classifies Claude Code and Codex panes through the vendored Herdr manifests. `internal/agentactivity/claude.go` and `codex.go` hold nothing but the process gate and a wrapper; `grep -n 'Rule{' internal/agentactivity/claude.go internal/agentactivity/codex.go` returns nothing. `manifestDetection` in `activity.go` is the list of cut-over providers, and membership also turns shadow mode off for that provider, since comparing the manifest lane against itself logs nothing and costs a second evaluation per frame.

Two evidence strings stay Sidecar's, because they name findings no manifest produces: `<agent>.process-mismatch` (the process gate refused before any rule ran) and `<agent>.known-live-fallback` (a positively identified live process with no matching rule). Everything else is now a Herdr rule id, or a `sidecar.` overlay rule id.

**`fallbackIsLowEvidence` is unchanged for both providers.** Claude and Codex keep `FallbackIdle: true` on the no-match idle, exactly as their Go probes did: both own explicit idle rules, so reaching the fallback means the chrome went unrecognised, not that a turn ended, and a fallback idle must not announce a completion. The Antigravity exception stays Antigravity's alone and is Phase 2's problem when that provider is cut over.

### Verdict flips

Every verdict is unchanged. Every evidence string changed, because the vocabulary did.

| Fixture | Old verdict / evidence | New verdict / rule | Reason |
| --- | --- | --- | --- |
| `claude/idle.txt` | idle / `claude.screen.idle` | idle / `live_prompt_box` | Upstream rule better. Sidecar matched `^❯` in the last twelve lines with two literal exclusions; upstream reads only the body of the prompt box, so a resolved form in the scrollback is outside the region rather than excluded literal by literal. |
| `claude/interrupted.txt` | idle / `claude.screen.idle` | idle / `live_prompt_box` | As above. |
| `claude/working.txt` | working / `claude.title.working` | working / `osc_title_working` | Upstream rule better: the same braille class plus the half-circle frames (U+25D0–U+25D3) Claude Code 2.1.228 switched to. |
| `claude/background-agents.txt` | working / `claude.title.working` | working / `osc_title_working` | As above. The U+2810 frame this fixture was minted for is inside both patterns. |
| `claude/working_halfcircle.txt` (new) | — | working / `osc_title_working` | New fixture. This is the drift the plan opened with: Sidecar's pattern never learned the half-circle spinner, so on a current Claude the title rule fell through and idle detection carried the turn. Synthetic, labelled as such: the harvested Claude here is 2.1.220. |
| `claude/blocked.txt` | blocked / `claude.screen.blocked` | blocked / `live_blocked_form` | Upstream rule better. Sidecar alternated over phrases anywhere in the last 24 lines; upstream requires "esc to cancel" *and* a confirm-or-select hint *and*, for the select shape, a navigation hint, read below the last horizontal rule. The `AskUserQuestion` form (`☐ Choice` … `Enter to select · ↑/↓ to navigate · Esc to cancel`) satisfies all three, so no overlay was needed for it. |
| `claude/overlay.txt` | unknown, skip / `claude.overlay.retain` | unknown, skip / `sidecar.overlay_retain` | Sidecar-only behaviour preserved through an overlay. |
| `codex/working.txt` | working / `codex.title.working` | working / `osc_title_working` | Upstream rule better; same ten-frame dots class. |
| `codex/tool_execution.txt` | working / `codex.title.working` | working / `osc_title_working` | As above. |
| `codex/blocked.txt` | blocked / `codex.title.blocked` | blocked / `osc_title_blocked` | Engine semantics. Sidecar got title-blocker-beats-title-spinner from file order; upstream states it as priority 1100 against 1050. |
| `codex/startup_idle.txt` | idle / `codex.screen.idle` | idle / `osc_title_idle` | Engine semantics, and the one consequence worth naming — see below. |
| `codex/interrupted.txt` | idle / `codex.screen.idle` | idle / `osc_title_idle` | As above. |
| `codex/completed.txt` | idle / `codex.screen.idle` | idle / `osc_title_idle` | As above. |
| `codex/transcript_viewer.txt` | unknown, skip / `codex.viewer.retain` | unknown, skip / `transcript_viewer` | Upstream rule better: it corroborates the banner with all four of the viewer's key hints, where Sidecar took the banner or a two-word fragment. |
| `codex/background_terminal.txt` | working / `codex.screen.background-terminal-working` | working / `sidecar.background_terminal_working` | Sidecar-only behaviour preserved through an overlay. |
| `codex/trust_directory.txt` (new) | — | blocked / `trust_directory` | New fixture for an upstream rule Sidecar had no equivalent for. Before the cutover a first-run Codex pane sat on an unanswered trust prompt reading idle. Synthetic, labelled as such: the prompt appears only the first time Codex opens a directory. |
| `codex/exit.txt` | unknown / `codex.process-mismatch` | unchanged | The process gate still refuses a pane whose foreground command is `zsh`. |

**Codex idle now depends on the pane title.** Upstream `codex.toml` has no composer rule; it reaches idle through `osc_title_idle`, which asks only that the title be non-empty and carry no spinner frame. Under tmux a pane title is effectively always non-empty — tmux seeds `#{pane_title}` with the host name and Codex overwrites it per turn — so a live pane still lands on the explicit rule, which the loopback proof confirms against a real tmux pane. A pane whose title is genuinely empty resolves idle through `codex.known-live-fallback` instead, which is `FallbackIdle` and therefore cannot announce a completed turn. That is the conservative direction and it is upstream's call, recorded rather than overlaid.

### Overlays written

| File | Rule | Priority | What it proves |
| --- | --- | --- | --- |
| `sidecar/claude.toml` | `sidecar.overlay_retain` | 900 | An "Esc to close" overlay footer in the bottom five non-empty lines retains the prior state. Upstream's `model_picker_menu` is gated on the literals "select model" + "enter to set as default" + "esc to cancel", none of which the 2.1.x model picker in `claude/overlay.txt` renders, so upstream alone classifies it from the title as idle — and the tracker turns working → idle into a completed turn, so opening the model picker mid-turn would announce a completion that never happened. The rule is narrower than the one it replaces (`claude.overlay.retain` matched the phrase anywhere in 24 lines) and uses the same footer shape upstream itself gates `btw_overlay_working` on. Priority 900 sits below every blocker and below `live_prompt_box` (950), and below `btw_overlay_working` (975) so upstream's decision that the /btw overlay means *working* survives. |
| `sidecar/codex.toml` | `sidecar.background_terminal_working` | 500 | A main turn parked on a background terminal is working, not idle. Herdr has no background-terminal rule; it treats a background terminal as something the user started and the agent is not waiting on, so `codex/background_terminal.txt` falls through to `osc_title_idle`. The gate is the pre-manifest rule's, unchanged: the waiting line at the head of a line, a running count of at least one, and both live hints (`/ps to view`, `/stop to close`) in either order, which is what `TestCodexBackgroundTerminalRequiresCurrentRunningChrome` pins. Priority 500 is upstream's own working tier, so every blocker still outranks it. A candidate for an upstream pull request. |

No rule was carried forward merely because it existed. `claude.screen.resolved-idle` was dropped: upstream buys the same behaviour structurally, by reading the blocked form only below the last horizontal rule, so a resolved form above the composer is out of the region rather than out-competed by an extra rule. `codex.screen.idle`, `claude.screen.idle`, `claude.screen.blocked`, `codex.screen.working` and the two title patterns were dropped as strictly weaker restatements of upstream rules.

### Result

- Census: 47 fixtures, 39 agree, 8 disagree; every claude and codex row reports `cutover` (one lane left) and the remaining eight disagreements are the six providers still on the Go rule tables.
- Differential harness (`scripts/herdr-diff.sh claude codex`): 17 fixtures compared, 15 agree, 0 disagree, 2 overlay divergences, 0 redundant. The two divergences are exactly the two overlay rules above.
- `Tracker` fixtures in `compat_test.go` pass unchanged. Nothing in the tracker, the resolver, or the report contract moved.
