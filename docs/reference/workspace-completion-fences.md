# Workspace asynchronous completion ownership

An asynchronous command belongs to the project, worktree, and viewer context that requested it. Switching projects while it runs must never write the next project's manifest, change its selection, inject a command into its shell, or show the old operation's error in its modal. Successful durable operations remain recorded for their original owner and are discovered when that project is viewed again. Dropping a presentation completion does not roll back a completed operation or kill its tmux session.

## Project Workspaces

`OperationScope` carries the requesting context epoch, project root, and working directory. Command constructors capture the scope before dispatch. Shell and pane commands use a completion scope independent of the single worktree lifecycle operation. Both `Plugin.Update` and the internal update handler reject stale scopes before applying state or scheduling continuations. The public entry point also rejects stale `plugin.EpochMessage` results before terminal reconciliation and live-watch routing; this covers asynchronous td-store discovery as well as content loads.

Shell creation persists through the original `workspaceops.Service` adapter. `ShellCreatedMsg` is only applied to the matching projection; its prefill command travels with the completion. Agent launch captures the original context and target before dispatch. Rename captures the original manifest and namespace before dispatch. Init clears pending prefill/resume state. Prefill enters interactive mode only after a successful injection, while the injected shell remains selected.

The completion audit for td-eeb7e8 covers these families:

| Family | Fence and routing |
| --- | --- |
| Shell create/recreate, delete, rename, agent start/errors, prefill, attach, and resume | Captured `OperationScope`; durable adapter and target captured before the worker runs. Success and error results obey the same fence. |
| Worktree create/setup/recovery, delete/dirty check, merge/PR/push, task linking, and branch/task loaders | Captured `OperationScope`; existing lifecycle operation ID and repository/worktree checks remain. Deferred creation identity remains available for recovery. Agent restart continuations also carry the requesting scope. |
| Shell liveness suspicion/probe/death and explicit agent stop | Captured completion scope, alongside the existing name-life, server-incarnation, and poll-generation checks. Generation-zero lifecycle results still carry project ownership. |
| Agent and shell output/poll timers; resize and session-validation timers | Existing keyed poll generations survive reset by invalidating prior owners; terminal ownership and validation/resize generations reject old work. Explicit stop results add completion scope. |
| Terminal split creation/seeding/close probes, history/search loads, and interactive send failures | Captured completion scope, alongside existing session, leaf, buffer identity, and request-generation checks. |
| Document link/search and pane-switcher suggestions | Captured completion scope; root, leaf, source, and search-request identities remain in effect. File scan replies already carry the root. |
| Content deck, document/issue/note/resource/plugin loads, live watchers, and terminal models | Existing epoch, source-context, request, watcher, or terminal subscription identities; epoch results are fenced before the public update entry routes them. Watcher transport signals acquire their bound project scope when delivered. |

Zero scopes remain accepted for legacy internal/test messages. Production command constructors stamp scopes. Global app requests, input, theme/focus changes, host inventory broadcasts, and watcher transport signals are not operation completions: they are intentionally delivered to the current projection, or routed by their existing source identity.

## Sessions

Sessions retains its multi-project model. Results already carry `Project`, workspace ID, source context, and remote host incarnation as appropriate. A configuration generation now additionally fences mutation, create, delete, rename, split, and reap replies when the configured project set changes. It compares configured project path membership, independent of order: adding, removing, or replacing a project advances authority; reordering the same projects and normal inventory polls do not. Reordering preserves in-flight collection, operation dialogs, pending split seeds, and delivery of their completions.

Changing the configured project set retires open operation dialogs and pending split seeds. A busy dialog must not remain on screen waiting for a completion whose configuration has just been invalidated. The durable worker still uses its original owner.

Create, rename, and delete completions also carry their dialog generation. Reopening a dialog invalidates the previous dialog's replies, even for the same resource. Local picker suggestions and file scans must match the selected source root. Split creation can route into its original current or cached workspace even after a replacement dialog opens: its pane adoption uses configuration and leaf identity, while its dialog updates additionally require the original dialog generation, including generation zero before any dialog opens. Restoration and layout adoption capture that generation too. A matching pending seed runs at that original session even when its workspace is cached; seed errors retain the originating dialog authority. It does not clear a different workspace's pending seed or close a replacement dialog. Remote host-incarnation checks remain authoritative for removed or retargeted hosts.

## Regression and live proof

`completion_scope_test.go` in both surfaces deterministically delivers previous-context results after replacement. The project test checks the next manifest on disk and its sidebar; sibling tests check pending commands, revision, state, and scheduled work. A delayed rename test uses colliding shell names in two manifests. Sessions tests cover old mutation successes/errors, replacement dialogs, and preservation across normal refresh polls. `configuration_completion_review_test.go` also reorders two configured projects through configuration updates, explicit refreshes, and polls, proving that dialogs, split seeds, and completion delivery retain their authority. Removing the completion fences reproduces the original manifest write and dialog changes; removing rename adapter capture reproduces the wrong-owner rename; restoring order-sensitive project equality reproduces canceled collection and discarded operation replies.

Run `go test ./internal/plugins/workspace ./internal/overview`, `scripts/workspace-operations-proof.sh`, and `scripts/ui-api-proof.sh`. Both live proofs build a temporary binary and isolate tmux, state, and configuration; they never install a binary or touch the default tmux server. `./scripts/demo.sh` provides an ephemeral interactive environment for trying project switching and shell operations.
