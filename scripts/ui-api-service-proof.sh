#!/bin/sh
# Fake-manager contract plus foreground upgrade/UI-config proof. No manager
# changes and no tmux commands, even for cleanup. Both state axes are isolated.
set -eu
unset TMUX TMUX_PANE
root=$(cd "$(mktemp -d /tmp/sc-u1c.XXXXXX)" && pwd -P)
server_pid=""
cleanup() {
 status=$?
 if [ -n "$server_pid" ] && kill -0 "$server_pid" 2>/dev/null; then
  kill -TERM "$server_pid" 2>/dev/null || true
  wait "$server_pid" 2>/dev/null || true
 fi
 rm -rf "$root"
 exit "$status"
}
trap cleanup EXIT INT TERM
fail() { echo "ui-api-service-proof: FAIL: $*" >&2; exit 1; }
mkdir -p "$root/state" "$root/config" "$root/ui" "$root/override" "$root/tmux" "$root/bin"
export XDG_STATE_HOME="$root/state" TMUX_TMPDIR="$root/tmux" SIDECAR_ISOLATED_STATE=1
config="$root/config/config.json"
printf '{"api":{"uiDir":"%s"}}\n' "$root/ui" > "$config"
printf 'CONFIG_UI_PROOF\n' > "$root/ui/index.html"
printf 'OVERRIDE_UI_PROOF\n' > "$root/override/index.html"
echo '== fake service-manager and CLI lifecycle tests'
go test ./internal/apiservice ./internal/cli -run 'Test(Service|Manager|StableExecutable|ParseManager|APIService)' -count=1
echo '== build isolated binary'
go build -o "$root/bin/first" ./cmd/sidecar
cp "$root/bin/first" "$root/bin/second"
ln -s "$root/bin/first" "$root/bin/sidecar"
start() {
 : > "$root/serve.out"
 "$root/bin/sidecar" -config "$config" api serve --port 0 --json "$@" > "$root/serve.out" 2> "$root/serve.err" &
 server_pid=$!
 i=0
 while [ ! -s "$root/serve.out" ]; do
  i=$((i+1)); [ "$i" -le 100 ] || fail "start timeout: $(cat "$root/serve.err")"
  kill -0 "$server_pid" 2>/dev/null || fail "start failed: $(cat "$root/serve.err")"
  sleep 0.1
 done
 tcp=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["tcp"])' "$root/serve.out")
}
start
curl -fsS "http://$tcp/" | grep -q CONFIG_UI_PROOF || fail 'config api.uiDir not served'
# Even an isolated invocation cannot accidentally install a real LaunchAgent.
if "$root/bin/sidecar" -config "$config" api service install > "$root/install.out" 2> "$root/install.err"; then fail 'isolated service install succeeded'; fi
grep -q 'disabled for isolated proofs' "$root/install.err" || fail 'missing isolation refusal'
echo '== retarget launch link; clean exit, endpoint/socket removed'
ln -s "$root/bin/second" "$root/bin/sidecar.next"
mv -f "$root/bin/sidecar.next" "$root/bin/sidecar"
i=0
while kill -0 "$server_pid" 2>/dev/null; do
 i=$((i+1)); [ "$i" -le 80 ] || fail 'binary replacement did not stop API'
 sleep 0.1
done
wait "$server_pid" || fail "API did not exit cleanly: $(cat "$root/serve.err")"
server_pid=""
grep -q 'executable changed' "$root/serve.err" || fail 'replacement not logged'
[ ! -e "$root/state/sidecar/api/endpoint.json" ] || fail 'endpoint survived exit'
[ ! -e "$root/state/sidecar/api/api.sock" ] || fail 'socket survived exit'
echo '== replacement restarts against same state; explicit --ui overrides config'
start --ui "$root/override"
# A fresh watcher must baseline the replacement rather than exit again.
sleep 2.2
kill -0 "$server_pid" 2>/dev/null || fail 'replacement entered a restart loop'
curl -fsS "http://$tcp/" | grep -q OVERRIDE_UI_PROOF || fail '--ui did not override config'
kill -TERM "$server_pid"
wait "$server_pid" || fail 'shutdown failed'
server_pid=""
echo 'ui-api-service-proof: PASS'
