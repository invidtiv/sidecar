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
python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));rows=[r for s in d["sections"] for r in s["rows"]];assert d["total"]==3;root=next(r for r in rows if r.get("target")=="fixture-echo");assert root["path"]=="/workspace/fixture" and not root.get("content_workspace_id");linked=[r for r in rows if r.get("content_workspace_id")];assert len(linked)==2 and linked[0]["content_workspace_id"]==linked[1]["content_workspace_id"];assert all(c["content_workspace_id"]==r["content_workspace_id"] for r in linked for c in r.get("candidates",[]))' "$root/sessions.json"
linked=$(python3 -c 'import json,sys,urllib.parse;d=json.load(open(sys.argv[1]));print(urllib.parse.quote(next(r["content_workspace_id"] for s in d["sections"] for r in s["rows"] if r.get("content_workspace_id")),safe=""))' "$root/sessions.json")
for workspace in "" "$linked"; do
	curl -fsS --unix-socket "$socket" "http://sidecar/api/v0/projects/fixture-project/content?kind=file&target=README.md&workspace=$workspace" > "$root/content.json"
	python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));assert d["content"].startswith("# Fixture project");assert d["path"]==("/workspace/feature" if sys.argv[2] else "/workspace/fixture")+"/README.md"' "$root/content.json" "$workspace"
	curl -fsS --unix-socket "$socket" "http://sidecar/api/v0/projects/fixture-project/tree?workspace=$workspace" > /dev/null
	curl -fsS --unix-socket "$socket" -D "$root/layout.headers" "http://sidecar/api/v0/projects/fixture-project/layout?workspace=$workspace" > /dev/null
	etag=$(python3 -c 'import sys;print(next(line.split(":",1)[1].strip() for line in open(sys.argv[1]) if line.lower().startswith("etag:")))' "$root/layout.headers")
	curl -fsS --unix-socket "$socket" -X PUT -H 'Content-Type: application/json' -H "If-Match: $etag" -d '{"layout":{"kind":"terminal","session":"fixture-echo"}}' "http://sidecar/api/v0/projects/fixture-project/layout?workspace=$workspace" > /dev/null
done
curl -fsS --unix-socket "$socket" 'http://sidecar/api/v0/projects/fixture-project/layout?workspace=fixture-shell' > "$root/legacy-layout.json"
python3 -c 'import json,sys;assert json.load(open(sys.argv[1]))["layout"]["session"]=="fixture-echo"' "$root/legacy-layout.json"
sc api status --json > "$root/status.json"
python3 -c 'import json,sys;d=json.load(open(sys.argv[1]));assert d["server_version"]=="fixture" and d["terminals"]==[]' "$root/status.json"
# Workspace fixtures retain recoverable records and never fall through to
# machine mutations, even for Local callers with full authority.
curl -fsS --unix-socket "$socket" http://sidecar/api/v0/projects > "$root/projects.json"
curl -fsS --unix-socket "$socket" 'http://sidecar/api/v0/projects/fixture-project/workspace?search=absent' > "$root/workspace.json"
python3 -c 'import json,sys;p=json.load(open(sys.argv[1]));w=json.load(open(sys.argv[2]));assert p["projects"][0]["key"]=="fixture-project";assert w["catalog"]["total"]==0;assert len(w["shells"])==2 and w["shells"][1]["status"]=="forgotten"' "$root/projects.json" "$root/workspace.json"
code=$(curl -sS --unix-socket "$socket" -o "$root/write-refused.json" -w '%{http_code}' -X POST -H 'Content-Type: application/json' -d '{}' http://sidecar/api/v0/projects/fixture-project/shells/create)
[ "$code" = 409 ]
python3 -c 'import json,sys;assert json.load(open(sys.argv[1]))["error"]["code"]=="unsupported"' "$root/write-refused.json"
# Exercise real pairing and ticket guards rather than bypassing browser auth.
origin=http://fixture.example
token=$(sc api pair --origin "$origin" --json | python3 -c 'import json,sys;print(json.load(sys.stdin)["token"])')
ticket=$(curl -fsS -X POST -H "Origin: $origin" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -H 'X-Sidecar-Request: 1' -d '{}' "http://$tcp/api/v0/ws-tickets" | python3 -c 'import json,sys;print(json.load(sys.stdin)["ticket"])')
timeout 40 "$root/proof" -url "ws://$tcp/api/v0/terminal?ticket=$ticket" -origin "$origin" -target fixture-echo -literal-echo -marker FIXTURE_ECHO
# Tickets are single-use. Open a fresh negotiated v1 connection with WebSocket
# compression, retaining the unchanged legacy-client journey above.
ticket=$(curl -fsS -X POST -H "Origin: $origin" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -H 'X-Sidecar-Request: 1' -d '{}' "http://$tcp/api/v0/ws-tickets" | python3 -c 'import json,sys;print(json.load(sys.stdin)["ticket"])')
timeout 40 "$root/proof" -url "ws://$tcp/api/v0/terminal?ticket=$ticket" -origin "$origin" -target fixture-echo -v1 -literal-echo -marker FIXTURE_V1_ECHO
[ ! -e "$root/tmux-called" ]
kill -TERM "$server_pid"
wait "$server_pid"
server_pid=""
[ ! -e "$socket" ]
[ ! -e "$root/state/sidecar/api/endpoint.json" ]
echo 'ui-api-fixture-proof: PASS (no tmux invoked)'
