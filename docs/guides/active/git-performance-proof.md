# Git plugin performance proof

Use this harness to reproduce Git plugin startup, subprocess, interaction, and
rendering measurements. It deliberately keeps both tmux and Sidecar state away
from the live application. Never substitute the default tmux server or a live
repository.

## Create an isolated fixture

```bash
PROOF_ROOT=$(mktemp -d /tmp/sidecar-git-proof.XXXXXX)
PROOF_REPO="$PROOF_ROOT/repo"
PROOF_BIN="$PROOF_ROOT/sidecar"
PROOF_LOG="$PROOF_ROOT/processes.jsonl"
mkdir -p "$PROOF_REPO" "$PROOF_ROOT/shims"
git -C "$PROOF_REPO" init -b main
git -C "$PROOF_REPO" config user.name 'Sidecar Proof'
git -C "$PROOF_REPO" config user.email sidecar@example.test
printf 'base\n' >"$PROOF_REPO/tracked.txt"
git -C "$PROOF_REPO" add tracked.txt
git -C "$PROOF_REPO" commit -m base
printf 'modified\n' >>"$PROOF_REPO/tracked.txt"
for n in $(seq 1 1000); do
  mkdir -p "$PROOF_REPO/untracked/$((n / 100))"
  printf '%s\n' "$n" >"$PROOF_REPO/untracked/$((n / 100))/file-$n.txt"
done
go build -o "$PROOF_BIN" ./cmd/sidecar
```

Record every Git invocation with a shim. Resolve the real executable before
putting the shim first on `PATH`, and pass arguments directly so unusual paths
are preserved:

```bash
REAL_GIT=$(command -v git)
export REAL_GIT PROOF_LOG
cat >"$PROOF_ROOT/shims/git" <<'SHIM'
#!/bin/bash
{
  printf '%s pid=%s git' "$(date -u +%FT%TZ)" "$$"
  printf ' %q' "$@"
  printf '\n'
} >>"$PROOF_LOG"
exec "$REAL_GIT" "$@"
SHIM
chmod +x "$PROOF_ROOT/shims/git"
```

The Bash-escaped argument log preserves argument boundaries, including spaces
and option-like filenames, while remaining easy to count and inspect.

## Drive the real application

Give every run a unique driver root. `tmux-drive.sh paths` must show a private
`TMUX_TMPDIR`, `XDG_STATE_HOME`, cache, and `-config` path; none may resolve
under `~/.local/state/sidecar` or `~/.config/sidecar`.

```bash
export SIDECAR_DRIVE_RUN_DIR="$PROOF_ROOT/drive"
export SIDECAR_DRIVE_REPO="$PROOF_REPO"
export SIDECAR_BIN="$PROOF_BIN"
export PATH="$PROOF_ROOT/shims:$PATH"
export SIDECAR_STARTUP_TRACE=stderr
./scripts/tmux-drive.sh paths
./scripts/tmux-drive.sh start 200 50
sleep 3
./scripts/tmux-drive.sh snap git-startup
./scripts/tmux-drive.sh keys 2
sleep 2
./scripts/tmux-drive.sh snap git-status
./scripts/tmux-drive.sh stop
```

Capture text and PNG before, during, and after stage/unstage actions. Repeat an
intentionally failing write and an external file change, then repeat the flow
from a linked worktree. Check that the header/footer stay fixed, input remains
responsive, the final status/diff/selection is correct, and the process log
shows one Git write per logical action plus bounded, coalesced reads.

Confirm the trace's `sidecar paths` line names `PROOF_REPO` as `project-root`.
The startup trace must reach `first ready frame` without Git plugin filesystem
writes or process spawns from `Init` or before `Start` returns. Repository
detection and initial loads may run only after Bubble Tea executes the returned
commands. Opening an existing repository must not edit `.gitignore`; Sidecar
adds its local-state entries only when the user explicitly initializes a new
repository from the Git plugin.

## Renderer decision gate

Run the representative 1,000-file status renderer benchmark:

```bash
go test ./internal/plugins/gitstatus -run '^$' -bench BenchmarkViewThousandFiles -benchmem -count 5
```

Optimize only when repeated measurements exceed one frame (about 16 ms) or a
profile attributes meaningful interactive latency to rendering. Record the
baseline and comparison together; do not infer a renderer problem from status
loading or Git subprocess time.
