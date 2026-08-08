# What Sidecar can learn from Herdr without replacing tmux

Status: research, 2026-08-08
Companion to: [Replacing Sidecar's tmux integration with Herdr](./replacing-tmux-with-herdr.md) — **on hold**
Related plan: [Workspace agent activity status (td-48ecf2)](../plans/active/td-48ecf2-workspace-agent-activity-status.md)

## Why this document

The replacement proposal is credible but expensive: five phases, a new external
runtime dependency at 0.8.0, and a known history/scrollback regression. Held for
now.

Most of what makes Herdr's terminal feel better than Sidecar's, though, is not
Herdr the product. It is a handful of **architectural choices** Sidecar can adopt
against its existing tmux backend. This document separates the two: what is a
portable idea, what is genuinely locked behind replacing tmux, and what is
ordinary tech debt found along the way.

Nothing here requires installing `herdr`.

## The headline finding

**Sidecar already receives the raw pane byte stream from tmux and discards it.**

Sidecar attaches to tmux in control mode. `control_protocol.go:95-110` parses
`%output` / `%extended-output` notifications and calls `decodeControlBytes` to
un-escape the payload into `controlEvent.Data`. That is the exact byte stream a
terminal emulator consumes.

Then `control_manager.go:511-514`:

```go
func (c *sessionControlClient) handleEvent(event controlEvent) {
	switch event.Kind {
	case controlEventOutput:
		c.markDirty(event.Pane)
```

The payload is never read. `controlEvent.Data` has no reader anywhere in
non-test code — it is decoded and dropped. `%output` is used purely as a dirty
flag that triggers a fresh `capture-pane -p -e -S -600` plus a `display-message`
metadata query (`control_manager.go:827-838`), and the result is stored as
`[]string` of rendered lines (`output_buffer.go:41`).

So Sidecar pays the full cost of control mode, receives everything it needs to
maintain a real screen model, and instead re-renders a 600-line text snapshot on
every output burst. This one decision is the root of most of the differences
below.

### What it costs today

| | Sidecar now | Achievable on tmux |
| --- | --- | --- |
| Bytes per output burst | full 600-line re-capture | the delta tmux already sent |
| Subprocess work per burst | `capture-pane` + `display-message` inside the control channel | none |
| Idle cost | none (dirty-flag driven) — this part is already right | none |
| Cursor | queried separately via `#{cursor_x},#{cursor_y},#{cursor_flag}` | naturally in the stream |
| Mouse mode | side-channel `#{mouse_any_flag}` query, because "`capture-pane -e` emits rendering escapes only — DECSET mode sequences never survive it" (`control_manager.go:32-36`) | naturally in the stream |
| Escape handling | five regexes lexically repairing mouse sequences, including ones that "lost their ESC prefix" (`output_buffer.go:12-34`) | a parser that never produces the problem |

That last row is the tell. `partialMouseEscapeRegex` exists to clean up SGR
mouse sequences whose `ESC` byte was eaten upstream, and
`mouseSequenceDetector` is described as "lenient… catches any mouse-like
content, including truncated/split sequences." These are string repairs standing
in for a state machine. A byte-fed VT parser has no notion of a "partial
sequence that lost its prefix" — it just hasn't finished the sequence yet.

## Portable lessons, ranked

### 1. Build the screen from bytes, not from re-captured text

**Herdr:** owns a real terminal model (vendored `libghostty-vt`), applies PTY
bytes to a cell grid, and emits absolutely-positioned blits.

**Sidecar could:** feed `controlEvent.Data` into a cell grid and render from
that, keeping `capture-pane` only for cold start, resync, and history.

This is the highest-leverage change in the document and it is entirely within
Sidecar's existing architecture. Concretely:

- a `Screen` type holding cells + cursor + modes, fed by `Write([]byte)`;
- `charmbracelet/x/ansi` for parsing (already a direct dependency);
- `x/cellbuf` / `ultraviolet` buffers for storage and rendering (already direct
  dependencies);
- `capture-pane` becomes the resync path — on subscribe, on resize, on
  suspected divergence — not the steady-state path.

Verification during the Herdr probe is relevant here: the frame dialect a real
VT model needs to *emit* turned out to be tiny (`CUP`, `SGR`, `DECSET ?25/?2026`,
`OSC 8`, `CSI 2J`). The *input* side is a full VT parse, which is the harder
half, but `x/ansi` already does the sequence-level work — what Sidecar would own
is the state machine that applies parsed sequences to a grid.

What this fixes, in order: the mouse-regex repairs disappear; the synthetic
cursor disappears; `#{mouse_any_flag}` and `#{cursor_*}` side-channel queries
disappear; alternate-screen and full-screen TUI fidelity improves; per-burst
cost drops from O(scrollback) to O(delta).

**Caveat, stated up front:** this is the one item here that is genuinely large.
It is a real terminal-model implementation, and Sidecar would own it. It is
worth scoping as its own spike with a fidelity harness (drive known byte
sequences, compare grid against `capture-pane`) before committing. If that
spike says "we are building a terminal emulator," that is the signal to revisit
the Herdr plan rather than push through.

### 2. Read the live bottom of the buffer, never the user's scrolled viewport

**Herdr:** agent detection always evaluates against the live bottom of the
terminal buffer, independent of where the user has scrolled.

**Sidecar:** the agent-activity plan already adopts this (§"What Herdr actually
does", item 2, and the bounded-region rules). Called out here because it is a
correctness rule that outlives that plan and should hold for *any* future
screen-scraping feature — search, notifications, anything.

### 3. Separate "runtime state" from "attention state"

**Herdr:** `AgentState::Idle` is what the runtime is doing;
`AgentStatus::Done` is a presentation state meaning "went idle and you haven't
looked yet."

**Sidecar:** the plan already ports this exactly (`done = idle && !seen`). Good.
The generalizable form is worth naming: **never let a presentation concern
mutate a runtime enum.** Sidecar's current `StatusWaiting` conflating idle,
completed, and blocked (plan, "Current Sidecar findings") is the same class of
bug in the other direction.

### 4. Wait on state changes instead of polling for them

**Herdr:** `agent.wait { target, until: [status…], timeout_ms }` and
`pane.wait_for_output` let a caller block on a transition. Status changes are
also pushed as `pane_agent_status_changed` events.

**Sidecar:** adaptively polls every managed session
(`internal/plugins/workspace/agent.go`). Polling is defensible for screen
scraping, but the *shape* is portable: once `agentactivity` exists, expose a
"notify me when this agent's state changes" seam rather than making every
consumer re-derive state from poll results. This matters as soon as anything
beyond the left pane wants status — notifications, the Kanban view, `td`
integration.

Cheap, and it falls naturally out of the td-48ecf2 work if the seam is designed
for it now rather than retrofitted.

### 5. Version and identity discipline for anything external

Three habits worth stealing, all cheap:

- **Negotiate on a protocol integer, not a version string.** Herdr's released
  0.8.0 speaks protocol 19 while its `main` declares 20 — the version string
  lied. Anywhere Sidecar talks to an external tool with a structured contract
  (`td` is the live example), pin the contract, not the release.
- **Sequence-guard external reports.** Herdr's metadata writes carry an optional
  `seq` per `source`, so a slow writer cannot clobber a newer value
  (`metadata_tokens.rs:24-40`). Sidecar has the same hazard in its poll pipeline
  — the td-48ecf2 test matrix already lists "stale poll generations cannot
  overwrite a newer state," and `ControlSnapshot.Generation` exists for this.
  Make it a rule, not a per-site fix.
- **Never key durable state on a positional ID.** Herdr's `wN:pM` pane IDs are
  index-shaped handles; `term_<hex>` is the real identity. Sidecar's equivalent
  smell is tmux session *names* doing double duty as identity and presentation.

### 6. Bound scrollback by bytes, not lines

**Herdr:** `scrollback_limit_bytes`, default 10 MB per pane.

**Sidecar:** 600 lines, hardcoded in four places (`control_manager.go:137, 674,
832, 860`) plus `captureLineCount = 600` in the workspace plugin. A line-count
bound is unbounded in memory — one pane emitting long lines costs arbitrarily
more than another. There is already a "hard cap on captured output size to avoid
runaway memory for TUI-heavy panes" in the workspace plugin, which is the byte
bound, added separately and locally. Unify on bytes.

Also: the 600 should be one constant, not five.

## Tech debt found along the way

These are independent of Herdr; they surfaced while reading the code.

| Item | Where | Note |
| --- | --- | --- |
| `controlEvent.Data` is decoded and never read | `control_protocol.go:100`, `control_manager.go:513` | Either use it (lesson 1) or stop paying `decodeControlBytes` on every output notification. Right now it is pure waste. |
| Scrollback constant duplicated 5× | `control_manager.go` ×4, `agent.go` | Single constant; bound by bytes. |
| Two status enums for one concept | `WorktreeStatus`, `AgentStatus` | Already flagged in td-48ecf2 Phase 3; noting it here so it is not lost if that plan is rescoped. |
| Generic word matching in `detectStatus` | `internal/plugins/workspace/agent.go` | `approve`, `finished`, `failed`, `error:` applied to scrollback without knowing which agent owns the UI. Superseded by td-48ecf2, but it is actively wrong today. |
| Mouse-sequence repair regexes | `output_buffer.go:12-34` | Symptom of lesson 1. If lesson 1 is not taken, these at least deserve a comment saying *why* they exist. |
| `capture-pane -e` cannot see DECSET | `control_manager.go:32-36` | Already well-commented. Listed because it is the concrete reason a byte-fed model is better, not because the current workaround is bad. |

## What we cannot fix without replacing tmux

Honesty requires naming the part that is not portable.

`geometry_lease.go` is 953 lines, plus `pane_fit.go` at 177 — roughly 1,100
lines whose entire purpose is compensating for the fact that **tmux has one pane
size per window, shared across all attached clients.** Two Sidecar instances
attached to one tmux server fight over geometry, so Sidecar arbitrates ownership
through a tmux user option, and non-owners render a clipped projection of
someone else's geometry.

Herdr has no equivalent problem. Its server keeps a per-client render size and a
per-terminal resize lock; the whole concern is a `HashSet` and one branch
(`headless.rs:2671`, `ui/panes.rs:189`). Not because Herdr is cleverer, but
because a server that owns the PTYs can render per client and tmux cannot.

That 1,100 lines is the genuine, irreducible cost of tmux as the backend. It is
also the strongest argument the replacement proposal has, and it is a better
argument than the one that document leads with. If the Herdr question is
reopened, lead with this.

Everything else in `internal/tty` — key normalization, viewport selection,
ANSI-aware slicing, search, links, Bubble Tea integration — is Sidecar's own
experience and would survive any backend.

## Relationship to td-48ecf2

The active agent-activity plan already absorbs Herdr's best detection ideas:
manifest-shaped rules, foreground-process gating, live-bottom regions,
debounced transitions, evidence IDs, and unseen-idle → `done`. It does not need
rewriting. Two small additions from this exploration:

1. **Design the state-change notification seam now** (lesson 4), even if the
   left pane is the only consumer in v1. Retrofitting a push seam onto a poll
   pipeline is the expensive order.
2. **Make sequence-guarding a stated contract** rather than a test-matrix line
   item (lesson 5). `ControlSnapshot.Generation` already exists; the new
   `agentactivity` results should carry the same guarantee explicitly.

Proceeding with td-48ecf2 as written is the right next move. It is the
highest-value, lowest-risk item on this page: it delivers the agent-awareness
half of the Herdr proposal's value with no new runtime dependency.

## Suggested sequencing

1. **Now:** proceed with td-48ecf2 as planned, plus the two additions above.
2. **Cheap, independent, any time:** the tech-debt table. Unify the scrollback
   constant, bound by bytes, and either use or stop decoding
   `controlEvent.Data`.
3. **Next real investment:** a scoped spike on lesson 1 — feed `%output` into a
   cell grid behind a flag, with a fidelity harness comparing the grid against
   `capture-pane` on known byte sequences. Decide from measurements, not from
   this document.
4. **Only if (3) says "we are writing a terminal emulator":** reopen the Herdr
   replacement question, leading with the geometry argument.
