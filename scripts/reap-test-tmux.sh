#!/usr/bin/env bash
#
# Reap tmux servers and directories left behind by test runs.
#
# internal/testenv tears down the private server it starts, but that teardown
# cannot run when a test binary dies by panic, by `go test` timeout, or by
# SIGKILL — Go exits those paths without unwinding TestMain. This reaps whatever
# those runs left behind.
#
# It only ever touches servers and directories under a temp directory named
# sidecar-tmux-test* (what testenv.IsolateTmux creates). The developer's own
# server is on $TMUX_TMPDIR/tmux-$UID/default or /tmp/tmux-$UID/default and is
# never a match — that is the whole point, see td-4d99ae and td-8d18de.
#
# Directories are only swept once they are older than MIN_AGE_MIN minutes, so a
# run that is in flight right now cannot have its TMUX_TMPDIR deleted out from
# under it. For internal/plugins/workspace that directory also holds the suite's
# XDG_STATE_HOME, so deleting a live one would be worse than the leak.
#
# Usage:
#   scripts/reap-test-tmux.sh          # list what would be reaped
#   scripts/reap-test-tmux.sh --kill   # actually reap

set -euo pipefail

KILL=0
[[ "${1:-}" == "--kill" ]] && KILL=1

# Directories younger than this are assumed to belong to a running test.
MIN_AGE_MIN=${REAP_MIN_AGE_MIN:-60}

if ! command -v lsof >/dev/null 2>&1; then
	echo "reap-test-tmux: lsof is required" >&2
	exit 1
fi

found=0
refused=0

while read -r pid; do
	[[ -z "$pid" ]] && continue

	# Every unix socket this process holds. A test server's is under a
	# sidecar-tmux-test* temp dir; anything else means it is not ours.
	socks=$(lsof -a -p "$pid" -U 2>/dev/null | awk 'NR>1 {print $NF}' | grep '^/' || true)

	# Refuse outright if the process touches a real default socket, whatever
	# else it holds. Belt and braces: we would rather leak than kill a session.
	if grep -qE '(^|/)tmux-[0-9]+/default$' <<<"$socks" && ! grep -q 'sidecar-tmux-test' <<<"$socks"; then
		continue
	fi
	if ! grep -q 'sidecar-tmux-test' <<<"$socks"; then
		continue
	fi

	found=$((found + 1))
	# `|| true`: pgrep listed this pid a moment ago, but these are dying
	# processes and ps exits non-zero once one is gone. Without it, pipefail
	# plus set -e would abort the whole sweep partway through.
	age=$(ps -o etime= -p "$pid" 2>/dev/null | tr -d ' ' || true)
	sock=$(grep 'sidecar-tmux-test' <<<"$socks" | head -1)
	if [[ $KILL -eq 1 ]]; then
		if kill -TERM "$pid" 2>/dev/null; then
			echo "reaped  pid=$pid age=${age:-?} sock=$sock"
		else
			echo "failed  pid=$pid age=${age:-?} sock=$sock" >&2
			refused=$((refused + 1))
		fi
	else
		echo "would reap  pid=$pid age=${age:-?} sock=$sock"
	fi
done < <(pgrep -x tmux 2>/dev/null || true)

# Sweep directories whose server is already gone. A tmux server exits by itself
# once its last session closes, so the common leftover from a panicked run is a
# directory with no process at all — which means this must run even when the
# loop above found nothing.
dirs=0
while read -r dir; do
	[[ -z "$dir" ]] && continue
	sock="$dir/tmux-$(id -u)/default"
	# A live or wedged server still owns this directory. Unlinking its socket
	# is exactly the failure this script exists to clean up after, so leave it.
	if [[ -S "$sock" ]] && lsof "$sock" >/dev/null 2>&1; then
		continue
	fi
	dirs=$((dirs + 1))
	if [[ $KILL -eq 1 ]]; then
		rm -rf "$dir" && echo "removed stale dir $dir"
	else
		echo "would remove stale dir $dir"
	fi
done < <(find "${TMPDIR:-/tmp}" -maxdepth 1 -name 'sidecar-tmux-test*' -type d -mmin +"$MIN_AGE_MIN" 2>/dev/null || true)

if [[ $found -eq 0 && $dirs -eq 0 ]]; then
	echo "no leaked test tmux servers"
	exit 0
fi

if [[ $KILL -eq 0 ]]; then
	echo
	echo "$found leaked server(s), $dirs stale dir(s). Re-run with --kill to reap."
	echo "Do not run --kill while a test suite is running."
fi

exit $((refused > 0 ? 1 : 0))
