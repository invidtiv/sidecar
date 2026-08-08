# Driving sidecar headlessly

Sidecar is a TUI, so "does this actually work?" normally needs a human at a
keyboard. `scripts/tmux-drive.sh` removes that: it runs sidecar inside a tmux
session on a dedicated socket, sends it keystrokes, and captures the screen as
text and PNG. Every bug in the embedded-terminal audit was reproduced and
verified this way.

## Why a dedicated socket

Sidecar creates tmux sessions of its own — one per shell, per agent, per inline
editor — on the user's default tmux server. If the host session lived there too,
`tmux list-panes -a` could not tell "the pane sidecar is drawn in" from "a pane
sidecar created", and a stray `kill-server` would take out the user's real work.

So there are up to three tmux servers in play:

| Socket | Session | What it is |
| --- | --- | --- |
| `-L sidecar-drive` | `host` | The pane sidecar itself renders into |
| default | `sidecar-sh-*`, `sidecar-edit-*` | Sessions sidecar creates |
| `-L sidecar-view` | `viewer` | Optional real client, only needed for cursor checks |

## Basic loop

```bash
SIDECAR_BIN=$HOME/go/bin/sidecar ./scripts/tmux-drive.sh start 200 50
sleep 3                                   # let the first frame paint

./scripts/tmux-drive.sh keys 5            # tmux send-keys passthrough
./scripts/tmux-drive.sh type 'echo hi'    # literal text
./scripts/tmux-drive.sh keys Enter

./scripts/tmux-drive.sh snap my-check     # -> /tmp/sidecar-drive/my-check.{txt,png}
./scripts/tmux-drive.sh panes             # inner pane sizes and commands
./scripts/tmux-drive.sh stop
```

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
sidecar is mirroring:

```bash
tmux display-message -t sidecar-sh-<project>-1 -p 'cur=#{cursor_x},#{cursor_y} hist=#{history_size}'
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
- **Pre-existing sessions.** `panes` greps for sidecar-created sessions, which
  will include ones from the user's real sidecar. Match on the session name for
  the project under test.
- **Clean up on the way out.** `stop` kills the host session but not the shells
  sidecar created; kill those by name. Never `kill-server` on the default socket.

## Reading the output

`snap` writes `$SIDECAR_DRIVE_OUT` (default `/tmp/sidecar-drive`). The `.txt`
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
