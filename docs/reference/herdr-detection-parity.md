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

Sidecar consequence: `Observation` carries `PaneHeight` from tmux `#{pane_height}`; the engine's window is `ansi.Strip` → drop trailing blank rows → last `PaneHeight` rows (24 when unknown) → right-trim each row. Sidecar's `capture-pane` still fetches up to 600 lines of scrollback; the engine bounds the tail.

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

`conformance_test.go` ports 36 of Herdr's 45 inline manifest tests under their Rust names. The nine not ported exercise Herdr's remote-manifest cache and override loader (six, no Sidecar equivalent until Phase 5), duplicate cases covered elsewhere (two), and one loader-failure test whose analogue is `TestOverlayFailureFallsBackToUpstream`.

Four overlays exist today, one per `\p{Alphabetic}` rule (antigravity, cursor, kiro, qodercli), each replacing the upstream rule id with `[\p{L}\p{Nl}]`; `TestOverlaysMakeEveryRuleCompilable` proves no merged manifest carries a dead rule.

`sidecar agent explain --file PATH --agent KIND [--title T] [--rows N] [--print-window] [--json]` evaluates a saved screen (testdata header format or raw text) with no tmux and no store, printing Herdr's text layout or the JSON explain record. `--print-window` prints the exact window the engine saw, which is Herdr's `agent read --source detection` offline.

### Differential harness

`scripts/herdr-diff.sh` runs every fixture with a `screen:` block through `herdr agent explain --file --json` (the 0.8.2 release binary, with `XDG_CONFIG_HOME` pointed at a throwaway override directory holding the vendored bytes) and through `sidecar agent explain --file --json`, and diffs `state`, `matched_rule.id`, and `fallback_reason`.

Result at `e2b85c7` against herdr 0.8.2: 45 fixtures compared, 45 agree, 0 disagree; 5 proof transcripts skipped. Two limitations are forced by Herdr's `--file` mode: it passes an empty `osc_title` (`explain_for_label`), so the comparison is screen-only with the title stripped on both sides, and it applies no read window, so the harness feeds both engines the window Sidecar prints with `--print-window`.

### Census before cutover

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

`features.ManifestDetection` (default off) installs a shadow sink at startup; while it is installed, `Detect` runs the manifest lane beside the Go rule tables and appends one JSON line per disagreement in state, skip, or fallback to `<state dir>/agent-detection-shadow.jsonl`, carrying both verdicts and the manifest explain record (region previews only, never the screen). A difference in evidence spelling alone is not logged, because `claude.title.working` and `osc_title_working` name the same finding. The returned result is still the Go rule tables' verdict for any provider not yet cut over.
