# Agent activity runtime evidence

Harvested 2026-08-08 on macOS from Sidecar-named tmux sessions at 120x40.
Terminal captures are reduced to the smallest stable, sanitized evidence rows;
no conversation content, paths outside the repository, or environment values are
retained. Metadata was captured atomically with `tmux display-message` fields.

Versions: tmux 3.6b; Codex CLI 0.147.0; Claude Code 2.1.220; Grok 1.0.0
(`3cd0d0cbcebe`, stable); Antigravity 1.1.11.

Codex is exhaustive for the Phase 1 steel thread: startup/idle, working, tool
execution, permission blocker, interruption, completion, transcript viewer, and
exit. `pane_current_command` is `node` for the installed Codex npm launcher;
the direct process tree was zsh -> node/Codex. The exact real pane PID, TPGID,
foreground process-group rows, sanitized commands, and reproducible capture
commands are committed in `codex/process_identity.txt`. Working and blocker title changes
were independently observed. The canceled blocker command did not execute.

Claude, Grok, and Antigravity are installed and version-pinned below, and Phase
2 harvested their current output on the isolated tmux socket
`sidecar-phase2-fixtures-95423`. Their minimal fixtures now cover:

- Claude: real idle, title/screen working, AskUserQuestion blocker, and
  interruption. The model-picker fixture remains a compatibility fixture from
  the pinned Herdr manifest because changing the active model/settings was not
  safe or necessary for runtime proof. A filesystem permission prompt was not
  available in the installed manual-mode configuration and is not synthesized.
- Grok: real idle, interruption, and startup/resume overlay. The installed
  non-tool turn completed before the capture interval; working remains a
  pinned-Herdr compatibility fixture. No safe blocker was available without
  attempting a consequential tool action, so no blocker is synthesized. Pane
  title and screen are used; OSC 9;4 is explicitly deferred because tmux does
  not expose it after consumption.
- Antigravity: real trust blocker, working, interrupted, and completed screens.
  The completed screen deliberately exercises `known-live-fallback`; the
  installed UI exposes no stable explicit idle marker beyond its composer.
  No additional permission blocker was safely available and none is synthesized.

Claude, Grok, and Antigravity were originally installed and version-pinned, but
their runtime state harvest and probes are Phase 2. Compatibility fixtures are
identified explicitly; unavailable real states are never represented as real
captures. Their availability records preserve the earlier Phase 0 limitations.

Herdr compatibility provenance remains pinned in
`docs/plans/active/td-48ecf2-workspace-agent-activity-status.md` at commit
10974c822d607f03e20e9741ec027910f0c1f93a. Current Codex 0.147.0 agrees with
the pinned title spinner, `Action Required`, working row, and composer rules;
the current transcript viewer uses `/ T R A N S C R I P T /` plus `q to quit`.

## Spinner frames are provider-specific (2026-08-09)

`claude/background-agents.txt` was harvested from a live pane whose main loop
had gone idle while background agents kept running. Claude Code drops
`esc to interrupt` from the bottom bar the moment the main loop stops, so the
prompt box alone reads as a finished turn; the title's braille frame and the
`N/M agents done` line are the only evidence work is still in flight. Task and
worktree names are redacted; the U+2810 title frame is verbatim, because that
frame is the evidence.

Measured the same day across every Claude pane on the machine: braille title
prefix while busy, U+2733 (`✳`) once fully idle. That matches the Herdr
`claude` manifest 2026.08.04.1, which matches the whole Braille block
(U+2800-U+28FF) for working and `^✳ ` for idle, while its `codex`
manifest keeps the narrow ten-frame dots set. Sidecar previously shared one
eleven-glyph pattern across Claude, Codex and Grok; each provider now owns its
own. See the package doc in `activity.go` before adding another.
