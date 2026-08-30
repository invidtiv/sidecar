---
sidebar_position: 8
title: Remote Hosts
---

# Remote Hosts

Watch and drive Sidecar sessions on another machine, over SSH, from the Sidecar in front of you.

## What this is

Sidecar runs on both machines. The local one spawns `sidecar host serve --stdio` on the remote one through your ordinary `ssh`, reads a stream of JSON snapshots back over that pipe, and shows the remote machine's shells, worktrees and agents in the Sessions browser beside your own. Selecting a remote row opens its live tmux pane through the same connection, and you can type into it. SSH is the entire transport and the entire trust boundary: nothing binds to a network, no port is opened, and no sockets or forwarding are involved.

There is no daemon on the remote host. The serve process is spawned per connection and dies when the pipe closes, so there is nothing to install as a service, nothing to autostart, and nothing to restart after an upgrade. The remote truth lives in that machine's tmux server and its Sidecar state tree, both of which outlive any Sidecar process, which is what makes the ephemeral design work.

The feature is off by default and costs nothing until you ask for it. With the flag off, no host is read from configuration and no connection is attempted. With the flag on and no host registered, the same is true: no ssh child is ever spawned.

## Prerequisites

**`ssh <target>` already works from this machine.** Sidecar adds no second place to describe how to reach a machine. The target is whatever your `ssh_config` resolves, with your keys, your `ProxyJump` and your agent. Anything that works in `ssh <target>` works here, and anything that does not cannot be fixed from Sidecar's side.

**Sidecar is installed on the remote machine**, and its login shell can find it. The remote command runs through `$SHELL -l -c`, so a Homebrew or package-managed install is found the way it would be in a terminal there.

**tmux is installed on the remote machine.** Sidecar observes tmux sessions, so a host without tmux has nothing to show.

### The login-shell PATH problem

A non-login ssh shell has no `/opt/homebrew/bin` on `PATH`, which is why the connection is wrapped in a login shell at all. That covers the normal case. It does not cover a machine that keeps `sidecar` somewhere a login shell does not look: a scratch build, a private prefix, a directory added to `PATH` only by an interactive rc file. For those, set an explicit binary path on the host entry (`--binary` on the CLI, **Sidecar path** in the form). It is the escape hatch, not the default, and most hosts never need it.

The same wrapper is why the login shell must keep quiet on stdout. See [A login profile that prints to stdout](#a-login-profile-that-prints-to-stdout).

## Turn the feature on

Remote hosts are behind the `sidecar_remote_hosts` feature flag.

```bash
sidecar --enable-feature=sidecar_remote_hosts
```

To keep it on, set it in `~/.config/sidecar/config.json`:

```json
{
  "features": {
    "flags": {
      "sidecar_remote_hosts": true
    }
  }
}
```

In the app, press `,` for Configuration and open **Feature Flags** under System. The **Remote Hosts** page has a shortcut to that switch: when the feature is off, the page still lists and edits your registry, says at the top that nothing is being watched, and offers **F  Turn on Remote hosts**, which jumps to the flag's own control. A configuration screen that hid the thing being configured would be how a flag becomes a secret.

`sidecar host list` says the same thing in a terminal: with the flag off it prints your hosts and then a line noting that none of them are being observed.

## Register a host

The registry lives in Sidecar's own `config.json`, so it has both a UI and a CLI, and the two write through the same validation.

### From Configuration

Press `,`, then open **Remote Hosts** in the Sidecar group.

| Key | Action |
|-----|--------|
| `A` | Add host |
| `E` | Edit the host under the cursor |
| `D` | Remove the host under the cursor (asks first) |
| `enter` | Connect to this host, or switch it off, on the focused row |

Edit and Remove are painted under the row you are on, and answer to their own keys and to the mouse; they are not separate cursor stops, so moving down goes to the next machine rather than into the row's own buttons.

The form is the host entry exactly:

| Field | What it is |
|-------|------------|
| **Target** | The ssh destination, as your `ssh_config` resolves it. The one field with no useful default |
| **Name** | What the machine is called in Sidecar, and what scopes its rows. Defaults to the target |
| **Sidecar path** | An explicit path to `sidecar` on that machine. Usually unnecessary |
| **Config path** | An optional `-config` path for the remote Sidecar, to watch a machine against a config other than its user default |
| **Environment** | Space-separated `KEY=VALUE` pairs for the remote Sidecar process. See [Isolation](#isolation-and-safety) |
| **Connect to this host** | Off keeps the machine registered without connecting to it |

Nothing is written until you save, and a host saved here connects without restarting Sidecar. Removing a host is a configuration change and nothing more: nothing on that machine is touched, and its shells, worktrees and agents go on running there.

Each row shows the machine's live condition, read from the running host registry rather than probed again by the page, so the settings screen and the Sessions row cannot disagree about a machine.

### From the CLI

```bash
# Register a machine. The id defaults to the target.
sidecar host add marcusbook

# Name it, and point at a sidecar the login shell cannot find.
sidecar host add marcusbook --id book --binary /opt/homebrew/bin/sidecar

# See what is registered, and whether the feature is on.
sidecar host list
sidecar host list --json

# Switch a machine off for the week without losing its settings.
sidecar host set book --disabled
sidecar host set book --target marcusbook.local --enabled

# Stop watching it and forget the entry.
sidecar host remove book
```

`sidecar host set` leaves every field you do not name alone. `--env` replaces the whole environment list rather than appending to it, so a script that runs twice produces the same entry; pass a single empty `--env ""` to clear it, and the same for `--binary` and `--config`.

None of these verbs connects to anything. Registering a machine is a configuration change; whether it answers is reported as health by the running Sidecar, and `sidecar host probe` is how to ask that question from a terminal.

Add `--json` to any of them for a structured result. Exit codes follow the vocabulary the other `sidecar shell` verbs use: `3` means no host is registered under that name, `5` means a value was rejected (an empty target, a name already registered, a malformed `--env`).

### By hand

The same entries in `~/.config/sidecar/config.json`:

```json
{
  "hosts": {
    "list": [
      { "id": "book", "target": "marcusbook" },
      { "id": "proof", "target": "proof-host", "disabled": true }
    ]
  }
}
```

Reachability is deliberately absent from this structure. There is no user, port, key or jump-host field, because your `ssh_config` already has them.

## What a remote machine looks like

Press `8` for the Sessions browser. A registered host contributes its projects and their workspaces as ordinary rows, grouped under a `hostid · projectname` label, sorted after this machine's own projects. Remote rows carry a `⇅` glyph and are drawn in a colour derived from the host name, so a machine keeps the same colour across restarts and reads the same way in the list and on the Activity board. The glyph is provenance, not status: the gutter marker still means what the agent is doing.

Press `9` for Activity. A remote agent-backed shell lands in its lane like a local one. A plain shell with no agent does not appear there, remote or local, because that board is about agents.

Preview cells use the snapshot the host already captured as part of its own status pass, so a list of remote rows costs no extra connections. Selecting a row opens the real pane through tmux control mode over the same SSH connection, with the ordinary history, search and selection behaviour.

A host with a problem is a row rather than a silence. A machine that simply stopped appearing would be indistinguishable from one with nothing running on it, which is the question the feature exists to answer. See [Health states](#health-states-and-their-fixes).

## What you can do to a remote workspace

Every action on a remote row is one `sidecar <verb> --json` invocation that the host runs itself, so the machine that owns the state is the machine that changes it. That also means each of these is equally reachable by an agent over plain ssh.

| Action | Key in Sessions | What runs on the host |
|--------|-----------------|-----------------------|
| Create a shell, optionally with an agent | `ctrl+n` | `sidecar create shell --project … --agent …` |
| Create a worktree (plan, confirm, then execute) | `n` | `sidecar create worktree --project … --plan`, then the real create |
| Start an agent in the shell just created | part of the same create | `sidecar shell send --target … --run …` |
| Rename a shell or a worktree | `R` | `sidecar shell rename --target …` |
| Delete a shell | `D` | `sidecar shell delete --target …` |
| Type into a live pane | `enter` | tmux control mode over the same connection |

Deleting a remote shell names the host in the confirmation, because the tmux session being closed is on another machine. The host closes the session and moves the shell's record to a tombstone, so `sidecar shell restore` on that machine can bring the record back.

### What is refused, and why

Three things are refused on a remote row. None of them is an arbitrary limit; each is an operation whose implementation would run here, on this filesystem, against a path that belongs to another machine. On two machines with a similar checkout layout that does not fail cleanly, it succeeds against the wrong repository.

| Refused | Reason |
|---------|--------|
| **Delete a worktree** | Removing a checkout resolves its path against a git repository, and carries branch-cleanup decisions (delete the local branch, delete the remote branch) that the host verb has no way to express. `sidecar shell delete` refuses a worktree session on the host side too, so the two surfaces cannot disagree about what delete means |
| **Merge** | The merge workflow resolves the workspace's path against a git repository and runs `git` and `gh` here |
| **Open as a project** | Navigation switches this Sidecar's project to a checkout. There is no checkout here to switch to |

These are refused up front rather than offered and then taken back: the footer does not advertise an action the confirmation would decline. Each becomes supported the day it gains a host-side CLI verb of its own, which is exactly how shell delete arrived.

## Health states and their fixes

Every state names one thing to do about it. The Sessions row, the Configuration row and `sidecar host probe` all read the same fix line, so you are never matching two descriptions of one machine.

| State | What it means | Fix |
|-------|---------------|-----|
| `connecting` | First attempt, before anything is known. A slow host is not a broken one | Nothing. Wait |
| `online` | The stream is live and the rows are current | Nothing |
| `stale` | The connection is up but no snapshot has arrived recently. Last-known rows are still shown, marked stale | The connection is up but quiet; it will recover on its own, or remove the host to stop trying |
| `unreachable` | ssh could not connect | Check the machine is on and `ssh <target>` works from here |
| `no-sidecar` | ssh connected but no sidecar binary ran | Install Sidecar on that machine, or set its `binary` path |
| `protocol-mismatch` | Both ends ran but speak different protocol versions | Update Sidecar on whichever machine is older |
| `no-tmux` | Sidecar is there but tmux is not, so the host has no sessions to observe | Install tmux on that machine |
| `not-protocol` | Something came back that is not the protocol, almost always a login shell writing to stdout | That machine's login shell prints to stdout; send it to stderr or guard it with a non-interactive check |
| `disabled` | A registered host you have switched off | Set `disabled: false` for this host to connect to it, or turn its **Connect to this host** toggle back on |

Selecting an unhealthy host's row shows the state, whatever the host or ssh actually said, and the fix.

## Isolation and safety

### What the host writes

`sidecar host serve` has exactly one write, and it is the same one a local Sidecar makes: a shell record whose tmux session is confirmed gone is reaped. Reaping is a tombstone through the same flocked, conditional writer the Sessions browser uses, so `sidecar shell restore` on the host still brings the record back. Nothing else is written: no geometry lease, no pane resize, no mutating tmux command at all.

That reap is why a row for a shell you exited leaves the viewer's screen within a poll instead of waiting until somebody opens Sidecar on that machine. It carries every guard the local reap has, including the one that matters most: an empty tmux pane listing is never acted on, because `tmux kill-server` does not unlink its socket and a dead server is otherwise indistinguishable from a server with no sessions.

Serve also watches each configured project's `shells.json`, so a shell created on the host appears here within a coalesce window rather than on the next inventory tick.

### Pinning a proof host to its own tmux server and state tree

The **Environment** field (`--env` on the CLI) is extra environment for the remote Sidecar process. Its reason for existing is the isolation discipline: a proof run against a remote host must be able to pin that host to its own tmux server and state tree exactly as a local proof run does. Without it, isolation would stop at the machine boundary, which is the point at which it matters most, because the remote tree belongs to someone else.

```bash
sidecar host add proof-host \
  --id proof \
  --env TMUX_TMPDIR=/tmp/proof \
  --env XDG_STATE_HOME=/tmp/proof/state \
  --env SIDECAR_ISOLATED_STATE=1
```

`SIDECAR_ISOLATED_STATE=1` is the promise that the remote process must never read or write the real user's Sidecar state, and it fails closed: if a path still resolves inside the host's real state or config directory, `sidecar host serve` refuses to start rather than touching it. The refusal is armed before the collection loop begins, so a proof run that forgot to move the state tree stops before it has observed anything.

Use it for proofs and demos. An ordinary host needs no environment at all.

## Troubleshooting

### Ask the machine directly

`sidecar host probe` is the smallest possible viewer: it spawns `sidecar host serve --stdio` on a target, consumes the stream, and prints a verdict naming the fix.

```bash
sidecar host probe marcusbook
sidecar host probe marcusbook --json
sidecar host probe marcusbook --raw --cycles 3     # the JSONL, verbatim
```

A healthy answer reports the host's Sidecar version, protocol number, OS and architecture, its tmux version and whether a server is running, how many projects and workspaces it has, and how long the handshake and first snapshot took. It also notes when the host cannot do argv0 process identity, which is what makes a shared-runtime pane's agent a guess rather than a fact.

`probe` accepts `--binary`, `--remote-config` and `--env` so you can test an entry's settings before registering it.

### A login profile that prints to stdout

This is the failure to know about, because the symptom does not look like its cause. The protocol travels on the remote command's stdout. If that machine's login shell prints anything to stdout, a banner, a version notice, an `nvm` line, a fortune, that output lands in the middle of the protocol stream and corrupts it. Sidecar names this specific condition rather than surfacing a JSON syntax error: the host shows `not-protocol`, and `sidecar host probe` says the same.

The fix is on the host, in its shell profile:

- Send the output to stderr instead: `echo "..." >&2`.
- Or guard it with a non-interactive check, so it only runs for an interactive login. In bash or zsh, `[[ $- == *i* ]] || return` early in the file, or wrap the noisy section in `case $- in *i*) ... ;; esac`.

Structured log lines are the nastiest version of this, because a line of JSON can look enough like a result to be mistaken for one. Sidecar guards against that specifically now, but the profile is still the thing to fix.

### Other common cases

**`no-sidecar` on a machine where sidecar plainly works.** The login shell there does not find it. Confirm with `ssh <target> '$SHELL -l -c "command -v sidecar"'`, and set the **Sidecar path** field or `--binary` to the absolute path it should be using.

**`no-tmux` on a machine where tmux plainly works.** Same cause, same check with `tmux`. Installing tmux where the login shell can find it is the fix.

**`protocol-mismatch`.** The two ends are different Sidecar builds. Update the older one; the probe output tells you which versions are in play.

**Rows appear but never change.** Look for `stale` on the host. A connected but quiet host keeps showing its last-known rows, marked as such, because last-known state is more useful than a blank machine as long as it says so.

**A remote pane that stops accepting keystrokes.** If the session on the host has ended, the pane leaves interactive mode and says so. If it has not, check that the machine is still reachable; a dropped link degrades the one host, never the app.

## Reference

- `sidecar host --help` and `sidecar host <verb> --help` for the full flag and exit-code tables.
- `sidecar shell delete`, `sidecar shell rename`, `sidecar shell send`, `sidecar shell forget` and `sidecar shell restore` are the same verbs the remote path calls, and they work locally too.
- [Workspaces Plugin](./workspaces-plugin) for what those shells and worktrees are, and what the Sessions browser does with them.
