# Agent Shell & Worktree Creation CLI

Let agents create Sidecar-visible shells and worktrees non-interactively:

```bash
sidecar create shell --name "dev server" --run "npm run dev"
sidecar create shell --split right --run "go test ./... -watch"   # terminal split beside the agent's shell
sidecar create worktree fix-auth --base main --agent claude
```

A shell created this way appears as a normal workspace row in the running Sidecar
(or as a terminal split on the agent's current shell), the user can click into it
and interact with the server the agent started, and it survives Sidecar restarts
like any other managed shell.

**Why this earns a CLI despite Sidecar being presentation-layer:** the ownership
test. Shell records (`shells.json`, display names, agent metadata) and worktree
lifecycle (setup hook, journal, session naming) are capabilities Sidecar *owns* —
they vanish if Sidecar is uninstalled. An agent can already run `tmux new-session`
or `git worktree add`, but the result is invisible to Sidecar and unmanaged.
Parity is owed here.

## Relationship to other plans

- **[terminal-splits-and-windowing.md](terminal-splits-and-windowing.md)** — owns
  the live Terminal leaf kind and split placement. This plan's `--split` mode is a
  *client* of its Phase A: the CLI ships the workspace-shell mode first, and the
  split mode lands only once the Terminal leaf exists. Placement vocabulary
  (`auto|right|below`, `panelayout.ApplyAxisOverride`) is shared, per that plan's
  decision 3.
- **implemented/agent-open-in-split-cli.md** — the template: `uirequest` bus,
  ack/exit-code contract, `--wait`. This plan adds new actions to that bus, not a
  new transport.
- **implemented/agent-shell-renaming-cli.md** — precedent for a CLI action that
  mutates shells.json and is reflected live.

## Settled decisions

1. **Two creation modes, one flag.** Default = a new **workspace shell** (a peer
   row in the project's workspace, like ctrl+n in the TUI). `--split
   auto|right|below` = a **terminal split** placed beside the agent's current
   shell per the terminal-splits placement model. `--split` without a value means
   `auto`.
2. **Works headless; better when Sidecar is running.** Workspace-shell creation
   goes through `workspaceops.CreateManagedShell` directly — the existing
   `ShellWatcher` + `mergeShellState` path makes it appear in any running
   instance with no IPC. A `uirequest` action is layered on top only for what
   the manifest can't carry: split placement, focus/selection, and a real ack.
   If no instance acks, the workspace-shell mode still succeeds (exit 0 with
   `"acked": false` in `--json`); the split mode fails with exit 3 (a split
   needs a live pane tree to split).
3. **`--run` executes the command** in the new shell via
   `workspaceops.StartAgentInShell` (tmux `send-keys … Enter`). `--type` is the
   no-Enter variant (mirrors `sendResumeCommandToShell`) for commands the user
   should review before running. They are mutually exclusive.
4. **Context resolution reuses `sidecar open`'s ladder** (`resolveOpenDestination`):
   current shell env (`SIDECAR_SHELL`) → `--shell` → `--project` → unique running
   instance → cwd's project root. The created shell belongs to the resolved
   project; `--split` additionally requires a resolvable *current shell* to split
   beside.
5. **One `create` command group, kind as subcommand:** `sidecar create shell`,
   `sidecar create worktree`. Per-kind flags stay separate and discoverable
   (`sidecar create --help` lists the kinds), and the group is where future
   kinds land — e.g. a `create server` shortcut (a shell pre-wired to run a
   project's dev server) — without renaming anything. Worktree is its own
   subcommand because its inputs differ (name, `--base`, `--agent`, `--skip-permissions`,
   setup hook). It wraps `workspaceops.ResolveWorktreePlan` + `ExecuteWorktree` +
   the setup pipeline (`workspaceops/setup.go`), honoring
   `.worktree-setup.sh` and the config's env-file rules — identical semantics to
   the TUI create modal, no forked logic. `--run`/`--agent` seed the worktree's
   agent session exactly as `launchCreatedWorktree` does.
6. **Naming and records are the shared core's, untouched.** Display names via
   `shellstate.NormalizeName` + `workspaceops.ShellNames`; sessions keep the
   `sidecar-sh-<proj>-N` / `sidecar-ws-<dir>` conventions; all manifest writes go
   through `shellstate` atomic APIs. The CLI adds zero new persistence.
7. **Structured output.** `--json` prints `{shell: {displayName, session,
   workDir}, acked, surface, placement}` (worktree variant adds `path`,
   `branch`, `setup` outcome). Exit codes match `sidecar open`: 0 ok, 2 usage,
   3 no instance (split mode only), 4 declined (e.g. window too small to split,
   live-leaf cap reached — refusal reasons come from the host verbatim).
8. **Discoverable to agents.** Both commands declare `Agent: AgentDoc{...}` so
   they appear in `sidecar --agents` automatically; `gendoc_test.go` regenerates
   the reference doc.

## Work sequence

### M1 — `sidecar create shell` (workspace-shell mode, headless-capable)

- New `create` command group in the registry (`internal/cli/registry.go`) with
  the `shell` subcommand's `Run` in `internal/cli/create_shell.go`.
- Refusal rule: if context resolution finds no known project (no shell env, no
  flags, no unique instance, cwd not a project root), exit 2 with a hint —
  never initialize project state. Mirrors `open`'s behavior.
- Flags: `--name`, `--run`, `--type`, `--project`, `--shell`, `--json`, `--wait`.
- Implementation is a thin shell over the global browser's proven path
  (`overview/global_create.go: submitCreateShell`): `ShellNames` →
  `NormalizeName` → `CreateManagedShell` (rolls back tmux on manifest failure) →
  optional `StartAgentInShell`. Extract any duplicated glue into
  `workspaceops` rather than copying it — the CLI must not grow logic the TUI
  lacks.
- New `uirequest` action `create` with a payload carrying a `kind` field
  (`{kind: "shell", session, displayName, focus}` here; `kind: "worktree"` in
  M2, future kinds later): on receipt the workspace plugin (and global
  browser) reconcile immediately (skip the watcher debounce), select the new
  shell, and ack. CLI polls acks per the `open` handshake; absence of an ack is
  non-fatal in this mode.
- **Proof:** isolated `tmux-drive.sh` run — running Sidecar on screen, agent
  shell runs `sidecar create shell --run 'python3 -m http.server'`, snapshot
  shows the new row selected with the server running; second proof with no
  Sidecar running, then launch, row appears.

### M2 — `sidecar create worktree`

- Flags: `<name>`, `--base`, `--agent`, `--skip-permissions`, `--run`, `--json`,
  `--no-launch`.
- Wraps `ResolveWorktreePlan`/`ExecuteWorktree`/setup pipeline with the same
  crash-safety expectations as the TUI (`create_operation.go`'s pending-creation
  journal — reuse it or extract its core into `workspaceops`, don't skip it).
- Same ack layering for selection in a running instance: the shared `create`
  action with `kind: "worktree"`.
- **Proof:** isolated run creating a worktree with a hook script; assert setup
  hook ran, env files copied, row appears, agent session named `sidecar-ws-…`.

### M3 — `--split` mode (gated on terminal-splits Phase A)

- Blocked until the Terminal leaf kind (`panelayout` `Shell` kind, A1–A2 of the
  terminal-splits plan) ships. Until then the flag exits 2 with a message naming
  the limitation.
- Then: `--split` routes entirely through `uirequest` (the `create` action with
  `Options.Split`); the host creates the session via the same
  `CreateManagedShell` core, opens it as a Terminal leaf via the duplicable-kind
  `PlanOpen` path with `ApplyAxisOverride`, and acks with the placement taken.
  Host-side refusals (fit, live-leaf cap) surface as exit 4 + reason.
- **Proof:** agent inside a Sidecar shell runs
  `sidecar create shell --split right --run '…server…'`; snapshot shows two live
  terminals, the server interactable, sidebar badge `◧◨`.

## Acceptance evidence

- Unit tables: flag parsing/exit codes; `create` request handling on both
  surfaces (workspace plugin + overview) asserting parity.
- `tmux-drive.sh` transcripts for the three proofs above, fully isolated
  (both tmux socket and state tree — `paths` check first).
- `sidecar --agents` output includes both commands; regenerated gendoc committed.
