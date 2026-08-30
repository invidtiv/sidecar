# Sidecar remote hosts Phase C evidence

Date: 2026-08-29 (America/Los_Angeles)

Candidate: `17192b80f3035c7fa7544d36c362c877a2c623b3` on `remote-phase-3`

Task: `td-677dde`

## Outcome

Five of the seven journeys passed cleanly. One passed with a real defect attached. One journey exposed a blocker that stops the Phase C feature working at all on a host project that has never been opened on that host.

| # | Journey | Result |
| --- | --- | --- |
| 1 | Create a remote shell (`ctrl+n`) | Pass |
| 2 | Create a remote worktree (`n`), with plan confirmation | Pass |
| 3 | Start an agent in a remote shell | Pass |
| 4 | Rename a remote shell and a remote worktree (`R`) | Pass |
| 5 | Refusals: delete, merge, navigate, `O` | Pass |
| 6 | Failure is actionable | Partial — detected and bounded, but the actionable half of the message never reaches the user |
| 7 | Rollback with the flag off | Pass |

The blocker is finding 1 below. Every journey from 1 to 4 only ran after the remote project was registered in the host's state tree by hand; the documented path — register a host, open Sessions, create a shell — fails on a fresh host.

Both machines were untouched. The remote baseline is byte-identical before and after. The local baseline changed only in files written by the user's own live Sidecar instances, which were running throughout and are not attributable to this run.

## Isolation

Both required axes on both machines, verified before any mutation ran.

| Axis | Local viewer | Remote host |
| --- | --- | --- |
| Machine | `aerie` | `marcusbook` (`MarcusBook-Pro.local`) |
| Sidecar binary | `…/scratchpad/bin/sidecar-phase-c` | `/private/tmp/sidecar-phase-c-proof/sidecar` |
| Reported build | `v1.10.1-0.20260830052912-17192b80f303` | same binary, sha1 `006255d5c1c005b5db0cffaee1ba8d15e4a2e39a` on both sides |
| State root | `/private/tmp/sidecar-drive-phase-c-501/state` | `/private/tmp/sidecar-phase-c-proof/state` |
| Cache root | `/private/tmp/sidecar-drive-phase-c-501/cache` | `/private/tmp/sidecar-phase-c-proof/cache` |
| Config | `/private/tmp/sidecar-drive-phase-c-501/config/config.json` | `/private/tmp/sidecar-phase-c-proof/config/config.json` |
| Private tmux | `/private/tmp/sidecar-drive-phase-c-501/tmux/tmux-501/default`, plus outer `-L sidecar-drive` | `/private/tmp/sidecar-phase-c-proof/tmux/tmux-501/default` |
| Isolation assertion | `SIDECAR_ISOLATED_STATE=1` | `SIDECAR_ISOLATED_STATE=1` |

`./scripts/tmux-drive.sh paths` was checked first and resolved nothing under `~/.local/state/sidecar` or `~/.config/sidecar`.

The user's installed sidecar on `marcusbook` was never replaced. It reported `devel+main.f6bc3e7f` with sha1 `4a40bceb7fa5f688699391feb9a1c39a07e5b6de` before the run and reported exactly that afterward. The Phase C binary was copied to a scratch path and reached only through the host registration's `binary` field.

### Remote isolation, proven three ways

**On the wire.** The live ssh invocation carried every lever, so no part of remote isolation depended on trusting the config file:

```
ssh -T -o ControlMaster=auto -o ControlPath=/tmp/sc-hosts-2137163544/marcusbook/ctl
    -o ControlPersist=300 -o ServerAliveInterval=15 -o ServerAliveCountMax=4
    -o BatchMode=yes marcusbook
    $SHELL -l -c 'SIDECAR_ISOLATED_STATE=1
                  XDG_STATE_HOME=/private/tmp/sidecar-phase-c-proof/state
                  XDG_CACHE_HOME=/private/tmp/sidecar-phase-c-proof/cache
                  TMUX_TMPDIR=/private/tmp/sidecar-phase-c-proof/tmux
                  /private/tmp/sidecar-phase-c-proof/sidecar
                  -config /private/tmp/sidecar-phase-c-proof/config/config.json
                  host serve --stdio'
```

**In the host's own hello.** The serve stream's first message reported the resolved remote state directory, which is the host telling the viewer where it actually landed rather than the viewer assuming:

```json
"capabilities":{"processIdentity":true,"isolatedState":true,
                "stateDir":"/private/tmp/sidecar-phase-c-proof/state/sidecar"}
```

**Fail-closed.** With `SIDECAR_ISOLATED_STATE=1` and no `XDG_STATE_HOME`, the remote binary refused rather than touching the real tree:

```
SIDECAR_ISOLATED_STATE=1 asserts isolated state, but
/Users/marcusvorwaller/.local/state/sidecar resolves inside the real user directory
/Users/marcusvorwaller/.local/state/sidecar; export XDG_STATE_HOME and pass -config to a temp dir
```

The remote private tmux server was a distinct server, not a distinct name on the same one: socket inode `11720393963`, pid `40464`, against the default server's inode `11693064406`, pid `55792`.

### Baselines

| Measure | Local before | Local after | Remote before | Remote after |
| --- | --- | --- | --- | --- |
| Default tmux sessions | 39 | 41 | 2 | 2 |
| Default tmux socket inode | 262294784 | 262294784 | 11693064406 | 11693064406 |
| Default tmux server pid | 94101 | 94101 | 55792 | 55792 |
| Sidecar state files | 1927 | 1927 | 489 | 489 |
| State manifest sha1 | `d8a83ed1…` | `13ab0d10…` | `157fdcc9…` | `157fdcc9…` |

**Remote: byte-identical.** Session list, socket inode, server pid, file count, and the checksum manifest over all 489 real state files are unchanged. The scratch tree and its private tmux server are gone.

**Local: two differences, neither from this run.**

*Two new default-server tmux sessions* — `sidecar-sh-sidecar-5` (22:52:03) and `sidecar-sh-sidecar-pane-repositioning-1` (22:51:36). Their panes are rooted at `/Users/marcus/code/sidecar` and `/Users/marcus/code/sidecar-pane-repositioning`, the user's real projects. This run's Sidecar had `projects.list: []` and a tmux socket inside its run dir, so it could not have created a session on the default server or in those directories. The user has three live Sidecar instances (pids 6807, 45559, and 16032 under default tmux 94101), all started before this run. Decisively: `~/.local/state/sidecar/projects/sidecar/shells.json` is byte-identical before and after (`7abaeb1c…`), so no shell record was written to the real tree — which a create from this run would have required.

*One changed state file* — `~/.local/state/sidecar/agent-activity.json`. The isolated run wrote its own copy at `/private/tmp/sidecar-drive-phase-c-501/state/sidecar/agent-activity.json`; the real one is written continuously by the user's live instances.

A pre-existing ssh from the user's own Sidecar was talking to `marcusbook` throughout this run, using the installed remote binary and the real remote state tree (`ssh … marcusbook $SHELL -l -c 'sidecar host serve --stdio'`, no env, no `-config`, parent tmux 94101). It was left alone. That the remote real state manifest is nonetheless unchanged is worth noting, but the credit is not this run's.

### Teardown

The remote scratch tree, its private tmux server, and its scratch processes were removed; the default server still holds its original two sessions. Both local tmux servers created by `tmux-drive.sh` are gone. The three ssh ControlMasters this run created were exited explicitly and their `/tmp/sc-hosts-*` directories removed; the user's pre-existing ones were not touched. Repository `.sidecar` directories are unchanged.

## Journeys

### 1. Create a remote shell — pass

`ctrl+n` listed `marcusbook · remoteproj` in the project picker alongside local projects. On confirm, the host created the shell and the row arrived with the next serve snapshot rather than being synthesized locally: the LIVE count went 1 → 2 and `◎ ❯ marcusbook · remoteproj Shell 1` appeared.

Verified on `marcusbook`, in the scratch tree only:

- tmux session `sidecar-sh-remoteproj-1` on the private socket
- `…/state/sidecar/projects/remoteproj/shells.json` gained a record whose own `namespace` field names the private socket: `/private/tmp/sidecar-phase-c-proof/tmux/tmux-501/default`
- the default tmux server still held its original two sessions

Capture: `11-j1-shell-created`, `12-j1-row`.

### 2. Create a remote worktree — pass

`n` resolved the plan on the host and showed it before anything was created:

```
Confirm Worktree

On marcusbook

Create phase-c-wt at
/private/tmp/sidecar-phase-c-proof/proj/remot…

From refs/heads/main (a59eb32d)
local branch only; no remote push
Runs .sidecar/worktree-setup.sh — optional
```

Branch, path, source ref with OID, remote policy, and the setup-hook line are all present. `a59eb32d` is the remote repository's actual HEAD. The hook line reflects the remote project's own config (`hookPath: .sidecar/worktree-setup.sh`, `hookRequired: false` → "optional"), so it is read from the host, not assumed.

On confirm, verified on `marcusbook`:

- `git worktree list` shows `/private/tmp/sidecar-phase-c-proof/proj/remoteproj-phase-c-wt  a59eb32 [phase-c-wt]`
- branch `phase-c-wt` exists
- the hook ran: `.setup-hook-ran` in the new worktree contains `phase-c setup hook ran in /private/tmp/sidecar-phase-c-proof/proj/remoteproj-phase-c-wt`
- tmux session `sidecar-ws-remoteproj-phase-c-wt` on the private socket
- the default tmux server unchanged

The row appeared locally as `◆ ⑂ marcusbook · remoteproj phase-c-wt` with the agent badge `◆ claude phase-c-wt`.

Minor: the path line is truncated in the modal at 200 columns.

Captures: `17-j2-plan`, `18-j2-created`, `19-j2-row`.

### 3. Start an agent in a remote shell — pass

`ctrl+n` with Agent = Claude Code created `sidecar-sh-remoteproj-2` and seeded it with `shell send --run`. The command reached the pane: captured directly from the remote private socket, not from the viewer's preview.

```
$ TMUX_TMPDIR=/private/tmp/sidecar-phase-c-proof/tmux tmux capture-pane -p -t sidecar-sh-remoteproj-2

 Accessing workspace:
 /private/tmp/sidecar-phase-c-proof/proj/remoteproj
 Quick safety check: Is this a project you created or one you trust? …
 ❯ 1. Yes, I trust this folder
   2. No, exit
```

`claude` is running in the remote pane and has resolved the remote workspace path. The same was true for the worktree session from journey 2.

### 4. Rename a remote shell and a remote worktree — pass

Shell: `R` on `Shell 1` → "renamed shell alpha". The remote `shells.json` record's `displayName` became `renamed shell alpha`; the local row updated.

Worktree: `R` on `phase-c-wt` → "renamed worktree beta". The remote display-name file
`…/projects/remoteproj/worktrees/remoteproj-phase-c-wt-04f5254cabc7/display-name` contains `renamed worktree beta`; the local row updated. The branch badge still reads `phase-c-wt`, which is correct — the rename changes Sidecar's display name, not the branch, path, or tmux session.

Captures: `26-j4-shell-renamed`, `30-j4-wt-renamed`.

### 5. Refusals — pass

All four refuse on a remote row and name the machine.

| Key | Action | Message |
| --- | --- | --- |
| `D` | delete | `renamed worktree beta is on marcusbook — Sidecar can watch and change a remote workspace…` |
| `m` | merge | `Overview item is stale: renamed worktree beta lives on marcusbook; Sidecar can watch…` |
| double-click | navigate | `renamed worktree beta lives on marcusbook; Sidecar can watch…` |
| `O` | open in Git | `renamed worktree beta is on marcusbook — Sidecar can watch and change a remote workspace…` |

`O` is the newly fixed one and it refuses. Before this change it would have handed a remote path to a local `SwitchWorktree`, which on two machines with the same layout opens the wrong repository silently rather than failing.

Navigation has no keyboard binding in `global-workspaces`; double-click is the only route, so it was driven with real SGR mouse events. The merge path reaches the identical guard (`RequestNavigationAction` → `refuseRemoteAction`), so the rule is covered twice.

Captures: `31-refuse-delete`, `32-refuse-merge`, `35-refuse-navigate`, `34-refuse-opengit`.

### 6. Failure is actionable — partial

**Unreachable host: good.** With the host pointed at a nonexistent remote binary, the Sessions browser showed a health row carrying the remote's own words and a fix, promptly and without hanging:

```
○ PAUSED (1)
 ⚠ marcusbook marcusbook
   zsh:1: no such file or directory: /private/tmp/sidecar-phase-c-proof/s…

marcusbook — unreachable
zsh:1: no such file or directory: /private/tmp/sidecar-pha…
check the machine is on and `ssh <target>` works from here
```

**Failed mutation: not actionable.** Creating a worktree whose branch already exists produced only:

```
Error: the remote Sidecar did not accept this…
```

The host says `branch "phase-c-wt" already exists` (exit 2), and `internal/hosts` correctly builds `the remote Sidecar did not accept this command: branch "phase-c-wt" already exists`. The create modal truncates that at its box width, discarding the entire half that says what went wrong. Nothing is written to the local debug log either, so the full text cannot be recovered from inside the app. The user is told a failure happened and not what it was.

**Not a failure mode:** stopping the remote scratch tmux server does not break a mutation. tmux starts on demand, so the create simply succeeded on a fresh server.

Captures: `42-j6-badbin-sessions`, `48-j6-dup-error`.

### 7. Rollback — pass

Run as a paired control with the same config, the same binary, and the same host entry left registered; only `features.sidecar_remote_hosts` differed.

| | Flag off | Flag on |
| --- | --- | --- |
| Sessions list | `no sessions` | host rows present |
| New `/tmp/sc-hosts-*` dir after opening Sessions | none | one |
| ssh process | none | `ssh … marcusbook … host serve --stdio` |

`SIDECAR_STARTUP_TRACE=stderr` with the flag off shows no host phase anywhere before the first ready frame at 67.353 ms:

```
    10.8ms  config.Load                     91µs
  10.893ms  state.Init                     102µs
  11.011ms  app.GetMainWorktreePath     13.484ms
  25.368ms  theme.Resolve+Apply             70µs
  25.624ms  plugin.Init:td-monitor      12.496ms
  38.402ms  app.New                     13.318ms
  51.755ms  tea.Program.Run                (mark)
  52.878ms  first frame (loading)          (mark)
  67.353ms  first ready frame              (mark)
```

The host connection is lazy in both states: nothing host-shaped runs at startup even with the flag on. The discriminator is opening Sessions, which is where the flag-off run still produced no ssh and no control directory.

## Findings

**1. A host project that has never been opened on the host cannot be mutated. Blocker.**

`host serve` reports the host's projects from its `config.projects.list`, so the create picker offers them. But `sidecar create shell --project` and every other targeted verb resolve `--project` through `matchProject` → `loadRegisteredProjects(stateDir)`, which reads only `$STATE/sidecar/projects/<slug>` directories on disk. A configured project that no Sidecar run has ever opened has no such directory, so the verb refuses:

```
unknown project "/private/tmp/sidecar-phase-c-proof/proj/remoteproj"
```

Reproduced with a configured project and an empty state tree, independent of symlinks. The viewer is doing the right thing — `remoteProjectRef` hands back the host's own reported root, opaque and unresolved locally — and the host's own CLI rejects it. Journeys 1 through 4 only ran after the project was registered by launching the remote Sidecar TUI once by hand.

The user-visible shape is worse than a plain error: the create modal offers a project and then fails with truncated text (finding 2), so the reason never surfaces.

**2. A failed remote mutation loses its message.** Covered in journey 6. The classification and text in `internal/hosts` are right; the create modal truncates them and nothing logs the full string.

**3. The merge and navigate refusal is labelled "Overview item is stale".** It routes through `ValidationMsg`, whose surface prefixes it that way. The row is not stale, and the heading contradicts the sentence under it.

**4. Refusal toasts truncate.** All four refusals end in `…`, cutting the second clause. The machine name survives, which is the part that matters most, but the guidance does not.

**5. `internal/hosts` control directories escape the isolated run dir.** They are created at `/tmp/sc-hosts-<n>/`, not under the state tree or the run dir, so `tmux-drive.sh stop` does not clean them and a proof run leaves artifacts outside its own tree. Cleaned by hand here.

**6. A hard kill leaves the ssh ControlMaster alive.** `tmux-drive.sh stop` kills the session rather than quitting Sidecar, so `Transport.Close()` never runs and the master lingers until `ControlPersist` expires. This is a property of the teardown method, not demonstrated to be a product defect; Phase B proved explicit quit leaves nothing behind.

**7. A symlinked project path produces duplicate state project directories.** With the scratch tree under `/tmp` (a symlink to `/private/tmp` on macOS), one Sidecar launch created both `remoteproj` (path `/tmp/…`, from config) and `remoteproj-2` (path `/private/tmp/…`, from cwd). Both then canonicalize to the same path, which makes `matchProject` ambiguous and would refuse with "matches more than one Sidecar project". Avoided by moving the whole proof to `/private/tmp`.

**8. External edits to `config.json` are not picked up by a running Sidecar.** Changing the host's `binary` while running produced no reload and no log line; a restart was required. Config saves made from inside the app do reconcile hosts.

**9. `ctrl+n` opens nothing when the only project source is an unreachable host.** No modal, no explanation.

**10. An isolated run still reads a real repository file.** The local run logged `migration: migrated legacy file src=/Users/marcus/code/sidecar/.sidecar/shells.json dst=/private/tmp/sidecar-drive-phase-c-501/state/…`. The source was copied, not modified — its mtime is still 24 Jul and its checksum is unchanged — so no damage, but `SIDECAR_ISOLATED_STATE=1` does not stop a read of the developer's repository state. Separately, the project root resolved to the main checkout `/Users/marcus/code/sidecar` rather than the worktree the run was launched in.

## What this does not prove

- **A first-run remote host works.** Finding 1 means it does not. Journeys 1 to 4 were proved against a host project registered by hand first.
- **The pre-fix `O` bug.** The old binary was not run, so the refusal was proved but the behaviour it replaced was not observed.
- **More than one host, and host churn.** One host, registered once. No second host, no host removed or retargeted mid-mutation, so the `hostReplyStale` fences and `remoteReplyDropped` were exercised by neither this proof nor any live path.
- **Concurrent mutations,** or a mutation racing a serve snapshot.
- **A host disconnecting mid-mutation.** The unreachable-host case was proved at connect time, not with a link dropped during a create.
- **Timeout behaviour.** No verb was made to exceed `remoteQuickTimeout`, `remoteCreateShellTimeout`, or `remoteWorktreeExecTimeout`.
- **Remote delete and remote merge.** Refused by design; not implemented, so nothing to prove beyond the refusal.
- **An agent doing work remotely.** `claude` reached its trust prompt in two remote panes. It was not answered and no task was run.
- **A host with a real, non-isolated state tree.** Everything remote ran against a scratch tree, which is the point, but it means the interaction with a populated real host was not exercised.
- **A remote project with a required setup hook, or a hook that fails.** Only `hookRequired: false` on a hook that succeeds.
- **A pre-run baseline of repository `.sidecar` directories.** Finding 10 was discovered mid-run; the checksum comparison starts from a mid-run capture, and the unchanged July mtime is the evidence that nothing was written.
- **The two new local tmux sessions with certainty.** The attribution to the user's own Sidecar is strong — wrong directories, wrong socket, real `shells.json` unchanged — but they were not observed being created.

## Retained artifacts

Text and PNG captures for every journey are under `/private/tmp/sidecar-drive-phase-c-501/out/`, numbered in run order (`00-startup` through `51-j7-flagon-contrast`). The remote scratch tree was removed and holds nothing.
