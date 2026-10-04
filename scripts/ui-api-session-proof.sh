#!/bin/sh
# Foreground client-chain proof; no service-manager or tmux mutations.
set -eu
unset TMUX TMUX_PANE
root=$(cd "$(mktemp -d /tmp/sc-u1i.XXXXXX)" && pwd -P)
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
fail() { echo "ui-api-session-proof: FAIL: $*" >&2; exit 1; }
mkdir -p "$root/state" "$root/tmux" "$root/ui" "$root/bin"
export XDG_STATE_HOME="$root/state" TMUX_TMPDIR="$root/tmux" SIDECAR_ISOLATED_STATE=1
config="$root/config.json"
printf '{}\n' > "$config"
printf OLD_UI > "$root/ui/index.html"
printf PRIVATE_OUTSIDE_ROOT > "$root/secret"
go build -o "$root/bin/first" ./cmd/sidecar
cp "$root/bin/first" "$root/bin/second"
ln -s "$root/bin/first" "$root/bin/sidecar"
sc() { "$root/bin/sidecar" -config "$config" "$@"; }
port=0
start() {
 : > "$root/serve.out"
 "$root/bin/sidecar" -config "$config" api serve --fixtures testdata/ui-api/v0 --ui "$root/ui" --port "$port" --json > "$root/serve.out" 2> "$root/serve.err" &
 server_pid=$!
 i=0
 while [ ! -s "$root/serve.out" ]; do
  i=$((i+1)); [ "$i" -le 100 ] || fail "startup timeout: $(cat "$root/serve.err")"
  kill -0 "$server_pid" 2>/dev/null || fail "startup failed: $(cat "$root/serve.err")"
  sleep 0.1
 done
 tcp=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["tcp"])' "$root/serve.out")
 port=${tcp##*:}
 base="http://$tcp"
}
stop() { kill -TERM "$server_pid"; wait "$server_pid" || fail 'shutdown failed'; server_pid=""; }
pair() {
 sc api open --print > "$root/pair.url"
 code=$(python3 -c 'import sys,urllib.parse as u;print(u.parse_qs(u.urlparse(open(sys.argv[1]).read().strip()).fragment)["code"][0])' "$root/pair.url")
 curl -fsS -X POST -H "Origin: $base" -H 'Content-Type: application/json' -H 'X-Sidecar-Request: 1' -d "{\"code\":\"$code\"}" "$base/api/v0/pairing/exchange" > "$root/exchange.json"
 python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["token"])' "$root/exchange.json"
}
auth_status() { curl -sS -o /dev/null -w '%{http_code}' -H "Origin: $base" -H "Authorization: Bearer $1" "$base/api/v0/hello"; }
start
token=$(pair)
other_tab=$(pair)
store="$root/state/sidecar/api/sessions.json"
mode=$(stat -f '%Lp' "$store" 2>/dev/null || stat -c '%a' "$store")
[ "$mode" = 600 ] || fail "sessions mode $mode"
if grep -q "$token" "$store"; then fail 'plaintext browser token on disk'; fi
[ "$(auth_status "$token")" = 200 ] || fail 'second pairing invalidated first tab'
echo '== rebuild static UI under the running server'
rm -rf "$root/ui"
mkdir "$root/ui"
printf NEW_UI > "$root/ui/index.html"
ln -s "$root/secret" "$root/ui/escape"
[ "$(curl -fsS "$base/s/aerie/demo")" = NEW_UI ] || fail 'rebuilt SPA unavailable'
[ "$(curl -fsS "$base/escape")" = NEW_UI ] || fail 'symlink escape leaked outside root'
echo '== ordinary restart retains both browser tokens'
stop
start
[ "$(auth_status "$token")" = 200 ] || fail 'browser token lost on restart'
[ "$(auth_status "$other_tab")" = 200 ] || fail 'other tab lost on restart'
echo '== executable upgrade exits cleanly; replacement reuses browser credential'
ln -s "$root/bin/second" "$root/bin/sidecar.next"
mv -f "$root/bin/sidecar.next" "$root/bin/sidecar"
i=0
while kill -0 "$server_pid" 2>/dev/null; do
 i=$((i+1)); [ "$i" -le 80 ] || fail 'upgrade did not stop server'
 sleep 0.1
done
wait "$server_pid" || fail 'upgrade did not exit cleanly'
server_pid=""
grep -q 'executable changed' "$root/serve.err" || fail 'upgrade not logged'
start
[ "$(auth_status "$token")" = 200 ] || fail 'browser token lost after upgrade'
echo '== CLI session revocation survives restart'
sc api pair --revoke-sessions --origin "$base" --json > "$root/revoked.json"
python3 -c 'import json,sys;assert json.load(open(sys.argv[1]))["revoked"]==2' "$root/revoked.json"
stop
start
[ "$(auth_status "$token")" = 401 ] || fail 'revoked browser token resurrected'
[ "$(auth_status "$other_tab")" = 401 ] || fail 'revoked other tab resurrected'
echo '== CLI origin revocation purges matching browser session durably'
token=$(pair)
sc api pair --origin "$base" --json > "$root/origin.json"
sc api pair --revoke "$base" --json > "$root/revoked-origin.json"
stop
start
[ "$(auth_status "$token")" = 401 ] || fail 'origin revoke resurrected browser token'
stop
echo 'ui-api-session-proof: PASS'
