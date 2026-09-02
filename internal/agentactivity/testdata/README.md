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

## After the Phase 2 manifest cutover (2026-09-01)

Every fixture here is now classified by Herdr's vendored manifest for its agent
(plus Sidecar's overlay), not by a Go rule table. `TestFixtureCensus` walks this
whole directory, prints what each screen resolves to and which rule id said so,
and fails when a fixture's own `state:` header disagrees with the verdict.
`sidecar agent explain --file <fixture> --agent <kind>` reproduces any row, and
`--print-window` prints the exact text detection saw.

Four fixtures were added during the cutover, and what each of them is for is the
point of adding it:

- `codex/trust_directory.txt` (synthetic) — the only upstream rule that reads
  the *top* of the read window. It is padded out to its declared 40 rows the way
  `tmux capture-pane` pads a real capture, which is what makes it able to catch a
  window that starts one row too low.
- `codex/approval_prompt.txt` (synthetic) — the same screen as `blocked.txt` with
  an ordinary title. Upstream's two screen blockers are both defined relative to
  the Codex prompt marker and this prompt puts the marker on its own option line,
  so neither can fire and only `sidecar.approval_blocker` catches it.
- `claude/allow_prompt.txt` (synthetic) — the tool-permission prompt that carries
  none of the literals upstream's three permission rules are gated on.
- `muse/trust_workspace.txt` (**real**, 2026-09-01, isolated socket, 120x40) — an
  unanswered "Do you trust this workspace?" prompt, captured live. This is the
  clearest thing the cutover buys: the deleted Go rule table read it as idle, so
  a pane sitting on a question Sidecar could not answer showed as a finished turn.

A synthetic fixture says so in its own header and says why it could not be
captured. That convention predates the cutover and still holds: an unavailable
real state is never represented as a real capture.

## Three overlay fixes after the cutover (2026-09-01)

Five more fixtures, taking the directory to 61. Three of them are about screens
that read *idle* while something was still owed to the user, which is the same
failure the whole plan opened with.

- `claude/waiting_background_agents.txt` (**real**, Claude Code 2.1.257,
  pane_height 57) — the main loop parked on a background subagent, reported by
  the user from their own pane. Claude paints two signals for it and upstream
  reads neither: `background_agents_working` reads the single last non-empty
  line above the prompt box, which on this screen is a
  `✔ Update installed · Restart to update` banner, and
  `background_shell_working` reads the footer for `· N shells ·` where 2.1.257
  writes `· ← 3 agents ·`. The pane resolved to `live_prompt_box`, an explicit
  visible idle, and announced a completed turn. Reduced to the evidence rows:
  all conversation text, the composer's own text and the session link are gone,
  and the subagent's task description is redacted. The waiting row and the
  banner are verbatim, because those two rows *are* the evidence.
- `claude/background_agents_footer.txt` — the same pane one repaint later, with
  the waiting row scrolled out of the read window so the footer's own agent
  count is all that is left. It is the positive fixture for
  `sidecar.background_agents_footer_working`, which is the half the waiting rule
  cannot reach.
- `grok/allow_prompt.txt` (**synthetic and unproven**) — Grok's `Allow …?`
  permission prompt over an arrow-key control line, carried from the deleted
  pre-manifest `grok.screen.blocked`. Nothing has captured this prompt in any
  release, and the header says so twice. It is written down anyway because Grok
  is the one provider whose idle rule is a *visible* idle: an unanswered prompt
  upstream cannot describe does not degrade to a quiet fallback there, it
  announces that the turn is done. A live capture replaces this file.
- `claude/legacy_permission_wait.txt` and `codex/weak_blocker.txt` (both
  synthetic) — one screen each for the two upstream rules that declare a blocked
  state with no `visible_blocker`, transcribed from those rules' own literals.
  They exist so the overlays that restore Sidecar's attention nag have something
  to be proved against, and so a re-sync that changes either upstream rule shows
  up as a differential-harness failure rather than as silence.
