# Phase A real-app proof (td-ff511f, slice 5)

Isolated run: `scripts/tmux-drive.sh` with `SIDECAR_DRIVE_RUN_DIR=/tmp/sidecar-drive-slice5`,
`SIDECAR_DRIVE_ARGS=-enable-feature=workspace_terminal_panel`, 200x50.
`paths` confirmed before the run: tmux socket, state home, cache and config all under the
run dir — nothing resolved under `~/.local/state/sidecar` or `~/.config/sidecar`.

| snapshot | what it shows |
| --- | --- |
| `p1-workspaces` | clean state tree: no split, no layout badge on any row |
| `p3-agent-none` | create modal, agent picker (None) for a plain live shell |
| `p4b` | `Shell 1` created — one live tmux session, primary terminal at 115x45 |
| `p5-modal-termsplit` | create modal on the **Terminal split** row: auto-name `term · sidecar-terminal-splits`, `Auto · Right · Below` placement row |
| `p6-two-terminals` | Enter (Auto) → two live terminals side by side; sidebar row badged `◧◨`; tmux 55x45 + 54x45 |
| `p7-both-live` | both terminals live: `PRIMARY-LIVE` and `SPLIT-LIVE` echoed in their own sessions |
| `p8-after-drag` | SGR mouse press/motion/release on the divider → 39x45 + 70x45, one tmux resize per pane on release |
| `p9-delete-confirm` / `p10-after-neighbor-close` | the neighbouring shell is deleted; the split terminal and its session survive |
| `p11-after-restart` | relaunch in the same isolated state tree: layout restored at the dragged ratio (70 cols), badge restored, split reattached to the same `sidecar-tp-…` session |
| `state-after-restart.json` | persisted `paneLayouts`: `split{axis cols, ratio 37}` with the shell leaf's durable session selector |

Fallout fixed in this slice: the split terminal's name followed the pane onto the next
workspace's session, titling one workspace's terminal with another's name; an unnamed leaf
now falls back to the auto-name for where it is, and `termPanelWorkDir` no longer panics
before a context is bound.
