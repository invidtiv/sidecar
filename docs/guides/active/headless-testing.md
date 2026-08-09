# Driving sidecar headlessly

Sidecar is a TUI, so "does this actually work?" normally needs a human at a
keyboard. `scripts/tmux-drive.sh` removes that: it runs sidecar inside a tmux
session on a dedicated socket, sends it keystrokes, and captures the screen as
text and PNG. Every bug in the embedded-terminal audit was reproduced and
verified this way.

## Two isolation axes: tmux transport AND Sidecar state

A proof run touches the developer's machine in two independent places, and
isolating one of them is not isolating the run:

1. **tmux** — the sessions sidecar creates and the pane it renders into.
2. **Sidecar state** — `shells.json`, `state.json`, `config.json`, `debug.log`.

"Never restart the default tmux server" is necessary but **not sufficient**. In
td-8d18de a proof run held a private tmux socket and still resolved the real
`~/.local/state/sidecar`. It rewrote the shared shell manifest down to its own
single session; the developer's live Sidecar was watching that file and dropped
six shells from the Workspaces sidebar whose tmux sessions were still running.
No tmux server was ever harmed. The damage was entirely on axis 2.

### Axis 1 — tmux

`cmd/sidecar/main.go` **unsets `TMUX`** on startup, and no tmux call site in the
codebase passes `-L` or `-S`. So wrapping the launch in `tmux -L something` moves
only the *outer host pane*; every `sidecar-sh-*` / `sidecar-edit-*` session
sidecar creates for itself still lands on whatever server `TMUX_TMPDIR` resolves
to — the user's default one, by default. **`TMUX_TMPDIR` is the only lever that
isolates sidecar's own sessions.**

| Socket | Session | What it is |
| --- | --- | --- |
| `-L sidecar-drive` | `host` | The pane sidecar itself renders into |
| `$TMUX_TMPDIR/tmux-$(id -u)/default` | `sidecar-sh-*`, `sidecar-edit-*` | Sessions sidecar creates — private only because `TMUX_TMPDIR` moved them |
| `-L sidecar-view` | `viewer` | Optional real client, only needed for cursor checks |

### Axis 2 — Sidecar state

| Lever | What it actually moves |
| --- | --- |
| `XDG_STATE_HOME` | The state dir, and with it `projects/<slug>/shells.json` |
| `XDG_CONFIG_HOME` | **Nothing.** `config.ConfigPath()` and `state.Init()` are `$HOME`-based and deliberately ignore it |
| `-config <path>` | `config.json`, and via its directory `state.json` and `debug.log` |
| `SIDECAR_ISOLATED_STATE=1` | Turns on the fail-closed guard |

With the guard on, any path that still resolves inside `$HOME/.local/state/sidecar`
or `$HOME/.config/sidecar` is a hard error: the manifest refuses to load or save,
project state directories refuse to be created, the version caches quietly skip
themselves, and the binary exits 1 at startup — before it opens `debug.log` —
rather than writing a byte.

Go test binaries assert isolation automatically. A package that forgets to move
`XDG_STATE_HOME` gets a refusal rather than silently creating directories in the
developer's real `~/.local/state/sidecar/projects`. A test that deliberately
exercises the ordinary unisolated path opts out with `SIDECAR_ALLOW_REAL_STATE=1`,
and must point `HOME` at a temp dir first.

`scripts/tmux-drive.sh` does both axes for you. It keeps one stable run root per
user (`/tmp/sidecar-drive-$(id -u)`, override with `SIDECAR_DRIVE_RUN_DIR`) so
`start`, `keys`, `snap` and `stop` — separate processes — agree on which server
and which state tree they mean:

```
$RUN_DIR/tmux/tmux-<uid>/default   the private tmux server (TMUX_TMPDIR)
$RUN_DIR/state/sidecar/...         XDG_STATE_HOME, holds shells.json
$RUN_DIR/cache/                    XDG_CACHE_HOME
$RUN_DIR/config/config.json        passed as -config
$RUN_DIR/out/                      text and PNG snapshots
```

The run root is canonicalized before any of these paths are derived. It must
have an existing parent, contain no `.`/`..` components or symlink traversal,
and resolve beneath `/tmp` or `$TMPDIR`. A custom `SIDECAR_DRIVE_OUT` must also
remain beneath that canonical run root.

Check it before you trust it, and confirm nothing resolves under
`~/.local/state/sidecar` or `~/.config/sidecar`:

```bash
./scripts/tmux-drive.sh paths
```

The launch repository defaults to the Sidecar checkout. To prove behavior in a
fixture, set `SIDECAR_DRIVE_REPO` to an absolute existing directory. `paths`
prints the resolved launch repository, and `start` uses it as both the tmux
working directory and Sidecar project root. This does not change any isolation
path:

```bash
SIDECAR_DRIVE_REPO=/tmp/my-fixture ./scripts/tmux-drive.sh paths
```

**One run root per driver.** Two agents driving proofs at the same time would
share that tmux server *and* that state tree, which is the cross-instance
contention this guide exists to avoid, merely relocated off the real tree.
`start` refuses when a session is already running in the run root; give each
agent its own `SIDECAR_DRIVE_RUN_DIR`, or pass `SIDECAR_DRIVE_FORCE=1` if you are
certain the existing session is yours and finished.

`SIDECAR_DIAG_PATHS=1` makes the binary itself say what it resolved (it is
printed unconditionally when isolation is asserted, and always goes to the log):

```
sidecar paths: state=/tmp/sidecar-drive-501/state/sidecar config=/tmp/sidecar-drive-501/config/config.json tmux-socket=/tmp/sidecar-drive-501/tmux/tmux-501/default project-root=/Users/you/code/sidecar manifest=/tmp/sidecar-drive-501/state/sidecar/projects/sidecar/shells.json
```

Launching sidecar for a proof by hand means doing all of it yourself:

```bash
XDG_STATE_HOME=/tmp/proof/state XDG_CACHE_HOME=/tmp/proof/cache \
TMUX_TMPDIR=/tmp/proof/tmux SIDECAR_ISOLATED_STATE=1 \
  sidecar -config /tmp/proof/config/config.json
```

## Basic loop

```bash
SIDECAR_BIN=$HOME/go/bin/sidecar ./scripts/tmux-drive.sh start 200 50
sleep 3                                   # let the first frame paint

./scripts/tmux-drive.sh keys 5            # tmux send-keys passthrough
./scripts/tmux-drive.sh type 'echo hi'    # literal text
./scripts/tmux-drive.sh keys Enter

./scripts/tmux-drive.sh snap my-check     # -> $SIDECAR_DRIVE_RUN_DIR/out/my-check.{txt,png}
./scripts/tmux-drive.sh panes             # inner pane sizes and commands
./scripts/tmux-drive.sh stop
```

For terminal-cutover proof, the driver also exposes narrowly scoped helpers for
the private inner server. `PANE` must resolve on that server; the process helper
only reports exact `tmux -C attach-session -f ignore-size -t SESSION` clients
that are descendants of the Sidecar host pane and whose session exists on the
private server:

```bash
FIXTURE_ROOT=$(mktemp -d /tmp/sidecar-terminal-cutover.XXXXXX)
./scripts/terminal-cutover-fixture.sh "$FIXTURE_ROOT"
export SIDECAR_DRIVE_RUN_DIR="$FIXTURE_ROOT"
export SIDECAR_DRIVE_REPO="$FIXTURE_ROOT/main"
export EDITOR="$FIXTURE_ROOT/editors/nvim-proof"

SIDECAR_DRIVE_ARGS='--enable-feature=notes_plugin' ./scripts/tmux-drive.sh start 200 50
./scripts/tmux-drive.sh panes
./scripts/tmux-drive.sh inner-type %3 'printf "CUTOVER_MARKER\\n"'
./scripts/tmux-drive.sh inner-keys %3 Enter

./scripts/tmux-drive.sh capture-hook-install
./scripts/tmux-drive.sh capture-hook-reset
# Produce steady output, then require this to stay empty.
./scripts/tmux-drive.sh capture-hook-show

./scripts/tmux-drive.sh control-clients
# Use only the exact PID printed above to exercise fallback/reseed.
./scripts/tmux-drive.sh control-kill PID
```

The fixture has committed proof files, two linked worktrees, deterministic
`nvim`/`nano` wrappers, and an isolated config enabling Notes with only Codex in
the agent picker. It refuses a nonempty destination and any path under `$HOME`.

`control-kill` rechecks ancestry, exact argv shape, inherited `TMUX_TMPDIR`
identity, and private-session membership immediately before sending `TERM`; it
refuses zero or ambiguous matches. There
is intentionally no general process killer or inner `kill-server` command.
The capture hook and its log live under `$SIDECAR_DRIVE_RUN_DIR/proof` and are
installed only on the explicit inner socket. `SIDECAR_DRIVE_ARGS` is split on
whitespace without shell evaluation, so use it only for flags whose individual
values contain no spaces.

`SIDECAR_BIN` defaults to whatever `sidecar` resolves to on `PATH`. Build to a
temp path and point at it to compare a fix against its parent commit — that is
how the keystroke-ordering regression was demonstrated:

```bash
git archive HEAD | (mkdir -p /tmp/prefix && tar -x -C /tmp/prefix)
(cd /tmp/prefix && go build -o /tmp/sidecar-prefix ./cmd/sidecar)
SIDECAR_BIN=/tmp/sidecar-prefix ./scripts/tmux-drive.sh start 200 50
```

Read the PNGs with the Read tool. They render the real colours and box drawing,
so layout bugs are obvious in a way that a text dump is not.

## Pacing

Keys are delivered as fast as the shell can spawn `tmux`, which is far faster
than sidecar repaints. Without sleeps a burst gets eaten by whatever modal the
first keystroke opened — typing `nvim …` starts with `n`, which is "new
worktree". Rules that work:

- ~3s after `start` before the first key
- ~1.5s after a key that changes tabs or panes
- ~2.5s after `ctrl+n` (a shell has to spawn)
- ~0.3s between `type` and the `Enter` that submits it

Prefer asserting on state (`panes`, `tmux display-message`) over sleeping longer.

## Checking the native cursor

The embedded terminal uses a **native** cursor: sidecar emits a `tea.Cursor` and
the host terminal draws it. It is not a character in the framebuffer, so
`capture-pane` cannot see it — the cursor is invisible in every snapshot.

To observe it, attach a real client from a second tmux server and read that
client's cursor:

```bash
tmux -L sidecar-view new-session -d -s viewer -x 200 -y 50 \
  'tmux -L sidecar-drive attach -t host'

tmux -L sidecar-view display-message -t viewer -p 'flag=#{cursor_flag} at #{cursor_x},#{cursor_y}'
```

`flag=0` means sidecar is not drawing a cursor at all. Compare against the pane
sidecar is mirroring — that pane is on the *private* inner server, so name its
socket explicitly rather than letting a bare `tmux` fall through to the
developer's default one:

```bash
INNER_SOCKET="${SIDECAR_DRIVE_RUN_DIR:-/tmp/sidecar-drive-$(id -u)}/tmux/tmux-$(id -u)/default"
tmux -S "$INNER_SOCKET" display-message -t sidecar-sh-<project>-1 \
  -p 'cur=#{cursor_x},#{cursor_y} hist=#{history_size}'
```

Two coordinate spaces meet here and mixing them up is the usual bug. A capture
is `capture-pane -S -N`: scrollback lines *followed by* pane rows, so the pane's
`cursor_y` is not a display row — pane row `j` is absolute line
`history_size + j`. `#{history_size}` is the conversion factor, and it is why the
cursor used to sit one row high on a fresh shell and drift further after a
full-screen program had scrolled the pane.

## Gotchas

- **Editor config leaks into results.** A first attempt at the post-vim cursor
  bug was wasted because neovim's deprecation prompt swallowed `:q!`, leaving the
  pane on the alternate screen (`alternate_on=1`) showing stale gutter rows.
  Use `nvim -u NONE` to isolate sidecar's behaviour; use the real config only
  when reproducing a report that depends on it.
- **Nerd-font glyphs shift columns.** Powerline separators in a statusline are
  measured differently by the program and by sidecar's truncation, so column
  assertions against a themed prompt are unreliable. Assert on rows, or on a
  plain prompt.
- **`capture-pane` strips trailing blank lines**, so a capture's length is not
  the pane height.
- **Pre-existing sessions.** `panes` lists the inner server by explicit socket
  path, so it can only ever report sessions this run created. If you query
  sessions yourself, pass `-S "$INNER_SOCKET"` for the same reason.
- **A shared `shells.json` is a cross-instance blast radius.** The shell manifest
  is a file several sidecars read and write. An un-isolated proof run does not
  just pollute it — it rewrites the live user's sidebar, and the shells that
  disappear are still running in tmux with unsaved work in them. Reconciliation
  is conservative now (td-8d18de: a foreign instance's absence is not evidence of
  death, and `r` re-runs discovery to self-heal), but the only real fix is to
  never point a test at that file. Isolate axis 2.
- **Clean up on the way out.** `stop` kills the host session and then the inner
  server by explicit socket path (`tmux -S "$INNER_SOCKET" kill-server`). Never
  run a bare `kill-server`: with no `-S` it trusts the ambient environment and
  will take down the developer's default server and every session on it.

## Reading the output

`snap` writes `$SIDECAR_DRIVE_OUT` (default `$RUN_DIR/out`). The `.txt`
keeps ANSI, which is what you want for grepping styles; strip it for width
assertions, and measure display width rather than byte length:

```bash
tmux -L sidecar-drive capture-pane -t host -p > plain.txt   # no -e, no ANSI
python3 -c "
import unicodedata
def w(s): return sum(2 if unicodedata.east_asian_width(c) in 'WF' else 1 for c in s)
for i, l in enumerate(open('plain.txt'), 1): print(i, w(l.rstrip()))
"
```
