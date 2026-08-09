---
name: create-adapter
description: >
  Create conversation adapters for importing AI chat history from different
  tools (Claude Code, Cursor, Warp, Codex, etc.). Covers the adapter.Adapter
  interface, caching strategies, incremental parsing, watch/FD management, and
  performance standards. Use when creating a new adapter, modifying adapter
  behavior, or debugging adapter performance issues. See references/ for
  Cursor DB and Warp SQLite schema details.
---

# Create Adapter

## Why Performance Matters

Adapters are the largest performance risk in Sidecar. Conversations refresh on watch events in a hot path that runs continuously during active sessions:

```
watch event -> coalescer -> session refresh -> adapter.Sessions() -> metadata parsing
```

If an adapter does full directory scans and full-file reparses on every change, CPU and FD usage spike quickly.

## Reference Adapters

Study these before writing a new adapter:
- `internal/adapter/claudecode` - Incremental JSONL parsing, targeted refresh
- `internal/adapter/codex` - Directory cache, two-pass metadata parsing, global watch scope
- `internal/adapter/cursor` - SQLite/WAL-aware cache invalidation, FD-safe DB access
- `internal/adapter/pi` - Global scope, JSONL, CWD-based filtering, session classification, message prefix stripping

## Required Interface

All adapters implement `adapter.Adapter`:

```go
type Adapter interface {
    ID() string
    Name() string
    Icon() string
    Detect(projectRoot string) (bool, error)
    Capabilities() CapabilitySet
    Sessions(projectRoot string) ([]Session, error)
    Messages(sessionID string) ([]Message, error)
    Usage(sessionID string) (*UsageStats, error)
    Watch(projectRoot string) (<-chan Event, io.Closer, error)
}
```

### Required Session Fields

Every session from `Sessions()` must set:
- `ID`, `Name`
- `AdapterID`, `AdapterName`, `AdapterIcon`
- `CreatedAt`, `UpdatedAt`
- `MessageCount`, `FileSize`

`FileSize` is used for dynamic debounce and huge-session auto-reload protection.

Treat source identity separately from lineage. Use the source's durable thread/session ID
for `Session.ID`; parent, root, fork, or lineage IDs describe relationships and must not
collapse distinct sessions. Decode metadata fields defensively when the source has emitted
multiple shapes over time (for example, a string in one version and an object in another).

### Path and Watch Strategy

Set `Session.Path` only when Sidecar should use tiered file watching for that adapter:
- **File-based append-only** (JSONL/log): set `Path` to absolute file path — this opts into TieredWatcher with HOT/COLD/FROZEN tiers
- **DB/WAL adapters** (Cursor, Warp, Kiro): prefer adapter-specific `Watch()` with WAL-aware invalidation; do not set `Path` unless tiered watching covers your write surface

**FROZEN tier**: File-based sessions with `Path` set automatically benefit from the FROZEN tier. Sessions unchanged for 24 hours (`FrozenThreshold`) are excluded from cold polling entirely — zero syscalls. They unfreeze when promoted to HOT (e.g., user selects the session). This is critical for adapters with thousands of session files; without it, `pollColdSessions()` does one `os.Stat()` per file every 30 seconds.

## Performance Standards

### 1) Cache metadata and messages aggressively

Minimum cache keys:
- Metadata: `path + size + modTime`
- Messages: `path + size + modTime`
- SQLite/WAL: include WAL size+mtime in the key

Use bounded LRU behavior for every cache and index. Prune stale paths. Assume caches evict
independently: a hit in one cache must restore any derived state required by another, or the
authoritative source must remain available so eviction cannot change results such as aggregate
usage or ID-to-path resolution.

### 2) Incremental parsing for append-only formats

For JSONL/event-log adapters:
- Cache last parsed byte offset
- Parse only appended bytes
- Fall back to full parse on shrink/rotation/corruption
- Preserve immutable head metadata from prior parse

### 3) Two-pass metadata for large files

When incremental metadata parse is impractical:
- Head pass: ID, CWD, first user message, first timestamp
- Tail pass: latest timestamp, token totals
- Skip middle of large files

When the source owns a metadata index, prefer its read-only index over scanning large event
logs. Probe the schema and required columns before use, open it read-only with bounded/FD-safe
access, and fall back to event-log discovery when it is missing, locked, or incompatible. The
source index is an adapter seam, not a second source of truth to mutate.

### 4) Avoid repeated expensive path work

Resolve project path once per `Sessions()` call (`Abs`/`EvalSymlinks`), reuse for all matches.

### 5) Return defensive copies from caches

Never return cache-owned slices/maps directly. Copy message/session structures to avoid mutation bugs.

### 6) Keep DB access FD-safe

For SQLite adapters:
- Open read-only (`mode=ro`)
- `SetMaxOpenConns(1)`, `SetMaxIdleConns(0)`
- Close rows and DB handles promptly
- Avoid multiple DB connections per `Messages()` call

### 7) Preserve aggregate facts across incremental loads

Usage and similar cumulative facts may arrive as repeated totals or deltas. Define the source
semantics, retain the authoritative aggregate across incremental parsing, and include all
components the source exposes. Do not reconstruct a partial aggregate from whichever message
cache entry survived eviction.

## Watching and FD Management

### 1) Prefer directory-level watches
Do not watch per-session files when directory-level watch gives equivalent signals.

### 2) Implement watch scope
If adapter watches a global path (same location regardless of worktree):
```go
func (a *Adapter) WatchScope() adapter.WatchScope {
    return adapter.WatchScopeGlobal
}
```
This prevents duplicate watchers across worktrees.

### 3) Always emit SessionID when known
Watch events should include session ID for targeted refresh (avoids full reloads).

### 4) Debounce and non-blocking sends
- Debounce bursty write events
- Use buffered channels
- Non-blocking sends: `select { case ch <- evt: default: }`

### 5) Leverage FROZEN tier for file-based adapters
File-based adapters that set `Session.Path` get TieredWatcher's three-tier system (HOT → COLD → FROZEN). Sessions unchanged for 24h are frozen and cost zero polling overhead. This is the primary defense against CPU spikes with thousands of session files. If your adapter has file-based sessions, always set `Path` — the FROZEN tier scales automatically.

### 6) Ensure cleanup
All watcher paths must close cleanly on plugin stop. No goroutine or FD leaks.

### 7) Combine known-file and discovery watching when needed

Tiered watching of known `Session.Path` values handles cheap appends, but it cannot discover a
project's first session or a new time-partitioned directory. Global file adapters may need both
known-file watching and one adapter-native discovery watcher. Watch creation at every directory
level that can appear later (including month/year rollover), and keep discovery project-filtered.

### 8) Resolve path IDs through the adapter

Do not assume a new file's basename is its session ID. If identity lives in metadata, implement
`SessionPathResolver` so watch events carry the same durable ID returned by `Sessions()`.

## Adapter Call Lifecycle

Some global adapters maintain caches and indexes across calls. Consumers must serialize calls
to a stateful adapter, make gate admission and work lifecycle/epoch cancellable, and reject stale
results after project switches or shutdown. A slow `Sessions()` result must remain observable
and eventually load (with a visible loading state); never time it out, silently discard it, and
leave its goroutine running. Global targeted refresh must admit only sessions already belonging
to the current project; unknown IDs require a project-filtered discovery pass.

## Message and Content Rendering

Adapters must provide rich structured content for Conversation Flow UI.

### Required message mapping
Map source records to:
- `Message.Role`, `Message.Content`, `Message.ContentBlocks`
- `Message.ToolUses` (legacy compatibility)
- `Message.ThinkingBlocks` (if available)
- `Message.Model` when available

### Tool linking rule
Use consistent `ToolUseID` for `tool_use` and `tool_result` blocks. If incremental parsing is used, preserve pending tool-link state across cache updates.

## Optional Interfaces

### TargetedRefresher
```go
type TargetedRefresher interface {
    SessionByID(sessionID string) (*Session, error)
}
```
Reduces refresh from O(N sessions) to O(1). Implement when adapter can resolve a session directly.

### ProjectDiscoverer
Implement when source format allows discovery of sessions beyond current git worktrees.

### SessionPathResolver
Implement when a file path alone does not encode the source's durable session ID. Tiered and
discovery watchers use it to turn new or changed paths into targeted refresh events.

### ProjectDiscoveryWatcher
Implement for global sources that must discover the first matching session even when
`Sessions()` initially returns no known files. Share only one global watcher per adapter, and
ensure its events trigger a project-filtered load before any session is admitted.

## Error Handling

- `Detect()`: return `(false, nil)` for missing data directories
- `Sessions()`: skip corrupt/unreadable entries and continue; hard-fail only on systemic errors
- `Messages()`: return `nil, nil` for missing session files; fail on parse errors
- `Watch()`: return `(nil, nil, err)` when watch setup fails
- Slow calls: surface loading/error state and accept the eventual result; do not translate a
  consumer timeout into a false empty history

## Benchmark Targets

New adapters should meet these performance targets:
- `Messages()` full parse (~1MB): under 50ms
- `Messages()` incremental append: under 10ms
- `Messages()` cache hit: under 1ms
- `Sessions()` on 50 session files: under 50ms

Also benchmark realistic source shapes: hundreds or thousands of indexed sessions, a large live-
scale transcript, cache hits, and incremental appends. Record fixture size and session/event count
with the result so a tiny synthetic benchmark cannot hide discovery or parsing regressions.

## Testing Requirements

Required tests for every new adapter:
- Relative vs absolute project path behavior in `Detect()`/`Sessions()`
- `Sessions()` sorted by `UpdatedAt desc`
- Required session fields populated (`Adapter*`, `FileSize`, `Path` when applicable)
- Cache hit behavior (no reparsing on unchanged files)
- File growth behavior (incremental parse path)
- File shrink/rotation behavior (fallback full parse)
- Tool use/result linking (including incremental append cases)
- Incremental `ContentBlocks` tool-use/result ID parity, not only legacy `ToolUses`
- Aggregate usage across multiple events, cache-write/read components, and independent cache eviction
- Watcher event emission includes `SessionID`
- Watcher cleanup (no leaked closers)
- Repeated-call FD stability
- Zero-history project followed by its first session creation
- Time-partition rollover (for example, a new month under an existing year)
- Global discovery and targeted refresh remain isolated across two projects/worktrees

Run tests:
```bash
go test ./internal/adapter/<adapter> -run .
go test ./internal/adapter/<adapter> -bench . -benchmem
```

## PR Compliance Checklist

### A) Correctness
- [ ] Full `adapter.Adapter` contract implemented
- [ ] `Sessions()` sets required identity and timestamp fields
- [ ] Durable source identity is distinct from parent/root lineage; historical metadata shapes parse
- [ ] `FileSize` populated for every session
- [ ] `Path` strategy explicit and correct for adapter type
- [ ] Message role/content mapping correct
- [ ] `ContentBlocks` include text/tool/thinking data
- [ ] Tool result linking correct (`ToolUseID` parity)

### B) Performance
- [ ] Metadata cache implemented and bounded
- [ ] Message cache implemented and bounded
- [ ] Every auxiliary index/aggregate cache is bounded and correct under independent eviction
- [ ] Incremental parse or two-pass strategy implemented
- [ ] Source-owned metadata indexes are read-only, schema-probed, and have a safe fallback
- [ ] No repeated `Abs/EvalSymlinks` in per-session loops
- [ ] No duplicate parsing for single-pass data
- [ ] Benchmarks added with realistic fixtures

### C) FD / Watching
- [ ] Directory-level watches preferred
- [ ] Global adapters implement `WatchScopeProvider`
- [ ] Global discovery is project-isolated, including zero-history first creation
- [ ] Watch events include `SessionID`
- [ ] Metadata-backed IDs resolve through `SessionPathResolver`
- [ ] Debounce + buffered + non-blocking send pattern
- [ ] DB adapters account for WAL in invalidation/watch
- [ ] Known-file and discovery watches cover time-partition rollover without duplication
- [ ] Watchers and goroutines close cleanly

### D) Integration
- [ ] Adapter registered via `register.go` and main import
- [ ] Search uses adapter `Messages()` path
- [ ] Large-session behavior validated (`FileSize`-driven)
- [ ] Slow/global calls are serialized, lifecycle-cancellable, and never discarded as empty

## Session Classification

Adapters can classify sessions by setting `SessionCategory` on `adapter.Session`. The conversations plugin supports category filtering (f menu: i/r/s keys) and a quick toggle (C key).

### Category Constants

Defined in `internal/adapter/adapter.go`:
- `adapter.SessionCategoryInteractive` — user-initiated interactive sessions
- `adapter.SessionCategoryCron` — automated/scheduled sessions
- `adapter.SessionCategorySystem` — system/gateway sessions

### Implementation Guidelines

- Classify during metadata parsing (zero extra I/O) — extract category from the first user message or session header
- Only set `SessionCategory` if the adapter has meaningful categories. Don't set it if all sessions are the same type
- If the category filter is active and `SessionCategory` is empty, sessions pass through (non-breaking for adapters that don't classify)
- Gateway/system messages may need special classification — e.g., "System: WhatsApp gateway connected" is actually interactive, not system. Check for known preamble patterns before defaulting to system category

### Example (from Pi adapter)

```go
func extractSessionMetadata(firstUserMessage string) (category, cronJobName, sourceChannel string) {
    if strings.HasPrefix(firstUserMessage, "[cron:") {
        return adapter.SessionCategoryCron, extractCronJobName(firstUserMessage), ""
    }
    if strings.HasPrefix(firstUserMessage, "System:") {
        if strings.Contains(firstUserMessage, "WhatsApp gateway") {
            return adapter.SessionCategoryInteractive, "", "whatsapp"
        }
        return adapter.SessionCategorySystem, "", ""
    }
    return adapter.SessionCategoryInteractive, "", detectSourceChannel(firstUserMessage)
}
```

## Rich Metadata Fields

Optional fields on `adapter.Session` for richer display and filtering:

- `CronJobName string` — for cron/scheduled sessions; used as session name when set
- `SourceChannel string` — for multi-channel adapters (e.g., "telegram", "whatsapp", "direct")

Optional field on `adapter.Message`:

- `SourceLabel string` — per-message source attribution badge (e.g., "[TG] Marcus", "[WA]", "[cron] job-name")

Set these during parsing when the source format contains channel/origin metadata. The conversations plugin and conversation flow UI use these for display.

## Message Content Cleaning

For adapters whose source format embeds structured prefixes in user messages (e.g., channel tags, cron headers), strip them during parsing to keep the conversation view clean.

### Pattern

1. Extract metadata (source label, channel, category) from the raw message prefix
2. Strip the prefix from `Message.Content` and text `ContentBlocks`
3. Store the extracted label in `Message.SourceLabel` for badge display

```go
// In processMessageLine for user messages:
content, _, _, contentBlocks := parseContent(raw.Message.Content)
sourceLabel := extractSourceLabel(content)    // "[TG] Marcus"
content = stripMessagePrefix(content)          // clean body only
for i := range contentBlocks {
    if contentBlocks[i].Type == "text" {
        contentBlocks[i].Text = stripMessagePrefix(contentBlocks[i].Text)
    }
}
msg := adapter.Message{
    Content:       content,
    ContentBlocks: contentBlocks,
    SourceLabel:   sourceLabel,
}
```

This keeps `Content` human-readable while preserving origin metadata in `SourceLabel`.

## Global Adapter Gotchas

Lessons learned from building global-scope adapters (Pi, Codex):

### CWD-based Project Filtering

Global adapters (`WatchScopeGlobal`) store sessions in a single directory regardless of project. They must filter by CWD matching `projectRoot` in `Sessions()`:

- Resolve `projectRoot` once per `Sessions()` call (Abs + EvalSymlinks)
- Read CWD from the cheapest authoritative source (a read-only metadata index or event-log header), and bound any cache used to avoid full-file parses for non-matching sessions
- Match with `filepath.Rel` — a session matches if its CWD is equal to or a subdirectory of the project root

### Category Filter Interaction

- The conversations plugin category filter only filters sessions that HAVE a `SessionCategory` set — empty passes through
- Don't enable category filter by default in the plugin — it breaks non-classifying adapters
- When adding classification to a new adapter, test that existing adapters without categories still display correctly

### Project Switching

Global adapters need to handle project switching gracefully:

- Each `Sessions(projectRoot)` result is project-filtered, even when adapter caches retain source-global metadata
- Cross-project/path identity indexes may accumulate across serialized calls, but must be bounded; eviction must fall back to authoritative index/log lookup rather than changing resolution behavior
- Project reinitialization cancels and replaces the prior watcher lifecycle/epoch; stale loads and events must be rejected
- Cache entries keyed only by source path may be shared across projects, while membership in a returned session list remains project-specific

### Watcher Lifecycle and Refresh

- Create only one adapter-native global discovery watcher within the active lifecycle, alongside tiered watches for known files when applicable
- Target refresh only for IDs already admitted to the current project's session list, and only through their owning adapter
- Treat an unknown ID from a global watcher as discovery: run a project-filtered full load before admitting it
- Ensure watch goroutines and queued calls use the current lifecycle context and cannot retain stale project state

## Schema References

See `references/cursor-db-format.md` for Cursor's per-session SQLite database structure (Merkle tree blobs, hex-encoded metadata, WAL considerations).

See `references/warp-sqlite-schema.md` for Warp's single SQLite database structure (ai_queries, agent_conversations, blocks tables, protobuf tasks).
