# Plan: Remove the Conversations plugin

**Status:** Phase 1 implemented (feature-flag soft landing; trial in progress)  
**Date:** 2026-08-11 (updated)  
**Related:** active Grok adapter plan (`td-d240af-grok-conversations-adapter.md`), hold OpenClaw adapter (`docs/plans/hold/openclaw-adapter-spec.md`)

---

## 0. Decision (current)

| Choice | Decision |
|--------|----------|
| Path | **Phase 1 first:** ship a release with Conversations **off by default**, fully inert when off, behind a feature flag. Observe. |
| Later | **Phase 2 (optional):** delete plugin + adapters if Phase 1 lands cleanly and demand stays low. |
| Tab renumbering (risk 2) | **Accept** — workspaces shifts left when conversations is off. |
| Live activity (`agentactivity`) | **Hard keep** — never touch; Phase 1 must not regress workspace activity / overview. |
| In-flight adapter work (Grok / OpenClaw) | **Accept freeze / cancel later** — do not block Phase 1; do not invest in new adapters during the trial. |
| Docs / PRIVACY | **Update in Phase 1** — document default-off, how to re-enable, and that disabled means no session-store reads. |

---

## 1. Intent

### Job to be done today

Conversations is a tab that **reads local harness history** (Claude Code, Codex, Cursor, Warp, …) into a unified list, lets you search/filter/export sessions, and optionally **resume a session into a Sidecar workspace**.

### Why this path (disable → maybe delete)

| Pressure | Evidence |
|----------|----------|
| Maintenance tax | ~**18.5k LOC** plugin + ~**28.5k LOC** adapters; **~250** plugin tests + **~372** adapter tests. CHANGELOG is dense with adapter path/symlink/watch/perf/FD work. |
| Diminishing product value | Each harness already owns its own history UI/CLI. Sidecar does not *own* that data. |
| Startup / FD cost | Adapter `AllAdapters()` + async `Detect()` + tiered watchers cost cycles even for users who never open the tab. |
| Reversibility | Feature flag default-off measures demand without burning the bridge. |

### Product framing for Phase 1

Conversations becomes an **opt-in** experimental surface (same shape as `notes_plugin` / `tasks_plugin`):

- **Default:** off for everyone.
- **Opt in:** set feature flag (config or CLI).
- **Opt out:** clear the flag (or set false) — back to zero cost.
- **Disabled means truly disabled:** no tab, no plugin `Init`/`Start`, no adapter construction, no session-store I/O, no watchers, no resume-modal path from a missing tab.

---

## 2. What users lose when off (default)

| Capability | Native harness coverage | Severity when off |
|------------|-------------------------|-------------------|
| Unified multi-agent session list | None (siloed) | Medium for multi-harness users |
| Cross-session content search | Partial per tool | Medium |
| Project / deleted-worktree archaeology | Weak | Medium for heavy worktree users |
| Resume into Sidecar (`R`) | Paste harness resume cmd | Low–medium |
| Export / yank / token analytics | Harness UI | Low |

**Unchanged when off:** workspaces, live agent activity, git, files, td/tasks, notes, on-disk harness history (never owned by Sidecar).

---

## 3. Architecture snapshot

### Today (always pays adapter tax)

```
main.go
  blank-import adapter/*   → RegisterFactory (cheap)
  AllAdapters()            → construct every adapter instance  ← ALWAYS
       │
assembly.Plan
  if plugins.conversations.enabled (default true)
       │
conversations.Plugin.Start()
  detectAdapters()         → Detect() per adapter (expensive I/O)
  watchers / loads / …
```

### Phase 1 target (flag off = no tax)

```
main.go
  blank-import adapter/*   → still linked (binary size only; no Detect)
  if conversationsWanted():
      AllAdapters()        ← ONLY when wanted
       │
assembly.Plan
  if conversationsWanted():
      register conversations
       │
  (else: no plugin → no Init/Start → no Detect/watch/load)
```

**`conversationsWanted()` (single helper, one definition of truth):**

```text
features.IsEnabled("conversations_plugin")
  AND  cfg.Plugins.Conversations.Enabled
```

| Flag | Config `plugins.conversations.enabled` | Result |
|------|------------------------------------------|--------|
| off (default) | anything | **Off** — no tab, no adapters |
| on | true (default) | **On** — full behavior |
| on | false | **Off** — hard kill-switch without clearing the feature flag |

**Why both knobs:**

- **Feature flag** is the product switch (discoverable, CLI override, matches notes/tasks, easy release note: “enable `conversations_plugin`”).
- **Config `enabled`** remains a secondary hard-off so power users who enable the flag in a shared config can still kill the plugin per-machine without fighting feature defaults. One-knob opt-in is still true: flip only the flag; leave config at default `true`.

**Do not** flip `plugins.conversations.enabled` default to `false` in Phase 1 — that would force two knobs for opt-in. The feature flag alone is the default-off mechanism.

### Hard boundary (risk 3)

```
KEEP forever for Phase 1 (and Phase 2):
  internal/agentactivity/**
  workspace activity_* / agent status / overview icons (styles)
  workspace agent start / pickers

DELETE candidates only in Phase 2:
  internal/plugins/conversations/**
  internal/adapter/**          # history parsers — NOT agentactivity
```

Never “clean up agent code” in a way that greps `agent` and hits `agentactivity`.

---

# Phase 1 — Feature-flag soft landing (ship this)

**Goal:** Release where Conversations is off by default, fully inert when off, trivially re-enableable, and live activity is proven untouched.

**Non-goals for Phase 1:** delete packages, cancel Grok adapter code (can freeze work), remove website page entirely (downgrade messaging instead).

---

## Phase 1.1 — Feature flag registration

Mirror notes/tasks.

**File:** `internal/features/features.go`

```go
// ConversationsPlugin enables the multi-agent session history tab.
// Off by default: history lives in each harness; this is an opt-in viewer.
ConversationsPlugin = Feature{
    Name:        "conversations_plugin",
    Default:     false,
    Description: "Enable the Conversations plugin (multi-agent session history)",
}
```

- Append to `allFeatures`.
- Unit tests in `internal/features/` (copy `tasks_plugin_test.go` pattern):
  - default off
  - config enables
  - CLI override disables after enable
  - listed in `ListAll()`

**User surfaces:**

```json
// ~/.config/sidecar/config.json
{
  "features": {
    "flags": {
      "conversations_plugin": true
    }
  }
}
```

```bash
sidecar --enable-feature=conversations_plugin
sidecar --disable-feature=conversations_plugin
```

Priority already implemented: CLI override > config flags > default.

---

## Phase 1.2 — Shared “wanted” predicate

Avoid three slightly different conditions that drift.

**Recommended location:** small helper next to assembly (or on `features` + config call site).

```go
// conversationsWanted reports whether the Conversations plugin should be
// registered and whether history adapters should be constructed.
// Both the feature flag and plugins.conversations.enabled must be true.
func conversationsWanted(cfg *config.Config) bool {
    if cfg == nil {
        cfg = config.Default()
    }
    if !features.IsEnabled(features.ConversationsPlugin.Name) {
        return false
    }
    return cfg.Plugins.Conversations.Enabled
}
```

Use this in **exactly two places**:

1. `assembly.Plan` — register tab or not  
2. `cmd/sidecar/main.go` — call `AllAdapters()` or leave empty map  

Optional third use: any future diagnostics modal that lists “would load adapters”.

**Tests (`assembly_test.go`):**

| Case | Flag | Config enabled | Expect conversations in Plan |
|------|------|----------------|------------------------------|
| default | unset | true (default) | **absent** |
| flag on | true | true | **present** |
| flag on, config off | true | false | **absent** |
| flag off, config on | false | true | **absent** |

Update existing `TestPlan_*` expectations that currently always include `conversations` in the default string — default Plan becomes:

```text
td-monitor,git-status,file-browser,workspace-manager
```

(with tasks/notes only when their flags are on). Flag-on cases restore `…,file-browser,conversations,workspace-manager,…`.

---

## Phase 1.3 — Zero resource usage when off

### What “truly disabled” requires

| Resource | Today | When `conversationsWanted` is false |
|----------|--------|--------------------------------------|
| Plugin registered | if config enabled | **No** — not in assembly Plan |
| `Plugin.Init` / `Start` / `Stop` | yes | **Never called** |
| Adapter `Detect()` / session load | on Start | **Never** |
| FS watchers / tieredwatcher / coalescer | after detect | **Never** |
| `AllAdapters()` construction | always in main | **Skip** — leave `pluginCtx.Adapters` empty (or nil) |
| Resume modal / `ResumeConversationMsg` | user action in tab | **Unreachable** (no tab); workspace handlers may remain dead code |
| Blank-import of adapter packages | always | **Still linked** — only registers factories in `init`; no file I/O. Acceptable Phase 1 residual (see below). |
| Binary size / compile of adapter code | always | Still present until Phase 2 delete |

### `main.go` change (critical)

Today:

```go
startuptrace.Track("adapter.AllAdapters", func() {
    pluginCtx.Adapters = adapter.AllAdapters()
})
```

Target:

```go
if conversationsWanted(cfg) {
    startuptrace.Track("adapter.AllAdapters", func() {
        pluginCtx.Adapters = adapter.AllAdapters()
    })
}
// else: empty map already set — no factory calls, no adapter state
```

Note: `features.Init(cfg)` already runs before this block in `main` — safe to call `IsEnabled`.

### `assembly.Plan` change

Replace:

```go
if cfg.Plugins.Conversations.Enabled {
    base = append(base, Entry{IDConversations, ...})
}
```

with:

```go
if conversationsWanted(cfg) {
    base = append(base, Entry{IDConversations, ...})
}
```

### Residual cost we explicitly accept in Phase 1

1. **Linker still includes** `internal/adapter/**` via blank imports in `main` — no runtime Detect, but ~binary size and init of `RegisterFactory` appends. Measuring: factory registration is O(adapters) memory with no disk I/O. **Do not** introduce build tags in Phase 1 unless measurement says init is hot (unlikely).
2. **Keymap defaults** for `conversations-*` contexts still registered globally — tiny memory, zero I/O. Optional cleanup later; not required for “no cycles.”
3. **Workspace resume handlers** stay compiled — dead unless a message is sent; no message source without the tab.

### Defense in depth (optional, nice)

If `Plugin.Start` is ever reached with empty adapters (mis-wire), it already no-ops detect over empty map. Still: **never register the plugin when unwanted** so `Start` is not scheduled.

Do **not** add a “disabled plugin stub” that Init’s and shows “enable the flag” — that would reintroduce a tab and partial lifecycle. Off means absent from the tab bar.

---

## Phase 1.4 — Docs & messaging (same release)

| Surface | Change |
|---------|--------|
| **CHANGELOG** | “Conversations plugin is now opt-in via feature flag `conversations_plugin` (default off). When disabled, Sidecar does not construct history adapters or read agent session stores. Re-enable: config flag or `--enable-feature=conversations_plugin`. Tab numbers for later plugins shift left when off.” |
| **README** | Move Conversations under optional/experimental; document the flag; stop implying it is a core always-on tab. |
| **`website/docs/conversations-plugin.md`** | Top callout: default off; how to enable; when off no local session reads. Keep page for opt-in users. |
| **`website/docs/intro.md` / homepage** | Soften “review past conversations” as optional. |
| **`docs/features.md`** | Note default-off + flag name. |
| **`PRIVACY.md`** | State that session-store reads happen **only when `conversations_plugin` is enabled** (and plugin config allows). Default install: those paths not touched for history. |
| **Feature-flags skill / guide** | Add `conversations_plugin` to the table. |
| **Screenshot sequences** | `capture-all-plugins.keys` should skip conversations unless the drive env enables the flag (or document opt-in capture). |

Do **not** delete docs in Phase 1 — opt-in users still need them.

---

## Phase 1.5 — Tests & proof

### Unit / package tests

1. `features`: flag default off + enable paths.  
2. `assembly`: matrix in 1.2; update all default Plan strings.  
3. **Do not** weaken workspace / `agentactivity` tests. If anything, add or keep a smoke that activity status still constructs without adapters populated.

### Manual / isolated proof (required before release)

Use isolated state + private tmux (`scripts/tmux-drive.sh paths` — confirm nothing under `~/.local/state/sidecar` or real config).

| # | Scenario | Expect |
|---|----------|--------|
| P1 | Default config, no flag | No Conversations tab; tab order … files → **workspaces** (or notes/tasks if on). |
| P2 | `--enable-feature=conversations_plugin` | Conversations tab returns; sessions can load (if local history exists). |
| P3 | Flag on + `plugins.conversations.enabled: false` | No tab. |
| P4 | Default off: `SIDECAR_STARTUP_TRACE=stderr` | **No** `adapter.AllAdapters`, no `adapter.Detect:*` lines. |
| P5 | Flag on: startup trace | `adapter.AllAdapters` present; detects only after plugin Start (async). |
| P6 | **Live activity intact (risk 3)** | With flag **off**, open workspace with an agent shell; overview/workspace still shows working/idle activity. **Regression here blocks release.** |
| P7 | Project switch with flag off | No adapter errors, no attempt to read session dirs. |

### Optional instrumentation

If useful for the trial period, one debug log line at startup:

```text
conversations: disabled (feature conversations_plugin off)
```

or

```text
conversations: enabled; adapters constructed=N
```

Keep it debug-level so default UX is quiet.

---

## Phase 1.6 — What we do *not* change in Phase 1

| Leave alone | Why |
|-------------|-----|
| `internal/agentactivity/**` | Live pane heuristics — different product |
| `internal/plugins/workspace/activity_*` | Same |
| Overview icon map in `styles` | Presentation only; no history I/O |
| Adapter source tree & create-adapter skill | Still needed when flag on |
| Grok conversations adapter plan | Freeze investment; cancel only if Phase 2 deletes |
| Workspace `ResumeConversationMsg` handlers | Harmless dead path when tab absent; delete in Phase 2 |
| Keymap conversation bindings | Dead contexts when tab absent; optional later tidy |

---

## Phase 1.7 — Rollout & observation

1. Ship Phase 1 in a normal release.  
2. Watch for: issues titled “where did conversations go”, Discord/GitHub asks to re-enable, anyone depending on resume-from-tab.  
3. Success signals for later delete: little/no re-enable demand over N weeks; no critical workflow reports.  
4. Failure signal: frequent opt-in + complaints about quality → either reinvest or restore default-on (flip `ConversationsPlugin.Default` to `true` — one-line rollback of the product decision without code archaeology).

**Emergency rollback of “default off”:** set `ConversationsPlugin.Default = true` and ship a patch. That is the point of the flag.

**Emergency full restore for one user:**

```bash
sidecar --enable-feature=conversations_plugin
# or features.flags.conversations_plugin = true in config.json
```

---

## Phase 1.8 — Effort (Phase 1 only)

| Work | Rough effort |
|------|----------------|
| Flag + tests | 1–2 hours |
| `conversationsWanted` + assembly + main | 1–2 hours |
| Fix assembly / overview tests that hardcode tab lists | 1–2 hours |
| Docs / CHANGELOG / PRIVACY / website callout | 2–3 hours |
| Isolated proof P1–P7 | 1–2 hours |
| **Total** | **~1 day** |

---

## Phase 1 success criteria (checklist)

- [ ] Feature `conversations_plugin` exists, **default false**, documented.  
- [ ] Default Plan has **no** conversations tab.  
- [ ] `--enable-feature=conversations_plugin` restores tab + loading.  
- [ ] When unwanted: **no** `AllAdapters()`, **no** Detect/session I/O/watchers (startup trace proof).  
- [ ] `plugins.conversations.enabled: false` still hard-disables even if flag on.  
- [ ] **Workspace live activity still works with flag off** (explicit regression gate).  
- [ ] CHANGELOG + PRIVACY + docs describe default-off and re-enable path.  
- [ ] `go test ./...` green.

---

# Phase 2 — Full removal (after trial; not yet scheduled)

Only if Phase 1 observation supports delete.

1. Delete `internal/plugins/conversations/**`  
2. Delete `internal/adapter/**` + blank imports + `plugin.Context.Adapters`  
3. Delete create-adapter skills  
4. Remove resume bridge in workspace  
5. Remove config `ConversationsPluginConfig` (or leave ignored)  
6. Docs/website page removal or “removed in vX” stub  
7. Cancel Grok conversations / OpenClaw-as-conversations plans  
8. `go mod tidy` carefully (notes may still need sqlite)

**Success criteria (Phase 2):** no conversations tab; no adapter package; activity still green; stale config keys ignored.

**Effort:** ~3–5 days after Phase 1.

---

## Risks (updated)

| Risk | Phase 1 handling |
|------|------------------|
| Regret / power users | Opt-in flag; one-line default rollback |
| Tab number muscle memory | **Accepted**; release note |
| **Accidental `agentactivity` breakage** | **Hard keep list; proof P6; no adapter/activity co-deletion** |
| In-flight adapter plans | Freeze; don’t expand during trial |
| Docs/PRIVACY claim session reads always | **Update in Phase 1** — only when flag on |
| Users with old muscle memory enable flag and expect perfection | Flag is “as before,” not a rewrite |
| Residual blank-import cost | Accepted; Phase 2 removes |

### Non-risks

- No transcript data loss on disk.  
- No migration.  
- No default tmux server restart.  
- Live agents in workspaces independent of history adapters.

---

## Alternatives (context)

| Option | Verdict |
|--------|---------|
| Full delete immediately | Deferred behind Phase 1 |
| **Feature flag default-off + zero runtime cost** | **Phase 1 — doing this** |
| Config-only `enabled: false` default | Weaker: no CLI `--enable-feature`, less consistent with notes/tasks; still need to skip `AllAdapters` |
| Keep plugin registered but empty | Rejected — still a tab and lifecycle noise |
| Build tags to omit adapters when off | Overkill for Phase 1; Phase 2 deletes instead |

---

## Open decisions (remaining)

1. **Trial length before Phase 2?** (e.g. one minor release, or N weeks of quiet)  
2. **Phase 2:** delete adapters with plugin (recommended) vs archive elsewhere?  
3. **Website after Phase 2:** remove page vs stub?

Phase 1 does not need these answered to ship.

---

## Appendix A — Phase 1 primary touch files

```
internal/features/features.go              # + ConversationsPlugin
internal/features/conversations_plugin_test.go  # new (or fold into existing)
internal/plugins/assembly/assembly.go      # conversationsWanted
internal/plugins/assembly/assembly_test.go # default plan without conversations
cmd/sidecar/main.go                        # conditional AllAdapters()
CHANGELOG.md
README.md
PRIVACY.md
docs/features.md
website/docs/conversations-plugin.md       # callout
website/docs/intro.md
.agents/skills/feature-flags/SKILL.md      # table row (if present)
docs/guides/... feature flags if any
scripts/sequences/capture-all-plugins.keys # skip or flag-on
```

**Do not touch for Phase 1:**

```
internal/agentactivity/**
internal/plugins/workspace/activity_*.go
internal/plugins/conversations/**   # leave implementation intact
internal/adapter/**                 # leave implementation intact
```

## Appendix B — Phase 2 delete list (reference)

```
internal/plugins/conversations/**
internal/adapter/**
cmd/sidecar/main.go blank imports
internal/plugin/context.go Adapters field
create-adapter skills
workspace resume bridge
…
```

## Appendix C — Size reference

| Component | LOC (approx) | Tests | Phase 1 | Phase 2 |
|-----------|--------------|-------|---------|---------|
| conversations plugin | 18.5k | 250 | keep, unregistered by default | delete |
| history adapters | 28.5k | 372 | keep, construct only if wanted | delete |
| agentactivity | 1.5k | — | **keep** | **keep** |
