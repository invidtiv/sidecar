# Plan: Full Grok support in conversations (td-d240af)

**Task:** [td-d240af](td-d240af) — Full Grok support in conversations  
**Epic:** td-cfcc8f — Summer 2026 improvements  
**Sibling (done):** td-fb5daf — Full Grok support in workspaces  

## Goal

Make Grok a first-class source in the conversations plugin at parity with Claude Code, Codex, Cursor, Gemini, etc.: discovery, listing, detail/view, resume / open-in-workspace, and clean failure isolation.

Workspace already knows Grok (`AgentGrok`, create/start pickers, `--always-approve`). This task is the **conversations adapter + plugin hooks** half.

---

## Acceptance criteria (from td)

| # | Criterion | How we prove it |
|---|-----------|-----------------|
| 1 | Grok conversations discovered and listed | `Detect` + `Sessions` against real `~/.grok/sessions` |
| 2 | Opening / detail view works | `Messages` maps user/assistant/tool/reasoning into Conversation Flow |
| 3 | Resume / open-in-workspace works like other agents | `resumeCommand` + `defaultAgentIdxForAdapter` → workspace injects `grok --resume <id>` |
| 4 | Adapter failures degrade cleanly | Per-adapter batch load already timeouts/skips errors; unit tests for corrupt files |

---

## Research: on-disk format (verified 2026-08-06)

### Layout

```
~/.grok/
  active_sessions.json          # live sessions: [{session_id, pid, cwd, opened_at}, ...]
  sessions/
    session_search.sqlite       # Grok's own FTS index (optional; do not depend on for v1)
    <url-encoded abs path>/     # e.g. %2FUsers%2Fmarcus%2Fcode%2Fsidecar
      <session-uuid>/
        summary.json            # metadata (cheap read)
        chat_history.jsonl      # canonical transcript (append-only-ish)
        updates.jsonl           # ACP stream (token totals, tool chunks; large)
        events.jsonl            # lifecycle / MCP events
        prompt_context.json
        system_prompt.txt
        terminal/*.log
        ...
```

**Project path encoding:** `url.PathEscape` / percent-encoding of the absolute path with `/` → `%2F`:

| Absolute path | Session dir name |
|---------------|------------------|
| `/Users/marcus/code/sidecar` | `%2FUsers%2Fmarcus%2Fcode%2Fsidecar` |

Verified: `urllib.parse.unquote` of dir names recovers cwd; `summary.json` `info.cwd` matches.

This is **per-project directories**, not a global flat bag like Codex. Closest kin: Claude Code (`~/.claude/projects/<encoded>/`) and Gemini (`~/.gemini/tmp/<hash>/chats/`).

### `summary.json` (primary metadata source)

```json
{
  "info": { "id": "<uuid>", "cwd": "/Users/.../sidecar" },
  "session_summary": "…",
  "generated_title": "Full Plan for TD-D240AF Ticket",
  "created_at": "2026-08-06T15:26:03.032188Z",
  "updated_at": "2026-08-06T15:26:50.968595Z",
  "last_active_at": "2026-08-06T15:26:50.968595Z",
  "num_messages": 94,
  "num_chat_messages": 37,
  "current_model_id": "grok-4.5",
  "agent_name": "grok-build-plan",
  "git_root_dir": "/Users/.../sidecar/",
  "head_branch": "main",
  "reasoning_effort": "high"
}
```

**Sessions list can be built almost entirely from `summary.json` + dir `Stat` — no need to open `chat_history.jsonl` for listing.** That is the performance win.

### `chat_history.jsonl` (message types observed)

| `type` | Shape | Map to |
|--------|-------|--------|
| `system` | `{type, content: string}` | Skip (system prompt dump) |
| `user` | `{type, content: [{type:"text", text}]}` | `role=user`; strip harness tags; prefer `<user_query>` body for titles |
| `assistant` | `{type, content: string, tool_calls?: [{id,name,arguments}], model_id, …}` | `role=assistant`; text + tool_use blocks |
| `tool_result` | `{type, tool_call_id, content: string}` | `tool_result` content block linked by `ToolUseID` |
| `reasoning` | `{type, id, summary:[{type:"summary_text", text}], encrypted_content, status}` | `ThinkingBlocks` from summary text only (ignore encrypted blob) |

**Important gaps:**

- **No per-line timestamps** in `chat_history.jsonl`. Use `summary.created_at` / `updated_at` for session bounds; leave message timestamps zero **or** (optional stretch) derive approximate times from `updates.jsonl` `_meta.agentTimestampMs`.
- **Token usage** not on chat lines. Partial signal: `updates.jsonl` chunks carry `_meta.totalTokens` (running total). For v1: either (a) omit / zero usage, or (b) tail-scan updates for last `totalTokens` and put all into `TotalTokens` / rough input estimate. Prefer **(a) for list path**, **(b) optional on Messages/Usage** with clear “best-effort” comment.
- Session sizes on this machine: ~30KB–1MB `chat_history`; `updates.jsonl` can be larger (~1MB+). **Never parse full updates on Sessions().**

### Resume CLI (verified)

```
grok --resume [<SESSION_ID_OR_TITLE>]   # also -r
grok --continue                         # most recent for cwd
```

Adapter resume command:

```text
grok --resume <session.ID>
```

Workspace already injects resume commands into shells / worktree agent start (`ResumeConversationMsg`).

### Live sessions

`~/.grok/active_sessions.json` lists PIDs. Use for `Session.IsActive` when `session_id` matches (best-effort; ignore if file missing/corrupt).

### Env / home override

Check for a Grok home override if one exists in the wild (`GROK_HOME` is not currently set in the author’s env; default `~/.grok`). Mirror Claude’s pattern: honor env if documented, else `filepath.Join(home, ".grok")`. Confirm with `grok` docs/source at implement time; if no env, hardcode `~/.grok`.

---

## Design decisions

### 1. Adapter ID / naming

| Field | Value | Rationale |
|-------|-------|-----------|
| `adapterID` | `"grok"` | Matches `workspace.AgentGrok = "grok"` |
| `adapterName` | `"Grok"` | Matches workspace display names |
| `Icon` | `"✦"` or similar single-width glyph | Distinct from Claude `◆`, Codex `▶`, Gemini `★` |

Do **not** use `"grok-cli"` / `"grok-build"` — keep ID aligned with agent type so future mappings stay 1:1.

### 2. Watch scope: **Project**

Sessions live under a per-cwd encoded directory. Implement:

```go
func (a *Adapter) WatchScope() adapter.WatchScope {
    return adapter.WatchScopeProject
}
```

Watch `~/.grok/sessions/<encoded(projectRoot)>` (and optionally the parent for new dirs). Avoid watching the entire `~/.grok/sessions` tree (other projects).

### 3. `Session.Path` for tiered watcher

Set `Path` to the absolute path of **`chat_history.jsonl`** (the append-heavy transcript). That opts into HOT/COLD/FROZEN tiers. Metadata still comes from `summary.json`; if summary mtime advances without history growth, list refresh still happens via directory watch.

### 4. Discovery strategy (not CWD scan of every file)

Unlike Codex (global dir + CWD filter), Grok is:

```
projectRoot → encode → sessionsDir/encoded → list UUID dirs → read summary.json
```

Also handle:

- `Abs` + `EvalSymlinks` once per `Sessions` call (same as other adapters).
- Trailing slash differences: normalize with `filepath.Clean`.
- Worktrees: plugin already calls `Sessions` per worktree path; each worktree has its own encoded dir. Implement `ProjectDiscoverer` to surface **deleted** worktree dirs under `~/.grok/sessions` that share the main repo path prefix (decode dir names; keep those under main’s parent path). Pattern: Claude Code’s `DiscoverRelatedProjectDirs`.

### 5. Message cleaning

User content is heavily wrapped (`<user_info>`, `<git_status>`, `<system-reminder>`, `<user_query>`). Reuse the same approach as Claude/Cursor:

1. Prefer text inside `<user_query>…</user_query>` for display and titles.
2. If absent, show remaining text after stripping known reminder/info blocks.
3. Do not show raw system-prompt `type=system` rows in the flow.

### 6. Tool linking

Assistant rows carry `tool_calls: [{id, name, arguments}]` (arguments is a **JSON string**). Following `tool_result` rows use `tool_call_id`. Map to:

- `ContentBlock{Type:"tool_use", ToolUseID, ToolName, ToolInput}`
- `ContentBlock{Type:"tool_result", ToolUseID, ToolOutput}` (+ legacy `ToolUses` for compatibility)

Preserve pending tool map across incremental appends (skill requirement).

### 7. Failure isolation

- `Detect`: missing `~/.grok/sessions` → `(false, nil)`.
- `Sessions`: skip corrupt `summary.json` / missing dirs; never fail the whole adapter on one bad session.
- `Messages`: missing history → `nil, nil`; parse errors on a line → skip line, continue.
- Plugin already per-adapter batches with 5s timeout and ignores `err` — do not panic or hold FDs.

### 8. Out of scope for v1

- Using `session_search.sqlite` for content search (plugin search goes through `Messages` + `MessageSearcher`).
- Parsing full `updates.jsonl` for stream reconstruction.
- Sub-agent session hierarchy (no evidence of separate subagent session files in layout; agent name is on summary only).
- Cost pricing for Grok models in `pricing` package (can leave `EstCost=0` until rates are known).
- Export/delete of Grok sessions from sidecar UI.

---

## Architecture

```
cmd/sidecar/main.go
  blank-import → internal/adapter/grok (register)

internal/adapter/grok/
  register.go      // init RegisterFactory
  types.go         // SummaryFile, chat line DTOs, SessionMetadata
  adapter.go       // Adapter interface + caches
  watcher.go       // fsnotify on project session dir
  search.go        // MessageSearcher via SearchMessagesSlice
  adapter_test.go  // fixtures + contract tests
  testdata/        // minimal summary + chat_history fixtures

internal/plugins/conversations/
  view_content.go  // resumeCommand, icons, abbrev, filter key, short name
  resume_modal.go  // defaultAgentIdxForAdapter → AgentGrok
  plugin_test.go   // resume command case

(optional) internal/adapter/pricing/  // grok tier later
```

Reference implementations to copy patterns from:

| Concern | Steal from |
|---------|------------|
| Per-project encoded dirs + DiscoverRelated | `claudecode` |
| Incremental JSONL + msg cache | `codex` / `pi` |
| summary-first metadata | new (Grok-specific), list path simpler than Codex two-pass |
| Tool linking incremental | `claudecode` `toolUseRefs` |
| User query stripping | `claudecode` / `cursor` XML helpers |
| Register + main import | any recent adapter (`geminicli`) |

---

## Implementation plan

### Phase 0 — Fixture harvest (30 min)

Create `internal/adapter/grok/testdata/` from real sessions (scrub secrets if any):

1. `summary_basic.json` — full summary shape  
2. `chat_basic.jsonl` — system, user (with user_query), assistant text, assistant+tool_calls, tool_result, reasoning  
3. `chat_malformed.jsonl` — bad line mid-file  
4. `chat_empty.jsonl`  
5. Directory layout fixture under `testdata/sessions/%2Ftmp%2Fproj/<uuid>/…` for Detect/Sessions tests  

### Phase 1 — Core adapter package

#### 1.1 `types.go`

```go
type SummaryFile struct {
    Info struct {
        ID  string `json:"id"`
        CWD string `json:"cwd"`
    } `json:"info"`
    SessionSummary  string    `json:"session_summary"`
    GeneratedTitle  string    `json:"generated_title"`
    CreatedAt       time.Time `json:"created_at"`
    UpdatedAt       time.Time `json:"updated_at"`
    LastActiveAt    time.Time `json:"last_active_at"`
    NumMessages     int       `json:"num_messages"`
    NumChatMessages int       `json:"num_chat_messages"`
    CurrentModelID  string    `json:"current_model_id"`
    AgentName       string    `json:"agent_name"`
    // ...
}

// Chat line is polymorphic — parse type first, then branch.
type chatLine struct {
    Type string `json:"type"`
    // common flexible fields as json.RawMessage / optional fields
}
```

#### 1.2 Path helpers

```go
func encodeProjectPath(abs string) string  // percent-encode like Grok
func decodeProjectPath(name string) (string, error)
func (a *Adapter) projectSessionsDir(projectRoot string) string
func (a *Adapter) sessionDir(projectRoot, sessionID string) string
func (a *Adapter) chatHistoryPath(...) string
func (a *Adapter) summaryPath(...) string
```

**Verify encoding against live dirs** in a unit test that encodes `/Users/marcus/code/sidecar` and compares to known name `%2FUsers%2Fmarcus%2Fcode%2Fsidecar`. If Grok uses a slightly different escape set than `url.PathEscape`, hardcode the observed algorithm (`/` → `%2F`, leave alphanumerics). Prefer implementing exactly what produces matching dir names on disk.

#### 1.3 `Adapter` methods

| Method | Behavior |
|--------|----------|
| `Detect` | `ReadDir(projectSessionsDir)`; true if any child dir has `summary.json` or `chat_history.jsonl` |
| `Sessions` | For each UUID dir: cache key = `summary path + size + mtime`; parse summary; fill Session fields; set Path=chat_history abs; sort UpdatedAt desc; rebuild sessionIndex |
| `SessionByID` | TargetedRefresher: index lookup → re-read summary |
| `Messages` | Incremental parse of chat_history.jsonl with msg cache (`path+size+mtime`, byteOffset) |
| `Usage` | From last Messages scan cache if available; else best-effort zero / optional updates tail |
| `Watch` | Project dir watcher |
| `WatchScope` | Project |
| `SearchMessages` | `SearchMessagesSlice` |
| `DiscoverRelatedProjectDirs` | Decode sibling dirs under `~/.grok/sessions`; keep those sharing main worktree path prefix |

**Required Session fields:**

```go
adapter.Session{
    ID, Name, // generated_title || session_summary || truncated first user query
    Slug: short ID (first 8 of uuid),
    AdapterID, AdapterName, AdapterIcon,
    CreatedAt, UpdatedAt, // from summary
    MessageCount: num_chat_messages, // or count of user+assistant after parse; prefer summary for list speed
    FileSize: chat_history size,
    Path: abs chat_history,
    TotalTokens: 0 or best-effort,
    IsActive: from active_sessions.json (cached briefly),
    // WorktreeName/Path filled by plugin when multi-root
}
```

#### 1.4 Message mapping details

```
user:
  Content / ContentBlocks text ← cleaned user_query text
  Role = "user"

assistant:
  Role = "assistant"
  Model = model_id
  Content = content string
  for each tool_call:
    ContentBlocks tool_use (ToolInput = arguments string)
    ToolUses append

tool_result:
  Prefer attaching to prior assistant tool_use via ToolUseID
  If orphan, emit synthetic assistant/tool message with tool_result block

reasoning:
  ThinkingBlocks{Content: join summary texts}
  Optional ContentBlocks type=thinking
  Skip if summary empty

system:
  skip
```

Incremental: on file grow, parse only new bytes; on shrink, full reparse.

#### 1.5 Watcher

- Watch project sessions directory.
- On create of UUID dir → watch it; emit SessionCreated when `summary.json` or `chat_history.jsonl` appears.
- On write to `chat_history.jsonl` / `summary.json` → SessionUpdated with SessionID = parent UUID.
- Debounce ~200ms; buffered non-blocking send; clean Close.
- SessionID from parent directory name of the changed file.

### Phase 2 — Registration & conversations UI hooks

#### 2.1 Register

- `internal/adapter/grok/register.go` + blank import in `cmd/sidecar/main.go`.

#### 2.2 Conversations plugin (`view_content.go`)

| Hook | Change |
|------|--------|
| `resumeCommand` | `case "grok": return fmt.Sprintf("grok --resume %s", session.ID)` |
| `renderAdapterIcon` | Distinct color (xAI-ish black/white or cyan) |
| `adapterAbbrev` | `"GK"` |
| `adapterShortName` | `"grok"` |
| `adapterFilterOptions` | Prefer a reserved key (e.g. `"k"` or next free letter) for Grok in the filter menu |

`modelShortName` already maps `grok*` → `"grok"` — no change required.

#### 2.3 Resume modal (`resume_modal.go`)

```go
case "grok":
    agentType = workspace.AgentGrok
```

in `defaultAgentIdxForAdapter`.

#### 2.4 Tests

- Extend `TestResumeCommand` with Grok case.
- Extend `defaultAgentIdxForAdapter` tests if present.

### Phase 3 — Performance hardening

Per create-adapter skill checklist:

- [ ] Metadata cache bounded (2048) keyed by summary path+size+mtime  
- [ ] Message cache LRU (128) with incremental byteOffset  
- [ ] No Abs/EvalSymlinks inside per-session loop  
- [ ] Defensive copies from caches  
- [ ] Sessions() on ~50 sessions under 50ms (benchmark with fixtures)  
- [ ] Messages() cache hit under 1ms; full ~1MB under 50ms  
- [ ] FROZEN tier via Path set  
- [ ] Active sessions file: short TTL cache (e.g. 1s) so list refresh doesn’t thrash it  

Detect must **not** open every chat_history — summary existence is enough.

### Phase 4 — Manual verification

```bash
go test ./internal/adapter/grok/...
go test ./internal/plugins/conversations/ -run 'Resume|Adapter'
go build ./...

# Real app
SIDECAR_BIN=$HOME/go/bin/sidecar ./scripts/tmux-drive.sh start 200 50
# Open conversations plugin, confirm Grok rows, open detail, yank resume, resume modal
```

Manual checklist:

1. Sidecar in `~/code/sidecar` lists recent Grok sessions with titles from `generated_title`.  
2. Open session → user query text clean, tools linked, reasoning visible when toggled.  
3. Copy resume → clipboard is `grok --resume <uuid>`.  
4. Resume → workspace shell gets command pretyped; worktree resume defaults agent to Grok.  
5. Corrupt one summary.json → other Grok sessions + Claude/Codex still load.  
6. Switch project → only that project’s Grok sessions shown.  
7. Worktree with its own Grok history → shows with worktree badge.

### Phase 5 — Docs / changelog

- Move or promote this plan to `docs/implemented/spec-grok-adapter.md` when done.  
- CHANGELOG entry under Unreleased: Grok conversations adapter.  
- Optional: one line in create-adapter skill “Reference Adapters” list.

---

## File checklist

| Path | Action |
|------|--------|
| `internal/adapter/grok/register.go` | Create |
| `internal/adapter/grok/types.go` | Create |
| `internal/adapter/grok/adapter.go` | Create |
| `internal/adapter/grok/watcher.go` | Create |
| `internal/adapter/grok/search.go` | Create |
| `internal/adapter/grok/adapter_test.go` | Create |
| `internal/adapter/grok/watcher_test.go` | Create (optional if covered in adapter_test) |
| `internal/adapter/grok/testdata/**` | Create |
| `cmd/sidecar/main.go` | Blank import |
| `internal/plugins/conversations/view_content.go` | Resume + display hooks |
| `internal/plugins/conversations/resume_modal.go` | Default agent mapping |
| `internal/plugins/conversations/plugin_test.go` | Resume command case |
| `docs/implemented/spec-grok-adapter.md` | Promote from this plan when done |
| `CHANGELOG.md` | Entry |

---

## Risk register

| Risk | Mitigation |
|------|------------|
| Path encoding mismatch across Grok versions | Golden test against observed encoding; if Grok adds alternate encodings, try Abs then EvalSymlinks variants like Claude |
| No message timestamps | Accept zero timestamps or synthetic ordering; UI already sorts by stream order |
| Token/cost incomplete | Ship with MessageCount from summary; tokens optional; EstCost 0 |
| `updates.jsonl` huge | Never open on Sessions(); optional Usage tail-read capped (last N KB) |
| Grok renames fields | Defensive JSON tags; ignore unknown; tests with real fixtures |
| Active sessions file lock/race | Read-only open; ignore errors |
| Double-counting worktree sessions | Same as other adapters; plugin merges by ID — ensure IDs are globally unique UUIDs (they are) |
| Filter key collision in adapter filter menu | Audit `adapterFilterOptions` reserved keys when adding Grok |

---

## Suggested implementation order (PRs)

Single PR is fine for this scope (~parity adapter). If splitting:

1. **PR1 — Adapter only:** package + tests + main import (sessions listable in plugin without resume polish).  
2. **PR2 — UI hooks:** resume, icons, filter, default agent.  

Or one PR: “feat(conversations): Grok adapter” with both.

Estimate: **~0.5–1 day** for someone familiar with adapters; **1–1.5 days** including careful fixtures, DiscoverRelated, and manual verification.

---

## Definition of done

- [ ] `go test ./internal/adapter/grok/...` green  
- [ ] `go test ./internal/plugins/conversations/ -run Resume` green  
- [ ] Real Grok sessions appear in conversations for current project  
- [ ] Detail view shows cleaned user text, assistant text, tools, reasoning  
- [ ] `grok --resume <id>` yank + resume modal → workspace  
- [ ] Bad Grok data does not empty Claude/Codex lists  
- [ ] CHANGELOG updated  
- [ ] td-d240af submitted for review with handoff  

---

## Appendix A — Example mapped message (assistant + tool)

Source:

```json
{
  "type": "assistant",
  "content": "I'll inspect the task.",
  "tool_calls": [
    {
      "id": "call-…-0",
      "name": "run_terminal_command",
      "arguments": "{\"command\":\"td show td-d240af\"}"
    }
  ],
  "model_id": "grok-4.5-build"
}
```

```json
{
  "type": "tool_result",
  "tool_call_id": "call-…-0",
  "content": "exit: 0\n..."
}
```

Target: one assistant `adapter.Message` with text block + tool_use block; tool_result either merged into same turn’s tool_use output or following message with linked `ToolUseID` (match Claude adapter convention in this codebase — prefer linking into the tool_use entry / ContentBlock so Conversation Flow collapses tool pairs).

## Appendix B — Related code pointers

- Adapter contract: `internal/adapter/adapter.go`  
- Skill: `.agents/skills/create-adapter/SKILL.md`  
- Plugin load isolation: `internal/plugins/conversations/plugin_loading.go` (per-adapter timeout)  
- Resume flow: `resume_modal.go` → `workspace.ResumeConversationMsg` → `shell.go` inject  
- Workspace Grok agent: `internal/plugins/workspace/types.go` (`AgentGrok`)  
- Prior workspace Grok work: td-fb5daf (closed)  
