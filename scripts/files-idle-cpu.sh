#!/bin/bash
# files-idle-cpu.sh - Measure idle CPU per rendered frame on the Files tab and
# compare it against Workspaces, reproducibly and in isolation.
#
# This is the measurement harness for docs/plans/active/files-tab-idle-cpu.md.
# It never renders its own opinion about the code: every number it prints comes
# from `terminalperf` counters and a `go tool pprof` CPU profile taken from a
# headless Sidecar driven by scripts/tmux-drive.sh.
#
# Isolation is inherited wholesale from tmux-drive.sh, which isolates BOTH the
# tmux server and the Sidecar state tree (td-8d18de). This script adds two
# things on top: it refuses to run unless `tmux-drive.sh paths` proves every
# resolved root lives under the run dir, and it stops the drive session from a
# trap so a failed measurement cannot leak a polling instance.
#
# Usage:
#   ./scripts/files-idle-cpu.sh                 # full baseline/after run
#   SIDECAR_DRIVE_RUN_DIR=/tmp/scdrv-m1 ./scripts/files-idle-cpu.sh
#
# Environment:
#   SIDECAR_DRIVE_RUN_DIR   run root; MUST be a short absolute path under /tmp
#                           (long paths overflow the unix socket path limit).
#                           Default /tmp/scfic-$(id -u).
#   FILES_IDLE_PPROF_PORT   localhost pprof/terminalperf port (default 6171)
#   FILES_IDLE_SECONDS      CPU profile duration per surface (default 20)
#   FILES_IDLE_SETTLE       seconds to let a tab settle before measuring (default 5)
#   FILES_IDLE_COLS/LINES   drive geometry (default 200x50)
#   FILES_IDLE_BIN          skip the build and measure this binary instead.
#                           Deliberately NOT SIDECAR_BIN: a Sidecar shell already
#                           exports SIDECAR_BIN, and inheriting it would measure
#                           an installed build while claiming to measure the tree.
#
# Output: a table of renders/s, CPU over the profile window, and CPU per
# rendered frame for each surface, the Files link-scan counters, and the top
# cumulative sidecar/internal frames from the Files profile.

set -euo pipefail

# Never let an attached tmux client select a server implicitly. tmux-drive.sh
# does this for itself; this script must do it too because it is the process
# that exports the environment tmux-drive.sh runs in.
unset TMUX TMUX_PANE

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd -P)"
DRIVE="$REPO_DIR/scripts/tmux-drive.sh"
LAUNCH_REPO="${SIDECAR_DRIVE_REPO:-$REPO_DIR}"
LAUNCH_REPO="$(cd "$LAUNCH_REPO" && pwd -P)"

export SIDECAR_DRIVE_RUN_DIR="${SIDECAR_DRIVE_RUN_DIR:-/tmp/scfic-$(id -u)}"
PORT="${FILES_IDLE_PPROF_PORT:-6171}"
SECONDS_PER_SURFACE="${FILES_IDLE_SECONDS:-20}"
SETTLE="${FILES_IDLE_SETTLE:-5}"
COLS="${FILES_IDLE_COLS:-200}"
LINES="${FILES_IDLE_LINES:-50}"

case "$PORT" in
    *[!0-9]*|"") echo "FILES_IDLE_PPROF_PORT must be a port number" >&2; exit 2 ;;
esac
# The diagnostic port is the one part of this harness that is NOT isolated by
# the run dir: two agents measuring at once both ask localhost:6171, and the
# second one silently reads the first one's counters and profile. Sidecar itself
# only logs the bind failure, so refuse up front and verify after start.
if ! PORT="$PORT" python3 - <<'PY'
import os, socket
s = socket.socket()
try:
    s.bind(("127.0.0.1", int(os.environ["PORT"])))
except OSError:
    raise SystemExit(1)
finally:
    s.close()
PY
then
    echo "refusing port $PORT: something is already listening on it." >&2
    echo "another proof run may own it - set FILES_IDLE_PPROF_PORT to a free port." >&2
    exit 2
fi
case "$SECONDS_PER_SURFACE$SETTLE" in
    *[!0-9]*) echo "FILES_IDLE_SECONDS and FILES_IDLE_SETTLE must be whole seconds" >&2; exit 2 ;;
esac

RUN_DIR="$SIDECAR_DRIVE_RUN_DIR"
case "$RUN_DIR" in
    /tmp/*) ;;
    *) echo "refusing run dir '$RUN_DIR': use a short absolute path under /tmp" >&2; exit 2 ;;
esac
# The inner tmux socket path is built under $RUN_DIR/tmux/tmux-<uid>/default and
# a unix socket path is capped near 104 bytes on darwin. Fail loudly here rather
# than with an opaque tmux error forty lines later.
if [ "${#RUN_DIR}" -gt 40 ]; then
    echo "refusing run dir '$RUN_DIR': ${#RUN_DIR} chars is too long for a tmux socket path" >&2
    exit 2
fi
mkdir -p "$RUN_DIR"
# tmux-drive.sh canonicalizes the run dir (on darwin /tmp is a symlink to
# /private/tmp), and the paths it prints are canonical. Compare like with like.
RUN_DIR="$(cd "$RUN_DIR" && pwd -P)"

OUT_DIR="$RUN_DIR/idle-cpu"
mkdir -p "$OUT_DIR"

need() { command -v "$1" >/dev/null 2>&1 || { echo "missing required tool: $1" >&2; exit 2; }; }
need curl
need go
need python3

# ---------------------------------------------------------------------------
# Isolation gate. Nothing below runs until the drive script's own resolution
# proves the run is off the developer's tmux server and state tree.
# ---------------------------------------------------------------------------
assert_isolated() {
    local paths
    paths="$("$DRIVE" paths)"
    echo "$paths"
    if printf '%s\n' "$paths" | grep -qiE '\.local/state/sidecar|\.config/sidecar'; then
        echo "REFUSING: a resolved path points into the developer's live Sidecar tree" >&2
        exit 1
    fi
    local key value
    while IFS= read -r line; do
        key="${line%%:*}"
        value="${line#*:}"
        # Strip leading whitespace without a subshell.
        while [ "${value# }" != "$value" ]; do value="${value# }"; done
        case "$key" in
            "inner socket"|"state home"|"cache home"|"config")
                case "$value" in
                    "$RUN_DIR"/*) ;;
                    *) echo "REFUSING: $key resolved outside the run dir: $value" >&2; exit 1 ;;
                esac
                ;;
        esac
    done <<<"$paths"
    echo "isolation: OK (every resolved root is under $RUN_DIR)"
}

stopped=0
stop_drive() {
    [ "$stopped" -eq 1 ] && return 0
    stopped=1
    "$DRIVE" stop >/dev/null 2>&1 || true
    echo "drive session stopped"
}
trap stop_drive EXIT INT TERM

# ---------------------------------------------------------------------------
# Build. A measurement of the working tree has to compile the working tree.
# ---------------------------------------------------------------------------
if [ -n "${FILES_IDLE_BIN:-}" ]; then
    SIDECAR_BIN="$FILES_IDLE_BIN"
else
    SIDECAR_BIN="$OUT_DIR/sidecar"
    echo "building $SIDECAR_BIN from $REPO_DIR ..."
    ( cd "$REPO_DIR" && go build -o "$SIDECAR_BIN" ./cmd/sidecar )
fi
export SIDECAR_BIN
echo "binary:        $SIDECAR_BIN"

# The launched sidecar inherits these through the private tmux server, which is
# created fresh below (the stop above guarantees it).
export SIDECAR_PPROF="$PORT"
export SIDECAR_TERMINAL_PERF=1

assert_isolated

# A stale server from an earlier run would hold the earlier environment, so the
# fresh server must be the one that learns SIDECAR_PPROF.
stop_drive
stopped=0

# Reset this run's Sidecar state tree. Two consecutive measurements have to see
# the same instance, and a shell manifest left by the previous run makes the
# second one refuse to create the chatty shells ("name is already in use") and
# then measure a different set of panes. Only ever the isolated tree: the
# isolation gate above has already proved it is under the run dir, and the run
# dir has already been proved to be under /tmp.
case "$RUN_DIR" in
    /private/tmp/*|/tmp/*) rm -rf "${RUN_DIR:?}/state/sidecar" ;;
    *) echo "refusing to reset a state tree outside /tmp: $RUN_DIR" >&2; exit 1 ;;
esac

# ---------------------------------------------------------------------------
# Seed the isolated state.json before the first start, mirroring the user's own
# fileBrowser entry: six expanded dirs, three preview tabs, a large markdown
# file as the active tab, tree pane widened.
# ---------------------------------------------------------------------------
CONFIG_DIR="$RUN_DIR/config"
# Mirrors tmux-drive.sh's own CONFIG. state.json lives beside it because Sidecar
# resolves it from the config file's directory, which is why -config is the only
# lever that moves either of them.
CONFIG="$CONFIG_DIR/config.json"
STATE_JSON="$CONFIG_DIR/state.json"
mkdir -p "$CONFIG_DIR"
LAUNCH_REPO="$LAUNCH_REPO" STATE_JSON="$STATE_JSON" python3 - <<'PY'
import json, os

path = os.environ["STATE_JSON"]
repo = os.environ["LAUNCH_REPO"]
state = {}
if os.path.exists(path):
    try:
        with open(path) as fh:
            state = json.load(fh)
    except (ValueError, OSError):
        state = {}
if not isinstance(state, dict):
    state = {}

state["fileBrowserTreeWidth"] = 54
browser = state.setdefault("fileBrowser", {})
browser[repo] = {
    "selectedFile": "docs/reference/design-language.md",
    "previewFile": "docs/reference/design-language.md",
    "expandedDirs": [
        "docs",
        "docs/plans",
        "docs/plans/active",
        "docs/reference",
        "docs/research",
        "docs/research/active",
    ],
    "tabs": [
        {"path": "internal/plugins/filebrowser/watcher.go"},
        {"path": "AGENTS.md"},
        {"path": "docs/reference/design-language.md"},
    ],
    "activeTab": 2,
    "activePane": "tree",
    "treeCursor": 29,
    "showIgnored": True,
}
with open(path, "w") as fh:
    json.dump(state, fh, indent=2)
print("seeded fileBrowser[%s] in %s" % (repo, path))
PY

# ---------------------------------------------------------------------------
# Start and wait for the diagnostic server, which is only up once the app is.
# ---------------------------------------------------------------------------
"$DRIVE" start "$COLS" "$LINES"

counters() { curl -fsS --max-time 5 "http://localhost:$PORT/debug/terminalperf"; }

echo -n "waiting for the diagnostic server "
ready=0
for _ in $(seq 1 60); do
    if counters >/dev/null 2>&1; then ready=1; break; fi
    echo -n "."
    sleep 1
done
echo
[ "$ready" -eq 1 ] || { echo "sidecar never served $PORT/debug/terminalperf" >&2; exit 1; }

# Prove the process answering on that port is the one this run launched. Without
# this every number below could belong to somebody else's instance.
served_cmdline="$(curl -fsS --max-time 5 "http://localhost:$PORT/debug/pprof/cmdline" | tr '\0' ' ')"
case "$served_cmdline" in
    "$SIDECAR_BIN "*"$CONFIG"*) echo "diagnostics: $served_cmdline" ;;
    *)
        echo "REFUSING: localhost:$PORT is served by another process:" >&2
        echo "  $served_cmdline" >&2
        echo "  expected: $SIDECAR_BIN -config $CONFIG" >&2
        exit 1
        ;;
esac

# Confirm the seed survived startup. sidecar rewrites state.json on exit, not on
# start, but a mismatch here would silently invalidate every number below.
if ! grep -q '"design-language.md"' "$STATE_JSON" 2>/dev/null && \
   ! grep -q 'design-language.md' "$STATE_JSON" 2>/dev/null; then
    echo "WARNING: the seeded fileBrowser entry is no longer in $STATE_JSON" >&2
fi

# ---------------------------------------------------------------------------
# Two chatty shells. They are what makes the instance non-idle at the message
# level while every surface under test is visually idle.
# ---------------------------------------------------------------------------
#
# The diagnostic server comes up before the project is registered in the freshly
# reset state tree, so the first create can lose that race with "no Sidecar
# project is registered for this directory". Retry rather than fail: a harness
# that is flaky about its own setup cannot be trusted about its numbers.
create_chatty() {
    local n="$1" attempt
    for attempt in $(seq 1 30); do
        if "$DRIVE" cli create shell --name "chatty$n" \
            --run 'while true; do date; ls -la /usr/bin | head -20; sleep 0.15; done' >/dev/null 2>&1; then
            return 0
        fi
        sleep 1
    done
    echo "could not create chatty shell $n after 30 attempts" >&2
    "$DRIVE" cli create shell --name "chatty$n" \
        --run 'while true; do date; sleep 0.15; done' >&2 || true
    return 1
}
for n in 1 2; do create_chatty "$n"; done
echo "created two chatty shells"
sleep 3

# ---------------------------------------------------------------------------
# Measure one surface: switch to its tab, let it settle, then take counters
# around a CPU profile of exactly the same window.
# ---------------------------------------------------------------------------
measure() {
    local name="$1" tab="$2"
    "$DRIVE" keys "$tab" >/dev/null
    sleep "$SETTLE"
    "$DRIVE" snap "idle-$name" >/dev/null 2>&1 || true

    local before after profile started ended
    before="$OUT_DIR/$name.before.json"
    after="$OUT_DIR/$name.after.json"
    profile="$OUT_DIR/$name.pprof"

    counters > "$before"
    started="$(python3 -c 'import time; print(time.time())')"
    curl -fsS --max-time "$((SECONDS_PER_SURFACE + 30))" \
        "http://localhost:$PORT/debug/pprof/profile?seconds=$SECONDS_PER_SURFACE" -o "$profile"
    ended="$(python3 -c 'import time; print(time.time())')"
    counters > "$after"

    go tool pprof -top -nodecount=200 "$SIDECAR_BIN" "$profile" \
        > "$OUT_DIR/$name.flat.txt" 2>/dev/null || true
    go tool pprof -top -cum -nodecount=400 "$SIDECAR_BIN" "$profile" \
        > "$OUT_DIR/$name.cum.txt" 2>/dev/null || true

    SURFACE="$name" BEFORE="$before" AFTER="$after" \
    CUM="$OUT_DIR/$name.cum.txt" ELAPSED="$(python3 -c "print($ended - $started)")" \
    python3 - <<'PY'
import json, os, re

name = os.environ["SURFACE"]
elapsed = float(os.environ["ELAPSED"])
before = json.load(open(os.environ["BEFORE"]))
after = json.load(open(os.environ["AFTER"]))


def delta(key):
    return after.get(key, 0) - before.get(key, 0)


renders = delta("application_views_rendered")
rate = renders / elapsed if elapsed else 0.0

total_ms = None
duration_s = None
try:
    head = open(os.environ["CUM"]).read()
    m = re.search(r"Duration:\s*([\d.]+)(m?s)", head)
    if m:
        duration_s = float(m.group(1)) / (1000.0 if m.group(2) == "ms" else 1.0)
    m = re.search(r"Total samples\s*=\s*([\d.]+)(ms|s|mins?)", head)
    if m:
        value = float(m.group(1))
        unit = m.group(2)
        total_ms = value if unit == "ms" else value * (1000.0 if unit == "s" else 60000.0)
except OSError:
    pass

print()
print("== %s ==" % name)
print("  window            %.2fs (profile duration %s)" %
      (elapsed, "%.2fs" % duration_s if duration_s else "n/a"))
print("  renders           %d" % renders)
print("  renders/s         %.2f" % rate)
if total_ms is not None:
    print("  CPU over window   %.0fms" % total_ms)
    if renders:
        print("  CPU per frame     %.2fms" % (total_ms / renders))
else:
    print("  CPU over window   (pprof header not parsed; see %s)" % os.environ["CUM"])

# Process CPU alone answers "how hot is the app", not "what does a frame cost":
# the chatty shells burn CPU that no surface's render path is responsible for.
# These cumulative frames are the render path itself, so their per-frame cost is
# the number this plan moves.
cum_ms = {}
try:
    for line in open(os.environ["CUM"]):
        fields = line.split()
        if len(fields) < 6:
            continue
        m = re.fullmatch(r"([\d.]+)(ms|s)", fields[3])
        if not m:
            continue
        value = float(m.group(1)) * (1000.0 if m.group(2) == "s" else 1.0)
        cum_ms.setdefault(fields[5], value)
except OSError:
    pass

interesting = [
    ("render path       ", "github.com/marcus/sidecar/internal/app.Model.View"),
    ("  content deck    ", "github.com/marcus/sidecar/internal/app.(*Model).renderContentDeck"),
    ("  deck link scan  ", "github.com/marcus/sidecar/internal/app.(*appContentDeck).scanPrimary"),
    ("  filebrowser view", "github.com/marcus/sidecar/internal/plugins/filebrowser.(*Plugin).View"),
    ("  paneframe.Compose", "github.com/marcus/sidecar/internal/paneframe.Compose"),
]
for label, symbol in interesting:
    value = cum_ms.get(symbol)
    if value is None:
        continue
    per_frame = " (%.2fms/frame)" % (value / renders) if renders else ""
    print("  %s %.0fms%s" % (label, value, per_frame))
print("  files link scans  %d built / %d cache hits" %
      (delta("files_link_scans"), delta("files_link_scan_cache_hits")))
print("  files frames      %d built / %d cache hits" %
      (delta("files_frames_built"), delta("files_frame_cache_hits")))
print("  deck compose      %d cache hits" % delta("deck_compose_cache_hits"))
print("  document frames   %d built / %d cache hits" %
      (delta("document_frames_built"), delta("document_frame_cache_hits")))
PY
}

echo
echo "profiling ${SECONDS_PER_SURFACE}s per surface (settle ${SETTLE}s) ..."
measure workspaces 4
measure files 3

# ---------------------------------------------------------------------------
# Where the Files frame goes. Cumulative, sidecar-owned frames only: runtime
# and third-party frames are noise for this question.
# ---------------------------------------------------------------------------
echo
echo "== top cumulative sidecar/internal frames on Files =="
if [ -f "$OUT_DIR/files.cum.txt" ]; then
    grep 'sidecar/internal' "$OUT_DIR/files.cum.txt" | head -25 || true
else
    echo "(no cumulative profile captured)"
fi

echo
echo "artifacts: $OUT_DIR"
