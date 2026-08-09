#!/bin/bash
# Build a deterministic, disposable repository for the terminal-cutover proof.
# The root doubles as SIDECAR_DRIVE_RUN_DIR, so config, state, tmux sockets and
# proof artifacts all remain below one unique scratch directory.
set -euo pipefail

ROOT="${1:-}"
if [ -z "$ROOT" ]; then
    ROOT=$(mktemp -d /tmp/sidecar-terminal-cutover.XXXXXX)
else
    case "$ROOT" in
        /*) ;;
        *) echo "fixture root must be absolute" >&2; exit 1 ;;
    esac
    if [ -e "$ROOT" ] && [ -n "$(ls -A "$ROOT" 2>/dev/null)" ]; then
        echo "fixture root must not exist or must be empty: $ROOT" >&2
        exit 1
    fi
    mkdir -p "$ROOT"
fi

case "$ROOT" in
    "/"|"$HOME"|"$HOME"/*)
        echo "fixture root must be a scratch directory outside HOME" >&2
        exit 1
        ;;
esac

MAIN="$ROOT/main"
mkdir -p "$MAIN" "$ROOT/config" "$ROOT/editors"
git -C "$MAIN" init -q -b main
git -C "$MAIN" config user.name "Sidecar Proof"
git -C "$MAIN" config user.email "sidecar-proof@invalid"
printf '%s\n' 'MAIN_PROOF_BASELINE' > "$MAIN/a-proof.txt"
printf '%s\n' 'SECOND_TAB_BASELINE' > "$MAIN/b-proof.txt"
printf '%s\n' '# Terminal proof note' '' 'NOTES_BASELINE' > "$MAIN/proof-note.md"
git -C "$MAIN" add a-proof.txt b-proof.txt proof-note.md
git -C "$MAIN" commit -q -m "terminal proof fixture"
git -C "$MAIN" worktree add -q -b proof-a "$ROOT/worktree-a"
git -C "$MAIN" worktree add -q -b proof-b "$ROOT/worktree-b"

printf '%s\n' '#!/bin/bash' 'exec nvim -u NONE -i NONE "$@"' > "$ROOT/editors/nvim-proof"
printf '%s\n' '#!/bin/bash' 'exec nano "$@"' > "$ROOT/editors/nano-proof"
chmod 700 "$ROOT/editors/nvim-proof" "$ROOT/editors/nano-proof"

cat > "$ROOT/config/config.json" <<JSON
{
  "projects": {"mode": "single", "root": "."},
  "plugins": {
    "workspace": {
      "defaultAgentType": "codex",
      "agents": ["codex"],
      "tmuxCaptureMaxBytes": 1048576
    },
    "notes": {"defaultEditor": "nvim"}
  },
  "features": {"flags": {"notes_plugin": true}}
}
JSON

printf 'fixture root:  %s\n' "$ROOT"
printf 'launch repo:   %s\n' "$MAIN"
printf 'worktree A:    %s\n' "$ROOT/worktree-a"
printf 'worktree B:    %s\n' "$ROOT/worktree-b"
printf 'config:        %s\n' "$ROOT/config/config.json"
printf '\nRun with:\n'
printf 'SIDECAR_DRIVE_RUN_DIR=%q SIDECAR_DRIVE_REPO=%q EDITOR=%q ./scripts/tmux-drive.sh paths\n' \
    "$ROOT" "$MAIN" "$ROOT/editors/nvim-proof"
