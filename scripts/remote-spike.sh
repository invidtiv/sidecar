#!/bin/bash
# remote-spike.sh - Phase 0 harness for `sidecar host serve` against a real second machine.
#
# Isolation is the whole point of this script. The Phase 0 spike observes and
# drives a machine that is somebody's live workstation, and the two things that
# must survive it untouched are:
#
#   1. The remote machine's DEFAULT tmux server. It holds irreplaceable live
#      Sidecar shells and agent sessions. Every tmux session this script creates
#      lives on a private socket under a private TMUX_TMPDIR, and teardown only
#      ever kills that private server by its own socket path.
#   2. The remote machine's REAL Sidecar state tree
#      (~/.local/state/sidecar, ~/.config/sidecar). Every sidecar process this
#      script starts runs with XDG_STATE_HOME and -config pointed into the run
#      root, and with SIDECAR_ISOLATED_STATE=1 so the binary refuses to start at
#      all if anything still resolves back into the real tree.
#
# Isolating either axis alone is not enough — that is the td-8d18de lesson, and
# it is why `paths` exists and why you should run it before you trust anything
# else here.
#
# The binary is built locally from the working tree and copied to the run root
# on the host. The host's own installed sidecar is never invoked, never
# replaced, and never on the PATH this script uses.
#
#   ./scripts/remote-spike.sh paths                 - print every isolated root, local and remote
#   ./scripts/remote-spike.sh deploy                - build here, copy the binary to the host run root
#   ./scripts/remote-spike.sh fixture               - create the private remote tmux server + agent panes
#   ./scripts/remote-spike.sh serve [ARGS...]       - run serve on the host, JSONL to stdout
#   ./scripts/remote-spike.sh probe [ARGS...]       - run the local probe against the host
#   ./scripts/remote-spike.sh control <SESSION>     - raw proxied control-mode attach (prints tmux control protocol)
#   ./scripts/remote-spike.sh remote-tui            - run a full sidecar TUI on the host against the SAME isolated tree
#   ./scripts/remote-spike.sh ssh <CMD...>          - run a command on the host through the master connection
#   ./scripts/remote-spike.sh teardown              - kill ONLY the private remote server and remove the run root
#
# Environment:
#   SPIKE_HOST      ssh target (default: marcusbook)
#   SPIKE_RUN_DIR   remote run root (default: /tmp/sidecar-spike-$USER)

set -euo pipefail

# Never let an attached tmux client select a server implicitly.
unset TMUX

HOST="${SPIKE_HOST:-marcusbook}"
REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
RUN_DIR="${SPIKE_RUN_DIR:-/tmp/sidecar-spike-$(id -un)}"

# Refuse anything teardown must not be allowed to delete.
#
# teardown runs `rm -rf "$RUN_DIR"` on the REMOTE machine, so this guard is the
# only thing between a typo and someone else's data. It is deliberately
# stricter than "is it under /tmp":
#
#   - A trailing slash or an empty component is rejected, because a prefix glob
#     like /tmp/* matches "/tmp/" itself — one stray character turns the run
#     root into the whole temp tree.
#   - /var/folders is not an accepted base at all. On macOS the DEFAULT tmux
#     server's socket lives under $TMPDIR there, so an rm -rf with that base
#     destroys the exact thing this script exists to protect.
#   - The first component must be named for this harness, so the run root can
#     only ever be a directory this script created.
refuse_run_dir() {
    echo "refusing SPIKE_RUN_DIR='$RUN_DIR': $1" >&2
    exit 1
}
case "$RUN_DIR" in
    */)     refuse_run_dir "a trailing slash makes the temp root itself the target" ;;
    *//*)   refuse_run_dir "empty path component" ;;
esac
case "/$RUN_DIR/" in
    */../*|*/./*) refuse_run_dir "dot and dotdot components are not allowed" ;;
esac
case "$RUN_DIR" in
    /tmp/sidecar-spike*|/private/tmp/sidecar-spike*) ;;
    *) refuse_run_dir "must be /tmp/sidecar-spike* — teardown deletes this path recursively" ;;
esac
case "$RUN_DIR" in
    *[!A-Za-z0-9_./-]*) refuse_run_dir "unsupported characters" ;;
esac

REMOTE_BIN="$RUN_DIR/sidecar"
REMOTE_STATE="$RUN_DIR/state"
REMOTE_CONFIG_DIR="$RUN_DIR/config"
REMOTE_CONFIG="$REMOTE_CONFIG_DIR/config.json"
REMOTE_TMUX_TMPDIR="$RUN_DIR/tmux"
REMOTE_FIXTURE="$RUN_DIR/project"

# tmux derives its socket as $TMUX_TMPDIR/tmux-<uid>/default, so the private
# server's path depends on the REMOTE user's uid, not this machine's. Resolving
# it once and caching it keeps every later command addressing the same server.
REMOTE_UID_CACHE=""
remote_uid() {
    if [ -z "$REMOTE_UID_CACHE" ]; then
        REMOTE_UID_CACHE="$(ssh "${SSH_OPTS[@]}" "$HOST" 'id -u' | tr -d '[:space:]')"
        case "$REMOTE_UID_CACHE" in
            ''|*[!0-9]*) echo "could not resolve remote uid on $HOST" >&2; exit 1 ;;
        esac
    fi
    printf '%s' "$REMOTE_UID_CACHE"
}
remote_socket() { printf '%s/tmux-%s/default' "$REMOTE_TMUX_TMPDIR" "$(remote_uid)"; }

# One shared ControlMaster for everything this script does, so the per-command
# cost is a round trip rather than a full handshake. This is the same recipe
# internal/hosts builds for the app.
LOCAL_CTL_DIR="/tmp/sidecar-spike-ctl-$(id -u)"
mkdir -p "$LOCAL_CTL_DIR"
chmod 700 "$LOCAL_CTL_DIR"
SSH_OPTS=(
    -T
    -o ControlMaster=auto
    -o "ControlPath=$LOCAL_CTL_DIR/ctl-%C"
    -o ControlPersist=300
    -o ServerAliveInterval=15
    -o ServerAliveCountMax=4
    -o BatchMode=yes
)

# The remote environment prefix applied to every sidecar and tmux invocation.
# TMUX_TMPDIR is the only lever that moves sidecar's own tmux sessions, because
# sidecar unsets TMUX and never passes -L.
remote_env() {
    printf '%s' \
        "TMUX= TMUX_TMPDIR=$(printf %q "$REMOTE_TMUX_TMPDIR") " \
        "XDG_STATE_HOME=$(printf %q "$REMOTE_STATE") " \
        "SIDECAR_ISOLATED_STATE=1 "
}

# rsh runs a command on the host under a login shell, so Homebrew's bin
# directory is on PATH. A non-login `ssh host tmux` reports tmux as missing on
# a stock macOS host even though it is plainly installed — that is a real
# finding, not a quirk of this script.
# The command is base64'd on the way over. Quoting it instead means it is
# parsed by the local shell, escaped, re-parsed by sshd's shell, and re-parsed
# again by the login shell — three rounds that mangle nested quotes and any
# non-ASCII (a fixture's braille pane title, for instance) beyond recovery.
# Base64 is a single word with no metacharacters, so exactly one round of
# quoting applies and the remote login shell sees the command byte for byte.
rsh() {
    local encoded
    encoded="$(printf '%s' "$*" | base64 | tr -d '\n')"
    # Decode into `$SHELL -l -c "$D"`, never `$SHELL -l -s`. Feeding the login
    # shell on stdin makes it run its interactive preexec hooks, and on a stock
    # macOS zsh those write OSC 697 sequences to STDOUT — the same pipe the
    # JSONL protocol travels on. `-l -c` runs the same profile without them.
    ssh "${SSH_OPTS[@]}" "$HOST" "D=\$(echo $encoded | base64 --decode); \$SHELL -l -c \"\$D\""
}

# rsh_raw is rsh without the login shell, and without the base64 wrapper, so
# the caller keeps stdin. The control-mode attach needs both: stdin carries the
# commands, and stdout carries the tmux control protocol. The serve stream does
# NOT use this — it needs the login shell's PATH to find tmux, and pays for it
# by having to tolerate whatever the profile prints. See cmd_serve.
rsh_raw() {
    ssh "${SSH_OPTS[@]}" "$HOST" "$@"
}

# rtmux drives ONLY the private remote server, addressed by its socket path.
# The path is explicit rather than implied by TMUX_TMPDIR: an empty or unset
# TMUX_TMPDIR silently falls back to the DEFAULT server, and "tmux kill-server"
# against the default server is precisely the accident this whole script exists
# to make impossible.
rtmux() {
    rsh "TMUX= tmux -S $(printf %q "$(remote_socket)") $*"
}

cmd_paths() {
    echo "local:"
    echo "  repo             $REPO_DIR"
    echo "  ssh control dir  $LOCAL_CTL_DIR"
    echo "remote ($HOST):"
    echo "  run root         $RUN_DIR"
    echo "  binary           $REMOTE_BIN"
    echo "  XDG_STATE_HOME   $REMOTE_STATE"
    echo "  -config          $REMOTE_CONFIG"
    echo "  TMUX_TMPDIR      $REMOTE_TMUX_TMPDIR"
    echo "  tmux socket      $(remote_socket)"
    echo "  fixture project  $REMOTE_FIXTURE"
    echo
    echo "these must NOT appear above: ~/.local/state/sidecar, ~/.config/sidecar, /tmp/tmux-*/default"
    echo
    echo "remote reality check:"
    rsh "echo '  hostname        '\$(hostname); \
         echo '  default server  '\$(tmux ls 2>/dev/null | wc -l | tr -d ' ')' sessions (MUST BE PRESERVED)'; \
         echo '  private server  '\$(TMUX= tmux -S $(printf %q "$(remote_socket)") ls 2>/dev/null | wc -l | tr -d ' ')' sessions'; \
         echo '  real state tree '\$(ls ~/.local/state/sidecar 2>/dev/null | wc -l | tr -d ' ')' entries (MUST BE PRESERVED)'"
}

cmd_deploy() {
    echo "building darwin/arm64 from the working tree..."
    (cd "$REPO_DIR" && GOOS=darwin GOARCH=arm64 go build -o "/tmp/sidecar-spike-build" ./cmd/sidecar)
    rsh "mkdir -p $(printf %q "$RUN_DIR") $(printf %q "$REMOTE_STATE") $(printf %q "$REMOTE_CONFIG_DIR") $(printf %q "$REMOTE_TMUX_TMPDIR") && chmod 700 $(printf %q "$REMOTE_TMUX_TMPDIR")"
    # tmux will not create the tmux-<uid> directory itself under a custom
    # TMUX_TMPDIR, and refuses to bind its socket without it. Create the
    # PARENT only: creating the socket path itself as a directory makes every
    # later tmux command fail with "Socket operation on non-socket".
    rsh "mkdir -p \$(dirname $(printf %q "$(remote_socket)")) && chmod 700 \$(dirname $(printf %q "$(remote_socket)"))"
    # Delete before copying. scp overwrites in place, which keeps the inode —
    # and macOS caches a code signature against the vnode, so the second deploy
    # of a differing binary is SIGKILLed on exec (exit 137) with no diagnostic
    # anywhere. A fresh inode gets a fresh signature check.
    rsh "rm -f $(printf %q "$REMOTE_BIN")"
    scp "${SSH_OPTS[@]}" -q /tmp/sidecar-spike-build "$HOST:$REMOTE_BIN"
    rsh "chmod +x $(printf %q "$REMOTE_BIN")"
    # A config pointing at the fixture project only. The host's real config is
    # never read: -config moves config.json and state.json together.
    rsh "cat > $(printf %q "$REMOTE_CONFIG") <<'JSON'
{
  \"projects\": {
    \"mode\": \"single\",
    \"root\": \".\",
    \"list\": [{\"name\": \"spike\", \"path\": \"$REMOTE_FIXTURE\"}]
  }
}
JSON"
    echo "deployed: $(rsh "$(printf %q "$REMOTE_BIN") --version" | head -1)"
}

# cmd_fixture builds the observable surface: a git repo, a Sidecar shell
# manifest naming three sessions, and three tmux panes on the PRIVATE server
# replaying agent screen signatures.
cmd_fixture() {
    rsh "set -e
        mkdir -p $(printf %q "$REMOTE_FIXTURE")
        cd $(printf %q "$REMOTE_FIXTURE")
        if [ ! -d .git ]; then
            git init -q .
            git config user.email spike@example.com
            git config user.name Spike
            echo spike > README.md
            git add README.md
            git commit -qm 'spike fixture'
        fi"
    # Shell manifest naming the sessions, in the v2 shape workspaceinventory
    # reads. Written into the ISOLATED state tree, never the real one.
    local state_project="$REMOTE_STATE/sidecar/projects/spike"
    # Remove any other project directory in the isolated tree first. Sidecar
    # allocates a new slug directory whenever it cannot match an existing
    # meta.json, and projectdir.Lookup returns the FIRST directory whose
    # meta.json matches — so a stale duplicate registered under the
    # non-canonical /tmp path shadows the real one and the manifest silently
    # reads empty. Keeping exactly one project directory keeps the fixture
    # honest about which manifest is being read.
    rsh "find $(printf %q "$REMOTE_STATE/sidecar/projects") -mindepth 1 -maxdepth 1 -type d ! -name spike -exec rm -rf {} + 2>/dev/null || true"
    rsh "mkdir -p $(printf %q "$state_project")"
    # A project's state directory is found by its meta.json, never by its
    # directory name (projectdir.findByMeta). Without this file the manifest
    # below is simply invisible: readShells is only ever asked for a directory
    # that meta.json already mapped back to the project root.
    # meta.json must record the CANONICAL path. /tmp is a symlink to
    # /private/tmp on macOS, and projectdir matches on the resolved path: a
    # meta.json saying /tmp/... is not found for a project whose root resolves
    # to /private/tmp/..., so Sidecar silently allocates a SECOND project
    # directory and reads an empty manifest from it. The symptom is a Sessions
    # browser with no shells in it and no error anywhere.
    local canonical; canonical="$(rsh "cd $(printf %q "$REMOTE_FIXTURE") && pwd -P" | tr -d '[:space:]')"
    rsh "printf %s $(printf %q "{\"path\": \"$canonical\"}") > $(printf %q "$state_project/meta.json")"
    # The manifest's namespace must be the tmux SOCKET PATH, not a friendly
    # label: tmuxenv.Namespace() is the socket path, and RefreshProjectStatus
    # refuses to correlate a shell row to a pane unless the two match exactly.
    # A wrong namespace does not error — the rows simply never go live, which
    # is a much harder thing to notice.
    local namespace; namespace="$(remote_socket)"
    rsh "cat > $(printf %q "$state_project/shells.json") <<'JSON'
{
  \"version\": 2,
  \"shells\": [
    {\"tmuxName\": \"spike-claude\", \"displayName\": \"Claude pane\", \"agentType\": \"claude\", \"namespace\": \"NAMESPACE\"},
    {\"tmuxName\": \"spike-codex\", \"displayName\": \"Codex pane\", \"agentType\": \"codex\", \"namespace\": \"NAMESPACE\"},
    {\"tmuxName\": \"spike-opencode\", \"displayName\": \"Opencode pane\", \"agentType\": \"opencode\", \"namespace\": \"NAMESPACE\"},
    {\"tmuxName\": \"spike-plain\", \"displayName\": \"Plain shell\", \"namespace\": \"NAMESPACE\"}
  ]
}
JSON"
    rsh "sed -i '' s#NAMESPACE#$(printf %q "$namespace")#g $(printf %q "$state_project/shells.json")"
    # Panes on the PRIVATE server only. Each runs a replay script that paints a
    # provider's screen signature and holds it, so the real detectors run over
    # real terminal output without spending tokens or needing auth.
    # Agent panes are created by `replay`, which knows what command each one
    # must run. Only the plain shell is created here, and it is held open by a
    # blocking read for the same reason the replay panes are.
    local socket; socket="$(remote_socket)"
    rsh "TMUX= tmux -S $(printf %q "$socket") has-session -t spike-plain 2>/dev/null || \
         tmux -S $(printf %q "$socket") new-session -d -s spike-plain -c $(printf %q "$REMOTE_FIXTURE") -x 120 -y 40 'read _hold'"
    # spike-bench runs a real interactive shell. The replay panes cannot serve
    # for throughput and input measurement: their foreground process is the
    # hold binary, which discards stdin and produces no output, so there is
    # nothing to type at and nothing to burst.
    rsh "TMUX= tmux -S $(printf %q "$socket") has-session -t spike-bench 2>/dev/null || \
         tmux -S $(printf %q "$socket") new-session -d -s spike-bench -c $(printf %q "$REMOTE_FIXTURE") -x 120 -y 40 'PS1=\"bench\\$ \" bash --norc --noprofile -i'"
    echo "fixture repo ready at $REMOTE_FIXTURE"
    rtmux ls
}

# cmd_replay makes one remote pane look exactly like a real agent pane.
#
#   replay <session> <fixture>
#
# The fixtures are this repo's own captured agent screens
# (internal/agentactivity/testdata/<provider>/*.txt), each carrying the three
# things the detectors actually read: pane_title, pane_current_command, and the
# screen text. Replaying all three is what makes this a test of the real
# detection stack rather than a test of a screen I invented.
#
# pane_current_command is the subtle one. DetectClaude refuses any pane whose
# command is not claude/node/bun/<semver>, so a pane running bash is reported
# as a process mismatch no matter what is painted in it. The trick is to run a
# COPY of /bin/sh named for the command the fixture recorded, so tmux's
# #{pane_current_command} reports that name. It has to keep the process in the
# foreground too — a trailing `sleep` would make the pane's command "sleep" —
# so it blocks on the `read` builtin instead.
cmd_replay() {
    local session="${1:?usage: replay <session> <fixture>}"
    local fixture="${2:?usage: replay <session> <fixture>}"
    [ -f "$fixture" ] || { echo "no such fixture: $fixture" >&2; exit 1; }

    local title command
    title="$(sed -n 's/^pane_title: //p' "$fixture" | head -1)"
    command="$(sed -n 's/^pane_current_command: //p' "$fixture" | head -1)"
    [ -n "$command" ] || { echo "$fixture has no pane_current_command" >&2; exit 1; }

    local base screen_local
    base="$(basename "$(dirname "$fixture")")-$(basename "$fixture")"
    screen_local="$(mktemp -t sidecar-spike-screen)"
    # Everything after the "screen:" marker is the pane content.
    awk 'seen { print } /^screen:/ { seen = 1 }' "$fixture" > "$screen_local"

    local socket; socket="$(remote_socket)"
    rsh "mkdir -p $(printf %q "$RUN_DIR/screens") $(printf %q "$RUN_DIR/bin")"
    scp "${SSH_OPTS[@]}" -q "$screen_local" "$HOST:$RUN_DIR/screens/$base"
    rm -f "$screen_local"
    # Deploy the hold binary under the exact name the fixture recorded, so
    # tmux reports that name as the pane's current command. See
    # scripts/spike-holdpane for why a copied or symlinked system binary
    # cannot do this job.
    if [ ! -f /tmp/sidecar-spike-holdpane ]; then
        (cd "$REPO_DIR" && GOOS=darwin GOARCH=arm64 go build -o /tmp/sidecar-spike-holdpane ./scripts/spike-holdpane)
    fi
    rsh "rm -f $(printf %q "$RUN_DIR/bin/$command")"
    scp "${SSH_OPTS[@]}" -q /tmp/sidecar-spike-holdpane "$HOST:$RUN_DIR/bin/$command"
    rsh "chmod +x $(printf %q "$RUN_DIR/bin/$command")"

    local run="$RUN_DIR/bin/$command $RUN_DIR/screens/$base"
    # Create the session around the replay command, or respawn an existing
    # pane into it. Creating it this way matters: a session started around a
    # plain shell dies the moment that shell reaches EOF, which is why an
    # earlier version of this fixture kept losing its panes between commands.
    # Recreate rather than branch. A conditional chain here has to survive two
    # rounds of shell quoting on its way to the host, and the semicolons inside
    # the replay command make that fragile in a way that fails confusingly.
    # Killing and recreating is one unambiguous command each and is idempotent.
    rsh "TMUX= tmux -S $(printf %q "$socket") kill-session -t $(printf %q "$session") 2>/dev/null" || true
    rsh "TMUX= tmux -S $(printf %q "$socket") new-session -d -s $(printf %q "$session") -c $(printf %q "$REMOTE_FIXTURE") -x 120 -y 40 $(printf %q "$run")"
    if [ -n "$title" ]; then
        rsh "TMUX= tmux -S $(printf %q "$socket") select-pane -t $(printf %q "$session") -T $(printf %q "$title")"
    fi
    echo "replayed $fixture into $session (command=$command)"
}

cmd_serve() {
    # A login shell is required, not optional. Addressing the binary by
    # absolute path is enough to START it, but serve then shells out to tmux
    # and git by name, and a non-login ssh shell has no /opt/homebrew/bin on
    # PATH — so the host reports "tmux: executable file not found" on a machine
    # where tmux is plainly installed. The cost is that a chatty login profile
    # writes to the same stdout that carries the protocol; that is what the
    # probe's "not-protocol" state exists to name.
    rsh "$(remote_env) $(printf %q "$REMOTE_BIN") -config $(printf %q "$REMOTE_CONFIG") host serve --stdio $*"
}

cmd_probe() {
    (cd "$REPO_DIR" && go run ./cmd/sidecar host probe "$HOST" \
        --binary "$REMOTE_BIN" \
        --remote-config "$REMOTE_CONFIG" \
        --env "TMUX_TMPDIR=$REMOTE_TMUX_TMPDIR" \
        --env "XDG_STATE_HOME=$REMOTE_STATE" \
        --env "SIDECAR_ISOLATED_STATE=1" \
        --env "TMUX=" \
        "$@")
}

cmd_control() {
    local session="${1:?usage: control <session>}"
    # Addressed by socket path for the same reason rtmux is, and with an
    # absolute tmux because stdout here carries the control protocol and must
    # not inherit a login shell's chatter.
    rsh_raw "TMUX= $(rsh 'command -v tmux') -S $(printf %q "$(remote_socket)") -C attach-session -f ignore-size -t $(printf %q "$session")"
}

cmd_remote_tui() {
    # A full Sidecar TUI on the host, bound to the SAME isolated tree serve
    # reads. This is the comparison the plan's exit check needs: "status shown
    # locally matches the remote machine's own Sidecar TUI".
    ssh "${SSH_OPTS[@]}" -t "$HOST" "\$SHELL -l -c $(printf %q "$(remote_env) $REMOTE_BIN -config $REMOTE_CONFIG")"
}

cmd_ssh() { rsh "$@"; }

cmd_teardown() {
    local socket
    socket="$(remote_socket)"
    # Belt and braces before a kill-server: the socket path must be non-empty,
    # must live under the run root, and must actually exist. Any of those
    # failing means the target is not the private server, and the only safe
    # action is to touch no server at all.
    case "$socket" in
        "$RUN_DIR"/*) ;;
        *) echo "refusing teardown: resolved socket '$socket' is outside $RUN_DIR" >&2; exit 1 ;;
    esac
    echo "killing ONLY the private server at $socket"
    rsh "TMUX= if [ -S $(printf %q "$socket") ]; then tmux -S $(printf %q "$socket") kill-server 2>/dev/null || true; else echo '  (no private server running)'; fi"
    rsh "rm -rf $(printf %q "$RUN_DIR")"
    ssh "${SSH_OPTS[@]}" -O exit "$HOST" 2>/dev/null || true
    rm -f /tmp/sidecar-spike-build /tmp/sidecar-spike-holdpane
    echo "default server left untouched:"
    rsh "tmux ls 2>/dev/null | wc -l | tr -d ' '" | sed 's/^/  sessions on default server: /'
}

case "${1:-}" in
    paths)      shift; cmd_paths "$@" ;;
    deploy)     shift; cmd_deploy "$@" ;;
    fixture)    shift; cmd_fixture "$@" ;;
    replay)     shift; cmd_replay "$@" ;;
    tmux)       shift; rtmux "$@" ;;
    serve)      shift; cmd_serve "$@" ;;
    probe)      shift; cmd_probe "$@" ;;
    control)    shift; cmd_control "$@" ;;
    remote-tui) shift; cmd_remote_tui "$@" ;;
    ssh)        shift; cmd_ssh "$@" ;;
    teardown)   shift; cmd_teardown "$@" ;;
    *) sed -n '2,40p' "$0"; exit 2 ;;
esac
