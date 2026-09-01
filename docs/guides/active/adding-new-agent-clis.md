# Adding New Agent CLIs to Sidecar

This guide is the complete, step-by-step developer and agent reference for adding support for a new AI coding CLI (such as Meta's Muse, Grok, Claude Code, Codex, OpenCode, Pi, etc.) to Sidecar.

Sidecar provides unified workspace creation, live status tracking, terminal embedding, prompt coordination, session resume durability, and conversation history across all supported CLIs. To make a CLI a first-class citizen, several subsystems need to be wired.

> [!TIP]
> **Borrowing from Herdr**: When adding a new CLI, you can reference the source code for [Herdr](https://github.com/marcus/herdr). Herdr is often ahead on prototyping and testing new CLI integrations (process names, regex rules, hook schemas, and screen captures), and Sidecar frequently ports or adapts proven patterns from Herdr.

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

[`internal/agentcatalog`](file:///Users/marcus/code/sidecar/internal/agentcatalog/agentcatalog.go) is the single shared source of truth for agent families Sidecar can launch, configure, and resume.

### 1.1 Register Family in [`agentcatalog.go`](file:///Users/marcus/code/sidecar/internal/agentcatalog/agentcatalog.go)

Add the new CLI family entry to `families` in [`internal/agentcatalog/agentcatalog.go`](file:///Users/marcus/code/sidecar/internal/agentcatalog/agentcatalog.go#L68-L86):

```go
{
    ID:                 "muse",
    Name:               "Meta Muse",
    Short:              "Muse",
    Command:            "muse",
    SkipPermissionsArg: "--always-approve", // or "--yes", "--dangerously-skip-permissions", etc.
    Aliases:            []string{"muse-cli"},
    AdapterID:          "muse",            // ID of the conversation history adapter
    ResumeArgs:         []string{"resume"}, // e.g. ["resume"], ["--resume"], ["--session"], ["threads", "continue"]
    ResumeKinds:        []string{"id"},    // "id" or "path"
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

Adding a family to [`internal/agentcatalog`](file:///Users/marcus/code/sidecar/internal/agentcatalog/agentcatalog.go) automatically wires:
- Creation pickers in [`internal/workspacecreate/form.go`](file:///Users/marcus/code/sidecar/internal/workspacecreate/form.go) (Worktree & Shell modals).
- Configuration settings in [`internal/configui/page_agents.go`](file:///Users/marcus/code/sidecar/internal/configui/page_agents.go) (Agent toggle allowlist and launch command override editors).
- Workspace default agent dropdown in [`internal/configui/page_workspaces.go`](file:///Users/marcus/code/sidecar/internal/configui/page_workspaces.go).
- CLI commands: `sidecar create shell --agent <kind>`, `sidecar create worktree --agent <kind>`, `sidecar agent start --kind <kind>`.
- Structured resume command generation via [`agentcatalog.BuildResume`](file:///Users/marcus/code/sidecar/internal/agentcatalog/agentcatalog.go#L228-L235) and [`agentcatalog.DisplayCommand`](file:///Users/marcus/code/sidecar/internal/agentcatalog/agentcatalog.go#L152-L162).

---

## Step 2: Live Activity & Process Identification

[`internal/agentactivity`](file:///Users/marcus/code/sidecar/internal/agentactivity) classifies live agent state (`working`, `blocked`, `idle`, `done`, `unknown`) from pane titles, tmux screen captures, and process information.

### 2.1 Register Process Name in [`internal/agentactivity/activity.go`](file:///Users/marcus/code/sidecar/internal/agentactivity/activity.go)

1. Update [`identifyProcessName(command)`](file:///Users/marcus/code/sidecar/internal/agentactivity/activity.go#L143-L169):
   ```go
   case command == "muse" || strings.HasPrefix(command, "muse-"):
       return "muse"
   ```
2. Update [`Detect(ob Observation) Result`](file:///Users/marcus/code/sidecar/internal/agentactivity/activity.go#L211-L234):
   ```go
   case "muse":
       return DetectMuse(ob)
   ```
3. Update [`Supports(agent string) bool`](file:///Users/marcus/code/sidecar/internal/agentactivity/activity.go#L236-L244):
   ```go
   case "codex", "claude", "grok", "antigravity", "pi", "copilot", "cursor", "opencode", "amp", "muse":
       return true
   ```
4. If the CLI runs under a generic runtime wrapper like Node, Bun, or Python, check [`NeedsProcessIdentity()`](file:///Users/marcus/code/sidecar/internal/agentactivity/activity.go#L173-L180) and screen identity heuristics in [`Identify()`](file:///Users/marcus/code/sidecar/internal/agentactivity/activity.go#L80-L121).

### 2.2 Create Provider Detection Rules in `internal/agentactivity/<provider>.go`

Create `internal/agentactivity/muse.go`:

```go
package agentactivity

import "regexp"

// Spinners are provider-owned: never share regexes across providers as glyph
// sets and framing drift independently.
var (
	museTitleWorking = regexp.MustCompile(`^[\x{2800}-\x{28FF}]\s`)
	museRules        = []Rule{
		// 1. Blocked/approval rules (permission requests, interactive confirmations)
		{
			ID:     "muse.screen.blocked",
			State:  StateBlocked,
			Region: RegionCurrent,
			LastN:  24,
			Regexp: regexp.MustCompile(`(?im)(Do you want to proceed\?|Allow command\?|Yes, allow|Enter to confirm.*Esc to cancel)`),
		},
		// 2. Overlays & modals (retain prior state, prevent false state transitions)
		{
			ID:     "muse.overlay.retain",
			State:  StateUnknown,
			Region: RegionLastLines,
			LastN:  24,
			Regexp: regexp.MustCompile(`(?im)(esc to close|model picker)`),
			Skip:   true,
		},
		// 3. Working/busy rules (title spinners, thinking phrases, progress bars)
		{
			ID:     "muse.title.working",
			State:  StateWorking,
			Region: RegionTitle,
			Regexp: museTitleWorking,
		},
		{
			ID:     "muse.screen.working",
			State:  StateWorking,
			Region: RegionLastLines,
			LastN:  16,
			Regexp: regexp.MustCompile(`(?im)(Thinking…|Working…|esc to interrupt)`),
		},
		// 4. Explicit idle / ready for prompt
		{
			ID:     "muse.screen.idle",
			State:  StateIdle,
			Region: RegionLastLines,
			LastN:  12,
			Regexp: regexp.MustCompile(`(?m)^❯(?:\s| |$)`),
			Not:    []string{"esc to interrupt"},
		},
	}
)

func DetectMuse(ob Observation) Result {
	if ob.Agent != "muse" || !museProcess(ob.CurrentCommand) {
		return Result{State: StateUnknown, Evidence: "muse.process-mismatch"}
	}
	result := Evaluate(ob, museRules)
	if result.State == StateUnknown && !result.SkipStateUpdate {
		return Result{State: StateIdle, Evidence: "muse.known-live-fallback", FallbackIdle: true}
	}
	return result
}

func museProcess(command string) bool {
	return command == "muse" || strings.HasPrefix(command, "muse-")
}
```

### 2.3 Critical Activity Detection Rules

- **Spinners are provider-owned**: Never share spinner regexes across providers. Codex uses standard braille dots; Claude, Grok, and Cursor cycle the entire U+2800–U+28FF block.
- **Overlays must use `Skip: true`**: Splash screens, model pickers, and transcript viewers must retain the prior semantic state rather than prematurely reporting `idle` or `unknown`.
- **Verify with tests**: Run `go test ./internal/agentactivity/...` to ensure [`TestTheProcessNameVocabularyMatchesTheAgentCatalog`](file:///Users/marcus/code/sidecar/internal/agentactivity/catalog_vocabulary_test.go#L19-L31) passes.

---

## Step 3: Workspace Plugin Compatibility

While new code queries [`internal/agentcatalog`](file:///Users/marcus/code/sidecar/internal/agentcatalog/agentcatalog.go), the workspace plugin maintains several helper tables in [`internal/plugins/workspace/types.go`](file:///Users/marcus/code/sidecar/internal/plugins/workspace/types.go):

1. **Add `AgentType` constant** in [`internal/plugins/workspace/types.go`](file:///Users/marcus/code/sidecar/internal/plugins/workspace/types.go#L135-L148):
   ```go
   AgentMuse AgentType = "muse" // Meta Muse
   ```
2. **Add to `buildSkipPermissionsFlags()`** in [`internal/plugins/workspace/types.go`](file:///Users/marcus/code/sidecar/internal/plugins/workspace/types.go#L155-L165):
   ```go
   agents := []AgentType{
       AgentClaude, AgentCodex, AgentCopilot, AgentAider, AgentAntigravity,
       AgentCursor, AgentOpenCode, AgentPi, AgentAmp, AgentGrok, AgentMuse,
   }
   ```
3. **Optional flags**:
   - [`SystemPromptAppendFlags`](file:///Users/marcus/code/sidecar/internal/plugins/workspace/types.go#L175-L178): If the CLI accepts a flag to append system prompt instructions (e.g. `AgentMuse: "--rules"`).
   - [`PrintModeArgs`](file:///Users/marcus/code/sidecar/internal/plugins/workspace/types.go#L184-L188): If the CLI has non-interactive stdout execution mode (e.g. `AgentMuse: {"-p"}`).
4. **Fallback session status** in [`internal/plugins/workspace/agent_session.go`](file:///Users/marcus/code/sidecar/internal/plugins/workspace/agent_session.go#L92-L111): Add a case to `detectAgentSessionStatus` if file-based inspection is supported.

---

## Step 4: Agent Lifecycle Hooks & Session Binding

When an agent CLI supports telemetry/lifecycle hooks, Sidecar can track exact sessions, state transitions, and process exits.

### 4.1 Record Capability in [`internal/agentlifecycle/capabilities.json`](file:///Users/marcus/code/sidecar/internal/agentlifecycle/capabilities.json)

Add an entry to [`capabilities.json`](file:///Users/marcus/code/sidecar/internal/agentlifecycle/capabilities.json):

```json
{
  "schemaVersion": 1,
  "provider": "muse",
  "source": "sidecar.muse.hooks",
  "assetVersion": "1",
  "tier": "session-identity",
  "evidence": "real-trace",
  "minProviderVersion": "1.0.0",
  "testedProviderRange": "1.0.0 - 1.0.5",
  "covered": [
    "session_identity",
    "work_start",
    "turn_complete"
  ],
  "knownGaps": [
    "Describe any unobserved transitions or quirks here."
  ],
  "orderingGuaranteed": false,
  "sourceDoc": "https://example.com/muse/docs",
  "sourceVersionNote": "Traced on darwin/arm64, 2026-08-31."
}
```

### 4.2 Approved Store Roots in [`internal/agentsession/trust.go`](file:///Users/marcus/code/sidecar/internal/agentsession/trust.go)

1. Register official source in [`OfficialSources()`](file:///Users/marcus/code/sidecar/internal/agentsession/trust.go#L18-L25) and [`OfficialSourceFor(kind)`](file:///Users/marcus/code/sidecar/internal/agentsession/trust.go#L33-L46):
   ```go
   case "muse":
       return "sidecar.muse.hooks"
   ```
2. Define approved storage roots in [`Roots.For(kind)`](file:///Users/marcus/code/sidecar/internal/agentsession/trust.go#L93-L128):
   ```go
   case "muse":
       base := r.env("MUSE_HOME")
       if base == "" {
           if r.Home == "" {
               return nil
           }
           base = filepath.Join(r.Home, ".muse")
       }
       return []string{filepath.Join(base, "sessions")}
   ```

### 4.3 Automated Integration Installer (Optional)

If Sidecar bundles an automatic hook or plugin installer for this CLI:
1. Implement [`agentintegration.Adapter`](file:///Users/marcus/code/sidecar/internal/agentintegration/install.go#L483-L498) in `internal/agentintegration/muse_install.go`.
2. Register the adapter in [`agentintegration.DefaultAdapters()`](file:///Users/marcus/code/sidecar/internal/agentintegration/install.go#L501-L503).
3. This exposes `sidecar agent integration install muse`, `status`, `update`, and `uninstall`, as well as the UI in **Configuration → Agents → Integrations**.

---

## Step 5: Conversation History Adapter

To allow Sidecar's **Conversations** plugin to display transcripts, search history, and view token analytics for this CLI:

### 5.1 Implement `adapter.Adapter` in `internal/adapter/<provider>/`

Create the adapter package in `internal/adapter/muse/`:
- `adapter.go`: Implements [`adapter.Adapter`](file:///Users/marcus/code/sidecar/internal/adapter/adapter.go#L36-L47):
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
- `watcher.go`: Watcher setup (use [`tieredwatcher`](file:///Users/marcus/code/sidecar/internal/adapter/tieredwatcher) for append-only JSONL files).

### 5.2 Register Adapter

1. Register in [`internal/adapter/register.go`](file:///Users/marcus/code/sidecar/internal/adapter/register.go).
2. Add blank import in [`cmd/sidecar/main.go`](file:///Users/marcus/code/sidecar/cmd/sidecar/main.go#L17-L30):
   ```go
   _ "github.com/marcus/sidecar/internal/adapter/muse"
   ```

### 5.3 Conversations UI Integration

In [`internal/plugins/conversations/view_content.go`](file:///Users/marcus/code/sidecar/internal/plugins/conversations/view_content.go):
- [`renderAdapterIcon()`](file:///Users/marcus/code/sidecar/internal/plugins/conversations/view_content.go#L162-L190): Add color styling for the adapter badge.
- [`adapterAbbrev()`](file:///Users/marcus/code/sidecar/internal/plugins/conversations/view_content.go#L192-L221): Add 2-letter abbreviation (e.g. `"MU"`).
- [`adapterShortName()`](file:///Users/marcus/code/sidecar/internal/plugins/conversations/view_content.go#L223-L253): Return short name string.
- [`adapterFilterOptions()`](file:///Users/marcus/code/sidecar/internal/plugins/conversations/view_content.go#L255-L325): Add dedicated filter shortcut key if desired.

---

## Step 6: Theme Colors & Visual Presentation

1. **Default Color**: In [`internal/styles/overview.go`](file:///Users/marcus/code/sidecar/internal/styles/overview.go#L23-L30), add a default hex color to `defaultAgentColors`:
   ```go
   "muse": "#A78BFA",
   ```
2. **Website & Theme Palette**: In [`website/src/data/themes.json`](file:///Users/marcus/code/sidecar/website/src/data/themes.json), add `"muse": "#..."` under `AgentColors` for themes.

---

## Step 7: Verification & Testing Checklist

Always run the full verification suite when adding a new agent CLI:

### 1. Unit & Parity Tests
```bash
# Verify catalog and process name resolution
go test ./internal/agentcatalog/...
go test ./internal/agentactivity/...

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

- [ ] **Step 1 (`internal/agentcatalog`)**: Add `Family` entry with launch/resume args and skip-permissions flag.
- [ ] **Step 2 (`internal/agentactivity`)**: Add process name to `identifyProcessName()`, dispatch in `Detect()`, and create `internal/agentactivity/<name>.go` with spinner and state rules.
- [ ] **Step 3 (`internal/plugins/workspace`)**: Add `AgentType` constant, append to `buildSkipPermissionsFlags()`, and set optional system prompt / print mode flags.
- [ ] **Step 4 (`internal/agentlifecycle` & `agentsession`)**: Add capability to `capabilities.json`, register official source, and configure approved store roots.
- [ ] **Step 5 (`internal/adapter`)**: Implement conversation adapter, register constructor, and add blank import in `cmd/sidecar/main.go`.
- [ ] **Step 6 (`internal/styles` & themes)**: Add default hex color to `defaultAgentColors` and `themes.json`.
- [ ] **Step 7 (Tests)**: Pass vocabulary parity tests, adapter unit tests, and live matrix tests.
