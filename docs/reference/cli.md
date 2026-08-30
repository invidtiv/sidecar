# Sidecar CLI Reference

Sidecar provides non-interactive commands for scripting and agent workflows.

## `sidecar agents`

List what an agent can do from Sidecar

List the Sidecar commands worth reaching for, one line each.
Also spelled "sidecar --agents".

```
Usage: sidecar --agents
```

**Exit codes:**

- `0`: success

**Examples:**

```bash
sidecar --agents
```

## `sidecar create`

Create a Sidecar-managed shell or worktree

Create Sidecar-owned shells and worktrees so they appear in the workspace.

```
Usage: sidecar create <command>
```

### `sidecar create shell`

Create a Sidecar-managed workspace shell

Create a new Sidecar-managed shell in the resolved project's workspace.
The shell is recorded in shells.json so it appears in Sidecar whether or not
an instance is running. --run executes a command in the new shell; --type types
it without pressing Enter so the user can review it.

From inside a Sidecar shell the default placement is a live terminal beside
the current shell. --tab places the shell in the workspace instead, switching
to a completely new surface; --split auto|right|below picks the side of the
beside-the-session split explicitly (the workspace_terminal_panel feature,
on by default, must not be disabled). Beside-the-session modes need a running
instance and a current shell (SIDECAR_SHELL / --shell) and do not add a
workspace row.

```
Usage: sidecar create shell [options]
```

**Options:**

- `--name NAME`: Display name (default: the next Shell N)
- `--run COMMAND`: Execute COMMAND in the new shell
- `--type COMMAND`: Type COMMAND without pressing Enter
- `--shell NAME`: Resolve the project from a registered shell
- `--project NAME`: Target project (slug, basename, or path)
- `--split auto|right|below`: Place a live terminal beside the current shell
- `--tab`: Open as a workspace shell instead of beside this session
- `--wait DURATION`: Time to wait for instances to acknowledge (default 1200ms; 0 = fire and forget)
- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: created (missing ack is non-fatal in workspace-shell mode)
- `1`: state or tmux failure
- `2`: usage error, or this directory is not in a registered project
- `3`: no running instance (split mode)
- `4`: instance declined (cap, too small, or feature off)
- `5`: a value was rejected: --name, or an unknown --project / --shell

**Examples:**

```bash
sidecar create shell --name "dev server" --run "python3 -m http.server"
sidecar create shell --split right --run "python3 -m http.server 8765"
sidecar create shell --json --wait 0
# type a command for the user to review
sidecar create shell --type "go test ./..."
```

### `sidecar create worktree`

Create a Sidecar-managed git worktree

Create a git worktree with the same setup pipeline as the TUI create modal:
plan, add, pending-creation journal, identity, and configured hook/env-file rules.
--agent launches the worktree session (sidecar-ws-…). --no-launch skips that
launch after the worktree and setup still complete.

--plan resolves the same plan and prints it without changing anything: no
worktree is added, no directory is created, no journal is written. It answers
the questions a confirmation has to ask — branch, path, source ref and OID,
remote policy, and whether a setup hook will run — while every validation
failure (an existing branch, an occupied path, an unsafe hook) still surfaces
as exit 5. --run and --no-launch describe a launch --plan never performs, so
they are refused with it; --agent and --skip-permissions are kept, since they
come back as plan fields.

```
Usage: sidecar create worktree [options] <name>
```

**Options:**

- `--base REF`: Base ref (default HEAD)
- `--plan`: Resolve and print the plan without creating anything
- `--agent TYPE`: Launch this agent in the new worktree session
- `--skip-permissions`: Pass the agent's auto-approve flag
- `--run COMMAND`: Execute COMMAND in the new worktree session
- `--no-launch`: Create the worktree without launching a session
- `--shell NAME`: Resolve the project from a registered shell
- `--project NAME`: Target project (slug, basename, or path)
- `--wait DURATION`: Time to wait for instances to acknowledge (default 1200ms; 0 = fire and forget)
- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: created (missing ack is non-fatal), or plan resolved with --plan
- `1`: git, setup, or tmux failure
- `2`: usage error (an unknown flag, a refused flag combination)
- `5`: a value was rejected: the plan (branch exists, path occupied, unknown base ref, unsafe hook), or an unknown --project / --shell

**Examples:**

```bash
sidecar create worktree fix-auth --base main --agent claude
sidecar create worktree scratch --no-launch --json
# what would be created, without creating it
sidecar create worktree fix-auth --base main --plan --json
```

## `sidecar help`

Show help for commands or emit JSON command metadata

Show help for Sidecar commands, or emit the full machine-readable command tree.

```
Usage: sidecar help [--json] [<command>]
```

**Options:**

- `--json`: Write the command tree as JSON to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: success
- `2`: unknown command

**Examples:**

```bash
sidecar help
sidecar help open
sidecar help --json
```

## `sidecar host`

Remote host observation over SSH

Observe another machine's Sidecar state. `serve` runs on the remote host;
`probe` connects to one from here.

```
Usage: sidecar host <serve|probe> [options]
```

### `sidecar host probe`

Connect to a remote host's serve stream and report what came back

Spawn `sidecar host serve --stdio` on an SSH target and consume its stream.

Prints a health verdict naming the fix when something is wrong: unreachable,
no sidecar on the host, protocol too old on either end, no tmux, or a stream
that is not the protocol at all (a login-shell banner on stdout is the usual
cause). With --raw it passes the JSONL through untouched, which is the form
to capture when recording evidence.

```
Usage: sidecar host probe <ssh-target> [--json] [--raw] [--cycles N] [--timeout D]
```

**Options:**

- `--json`: Write one structured result object to stdout
- `--raw`: Pass the host's JSONL through verbatim
- `--cycles N`: Stop after N snapshots (default 1)
- `--timeout D`: Give up after this long (default 30s)
- `--binary PATH`: Explicit sidecar path on the host
- `--remote-config PATH`: -config path for the remote sidecar
- `--env K=V`: Environment for the remote process (repeatable)
- `-h, --help`: Show this help

**Exit codes:**

- `0`: host answered and is compatible
- `1`: host unreachable, incompatible, or not serving the protocol
- `2`: usage error

**Examples:**

```bash
sidecar host probe marcusbook
# Record a raw transcript
sidecar host probe marcusbook --raw --cycles 3
```

### `sidecar host serve`

Stream this machine's Sidecar state as JSONL (spawned over SSH by a remote viewer)

Run the headless, read-only host agent: collect this machine's projects,
shells, worktrees and agent status on the ordinary Overview cadence, and
stream a versioned JSONL snapshot plus status transitions to stdout.

This is not a daemon. It is spawned per connection over an SSH stdio pipe
and exits when that pipe closes. It writes no Sidecar state: it never
touches shells.json, never reaps a dead shell, never takes a geometry
lease, and never resizes a pane.

Nothing is bound to a network. SSH is the entire transport and the entire
trust boundary.

```
Usage: sidecar host serve --stdio [--cycles N] [--project NAME=PATH]
```

**Options:**

- `--stdio`: Serve on stdin/stdout (the only transport)
- `--cycles N`: Exit after N collection cycles (0 = run until the pipe closes)
- `--project NAME=PATH`: Observe this project instead of the configured list (repeatable)
- `-h, --help`: Show this help

**Exit codes:**

- `0`: stream ended cleanly
- `1`: serve failed
- `2`: usage error

**Examples:**

```bash
# What a viewer runs over ssh
sidecar host serve --stdio
# One cycle, for inspection
sidecar host serve --stdio --cycles 1
```

## `sidecar layout`

Read and compose the pane layout agents work beside

Read the current pane layout (`layout get`) or open several panes at once
in one atomic call (`layout apply`). Both act on the surface showing this
Sidecar shell — or, with --sessions, the global Sessions surface — and never
queue: a request whose destination is off screen declines with the reason.

```
Usage: sidecar layout <command>
```

### `sidecar layout apply`

Open several panes in one all-or-nothing call

Compose panes onto the surface showing this Sidecar shell.

--spec is a FULL layout, given as columns of stacked panes; it replaces
what is on screen:

  {"columns":[
    {"panes":[{"kind":"primary"}]},
    {"panes":[{"kind":"file","targets":["path:line","path2",...]},
              {"kind":"issue","targets":["td-xxxxxx",...]}]},
    {"panes":[{"kind":"shell","run":"...","name":"..."}]}
  ]}

A spec needs exactly one "primary" pane and must CARRY every live leaf
already on screen exactly as `layout get` prints them: the primary as
{"kind":"primary"}, a split terminal as {"kind":"shell","session":
"<tmux-session>"}. A spec omitting a live terminal declines naming the
session — apply never destroys one. Passive panes not named are closed
freely (their content re-opens). Pass `-` to read the spec from stdin.

--pane opens panes ADDITIVELY without closing anything. Each value is one
descriptor as its JSON object verbatim:

  {"kind":"file","targets":["path:line",...],"at":"2.1"}
  {"kind":"issue","targets":["td-xxxxxx"]}
  {"kind":"note","targets":["nt-xxxxxx"]}
  {"kind":"diff","targets":["spec"]}   no targets = the working tree
  {"kind":"resource","provider":"<instance>","targets":["LOCATOR"]}
  {"kind":"shell","run":"...","type":"...","name":"..."}

The first target opens a pane; the rest join it as tabs of the same kind.
"at" is an optional grid cell col.row (1-based) and is a requirement, not a
preference: an unreachable cell declines rather than landing elsewhere.
File paths are workspace-relative; diffs re-resolve host-side; providers are
validated against the live matcher snapshot.

Either form is validated and fit-tested before anything changes: it all
happens, or nothing changes and the decline names the first violation.

The ack's items array lists EVERY requested pane with verdict opened,
retargeted, carried (a live leaf the spec kept rather than created), or
declined, plus its landed cell — so one round trip shows everything wrong
with a refused spec. Like get, apply never queues.

```
Usage: sidecar layout apply (--spec '<json>' | --pane '<json>' [--pane '<json>' ...]) [--sessions [ROW]]
```

**Options:**

- `--spec JSON`: A full layout replacing the screen: columns of stacked panes (- reads stdin)
- `--pane JSON`: One pane descriptor to add (repeatable); see above for the object shape
- `--shell NAME`: Target a registered shell by display name or tmux name
- `--project NAME`: Target a project's Workspaces surface (slug, basename, or path)
- `--sessions [ROW]`: Target the global Sessions surface (optional row by ID or display name)
- `--wait DURATION`: Time to wait for instances to acknowledge (default 1200ms)
- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: applied (or every pane retargeted an existing one)
- `1`: state failure
- `2`: usage or validation error
- `3`: no running instance
- `4`: declined host-side; the reason names the first violation
- `5`: an unknown --project or --shell

**Examples:**

```bash
# read before you write
sidecar layout get --json
# a full layout: primary left, file over issue right
sidecar layout apply --spec '{"columns":[{"panes":[{"kind":"primary"}]},{"panes":[{"kind":"file","targets":["README.md"]},{"kind":"issue","targets":["td-756c34"]}]}]}'
# apply a spec from stdin
sidecar layout apply --spec - <layout.json
# add two panes, auto-placed
sidecar layout apply --pane '{"kind":"file","targets":["internal/palette/list.go:112","internal/palette/state.go"]}' --pane '{"kind":"shell","run":"make dev","name":"dev server"}'
# explicit cell, structured result
sidecar layout apply --pane '{"kind":"file","targets":["README.md"],"at":"2.1"}' --json
```

### `sidecar layout get`

Read the current pane layout

Read the pane layout of the surface showing this Sidecar shell: the grid
projection, every pane's kind, targets and tmux session, geometry, and the
caps and floors an apply would be held to.

--sessions addresses the global Sessions surface of a running instance
(optional ROW is a durable inventory ID, then a display name). It is
mutually exclusive with --shell and --project.

A layout that escapes the grid vocabulary reports "grid": null plus the raw
tree; it is still valid. Human output is a small ASCII sketch plus a table;
--json passes the payload through unchanged, which is the contract.

Unlike open, a layout request never queues: when this shell is not on
screen the request declines instead (exit 4), because a stale answer is
worse than a refusal.

```
Usage: sidecar layout get [--json] [--sessions [ROW]]
```

**Options:**

- `--shell NAME`: Target a registered shell by display name or tmux name
- `--project NAME`: Target a project's Workspaces surface (slug, basename, or path)
- `--sessions [ROW]`: Target the global Sessions surface (optional row by ID or display name)
- `--wait DURATION`: Time to wait for instances to acknowledge (default 1200ms)
- `--json`: Write the layout payload itself to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: answered
- `1`: state failure
- `2`: usage error
- `3`: no running instance
- `4`: declined: the origin shell is not on screen
- `5`: an unknown --project or --shell

**Examples:**

```bash
sidecar layout get
# the machine contract: read before you write
sidecar layout get --json
# the selected row on the global Sessions surface
sidecar layout get --sessions --json
```

## `sidecar notify`

Configure, test, post, dismiss, and list Sidecar notifications

Sidecar's notification surface: a toast in the running instance, an entry in the
notification centre, and a count in the header until the user reads it.

```
Usage: sidecar notify <command>
```

### `sidecar notify config`

Show or change notification delivery configuration

Print resolved notification settings and defaults without changing the file. Use config set for global native and sound modes; source and quiet-hour mutation arrive with the focused rule routes.

```
Usage: sidecar notify config [--json]
```

**Options:**

- `--json`: Write notification configuration as JSON
- `-h, --help`: Show this help

#### `sidecar notify config set`

Set global notification delivery modes

Set one or both global delivery modes. Values are off, background, or always. The save is validated, preserves unrelated notification rules, and applies to running Sidecar instances without restart.

```
Usage: sidecar notify config set [--native MODE] [--sound MODE] [--json]
```

**Options:**

- `--native MODE`: Set system notifications: off, background, or always
- `--sound MODE`: Set sounds: off, background, or always
- `--json`: Write the resulting notification configuration as JSON
- `-h, --help`: Show this help

**Exit codes:**

- `0`: saved
- `1`: configuration I/O failure
- `2`: usage or validation error

**Examples:**

```bash
sidecar notify config set --native background --sound background
```

### `sidecar notify dismiss`

Dismiss a notification you posted

Dismiss one notification. A caller may only dismiss notifications it posted:
identity is the Sidecar shell you are in, or failing that the working directory,
so the notification you posted a moment ago is dismissible and the user's own
and other agents' are not.

```
Usage: sidecar notify dismiss [--json] <id>
```

**Options:**

- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: dismissed
- `1`: state failure
- `2`: usage error
- `3`: no notification with that id
- `4`: that notification was posted by someone else

**Examples:**

```bash
sidecar notify dismiss ntf-06215f4b1a2c3-9f1e2d3c
```

### `sidecar notify list`

List notifications

List notifications, newest first. This reads Sidecar's notification log directly,
so it answers whether or not Sidecar is running and never changes anything.

By default dismissed notifications are left out; --all includes them.

```
Usage: sidecar notify list [--all] [--unread] [--json]
```

**Options:**

- `--all`: Include dismissed notifications
- `--unread`: Only notifications the user has not seen
- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: success
- `1`: the notification log could not be read
- `2`: usage error

**Examples:**

```bash
sidecar notify list
sidecar notify list --unread --json
```

### `sidecar notify post`

Post a notification the user sees in Sidecar

Post a notification. It appears as a toast in the running Sidecar instance for
this shell's project and stays in the notification centre until dismissed.

With no instance running the notification is written to Sidecar's notification
log and appears at the next start; nothing is lost.

--expiry sets how long the toast stays on screen — a duration such as 10s, or
"never" for one that waits for the user. Expiry never removes the notification
from the centre.

--target attaches a call to action: the notification centre numbers targets 1-N
and the user jumps to one with enter or a digit. Repeat it for several, in the
order they should be numbered. The form is kind:value[:line][@project], where
kind is issue, task, commit, file, session or url; :line applies to files only;
and @project names another checkout by configured project name or by path, in
which case Sidecar switches projects and then lands. Ids written in the title or
body are still found by scanning — --target is for precision and for targets the
text does not spell out. A session target attaches a Sidecar-owned tmux
session — the sidecar-sh-… and sidecar-ws-… names shells and worktree agents
run under, which are also the only ones found by scanning. A task target opens
the Tasks tab.

```
Usage: sidecar notify post [options] <title>
```

**Options:**

- `--body TEXT`: Detail line shown under the title
- `--target SPEC`: Call to action, kind:value[:line][@project]; repeatable
- `--source ID`: Source: agent, waiting, session, tasks, td, system (default agent)
- `--expiry DURATION`: Toast lifetime (e.g. 10s), or "never" (default: the source's)
- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: posted, or stored for the next start
- `1`: state failure
- `2`: usage or validation error

**Examples:**

```bash
sidecar notify post "Tests are green"
sidecar notify post "Need a decision" --source waiting --expiry never
sidecar notify post "Build failed" --body "go test ./internal/app" --json
sidecar notify post "Review needed" --target issue:td-4c1f9a --target file:internal/app/model.go:42
sidecar notify post "Fixed upstream" --target issue:td-99aabb@braid
```

### `sidecar notify status`

Probe native and sound provider availability

Probe providers without sending a notification or changing configuration.

```
Usage: sidecar notify status [--json]
```

**Options:**

- `--json`: Write provider capabilities as JSON
- `-h, --help`: Show this help

**Exit codes:**

- `0`: probe completed
- `1`: output failure
- `2`: usage error

**Examples:**

```bash
sidecar notify status --json
```

### `sidecar notify test`

Explicitly test enabled notification channels

Exercise enabled providers without creating a notification-centre record. Explicit tests bypass foreground and quiet-hours suppression but still honor disabled channels and unavailable providers.

```
Usage: sidecar notify test --channel native|sound|all [--event waiting|done|failure] [--json]
```

**Options:**

- `--channel CHANNEL`: Test native, sound, or all (required)
- `--event EVENT`: Use waiting, done, or failure (default waiting)
- `--json`: Write per-channel attempted/provider/delivered/error results
- `-h, --help`: Show this help

**Exit codes:**

- `0`: requested channels delivered
- `1`: provider or output failure
- `2`: usage error
- `3`: a requested channel was disabled or unavailable

**Examples:**

```bash
sidecar notify test --channel all --event waiting --json
```

## `sidecar open`

Show a file, a td issue, a note, a git diff, or a provider resource in a split pane

Show a file, a td issue, a td note, a git diff, or an external provider resource to the user as a
split pane in a Sidecar workspace. From a Sidecar shell this targets that shell.
Otherwise it targets the unique running instance, or a specific --shell / --project.
--diff with no spec is the working tree. --provider names a configured terminal resource
provider instance and is required for a resource: a bare locator is never guessed at.
--split only overrides the split axis; it never halves a live terminal after content is open.
--at places the pane at an explicit grid cell and is a requirement: a kind whose open
would retarget an existing pane, or any cell that cannot be honored exactly, declines
rather than land elsewhere (--split expresses a preference; --at, a demand).

```
Usage: sidecar open [options] [<target>]
```

**Targets:**

- `path`: A file inside the target workspace, optionally "path:line"
- `td-xxxxxx`: A td issue id
- `sidecar://note/nt-xxxx`: A td note, opened as a read-only pane
- `--diff`: Working-tree diff (wt); add a spec for a commit or range
- `spec`: A git commit or range (abc1234, A..B); --diff accepts HEAD and branch names
- `locator`: With --provider, a resource key such as CASH-1245

**Options:**

- `--line N`: Line to reveal (alternative to "path:line")
- `--diff`: Open a Diff leaf (working tree if no spec)
- `--provider ID`: Open a locator through a configured terminal resource provider
- `--shell NAME`: Target a registered shell by display name or tmux name
- `--project NAME`: Target a project's Workspaces surface (slug, basename, or path)
- `--split auto|right|below`: Where to place a new pane (default auto)
- `--at COL[.ROW]`: Place at an explicit grid cell (1-based); a requirement, mutually exclusive with --split
- `--wait DURATION`: Time to wait for instances to acknowledge (default 1200ms; 0 = fire and forget)
- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: opened or queued
- `1`: state failure
- `2`: usage or validation error
- `3`: no running instance, or several running with no target
- `4`: an instance declined (e.g. the window is too small to split)
- `5`: an unknown --project or --shell

**Examples:**

```bash
# file, in a split beside the terminal
sidecar open internal/cli/cli.go
# file at a line
sidecar open internal/cli/cli.go:88
# td issue
sidecar open td-348d88
# td note pane
sidecar open sidecar://note/nt-4jdj4e
# working-tree Diff leaf
sidecar open --diff
# that commit, not the working tree
sidecar open --diff HEAD
# commit, unless a file of that name exists
sidecar open abc1234
# resource pane for that provider's locator
sidecar open --provider jira-work CASH-1245
# structured result for the agent
sidecar open --json --split below README.md
# explicit cell: second column, top row
sidecar open README.md --at 2.1
# from any terminal, that project's Workspaces surface
sidecar open --project sidecar README.md
```

## `sidecar setup`

Start Sidecar with Configuration open on Sidecar Setup

Start Sidecar normally, with Configuration open on the Sidecar Setup page.
Setup lists what is left to do — add a project, install tmux, connect agent
instructions — and opens a focused repair for each one.

This is a launch route, not a second settings interface: it renders nothing in
the terminal and changes nothing on its own. Sidecar's ordinary options still
apply (sidecar setup -project /path). Escape returns to the surface underneath,
and the header gear reopens Configuration at any time.

If startup fails before Sidecar can draw — a malformed config file, a terminal
that is not interactive — it exits nonzero with the specific next step and a
support path that uploads nothing.

```
Usage: sidecar setup [options]
```

**Options:**

- `-h, --help`: Show this help

**Exit codes:**

- `0`: Sidecar ran and exited normally
- `1`: startup failed before the first frame
- `2`: usage error

**Examples:**

```bash
sidecar setup
# that project's Setup
sidecar setup -project ~/code/myproject
```

## `sidecar shell`

Manage Sidecar shell records and the current shell's name

List, forget, and restore this project's shell records; read or rename a shell; and send a command into one.

```
Usage: sidecar shell <command>
```

### `sidecar shell forget`

Forget a shell record by tmux name

Forget a Sidecar-managed shell record in the current project. The definition
moves to a tombstone so `sidecar shell restore` can put it back; the tmux
session is not started or killed.

A name that is already forgotten is already in that state (exit 0). A name
that is in neither the live list nor the tombstones is not found (exit 1).

A forgotten record stays restorable for 14 days by default; set
shells.tombstoneRetention in config.json ("30d", "336h", or "forever") to
change the window.

```
Usage: sidecar shell forget [--json] <tmux-name>
```

**Options:**

- `--json`: Write one structured result object to stdout
- `--shell NAME`: Resolve the project from a registered shell
- `--project NAME`: Target project (slug, basename, or path)
- `-h, --help`: Show this help

**Exit codes:**

- `0`: forgotten, or already forgotten
- `1`: not found, or state failure
- `2`: usage error
- `5`: an unknown --project or --shell

**Examples:**

```bash
sidecar shell forget sidecar-sh-sidecar-1
sidecar shell forget --json sidecar-sh-sidecar-1
```

### `sidecar shell list`

List this project's shell records

List Sidecar-managed shell records for the current project. Live records
are listed first, then forgotten ones, so either surface can restore a record
by tmux name.

This reads shells.json directly; it does not start or inspect tmux sessions.

```
Usage: sidecar shell list [--json]
```

**Options:**

- `--json`: Write one structured result object to stdout
- `--shell NAME`: Resolve the project from a registered shell
- `--project NAME`: Target project (slug, basename, or path)
- `-h, --help`: Show this help

**Exit codes:**

- `0`: success
- `1`: state failure
- `2`: usage error
- `5`: an unknown --project or --shell

**Examples:**

```bash
sidecar shell list
sidecar shell list --json
```

### `sidecar shell name`

Print the current shell's display name

Print the Sidecar display name of the managed shell or worktree agent containing
this command. Reads registered Sidecar state (authoritative), not the agent SDK
or $SIDECAR_SHELL_NAME, so reopening another agent in place keeps its context.

Human output is the display name alone, one line, for easy scripting.
JSON includes the stable tmux session id and display name.

```
Usage: sidecar shell name [--json]
```

**Options:**

- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: success
- `1`: identity or state failure
- `2`: usage error

**Examples:**

```bash
sidecar shell name
sidecar shell name --json
```

### `sidecar shell rename`

Rename the current shell, or one named with --target

Rename the Sidecar-managed shell or worktree agent containing this command, or
with --target, one you are not sitting in. This changes Sidecar's display name;
it does not rename the tmux session, Git branch, or worktree directory.

The current display name is also published as $SIDECAR_SHELL_NAME. "Shell 3"
is the unset default; a previous task's name is equally stale — rename when
the name no longer describes the work in this shell.

--target takes a tmux session name: a sidecar-sh-… record from `sidecar shell
list`, or a sidecar-ws-… worktree agent. The session must belong to the resolved
project (--project, or the project this directory is in) — a name Sidecar does
not own is refused rather than renamed. --shell and --project only scope a
--target; without one, the current shell is the only subject.

```
Usage: sidecar shell rename [--target SESSION [--project NAME]] [--json] <display-name>
```

**Options:**

- `--target SESSION`: Rename this tmux session instead of the current shell
- `--shell NAME`: Resolve the project from a registered shell (with --target)
- `--project NAME`: Target project (slug, basename, or path; with --target)
- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: renamed, or already named that
- `1`: identity, ambiguity, or state failure
- `2`: usage error; without --target, also a rejected display name (the current-shell form's long-standing code)
- `3`: --target names no session this project owns, or one on a different tmux server
- `5`: with --target: a value was rejected — the display name (already used, or not legal), or an unknown --project / --shell

**Examples:**

```bash
sidecar shell rename "shell rename implementation"
sidecar shell rename --target sidecar-sh-sidecar-2 --json "release prep"
sidecar shell rename --project sidecar --target sidecar-ws-sidecar-fix-auth "fix auth"
```

### `sidecar shell restore`

Restore a forgotten shell record by tmux name

Restore a forgotten Sidecar-managed shell record in the current project.
Display name, agent type, skip-perms, and working directory come back with it.
The tmux session is not started.

A name that is still live is already in that state (exit 0). A name that is in
neither the live list nor the tombstones is not found (exit 1) — including a
record whose retention window (shells.tombstoneRetention, 14 days by default)
has passed.

```
Usage: sidecar shell restore [--json] <tmux-name>
```

**Options:**

- `--json`: Write one structured result object to stdout
- `--shell NAME`: Resolve the project from a registered shell
- `--project NAME`: Target project (slug, basename, or path)
- `-h, --help`: Show this help

**Exit codes:**

- `0`: restored, or already live
- `1`: not found, or state failure
- `2`: usage error
- `5`: an unknown --project or --shell

**Examples:**

```bash
sidecar shell restore sidecar-sh-sidecar-1
sidecar shell restore --json sidecar-sh-sidecar-1
```

### `sidecar shell send`

Run or type a command in a shell you are not sitting in

Send a command to an existing Sidecar-managed session. --run presses Enter;
--type leaves the command on the prompt for the user to read and run. This is
the same distinction `sidecar create shell --run/--type` makes, for a session
that already exists.

--target is required and must name a session the resolved project owns: a
sidecar-sh-… record in its shells.json, or a sidecar-ws-… agent for one of its
registered worktrees. tmux resolves a session name against whatever answers to
it, so an unregistered name is refused (exit 3) rather than typed into.

The keys go to the tmux server this process resolves, and the session must be
running: a record for a session that is not up is a tmux failure (exit 1), not
a silent success.

```
Usage: sidecar shell send --target SESSION (--run COMMAND | --type COMMAND) [--project NAME] [--json]
```

**Options:**

- `--target SESSION`: The tmux session to send to (required)
- `--run COMMAND`: Execute COMMAND in the session
- `--type COMMAND`: Type COMMAND without pressing Enter
- `--shell NAME`: Resolve the project from a registered shell
- `--project NAME`: Target project (slug, basename, or path)
- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: sent
- `1`: tmux, ambiguity, or state failure
- `2`: usage error
- `3`: --target names no session this project owns, or one recorded on a different tmux server
- `5`: an unknown --project or --shell

**Examples:**

```bash
# start an agent in an existing shell
sidecar shell send --target sidecar-sh-sidecar-2 --run "claude"
sidecar shell send --target sidecar-ws-sidecar-fix-auth --run "go test ./..."
# leave it for the user to run
sidecar shell send --target sidecar-sh-sidecar-2 --type "git push" --json
```

## `sidecar terminal-links`

Inspect terminal resource providers

Inspect the external executables that teach Sidecar to recognize resource keys in
terminal output. This is a protocol and administration surface, not a replacement
for a provider's own CLI.

```
Usage: sidecar terminal-links <command>
```

### `sidecar terminal-links check`

Check one terminal resource provider instance

Check one configured provider instance: that it is enabled, that its command
resolves, and that its describe method answers the protocol. The child runs with
the exact working directory, base environment, passEnv policy, and timeout Sidecar
uses in the TUI, so this is the authoritative host-environment proof.

--resolve is separate and explicit because it can perform network access and print
private resource data. Without it, nothing is resolved.

The provider's stderr is drained and discarded, never printed: reproduce provider
failures by running the provider's own CLI deliberately.

```
Usage: sidecar terminal-links check [--resolve LOCATOR] [--json] [--config PATH] <instance>
```

**Options:**

- `--resolve LOCATOR`: Also resolve one locator (may hit the network and print private data)
- `--json`: Write one structured result object to stdout
- `--config PATH`: Read a specific config file
- `-h, --help`: Show this help

**Exit codes:**

- `0`: the instance checked out
- `1`: the command, describe, or resolve failed
- `2`: usage error
- `3`: no provider instance with that id is configured

**Examples:**

```bash
sidecar terminal-links check jira-work
sidecar terminal-links check jira-work --json
sidecar terminal-links check jira-work --resolve CASH-1245 --json
```

### `sidecar terminal-links list`

List configured terminal resource providers

List the terminal resource providers configured under "terminalResources".
By default this reads configuration and resolves each command on PATH; it starts
no process. --describe additionally asks each enabled provider to describe itself,
which is local and non-interactive but does spawn one child per instance.

passEnv is reported by name and presence only. A passed value is never printed.

Enabling a provider trusts that executable with your full OS privileges: a process
boundary is crash isolation, not a sandbox.

```
Usage: sidecar terminal-links list [--describe] [--json] [--config PATH]
```

**Options:**

- `--describe`: Also run each enabled provider's describe method
- `--json`: Write one structured result object to stdout
- `--config PATH`: Read a specific config file
- `-h, --help`: Show this help

**Exit codes:**

- `0`: success
- `1`: configuration could not be read
- `2`: usage error

**Examples:**

```bash
sidecar terminal-links list
sidecar terminal-links list --json
sidecar terminal-links list --describe --json
```

