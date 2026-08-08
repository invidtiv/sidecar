# Plan: Workspace agent activity status (td-48ecf2)

**Task:** `td-48ecf2` — Plan workspace agent activity status

**Research snapshot:** 2026-08-08

**Herdr source reviewed:** [`herdrdev/herdr@10974c8`](https://github.com/herdrdev/herdr/tree/10974c822d607f03e20e9741ec027910f0c1f93a)

## Decision first

Show a semantic activity indicator on every Sidecar-managed agent entry in the
Workspaces left pane:

| State | Meaning | Suggested tab treatment |
| --- | --- | --- |
| `working` | The agent is processing the current turn or has live background work. | animated/active green `●` |
| `blocked` | Visible agent-owned UI requires a human decision or answer. | high-attention amber `◆` |
| `idle` | The agent is at its input prompt and has already been viewed in this state. | quiet gray `○` |
| `done` | The agent reached idle since the user last viewed that workspace. | review-ready blue/green `✓` |
| `unknown` | The session exists, but Sidecar cannot classify it safely. | muted `?` (never pretend it is working) |
| `exited` | The tmux pane/process ended. | existing orphan/error treatment |

`done` is a **Sidecar presentation/attention state**, not a fifth runtime state.
The detector returns `idle`; the workspace plugin renders `done` while that idle
transition is unseen. Selecting/attaching to the entry acknowledges it and makes
it `idle`. This is the same useful distinction Herdr makes between a pane's
`AgentState::Idle` and its public `AgentStatus::{Idle, Done}`.

Do **not** add activity methods to `internal/adapter.Adapter`. Those adapters own
conversation discovery/history and may observe sessions Sidecar did not launch.
Activity belongs to the live tmux pane owned by the workspace plugin. Introduce
a small workspace runtime seam (tentatively `AgentActivityProbe`) with one
implementation per supported agent plus a shared manifest evaluator. The four
existing conversation adapters remain consumers of persistent transcripts, not
authorities for live status.

## User journey and acceptance evidence

Marcus can start Codex, Claude Code, Grok, and Antigravity in separate Sidecar
workspaces/shell tabs, continue working elsewhere, and read the left pane without
opening each terminal:

1. A submitted turn changes the entry to `working` quickly.
2. A permission/question UI changes it to `blocked` with higher visual priority.
3. A completed turn changes an unviewed entry to `done`.
4. Opening that entry acknowledges it and leaves a quiet `idle` indicator.
5. An agent exit remains distinct from completion.
6. An unfamiliar UI becomes `unknown` or conservative `idle`, never a fabricated
   blocker and never permission for Sidecar to send input.

Proof must use the real installed agents inside Sidecar-managed tmux panes and
`scripts/tmux-drive.sh`; unit fixtures alone are insufficient. Capture one fixture
and one headless screenshot/text snapshot for every state each agent can produce.

## What Herdr actually does

Herdr's current model is documented in its [Agents guide](https://github.com/herdrdev/herdr/blob/10974c822d607f03e20e9741ec027910f0c1f93a/docs/next/website/src/content/docs/agents.mdx):

1. Identify the foreground agent from the pane's foreground process group,
   including common runtime wrappers.
2. Read the live **bottom of the terminal buffer**, not the user's scrolled
   viewport.
3. Evaluate ordered, per-agent TOML manifests against live screen regions and,
   when available, OSC title/progress values.
4. Classify the runtime as `idle`, `working`, `blocked`, or `unknown`.
5. Debounce transitions, retain state while transcript/history viewers are open,
   and distinguish visible evidence from a default known-agent idle fallback.
6. Roll pane status upward and turn unseen idle into `done`.

The relevant implementation is the [detector core](https://github.com/herdrdev/herdr/blob/10974c822d607f03e20e9741ec027910f0c1f93a/src/detect/mod.rs), [manifest evaluator](https://github.com/herdrdev/herdr/blob/10974c822d607f03e20e9741ec027910f0c1f93a/src/detect/manifest.rs), polling/state arbitration in [pane.rs](https://github.com/herdrdev/herdr/blob/10974c822d607f03e20e9741ec027910f0c1f93a/src/pane.rs), and unseen-state priority in [workspace/aggregate.rs](https://github.com/herdrdev/herdr/blob/10974c822d607f03e20e9741ec027910f0c1f93a/src/workspace/aggregate.rs).

Herdr deliberately treats lifecycle hooks for these four agents as session
identity aids, not status authorities. Hooks can miss interrupts, permission
resolution, subagent completion, or background activity. In particular,
Antigravity has no complete blocker lifecycle event and its `Stop` is end-of-turn,
not process exit. Its official hook model exposes `PreInvocation`,
`PostInvocation`, `Stop`, and tool hooks, while transcript/session metadata is
available in each payload ([Google Antigravity hooks](https://www.antigravity.google/docs/hooks)).
Grok also exposes hooks, but they are tool/session lifecycle integrations and do
not replace its richer TUI signals ([Grok skills/plugins/hooks](https://docs.x.ai/build/features/skills-plugins-marketplaces)).

### Herdr manifests worth porting as fixtures, not copying blindly

Pin the reviewed rules as provenance in tests, then re-harvest against installed
versions before implementation. Herdr can update manifests independently; a
vendored one-time copy will drift.

| Agent | Strong working evidence | Strong blocked evidence | Strong idle evidence |
| --- | --- | --- | --- |
| Claude Code | braille activity title; live working overlay | live confirm/select form, permission prompt | live `❯` prompt box or idle title |
| Codex | braille spinner in title; `Working (... esc to interrupt)` | `Action Required`, confirm/submit/allow UI | non-spinner title and live composer |
| Grok | busy title/progress; spinner line ending `[stop]`; background-task chip | `Action Required`; option dialog and selection hints | title ending `grok`; shortcuts footer without cancel |
| Antigravity (`agy`) | braille spinner plus `...ing`; nonzero background-task count | `requesting permission for:` plus proceed/amend UI | no strong explicit rule in the reviewed manifest; known-agent fallback |

Reviewed manifests: [Claude](https://github.com/herdrdev/herdr/blob/10974c822d607f03e20e9741ec027910f0c1f93a/src/detect/manifests/claude.toml), [Codex](https://github.com/herdrdev/herdr/blob/10974c822d607f03e20e9741ec027910f0c1f93a/src/detect/manifests/codex.toml), [Grok](https://github.com/herdrdev/herdr/blob/10974c822d607f03e20e9741ec027910f0c1f93a/src/detect/manifests/grok.toml), and [Antigravity](https://github.com/herdrdev/herdr/blob/10974c822d607f03e20e9741ec027910f0c1f93a/src/detect/manifests/antigravity.toml).

## Current Sidecar findings

Sidecar already has most of the transport needed:

- `internal/plugins/workspace/agent.go` adaptively polls every managed tmux
  session and batch-captures the recent bottom buffer.
- `Agent`, `WorktreeStatus`, and `AgentStatus` already carry status into both
  worktree and shell entries.
- `internal/plugins/workspace/view_list.go` already assigns status icons/colors
  in the left pane.
- `internal/plugins/workspace/agent_session.go` has provider-specific session-file
  readers for Claude, Codex, Antigravity, OpenCode, Cursor, Pi, and Amp.

The existing classifier is not reliable enough for the requested behavior:

- `detectStatus` applies generic words such as `approve`, `finished`, `failed`,
  and `error:` to recent scrollback without knowing which agent owns the UI.
- Session-file freshness treats any write within 30 seconds as active; stale
  content then maps last user/assistant role to active/waiting. It cannot
  reliably distinguish an idle prompt from a live permission question.
- Grok has no workspace session-file probe today.
- `StatusWaiting` currently conflates idle/completed with blocked.
- Status has no evidence/source/confidence, transition timestamp, or unseen
  acknowledgement state, so bad classifications are difficult to diagnose.
- Worktree and shell polling/rendering use related but different status enums.

### tmux constraint

`capture-pane -e` returns rendered cells and SGR styling, not OSC sequences that
the terminal already consumed. Sidecar can extend its existing atomic
`tmux display-message` metadata to capture `#{pane_title}` (OSC 0/2) alongside
geometry/cursor data. There is no equivalent current path for Grok's OSC 9;4
progress value, so v1 must not depend on that signal. Screen rules and pane title
are sufficient for the steel thread; progress support is a later measured need.

## Proposed design

### 1. One shared semantic model

Add a package such as `internal/agentactivity` that is independent of Bubble Tea,
tmux subprocesses, and conversation storage:

```go
type State string
const (
    StateUnknown State = "unknown"
    StateIdle    State = "idle"
    StateWorking State = "working"
    StateBlocked State = "blocked"
)

type Observation struct {
    Agent      workspace.AgentType // or a product-neutral local ID type
    Screen     string              // current live bottom-buffer snapshot
    PaneTitle  string
    Process    ProcessSnapshot
    CapturedAt time.Time
}

type Result struct {
    State           State
    Evidence        string // stable rule ID, safe for logs/tests
    VisibleIdle     bool
    VisibleWorking  bool
    VisibleBlocker  bool
    SkipStateUpdate bool
}

type Probe interface {
    Detect(Observation) Result
}
```

Keep the evaluator state-free. It should support only rule operations the four
real manifests require: literal contains, regex/line-regex, `all`/`any`/`not`,
priority, and bounded regions (`whole_recent`, last N non-empty lines, prompt
body/last marker/horizontal-rule approximations, `pane_title`). A Go data table
is acceptable for v1; use embedded TOML only if re-harvested rule churn proves
that data updates are materially easier than code review.

Provider files (`claude.go`, `codex.go`, `grok.go`, `antigravity.go`) own only
provider evidence. The core owns ordering, normalization, transition semantics,
and diagnostics. This is the adapter seam requested, but it is deliberately
separate from conversation `adapter.Adapter`.

### 2. Foreground-process identity is a gate

Before matching screen text, confirm that the tmux pane's foreground command is
the expected agent. Extend atomic metadata with `#{pane_current_command}` and,
where necessary, query the pane PID/process group asynchronously. Start with
the direct names Sidecar launches (`claude`, `codex`, `grok`, `agy`) and Node/Bun
wrappers observed on this Mac. Do not apply agent manifests to a returned shell
prompt or to a different foreground program.

Use Sidecar's recorded `Agent.Type` as the expected identity, not as proof that
the process is still present. If identity cannot be established, retain the last
state during a short grace window and then return `unknown`/`exited` according to
tmux/process liveness.

### 3. Evidence precedence and transition policy

Use this order:

1. pane/process exit → `exited` outside the activity detector;
2. strong visible blocker → `blocked`;
3. strong visible working evidence or continuing live screen/title changes →
   `working`;
4. strong visible prompt/idle evidence → pending `idle`;
5. transcript/history viewer → retain prior semantic state;
6. known live agent with no matching evidence → conservative pending `idle`;
7. unknown process/agent → `unknown`.

Require two consistent idle observations separated by roughly 300–500 ms before
publishing `idle`; publish strong blockers immediately. Add a short startup grace
period so the initial splash does not become done. Do not infer working solely
from session-file mtime. Session files may remain a secondary fallback only when
screen/title capture is unavailable, and the result must expose that evidence ID.

Track on `Agent`:

```go
ActivityState     agentactivity.State
ActivityEvidence  string
ActivityChangedAt time.Time
ActivitySeen      bool
```

`done = state == idle && !ActivitySeen`. Mark seen when the entry is selected
and its live output is visible (or interactive attach begins), not merely when
the Workspaces plugin is focused. A new working/blocked transition clears seen;
blocked remains blocked even after viewing because it still requires action.
Do not persist seen state in v1; restart may show current idle as idle rather
than manufacturing old notifications.

### 4. Reuse the existing poll and capture path

Do not add another watcher, goroutine per adapter, or session-directory scan.
Extend `AgentOutputMsg`/`AgentPollUnchangedMsg` with the activity result and title
metadata produced inside the existing async capture command. Evaluate status on
every poll because pane title/process metadata can change without captured text.

Unify worktree/shell presentation over the semantic state instead of translating
through both `WorktreeStatus` and `AgentStatus`. Keep git/orphan/error status
orthogonal. This prevents an agent completion indicator from overwriting a
missing-worktree or crashed-session warning.

### 5. Left-pane rendering

Both worktree entries and agent-backed Shell entries get the same leading status
cell. The selected row must still show the state through glyph/label, because its
selection background currently removes provider-specific color. Suggested
compact mapping:

```text
● working    ◆ blocked    ✓ done    ○ idle    ? unknown    ◌ exited
```

Use theme colors and a textual status in narrow/accessible fallback or the second
line when space allows; color alone is not the contract. Keep current two-line
entry height and hit geometry. Do not add a plugin footer.

## Agent-specific implementation notes

### Codex

- Prefer `pane_title` `Action Required` for blocked and braille spinner for
  working.
- Match the live `Working (... esc to interrupt)` row as a screen fallback.
- Match current composer/prompt chrome for idle; harvest the actual installed
  build because Herdr's reviewed idle rule relies heavily on non-spinner title.
- Preserve state in transcript viewer (`q to quit`, scroll/home/end hints).
- Existing Codex JSONL parsing becomes diagnostic/fallback only, not primary.

### Claude Code

- Block on live confirm/select/permission forms, not old transcript words.
- Idle requires the live prompt box/body; working may use title spinner or live
  overlay evidence.
- Preserve state while detailed transcript/model picker overlays are visible.
- Do not treat `Stop`/`SubagentStop` hooks alone as authoritative; subagent recap
  ordering is a known source of false transitions.

### Grok

- Add a workspace probe (the conversation adapter plan is separate).
- Title is the strongest available signal through tmux: idle title ends in
  `grok`; busy title includes activity/spinner; blocked includes
  `Action Required`.
- Screen fallbacks cover `[stop]`, background task count, option dialogs, and
  footer hints.
- Defer OSC 9;4 progress until Sidecar has a proven transport for it.

### Antigravity (`agy`)

- Working: current spinner/action `...ing` line or nonzero task count.
- Blocked: the visible permission request/proceed/amend form.
- Idle: conservative known-agent fallback after debounce unless real installed
  output reveals stable prompt chrome suitable for an explicit rule.
- Hooks can provide conversation ID/transcript path but must not author blocked
  or completion state.

## Delivery plan

### Phase 0 — Runtime evidence pack

1. Record exact installed versions and `tmux -V`.
2. For each agent, start a Sidecar-managed shell and capture sanitized output,
   `pane_title`, `pane_current_command`, pane PID/foreground process data for:
   startup, idle, working, tool execution, permission/question blocker,
   interrupted turn, completed turn, transcript viewer, and exit.
3. Store minimal golden fixtures under
   `internal/agentactivity/testdata/<agent>/`; document unavailable states rather
   than synthesizing them.
4. Compare each fixture to the pinned Herdr rule and note drift.

Exit gate: every proposed v1 rule has a real fixture from the currently installed
binary or is explicitly marked as a compatibility fixture from pinned Herdr.

### Phase 1 — Codex steel thread

1. Add the product-neutral state/result types and rule evaluator.
2. Extend tmux metadata capture with pane title/current command.
3. Implement the Codex probe, idle debounce, and unseen-idle `done` derivation.
4. Render the state on both a worktree and an agent-backed shell entry.
5. Prove submit → working → blocked/idle → unseen done → seen idle in the real app.

Exit gate: no extra subprocess per ordinary poll, existing capture batching still
works, and Codex's real journey is visible in the left pane.

### Phase 2 — Claude, Grok, and Antigravity probes

Add one provider at a time, each with real fixtures, focused tests, and a real
Sidecar pass. Avoid a single giant manifest port. Grok includes title metadata;
Antigravity explicitly tests conservative fallback; Claude tests overlays and
permission resolution.

Exit gate per provider: idle/working plus every available blocker state passes;
interrupt and transcript-viewer cases do not create false completion.

### Phase 3 — Replace legacy authority and unify shell/worktree state

1. Route all four agents through `agentactivity`.
2. Stop allowing generic `detectStatus`/session-file logic to override their
   semantic result; retain legacy fallback for unsupported agents temporarily.
3. Unify duplicated `WorktreeStatus`/`AgentStatus` presentation logic or isolate
   git/liveness status from activity explicitly.
4. Add debug logging (`agent`, prior/new state, evidence ID, capture age) behind
   normal debug controls; never log terminal content.

Exit gate: the four agents have exactly one live status authority and unsupported
agents retain current behavior.

### Phase 4 — Hardening and rollout

1. Add a feature flag (`workspace_agent_activity`) for one release if this lands
   amid other workspace terminal changes.
2. Measure capture/poll CPU and tmux subprocess counts with 1, 4, and 10 agents,
   focused and backgrounded.
3. Run `go test ./internal/agentactivity ./internal/plugins/workspace`, then
   `go test ./...` and `go build ./...`.
4. Use `scripts/tmux-drive.sh` for fixed-width text/PNG proof; specifically verify
   header/footer height and narrow sidebar truncation.
5. Independently review transition semantics, false-positive fixtures, polling
   overhead, and left-pane rendering before approval.

## Test matrix

### Core contract tests

- rules are priority ordered and deterministic;
- blocker beats working/idle when evidence overlaps;
- regions only inspect the live bottom area;
- ANSI/SGR stripping preserves Unicode screen glyph matching;
- transcript viewer returns `SkipStateUpdate`;
- no match for a known agent yields conservative idle only after debounce;
- process mismatch yields unknown and cannot reuse another agent's manifest;
- idle transition is done until selected, then idle;
- blocked remains blocked after selection;
- restart has no stale persisted done badge.

### Adapter fixtures

For each of Codex, Claude, Grok, and Antigravity:

- startup and plain idle;
- active model response;
- active tool/subagent/background task where supported;
- permission approval and ask-user UI;
- cancellation/interruption;
- transcript/history or settings overlay;
- custom/narrow terminal width;
- old blocker text in scrollback with a live idle prompt;
- provider version drift fixture that safely becomes unknown/idle.

### Integration/performance

- unchanged screen but changed pane title updates state;
- batch capture returns metadata aligned to the correct session;
- stale poll generations cannot overwrite a newer state;
- killed tmux session remains exited/orphaned, not done;
- background poll intervals still refresh a blocker within the documented bound;
- no goroutine/timer leak after repeated start/stop;
- no new synchronous work in `Init()`/`Start()`.

## Explicitly deferred

- Installing or editing provider hooks from Sidecar. That mutates user-global
  harness configuration and is unnecessary for the first useful path.
- Adding status to the Conversations plugin or its `adapter.Session` model.
- Sending notifications or sounds; first prove the visual status is accurate.
- Remote manifest auto-update. Start with reviewed code/fixtures; add a signed,
  inspectable update channel only if real provider churn justifies it.
- Automatically responding to blocked prompts. Status is observation only.
- Grok OSC progress until tmux exposes it reliably or Sidecar owns a PTY parser
  that captures it before consumption.

## Risks and mitigations

| Risk | Mitigation |
| --- | --- |
| Provider UI text changes | real golden fixtures, stable evidence IDs, per-agent files, conservative fallback |
| Old transcript text looks blocked | bounded live regions and foreground process gate |
| Session file activity lies about live state | screen/title authority; files are fallback diagnostics only |
| Completion badge becomes noisy | unseen-idle semantics and explicit acknowledgement |
| Polling gets more expensive | reuse capture batch; metadata in the same tmux invocation; benchmark subprocess count |
| Current workspace terminal edits conflict | land as reviewable phases; isolate the new package first and integrate after active terminal work settles |

## Completion checklist

- [ ] Real evidence pack for all four installed agents
- [ ] Shared state model and state-free evaluator
- [ ] Foreground identity and pane-title metadata
- [ ] Codex steel thread proven in the real Sidecar app
- [ ] Claude, Grok, and Antigravity probes proven independently
- [ ] Worktree and shell left-pane parity
- [ ] Idle-versus-unseen-done acknowledgement behavior
- [ ] Focused/full tests, build, performance probe, and tmux-drive visual proof
- [ ] Independent review completed and findings resolved

## Phase 0/1 implementation evidence (2026-08-08)

Implementation is tracked by epic `td-8625a6` and children `td-31ab2b`,
`td-495065`, and `td-52abdf`. Phase 2 provider probes remain unimplemented.

- Runtime fixtures and exact installed versions are under
  `internal/agentactivity/testdata/`. Codex 0.147.0 covers startup/idle,
  working, tool execution, blocker, interruption, completion, transcript
  viewer, and exit. Claude, Grok, and Antigravity availability/unavailable
  Phase 2 states are explicit rather than synthetic.
- `go test ./internal/agentactivity ./internal/plugins/workspace`,
  `go test ./...`, and `go build ./...` pass at `4f4b9a4`.
- The no-extra-process contract is protected by
  `TestBatchCaptureIncludesActivityMetadataInSameTmuxInvocation`: each pane's
  title/current-command display and screen capture remain one tmux argv chain.
- A real `v0.1.0-phase1-proof2` binary was driven at 200x50. Text and PNG
  artifacts are in `/tmp/sidecar-drive-phase1/`: `phase1-working-final`,
  `phase1-done-final`, `phase1-seen-idle-final`, and
  `phase1-blocked-final`. They prove worktree working plus agent-backed shell
  working -> unseen done -> selected idle and an immediate permission blocker.
  The header/footer remain visible in every inspected PNG.
- Real proof found and fixed a seen-state bug before handoff: a viewed working
  state no longer acknowledges a future idle transition. The approval command
  used for blocker proof was canceled and `/tmp/sidecar-phase1-proof-never-create`
  was verified absent. Proof-only shells and tmux sessions were removed.

Independent-review follow-up at `651dc26` adds durable process-group evidence
and closes two integration gaps. `AgentPollUnchangedMsg` now applies semantic
activity, and acknowledgement requires focused, actually visible Workspaces
output rather than selection alone. The reviewer overlay reproductions and new
worktree/shell regressions pass. A real `v0.1.0-phase1-reviewfix` binary was
driven again at 200x50; `/tmp/sidecar-drive-phase1-reviewfix/` contains
`title-only-working.{txt,png}` (unchanged screen, spinner title alone publishes
working), `background-working.{txt,png}`, and
`background-return-idle.{txt,png}`. The background completion remains unseen
while Git is focused; returning to its already-selected live output immediately
acknowledges it to idle, as designed. Proof-only shells/sessions were removed.
