# Agent broadcast: one message to every live agent

**Status:** implemented. **Scope:** a new application core (`internal/agentbroadcast`), a `sidecar agent broadcast` verb, one modal hosted by the workspace list and the Sessions surface, `sidecar agents` help, the coordinate-agents skill, `docs/reference/cli.md`. **Created:** 2026-09-05

One sentence: **an agent or a human can put one short message in front of every live agent in a project, or every live agent Sidecar can see, without starting anything, and get back a per-target receipt for what was and was not delivered.**

## Recommendation

Build it, and keep it exactly this small: a fan-out over `sidecar agent prompt`. The reasons it is worth doing at all are the same reasons it is cheap.

- **Sidecar already owns the two things nobody else has.** It knows which panes hold a positively identified, live agent right now, and it can put text into one of them with the safety rules `agent prompt` already enforces. Those are the whole feature. Comms owns durable messaging and deliberately does not push; a pull-based bus is only as good as agents remembering to pull, which is why "everyone stop, there is a code freeze" has no reliable delivery path today. Broadcast is the one push primitive that makes the pull model workable for urgent things, and it does not need to become a second messaging system to do that.
- **The safety story already exists.** `agent prompt` refuses before writing a byte to a pane that is blocked, dead, in copy mode, unidentified, or stale, and it never starts a provider. Broadcast inherits every one of those refusals per target. The "don't wake stopped agents" worry in the brief is answered by construction: a shell whose agent has exited has no identified provider, so it is not a recipient and cannot be made one.
- **Receipts, not acknowledgements.** Every prompt already carries a receipt (`submitted` / `not_submitted` / `unknown`, plus the stall check that proves the agent reacted). Broadcast reports those per target and stops there. No read tracking, no reply channel, no waiting for N agents to settle. A caller who wants replies puts the durable content in comms and broadcasts the pointer.

Two things in the brief I would change, both argued below: prefix the delivered text with a short envelope so the receiving agent knows it is a broadcast and not its user typing, and treat the checklist and `--dry-run` as the same artefact so the human modal is never a richer surface than the agent verb.

## The journeys this plan must make real

### 1. An agent warns every other agent in the project

An agent about to restart a shared dev server, or one that has just learned of a code freeze, runs:

```bash
sidecar agent broadcast "Code freeze on main until td-1a2b3c lands; hold pushes. Details: comms peek msg_01J9…" --json
```

Without a scope flag the recipients are the live agents in the caller's own project, minus the caller. Each recipient's next turn begins with the message. The caller gets back one row per target saying `submitted`, or the exact refusal code it did not get past, and exits 0 if at least one was submitted.

### 2. An agent tells everyone Sidecar can see

The same command with `--all`. Every registered project's live agents are candidates. Remote hosts are not, in the first slice; see below.

### 3. A human does the same from the keyboard

`B` on the workspace list, or on the Sessions surface, opens **Broadcast to agents**. The list is the recipients, already checked where Sidecar would send and unchecked with a dim reason where it would not. The human edits the text, toggles rows if they want, and presses enter. A toast summarises delivered and skipped; the notification centre keeps the per-target rows.

### 4. Either of them sees what would happen first

`sidecar agent broadcast TEXT --dry-run --json` prints the plan and sends nothing. The modal's checklist is that same plan rendered, so a human and an agent are looking at one truth.

## What Sidecar owns, and what it does not

| Owned by Sidecar | Not owned by Sidecar |
| --- | --- |
| Which panes hold a live, identified agent, and their status right now | Whether the receiving agent does anything with the message |
| Writing text into a managed agent's input safely | Durable messages, threads, read receipts, replies (comms, or nothing) |
| The per-target delivery receipt | Waiting for the recipients to settle (`agent wait`, per target, if a caller wants it) |
| Excluding the sender and refusing unsafe targets | Any notion of an inbox for agents that were not live at send time |

Uninstall Sidecar and the ability to reach "every live agent" vanishes, so this passes the ownership test that carried `agent prompt`. It is deliberately **not** a comms adapter: Sidecar does not know comms exists. The recommended convention is documentation, not code: durable content goes in a comms message, the broadcast carries a one-line summary and the message id. A `--ref` flag that formats that pointer is a later slice if the convention proves itself.

## Contract

### The verb

```text
sidecar agent broadcast TEXT [--project NAME | --all] [--to TARGET ...] [--exclude TARGET ...] [--status STATUS ...] [--include-self] [--raw] [--dry-run] [--json]
```

| Flag | Meaning |
| --- | --- |
| *(none)* | Recipients are the live agents in the caller's project. Outside a managed shell the project is ambiguous, so `--project` or `--all` is required (usage error otherwise). |
| `--project NAME` | Scope to one project, with the same resolution `agent get --project` uses. |
| `--all` | Scope to every registered project on this machine. |
| `--to TARGET` | Explicit recipients instead of discovery. Repeatable. Each target resolves the way `agent prompt TARGET` does, and each still passes the promptable check. This is the agent's override in the brief: session names from `shells.json`, or unique display names. |
| `--exclude TARGET` | Remove a discovered recipient. Repeatable. |
| `--status STATUS` | Narrow discovery to agents in these states. Default is the promptable set: `idle`, `done`, `working`. Repeatable. |
| `--include-self` | Do not drop the calling shell. Default drops `SIDECAR_SHELL` so an agent never prompts itself. |
| `--raw` | Deliver the text exactly as given, without the envelope. |
| `--dry-run` | Print the plan and send nothing. Exit 0 even if the plan has no recipients. |
| `--host ID` | Not accepted in the first slice; usage error naming this plan's remote-hosts section. |

`--to` and scope flags compose: `--to` adds to a discovered set rather than replacing it only when a scope flag is also given; alone, `--to` is the whole set. Text is a positional argument or `-` for stdin, like comms.

### What a target must be

A recipient receives text only if, at send time, it passes the exact `promptable` check `agent prompt` applies: exactly one live pane, not in copy mode, a provider positively identified, status `current`, and status in `idle`, `done`, or `working`. There is no `--force` past that set. In particular:

- A pane with **no identified provider** is never a recipient. This is what "we don't want to start up agents that are stopped" means in code: text is never typed into a shell prompt, and the raw `shell send --run` path is not used.
- A **blocked** agent is skipped with `agent_blocked`. Broadcast does not answer approvals, and typing a message into an approval prompt would be answering one.
- A **working** agent is a recipient by default. Every supported provider queues text typed mid-turn as the next user turn, which is the right semantics for "be aware of X when you next look up". A caller who disagrees narrows with `--status idle --status done`.
- An agent whose status is **`unknown`** or whose freshness is not `current` is skipped with `agent_not_ready`. The override for this case is the same as `agent prompt`'s: none. If detection is wrong about a pane, the fix is `sidecar agent explain` and the manifest, not a broadcast flag that bypasses detection for every pane at once.

The sender's own shell is dropped by default. Panes that are Sidecar-managed but not agents (plain shells, editors) do not appear in the plan at all; the plan's summary reports how many managed shells were not candidates so a caller can tell "no agents" from "no shells".

### The envelope

Delivered text is, by default:

```text
[Sidecar broadcast from "tacoma-fable" in clara-home] Code freeze on main until td-1a2b3c lands; hold pushes.
```

From the TUI, the sender is `the user` rather than a shell name. The receiving agent sees a user turn; without the envelope it would have no way to know the text was not typed by its own user, and would act on "stop pushing" with the authority of a direct instruction. A one-line prefix is the honest amount of framing. `--raw` exists for callers who are composing their own framing and is not offered in the modal.

### Output

Human output is one row per target:

```text
tacoma-fable        claude   working   submitted
Shell 19            claude   idle      submitted
inventory           muse     unknown   skipped   agent_not_ready: status is unknown, not current
blender             codex    working   skipped   sender
3 submitted, 2 skipped. 2 managed shells had no live agent.
```

`--json` is one object:

```json
{
  "text": "…delivered text, envelope included…",
  "scope": {"kind": "project", "project": "clara-home"},
  "recipients": [
    {"target": {…pinned target…}, "agent": {"kind": "claude", "status": "working"}, "outcome": "submitted", "receipt": {…}},
    {"target": {…}, "agent": {…}, "outcome": "skipped", "reason": {"code": "agent_not_ready", "message": "…"}}
  ],
  "summary": {"submitted": 3, "skipped": 2, "unknown": 0, "shellsWithoutAgent": 2}
}
```

`outcome` is `submitted`, `skipped` (refused before any byte was written, with the `agent prompt` error code as the reason), or `unknown` (a write may have landed; never retried automatically, same as a single prompt). `--dry-run` uses `would_send` and `skipped`.

Exit codes follow `agent prompt`: 0 when at least one recipient was submitted or `--dry-run` ran; 5 when every recipient was refused, or the feature is off; 2 for usage; 1 for a transport failure that stopped the fan-out. An empty plan with no `--dry-run` exits 5 with `no_recipients`, so an agent that scripted a broadcast into an empty project finds out.

### Delivery is parallel and bounded

Each recipient is its own pinned prompt, so submissions run concurrently with a small bound (four at a time is enough; the cost is tmux round-trips, not CPU). The 5-second stall check stays, because it is the receipt. Worst case for a large fan-out is therefore about five seconds plus overhead, not five seconds per agent. Nothing waits for any recipient to settle; `--wait` is deliberately absent, and the help text says to use `agent wait` per target if that is wanted.

## One application core

The rule is the one every other agent verb follows: one function, two thin callers.

```text
internal/agentbroadcast
  Plan(ctx, PlanRequest) (Plan, error)     // discovery + eligibility, no writes
  Send(ctx, Plan, Text) (Result, error)    // fan-out over agentcontrol.Service.Prompt
```

- `PlanRequest` carries scope (project, all, explicit targets), excludes, status filter, and the sender identity to drop. `Plan` is the list of candidates with a verdict and reason per row, in the order the modal will show them (grouped by project, then by display name).
- `Plan` uses the same candidate enumeration `agent list` uses today (`managedTargetCandidates` over `scanProjects`), so a pane is one row however many state directories can see its checkout. That enumeration moves from `internal/cli` into a package both the CLI and the TUI can import, or `agentbroadcast` takes it as a function. Which is a detail to settle in the first slice; the constraint is that the TUI must not grow its own copy.
- `Send` calls `agentcontrol.Service.Prompt` per row with `Wait: false`. It adds no new write path. The stall receipt, the pin, and the refusal codes are `agentcontrol`'s.
- Envelope formatting is a pure function in the same package, with the sender description as input.

The CLI verb parses flags into a `PlanRequest` and renders `Result`. The modal builds a `PlanRequest` from its scope toggle, renders `Plan` as the checklist, applies the human's toggles as excludes, and calls `Send`. Neither surface knows how eligibility is decided.

Gate: `agent_control`, the existing default-off flag. Broadcast is the same capability as prompt, applied to a set; a second flag would be ceremony. The modal's keybinding and palette entry carry `Feature: "agent_control"` so they do not appear when the flag is off, exactly as `M` / `pane_move` does today.

## The modal

Hosted in two places over one component: the workspace list (project scope, `B` in the `workspace-list` context) and the Sessions surface (global scope, `B` in `global-workspaces`). Both keys are unbound today. A palette entry, "Broadcast to agents", reaches the same modal from anywhere agent control is on.

Rules from `modal-redesign.md` apply: one background, columns not wrapped lines, plain-text actions. The whole design goal is that it reads in two seconds.

```text
╭─ Broadcast to agents ───────────────────────────────────────╮
│                                                             │
│  SCOPE   ❯ this project   all projects                      │
│                                                             │
│  RECIPIENTS ───────────────────────────── 3 of 5 selected   │
│  [x] tacoma-fable          claude   working                 │
│  [x] Shell 19              claude   idle                    │
│  [ ] inventory             muse     unknown   not current   │
│  [ ] muse orchestrator     muse     blocked   answering     │
│  [x] ix-junction-sol       codex    idle                    │
│      2 shells have no live agent and are not listed         │
│                                                             │
│  MESSAGE ──────────────────────────────────────────────────  │
│  ❯ Code freeze on main until td-1a2b3c lands; hold pushes.  │
│                                                             │
│  enter send   space toggle   a all   n none   esc cancel    │
╰─────────────────────────────────────────────────────────────╯
```

- **Rows are the plan, verbatim.** Checked means Sidecar would send. Unchecked with a dim reason means it would refuse; the human can check such a row, and the send will refuse it again and report why. That is the human's override in the brief, and it is honest: the box is a request, the receipt is the answer. Sidecar's own refusals are not overridable from either surface, which is the same line `agent prompt` draws.
- **Only agents are listed.** Shells without a live agent are a count, not rows. Listing every managed shell with a checkbox is what would make this overwhelming, and none of those rows could ever receive anything.
- **Grouped by project under `all projects`,** with the project as a section label. Under `this project` there is no grouping and the row count is small.
- **The sender's own pane is not applicable** in the TUI: the user is not an agent. Every live agent is a candidate.
- **The text field is single-line.** Broadcast is a headline, not a document. Long content belongs in a comms message or a file the headline points at. The envelope is applied on send and shown nowhere in the modal, to keep the field about what the user is saying.
- **Focus order:** scope, list, message, with the message field focused first, because the common case is "type, enter". Changing scope re-plans and re-renders the list without losing the text.
- **Result:** a toast, `Broadcast sent to 3 agents (2 skipped)`, and the per-target rows filed in the notification centre under a `broadcast` source so the skipped reasons are one keypress away rather than lost.

## Remote hosts

Not in the first slice. `--all` means this machine. The per-verb `--host ID` pattern already exists and would give "broadcast within host X" for free once the host's Sidecar knows the verb, and a later `--all-hosts` is a fan-out of that with the host in each receipt row. Both wait until the local shape has been used for a while; the Sessions surface already shows remote agents, so the modal would need to say plainly that remote rows are not yet recipients rather than silently omitting them.

## Work sequence

### S0: core and plan

- `internal/agentbroadcast` with `Plan`, `Send`, the envelope function, and the verdict vocabulary.
- Move or expose candidate enumeration so the package does not depend on `internal/cli`.
- Tests over the existing `agentcontrol` fake terminal: sender exclusion, status filter, blocked and unknown skipped with the right codes, `--to` resolution through the same tie-break rules, parallel send with one refusing target does not stop the others, `unknown` submission outcome is reported and not retried.

### S1: the verb

- `sidecar agent broadcast` with `--dry-run` first, then send. `--json` envelope, exit codes, `sidecar agents` line, `docs/reference/cli.md` via gendoc.
- Update `.agents/skills/coordinate-agents/SKILL.md` and its `.claude` mirror with a short "Tell every agent something" section, including the comms pointer convention.
- Real consumer proof: from a managed shell in a demo environment with three agents, broadcast, and show each agent's next turn begins with the enveloped text; show a blocked agent skipped with `agent_blocked`; show a plain shell absent from the plan.

### S2: the modal

- One component, hosted by the workspace plugin and `internal/overview`, keybinding `B` in both contexts plus the palette entry, all carrying the `agent_control` feature.
- Toast and notification-centre rows.
- `./scripts/demo.sh` scenario with mixed agent states so the modal's reasons column is visible.

### S3: closeout

- `docs/features.md` entry, plan moved to `implemented/`, td issue closed with the proof.

## Acceptance evidence

- `sidecar agent broadcast --dry-run --json` and the modal's initial checklist are produced by one `Plan` call and agree row for row; a test asserts this by rendering both from one fixture.
- A pane with no identified provider never appears in a plan and never receives bytes, proven with a managed shell sitting at a bash prompt during a broadcast.
- No provider is ever started by a broadcast: the fan-out has no code path to `agentcontrol.Service.Start`, and a test asserts the fake terminal saw only prompt writes.
- A blocked agent's screen is unchanged after a broadcast that listed it.
- A fan-out to eight agents completes in under ten seconds on the demo environment.

## Settled decisions

1. **Envelope wording.** Delivered text is `[Sidecar broadcast from "<shell>" in <project>] …` from the CLI and `[Sidecar broadcast from the user] …` from the TUI. No timestamp: the point is framing, not a log line. `--raw` skips the envelope. Tests pin this exact string.
2. **`working` agents are recipients by default.** Queued-as-next-turn is what every provider does and is what "be aware of X" wants. A caller who wants only idle/done passes `--status idle --status done`. No config setting.
3. **`B` plus a palette entry.** Two unbound `B`s exist (`workspace-list` and `global-workspaces`). Bind both, gate on `agent_control`, and add a palette entry "Broadcast to agents" so the same modal is reachable from anywhere the flag is on.

## Rejected alternatives

- **A `--all` flag on `agent prompt`.** The target semantic changes from one to a set, the output changes from one receipt to many, and `--wait` stops making sense. A separate verb keeps `prompt`'s contract clean.
- **Delivering through `shell send --run`.** It types into any registered session with no provider check, which is exactly the "wake a stopped agent" failure the brief is worried about.
- **Acknowledgements or a reply channel.** That is a messaging system, and one exists. Receipts are the honest ceiling for a terminal-input primitive.
- **Listing every managed shell in the modal with a checkbox.** Rows that can never receive anything are noise, and enough of them make the modal the overwhelming thing the brief asked to avoid.
