# Plan: Graduate the byte-fed tmux terminal integration

**Task:** `td-94e749` — Graduate or retire the byte-fed terminal rollout flag

**Upstream follow-ups:** `td-a04666`

**Status (2026-08-09):** Implementation, isolated real-app fallback/reseed
proof, focused/race/full/build gates, and managed worktree install proof are
complete. Every implementation slice has independent approval; final
integrated review is the remaining closure gate for `td-94e749`.

**Decision:** Graduate the byte-fed renderer. Marcus has judged the live rollout
materially better overall despite the known `x/vt` gaps. Those gaps remain
tracked upstream, but they no longer block adoption.

## Outcome

Every visible, tmux-backed terminal that Sidecar owns as a presentation surface
uses ordered `%output` bytes as its steady-state rendering source:

- the workspace terminal panel;
- every worktree agent preview/interactive pane and its per-worktree terminal
  panel, including List and Kanban projections;
- workspace project shells and their terminal panels;
- the Files inline editor; and
- the Notes inline editor.

There is no terminal-surface exception or legacy compatibility mode in this
cutover. Worktree agents and their terminals are first-class migration targets,
not merely regression coverage around the shared code.

`capture-pane` remains part of the new integration for initial seed, explicit
resynchronization, lazy older history, diagnostics, and automatic fallback when
control mode or the byte-fed model is unavailable. What is removed is the old
*steady-state capture renderer* and its duplicate terminal-state heuristics,
not the recovery path.

The temporary launch/config rollout switch and all switch-only branches are
removed. There is one intended rendering path rather than an old/new toggle.

## Implemented state

The shared component now provides the hard transport and rendering pieces:

```text
tmux %output bytes
  -> session-scoped control client and ordered actor
  -> seeded internal/tty/screenmodel model
  -> owner/target/generation-scoped model frame
  -> OutputBuffer and existing terminal viewport
  -> native cursor, modes, history, search, and selection

model cannot establish/retain presentation
  -> capture seed or keyed polling fallback
```

Every consumer now uses that component:

| Consumer | Implemented presentation contract |
| --- | --- | --- |
| Per-worktree terminal panel | Shared byte-fed model with capture seed/recovery |
| Worktree agent preview/interactive pane | Shared byte-fed model; separate semantic activity observation |
| Project shell preview/interactive pane and terminal panel | Shared byte-fed model; separate semantic activity observation |
| Files inline editor | Shared byte-fed `tty.Model` lifecycle |
| Notes inline editor | Shared byte-fed `tty.Model` lifecycle |

The model adapter, control protocol, ordered seed barrier, alternate-screen
seeding, invalidation, capture fallback, output buffer, and viewport already
exist under `internal/tty`. The migration should reuse them rather than add a
second plugin-level transport.

## Product and design rules

1. A terminal transfers display ownership only after its first accepted seeded
   model frame. Until then, provisional capture output remains visible.
2. Model invalidation, discarded control bytes, or control-client death returns
   that consumer to capture polling and initiates a fresh seed where possible.
3. All deliveries remain scoped by owner, session/pane target, role, activation,
   and generation. A late frame from a closed editor or previously selected
   workspace pane must be inert.
4. `%output` drives presentation only. Agent activity continues to use real
   provider/tmux evidence such as pane title and current command; the terminal
   emulator must not infer semantic agent status.
5. Plugins do not grow their own screen-model implementations. Files and Notes
   continue to embed one shared terminal-surface component; Worktrees migrates
   to that same component instead of retaining a plugin-private controller.
6. Known `x/vt` bugs in `td-a04666` stay explicit and independently fixable.
   Do not fork `x/vt`, add application-specific escape repairs, or keep the old
   renderer as an undocumented compatibility mode.
7. No test or proof run touches the default tmux server or the real Sidecar
   state/config tree.

## Reusable terminal-surface boundary

The cutover must leave a single, boring embedding API for future terminal
surfaces. Evolve `tty.Model` into that component or replace it with a clearly
named `tty.Terminal`; do not keep both as competing public abstractions. The
component owns all terminal mechanics:

- target lifecycle: open/enter, switch, resize, visibility/focus, suspend, and
  close/exit;
- pooled control subscription, byte-fed screen model, seed/resync, and capture
  fallback;
- output buffer, absolute history coordinates, cursor, terminal modes, and
  viewport rendering;
- scoped Bubble Tea delivery, mailbox/backpressure handling, and rejection of
  stale target generations; and
- input mapping, paste, mouse forwarding, and full tmux attach support.

Its embedding surface should remain approximately this small (exact names may
follow existing `tty.Model` conventions):

```go
terminal := tty.NewTerminal(config)
cmd := terminal.Open(tty.Target{Session: session, Pane: pane})
terminal.Resize(width, height)
cmd = terminal.Update(msg)
content := terminal.View()
cursor := terminal.Cursor()
terminal.Close()
```

Plugins own only their product and layout policy:

- Worktrees owns selected worktree/shell identity, agent activity evidence,
  List/Kanban projections, split layout, and which target is visible/focused.
- Files owns editor-session creation, tabs/reentry, file-save behavior, and the
  terminal's pane origin.
- Notes owns editor-session creation, autosave/store synchronization, project
  epochs, and the terminal's pane origin.

Neither the component API nor plugin code should expose `ControlManager`,
`screenmodel`, capture polling, model frames, or a renderer feature flag. A
future plugin should be able to embed a terminal by supplying a target and
viewport, routing `Update`, and rendering `View`/`Cursor`, without implementing
transport or recovery logic.

## Implementation slices

### 1. Establish the shared model-first terminal component

- Refactor the proven rollout lifecycle behind the one reusable terminal-surface
  API described above. Preserve the existing `internal/tty/screenmodel` adapter
  and session-pooled `ControlManager` internally; do not leak either one to
  plugins.
- Give the component reusable target and subscription lifecycle for open/enter,
  resize, visibility/focus changes, target replacement, and close/exit.
- Deliver control callbacks through a bounded mailbox/listener `tea.Cmd` (or an
  equivalently scoped message bridge). Callback goroutines must not mutate
  Bubble Tea model state directly, and blocked consumers must not stall the
  ordered control reader.
- On entry, retain polling until the first seeded frame is accepted. Once the
  model is live, cancel/invalidate outstanding poll generations. On model or
  control invalidation, resume exactly one current polling chain and attempt a
  clean resubscription/seed.
- Keep output/view, cursor, preferred mouse mode, pane-coordinate mapping, key
  mapping, paste, and attach behavior as the stable embedding capabilities.
- Preserve capture for seed/resync and fallback. Remove capture-after-keystroke
  and steady adaptive polling from the healthy model-backed state.
- Ensure close and target replacement release subscriptions, stop listeners,
  invalidate generations, and drain or reject queued deliveries.

Acceptance evidence for this slice: component-level contract tests prove a
seeded live state without per-burst capture plus open/close/reopen, target
switch, resize, visibility/focus, session death, control death, stale delivery,
mailbox pressure, and fallback recovery. The same contract suite must later run
through Worktrees, Files, and Notes embeddings without plugin-type branches in
the shared component.

### 2. Make the shared component the ordinary Worktrees and workspace contract

- Remove the feature check from
  `internal/plugins/workspace/terminal_control.go` and request model presentation
  for every visible per-worktree terminal panel, worktree agent, project shell,
  and shell terminal-panel consumer.
- Replace the plugin-private control/model lifecycle with the shared terminal
  component. Worktrees may coordinate multiple components/targets, but it must
  not interpret model frames or implement capture recovery itself.
- Keep role/source-aware target selection so frames update the correct output
  buffer, cursor/mouse state, absolute history, and visible viewport without a
  late delivery overwriting a newly selected target.
- Preserve the independently scheduled semantic status cadence for agent
  activity. Verify List, Interactive, and Kanban behavior against actual agent
  and shell consumers rather than treating rendered output as status evidence.
- Preserve mixed-subscriber correctness: a capture-dependent diagnostic or
  semantic subscriber sharing a pane must still receive its captures, and one
  subscriber's failure must not revoke another subscriber's valid presentation.
- Replace rollout-era ownership names, panel-only helpers, and opt-in comments
  with names that describe the permanent presentation contract. Keep
  an explicit live-model state if it still distinguishes seeded model delivery
  from provisional/fallback capture; remove it if it has become an always-true
  request option.

Acceptance evidence for this slice: worktree agents, their terminal panels,
project shells, and shell terminal panels render and accept input through the
model path; switching worktrees/shells, changing List/Kanban/Interactive views,
hiding/showing the panel, resizing, and killing/restarting the isolated control
client either reseed correctly or visibly fall back; activity state remains
truthful in List and Kanban.

### 3. Adopt the shared path in Files and Notes

- Keep both plugins thin: their existing editor-session creation, save/exit,
  pane geometry, view, cursor, mouse, copy/paste, and attach flows continue to
  call the shared terminal component.
- Update their ordinary Bubble Tea message routing for the component. Model
  delivery/listener messages remain internal to the component; avoid
  copy-pasting lifecycle or fallback logic into each plugin.
- Verify Files tab switching and editor reattachment: an inactive tab must not
  keep applying frames, and returning to the tab must seed the still-running
  tmux editor before transferring presentation.
- Verify Notes autosave and project reinitialization remain independent of the
  renderer. A stale terminal message must never reach a newly opened note or a
  store from the previous project epoch.
- Cover vim/nvim alternate screen, nano/emacs-style main screen, syntax color,
  native cursor, scrolling/clipping, mouse forwarding, literal and modified
  keys, multiline paste, editor exit, and full tmux attach.

Acceptance evidence for this slice: the real Files and Notes inline-editor
journeys use model presentation in an isolated Sidecar run, and instrumentation
shows no steady-state `capture-pane` for ordinary editor output. Run the shared
embedding contract against Worktrees, Files, and Notes and prove that the
component contains no plugin-name/type switch or callback that exposes its
transport internals.

### 4. Remove the old product path and temporary rollout surface

- Delete the temporary renderer feature symbol, registry/default test, and
  CLI/config discoverability.
- Remove flag-disabled tests and rewrite rollout-specific tests as permanent
  contract and fallback tests. Keep negative tests for failure recovery rather
  than preserving an artificial “old mode”.
- Delete code used only by steady-state capture rendering: redundant
  capture-on-output scheduling, terminal-mode scanning, mouse-fragment cleanup,
  cursor/mode side queries, and duplicate application helpers. Before deleting
  each piece, prove it is not still used by seed/resync/history/fallback. No
  worktree-only copy of the legacy renderer may remain.
- Keep `SIDECAR_TMUX_SCREEN_COMPARE` only if it remains useful as a diagnostic
  oracle for upstream upgrades. It is not a user-selectable renderer and must
  not restore capture presentation in normal operation.
- Update the shell-integration skill, feature documentation, headless-testing
  guidance, transport decision record, and the earlier byte-screen plan to say
  that model presentation is the normal path and capture is the bootstrap/recovery
  adapter.
- Record the explicit graduation decision and the non-blocking status of
  `td-a04666` in `td-94e749`.

Acceptance evidence for this slice: repository search finds no stale rollout
switch or experimental-renderer claim, and there is no
reachable healthy-state capture renderer hidden behind another condition.

## Test and proof plan

Run focused tests while each slice is small, then the full gates:

```bash
go test ./internal/tty/... ./internal/plugins/workspace/... \
  ./internal/plugins/filebrowser/... ./internal/plugins/notes/...
go test -race ./internal/tty/... ./internal/plugins/workspace/... \
  ./internal/plugins/filebrowser/... ./internal/plugins/notes/...
go test ./...
go build ./...
git diff --check
```

Add or retain behavior-faithful coverage for:

- seed/reseed on main and alternate screens, resize, pane switch, editor
  reattachment, and Sidecar restart;
- ordered bytes, stale generations, mailbox saturation, discarded bytes,
  control reconnect/death, model invalidation, and single-owner fallback;
- absolute history, lazy prepend overlap, search/selection stability, native
  cursor, Unicode/graphemes, colors, OSC links, mouse modes, and paste;
- multiple worktrees and their terminal panels, a real agent, a project shell
  and its terminal panel, the Files editor, and the Notes editor;
- selection changes among worktrees and shells without cross-target frames or
  stale status.

Real-app proof uses `scripts/tmux-drive.sh`. First run
`./scripts/tmux-drive.sh paths` and verify both the tmux socket and state/config
paths are isolated. Capture text and PNG evidence for worktree agent output,
the per-worktree terminal panel, a project shell and its terminal panel, Files,
and Notes. Include a selection-switch proof using at least two worktrees or
worktree/shell targets. Add a command-counting shim or diagnostic counter to
prove healthy steady-state output does not invoke `capture-pane`; then kill the
isolated control client and prove capture fallback visibly continues before a
successful reseed.

Because this changes a shared concurrency and rendering path, completion also
requires an independent review covering every slice, followed by any fixes and
a second clean verdict. The final install proof is from the intended worktree
with `make install-worktree`, followed by `make install-status`; it must not
alter or restart the default tmux server.

## Completion criteria

The cutover is complete when:

1. every integrated terminal consumer—including worktree agents and terminal
   panels, project shells and terminal panels, Files, and Notes—uses the
   byte-fed model for healthy steady-state rendering;
2. selection and lifecycle changes cannot deliver output, cursor, history, or
   activity state across worktree/shell/editor targets;
3. seed/resync/history/diagnostic and control-failure capture paths remain
   tested and working;
4. the legacy steady-state renderer, duplicate heuristics, feature flag, and
   stale documentation are removed;
5. focused, race, full test, build, isolated real-app, fallback, and managed
   worktree-install proofs pass; and
6. an independent reviewer approves the integrated change.

The open `td-a04666` stories do not prevent completion. They remain the durable
place to bring qualifying upstream fixes into the shared adapter and rerun the
relevant model and real-terminal proofs.
