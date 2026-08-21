# Split terminal lifecycle proof (td-5e8d07, commit 96e17898)

Isolated run: `scripts/tmux-drive.sh` with `SIDECAR_DRIVE_RUN_DIR=/tmp/sidecar-drive-lifecycle`,
`SIDECAR_DRIVE_ARGS=-enable-feature=workspace_terminal_panel`, 200x50, a binary built from this
worktree (`go build ./cmd/...`). `paths` was confirmed before the run: tmux socket, state home,
cache and config all resolved under the run dir — nothing under `~/.local/state/sidecar` or
`~/.config/sidecar`. `stop` tore down both the host session and the private inner server.

Setup: one plain shell (`Shell 1`, agent None), then a `Terminal split` created from the create
modal (Auto placement) — two live terminals, sidebar row badged `◧◨`,
`sidecar-sh-…-1` at 55x45 and `sidecar-tp-sidecar-sh-…-1` at 54x45.

| snapshot | what it shows |
| --- | --- |
| `L1-modal-termsplit-disabled` | with two terminals on screen, the create modal's **Terminal split** row renders muted, and the form under it states the rule: `Two terminals are already on screen — close one first`. The placement row (`Auto/Right/Below`) and `Create` are muted too. |
| `L1b-enter-refused` | Enter on that disabled form is refused: the modal stays open, and `panes` still lists exactly the same two sessions — no third was created. |
| `L2a-before-click` | the split terminal's header carries the `×` (column 196, row 3); the primary's header deliberately does not. |
| `L2b-after-close-click` | an SGR click on that `×` closes the split: the primary is back to 115x45 full width, the `◧◨` badge is gone, and the split's tmux session `sidecar-tp-…` survives (the confirm is skipped because only the login shell was running). |
| `L2c-primary-focus-typed` | focus landed in the primary terminal: Enter enters INTERACTIVE and `echo PRIMARY-FOCUS-OK` runs there. |
| `L3a-two-terminals` | the split recreated, two live terminals again. |
| `L3b-split-focused` | a click into the split terminal focuses it — its header shows `INTERACTIVE ctrl+\ exit ×`. |
| `L3c-after-exit-split` | `exit` typed in the **split** terminal: the pane closes, the split collapses, the primary is back to 115x45, and `panes` lists only `sidecar-sh-…-1` — the `sidecar-tp-…` session is gone. No wedge; a `⚠ Session ended` flash reports it. |
| `L3d-primary-focus-after-exit` | the primary is immediately focusable again: `echo PRIMARY-ALIVE-AFTER-SPLIT-EXIT` runs in it. |
| `L4-after-exit-primary` | `exit` in the primary/last terminal keeps today's behavior: the shell is removed, the page falls back to the worktree list, and no sidecar-created panes remain. |

No fallout needed fixing in this pass.
