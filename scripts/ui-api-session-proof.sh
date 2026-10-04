#!/bin/sh
# Foreground client-chain proof; no service-manager or tmux mutations.
set -eu
unset TMUX TMUX_PANE
unset SIDECAR_SHELL SIDECAR_SHELL_NAME SIDECAR_MANAGED_SHELL SIDECAR_TMUX_SERVER SIDECAR_HOST
root=$(cd "$(mktemp -d /tmp/sc-u1i.XXXXXX)" && pwd -P)
cleanup() {
 status=$?
 rm -rf "$root"
 exit "$status"
}
trap cleanup EXIT INT TERM
mkdir -p "$root/state" "$root/tmux" "$root/ui" "$root/bin"
export XDG_STATE_HOME="$root/state" TMUX_TMPDIR="$root/tmux" SIDECAR_ISOLATED_STATE=1
config="$root/config.json"
printf '{}\n' > "$config"
printf OLD_UI > "$root/ui/index.html"
printf PRIVATE_OUTSIDE_ROOT > "$root/secret"
go build -o "$root/bin/first" ./cmd/sidecar
cp "$root/bin/first" "$root/bin/second"
ln -s "$root/bin/first" "$root/bin/sidecar"
timeout 100 node scripts/ui-api-session-proof.mjs "$root"
echo 'ui-api-session-proof: PASS'
