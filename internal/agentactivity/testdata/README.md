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

Claude, Grok, and Antigravity are installed and version-pinned below, but their
runtime state harvest and probes are Phase 2. No synthetic state fixtures were
created. Their availability records deliberately document every unavailable
Phase 0 state so Phase 1 cannot accidentally claim provider support.

Herdr compatibility provenance remains pinned in
`docs/plans/active/td-48ecf2-workspace-agent-activity-status.md` at commit
10974c822d607f03e20e9741ec027910f0c1f93a. Current Codex 0.147.0 agrees with
the pinned title spinner, `Action Required`, working row, and composer rules;
the current transcript viewer uses `/ T R A N S C R I P T /` plus `q to quit`.
