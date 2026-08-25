# Plan: Shell record durability across tmux server death

## Goal

Make it structurally impossible for Sidecar to erase a project's shell records because its tmux server died or restarted. A tmux server going away must degrade shells to offline rows the user can recreate — never to an empty `shells.json`. Every removal of a shell record must trace back either to an explicit user action or to positive, per-session evidence gathered inside a single continuously-observed tmux server incarnation; and any removal that does happen must be reversible through a non-interactive path, because Sidecar owns this data and nothing underneath it can reconstruct a `displayName`, `agentType`, or `skipPerms`.

## The incident

The default tmux server restarted at 13:29 on 2026-08-22. By 13:31 the manifests for `recall`, `clara-home`, `vibes`, `clara`, and `td` had each been rewritten to `{"version":1,"shells":[]}`. `docs/plans/active/tmux-crash-recovery-2026-08-22.md` records the fallout: display names, working directories, agent types and skip-perms flags for every managed shell in five projects, gone in one pass, with no backup and no tombstone. The only `.bak` anywhere under `~/.local/state/sidecar` is `shells.json.overwritten-phase2-20260808T112359.bak`, a one-off from an Aug 8 migration — there is no code in the repo that writes one (`grep -rn '\.bak' --include='*.go'` finds only unrelated `.todos` advice).

One detail corroborates the mechanism precisely: `sidecar/shells.json` kept its `sidecar-global-workspace` entry. That is not a timing accident. That name does not match `shellDiscoveryPattern` (`internal/plugins/workspace/shell.go:242`), so the prune branch refused to claim it. Every record the pattern *did* match was deleted; every record it did not was kept. That is the signature of the startup reconciler, not of the per-shell runtime reaper.

## Current state (verified)

### Where the records live

`ShellManifest` (`internal/plugins/workspace/shell_manifest.go:22`) is the in-process handle on `$XDG_STATE_HOME/sidecar/projects/<slug>/shells.json`. The persisted row is `shellstate.Definition` (`internal/shellstate/shellstate.go:240`) — `tmuxName`, `displayName`, `namespace`, `createdAt`, `agentType`, `skipPerms`, `workDir`. `internal/plugins/workspace` aliases it as `ShellDefinition` (`shell_manifest.go:48`) rather than defining a second shape.

The whole point of the file is recreate-after-death. A definition that is not running renders as `IsOrphaned` (`shell_startup.go:340`), and pressing Enter on that row calls `recreateOrphanedShell` (`internal/plugins/workspace/shell.go:621`), which rebuilds the tmux session under the same name and re-applies `shell.Name`, `shell.ChosenAgent` and `shell.SkipPerms`. **Deleting the record is precisely deleting the only recovery path that exists.**

### The three writers

1. **Startup reconciliation** — `reconcileShellStartup` (`internal/plugins/workspace/shell_startup.go:206`), the only caller of the whole-file replace `manifest.Save()` → `saveLocked()` (`shell_manifest.go:96,106`). It computes an authoritative list and overwrites.
2. **Runtime per-shell close** — `ShellSessionDeadMsg` / `ShellKilledMsg` in `internal/plugins/workspace/update.go:1218` and `:1276` call `p.shellManifest.RemoveShell(...)`; plus `internal/plugins/workspace/plugin.go:1457`.
3. **Global browser reap** — `Model.applyShellProbe` (`internal/overview/shell_liveness.go:146`) → `workspaceops.ForgetManagedShell` (`internal/workspaceops/shell.go:121`) → `shellstate.RemoveIfUnchangedAtPath` (`internal/shellstate/shellstate.go:187`).

Writers 2 and 3 remove one identity at a time under an exclusive flock; writer 1 replaces the file wholesale. Every non-startup edit goes through `mutateLocked` (`shell_manifest.go:142`), which re-reads inside the lock and merges — that is the td-8d18de fix. `saveLocked` is the one surviving blind rewrite, and its doc comment says so: *"Reserved for the startup reconciliation, which has just computed the authoritative list."*

### How liveness is decided today

`internal/shellliveness` is careful and well-reasoned. Its package comment states the asymmetry outright (`liveness.go:12-16`): a stale row is cosmetic, a deleted live shell is data loss, so every ambiguous signal resolves to `Unknown` and `Unknown` never closes anything. `unknownMarkers` (`liveness.go:73`) explicitly lists `"no server running"` with the comment *"it is also what a server restart looks like from the outside, and it would otherwise condemn every shell at once."* `ProbeSession` (`liveness.go:127`) returns `Unknown` on any error. `Tracker.SeenAlive` gates auto-close on this process having watched the session run (`liveness.go:268`), and `Incarnation` (`liveness.go:253`) fences a verdict that was overtaken by a session of the same name being recreated.

All of that reasoning is correct and none of it saved the five projects, because **`shellliveness` is not on the startup path at all.** Startup uses a separate discovery function with the opposite policy.

### The actual root cause

`discoverTmuxSessionNamesForWorkDir` (`internal/plugins/workspace/shell.go:255`) treats "no server running" as a *trusted* empty answer, in direct opposition to `shellliveness`:

```go
if err != nil {
    if !tmuxReportedNoServer(err) {
        return nil, fmt.Errorf("tmux list-sessions: %w", err)
    }
    return nil, nil          // shell.go:262 — trusted "zero sessions"
}
```

Its doc comment argues the case explicitly (`shell.go:249-254`): *"tmux exits 1 with 'no server running' when the server is simply down — that is a genuine empty answer."*

That `(nil, nil)` flows into `loadShellStartup` (`shell_startup.go:186`) as `discoveryErr == nil`, so `reconcileShellStartup` runs with `discoveryFailed = false` and an empty `running` set. Every definition then hits the prune branch:

```go
ours := definition.Namespace == ns && pattern.MatchString(definition.TmuxName)   // shell_startup.go:252
...
if !discoveryFailed && ours {
    changed = true
    continue                                                                      // shell_startup.go:261-264
}
...
manifest.Shells = definitions
if changed {
    _ = manifest.Save()                                                           // shell_startup.go:299-301
}
```

`ns` is `tmuxenv.Namespace()`, which is the *socket path* and nothing else (`internal/tmuxenv/tmuxenv.go:25,52`, and `TestNamespaceIsTheSocketPath`). The socket path is unchanged by a server restart. So after a restart every record still claims this namespace, still matches the pattern, and is pruned.

The write is atomic and locked, so it lands cleanly and irrecoverably. Result: `{"version":1,"shells":[]}`.

There is a second, equally live route to the same outcome that no guard catches at all: the server restarts *and comes back up* with different sessions. Discovery then succeeds with a non-empty listing that contains none of the old shells. `discoveryFailed` is false because there was no error, `ours` is true because the socket path is the same, and the prune runs identically. The 13:29 restart / 13:31 reap timing is consistent with either; the mechanism and the fix are the same.

### The schema `version` field

`manifestVersion = 1` (`shell_manifest.go:51`) is written on every save (`writeLocked`, `shell_manifest.go:189`) and `shellstate` hardcodes `Version: 1` in both `mutateManifest` (`shellstate.go:215,224`) and its struct (`shellstate.go:255`). **Nothing in the repo ever reads it.** There is no version switch, no migration table keyed on it, and `internal/migration` handles only the one-time legacy-path move (`internal/migration/migration.go:1-31`), not schema evolution. The field is currently decoration. A newer-format file written by a future binary would today be silently read as v1, have its unknown keys dropped by `json.Unmarshal` into the known struct, and be rewritten without them — the same class of bug as this one, one writer destroying information it does not understand.

## The design flaw

Liveness is modeled as a property of a single shell. It is not. It is a *joint* property of a shell and the tmux server incarnation the question was asked of.

Every liveness signal Sidecar has — a `list-sessions` listing, a `list-panes -a` inventory, a capture failure, a `has-session` probe — is a statement about one server process. When that process dies, all of those signals change at once, for every shell, for a reason that has nothing to do with any individual shell. The code has no representation of the server as a thing with an identity, so it cannot tell "this one shell exited" from "the world I was observing was replaced." `tmuxenv.Namespace()` looks like it should carry that identity and does not: it identifies the *socket*, deliberately (`tmuxenv.go:11-16` — a hostname component was rejected because a macOS network rename would orphan every entry), and a socket outlives the servers that bind it.

A correct reaper, when its entire evidence source disappears, concludes *nothing*. The rule it needs is: **a shell may only be condemned by evidence drawn from the same server incarnation in which it was last seen alive.** `shellliveness.Tracker` already implements exactly this idea one level down — `Incarnation` (`liveness.go:253`) exists because a *tmux name* can be reused and a verdict about a previous life must not close the current one. The bug is that the same argument applies one level up, to the *server*, and nobody made it there.

There is a second, independent flaw stacked on top, and it is the one that turned a wrong verdict into permanent data loss: **absence is allowed to delete.** `reconcileShellStartup` may shrink the file to zero in one atomic write, and the file it is shrinking is the sole copy of metadata that exists nowhere else on the machine. A record's cost when stale is one offline row in a sidebar. Its value when needed is the entire recreate path. Those are not symmetric, and the code treats them as if they were.

## Options considered

**1. Make "no server running" an error at the discovery boundary.** Change `shell.go:259-262` to stop trusting the empty answer, aligning it with `shellliveness.unknownMarkers`. Two-line diff, fixes the exact incident. Rejected as a complete fix: it does nothing about the restarted-server case (server up, different sessions, discovery succeeds), nothing about writers 2 and 3, and it leaves the file still capable of being emptied by any future caller that gets `running` wrong. It is correct as far as it goes and is folded into the recommendation as a consistency fix, not as the fix.

**2. Track tmux server incarnation; invalidate all liveness when it changes.** Give the server an identity beyond its socket path, carry that identity alongside every liveness observation and verdict, and treat a change of incarnation as invalidating every prior "I saw this alive." Fixes the whole class, at all three writers, for both server-death and server-restart. Recommended.

**3. Bulk-reap circuit breaker.** Refuse any pass that would remove more than K records, or more than some fraction of the file. Cheap, and catches causes nobody anticipated. Rejected as the primary fix: it is a magic number defending a path that should not exist, and it would equally block a legitimate mass cleanup. Once the startup path cannot shrink the file at all, the breaker has nothing left to guard.

**4. Snapshot `shells.json.<timestamp>.bak` before any destructive rewrite.** Rejected as the primary fix, for the reason the brief states: it softens the destructive path instead of removing it, and a backup nobody knows to look for is not recovery. It also implies a retention policy and a cleanup job for a directory the user never opens. Subsumed by option 6, which is reversible through a supported command rather than through archaeology.

**5. Make the startup reconciliation strictly additive.** Delete the prune branch (`shell_startup.go:261-264`) and `saveLocked`/`Save` with it, so the startup path can only ever add definitions and update fields on existing ones — `EnsureShells` semantics (`shell_manifest.go:228`). After this, *no code path anywhere can write a manifest with fewer entries than it read*, except the two single-identity removal functions. Recommended, and it is the piece that makes the failure structurally impossible rather than merely unlikely. Cost: a shell that exited while Sidecar was not running is no longer swept up automatically; it becomes an offline row. That cost is real and is paid for by option 7.

**6. Tombstones instead of hard deletion, plus a restore path.** Single-identity removals move the record to a `tombstones` array with a `deletedAt`, retained for a bounded window, rather than dropping it. Recommended as defense in depth: options 2 and 5 remove the known routes to loss, and a tombstone makes the *unknown* routes non-fatal. It is a different mechanism from a `.bak` file in the way that matters — the record stays inside the format the app already reads and writes, under the same lock, addressable by a supported command.

**7. Explicit prune, interactive and non-interactive.** Since option 5 removes the automatic sweep, the user needs a deliberate way to forget an offline shell. Interactive already exists (`ViewModeConfirmDeleteShell`, `internal/plugins/workspace/keys.go:37,316`). The non-interactive counterpart does not: `workspaceops.ForgetManagedShell` (`internal/workspaceops/shell.go:121`) is the operation, and no CLI command reaches it. Sidecar owns shell records outright — they vanish if Sidecar is uninstalled — so per `/Users/marcus/.claude/CLAUDE.md` §2 this capability is owed a non-interactive path. Recommended.

**8. Do nothing automatic; never remove a record without a user saying so.** The purest version of option 5, extended to writers 2 and 3. Rejected: typing `exit` in a shell is the common case, and an offline row accruing for every exited shell is a real UX regression that td-6a4100 already solved. Auto-close is worth keeping — under option 2's discipline, where it is scoped to one continuously-observed server.

## Recommended design

Four parts, in dependency order. Parts A and B are the fix; C and D are what make it safe to live with and what make any residual bug survivable.

### A. Server incarnation as a first-class identity

Add `internal/tmuxserver` (or extend `internal/tmuxenv`, which already owns "which server do I talk to") with an opaque comparable `Incarnation`. Two candidate sources, and the design should use both:

- **Free, no subprocess:** `os.Stat(tmuxenv.SocketPath())` → `(inode, ctime)`. A tmux server unlinks and rebinds its socket on start, so a restart changes the inode. Costs one `stat`, which matters: `AGENTS.md` §Startup Latency forbids subprocess spawns on the pre-first-frame path, and this must be readable there.
- **Authoritative, already paid for:** `#{pid}` is a server-scoped format, so the existing discovery call can become `tmux list-sessions -F '#{session_name} #{pid}'` and return the server PID in the *same* invocation — zero additional spawns. **Confirm this format resolves in a `list-sessions` format string before relying on it** (see Open questions).

Do **not** use `#{session_id}`: session ids restart from `$0` on a new server, so they are not a cross-server identity and would read as "the same session came back."

Three incarnation states must be distinguishable, and this is the whole point of the type: `Present(id)`, `Absent` (tmux answered "no server running" — a fact about the server, not about any shell), and `Unknown` (we could not ask). Any transition between distinct states invalidates prior liveness.

Then thread it through `shellliveness`:

- `Tracker.ObserveServer(inc Incarnation)` — when the observed incarnation differs from the tracked one, clear `seenAlive` on every entry and reset every `gone` count. The `SeenAlive` gate (`liveness.go:268,284`) then does the rest of the work with no other change: after a restart no shell has been seen alive *under the current server*, so `ShouldProbe` returns false, no probe is ever taken, and `Confirm` can never fire. Shells that really are still there get re-observed by the next discovery pass and re-enter `seenAlive` legitimately.
- `Verdict` results carry the incarnation they were taken under; `Confirm` refuses a verdict whose server incarnation is not the current one, exactly as it already refuses one whose *name* incarnation moved (`liveness.go:328-334`).

This is a small change with large reach: it fixes writers 2 and 3 at their shared decision point rather than at each call site, and it is the same argument the package already makes one level down, which should make it read as obvious in review.

Keep it all in `internal/shellliveness` and `internal/tmuxserver` — state-free, no `tea.Cmd`, no plugin state — per `/Users/marcus/.claude/CLAUDE.md` §2's rule about resolution logic living where a headless caller could adopt it unchanged.

### B. The startup path becomes additive-only

In `reconcileShellStartup` (`shell_startup.go:206`):

- Delete the prune branch (`:261-264`). A definition that is not in `running` is retained and rendered offline — which is already what happens when `discoveryFailed` is true, so the offline-row rendering path (`:313-324`, `shellSessionFromDefinition`, `mergeShellState`'s visibility split at `shell_merge.go:79-118`) needs no change.
- Replace `manifest.Save()` (`:301`) with `manifest.EnsureShells(...)` plus a field-update merge for the namespace/workdir/display-name backfills at `:234-241` and `:285-297`. All of those are additive or in-place edits; none needs to shrink the list.
- Delete `Save()` and `saveLocked()` (`shell_manifest.go:96,106`) entirely. This is the load-bearing step. With them gone, the only functions in the codebase that can produce a shorter list are `RemoveShell` (`shell_manifest.go:255`), `shellstate.RemoveAtPath` (`shellstate.go:158`) and `shellstate.RemoveIfUnchangedAtPath` (`shellstate.go:187`) — all single-identity, all under the exclusive lock, all reachable only from an explicit close or a confirmed per-session death. "Empty the file" stops being an expressible operation.
- Fix `discoverTmuxSessionNamesForWorkDir` (`shell.go:255-263`) to stop trusting the "no server running" empty answer, and to report the incarnation. Its return becomes something like `(names []string, inc tmuxserver.Incarnation, err error)` with `Absent` where it currently returns `(nil, nil)`. Update the doc comment at `:247-254`, which currently argues for the behavior being removed — leaving a comment that defends the bug is how the bug comes back.

Also align `internal/overview`'s guard while in the area. `reapDeadShells` (`overview/shell_liveness.go:81`) skips on `len(m.currentPanes) == 0` with a comment that names this exact scenario, and its own reasoning ("a guard that only works because the last line of defence holds is not a guard") is the right instinct — but it only covers a server that is *down*, not one that came back with other panes. Incarnation gating replaces the heuristic with the real thing; keep the empty-inventory skip as a cheap belt anyway.

### C. Explicit removal, both surfaces

- Keep the interactive delete (`keys.go:316`, `executeShellDelete`) as is.
- Add `sidecar shell list [--json]`, `sidecar shell forget <tmux-name>` and `sidecar shell restore <tmux-name>` to `internal/cli/registry.go` (the command tree at `RootCommand`, `registry.go:13`), routing to `workspaceops.ForgetManagedShell` / `shellstate` and a new `RestoreAtPath`. Follow the `shell name` / `shell rename` shape (`registry.go:42,71`, `cli.go:141,190`): `--json` structured output, exit 0/1/2, an `Agent` doc block, no interactive prompt.
- Not owed: anything about *running* the shell. `sidecar create shell` already exists, and killing a tmux session is tmux's job. This is only the record, which is Sidecar's.

### D. Tombstones and the schema version

- Move single-identity removals to `deletedAt` tombstones: the definition moves from `shells` into a `tombstones` array, retained for a bounded window (14 days is a reasonable default; make it config, not a constant buried in a writer). `sidecar shell restore` moves it back.
- Bump `manifestVersion` to 2 and, for the first time, *read* it. Two rules: a reader that sees a version it does not understand refuses to write the file rather than silently rewriting it without the fields it dropped; a reader that sees v1 upgrades in place on first write. This is a small amount of work that turns a decorative field into the forward-compatibility guard it was presumably always meant to be — and it is the same failure mode as the one this plan is about, one writer destroying what it does not understand.
- Accept and document the degradation: an older binary reading a v2 file ignores `tombstones` and drops the key on its next write. That is no worse than today. Say it in the code comment rather than discovering it later.

## Status (2026-08-24)

Steps 1-6 and the tombstone half of step 7 landed on `strong-tmux`: `internal/tmuxserver`, the tracker's `ObserveServer`/server-tagged `Confirm`, both surfaces' bindings, the additive startup path (`Save`/`saveLocked` are gone), `sidecar shell list|forget|restore`, and `deletedAt` tombstones with the writer-boundary shrink test and the isolated `kill-server` end-to-end proof.

Still open, both from part D:

- **Schema version 2 and the refuse-to-write-unknown-version guard.** `manifestVersion` is still written and never read. The tombstone doc comments in `shellstate.go` and `shell_manifest.go` point here.
- **Bounded tombstone retention.** Tombstones currently accumulate without expiry, so a long-lived project's `shells.json` grows by one record per shell ever forgotten. Retention must be config-backed, not a constant in a writer (see Open questions).

## Work sequence

1. **Regression test first, before any fix.** In `internal/plugins/workspace`, a table-driven test over `reconcileShellStartup` covering: (a) discovery returns `Absent`, (b) discovery returns a non-empty listing from a different server incarnation, (c) discovery errors. All three must leave every definition in the manifest. Case (b) fails against `main` today and is the one that proves the fix goes past the incident. These use the `shellStartupHooks` seams (`shell_startup.go:33`) and need no tmux at all.
2. **`internal/tmuxserver`**: the `Incarnation` type, the socket-stat source, the format-string source, and the three-state `Present`/`Absent`/`Unknown` distinction. Pure, table-testable, no tmux required for the unit tests.
3. **`internal/shellliveness`**: `ObserveServer`, incarnation-tagged verdicts, `Confirm` refusal on a stale server incarnation. Pure; extends the existing `liveness_test.go`.
4. **Bind the tracker to the incarnation** at both surfaces' single binding files — `internal/plugins/workspace/shell_liveness.go` and `internal/overview/shell_liveness.go`. Per `AGENTS.md` §Project and global workspace parity, if this lands in one and not the other it is a bug; the shared decision stays in `shellliveness`.
5. **Additive startup** (part B). Delete the prune branch, delete `Save`/`saveLocked`, fix discovery's return, rewrite the doc comments that currently justify the old behavior. Step 1's tests go green here.
6. **CLI surface** (part C): `shell list` / `shell forget` / `shell restore`, with `parityscan`/`cli_test.go`-style coverage of help text and exit codes.
7. **Tombstones and version 2** (part D), including the refuse-to-write-unknown-version guard and its test.
8. **Recovery note for the incident.** The five emptied manifests are not recoverable by this plan — the data is gone. Worth a line in `docs/plans/active/tmux-crash-recovery-2026-08-22.md` pointing at this plan, and a td issue closing the loop that document already asked for ("This is worth a td issue — the reaper destroys the exact metadata needed to rebuild the shells it is reaping").

Steps 2-4 and 5 are independently landable; 5 alone closes the incident, 2-4 close the class. Prefer landing 1 and 5 together first, since that is the smallest change that stops the bleeding, then 2-4.

## Testing plan

**Isolation is non-negotiable and both axes are required.** `AGENTS.md` is explicit that a private tmux socket alone is how td-8d18de destroyed six live shells; the state tree must be isolated too. The precedent to copy is `internal/plugins/workspace/tmux_isolation_test.go:32`, whose `TestMain` isolates tmux via `testenv.IsolateTmux()` (`internal/testenv/tmux.go:57`) *and* points `XDG_STATE_HOME`/config at the same temp tree. `internal/config.IsolationAsserted()` (`internal/config/isolation.go:53`) is on by default in test binaries, so `AssertIsolatedPath` (`isolation.go:70`) turns any path that escapes back to `~/.local/state/sidecar` into a hard error rather than a silent write — every manifest writer already funnels through it (`shell_manifest.go:59,111,143`; `shellstate.go:204,439`). Any new test package that touches tmux needs its own `TestMain`.

**Simulating server death without going near the real server.** On the private socket only:

1. `tmux -S <private-socket> new-session -d -s <name>` for each fixture shell; record the incarnation.
2. `tmux -S <private-socket> kill-server` — this is the crash. Note that `testenv.teardownFor` (`testenv/tmux.go:85`) already kills by explicit `-S <socket>` path for exactly this reason: a bare `tmux kill-server` trusts the ambient environment and is one disturbed variable away from destroying the developer's server. New tests must do the same and must never issue a bare `kill-server`.
3. For the restart case, start a fresh server on the *same* socket with *different* session names. This is the case that reproduces the second route to the bug and that no current guard catches.
4. Assert: `shells.json` still contains every record, byte-for-byte on the definition fields; the rows render as `IsOrphaned`; and `recreateOrphanedShell` on one of them restores its display name, agent type and skip-perms.

Most of the coverage should not need tmux at all. `reconcileShellStartup` takes `shellStartupHooks` (`shell_startup.go:33`) with injectable `discoverSessions`, `loadManifest`, `namespace` and `now`; `shellLivenessProbe` is a package var indirected for tests in both surfaces (`workspace/shell_liveness.go:16`, `overview/shell_liveness.go:25`). Push the incarnation logic into pure functions and test it there; reserve the real-tmux tests for the end-to-end proof that the plumbing is connected.

**An invariant test worth writing once.** A test that asserts no manifest write ever reduces the entry count except through the three single-identity removal functions. A `grep`-style source assertion is crude; a better version wraps the writer boundary and fails on a shrink. This is the test that keeps the structural property structural after someone adds a fourth writer.

**Headless proof.** `./scripts/tmux-drive.sh paths` first, confirm nothing resolves under `~/.local/state/sidecar` or `~/.config/sidecar`, then a start/keys/snap run showing offline rows after a simulated server loss and a successful recreate. `./scripts/tmux-drive.sh stop` on completion or error.

## Acceptance evidence

- The pre-fix regression test (case (b), restarted server with different sessions) is demonstrated failing on `main` and passing after step 5. Both outputs recorded.
- After a private-socket `kill-server` and a Sidecar restart against an isolated state tree, `shells.json` is byte-identical on all definition fields; screenshot or capture of the sidebar showing offline rows; a recreate from one of those rows brings back the display name, agent type and skip-perms.
- `grep -rn 'saveLocked\|func (m \*ShellManifest) Save' internal/` returns nothing.
- `sidecar shell list --json`, `sidecar shell forget <name>`, `sidecar shell restore <name>` each demonstrated non-interactively with exit codes, including the not-found and already-in-that-state cases.
- A v2 manifest with tombstones survives a full app lifecycle; a synthetic v3 file causes a refusal to write, not a silent downgrade.
- `go build ./...`, `go test ./...`, `GOOS=linux GOWORK=off golangci-lint run ./...` clean.
- `SIDECAR_STARTUP_TRACE=stderr` before/after within noise. Part A adds at most one `stat` to the startup path and no subprocess; if the trace disagrees, the format-string source is being called somewhere it should not be.
- `./scripts/tmux-drive.sh paths` output pasted, showing isolation on both axes, for every proof run.

## Non-goals

- Recovering the five manifests emptied on 2026-08-22. The data is gone; this plan prevents the next occurrence.
- Any change to how tmux sessions themselves are created, killed, or attached. Sidecar does not own tmux, and per `/Users/marcus/.claude/CLAUDE.md` §2 it owes no CLI for capabilities that belong to something underneath it. The scope here is exactly the record, which Sidecar does own.
- A general backup/restore system for the whole state tree. Tombstones cover the one file that holds unreconstructible data.
- The `internal/migration` legacy-path move (`internal/migration/migration.go`). Untouched; part D's version handling is schema evolution, a different concern that happens to use the same word.
- Reconsidering the socket-path definition of `tmuxenv.Namespace()`. It is correct for what it identifies (`tmuxenv.go:11-16` gives the reasoning, and `TestNamespaceIsTheSocketPath` pins it). Incarnation is a *new, separate* identity, not a replacement.
- `recreateOrphanedShell` passing `p.ctx.WorkDir` rather than the definition's own `WorkDir` (`internal/plugins/workspace/shell.go:632`). Almost certainly a latent bug for a shell whose record names a different worktree, but it is orthogonal and deserves its own issue.

## Open questions

- **Does `#{pid}` resolve inside a `list-sessions` format string?** It is a server-scoped format and should, which would give the incarnation for free in a call already being made. Verify on an isolated socket before designing around it; the socket-stat source is the fallback and is sufficient on its own.
- **Does every tmux version in the supported range unlink and rebind the socket on restart**, giving a new inode? If a version reuses the file, the stat source degrades to "no signal" — which must be represented as `Unknown`, not as "same incarnation." Design the type so that is the safe default.
- **Sidecar instances that run outside tmux** survive a server restart and would observe the transition live rather than at startup. That is the case part A is aimed at, and it is worth confirming the tracker reset fires on the transition rather than only at construction.
- **Tombstone retention default.** 14 days is a guess. It should be config-backed either way, and the number matters less than the fact that it is not hardcoded in a writer.
