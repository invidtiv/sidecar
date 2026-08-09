#!/bin/bash
# Build a disposable, behavior-faithful cross-project Overview fixture.
#
# Usage:
#   scripts/cross-project-overview-fixture.sh FIXTURE_ROOT DRIVE_ROOT COUNT
#   scripts/cross-project-overview-fixture.sh transition-done FIXTURE_ROOT DRIVE_ROOT
#
# Run `SIDECAR_DRIVE_RUN_DIR=DRIVE_ROOT SIDECAR_DRIVE_REPO=FIXTURE_ROOT/projects/project-01
# scripts/tmux-drive.sh paths` before setup. The fixture uses only the private
# tmux socket and Sidecar state/config tree below DRIVE_ROOT.
set -euo pipefail

die() { echo "$*" >&2; exit 1; }

canonical_scratch_path() {
    local raw="$1" parent base canonical_parent canonical system_tmp env_tmp
    case "$raw" in /*) ;; *) die "path must be absolute: $raw" ;; esac
    case "/$raw/" in */./*|*/../*) die "path cannot contain dot components: $raw" ;; esac
    case "$raw" in *[!A-Za-z0-9_./-]*) die "path contains unsupported characters: $raw" ;; esac
    parent=$(dirname "$raw")
    base=$(basename "$raw")
    [ -d "$parent" ] || die "path parent must exist: $parent"
    canonical_parent=$(cd "$parent" && pwd -P)
    canonical="$canonical_parent/$base"
    if [ -e "$raw" ]; then
        [ -d "$raw" ] || die "path is not a directory: $raw"
        [ "$(cd "$raw" && pwd -P)" = "$canonical" ] || die "path cannot traverse a symlink: $raw"
    fi
    system_tmp=$(cd /tmp && pwd -P)
    env_tmp=$(cd "${TMPDIR:-/tmp}" && pwd -P)
    case "$canonical" in "$system_tmp"/*|"$env_tmp"/*) ;; *) die "path must be below /tmp or TMPDIR: $raw" ;; esac
    printf '%s\n' "$canonical"
}

write_worktree_state() {
    local worktrees_dir="$1" worktree="$2" provider="$3" task="$4" key state_dir
    key=$(printf '%s' "$worktree" | shasum -a 256 | awk '{print $1}')
    state_dir="$worktrees_dir/$(basename "$worktree")-${key:0:12}"
    mkdir -p "$state_dir"
    printf '{"path":"%s","key":"%s"}\n' "$worktree" "$key" > "$state_dir/meta.json"
    printf '%s\n' "$provider" > "$state_dir/agent"
    printf '%s\n' "$task" > "$state_dir/task"
}

start_agent_pane() {
    local socket="$1" session="$2" cwd="$3" command="$4" evidence="$5" title="$6"
    tmux -S "$socket" new-session -d -s "$session" -c "$cwd" "$command '$evidence'"
    tmux -S "$socket" select-pane -t "$session:" -T "$title"
}

if [ "${1:-}" = "transition-done" ]; then
    [ "$#" -eq 3 ] || die "usage: $0 transition-done FIXTURE_ROOT DRIVE_ROOT"
    fixture=$(canonical_scratch_path "$2")
    drive=$(canonical_scratch_path "$3")
    socket="$drive/tmux/tmux-$(id -u)/default"
    [ -S "$socket" ] || die "private tmux socket not found: $socket"
    tmux -S "$socket" respawn-pane -k -t 'sidecar-ws-agent-05:' \
        "$fixture/bin/codex '$fixture/evidence/codex-idle.txt'"
    echo "transitioned overview-transition to explicit Codex idle evidence"
    exit 0
fi

[ "$#" -eq 3 ] || die "usage: $0 FIXTURE_ROOT DRIVE_ROOT COUNT"
fixture=$(canonical_scratch_path "$1")
drive=$(canonical_scratch_path "$2")
count="$3"
case "$count" in 1|10|30) ;; *) die "COUNT must be 1, 10, or 30" ;; esac

if [ -e "$fixture" ]; then
    [ -z "$(ls -A "$fixture" 2>/dev/null)" ] || die "fixture root must be empty: $fixture"
else
    mkdir -p "$fixture"
fi
mkdir -p "$drive"
for path in "$drive/config" "$drive/state" "$drive/cache" "$drive/tmux"; do
    [ ! -e "$path" ] || die "drive path already populated before fixture setup: $path"
done

mkdir -p "$fixture/projects" "$fixture/evidence" "$fixture/bin" "$drive/config" \
    "$drive/state/sidecar/projects" "$drive/cache" "$drive/tmux"
cp "$(dirname "$0")/../internal/agentactivity/testdata/codex/working.txt" "$fixture/evidence/codex-working.txt"
cp "$(dirname "$0")/../internal/agentactivity/testdata/codex/blocked.txt" "$fixture/evidence/codex-blocked.txt"
cp "$(dirname "$0")/../internal/agentactivity/testdata/codex/startup_idle.txt" "$fixture/evidence/codex-idle.txt"
cp "$(dirname "$0")/../internal/agentactivity/testdata/claude/blocked.txt" "$fixture/evidence/claude-blocked.txt"

renderer_src="$fixture/renderer.go"
cat > "$renderer_src" <<'GO'
package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) != 2 {
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		panic(err)
	}
	// Real agent TUIs keep their current prompt/status at the viewport bottom.
	// Scroll the sourced evidence there so production bottom-window probes see
	// the same shape even when Sidecar resizes a proof pane.
	fmt.Print(strings.Repeat("\n", 100))
	fmt.Print(string(b))
	for {
		time.Sleep(time.Hour)
	}
}
GO
go build -o "$fixture/bin/codex" "$renderer_src"
cp "$fixture/bin/codex" "$fixture/bin/claude"
rm "$renderer_src"

real_count="$count"
if [ "$count" -ge 10 ]; then
    real_count=$((count - 3))
fi

projects_json=""
for i in $(seq 1 "$real_count"); do
    id=$(printf '%02d' "$i")
    root="$fixture/projects/project-$id"
    mkdir -p "$root"
    git -C "$root" init -q -b main
    git -C "$root" config user.name "Sidecar Proof"
    git -C "$root" config user.email "sidecar-proof@invalid"
    printf 'project %s\n' "$id" > "$root/README.txt"
    git -C "$root" add README.txt
    git -C "$root" commit -q -m "overview fixture $id"
    name="Project $id"
    if [ "$i" -le 2 ]; then name="Twin"; fi
    entry=$(printf '{"name":"%s","path":"%s"}' "$name" "$root")
    if [ -n "$projects_json" ]; then projects_json="$projects_json,"; fi
    projects_json="$projects_json$entry"

    project_state="$drive/state/sidecar/projects/project-$id"
    mkdir -p "$project_state/worktrees"
    printf '{"path":"%s"}\n' "$root" > "$project_state/meta.json"
    provider="codex"
    evidence="$fixture/evidence/codex-idle.txt"
    case "$i" in
        1) evidence="$fixture/evidence/codex-working.txt" ;;
        2) evidence="$fixture/evidence/codex-blocked.txt" ;;
        4) provider="unsupported" ;;
        5) evidence="$fixture/evidence/codex-working.txt" ;;
    esac
    agent_worktree="$fixture/projects/agents-$id/agent-$id"
    mkdir -p "$(dirname "$agent_worktree")"
    git -C "$root" worktree add -q -b "agent-$id" "$agent_worktree"
    write_worktree_state "$project_state/worktrees" "$agent_worktree" "$provider" "td-proof-$id"

    if [ "$i" -ne 4 ]; then
        # Match Sidecar's production worktree-session naming so the real
        # Workspaces plugin reconnects rather than treating metadata as ended.
        session="sidecar-ws-agent-$id"
        # Sessions are started after the socket directory is ready below.
        printf '%s\t%s\t%s\t%s\t%s\n' "$session" "$agent_worktree" "$fixture/bin/codex" "$evidence" "OpenAI Codex (proof)" >> "$fixture/panes.tsv"
    fi
done

if [ "$real_count" -ge 2 ]; then
    for i in 1 2; do
        id=$(printf '%02d' "$i")
        root="$fixture/projects/project-$id"
        shared="$fixture/projects/shared-$id/shared"
        mkdir -p "$(dirname "$shared")"
        git -C "$root" worktree add -q -b "shared-$id" "$shared"
        project_state="$drive/state/sidecar/projects/project-$id"
        evidence="$fixture/evidence/codex-idle.txt"
        [ "$i" -eq 1 ] && evidence="$fixture/evidence/codex-working.txt"
        write_worktree_state "$project_state/worktrees" "$shared" "codex" "td-shared-$id"
        printf 'overview-shared-%s\t%s\t%s\t%s\t%s\n' "$id" "$shared" "$fixture/bin/codex" "$evidence" "OpenAI Codex (shared)" >> "$fixture/panes.tsv"
    done

    namespace="$drive/tmux/tmux-$(id -u)/default"
    p1_state="$drive/state/sidecar/projects/project-01"
    p2_state="$drive/state/sidecar/projects/project-02"
    cat > "$p1_state/shells.json" <<JSON
{"version":1,"shells":[
  {"tmuxName":"overview-agent-shell","displayName":"Agent shell","namespace":"$namespace","agentType":"claude"},
  {"tmuxName":"overview-plain-shell","displayName":"Plain shell","namespace":"$namespace"},
  {"tmuxName":"overview-collision","displayName":"Collision A","namespace":"$namespace","agentType":"codex"}
]}
JSON
    cat > "$p2_state/shells.json" <<JSON
{"version":1,"shells":[
  {"tmuxName":"overview-collision","displayName":"Collision B","namespace":"$namespace","agentType":"codex"}
]}
JSON
    printf 'overview-agent-shell\t%s\t%s\t%s\t%s\n' "$fixture/projects/project-01" "$fixture/bin/claude" "$fixture/evidence/claude-blocked.txt" "Claude proof" >> "$fixture/panes.tsv"
    printf 'overview-collision\t%s\t%s\t%s\t%s\n' "$fixture/projects/project-01" "$fixture/bin/codex" "$fixture/evidence/codex-idle.txt" "OpenAI Codex (collision)" >> "$fixture/panes.tsv"
fi

if [ "$count" -ge 10 ]; then
    first="$fixture/projects/project-01"
    projects_json="$projects_json,$(printf '{"name":"Canonical duplicate","path":"%s"}' "$first")"
    mkdir -p "$fixture/projects/non-git"
    projects_json="$projects_json,$(printf '{"name":"Broken non-Git","path":"%s"}' "$fixture/projects/non-git")"
    projects_json="$projects_json,$(printf '{"name":"Missing project","path":"%s"}' "$fixture/projects/missing")"
fi

cat > "$drive/config/config.json" <<JSON
{
  "projects": {"mode":"single","root":".","list":[$projects_json]},
  "plugins": {
    "workspace": {
      "agents":["codex","claude"],
      "tmuxCaptureMaxBytes":1048576,
      "autoCreateShell":false
    }
  },
  "features":{"flags":{"cross_project_overview":true}},
  "ui":{"showClock":false,"nerdFontsEnabled":false,"theme":{"name":"default"}}
}
JSON

socket="$drive/tmux/tmux-$(id -u)/default"
mkdir -p "$(dirname "$socket")"
chmod 700 "$(dirname "$socket")"
while IFS=$'\t' read -r session cwd command evidence title; do
    [ -n "$session" ] || continue
    start_agent_pane "$socket" "$session" "$cwd" "$command" "$evidence" "$title"
done < "$fixture/panes.tsv"
if [ "$real_count" -ge 2 ]; then
    mkdir -p "$fixture/plain-shell-cwd"
    tmux -S "$socket" new-session -d -s overview-plain-shell -c "$fixture/plain-shell-cwd"
fi

printf 'fixture root: %s\n' "$fixture"
printf 'drive root:   %s\n' "$drive"
printf 'launch repo:  %s\n' "$fixture/projects/project-01"
printf 'configured:   %s\n' "$count"
printf 'real repos:   %s\n' "$real_count"
printf 'inner socket: %s\n' "$socket"
