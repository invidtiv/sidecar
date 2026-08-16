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

## `sidecar open`

Show a file, a td issue, or a git diff in a split pane

Show a file, a td issue, or a git diff to the user as a split pane in a Sidecar workspace.
From a Sidecar shell this targets that shell. Otherwise it targets the unique running
instance, or a specific --shell / --project. --diff with no spec is the working tree.
--split only overrides the split axis; it never halves a live terminal after content is open.

```
Usage: sidecar open [options] [<target>]
```

**Targets:**

- `path`: A file inside the target workspace, optionally "path:line"
- `td-xxxxxx`: A td issue id
- `--diff`: Working-tree diff (wt); add a spec for a commit or range
- `spec`: A git commit or range (abc1234, A..B); --diff accepts HEAD and branch names

**Options:**

- `--line N`: Line to reveal (alternative to "path:line")
- `--diff`: Open a Diff leaf (working tree if no spec)
- `--shell NAME`: Target a registered shell by display name or tmux name
- `--project NAME`: Target a project's Workspaces surface (slug, basename, or path)
- `--split auto|right|below`: Where to place a new pane (default auto)
- `--wait DURATION`: Time to wait for instances to acknowledge (default 1200ms; 0 = fire and forget)
- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: opened or queued
- `1`: state failure
- `2`: usage or validation error
- `3`: no running instance, or several running with no target
- `4`: an instance declined (e.g. the window is too small to split)

**Examples:**

```bash
# file, in a split beside the terminal
sidecar open internal/cli/cli.go
# file at a line
sidecar open internal/cli/cli.go:88
# td issue
sidecar open td-348d88
# working-tree Diff leaf
sidecar open --diff
# that commit, not the working tree
sidecar open --diff HEAD
# commit, unless a file of that name exists
sidecar open abc1234
# structured result for the agent
sidecar open --json --split below README.md
# from any terminal, that project's Workspaces surface
sidecar open --project sidecar README.md
```

## `sidecar shell`

Manage the current Sidecar shell context

Manage the current Sidecar-managed shell or worktree agent context.

```
Usage: sidecar shell <command>
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

Rename the current shell's display name

Rename only the Sidecar-managed shell or worktree agent containing this command.
This changes Sidecar's display name; it does not rename the tmux session, Git
branch, or worktree directory.

The current display name is also published as $SIDECAR_SHELL_NAME. "Shell 3"
is the unset default; a previous task's name is equally stale — rename when
the name no longer describes the work in this shell.

```
Usage: sidecar shell rename [--json] <display-name>
```

**Options:**

- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: success
- `1`: identity or state failure
- `2`: usage or validation error

**Examples:**

```bash
sidecar shell rename "shell rename implementation"
```

