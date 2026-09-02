#!/bin/sh
# loopback-ssh.sh - Fake ssh for the portable loopback remote.
#
# Shared by scripts/loopback-remote.sh and internal/cli/agent_remote_loopback_test.go.
# There is no sshd. This ignores every -o option and the target, takes the LAST
# argument — the remote word internal/hosts rendered, `$SHELL -l -c '<quoted
# command>'` — and runs it locally by substituting a concrete shell and letting
# /bin/sh re-parse the quoting.
#
# Environment:
#   SIDECAR_LOOPBACK_SSH_DELAY  spawn delay before the command (Go duration or seconds)
#   SIDECAR_LOOPBACK_SSH_EXIT   when set, print a version-skew-style refusal after
#                               the banners and exit with that code (no command)

# A login profile that prints on both pipes is the first thing a real host
# does, and it is what the serve stream's prelude skipping, the run-verb banner
# recovery, and the stderr envelope recovery all exist for. Emit one on each,
# every time — unconditionally, because a fixture that can be talked out of
# printing it stops being the regression test for td-055768, where a single
# stdout banner left every host permanently `not-protocol`.
printf 'Welcome to loopback -- stdout banner\n'
printf 'Last login: Tue -- stderr banner\n' >&2

loopback_delay_seconds() {
    awk -v s="$1" 'BEGIN {
        if (s == "") { print "0"; exit 0 }
        if (s ~ /^[0-9]+(\.[0-9]+)?$/) { print s; exit 0 }
        u["ns"]=1e-9; u["us"]=1e-6; u["µs"]=1e-6; u["μs"]=1e-6
        u["ms"]=1e-3; u["s"]=1; u["m"]=60; u["h"]=3600
        total=0
        while (length(s) > 0) {
            if (match(s, /^[0-9]+(\.[0-9]+)?(ns|us|µs|μs|ms|s|m|h)/) == 0) {
                exit 2
            }
            tok = substr(s, RSTART, RLENGTH)
            s = substr(s, RSTART+RLENGTH)
            match(tok, /^[0-9]+(\.[0-9]+)?/)
            n = substr(tok, RSTART, RLENGTH) + 0
            unit = substr(tok, RSTART+RLENGTH)
            total += n * u[unit]
        }
        printf "%.9f\n", total
    }'
}

if [ -n "${SIDECAR_LOOPBACK_SSH_DELAY:-}" ]; then
    delay_sec=$(loopback_delay_seconds "$SIDECAR_LOOPBACK_SSH_DELAY") || {
        printf 'invalid SIDECAR_LOOPBACK_SSH_DELAY=%s\n' "$SIDECAR_LOOPBACK_SSH_DELAY" >&2
        exit 2
    }
    case "$delay_sec" in
        ''|0|0.0|0.00|0.000|0.000000000) ;;
        *) sleep "$delay_sec" ;;
    esac
fi

if [ -n "${SIDECAR_LOOPBACK_SSH_EXIT:-}" ]; then
    printf 'unknown flag "--json"\n' >&2
    exit "$SIDECAR_LOOPBACK_SSH_EXIT"
fi

last=
for arg in "$@"; do last=$arg; done
# The remote word is `$SHELL -l -c '<quoted command>'`. Strip the wrapper
# literally and let this shell re-parse the quoting, so the allow-list quoter
# is unwound by a shell rather than by a regexp.
inner=${last#* -l -c }
eval "exec /bin/sh -c $inner"
