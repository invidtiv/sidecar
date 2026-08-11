# Cursor Agent CLI evidence and proof record

- Availability: no live Cursor subscription on the harvest machine. Desktop
  Cursor is installed; `cursor-agent` / `agent` CLI was **not** executed.
- Authority for rules:
  1. Herdr Cursor screen manifest **2026.08.03.1** (bundled + herdr.dev remote).
  2. Static string harvest of Homebrew cask `cursor-cli` **2026.08.04-aaa8809**
     (`https://downloads.cursor.com/lab/2026.08.04-aaa8809/darwin/arm64/agent-cli-package.tar.gz`).
     Process was never launched; only package JS/strings were inspected.
- Identity: launcher is a bash wrapper that `exec -a "$0"` the bundled node, so
  `pane_current_command` is `cursor-agent` (or `agent` / `cursor-agent.cmd`).
  Bare `agent` and `node` require Cursor screen chrome before Identify claims
  ownership (Grok and others also use `agent`).
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
- Isolated proof: fixtures + unit tests only; no real state tree or default
  tmux server was touched.
