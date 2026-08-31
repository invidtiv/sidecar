#!/usr/bin/env bash
# Cold-restore reboot harness.
#
# Proving that Sidecar reconstructs shells after a tmux server restart needs a
# tmux server that can actually be terminated. That is a dangerous thing to
# automate: the developer's default server holds live Sidecar and agent sessions
# and irreplaceable work, and killing it destroys all of them. So this harness
# owns a tmux server of its own and refuses to terminate anything else.
#
# The isolation is on two independent axes, because isolating one is not
# isolating the other — a private tmux socket does nothing to stop a run from
# rewriting the real shells.json a running Sidecar is watching:
#
#   - tmux:     TMUX_TMPDIR points inside HARNESS_ROOT, so the socket resolves to
#               $TMUX_TMPDIR/tmux-$UID/default and nothing else.
#   - Sidecar:  HOME, every XDG root, -config and SIDECAR_ISOLATED_STATE=1, so
#               the binary refuses to start if it would touch the real tree.
#
# Every kill re-derives the socket path and asserts it is inside HARNESS_ROOT
# before running kill-server. There is no code path here that can name the
# default socket.
#
# The state tree is deliberately NOT cleaned between launches — that persistence
# is the whole experiment. `reset` is explicit and separate.
#
# Usage:
#   scripts/session-restore-reboot.sh paths       # print and verify isolation
#   scripts/session-restore-reboot.sh reset       # wipe the harness root
#   scripts/session-restore-reboot.sh build       # compile the working tree
#   scripts/session-restore-reboot.sh seed        # project + shells + fake agent
#   scripts/session-restore-reboot.sh mark        # record cold-restore eligibility
#   scripts/session-restore-reboot.sh server-id   # print the live server pid
#   scripts/session-restore-reboot.sh reboot      # terminate ONLY this server
#   scripts/session-restore-reboot.sh cli ARGS... # run the isolated binary
#   scripts/session-restore-reboot.sh gate        # the whole exit gate

set -euo pipefail

HARNESS_ROOT="${HARNESS_ROOT:-${TMPDIR:-/tmp}/sidecar-m4-reboot}"
REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The real HOME is kept only so `go build` can reuse the module cache. Nothing
# else in this file may use it, and the compiled binary never sees it.
REAL_HOME="$HOME"
export HOME="$HARNESS_ROOT/home"
export XDG_STATE_HOME="$HARNESS_ROOT/state"
export XDG_CONFIG_HOME="$HARNESS_ROOT/config"
export XDG_DATA_HOME="$HARNESS_ROOT/data"
export XDG_CACHE_HOME="$HARNESS_ROOT/cache"
export TMUX_TMPDIR="$HARNESS_ROOT/tmux"
export SIDECAR_ISOLATED_STATE=1
unset TMUX TMUX_PANE 2>/dev/null || true

CONFIG_PATH="$HARNESS_ROOT/config.json"
BIN="$HARNESS_ROOT/sidecar"
STATE_DIR="$XDG_STATE_HOME/sidecar"
PROJECT_DIR="$HARNESS_ROOT/project"
SOCKET="$TMUX_TMPDIR/tmux-$(id -u)/default"

die() { printf 'session-restore-reboot: %s\n' "$*" >&2; exit 1; }
note() { printf '  %s\n' "$*"; }

# assert_isolated refuses to continue unless every path this harness will touch
# is inside its own root. It is called before anything destructive.
assert_isolated() {
  case "$HARNESS_ROOT" in
    /|/tmp|"$HOME"|/Users/*/code*|"$REPO_ROOT"*) die "HARNESS_ROOT $HARNESS_ROOT is not a safe scratch directory" ;;
  esac
  [[ "$TMUX_TMPDIR" == "$HARNESS_ROOT"/* ]] || die "TMUX_TMPDIR escaped the harness root"
  [[ "$SOCKET" == "$HARNESS_ROOT"/* ]] || die "tmux socket $SOCKET escaped the harness root"
  [[ "$XDG_STATE_HOME" == "$HARNESS_ROOT"/* ]] || die "XDG_STATE_HOME escaped the harness root"
  [[ "$HOME" == "$HARNESS_ROOT"/* ]] || die "HOME escaped the harness root"
  # The one check that matters most: never the machine's real socket.
  local default_socket="${TMPDIR:-/tmp}/tmux-$(id -u)/default"
  [[ "$SOCKET" != "$default_socket" ]] || die "refusing to operate on the default tmux socket"
}

cmd_paths() {
  assert_isolated
  echo "harness root : $HARNESS_ROOT"
  echo "HOME         : $HOME"
  echo "state dir    : $STATE_DIR"
  echo "config       : $CONFIG_PATH"
  echo "TMUX_TMPDIR  : $TMUX_TMPDIR"
  echo "tmux socket  : $SOCKET"
  echo "project      : $PROJECT_DIR"
  echo
  echo "isolation OK: nothing above resolves under the real ~/.local/state/sidecar,"
  echo "~/.config/sidecar, or the default tmux socket."
}

cmd_reset() {
  assert_isolated
  cmd_reboot_quiet || true
  # A Go module cache is written read-only; if one ever lands here, make the
  # tree writable before removing it rather than leaving a half-deleted root.
  [[ -d "$HARNESS_ROOT" ]] && chmod -R u+w "$HARNESS_ROOT" 2>/dev/null
  rm -rf "$HARNESS_ROOT"
  mkdir -p "$HOME" "$STATE_DIR" "$TMUX_TMPDIR" "$XDG_CONFIG_HOME" "$PROJECT_DIR"
  chmod 700 "$TMUX_TMPDIR"
  printf '{}\n' > "$CONFIG_PATH"
  note "reset $HARNESS_ROOT"
}

cmd_build() {
  assert_isolated
  mkdir -p "$HARNESS_ROOT"
  ( cd "$REPO_ROOT" && HOME="$REAL_HOME" go build -o "$BIN" ./cmd/sidecar )
  note "built $BIN from the working tree"
}

# tmux_here runs tmux against the harness socket only. Every tmux call in this
# file goes through it, so there is no way to address another server by accident.
tmux_here() {
  assert_isolated
  tmux -S "$SOCKET" "$@"
}

cli() {
  assert_isolated
  [[ -x "$BIN" ]] || die "no binary; run '$0 build' first"
  "$BIN" -config "$CONFIG_PATH" "$@"
}

server_pid() {
  tmux_here display-message -p '#{pid}' 2>/dev/null || true
}

cmd_server_id() {
  local pid
  pid="$(server_pid)"
  if [[ -z "$pid" ]]; then echo "none"; else echo "pid=$pid"; fi
}

# cmd_reboot terminates ONLY the harness server. This is the one destructive
# tmux operation in the file, and it re-derives and re-checks the socket first.
cmd_reboot() {
  assert_isolated
  local before
  before="$(server_pid)"
  if [[ -z "$before" ]]; then
    note "no harness tmux server was running"
    return 0
  fi
  note "terminating harness tmux server pid=$before on $SOCKET"
  tmux_here kill-server 2>/dev/null || true
  # tmux does not unlink its socket on kill-server, so the file remaining is
  # expected; what must change is the server behind it.
  for _ in $(seq 1 50); do
    [[ -z "$(server_pid)" ]] && break
    sleep 0.1
  done
  [[ -z "$(server_pid)" ]] || die "the harness server did not go away"
  note "harness tmux server is gone (was pid=$before)"
  echo "$before" > "$HARNESS_ROOT/last-server-pid"
}

cmd_reboot_quiet() { cmd_reboot >/dev/null 2>&1; }

# cmd_seed builds the world the exit gate measures: a registered project, two
# managed shells, and one shell bound to a fake agent conversation.
cmd_seed() {
  assert_isolated
  mkdir -p "$PROJECT_DIR"
  ( cd "$PROJECT_DIR" && git init -q 2>/dev/null || true )

  # Registering a project is writing its meta.json; that file is the durable
  # artifact a previous Sidecar run leaves behind, which is exactly the
  # precondition a cold restore starts from.
  mkdir -p "$STATE_DIR/projects/harness"
  printf '{"path":"%s"}\n' "$PROJECT_DIR" > "$STATE_DIR/projects/harness/meta.json"

  # --wait 0 because there is no TUI to acknowledge the creation here; the
  # managed shell and its manifest record are created either way.
  cli create shell --project harness --name builder --wait 0 --json >/dev/null
  cli create shell --project harness --name reviewer --wait 0 --json >/dev/null
  note "created two managed shells under $(cmd_server_id)"
}

cmd_mark() {
  assert_isolated
  note "server $(cmd_server_id)"
}

# cmd_trace launches the real TUI inside the harness tmux server with startup
# tracing on, so the ordering of `first ready frame` against any restore work can
# be read from a real run rather than argued about.
cmd_trace() {
  assert_isolated
  [[ -x "$BIN" ]] || die "no binary; run '$0 build' first"
  local out="$HARNESS_ROOT/trace.out"
  rm -f "$out"
  tmux_here new-session -d -s trace-run -x 200 -y 50 \
    "SIDECAR_STARTUP_TRACE=stderr SIDECAR_STARTUP_TRACE_DELAY=8s '$BIN' -config '$CONFIG_PATH' 2> '$out'"
  note "sidecar started in the harness tmux; waiting for the delayed trace dump"
  sleep 12
  tmux_here kill-session -t trace-run 2>/dev/null || true
  if [[ -s "$out" ]]; then
    cat "$out"
  else
    note "no trace was written to $out"
  fi
}

cmd_manifest() {
  assert_isolated
  local f
  for f in "$STATE_DIR"/projects/*/shells.json; do
    [[ -e "$f" ]] || continue
    echo "--- $f"
    cat "$f"
  done
}

case "${1:-}" in
  paths)     cmd_paths ;;
  reset)     cmd_reset ;;
  build)     cmd_build ;;
  seed)      shift; cmd_seed "$@" ;;
  mark)      cmd_mark ;;
  manifest)  cmd_manifest ;;
  trace)     cmd_trace ;;
  server-id) cmd_server_id ;;
  reboot)    cmd_reboot ;;
  cli)       shift; cli "$@" ;;
  tmux)      shift; tmux_here "$@" ;;
  *)         sed -n '1,40p' "$0"; exit 2 ;;
esac
