# Herdr parity: close the remaining gap

**Status:** Planning, opened 2026-09-02. This is the controlling plan for the work that remains after [Herdr detection parity](herdr-detection-parity.md) Phases 0 through 5 landed. That plan owns the screen lane and is finished except for its Phase 6, which this document replaces and expands. Nothing here is implemented yet.

**Baseline:** Sidecar `main` at `9b8739f7`; Herdr vendored at `master` `d08e4468`, harness binary pinned at `preview-2026-08-31-b1ff4582e968`. Facts below were read from `internal/agentactivity/manifests/authority.upstream.json`, `aliases.upstream.json`, `upstream.lock.json`, `internal/agentlifecycle/capabilities.json` and the vendored asset tree at that commit, not from memory.

## One sentence

**Sidecar's screen lane is at parity with Herdr and stays there automatically; what remains is the hooks lane, where Herdr installs 17 provider integrations and Sidecar has 3, plus three agents Herdr identifies that Sidecar does not and four identity and signal mechanisms Sidecar never built.**

## Where parity stands today

### The screen lane is done and self-maintaining

Sidecar executes Herdr's manifest grammar at engine version 3, runs their 21 vendored manifests with 22 Sidecar overlay rules layered on top, and a weekly workflow opens a review whenever upstream moves. The review shows the manifest diff, the fixture verdict flips, and which overlay rules have stopped earning their place. A differential harness compares both engines against the real Herdr binary on every sync.

Current evidence: fixture census 61 fixtures, 24 declaring a state, 0 mismatches. Differential harness 61 compared, 37 agree, 0 disagree, 22 overlay divergences, 0 redundant. Merged mode 61 compared, 59 agree, 0 disagree. Two rows have no Herdr oracle, both Muse.

Nothing in this plan changes the screen lane. It is the permanent fallback and it is finished.

### Agent coverage: 20 of Herdr's 23, and the three absences are not the same kind

Herdr's `lookup_agent` table names 23 agents. Twenty-one of those have a screen manifest, and Sidecar vendors all 21. Sidecar registers 20 as families: ten launchable (`claude`, `codex`, `copilot`, `antigravity`, `cursor`, `opencode`, `pi`, `amp`, `grok`, `muse`) and ten detection-only (`cline`, `devin`, `droid`, `hermes`, `kilo`, `kimi`, `kiro`, `maki`, `qodercli`, `qwen`).

| Agent | Herdr has | Sidecar has | Why it is absent |
| --- | --- | --- | --- |
| `gemini` | alias, screen manifest | manifest vendored, no family | Deliberate. Antigravity replaced it and `agy` is already a full family. Registering it later is one alias line. |
| `mastracode` | alias, hooks integration (v2) | nothing | No screen manifest upstream, so there is nothing for the manifest lane to inherit. Reachable only through a hooks port. |
| `omp` | alias, hooks integration (v9) | nothing | Same. Herdr gives it lifecycle authority through hooks and ships no screen rules for it at all. |

So the agent-coverage gap is two agents, not three, and both of them are hooks-lane work rather than detection work. `gemini` is a decision already made.

### The hooks lane is where the real gap is: 3 of 17

Herdr ships 17 installable provider integrations, 33 asset files, all vendored under `internal/agentintegration/upstream/`. Sidecar has three adapters: `claude`, `codex`, `opencode`.

**Where Herdr holds lifecycle authority through hooks** and Sidecar's proved tier is below `full`. This is what `TestHerdrAuthorityGaps` prints today:

| Agent | Herdr integration | Sidecar tier | Sidecar adapter |
| --- | --- | --- | --- |
| `opencode` | v10, plugin | `full` | yes |
| `pi` | v8, extension | `session-identity` | **no adapter exists** (see open question 1) |
| `kimi` | v7, hooks | none | no |
| `kilo` | v4, plugin | none | no |
| `omp` | v9, hooks | none | no |
| `mastracode` | v2, hooks | none | no |

**Where Herdr installs an integration for session identity only** and Sidecar has none. State still comes from the screen for these in both projects, so the gap is exact session binding, not state:

`agy` (v3), `copilot` (v3), `cursor` (v1), `devin` (v2), `droid` (v3), `grok` (v1), `hermes` (v5), `qodercli` (v3), `qwen` (v1). Sidecar covers `claude` (v9) and `codex` (v8) here already.

**Where Herdr ships no integration at all**, so there is no gap: `amp`, `cline`, `gemini`, `kiro`, `maki`, `muse`.

### Other detection mechanisms Sidecar has not built

Four, and they are independent of the hooks work.

1. **Process-tree scoring past generic runtimes.** Herdr keeps a generic-runtime list (`sh bash zsh fish tmux node bun cmd powershell pwsh`, plus a `python[3[.N]]` rule), unwraps a runtime using its argv, and scans the foreground process group preferring the group leader while scoring non-runtime matches higher. Sidecar matches argv[0] basenames only. **Measured consequence:** an agent installed as a plain `#!/usr/bin/env node` shim leaves the interpreter path in argv[0], tmux reports `node`, and neither identity input names the agent, so the pane is never claimed at all. Likely affected among the ten detection-only families: `qwen`, `cline`, `kilo`, `kimi`, `qodercli`. An agent that renames its own process, as Claude Code does, is unaffected. This is the single change that would make the most existing badges appear.

2. **A launch-time agent hint.** Herdr's `HERDR_AGENT=<agent>` on a wrapper command names the manifest to use when a sandbox hides the process. The detection-parity plan says Sidecar has an equivalent in `SIDECAR_AGENT`; it does not. `grep -rn SIDECAR_AGENT internal/` returns nothing. Managed shells already know their launch kind, so the hint matters for unmanaged and sandboxed panes.

3. **`osc_progress` is permanently empty under tmux.** tmux consumes OSC 9;4 and exposes no payload, so seven upstream rules can never fire: `claude` 2, `grok` 4, `qwen` 1. The engine records this as region evidence rather than pretending. This is a tmux limitation, not a Sidecar defect, and closing it needs a terminal-model change well outside this plan. It is listed so nobody re-discovers it.

4. **Herdr's `agent read --source detection`** has a Sidecar equivalent in `explain --file --print-window`, but only offline. There is no live "print exactly the text detection saw for this pane" verb. Minor, and a tuning-loop convenience rather than a parity gap.

## Scope

**In scope:** porting Herdr's integration assets to Sidecar-native ones with Sidecar's own installers, per provider, on Sidecar's transport; registering `mastracode` and `omp` once they have a port; process-tree scoring and the launch hint; and the capability-matrix bookkeeping that keeps all of it honest.

**Out of scope:** the screen lane, the manifest engine, the sync tooling, the runtime fetch, `osc_progress`, and any change to how tiers are earned. A tier is proved by traces from a released provider version and is never copied from Herdr's table.

## Decisions carried forward from the detection-parity plan

These are settled and are not reopened here.

1. **Sidecar-native ports, not a compatibility shim.** Sidecar maintains its own copies of Herdr's assets, installed into the same provider locations by Sidecar's own installers, talking to Sidecar through `sidecar agent report`. Claiming to be Herdr through `HERDR_*` variables buys one agent today and a real identity collision whenever Sidecar and Herdr are nested.
2. **Every asset has two halves.** A provider half (which hook or plugin event maps to `working`, `blocked`, `idle`, or a session reference; the ordering guards; the per-provider quirks) which is the knowledge and is kept verbatim. A transport half (gate on `HERDR_ENV`, write JSON-RPC to `HERDR_SOCKET_PATH`) which is swapped one-for-one for Sidecar's: gate on `SIDECAR_MANAGED_SHELL` and `SIDECAR_BIN`, spawn `$SIDECAR_BIN agent report`. `internal/agentintegration/assets/opencode/sidecar-lifecycle.js` is the reference translation.
3. **A tier is earned by traces, never copied.** Herdr's authority table says which tier is *achievable*. It is a target and nothing more.
4. **Ownership is absolute.** An installer only ever removes changes Sidecar made, identified by the `sidecar-integration:` marker and by nothing else.

## Open questions, to answer before or during the first slice

1. ~~**Sidecar claims a `session-identity` tier for `pi` from a source with no installer.**~~ **Answered 2026-09-02: the entry was recorded ahead of the port, and it is retracted.** No asset exists. `DefaultAdapters()` returns OpenCode, Codex and Claude; `portedFrom` records those same three; `internal/agentintegration/assets/` holds only `opencode`; and `upstream/pi/herdr-agent-state.ts` is vendored read-only, embedded solely so `herdrsync` can diff it. Nothing Sidecar installs could produce a report carrying `sidecar.pi.extension`, so the tier was granted to a source that cannot speak. The `pi` entry is out of `capabilities.json` and the source id is out of `OfficialSources()`, which also stops a hand-written Pi hook from marking a reference resumable through a source Sidecar never wrote. `TestHerdrAuthorityGaps` now prints `pi ... sidecar=(none)` beside the four other hooks-authority agents. The evidence the entry carried is preserved in [the capability matrix](../../reference/agent-lifecycle-capability-matrix.md#pi), which is the brief Slice 1 works from.
2. **Which provider goes first.** Herdr's hooks-authority list is `pi`, `kimi`, `kilo`, `omp`, `mastracode`. The plan below proposes Pi, on the grounds that it is already installed on the maintainer's machine and already has a capability entry to either prove or retract. Confirm before starting.
3. **Whether the nine session-identity ports are worth doing at all.** For those agents Herdr reads state from the screen exactly as Sidecar does, so the port buys exact session binding and nothing else. That is real but small. It may be right to do two or three where a conversation adapter already exists and stop.
4. **How process-tree scoring interacts with Sidecar's stricter process gate.** Sidecar refuses to evaluate a manifest against a pane whose foreground process is not that agent, which is stricter than Herdr and deliberate. Widening identity without widening the gate leaves the badge missing anyway; widening both needs care that one agent's manifest can never read another's screen.

## Work sequence

Each slice is independently shippable and independently reviewable. Sizing is relative, not calendar.

### Slice 1 — Resolve the Pi claim, and port Pi (small, then medium)

~~Answer open question 1 first and act on it: if the `pi` tier is unearned, remove it and let `TestHerdrAuthorityGaps` show `pi sidecar=(none)` until a port earns it back.~~ **Done, 2026-09-02** (`td-d452b1`). The tier was unearned and is retracted, along with the `sidecar.pi.extension` official source; the gap list shows `pi sidecar=(none)`. Read the capability matrix's Pi section before writing the asset: it is the whole of what the retracted entry knew, including the two traps that are not visible in the event names (`agent_settled` rather than `agent_end` for turn completion, and `session_shutdown` firing for three reasons that are not an exit).

Then port Pi as the steel thread for every port that follows, because it is the provider where Herdr's advantage over the screen lane is largest and provable: Pi's upstream manifest has one rule, and it is a *working* rule, so an idle Pi pane only ever reaches the low-evidence fallback. That is why `agent start` on Pi times out waiting for a positive kind match, a failure recorded as pre-existing during the Phase 2 cutover.

- `internal/agentintegration/assets/pi/` — the Sidecar asset, provider half kept verbatim from `upstream/pi/herdr-agent-state.ts`, transport half swapped. Header records `ported-from: herdr pi integration version 8 at <commit>` so the sync report computes the diff on the next bump.
- A `PiAdapter` implementing install, status, update, repair, uninstall. Herdr's `targets.rs` and `config_edit.rs` are the reference for where Pi reads its extensions and what a safe edit looks like, including `PI_CODING_AGENT_DIR` and `PI_CONFIG_DIR` and its refusal on an extension-directory collision.
- Hook-payload fixtures translated to Sidecar's report shape, from Herdr's shared `herdr-agent-state.test.ts`.
- A capability entry starting at the tier the lifecycle plan's rules allow before traces, promoted only on evidence from a released Pi version.

**Exit gate:** a live Pi pane reports `working`, `blocked` and `idle` through hooks; `agent start` on Pi no longer times out; the tier is whatever the traces prove and not more.

### Slice 2 — The remaining hooks-authority providers (medium)

`kimi`, `kilo`, `omp`, `mastracode`, in that order. Each follows Slice 1's shape. `omp` and `mastracode` also gain their first Sidecar identity here: an alias case and a catalog family, detection-only in the sense of Phase 4 except that their state comes from hooks rather than from a screen manifest, since upstream ships none for them.

**Exit gate:** `TestHerdrAuthorityGaps` prints an empty list, or only providers Herdr added since the last sync.

### Slice 3 — Process-tree scoring and the launch hint (medium)

Independent of Slices 1 and 2 and possibly worth doing first, because it makes badges appear for agents already registered.

- A generic-runtime predicate matching Herdr's list, kept distinct from Sidecar's existing `shell` bucket, which is a launch-readiness gate and not a scoring predicate.
- argv-based unwrapping of a runtime, and a foreground process-group scan preferring the group leader and scoring non-runtime matches higher.
- `SIDECAR_AGENT` as the process-identity hint, a hint only and never a lifecycle claim.
- Widen the process gate in step, per open question 4.

**Exit gate:** a Qwen or Cline pane installed as a plain `env node` shim shows a state badge. A test pins that one agent's manifest is never evaluated against another agent's pane.

### Slice 4 — Session-identity ports, as many as earn their place (small each)

Answer open question 3 first. Then port in order of live use, confirming for each that Sidecar's existing screen coverage plus the new session binding is worth the maintenance. Herdr's Claude asset is at v9 and Codex at v8; the sync report already shows the diff against the version Sidecar's adapters were written against, so re-porting those two is a diff review rather than new work.

**Exit gate:** every port has a fixture, a capability entry earned by traces, and a `ported-from` header the sync report can diff.

## Acceptance evidence

- `TestHerdrAuthorityGaps` prints an empty list.
- Every agent in Herdr's `lookup_agent` table has a Sidecar identity, except `gemini`, which is declared out and pinned as such by `TestEveryVendoredManifestIsRegisteredOrDeclaredUnregistered`.
- A live pane for each hooks-authority provider reports state through hooks, with the screen lane as fallback, provable by stopping the integration and watching the lane change in `sidecar agent explain`.
- A node-shim-installed agent shows a badge.
- No capability tier is claimed without a trace from a released provider version.

## Risks

- **Seventeen assets is a maintenance surface, and it is the reason to port selectively rather than exhaustively.** The sync report makes each bump a diff review, which is what keeps this affordable; a port nobody uses is still a diff somebody reads. Slice 4 exists to be cut.
- **Provider hook contracts change faster than manifests.** Herdr's integration versions bumped several times a month across the 0.8.x line. The mitigation is the same one the manifest lane uses: the version is in the lock, the diff is in the report, and the trace tests say which transition moved.
- **A wrong hook mapping is worse than no hooks**, because a hooks-lane verdict outranks the screen lane. Every port lands with fixtures translated from Herdr's own test payloads before it is trusted.
- **Widening process identity can misattribute a pane.** Sidecar's stricter gate is what prevents one agent's manifest reading another's screen; Slice 3 must widen identity and the gate together or not at all.

## Related plans

- [Herdr detection parity](herdr-detection-parity.md) owns the screen lane, the manifest engine, the sync tooling, the overlays, and the opt-in runtime fetch. Implemented through Phase 5. Its Phase 6 is superseded by this document.
- [Deterministic agent lifecycle hooks](notification-agent-lifecycle-hooks.md) owns the report contract, authority arbitration, the capability matrix and the evidence tiers. This plan adds providers to that matrix; it does not change its rules.
- [Herdr agent control and session restore](herdr-agent-control-and-session-restore.md) owns `sidecar agent start/prompt/wait/read`. Those verbs consume `agentactivity.Result` and inherit anything this plan improves. Slice 1's exit gate names one of its failures.
