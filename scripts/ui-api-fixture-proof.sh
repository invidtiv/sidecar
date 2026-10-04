#!/bin/sh
# Real CLI/HTTP/WebSocket fixture journey; no tmux process is needed or started.
set -eu
unset TMUX TMUX_PANE
repo=$(pwd -P)
root=$(mktemp -d /tmp/sc-fixture.XXXXXX)
server_pid=""
cleanup() {
 result=$?
 if [ -n "$server_pid" ]; then kill -TERM "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; fi
 rm -rf "$root"
 exit "$result"
}
trap cleanup EXIT INT TERM
mkdir -p "$root/state" "$root/config" "$root/tmux" "$root/no-tmux"
export XDG_STATE_HOME="$root/state" TMUX_TMPDIR="$root/tmux" SIDECAR_ISOLATED_STATE=1
config="$root/config/config.json"
printf '{}\n' > "$config"
go build -o "$root/sidecar" ./cmd/sidecar
go build -o "$root/proof" ./internal/tools/uiapiproof
# Fail and record any accidental tmux invocation, including one using PATH.
printf '#!/bin/sh\nprintf "unexpected tmux invocation\\n" >> "%s/tmux-called"\nexit 99\n' "$root" > "$root/no-tmux/tmux"
chmod +x "$root/no-tmux/tmux"
export PATH="$root/no-tmux:$PATH"
sc() { "$root/sidecar" -config "$config" "$@"; }
sc api spec --json > "$root/spec.json"
cmp "$root/spec.json" "$repo/docs/reference/ui-api.openapi.json"
"$root/sidecar" -config "$config" api serve --port 0 --fixtures "$repo/testdata/ui-api/v0" --json > "$root/start.json" 2> "$root/server.err" &
server_pid=$!
i=0
while [ ! -s "$root/start.json" ]; do
 i=$((i+1)); [ "$i" -lt 100 ] || { cat "$root/server.err" >&2; exit 1; }
 kill -0 "$server_pid" 2>/dev/null || { cat "$root/server.err" >&2; exit 1; }
 sleep 0.1
done
socket=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["unix_socket"])' "$root/start.json")
tcp=$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1]))["tcp"])' "$root/start.json")
curl -fsS --unix-socket "$socket" http://sidecar/api/v0/sessions > "$root/sessions.json"
python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));assert d["total"]==1 and d["sections"][0]["rows"][0]["target"]=="fixture-echo"' "$root/sessions.json"
sc api status --json > "$root/status.json"
python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));assert d["server_version"]=="fixture" and d["terminals"]==[]' "$root/status.json"
# Exercise real pairing and ticket guards rather than bypassing browser auth.
origin=http://fixture.example
token=$(sc api pair --origin "$origin" --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')
ticket=$(curl -fsS -X POST -H "Origin: $origin" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -H 'X-Sidecar-Request: 1' -d '{}' "http://$tcp/api/v0/ws-tickets" | python3 -c 'import json,sys;print(json.load(sys.stdin)["ticket"])')
timeout 40 "$root/proof" -url "ws://$tcp/api/v0/terminal?ticket=$ticket" -origin "$origin" -target fixture-echo -literal-echo -marker FIXTURE_ECHO
[ ! -e "$root/tmux-called" ]
kill -TERM "$server_pid"
wait "$server_pid"
server_pid=""
[ ! -e "$socket" ]
[ ! -e "$root/state/sidecar/api/endpoint.json" ]
echo 'ui-api-fixture-proof: PASS (no tmux invoked)'
