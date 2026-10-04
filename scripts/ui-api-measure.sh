#!/bin/sh
# Isolated measurements for the UI API v0 terminal stream (U0-c, td-d8fcb0).
#
# Builds a temporary binary, starts a private tmux server and an isolated
# Sidecar state/config tree, creates five managed shells there, runs
# `sidecar api serve --port 0`, and drives the terminal WebSocket with
# internal/tools/uiapimeasure. It reports, per scenario, frames/s, bytes/s on
# the wire (raw and gzip-estimated), capture-pane commands/s, Sidecar and tmux
# CPU, and keystroke-to-echo latency. Results go to stdout as JSON lines and as
# a Markdown table; see docs/plans/active/sidecar-ui-api/u0-measurements.md.
#
# Capture counting: Sidecar captures through tmux control-mode clients, so a
# capture is a command written to a `tmux -C` client's stdin, not a process. A
# wrapper named `tmux` on the serve process's PATH tees every control client's
# stdin to a log and records every other tmux spawn. --no-count runs without
# the wrapper, to check that it does not move the latency numbers.
#
# Every generator is bounded by timeout(1). The script removes only what it
# created and never touches the default tmux server or the real state tree.
#
# usage: scripts/ui-api-measure.sh [--no-count] [--window SECONDS] [--keystrokes N] [TMUX_BIN]
set -eu

# Nothing from the caller's Sidecar shell may leak into the run: $TMUX would
# point tmux at the default server, and the SIDECAR_* shell variables would
# make `create shell` place a split beside the caller's shell.
unset TMUX TMUX_PANE SIDECAR_SHELL SIDECAR_SHELL_NAME SIDECAR_MANAGED_SHELL SIDECAR_TMUX_SERVER SIDECAR_HOST SIDECAR_NAMESPACE SIDECAR_BIN

count=1
window=15
keystrokes=200
while [ $# -gt 0 ]; do
	case "$1" in
	--no-count) count=0; shift ;;
	--window) window=$2; shift 2 ;;
	--keystrokes) keystrokes=$2; shift 2 ;;
	-*) echo "unknown flag $1" >&2; exit 2 ;;
	*) break ;;
	esac
done
tmux_bin=${1:-$(command -v tmux)}
timeout_bin=$(command -v timeout || command -v gtimeout || true)
[ -n "$timeout_bin" ] || { echo "timeout(1) (coreutils) is required to bound the generators" >&2; exit 2; }
# Generators outlive one scenario's warmup, window and latency run, and never 60s.
gen_s=55
uid=$(id -u)
repo=$(pwd -P)
[ -f "$repo/go.mod" ] && [ -d "$repo/internal/uiapi" ] || { echo "run from the sidecar repository root" >&2; exit 2; }

# Unix socket paths are limited to 103 bytes, so the tree stays short under /tmp.
root=$(cd "$(mktemp -d /tmp/sidecar-uiapi-measure.XXXXXX)" && pwd -P)
: > "$root/.sidecar-uiapi-measure-owned"
socket="$root/tmux/tmux-$uid/default"
server_pid=""

cleanup() {
	status=$?
	if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
		kill -TERM "$server_pid" 2>/dev/null || true
		wait "$server_pid" 2>/dev/null || true
	fi
	if [ -S "$socket" ]; then
		env -u TMUX -u TMUX_PANE "$tmux_bin" -S "$socket" kill-server 2>/dev/null || true
	fi
	if [ -f "$root/.sidecar-uiapi-measure-owned" ]; then
		rm -rf "$root"
	fi
	exit "$status"
}
trap cleanup EXIT INT TERM

fail() { echo "ui-api-measure: FAIL: $*" >&2; exit 1; }
step() { echo "== $*" >&2; }
t() { env -u TMUX -u TMUX_PANE "$tmux_bin" -S "$socket" "$@"; }

mkdir -p "$root/tmux/tmux-$uid" "$root/state" "$root/config" "$root/project" "$root/bin" "$root/log"
chmod 700 "$root/tmux" "$root/tmux/tmux-$uid"
export XDG_STATE_HOME="$root/state"
export TMUX_TMPDIR="$root/tmux"
export SIDECAR_ISOLATED_STATE=1
config="$root/config/config.json"
printf '{"projects":{"list":[{"name":"measure","path":"%s"}]}}\n' "$root/project" > "$config"
mkdir -p "$root/state/sidecar/projects/measure"
printf '{"path":"%s"}\n' "$root/project" > "$root/state/sidecar/projects/measure/meta.json"
git init -q "$root/project"

step "build"
go build -o "$root/sidecar" ./cmd/sidecar
go build -o "$root/uiapimeasure" ./internal/tools/uiapimeasure
sc() { "$root/sidecar" -config "$config" "$@"; }

# An agent-like full-screen repaint: alternate screen, every row rewritten with
# colour at a fixed rate, a spinner and counters changing on most rows.
cat > "$root/redraw.py" <<'PY'
import shutil, sys, time
hz = float(sys.argv[1])
cols, rows = shutil.get_terminal_size()
spin = "|/-\\"
w = sys.stdout
w.write("\x1b[?1049h\x1b[?25l")
i = 0
try:
    while True:
        i += 1
        out = ["\x1b[H"]
        out.append("\x1b[1;37;44m %s working  frame %-8d\x1b[K\x1b[0m\r\n" % (spin[i % 4], i))
        for r in range(1, rows - 1):
            n = (i * 7 + r * 13) % 1000
            bar = "#" * ((i + r) % 40)
            line = "\x1b[3%dm%3d\x1b[0m  task-%02d  %-40s %6d tok  \x1b[2m%s\x1b[0m" % (1 + r % 6, r, r, bar, n * 37, "x" * (r % 20))
            out.append(line[: cols + 40] + "\x1b[K\r\n")
        out.append("\x1b[7m esc to interrupt  %d \x1b[K\x1b[0m" % i)
        w.write("".join(out))
        w.flush()
        time.sleep(1 / hz)
finally:
    w.write("\x1b[?25h\x1b[?1049l")
    w.flush()
PY

if [ "$count" = 1 ]; then
	# A capture is a command on a control client's stdin; tee it to a log.
	cat > "$root/bin/tmux" <<EOF
#!/bin/sh
printf '%s\n' "\$*" >> "$root/log/spawn.log"
for a in "\$@"; do
	if [ "\$a" = "-C" ]; then
		tee -a "$root/log/control.log" | "$tmux_bin" "\$@"
		exit \$?
	fi
done
exec "$tmux_bin" "\$@"
EOF
	chmod 755 "$root/bin/tmux"
	: > "$root/log/spawn.log"
	: > "$root/log/control.log"
	count_flags="-control-log $root/log/control.log -spawn-log $root/log/spawn.log"
	serve_path="$root/bin:$PATH"
else
	count_flags=""
	serve_path="$PATH"
fi

step "create five managed shells on the private tmux server"
shells=""
for name in busy1 busy2 busy3 busy4 echo; do
	created=$(cd "$root/project" && sc create shell --name "measure $name" --json --wait 0)
	session=$(printf '%s' "$created" | python3 -c 'import json,sys; print(json.load(sys.stdin)["shell"]["session"])')
	t has-session -t "$session" || fail "shell $session is not on the private server"
	# Every pane is 160x48, a typical browser terminal, so frame sizes are comparable.
	t resize-window -t "$session" -x 160 -y 48
	shells="$shells $session"
done
set -- $shells
busy1=$1 busy2=$2 busy3=$3 busy4=$4 echo_s=$5
tmux_pid=$(t display-message -p '#{pid}')
sleep 2 # let the shells finish starting

step "sidecar api serve"
PATH="$serve_path" "$root/sidecar" -config "$config" api serve --port 0 --json > "$root/serve.out" 2> "$root/serve.err" &
server_pid=$!
i=0
while [ ! -s "$root/serve.out" ]; do
	i=$((i + 1))
	[ "$i" -le 100 ] || fail "server did not start: $(cat "$root/serve.err")"
	kill -0 "$server_pid" 2>/dev/null || fail "server exited: $(cat "$root/serve.err")"
	sleep 0.1
done
tcp=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["tcp"])' "$root/state/sidecar/api/endpoint.json")
token=$(sc api pair --origin http://measure.example --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')

line_gen() { t send-keys -t "$1" "$timeout_bin $gen_s sh -c 'while :; do date; sleep 0.01; done'" Enter; }
redraw_gen() { t send-keys -t "$1" "$timeout_bin $gen_s python3 $root/redraw.py 25" Enter; }
cat_gen() { t send-keys -t "$1" "$timeout_bin $gen_s cat" Enter; }
stop_all() {
	for s in $busy1 $busy2 $busy3 $busy4 $echo_s; do t send-keys -t "$s" C-c; done
	sleep 1
	for s in $busy1 $busy2 $busy3 $busy4 $echo_s; do t send-keys -t "$s" clear Enter; done
	sleep 1
}
measure() {
	label=$1
	shift
	# shellcheck disable=SC2086
	"$root/uiapimeasure" -url "ws://$tcp/api/v0/terminal" -bearer "$token" -pid "$server_pid" -tmux-pid "$tmux_pid" \
		-window "${window}s" -keystrokes "$keystrokes" $count_flags -label "$label" "$@" | tee -a "$root/results.jsonl"
}

step "scenario: four generators, nothing attached (tmux baseline)"
line_gen "$busy1"; line_gen "$busy2"; redraw_gen "$busy3"; redraw_gen "$busy4"
sleep 1
measure "4 generators, 0 attached"
stop_all

step "scenario: one idle terminal, keystroke latency"
cat_gen "$echo_s"
sleep 1
measure "idle, 1 attached (echo)" -echo "$echo_s" -window 0s
stop_all

step "scenario: one busy terminal, line output"
line_gen "$busy1"
sleep 1
measure "1 busy: line output" -busy "$busy1"
stop_all

step "scenario: one busy terminal, 25 Hz full-screen redraw"
redraw_gen "$busy3"
sleep 1
measure "1 busy: 25 Hz redraw" -busy "$busy3"
stop_all

step "scenario: four busy terminals (2 line, 2 redraw) plus keystroke latency on a fifth"
line_gen "$busy1"; line_gen "$busy2"; redraw_gen "$busy3"; redraw_gen "$busy4"; cat_gen "$echo_s"
sleep 1
measure "4 busy (2 line + 2 redraw) + echo" -busy "$busy1,$busy2,$busy3,$busy4" -echo "$echo_s"
stop_all

kill -TERM "$server_pid"
wait "$server_pid" || fail "serve exited non-zero: $(cat "$root/serve.err")"
server_pid=""

step "summary"
python3 - "$root/results.jsonl" <<'PY'
import json, sys
rows = [json.loads(l) for l in open(sys.argv[1]) if l.strip()]
def g(r, k):
    v = r.get(k)
    return "" if v is None else str(v)
print("| Scenario | frames/s | wire KB/s | gzip/msg KB/s | deflate stream KB/s | capture-pane/s | sidecar CPU % | tmux server CPU % | echo p50 ms | echo p95 ms |")
print("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |")
for r in rows:
    e = r.get("keystroke_echo_ms") or {}
    print("| %s | %s | %s | %s | %s | %s | %s | %s | %s | %s |" % (r["label"], g(r, "frames_per_s"), g(r, "wire_kb_per_s"),
        g(r, "gzip_per_message_kb_per_s"), g(r, "deflate_stream_kb_per_s"), g(r, "capture_pane_per_s"),
        g(r, "sidecar_cpu_pct"), g(r, "tmux_server_cpu_pct"), e.get("p50", ""), e.get("p95", "")))
PY
echo "private_socket=$socket" >&2
echo "ui-api-measure: done" >&2
