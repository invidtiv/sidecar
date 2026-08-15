# Sidecar CLI Reference

Sidecar provides non-interactive commands for scripting and agent workflows.

## `sidecar agents`

List what an agent can do from inside a Sidecar shell

List the Sidecar commands worth reaching for from inside a project shell,
one line each. Also spelled "sidecar --agents".

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

Show a file or a td issue in a split pane

Show a file or a td issue to the user, as a split pane in the Sidecar workspace
containing this shell.

```
Usage: sidecar open [options] <target>
```

**Targets:**

- `path`: A file inside this shell's workspace, optionally "path:line"
- `td-xxxxxx`: A td issue id

**Options:**

- `--line N`: Line to reveal (alternative to "path:line")
- `--split auto|right|below`: Where to place a new pane (default auto)
- `--wait DURATION`: Time to wait for instances to acknowledge (default 1200ms; 0 = fire and forget)
- `--json`: Write one structured result object to stdout
- `-h, --help`: Show this help

**Exit codes:**

- `0`: opened or queued
- `1`: state failure
- `2`: usage or validation error
- `3`: no running Sidecar instance is showing this shell
- `4`: an instance declined (e.g. the window is too small to split)

**Examples:**

```bash
# file, in a split beside the terminal
sidecar open internal/cli/cli.go
# file at a line
sidecar open internal/cli/cli.go:88
# td issue
sidecar open td-348d88
# structured result for the agent
sidecar open --json --split below README.md
```

## `sidecar shell`

Manage the current Sidecar project shell

Manage the current Sidecar project shell.

```
Usage: sidecar shell <command>
```

### `sidecar shell name`

Print the current shell's display name

Print the Sidecar display name of the project shell containing this command.
Reads the registered manifest (authoritative), not $SIDECAR_SHELL_NAME, so it
works for shells created before that environment cue existed.

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

Rename only the Sidecar project shell containing this command. This changes
Sidecar's display name; it does not rename the tmux session.

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

