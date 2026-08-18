# Cursor Agent CLI evidence and proof record

- Availability: no live Cursor subscription on the harvest machine. Desktop
  Cursor is installed; `cursor-agent` / `agent` CLI was **not** executed.
- Authority for rules:
  1. Herdr Cursor screen manifest **2026.08.03.1** (bundled + herdr.dev remote).
  2. Static string harvest of Homebrew cask `cursor-cli` **2026.08.04-aaa8809**
     (`https://downloads.cursor.com/lab/2026.08.04-aaa8809/darwin/arm64/agent-cli-package.tar.gz`).
     Process was never launched; only package JS/strings were inspected.
- Identity follows Herdr (`identify_agent` / `identify_agent_in_job`): process
  name or resolved argv0, not screen text. `cursor-agent` / `cursor` /
  `cursor-agent.cmd` are Cursor. Bare `agent` is Cursor only when `$PATH`
  `agent` resolves to `cursor-agent` (Herdr's symlink test) or the live
  header/tagline ("Cursor Agent" / "Plan, search, build anything") is on
  screen. `node` is never Cursor — that comm name is Codex's npm launcher.
- Session files: `~/.cursor/chats/.../store.db` remain conversation history only.
  Workspace intentionally does not use SQLite mtime for activity when
  `agentactivity` supports the provider (same model as Herdr: hooks = identity,
  screen = status).
- Explicitly unavailable: live pane text/PNG captures, real interruption,
  transcript/viewer overlays, and any authenticated turn.
- False-positive boundaries:
  - Approvals need full live vocabulary (options/hints), not bare discussion.
  - `Run Everything` approval-mode chrome must not classify as blocked
    (Herdr fae0b236).
  - `Finished` / `N background tasks` is a completion group title, not working.
  - Process mismatch (e.g. zsh) never classifies.
  - Identity is process-or-alias, not activity phrases. Generic singles
    (`ctrl+c to stop`, `Add a follow-up`, `Waiting for approval`,
    `Run this command?`, `Write to this file?`) classify activity only
    after the pane is already Cursor. They must not claim a `node` pane.
- Isolated proof: fixtures + unit tests only; no real state tree or default
  tmux server was touched.
