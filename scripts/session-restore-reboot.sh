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
#   scripts/session-restore-reboot.sh server-id   # print the live server pid
#   scripts/session-restore-reboot.sh reboot      # terminate ONLY this server
#   scripts/session-restore-reboot.sh trace       # startup phase offsets from a real run
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
  bind_fake_agent
}

# bind_fake_agent gives one shell an exact conversation binding.
#
# It writes the binding into the manifest rather than going through `agent
# report-session`, and the reason is worth stating: that command evaluates a
# generation fence against the provider process actually occupying the pane, so
# a report that no live provider produced is refused — correctly. The exit gate
# needs a bound shell, not a demonstration of the fence, so the binding is
# seeded as durable state a previous session would have left behind.
#
# The reference is marked reported, which is what makes it resume-eligible, so
# the ask-policy refusal and the resume plan both have something real to act on.
bind_fake_agent() {
  local manifest="$STATE_DIR/projects/harness/shells.json"
  [[ -f "$manifest" ]] || die "no manifest to bind an agent into"
  SIDECAR_MANIFEST="$manifest" python3 - <<'PY'
import json, os, datetime
path = os.environ["SIDECAR_MANIFEST"]
with open(path) as fh:
    doc = json.load(fh)
now = datetime.datetime.now(datetime.timezone.utc).isoformat().replace("+00:00", "Z")
bound = None
for shell in doc.get("shells", []):
    if shell.get("displayName") == "reviewer":
        shell["agentType"] = "codex"
        shell["agent"] = {
            "kind": "codex",
            "session": {
                "kind": "id",
                "value": "01a05614-0ca7-7c31-9b1e-000000000001",
                "source": "sidecar.codex.hooks",
                "reported": True,
                "reportedAt": now,
            },
        }
        bound = shell["tmuxName"]
with open(path, "w") as fh:
    json.dump(doc, fh, indent=2)
    fh.write("\n")
print(bound or "none")
PY
  note "bound a fake codex conversation to the reviewer shell"
}

# cmd_trace reproduces the first-frame ordering evidence the plan cites.
#
# The claim it exists to check is that the cold restore runs strictly after the
# app has painted — the plan's numbers are `first ready frame 84.985ms` followed
# by `session restore 86.15ms` — and that is a claim about a real run, not
# something a unit test can hold. So it launches the real TUI, inside the
# harness's own tmux server and against the harness's own state tree, with
# SIDECAR_STARTUP_TRACE on, and reads the phase offsets back out.
#
# The environment is passed explicitly rather than inherited. The harness server
# may have been started by an earlier subcommand and carries whatever global
# environment it captured then; naming every isolating variable on the command
# line means the traced binary cannot be the one that finds the developer's real
# state tree.
#
# The only thing it terminates is the session it just created, by name. There is
# no kill-server here, and there must never be one: the harness server is also
# holding the seeded shells.
TRACE_SESSION="sidecar-startup-trace"

cmd_trace() {
  assert_isolated
  [[ -x "$BIN" ]] || die "no binary; run '$0 build' first"

  local out="$HARNESS_ROOT/startup-trace.txt"
  local delay="${TRACE_DELAY:-4s}"
  rm -f "$out"
  mkdir -p "$HARNESS_ROOT"

  tmux_here kill-session -t "$TRACE_SESSION" 2>/dev/null || true
  tmux_here new-session -d -s "$TRACE_SESSION" -x 200 -y 50 \
    "env HOME='$HOME' \
         XDG_STATE_HOME='$XDG_STATE_HOME' \
         XDG_CONFIG_HOME='$XDG_CONFIG_HOME' \
         XDG_DATA_HOME='$XDG_DATA_HOME' \
         XDG_CACHE_HOME='$XDG_CACHE_HOME' \
         TMUX_TMPDIR='$TMUX_TMPDIR' \
         SIDECAR_ISOLATED_STATE=1 \
         SIDECAR_STARTUP_TRACE=stderr \
         SIDECAR_STARTUP_TRACE_DELAY='$delay' \
         '$BIN' -config '$CONFIG_PATH' 2>'$out'"
  note "sidecar running in tmux session $TRACE_SESSION; waiting for the trace dump"

  # The binary dumps the report SIDECAR_STARTUP_TRACE_DELAY after start without
  # needing a clean quit, so wait for the report's own header rather than
  # sleeping a guessed interval.
  local waited=0
  while ((waited < 300)); do
    grep -q 'sidecar startup trace' "$out" 2>/dev/null && break
    sleep 0.1
    waited=$((waited + 1))
  done
  tmux_here kill-session -t "$TRACE_SESSION" 2>/dev/null || true

  if ! grep -q 'sidecar startup trace' "$out" 2>/dev/null; then
    note "no trace was written to $out"
    [[ -s "$out" ]] && sed -n '1,40p' "$out"
    return 1
  fi

  echo
  cat "$out"
  echo
  echo "== phase offsets"
  trace_phase "$out" "first ready frame"
  trace_phase "$out" "session restore"

  local frame restore
  frame="$(trace_offset "$out" "first ready frame")"
  restore="$(trace_offset "$out" "session restore")"
  if [[ -z "$frame" || -z "$restore" ]]; then
    note "one of the two phases is missing; the ordering claim cannot be checked from this run"
    return 1
  fi
  # Offsets are printed in source order by ascending offset, so the line numbers
  # answer the ordering question without parsing durations.
  if ((frame < restore)); then
    printf '  ok    the cold restore starts after the first ready frame\n'
  else
    printf '  FAIL  session restore was recorded before the first ready frame\n'
    return 1
  fi
}

# trace_phase prints the report line for one phase, offset included.
trace_phase() {
  local line
  line="$(grep -F -- "$2" "$1" | head -1)"
  if [[ -z "$line" ]]; then
    printf '  MISSING  %s\n' "$2"
  else
    printf '  %s\n' "$(printf '%s' "$line" | sed 's/^ *//')"
  fi
}

# trace_offset prints the 1-based position of a phase within the report body,
# which is ordered by offset from process start.
trace_offset() {
  awk -v want="$2" '
    /sidecar startup trace/ { body = 1; n = 0; next }
    body && index($0, want) { print ++n; exit }
    body { n++ }
  ' "$1"
}

# cmd_gate runs the whole exit gate end to end and fails loudly on any step.
#
# It exists because the gate is only worth as much as its reproducibility: a
# journey that has to be driven by hand from a description is a journey nobody
# re-runs, and the first version of this file documented a `gate` verb it never
# implemented.
cmd_gate() {
  assert_isolated
  local failures=0
  check() {
    if [[ "$2" == "$3" ]]; then
      printf '  ok    %s\n' "$1"
    else
      printf '  FAIL  %s\n        got %q want %q\n' "$1" "$2" "$3"
      failures=$((failures + 1))
    fi
  }
  contains() {
    if grep -qF -- "$3" <<<"$2"; then
      printf '  ok    %s\n' "$1"
    else
      printf '  FAIL  %s\n        %q does not contain %q\n' "$1" "$2" "$3"
      failures=$((failures + 1))
    fi
  }

  echo "== isolation"
  cmd_paths | tail -3

  echo "== build and seed"
  cmd_reset >/dev/null
  cmd_build >/dev/null
  cmd_seed >/dev/null
  local before_server records
  before_server="$(cmd_server_id)"
  records="$(manifest_shell_count)"
  check "two managed shells recorded" "$records" "2"

  echo "== terminate only the harness server"
  local default_before default_after
  default_before="$(default_server_session_count)"
  cmd_reboot >/dev/null
  check "harness server is gone" "$(cmd_server_id)" "none"
  default_after="$(default_server_session_count)"
  check "the default tmux server was not touched" "$default_after" "$default_before"

  echo "== records survive the server death"
  check "both shell records survived" "$(manifest_shell_count)" "2"
  check "nothing was tombstoned" "$(manifest_tombstone_count)" "0"

  echo "== status reports a cold restore"
  local status_json
  status_json="$(cli session status --json)"
  contains "serverChanged is true" "$status_json" '"serverChanged": true'
  contains "the dead server is named" "$status_json" "$before_server"
  contains "the session value is never printed" "$(printf '%s' "$status_json" | grep -c '01a05614' || true)" "0"

  echo "== ask policy refuses an unconfirmed resume"
  local refuse_out refuse_code
  refuse_out="$(cli session restore --agents 2>&1)" && refuse_code=0 || refuse_code=$?
  check "exit 5 (input rejected)" "$refuse_code" "5"
  contains "the refusal names --yes" "$refuse_out" "--yes"
  check "nothing was created" "$(tmux_session_count)" "0"

  echo "== dry run creates nothing"
  cli session restore --dry-run >/dev/null
  check "still no sessions" "$(tmux_session_count)" "0"

  echo "== restore"
  cli session restore >/dev/null
  check "both shells are back" "$(tmux_session_count)" "2"
  check "records intact" "$(manifest_shell_count)" "2"
  contains "markers re-stamped to the new server" "$(manifest_markers)" "$(cmd_server_id)"

  echo "== second restore is idempotent"
  local second
  second="$(cli session restore)"
  contains "everything reattached" "$second" "reattached"
  check "still exactly two sessions" "$(tmux_session_count)" "2"

  echo
  if ((failures)); then
    printf 'GATE FAILED: %d check(s)\n' "$failures"
    return 1
  fi
  printf 'GATE PASSED\n'
}

# count_matches emits exactly one number. `grep -c` prints 0 *and* exits 1 when
# nothing matches, so the naive `grep -c ... || echo 0` prints two lines.
count_matches() {
  local n
  n="$(grep -c "$1" "$2" 2>/dev/null || true)"
  printf '%s' "${n:-0}"
}

manifest_shell_count() {
  local f="$STATE_DIR/projects/harness/shells.json"
  [[ -f "$f" ]] || { printf '0'; return; }
  count_matches '"tmuxName"' "$f"
}

manifest_markers() {
  local f="$STATE_DIR/projects/harness/shells.json"
  [[ -f "$f" ]] && grep -o '"lastSeenServer": *"[^"]*"' "$f" || true
}

manifest_tombstone_count() {
  local f="$STATE_DIR/projects/harness/shells.json"
  [[ -f "$f" ]] || { printf '0'; return; }
  count_matches '"deletedAt"' "$f"
}

tmux_session_count() {
  local n
  n="$(tmux_here list-sessions -F '#{session_name}' 2>/dev/null | grep -c . || true)"
  printf '%s' "${n:-0}"
}

# default_server_session_count counts the DEVELOPER's sessions, read-only, so the
# gate can prove it never touched them. It is the only place this file looks
# outside the harness, and it can only read.
default_server_session_count() {
  local n
  # env -u TMUX_TMPDIR so this reads the DEVELOPER's default socket rather than
  # the harness one every other tmux call in this file is pinned to. It is the
  # only outward-facing call here and it is read-only by construction.
  n="$(env -u TMUX_TMPDIR tmux list-sessions -F '#{session_name}' 2>/dev/null | grep -c . || true)"
  printf '%s' "${n:-0}"
}

case "${1:-}" in
  paths)     cmd_paths ;;
  reset)     cmd_reset ;;
  build)     cmd_build ;;
  seed)      shift; cmd_seed "$@" ;;
  trace)     cmd_trace ;;
  gate)      cmd_gate ;;
  server-id) cmd_server_id ;;
  reboot)    cmd_reboot ;;
  cli)       shift; cli "$@" ;;
  tmux)      shift; tmux_here "$@" ;;
  *)         sed -n '1,40p' "$0"; exit 2 ;;
esac
