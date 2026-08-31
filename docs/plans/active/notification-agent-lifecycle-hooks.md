# Deterministic agent lifecycle hooks

**Status:** active; Phase A and Phase B implemented on 2026-08-30, Phase B exit gate passed live against real OpenCode 1.18.25 (3/3 runs), Phase C onward planned. M6 of [Notification sounds and native delivery](notification-sounds-and-native-delivery.md) **Tracking:** `td-43a93f` (Phase B: `td-b5a467`) **Created:** 2026-08-30 **Updated:** 2026-08-30 **Evidence baseline:** Sidecar `main` at `48ddd720`; OpenCode 1.18.23 and 1.18.25, Codex 0.151.0, Claude Code 2.1.220, Pi 0.84.3 surveyed, OpenCode traced including cancellation. Herdr `4a3b04f59ba3b7d8a15cea187b23e1e80c343b0c` inspected for comparison.

This is the controlling plan for Herdr-like agent lifecycle integrations in Sidecar. It covers provider hooks and plugins, a deterministic reporting contract, lifecycle-authority arbitration, installation and diagnostics, and the fallback to Sidecar's existing process/title/screen detection. The parent notification plan continues to control notification records, event policy, sound/native delivery, duplicate prevention, and SSH notification transport. [Herdr agent control and session restore](herdr-agent-control-and-session-restore.md) continues to control starting, prompting, waiting for, reading, resuming, and restoring agent sessions; its session-identity reports may share this initiative's integration and storage seams but do not gain lifecycle authority automatically.

## Outcome

When a supported agent starts work, needs input, finishes a turn, or ends a session, its own lifecycle events can tell Sidecar what happened. Sidecar no longer has to infer every transition from pixels, status lines, spinner text, or timing. A hook report is still only evidence: one shared resolver decides whether the source is complete and current enough to author the pane's lifecycle state. Existing screen and process observation remains available whenever the integration is absent, incomplete, stale, conflicting, or unhealthy.

The useful local journey is:

1. Open **Configuration → Agents → Integrations** and inspect which installed agent CLIs have a Sidecar integration available.
2. Install or update one integration after reviewing the exact user-level files it will change.
3. Start the agent in a Sidecar-managed shell and send a prompt.
4. The integration reports `working`, then `blocked` or `idle`, against that exact tmux pane and agent run. Sidecar's project Workspace and global Sessions views show the same resolved state.
5. The resolved transition enters the existing `notify.LaneTracker` once, so an enabled background notification follows the same store, policy, claim, sound, native, and later SSH path as a screen-detected transition.
6. Run `sidecar agent explain --current --json` to see the effective state, authoritative source, last report, integration health, and fallback reason without opening the TUI.
7. Uninstall or break the integration and confirm that the pane returns to ordinary screen/process detection rather than becoming frozen or silent.

The first implementation steel thread is one provider whose public integration API and recorded fixtures cover prompt submission, tool work, permission/question blocking, completion, cancellation, session change, and process exit. **Phase A confirmed OpenCode as that provider** against the released 1.18.23 API; see the Phase A evidence record below for the traces, the gaps, and why the alternatives lost. Codex and Claude follow through the same core only after their current hook events are traced across the same cases. No provider receives full lifecycle authority based on documentation or a happy-path test alone, and Phase A's own evidence is not yet sufficient to grant OpenCode that authority either.

## Why this is a separate initiative

The existing notification implementation already consumes resolved agent lanes correctly. Its remaining weakness is upstream: `internal/agentactivity` derives those lanes from process metadata, terminal title, and captured screen content, with provider-specific heuristics for Codex, Claude, Grok, Cursor, OpenCode, Pi, Copilot, Amp, and Antigravity. Those heuristics remain valuable coverage for unmanaged and unsupported sessions, but they are vulnerable to wording, theme, width, animation, localization, and provider-version changes.

Lifecycle hooks cross several independent boundaries: provider configuration mutation, hook asset versioning, cross-process state, pane and session identity, event ordering, source trust, fallback arbitration, Configuration and CLI parity, and remote-host behavior. Treating that as a small notification adapter would put business logic in installers or hooks and create a second event path. M6 instead establishes a reusable lifecycle-reporting seam that every Sidecar surface and notification consumer inherits.

## Scope

### In scope

- A provider-neutral lifecycle report model for `working`, `blocked`, and `idle`, plus explicit authority release and a terminal outcome (`completed`, `cancelled`, `failed`, or `unknown`) where the provider can report it.
- Stable binding to host, tmux server incarnation, pane, provider process generation, agent run, and optional provider session identity.
- Monotonically ordered reports per source and run, with stale, duplicate, cross-pane, and prior-run reports rejected before arbitration.
- A narrow JSONL-backed report store behind an interface, readable by Sidecar processes without requiring a running TUI or daemon.
- One state-free resolver that combines hook/plugin reports, integration capability, process identity, and existing screen observation into the effective `agentactivity.Result` and explains the choice.
- Explicit capability tiers: full lifecycle authority, advisory event hints, session identity only, and screen-only fallback.
- Versioned bundled integration assets and provider-specific config adapters that install, update, repair, inspect, and uninstall only Sidecar-owned changes.
- A complete non-interactive CLI with human and JSON output for reporting, releasing, explaining, listing, installing, updating, repairing, and removing integrations.
- A Configuration surface over the same application service, including capability, installed/current/outdated state, authority tier, target paths, privacy copy, and actionable errors.
- A documented custom-reporter contract. Custom sources remain advisory in the initial initiative; explicit lifecycle trust is deferred until a concrete custom integration proves the need and coverage.
- Local and registered-remote operation. Hooks run beside the agent on the host that owns the tmux pane and write only that host's lifecycle state.
- Focused unit, fixture, cross-process, isolated tmux, real-provider, remote, performance, privacy, and independent-review evidence.

### Deliberately out of scope

- Removing screen, title, or process detection. It remains the permanent compatibility and recovery path.
- Letting hooks or plugins call `sidecar notify post`, play sounds, emit terminal protocols, or choose delivery policy. They report lifecycle facts only.
- Automatically installing integrations, silently editing provider configuration, or installing repository-scoped hooks from an untrusted checkout.
- Making a session-identity-only hook authoritative for lifecycle state.
- Starting, prompting, resuming, restoring, or replacing agents. Those belong to [Herdr agent control and session restore](herdr-agent-control-and-session-restore.md).
- A new Sidecar daemon, listening socket, network service, generic plugin runtime, or arbitrary command-execution hook framework in the first release.
- Sending lifecycle reports across an ad hoc SSH command or writing directly into another machine's state. Registered hosts resolve state on the remote host and the notification M5 stream carries the resulting event to viewers.
- Provider-specific behavior that cannot be represented by the shared lifecycle contract. Preserve it as diagnostic evidence until a real cross-provider need justifies extending the model.
- Treating subagent activity as the parent pane's blocking state without a proved provider-specific aggregation rule.

## Current state and seams to preserve

- `internal/agentactivity` owns provider detection and the temporal `Tracker`. `Observation` carries process, title, and screen evidence; `Result` exposes state, evidence, visibility, and fallback-idle semantics. Provider-specific detectors should remain pure fallback adapters.
- `internal/agentstatus.Resolve` turns tracked activity, provider health, orphaning, and freshness into the presentation used by both project and global workspace surfaces.
- Project Workspace currently applies `agentactivity.Detect` independently in `internal/plugins/workspace/agent.go` and `internal/plugins/workspace/shell.go`. `internal/workspaceinventory` has its own detection path for global Sessions and remote-host inventory. M6 must extract one observation-plus-authority resolver used by all three sites; changing only `workspaceinventory` would leave the project surface and its notifications screen-only.
- `internal/plugins/workspace/agent_triggers.go` is the existing project-Workspace adapter from resolved lanes into `notify.LaneTracker`. It remains the sole local project bridge from agent state to notification events. The parent plan's M5 remote status adapter is deliberately a second consumer of the same resolved-state and `LaneTracker` rules at the host-stream boundary, not a second lifecycle classifier.
- `internal/notify.LaneTracker` owns transition-to-notification semantics, waiting lifecycle, and duplicate prevention. Hook code must not replicate these rules.
- `internal/workspaceops` creates Sidecar-managed tmux shells and already publishes stable Sidecar shell identity in the tmux environment. M6 extends that environment contract rather than writing pane identity into provider assets.
- Sidecar has no always-running local daemon and does not need one for the first steel thread. A short-lived report CLI and a locked JSONL store keep the capability usable by hooks, humans, tests, and future callers even when no TUI is running.
- Registered remote hosts already calculate agent presentations where their panes live. M6 changes that input on the remote side; M5 remains responsible for transporting a resulting notification event to the local viewer.

## Settled architecture

### Lifecycle report contract

Add a provider-neutral `internal/agentlifecycle` package with no CLI, TUI, tmux, provider-config, or notification dependencies. Its report record contains:

- schema version and report ID;
- host identity and tmux server incarnation;
- pane ID, provider, integration source, and installed integration version;
- Sidecar-assigned agent run ID, matching provider process generation, and optional salted provider-session fingerprint;
- state `working`, `blocked`, or `idle`;
- strictly increasing sequence within `(server incarnation, pane, source, run)`;
- observed time and a bounded, sanitized diagnostic reason code/message;
- report kind `state`, `session`, `end`, or `release`; `end` carries the bounded terminal outcome rather than inventing another steady-state lane.

The report command derives host, server, pane, and matching provider process identity from the current Sidecar-managed environment, tmux context, and hook process ancestry. A provider session identifier may establish or rotate a run, but the lifecycle store retains only a host-salted fingerprint; the exact validated reference, when session restore needs it, goes separately to the `agentsession`-owned shell binding. When no session identifier exists, Sidecar assigns a run epoch from the matching provider process generation. Provider input cannot choose a different host, pane, process generation, or arbitrary Sidecar run ID. An explicit test-only diagnostic override may be injected behind the application service, but it is not part of the ordinary hook command.

The core validates enum values, byte and Unicode bounds, time skew, source/provider compatibility, sequence monotonicity, session continuity, and current process identity before appending. Replayed reports are idempotent. A new run or session reanchors sequence without allowing a late report from the prior run to regain authority.

### State store and cross-process behavior

Use an append-only JSONL store under Sidecar's host-local state directory, behind `Append`, `Latest`, `Release`, `List`, and `Compact` operations. Follow the repository's existing lock, atomic append, malformed-line tolerance, and repairable fold conventions. Namespace records by tmux server incarnation so a recycled `%pane` identifier after a server restart cannot inherit authority.

The store is shared by all Sidecar processes on that host. Writers take a bounded exclusive lock; readers fold only new records and retain a small in-memory latest-state index. Compaction keeps the latest live report and a bounded diagnostic history per source/run, never provider prompt or tool content. An unreadable or untrusted store disables hook authority and returns to screen detection; it does not guess, block rendering, or prevent the agent from running.

The JSONL history is lifecycle evidence, not a durable notification queue. A Sidecar process seeds its tracker from the latest valid report when it starts or first sees a pane; it does not replay intermediate `working` → `blocked` → `idle` reports into notifications. Only a report observed as a live change for the current tracked run can advance the existing notification seam, subject to the parent plan's live-event and dedupe rules.

A direct file-backed CLI is the boring default because it works without a TUI, is inspectable by an agent, and introduces no service lifecycle. Keep storage and reporting behind interfaces so a future local socket can replace report transport if measured process-start or locking cost becomes material.

### Authority and fallback arbitration

One pure resolver receives current process identity, installed integration metadata, the latest valid reports, existing screen result, and time. It returns the effective activity result plus an explanation with source, tier, freshness, and fallback reason. Project worktree polling, project managed-shell polling, and `workspaceinventory` call this same resolver after building their ordinary `agentactivity.Observation`; no surface owns a private authority decision.

| Tier | Meaning | State behavior |
| --- | --- | --- |
| Full lifecycle | The installed provider/version has proved coverage for work, blocking, unblocking, completion, cancellation, session change, and process exit. | A fresh same-run report authors `working`, `blocked`, or `idle`. Screen evidence remains diagnostic and cannot reverse it. |
| Advisory | The integration has useful explicit events but known lifecycle gaps. | A fresh event may strengthen a matching transition, but missing events never suppress screen/process detection. Exact precedence is fixture-tested per event kind. |
| Session identity | The integration reliably identifies the provider session but not state. | Session metadata enriches diagnostics and future resume behavior; screen/process detection remains the sole lifecycle authority. |
| Screen fallback | No valid integration evidence is available. | Existing provider detector and tracker behavior is unchanged. |

Authority belongs to a specific source version and observed run, not to a provider name forever. The bundled capability registry records the minimum and tested version range, lifecycle coverage, event mappings, and known gaps. Unknown or newer provider versions begin as advisory until compatibility is proved; an outdated asset reports `outdated` and follows its last proved tier only when the provider contract is still within the tested range.

Process exit, pane replacement, server-incarnation change, explicit `release`, integration removal, session mismatch, expiry, or repeated invalid reports clears full authority. A valid `end` report records its outcome, clears lifecycle authority, and can contribute terminal health evidence to the shared status projection; process liveness still confirms that the matching run actually ended before Sidecar treats the pane as orphaned or failed. The resolver then uses current screen/process evidence immediately; it does not retain a stale `blocked` or `idle` lane through a grace period. A temporary lack of hook events while the same run is working must have a bounded freshness policy derived from real provider event cadence, with the exact timeout visible in `explain` output.

Only the shared resolved result advances each surface's existing `agentactivity.Tracker` and `agentstatus` projection. Project Workspace and the M5 remote host adapter then consume those projections through independent `notify.LaneTracker` instances at their existing ownership boundaries. A hook-to-idle transition and a simultaneous screen-to-idle transition therefore become one state transition per owning adapter, with the parent plan's logical dedupe and claims preventing duplicate local records or delivery across processes. Hooks never create notification IDs or delivery claims.

### Managed shell environment

Extend Sidecar-managed shell creation with a small documented environment contract:

- a boolean cue that the command is running inside a Sidecar-managed shell;
- the stable Sidecar shell/session identity;
- the tmux server incarnation and current pane identity, verified again by the report command;
- the absolute path or argv-safe command for the active Sidecar binary when provider hook formats require it;
- the host-local state namespace, without exposing a writable path supplied by provider input.

Hooks no-op successfully and silently outside that environment. Existing shells continue through screen detection until recreated or their agent is relaunched with the new environment. The contract is harness-agnostic: provider adapters translate native events into it, but no core type mentions Codex, Claude, OpenCode, Pi, Cursor, or another vendor.

### Integration assets and configuration mutation

Add `internal/agentintegration` as the application service used by CLI and Configuration. Provider adapters own discovery, current-version capability, target paths, parsing, merge, validation, install, update, repair, and uninstall. Bundled assets carry a Sidecar integration ID, schema version, asset version, and checksum.

Installation is explicit and operates only on the user's machine-level provider configuration. Before mutation, show the exact paths and planned changes. Reject unsafe symlinks, unexpected owners/types, malformed configuration that cannot be preserved, and permissions that would broaden access. Write atomically, retain a recoverable backup when replacing an existing user file, and preserve unrelated hooks, plugins, settings, order, comments where the format supports them, and unknown future fields. An uninstall removes only the exact Sidecar-owned entry or managed asset; it never rewrites the file from a default template.

Do not install project-local hook files in a repository. Provider formats that require an executable asset get a versioned Sidecar-owned file in an application state/config directory, not a generated shell string. Invoke Sidecar directly with fixed argv, pass provider JSON through bounded stdin where required, and never interpolate prompt, tool, path, or environment content into a shell command.

Integration status is one of `provider-missing`, `not-installed`, `current`, `outdated`, `needs-repair`, or `unsupported`. It also reports the effective authority tier and reason. Discovery and status checks run lazily from a `tea.Cmd` or explicit CLI request; none runs in plugin `Init`, `Start` before returning its command, `View`, or the first-frame path.

### CLI and Configuration parity

The initial owned surface is:

```text
sidecar agent report --state working|blocked|idle --source SOURCE --provider PROVIDER --seq N [--session-id ID] [--reason CODE] [--json]
sidecar agent end --outcome completed|cancelled|failed|unknown --source SOURCE --provider PROVIDER --seq N [--session-id ID] [--reason CODE] [--json]
sidecar agent release --source SOURCE --provider PROVIDER --seq N [--session-id ID] [--reason CODE] [--json]
sidecar agent explain [--current | --shell TARGET] [--json]
sidecar agent integration list [--json]
sidecar agent integration status [PROVIDER] [--json]
sidecar agent integration install PROVIDER [--dry-run] [--json]
sidecar agent integration update PROVIDER [--dry-run] [--json]
sidecar agent integration repair PROVIDER [--dry-run] [--json]
sidecar agent integration uninstall PROVIDER [--dry-run] [--json]
```

`report`, `end`, and `release` are deterministic, non-interactive hook surfaces. They consume provider input only through fixed flags or bounded stdin defined by that adapter, write no human text on success unless requested, return quickly, and fail open from the agent's perspective: a reporting failure is diagnostic but never changes the provider's decision or output. Human and JSON errors distinguish invalid context, stale sequence, run mismatch, unsupported source, store failure, and accepted-but-advisory evidence.

**Configuration → Agents → Integrations** lists discovered providers and custom sources. It shows installed/current version, authority tier, last valid report, target paths, provider availability, fallback status, and privacy behavior; it offers install, update, repair, uninstall, recheck, and open-documentation actions through the same application service. Mutating actions require an explicit confirmation that names the files. Every mutating CLI command supports `--dry-run` with the exact ordered file operations and before/after ownership status in human and JSON output, so an agent can inspect the change before authorizing the explicit mutation. `sidecar agent explain` provides every diagnostic fact shown in the UI, and every UI mutation has an equivalent non-interactive command with structured output.

Custom integrations use the same report schema and environment contract. They default to advisory. A later implementation slice may add an explicit configuration/CLI trust record for lifecycle authority, but it must name the source, version range, covered events, and revocation path; accepting a report cannot implicitly grant authority.

No HTTP API or MCP surface is required for the local steel thread because the hook and integration capability is owned and complete through the CLI. The application service and pure contracts remain transport-free so a remote or agent tool API can be added without moving rules out of the core.

### Provider rollout policy

Before writing an adapter, capture the current official event contract and sanitized real event fixtures for that provider version. The matrix is evidence, not aspiration:

| Provider group | Initial plan | Promotion gate |
| --- | --- | --- |
| OpenCode | **Confirmed Phase A steel thread; promoted to `full` in Phase B once cancellation was traced.** | Real traces cover work, tool use, permission/question blocking and resolution, completion, cancellation, child sessions, restart, and process exit. Traced: everything except child sessions, which are not required for `full` and remain an honest recorded gap. |
| Codex | Build through the shared handler after the steel thread; current official hooks expose broad lifecycle events. | Real traces prove the required state transitions, ordering, stop/failure behavior, compaction, subagent isolation, and version compatibility. |
| Claude Code | Build through the shared handler after the steel thread; current official hooks expose session, prompt, tool, permission, notification, subagent, stop, and failure events. | Real traces prove that every completion and blocker path maps without screen-only gaps, including user cancellation and permission resolution. |
| Pi | Evaluate the extension shape demonstrated by Herdr as a second independent plugin model. | The released extension API and real traces meet the full-lifecycle contract without reaching into provider internals. |
| Cursor, Grok, Copilot, Amp, Antigravity, and other catalog agents | Retain current screen/process behavior while capability discovery proceeds. | A stable supported hook/plugin API and complete fixtures justify advisory, session-only, or full authority honestly. |

Where coverage is incomplete, ship the integration at its proved lower tier or do not install it. Do not add provider polling, patch provider binaries, scrape private databases for live state, or label session-identity callbacks as deterministic lifecycle support.

### Remote hosts and SSH notifications

On a registered host, provider hooks invoke that host's `sidecar agent report`; its store and resolver remain host-local. `sidecar host serve --stdio` observes the resolved lane and, after parent-plan M5, forwards only the ordinary bounded semantic notification event. The local viewer still owns notification storage, foreground policy, quiet hours, delivery claims, sounds, native providers, and optional direct-terminal delivery.

Lifecycle records, provider session IDs, hook payloads, and integration configuration do not cross SSH for notification delivery. A future remote integration-management command belongs to the mutation phase of [Remote hosts](sidecar-remote-hosts.md) and must call the same application service on the host; M6 does not tunnel arbitrary installer commands or config files.

This gives local and remote panes the same state semantics while preserving host ownership: the hook is close to the agent, the resolver is close to the pane, and the notification is delivered close to the user.

## Security, privacy, and failure rules

- Provider hook input is untrusted local data. Bound stdin, record size, string lengths, nesting, sequence range, clock skew, and processing deadline before persistence.
- Never store prompt text, response text, tool arguments/results, environment values, credentials, full hook payloads, or arbitrary provider paths. Persist only lifecycle state, stable opaque identity, bounded reason codes, source/version, sequence, and timestamps required for arbitration.
- Never shell-compose provider data. Use direct executable argv and structured stdin. Hook assets do not evaluate repository content or source shell files.
- Validate that the current process is inside the claimed Sidecar/tmux context. A report cannot select another pane, server, host, or provider run through input flags.
- A hook exits successfully and silently when outside Sidecar or when reporting is unavailable. Instrument failures through bounded local diagnostics without delaying or changing the agent operation.
- Integration install/update/uninstall is explicit, atomic, reversible, and limited to Sidecar-owned entries. Preserve unrelated user configuration and never auto-adopt an existing similarly named script.
- Full authority expires or releases on every identity discontinuity. Stale state must fail toward current screen/process detection, not toward a guessed idle or blocked state.
- No lifecycle event directly produces an external side effect. Notification policy, privacy, dedupe, live-event grace, delivery claims, and provider adapters remain downstream and unchanged.
- Do not run discovery, config parsing, report-store scans, or subprocesses on the startup paint or synchronous render path.
- All tmux proof uses a distinct socket/server and isolated Sidecar state. Never stop, restart, replace, or clean up the machine's default tmux server.

## Work sequence

### Phase A — Contract and evidence baseline

1. Capture compatibility fixtures for current `agentactivity.Result`, tracker transitions, `agentstatus.Presentation`, workspace inventory, notification lane events, and provider fallback behavior before changing resolution.
2. Record the current official lifecycle event contract and sanitized real payload traces for OpenCode, Codex, Claude Code, and Pi. Build a checked-in capability matrix with source version, tested provider range, covered transitions, ordering behavior, known gaps, and justified authority tier.
3. Define versioned lifecycle report, explanation, integration-status, and JSON CLI schemas. Freeze reason/error codes and state the freshness/release rules.
4. Build a reusable fake-provider/hook harness on an isolated tmux server and private Sidecar state tree. It must replay out-of-order events, duplicate events, session changes, child agents, cancellation, process exit, and hook failure without touching user configuration.

Exit gate: one pure arbitration table proves every tier and fallback reason, and recorded provider evidence selects the first full-lifecycle steel-thread provider without assuming completeness.

**Phase A steel-thread decision (traced 2026-08-30): OpenCode.** All four candidate CLIs were installed and surveyed; OpenCode was driven for real inside an isolated `XDG_CONFIG_HOME` and produced the sequence `session.created → chat.message → session.status {"type":"busy"} → tool.execute.before → permission.asked → permission.replied → session.status {"type":"idle"} → session.idle → dispose`, with a failed turn producing `session.error` followed by `session.idle` rather than latching on `working`. The deciding property is that `session.status` is state-shaped rather than transition-shaped: every emission re-asserts ground truth, so a dropped or reordered event self-corrects instead of freezing a pane. OpenCode is also the only candidate with an explicit unblock event and a positive readiness signal, and it installs as one dropped file with no edit to an existing user config file. Codex has the better paper contract, including the only first-class `Interrupt` event, but is untraced, is feature-flagged off by default, and needs two owned mutations; Herdr shipped a Codex lifecycle hook set through eight versions and then deliberately removed it. Claude Code has the richest event surface and the largest user base but **no user-cancellation event exists at all**, which is a contract gap no tracing can close, and its config merges additively across five layers so the hook set can never be owned. Pi has the cleanest agent-flow abstraction but its blocked lane, in the one shipped integration, comes from a bus message named for another tool rather than a documented event.

**Phase A honest limits.** OpenCode is recorded at `advisory`, not `full`, because cancellation is untraced, and `Capability.TierFor` enforces that rather than trusting the registry. Three findings came from tracing and appear in no vendor documentation: the blocked lane is *not* state-shaped, so the self-correcting property does not cover it; `tool.execute.after` never fired on 1.18.23 although `tool.execute.before` did; and both `~/.config/opencode/plugin/` and `~/.config/opencode/plugins/` are loaded, so an asset present in both double-fires every event. Codex, Claude Code, and Pi are recorded as docs-only and are marked untraced.

**Phase A artifacts.** `internal/agentlifecycle` holds the versioned report, explanation, integration-status, and capability schemas, the frozen reason/error/tier/fallback vocabularies, the freshness policy, and the pure resolver. `TestArbitrationTable` is the exit gate: 39 rows cross every tier with fresh, stale, released, and mismatched-identity conditions, and its coverage assertions fail if any tier, authority, freshness, or fallback reason in the frozen vocabularies is never exercised. `TestLifecycleJSONContracts` and `TestLifecycleVocabulariesAreFrozen` freeze the wire schemas and enums against hand-edited fixtures with no update flag. `internal/agentlifecycle/lifecycleharness` is the reusable fake provider and hook harness on a private tmux socket and a private state tree, covering out-of-order events, duplicates, session rotation, child agents, cancellation, real process exit, hook failure, and invalid input. The capability matrix is `docs/reference/agent-lifecycle-capability-matrix.md` with `capabilities.json` as its machine-readable form (moved from `testdata/` to `internal/agentlifecycle/` in Phase B and embedded, since the resolver reads it at runtime); `TestCapabilityMatrixCannotClaimUnearnedAuthority` re-derives every entry's tier from its own recorded evidence so no entry can claim authority it has not earned.

**Phase A findings in existing behavior, pinned rather than fixed.** Capturing the fixtures surfaced several things Phase B's extraction must preserve or deliberately change, and which were left exactly as they are so the characterization stays honest. The `StaleAfter: time.Minute` window in `workspaceinventory.observeContext` cannot be observed at all, because `agentstatus.Input` is built with `Now` and `CapturedAt` set to the same instant, so freshness on that path is unconditionally `current` and the blocked-attention gate that depends on it never fires there. `agentactivity.Tracker.Apply` treats a same-state, different-evidence observation as a transition, so evidence churn re-stamps `ChangedAt` and silently extends a done row's TTL. `skipSince` is never cleared on the cap-exceeded path, so once `SkipRetentionCap` elapses every later skip result falls straight through until a non-skip observation arrives. A zero `Tracker` reports `DisplayState() == ""` and reaches the paused-unknown row by fall-through rather than by an explicit rule. `Ambiguous` reaches `FreshnessUnavailable` by two independent routes while `Missing` and `Orphaned` need an explicit override and `Err` and `Paused` keep whatever freshness the capture earned, so any consolidation must preserve that asymmetry. `ResetForProcessChange` zeroes `ChangedAt`, which disables done-TTL decay until the next commit. Health and legacy answers never carry `Evidence` or `ChangedAt`; only the semantic branch republishes them.

**Phase A compatibility fixtures:** the current behavior of `agentactivity` (state vocabulary, `Result`, `Snapshot` JSON, the nine supported providers, detector fallback evidence, and the tracker's idle-debounce, skip-retention, seen, and inferred-idle rules), `agentstatus` (lane and freshness vocabularies, the health precedence order, the semantic and legacy branches, freshness thresholds, and done expiry), `notify.LaneTracker` (debounce, the three emitted transitions, body and name composition, dedupe key shape, and the baseline/flap/settle/withdraw lifecycle), and `workspaceinventory` detection (identity demotion, orphan and ambiguity rules, the plain-worktree early exit, and tracker keying) are pinned before Phase B extracts the shared resolver.

### Phase B — Report core and local steel thread

**Entry condition (Phase A review, 2026-08-30):** trace OpenCode cancellation before building the steel thread. At `advisory` tier the resolver vetoes any report the screen contradicts, so this phase's exit gate — the lane walk driven *by native provider events* — is only honestly satisfiable once OpenCode earns `full`, and cancellation is the one untraced transition holding it at `advisory`. If cancellation proves untraceable on the current release, this plan must make an explicit recorded decision — either restate the exit gate tier-honestly (for example, the lane walk with screen detection disabled) or hold Phase B for a provider release that closes the gap — rather than quietly dropping `cancelled` from the full-lifecycle transition set.

**Entry condition satisfied (traced 2026-08-30, OpenCode 1.18.25).** Cancellation is observable. Interrupting a busy turn emits `session.error` carrying `error.name = "MessageAbortedError"`, immediately followed by `session.status {"type":"idle"}` and `session.idle`; the TUI marks the turn `interrupted` and the response stops mid-token. A contrast run with no credentials for the selected model produced the identical event *shape* with `error.name = "ProviderAuthError"`, so cancellation and failure are separable only by the bounded error class name — which is what the provider handler reads, and which is recorded as a known gap for any adapter that does not. The lane is safe either way because `session.status` re-asserts ground truth; only the `end` report's `Outcome` depends on the discriminator. Capture used a private tmux socket and a temporary `XDG_CONFIG_HOME`; the user's real `~/.config/opencode` was never read or written. Two capture facts worth keeping: interrupting takes **two** Escape presses (the first only arms the confirmation, changing the footer to `esc again to interrupt`), and the abort reaches the bus several seconds after the screen settles. Both traces are checked in as `cancelled-turn.tsv` and `provider-error-named.tsv`. OpenCode's covered set now satisfies `FullLifecycleTransitions()` and `TierFor` derives `full`; `TestOpenCodeEarnsFullOnlyFromTheCancellationEvidence` asserts that removing the cancellation evidence returns it to `advisory`. **The exit gate below therefore stands as written and needs no tier-honest restatement.**

1. Add `internal/agentlifecycle`, memory and JSONL stores, locking/compaction, validation, sequence/run handling, and the pure authority resolver.
2. Extend Sidecar-managed shell environment identity and implement `sidecar agent report`, `end`, `release`, and `explain` with human/JSON output and generated CLI documentation.
3. Extract one observation-plus-authority resolver and call it from project worktree polling, project managed-shell polling, and `workspaceinventory` before their existing trackers. Keep provider detectors as the unchanged fallback, keep `agent_triggers.go` as the project notification adapter, and let the M5 remote adapter consume the same resolved semantics at its separate boundary.
4. Implement the first provider handler and bundled asset against the recorded fixtures, then prove the full hook → report → resolved lane → existing notification path with fake delivery providers.
5. Prove that screen disagreement cannot override fresh full authority, simultaneous hook/screen agreement does not duplicate, explicit release/process exit restores fallback, and an old run cannot replay after restart.

Exit gate: one Sidecar-managed agent reaches working → blocked → working → idle from native provider events, produces the existing needs-input/finished records exactly once, and returns immediately to screen fallback when the integration is absent or unhealthy.

**Phase B implemented 2026-08-30 (`td-b5a467`); exit gate passed live.** Steps 1–5 are built and tested, and the exit gate was run three times against real OpenCode 1.18.25 with the real bundled asset installed, in a sandbox where `HOME` itself is redirected so no path reaches the user's real config or state. All three runs produced the same four reports in order — `session_start` idle, `turn_start` working, `end` cancelled, `release` process_exit — under one stable run id and process generation, with `sidecar agent explain` reporting `authority: lifecycle, tier: full, freshness: fresh` while the run was live, and the cancelled turn recorded as `cancelled` rather than a clean completion.

**The first attempt at this gate failed, and the failures are the most valuable evidence in this phase.** An independent reviewer ran it and found two defects invisible to every offline test: the asset spawned each report as an independent subprocess, so sequences were assigned in order and delivered out of order and the store correctly rejected the loser, silently dropping the terminal `end` report in two runs out of three; and the asset had no `ended` latch, so the trailing `session.status idle` that follows every `session.error` superseded the end report and a cancelled turn was announced as a clean completion. Running the gate afterwards surfaced two more: OpenCode skips any plugin module with a non-function export, and a hook that is a direct child of the pane's root process resolved its own pid as the provider generation, giving every report a different run. None of these could be reached from Go; all four are now covered by tests, three of them by tests that execute the shipped JavaScript.

**Phase B artifacts.** `internal/agentlifecycle/lifecyclestore` holds the memory and JSONL stores behind one `Store` interface, with the ordering rules in a single shared admission fold both implementations use — so the contract test is meaningful rather than decorative. The JSONL store follows the `internal/notify` house pattern (flock on a sidecar `.lock`, `O_APPEND` writes, temp-file-and-rename compaction) and adds one rule worth naming: a load refolds through the same admission checks as an append, so a hand-edited log that rewinds a sequence or resurrects a finished run is dropped rather than believed. `ReadAll` is the lock-free, side-effect-free inspection path. `agentlifecycle.Validate` and `SanitizeDetail` bound every record at the seam rather than in the CLI, because a bound that lives in one entry point is a bound the next caller silently does not have.

`internal/agentlifecycle/lifecycleenv` is where untrusted provider input meets Sidecar's view of the world: host, tmux server, pane, and provider process generation are derived there from the environment and live tmux and verified against it, and none of them is reachable through a flag. `TestLifecycleHooksCannotSelectAnotherPane` asserts that no such flag exists. The provider generation comes from a bounded walk up the hook's ancestry to the last process before the pane's shell, so relaunching an agent in the same pane is a new generation while the pane and its shell stay the same.

`internal/agentresolve` is the single observation-plus-authority resolver, called by project worktree polling, project managed-shell polling, and `workspaceinventory`. `internal/agentintegration` holds the OpenCode handler, the versioned bundled asset, and the store-backed `Source`.

**Deviation on record: the incarnation namespace is the tmux server PID**, not `tmuxserver.Incarnation.String()`. That string embeds the socket ctime, which tmux bumps every time the attached-client set changes, so namespacing stored records by it would have orphaned every report the moment a user attached or detached a client — silently returning a healthy pane to screen fallback. The PID is stable for the server's lifetime and new after a restart, and it is what `agentcontrol.sameOccupant` already compares.

The deviation only preserves the plan's recycled-pane rule if the whole rule is implemented, and the first attempt did not. tmux hands out `%N` from a per-server counter, so after a restart the first pane is `%0` again; with blocked and idle freshness measured in hours, a stored record matched on pane id alone would still be fresh and would hand a brand new shell a dead run's lane. The full rule is therefore:

1. A pane lookup matches on **both** pane id and server incarnation, so a record from a previous server is never found at all.
2. The live identity handed to the resolver is built from what is true **now** — never copied from the record. Copying it makes every identity check compare a value with itself, so `server_incarnation_changed`, `host_mismatch`, and `process_generation_mismatch` become unreachable and a stored claim is trusted purely because it exists.
3. Liveness compares the **full** generation string including the `start=` component, which exists precisely to disambiguate PID reuse. A recycled pid must not read as the same process.

`TestARecycledPaneCannotInheritADeadRunsLane`, `TestALiveIdentityIsNeverCopiedFromTheRecord`, and `TestGenerationLivenessHonoursTheStartTime` hold each of the three.

**Phase B evidence.** `TestNoEvidenceIsExactlyDetect` runs all 54 checked-in provider screen fixtures through both the pre-extraction path (`agentactivity.Detect`) and the new one (`agentresolve.Resolve` with no source) and requires field-for-field equality — this is the proof the extraction preserved semantics. The Phase A compatibility fixtures were **not modified** and pass; both pinned defects stay pinned. `TestTheThreeCallSitesGoThroughOneResolver` is a source-level guard, deliberately: a fourth direct `Detect` call would fail no behavioral test today, because with no source installed both paths agree exactly — it would only start being wrong once an integration was installed, on one surface, silently. The OpenCode handler is replayed against all four checked-in traces, including the cancelled/failed pair whose only difference is the error class name. `TestNativeEventsWalkTheLanesAndNotifyOnce` walks working → blocked → working → idle from native events through the real store, source, resolver, `agentactivity.Tracker`, `agentstatus`, and `notify.LaneTracker`, and asserts exactly one needs-input and one finished record across additional idle polls. `TestFreshFullAuthorityBeatsAContradictingScreen`, `TestSimultaneousAgreementProducesOneTransition`, `TestIntegrationRemovedReturnsToScreenImmediately`, and `TestAnOldRunCannotReplayAfterRestart` cover the four arbitration properties.

**Phase B honest limits.** `sidecar agent explain --shell TARGET` refuses rather than answering, because explaining another shell needs that shell's pane and run identity from the managed-shell inventory. This is a real usability gap rather than a cosmetic one: `--current` describes the pane it runs in, and an agent's pane is normally occupied by a TUI, so there is no ordinary way to ask about a running agent from a second pane. The live gate worked around it by supplying the agent pane's `TMUX_PANE` to the helper pane, which `lifecycleenv` still verified against live tmux and the claimed session; that is a harness accommodation, not a shipped path. Integration status is inferred from the reported asset version rather than from inspecting installed files, which is Phase C's job. There is no installer: the asset must be placed by hand until Phase C, which also means no user can accidentally enable this yet.

**Phase B findings addressed from the Phase A review (`td-f4d92c`).** 1) `TierFor` now polices every tier boundary, not only full→advisory: an `advisory` claim with no evidence or no covered transition, and a `session-identity` claim that does not cover session identity, fall out to `screen-fallback`. Advisory is not a harmless tier — it still authors state when the screen has no opinion — so an entry could previously escape scrutiny by claiming less. The matrix doc's enforcement sentence is corrected to match. 2) `Report.Detail` has `MaxDetailBytes` and a sanitizer; newline and tab are treated as separators rather than dropped, because welding the words on either side together changes what the diagnostic says. 3) The unexercised arbitration cells are added. 4) `ReasonIntegrationRemovedMid` was reported before identity was checked, so a leftover report from another pane or run was announced as a removed integration; it now requires a matching identity. 5) The advisory-agreement-upgrades-an-inferred-idle path is proved deliberately, because its real consequence is that an advisory integration changes *which notifications a user receives*, not merely how a lane is labelled. 6) The `skipSince` mechanism note remains a plan-only record, unchanged.

### Phase C — Integration manager and surface parity

1. Add the provider adapter interface, bundled asset registry, status/version/checksum model, safe config merge, backup, atomic write, exact uninstall, and fixture coverage.
2. Implement CLI list/status/install/update/repair/uninstall commands, shared dry-run plans for every mutation, and the **Configuration → Agents → Integrations** route over one application service.
3. Add responsive 60×24, medium, and wide rendering; keyboard/mouse/confirmation/search behavior; lazy discovery; live status refresh; and byte-equivalent mutation tests between Configuration and CLI.
4. Document the managed environment and custom report schema, including advisory default and the future explicit trust boundary.

Exit gate: installation is explicit, idempotent, reversible, preserves unrelated configuration in adversarial fixtures, and every UI fact/action has a deterministic JSON-capable CLI equivalent.

### Phase D — Provider expansion by evidence

1. Add Codex and Claude Code adapters through the same core, promoting each only to the tier supported by its real current-version traces.
2. Add Pi or another independent plugin model to prove the adapter boundary is not shaped around the first provider.
3. Evaluate the remaining catalog providers. Ship lower-tier identity/advisory support where useful and leave unsupported agents on screen fallback with an honest reason.
4. Add update/outdated/unsupported compatibility tests and document how a provider-version change is requalified.

Exit gate: at least two structurally different provider APIs share the lifecycle core and installer service without provider branching in arbitration, notification, Configuration, or workspace presentation.

### Phase E — Remote integration and operational hardening

1. After notification M5 exists, run the hook-backed steel thread on a registered remote host and prove that only the resolved semantic event crosses SSH and only the viewer invokes delivery providers.
2. Exercise concurrent hook writers and Sidecar readers, corrupt/truncated JSONL, lock contention, compaction, server/pane ID reuse, process replacement, clock skew, event bursts, and repeated provider failures.
3. Measure hook invocation latency, report-command process cost, store growth, reader work, inventory polling, and startup trace. Optimize only measured bottlenecks; do not add a daemon speculatively.
4. Run Darwin/Linux release-architecture builds, all focused and repository gates, the isolated real-app/demo journey, and deliberate real-provider traces on available systems.
5. Update user/configuration docs, CLI reference, provider support matrix, privacy/security guidance, troubleshooting, and the parent notification plan's evidence.
6. Independently review source trust, config mutation, event ordering, process/session identity, fallback safety, remote boundary, startup behavior, surface parity, and real proof evidence; fix findings and rerun affected gates.

Exit gate: supported local and remote providers produce deterministic state and existing notifications without screen parsing while unsupported or failed integrations visibly and safely retain current behavior, with independent approval of the integrated candidate.

## Test matrix

### Arbitration and lifecycle

- Every authority tier × report state × fresh/stale/released × matching/mismatched process, run, session, pane, server, host, and provider.
- Duplicate, missing, decreasing, reset, and very large sequence values; late prior-run reports after a new run; same sequence from different sources.
- Hook/screen agreement and disagreement; blocking/unblocking; completion; cancellation; process exit; pane replacement; tmux server restart; integration removal and update.
- Parent/subagent events where supported, including proof that a child does not incorrectly block or complete the parent.
- Full authority never produces duplicate lane transitions; advisory/session-only evidence never suppresses needed fallback.

### Store and process behavior

- Memory/JSONL contract parity, two-process append races, reader during compaction, lock timeout, partial line, invalid record, permissions failure, disk failure, bounded history, and restart fold.
- Recycled pane IDs across server incarnations cannot inherit authority.
- No prompt/tool content or secrets appear in records, errors, debug logs, human output, or JSON diagnostics.
- Report failure does not delay or fail the provider hook operation; missing TUI remains supported.

### Installer and assets

- Missing, valid, malformed, symlinked, permission-restricted, commented, and unknown-field provider configs.
- Existing unrelated hooks before/after Sidecar entries; repeated install/update/repair/uninstall; interrupted atomic replacement; backup recovery; modified managed assets.
- Provider missing/current/newer/older, asset current/outdated/corrupt, and honest authority/status transitions.
- Exact argv/stdin construction with adversarial Unicode, quotes, newlines, shell metacharacters, paths, and oversized payloads; no shell evaluation.

### Surfaces and journeys

- Human and JSON CLI schemas, exit codes, help generation, current-shell and explicit-target diagnostics, no-TUI operation, dry-run/mutation equivalence, and configuration preservation.
- Configuration route discovery/search, 60×24 through wide layouts, keyboard/mouse focus, confirmation/cancel, status refresh, errors, and no work in `Init`, synchronous `Start`, or `View`.
- Project Workspace, global Sessions, and remote inventory show identical resolved state and explanation for the same pane from the shared resolver.
- Isolated background-agent journey produces one sticky wait, one completion, and configured external delivery; visible-origin, quiet-hours, and disabled-channel behavior remain unchanged.
- Removing the integration during a run, hook crash, provider upgrade, stale store, and Sidecar restart all converge to an explicit fallback state without replay.
- Registered remote hook report stays remote, one bounded notification event crosses M5, and two local viewers follow existing destination-host dedupe semantics.

## Acceptance evidence

- [ ] One full-lifecycle provider drives working, needs-input, resumed-working, and finished state without terminal text/title heuristics.
- [ ] A second structurally different provider proves the integration seam, and every other advertised provider tier is supported by current-version fixtures and real traces.
- [ ] Fresh full authority wins over contradictory screen evidence; stale, released, invalid, partial, and failed authority immediately expose an actionable fallback reason.
- [ ] Hook and screen observations feed one tracker and produce one notification transition, store record, and external-delivery claim.
- [ ] Report, end, release, explain, integration management, and all Configuration actions have stable human and JSON CLI behavior over the same application core.
- [ ] Install/update/repair/uninstall are explicit, reversible, versioned, idempotent, safe around unrelated user config, previewable through byte-equivalent human/JSON dry runs, and never install repository-local code.
- [ ] The report store is inspectable and repairable JSONL, tolerates concurrent processes and corruption, and retains no prompt, response, tool, or secret content.
- [ ] Hooks no-op outside a Sidecar-managed shell and cannot report for another pane, server, host, provider run, or stale session.
- [ ] Project Workspace, global Sessions, and remote inventory use the same authority resolver and remain semantically identical for the same pane, while current screen-only providers retain their existing behavior.
- [ ] Startup and render paths add no config scan or subprocess; measured reporting and polling overhead stays bounded under a burst.
- [ ] A registered remote agent resolves hook-backed state on its host and M5 transports only the normal semantic notification event to local delivery.
- [ ] Isolated tmux/state proof, real-provider traces, Darwin/Linux gates, documentation, and independent review are recorded before completion.

## References

- [Notification sounds and native delivery](notification-sounds-and-native-delivery.md) controls downstream notification semantics, dedupe, policy, delivery providers, and M5 SSH transport.
- [Agent lifecycle capability matrix](../../reference/agent-lifecycle-capability-matrix.md) records the Phase A provider evidence, the per-provider gaps, and the tier each one has earned.
- [Herdr integrations](https://herdr.dev/docs/integrations/) is the product reference for explicit integration management, lifecycle-authority versus session-identity capability, ordered reporting, release, and screen fallback. Herdr commit `4a3b04f59ba3b7d8a15cea187b23e1e80c343b0c` was inspected for the report/release command contract, per-source sequence handling, hook authority resolver, capability allowlist, and provider assets.
- [Claude Code hooks](https://code.claude.com/docs/en/hooks) documents current session, prompt, tool, permission, notification, subagent, stop, and failure hook events plus user-level configuration behavior.
- [Codex configuration reference](https://developers.openai.com/codex/config-reference) documents the current stable hooks feature, inline or `hooks.json` configuration, lifecycle events, command/MCP handlers, and synchronous/asynchronous behavior.
- [OpenCode plugins](https://opencode.ai/docs/plugins/) documents the plugin event surface used for the first full-lifecycle candidate.
- [Herdr agent control and session restore](herdr-agent-control-and-session-restore.md) controls agent operations, native session references, resume, and cold restoration; session identity can share M6 reports without claiming state authority.
- [Remote hosts](sidecar-remote-hosts.md) controls host registration, the SSH stdio protocol, remote pane identity, and future mutation transport.
