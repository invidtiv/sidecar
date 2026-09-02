# Adding New Agent CLIs to Sidecar

This guide is the complete, step-by-step developer and agent reference for adding support for a new AI coding CLI (such as Meta's Muse, Grok, Claude Code, Codex, OpenCode, Pi, etc.) to Sidecar.

Sidecar provides unified workspace creation, live status tracking, terminal embedding, prompt coordination, session resume durability, and conversation history across all supported CLIs. To make a CLI a first-class citizen, several subsystems need to be wired.

> [!TIP]
> **Borrowing from Herdr**: When adding a new CLI, you can reference the source code for [Herdr](https://github.com/herdrdev/herdr). Herdr is often ahead on prototyping and testing new CLI integrations (process names, regex rules, hook schemas, and screen captures), and Sidecar frequently ports or adapts proven patterns from Herdr.

---

## Subsystem Architecture Overview

Adding a new agent CLI touches up to seven distinct subsystems:

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ 1. Catalog & Launch Registry (internal/agentcatalog)                        │
│    Canonical ID, display name, default command, auto-approve flag, resume   │
└──────────────┬──────────────────────────────────────────────┬───────────────┘
               │                                              │
               ▼                                              ▼
┌──────────────────────────────┐              ┌──────────────────────────────┐
│ 2. Live Activity & State     │              │ 3. Shell Creation & Settings │
│    (internal/agentactivity)  │              │    (workspacecreate, configui│
│    Process identification,   │              │     workspace plugin)        │
│    screen/title regex rules  │              │    Modals, pickers, defaults │
└──────────────┬───────────────┘              └──────────────┬───────────────┘
               │                                              │
               ▼                                              ▼
┌──────────────────────────────┐              ┌──────────────────────────────┐
│ 4. Lifecycle Hooks & Trust   │              │ 5. Conversation History      │
│    (agentlifecycle, session, │              │    (internal/adapter/<cli>)  │
│     agentintegration)        │              │    JSONL/DB transcript       │
│    session reporting, roots  │              │    parser, tiered watcher    │
└──────────────┬───────────────┘              └──────────────┬───────────────┘
               │                                              │
               ▼                                              ▼
┌──────────────────────────────┐              ┌──────────────────────────────┐
│ 6. Theme & Visual Language   │              │ 7. Verification & Matrix     │
│    (styles/overview,         │              │    (agentcontrol live tests, │
│     website/src/data/themes) │              │     vocabulary parity tests) │
│    Badge colors, palette     │              │    Prompt/wait/read control  │
└──────────────────────────────┘              └──────────────────────────────┘
```

---

## Step 1: Catalog & Launch/Resume Registry

`internal/agentcatalog` is the single shared source of truth for agent families Sidecar can launch, configure, and resume.

### 1.1 Register Family in `internal/agentcatalog/agentcatalog.go`

Add the new CLI family entry to `families` in `internal/agentcatalog/agentcatalog.go`:

```go
{
    ID:                 "muse",
    Name:               "Muse Spark",       // Display name is Muse Spark (CLI binary is `muse`, product is Muse Code)
    Short:              "Muse",
    Command:            "muse",
    SkipPermissionsArg: "--yolo",            // Muse Code: --yolo disables approval + sandbox (also --disable-approval, --trust-workspace)
    Aliases:            []string{"muse-cli"},
    AdapterID:          "muse",            // ID of the conversation history adapter
    ResumeArgs:         []string{"resume"}, // muse resume <id> | muse resume --last (picker when no arg on TTY)
    ResumeKinds:        []string{"id"},    // "id" only; Muse does not resume from path
},
```

### 1.2 Family Fields Explained

- **`ID`**: Canonical identity stored in `config.json` (`plugins.workspace.agents`, `agentStart`, `defaultAgentType`).
- **`Name`**: Full human-readable display name for modals and menus.
- **`Short`**: Compact label for settings tables and tabs.
- **`Command`**: Binary executable name launched by default.
- **`SkipPermissionsArg`**: Argv flag appended when auto-approval is toggled on in creation modals or CLI (`--skip-permissions`).
- **`Aliases`**: Alternative identifiers (such as legacy names or conversation adapter IDs) that resolve to this family.
- **`ResumeArgs`**: Command arguments placed between `Command` and the session identifier (e.g. `muse resume <session-id>`).
- **`ResumeKinds`**: Slice containing `"id"`, `"path"`, or both.
- **`AdapterID`**: Conversation adapter ID if different from `ID`.

### 1.3 What Inherits This Automatically

Adding a family to `internal/agentcatalog` automatically wires:
- Creation pickers in `internal/workspacecreate/form.go` (Worktree & Shell modals).
- Configuration settings in `internal/configui/page_agents.go` (Agent toggle allowlist and launch command override editors).
- Workspace default agent dropdown in `internal/configui/page_workspaces.go`.
- CLI commands: `sidecar create shell --agent <kind>`, `sidecar create worktree --agent <kind>`, `sidecar agent start --kind <kind>`.
- Structured resume command generation via `agentcatalog.BuildResume` and `agentcatalog.DisplayCommand`.

---

## Step 2: Live Activity & Process Identification

`internal/agentactivity` classifies live agent state (`working`, `blocked`, `idle`, `done`, `unknown`) from pane titles, tmux screen captures, and process information.

### 2.1 Register Process Name in `internal/agentactivity/activity.go`

1. Update `identifyProcessName(command)`:
   ```go
   case command == "muse" || strings.HasPrefix(command, "muse-"):
       return "muse"
   ```
2. Add the provider to `processGate` in `internal/agentactivity/manifest_detect.go`:
   ```go
   case "muse":
       return museProcess(command)
   ```
3. Update `Supports(agent string) bool`:
   ```go
   case "codex", "claude", "grok", "antigravity", "pi", "copilot", "cursor", "opencode", "amp", "muse":
       return true
   ```
4. If the CLI runs under a generic runtime wrapper like Node, Bun, or Python, check `NeedsProcessIdentity()` and screen identity heuristics in `Identify()`.

### 2.2 Vendor the detection manifest, not a Go rule table

There are no Go rule tables left. Every provider Sidecar claims executes Herdr's vendored detection manifest through `internal/agentactivity/manifest`, and anything Sidecar knows that upstream does not is a data overlay under `internal/agentactivity/manifests/sidecar/`. `internal/agentactivity/<provider>.go` holds the process gate, a comment recording what the deleted rule table knew and where it went, and nothing else. See `docs/plans/active/herdr-detection-parity.md` and `docs/reference/herdr-detection-parity.md`.

1. **Check whether the manifest is already vendored.** `ls internal/agentactivity/manifests/upstream/` lists 21 agents; Herdr's catalog is likely to have the one you are adding. If it does, the detection half of this step is already done — the loader keys on the file name, so `ManifestAgentID` is the only place a Sidecar family name that differs from Herdr's file name is written down (today: `copilot` → `github-copilot`).

2. **If it is not vendored, sync rather than write.** `scripts/sync-herdr.sh` refreshes the whole vendored tree from a Herdr ref, updates `upstream.lock.json`, and renders `report.md`. Never hand-edit a file under `upstream/`: `TestVendoredManifestsMatchLock` fails on any edit, which is what keeps a re-sync a clean file replacement.

3. **Write the process gate.** `<provider>Process(command string) bool` is Sidecar's own refusal and is stricter than Herdr's: it declines to evaluate the manifest at all unless the pane's foreground command is the provider or a permitted runtime wrapper. Register it in `processGate` in `manifest_detect.go` and give the provider a `Detect<Provider>` wrapper that refuses with `<provider>.process-mismatch` and otherwise calls `DetectManifestResult`.

4. **Mint a fixture and read which rule matched.**

   ```bash
   tmux -L probe -f /dev/null new-session -d -s cap -c "$PWD" -x 120 -y 40
   tmux -L probe send-keys -t cap 'muse' Enter && sleep 12
   tmux -L probe capture-pane -p -e -N -t cap > /tmp/muse.txt
   tmux -L probe kill-server
   sidecar agent explain --file /tmp/muse.txt --agent muse
   sidecar agent explain --file /tmp/muse.txt --agent muse --print-window   # what detection saw
   ```

   Save the capture under `internal/agentactivity/testdata/<provider>/` in the header format the other fixtures use (`pane_title:`, `pane_current_command:`, `pane_height:`, an optional `state:` the census checks, then a line reading exactly `screen:`). `TestFixtureCensus` classifies every fixture and fails when one declares a state it does not reach.

5. **Only then consider an overlay.** An overlay rule is a claim that upstream is wrong or silent about a screen you have captured, and every one of them costs something on the next sync. Write it in `internal/agentactivity/manifests/sidecar/<herdr-agent-id>.toml`, prefix new rule ids `sidecar.`, state which upstream priorities it sits between and why, and prove it with the fixture. `scripts/herdr-diff.sh` reports a rule that reaches the verdict Herdr reaches without it as **redundant** and fails, which is how an overlay rule that has stopped earning its place gets deleted.

### 2.3 Critical activity detection rules

- **Do not reintroduce a Go rule table.** If a screen is misread, the fix is an overlay rule with a fixture, or a pull request to Herdr.
- **Overlays that retain state need corroborating chrome.** A rule keyed on a bare word ("transcript", "resume session") freezes the badge for `SkipRetentionCap` whenever a turn merely discusses one. Two such rules were deleted in the Phase 2 cutover for exactly this reason.
- **Verify with tests**: `go test ./internal/agentactivity/...`, and `TestTheProcessNameVocabularyMatchesTheAgentCatalog` for the identity half.

---

## Step 3: Workspace Plugin Compatibility

While new code queries `internal/agentcatalog`, the workspace plugin maintains several helper tables in `internal/plugins/workspace/types.go`:

1. **Add `AgentType` constant** in `internal/plugins/workspace/types.go`:
   ```go
   AgentMuse AgentType = "muse" // Muse Spark (Muse Code)
   ```
2. **Add to `buildSkipPermissionsFlags()`** in `internal/plugins/workspace/types.go`:
   ```go
   agents := []AgentType{
       AgentClaude, AgentCodex, AgentCopilot, AgentAider, AgentAntigravity,
       AgentCursor, AgentOpenCode, AgentPi, AgentAmp, AgentGrok, AgentMuse,
   }
   ```
3. **Optional flags**:
   - `SystemPromptAppendFlags`: If the CLI accepts a flag to append system prompt instructions (e.g. `AgentMuse: "--rules"`).
   - `PrintModeArgs`: If the CLI has non-interactive stdout execution mode (e.g. `AgentMuse: {"-p"}`).
4. **Fallback session status** in `internal/plugins/workspace/agent_session.go`: Add a case to `detectAgentSessionStatus` if file-based inspection is supported.

---

## Step 4: Agent Lifecycle Hooks & Session Binding

When an agent CLI supports telemetry/lifecycle hooks, Sidecar can track exact sessions, state transitions, and process exits.

### 4.1 Record Capability in `internal/agentlifecycle/capabilities.json`

Add an entry to `internal/agentlifecycle/capabilities.json`. Muse Spark 1.0.1 has **no published lifecycle hook** (extension surface is skills/MCP/MSP wire, not `hooks.json`); set `screen-fallback` until hooks are shipped:

```json
{
  "schemaVersion": 1,
  "provider": "muse",
  "source": "",
  "assetVersion": "",
  "tier": "screen-fallback",
  "evidence": "none",
  "minProviderVersion": "",
  "testedProviderRange": "",
  "covered": [],
  "knownGaps": [
    "No Sidecar integration is built, so nothing is claimed and screen detection remains the sole authority.",
    "Muse Spark 1.0.1 traced locally (darwin/arm64, echo provider) but hooks are not shipped: Muse Code's extension surface is skills, MCP servers, and the MSP wire schema, not lifecycle hooks. No published hook contract like Claude Code's hooks or Codex's hooks.json was found in the binary or docs at https://dev.meta.ai/docs/muse-code/.",
    "The session log (JSONL at ~/.local/share/muse/sessions/YYYY/MM/DD/<uuid>/session.jsonl) and SQLite index at ~/.local/share/muse/session-index.db are the durable stores; reasoning text is encrypted (encrypted_content) and tool call/result shapes are visible but not hook-authored.",
    "Screen detection is therefore the sole lane authority; the capability entry exists so the provider appears in the matrix and can be promoted when and if Muse ships lifecycle hooks."
  ],
  "orderingGuaranteed": false,
  "sourceDoc": "https://dev.meta.ai/docs/muse-code/",
  "sourceVersionNote": "muse 1.0.1 on darwin/arm64, inspected 2026-08-31. No hook contract found; session storage verified via local JSONL and SQLite traces, and live TUI captured via private tmux socket (braille title spinner U+2800-FF, ◈ Thinking + esc to interrupt chrome, ⟩ prompt idle)."
}
```

When Muse ships hooks, promote to `session-identity` / `advisory` / `full` with real traces as for `claude`/`codex`/`pi`.

### 4.2 Approved Store Roots in `internal/agentsession/trust.go`

No official source is registered for Muse Spark yet (screen-fallback). `OfficialSources()` / `OfficialSourceFor` stay empty; add them only when a `sidecar.muse.hooks` source is shipped.

Define approved storage roots in `Roots.For(kind)` in `internal/agentsession/trust.go` (supports `XDG_DATA_HOME` and `MUSE_HOME` overrides — Muse stores sessions under `~/.local/share/muse/sessions` by default, not `~/.muse`):
   ```go
   case "muse":
       base := r.env("XDG_DATA_HOME")
       if base == "" {
           if r.Home == "" {
               return nil
           }
           base = filepath.Join(r.Home, ".local", "share")
       }
       return []string{filepath.Join(base, "muse", "sessions")}
       // Also check MUSE_HOME when set: if r.env("MUSE_HOME") != "" { base = r.env("MUSE_HOME") }
   ```
The actual implementation checks `MUSE_HOME` first, then `XDG_DATA_HOME`, then `~/.local/share/muse/sessions` — keep `WithinRoots` symlink-aware as for `codex`/`opencode`.

### 4.3 Automated Integration Installer (Optional)

If Sidecar bundles an automatic hook or plugin installer for this CLI:
1. Implement `agentintegration.Adapter` in `internal/agentintegration/muse_install.go`.
2. Register the adapter in `agentintegration.DefaultAdapters()` in `internal/agentintegration/install.go`.
3. This exposes `sidecar agent integration install muse`, `status`, `update`, and `uninstall`, as well as the UI in **Configuration → Agents → Integrations**.

---

## Step 5: Conversation History Adapter

To allow Sidecar's **Conversations** plugin to display transcripts, search history, and view token analytics for this CLI:

### 5.1 Implement `adapter.Adapter` in `internal/adapter/<provider>/`

Create the adapter package in `internal/adapter/muse/`:
- `adapter.go`: Implements `adapter.Adapter`:
  - `ID() string`: `"muse"`
  - `Name() string`: `"Meta Muse"`
  - `Icon() string`: `"M"` or custom glyph
  - `Detect(projectRoot string) (bool, error)`: Checks if sessions exist for project
  - `Capabilities() CapabilitySet`
  - `Sessions(projectRoot string) ([]Session, error)`: Returns parsed session metadata
  - `Messages(sessionID string) ([]Message, error)`: Returns structured messages
  - `Usage(sessionID string) (*UsageStats, error)`: Token usage statistics
  - `Watch(projectRoot string) (<-chan Event, io.Closer, error)`: File watcher
- `types.go`: Native data structures for parsing session logs.
- `watcher.go`: Watcher setup (use `internal/adapter/tieredwatcher` for append-only JSONL files).

### 5.2 Register Adapter Factory

1. Create `internal/adapter/muse/register.go` to self-register via `adapter.RegisterFactory`:
   ```go
   package muse

   import "github.com/marcus/sidecar/internal/adapter"

   func init() {
       adapter.RegisterFactory(func() adapter.Adapter {
           return New()
       })
   }
   ```
2. Add blank import in `cmd/sidecar/main.go`:
   ```go
   _ "github.com/marcus/sidecar/internal/adapter/muse"
   ```

### 5.3 Conversations UI Integration

In `internal/plugins/conversations/view_content.go`:
- `renderAdapterIcon()`: Add color styling for the adapter badge.
- `adapterAbbrev()`: Add 2-letter abbreviation (e.g. `"MU"`).
- `adapterShortName()`: Return short name string.
- `adapterFilterOptions()`: Add dedicated filter shortcut key if desired.

---

## Step 6: Theme Colors & Visual Presentation

1. **Default Color & Icon Glyph**: In `internal/styles/overview.go`:
   - Add default hex color to `defaultAgentColors`:
     ```go
     "muse": "#A78BFA",
     ```
   - Add icon glyph to `defaultAgentIcons` (must match `Adapter.Icon()`):
     ```go
     "muse": "◈", // not ✦ (Grok) — verified via Muse adapter
     ```
2. **Per-theme palettes** (missing seam — guide previously omitted these):
   - `internal/styles/themes.go`: add `"muse": "#A78BFA"` to `DefaultTheme.Colors.AgentColors` (Sidecar Modern).
   - `internal/styles/curated_themes.go`: add `"muse"` to **every** theme's `AgentColors` map. Values must already pass `EnsureContrastOn(..., surface, 4.5)` or `TestCatppuccinMochaPassesNormalizationUnchanged` fails. For example catppuccin-mocha `#A78BFA` → `#ae94fa` on `#393947`; use the normalized value per theme (run `TestDebugMuse` helper to derive).
3. **Website & Theme Palette**: In `website/src/data/themes.json`, add `"muse": "#A78BFA"` (or the per-theme normalized value) under `AgentColors` for all 21 themes.

Why this matters: `NormalizePalette` rewrites any `AgentColors` entry that fails contrast against `SurfaceRaised`. A single `#A78BFA` for every curated theme would be rewritten for most dark themes (catppuccin, tokyonight, etc.) and break the “passes unchanged” test. Store the post-normalization value.

---

## Step 7: Verification & Testing Checklist

Always run the full verification suite when adding a new agent CLI:

### 1. Unit & Parity Tests
```bash
# Verify catalog and process name resolution
go test ./internal/agentcatalog/...
go test ./internal/agentactivity/...

# Verify icon consistency with conversations adapters
go test ./internal/styles/ -run TestAgentIconMatchesConversationsAdapters

# Verify workspace pickers match catalog
go test ./internal/plugins/workspace/ -run TestAgentPickersFollowCatalog

# Verify conversation adapter
go test ./internal/adapter/muse/... -v
```

### 2. Live Agent Matrix Test
Test the full programmatic coordination flow (start -> identify -> prompt -> wait -> read -> send-keys):
```bash
SIDECAR_LIVE_AGENT_MATRIX=muse go test -v ./internal/agentcontrol -run TestLiveProviderMatrix
```

### 3. Headless TUI Verification
Use `scripts/tmux-drive.sh` in an isolated environment:
```bash
./scripts/tmux-drive.sh paths
SIDECAR_BIN=$HOME/go/bin/sidecar ./scripts/tmux-drive.sh start 200 50
# Open workspace creation modal and verify the new agent appears in the list
./scripts/tmux-drive.sh keys n
./scripts/tmux-drive.sh snap modal-create-workspace
./scripts/tmux-drive.sh stop
```

---

## Summary Checklist for Adding a New CLI

- [ ] **Step 1 (`internal/agentcatalog`)**: Add `Family` entry with launch/resume args and skip-permissions flag (`--yolo` for Muse).
- [ ] **Step 2 (`internal/agentactivity`)**: Add process name to `identifyProcessName()`, dispatch in `Detect()`, and create `internal/agentactivity/<name>.go` with spinner and state rules (idle `⟩` for Muse, not `❯`).
- [ ] **Step 3 (`internal/plugins/workspace`)**: Add `AgentType` constant, append to `buildSkipPermissionsFlags()`, and set optional system prompt / print mode flags; add `AgentMuse` case to `detectAgentSessionStatus` if file-based status is supported.
- [ ] **Step 4 (`internal/agentlifecycle` & `agentsession`)**: Add capability to `capabilities.json` (use `screen-fallback`/`none` if no hooks as for Muse), register official source only when a hook is shipped, and configure approved store roots (`XDG_DATA_HOME/muse/sessions` for Muse).
- [ ] **Step 5 (`internal/adapter`)**: Implement conversation adapter (`adapter.go`, `types.go`, `watcher.go`, `register.go`), register constructor, and add blank import in `cmd/sidecar/main.go`.
- [ ] **Step 6 (`internal/styles` & themes)**: Add default hex color and icon (`◈` for Muse) to `defaultAgentColors`/`defaultAgentIcons` in `internal/styles/overview.go`, add to `internal/styles/themes.go` and **every** entry in `internal/styles/curated_themes.go` (pre-normalized), and update `website/src/data/themes.json` for all themes.
- [ ] **Step 7 (Tests)**: Pass vocabulary parity tests (`TestTheProcessNameVocabularyMatchesTheAgentCatalog`), icon tests (`TestAgentIconMatchesConversationsAdapters`), workspace picker tests (`TestAgentPickersFollowCatalog`), and live matrix tests (`SIDECAR_LIVE_AGENT_MATRIX=muse`).
