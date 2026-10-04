#!/bin/sh
# Isolated live proof for the UI API v0 (docs/reference/ui-api.md).
#
# Builds a temporary binary, starts a private tmux server and an isolated
# Sidecar state/config tree, creates one managed shell there, runs
# `sidecar api serve`, and exercises it: hello/sessions/status over the Local
# socket with curl --unix-socket, the guards on the Browser listener, browser
# pairing through `sidecar api open --print`, origin pairing and a ticket, and
# one terminal round-trip over the WebSocket. It removes only what it created
# and never touches the default tmux server or the real state tree.
#
# usage: scripts/ui-api-proof.sh [TMUX_BIN]
set -eu

unset TMUX TMUX_PANE
tmux_bin=${1:-$(command -v tmux)}
uid=$(id -u)
repo=$(pwd -P)
[ -f "$repo/go.mod" ] && [ -d "$repo/internal/uiapi" ] || { echo "run from the sidecar repository root" >&2; exit 2; }

root=$(cd "$(mktemp -d /tmp/sidecar-uiapi-proof.XXXXXX)" && pwd -P)
: > "$root/.sidecar-uiapi-proof-owned"
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
	if [ -f "$root/.sidecar-uiapi-proof-owned" ]; then
		rm -rf "$root"
	fi
	exit "$status"
}
trap cleanup EXIT INT TERM

fail() { echo "ui-api-proof: FAIL: $*" >&2; exit 1; }
step() { echo "== $*"; }

mkdir -p "$root/tmux/tmux-$uid" "$root/state" "$root/config" "$root/project"
chmod 700 "$root/tmux" "$root/tmux/tmux-$uid"
export XDG_STATE_HOME="$root/state"
export TMUX_TMPDIR="$root/tmux"
export SIDECAR_ISOLATED_STATE=1
config="$root/config/config.json"
printf '{"projects":{"list":[{"name":"proof","path":"%s"}]}}\n' "$root/project" > "$config"
mkdir -p "$root/state/sidecar/projects/proof"
printf '{"path":"%s"}\n' "$root/project" > "$root/state/sidecar/projects/proof/meta.json"
git init -q "$root/project"

step "build"
go build -o "$root/sidecar" ./cmd/sidecar
go build -o "$root/uiapiproof" ./internal/tools/uiapiproof
sc() { "$root/sidecar" -config "$config" "$@"; }

step "create a managed shell on the private tmux server"
created=$(cd "$root/project" && sc create shell --name "UI API proof" --json --wait 0)
session=$(printf '%s' "$created" | python3 -c 'import json,sys; print(json.load(sys.stdin)["shell"]["session"])')
env -u TMUX -u TMUX_PANE "$tmux_bin" -S "$socket" has-session -t "$session" || fail "shell $session is not on the private server"
echo "session=$session socket=$socket"

step "sidecar api serve"
# The binary itself, not the sc function: $! must be the server's PID.
"$root/sidecar" -config "$config" api serve --port 0 --json > "$root/serve.out" 2> "$root/serve.err" &
server_pid=$!
endpoint="$root/state/sidecar/api/endpoint.json"
i=0
while [ ! -s "$root/serve.out" ]; do
	i=$((i + 1))
	[ "$i" -le 100 ] || fail "server did not start: $(cat "$root/serve.err")"
	kill -0 "$server_pid" 2>/dev/null || fail "server exited: $(cat "$root/serve.err")"
	sleep 0.1
done
[ -f "$endpoint" ] || fail "endpoint.json missing"
mode=$(stat -f '%Lp' "$endpoint" 2>/dev/null || stat -c '%a' "$endpoint")
[ "$mode" = 600 ] || fail "endpoint.json mode $mode"
api_sock=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["unix_socket"])' "$endpoint")
tcp=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["tcp"])' "$endpoint")
base="http://$tcp"
echo "unix_socket=$api_sock tcp=$tcp"
if sc api serve --port 0 > /dev/null 2> "$root/second.err"; then
	fail "a second serve started on the same state tree"
fi
grep -q "already running" "$root/second.err" || fail "second serve gave no single-instance refusal: $(cat "$root/second.err")"

step "Local socket: hello, sessions, status"
local_get() { curl -fsS --unix-socket "$api_sock" "http://sidecar$1"; }
local_get /api/v0/hello | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["api_version"]==0 and d["terminal"]=={"protocol":"mobile","version":0}, d; print("hello ok", d["api_instance"])'
local_get "/api/v0/sessions?sort=name" > "$root/sessions.http.json"
sc mobile sessions --json --sort name > "$root/sessions.cli.json"
python3 - "$root/sessions.http.json" "$root/sessions.cli.json" "$session" <<'PY'
import json, sys
def strip(v):
    if isinstance(v, dict):
        v.pop("observed_at", None)
        for c in v.values(): strip(c)
    elif isinstance(v, list):
        for c in v: strip(c)
    return v
http, cli = (strip(json.load(open(p))) for p in sys.argv[1:3])
for d in (http, cli): d.pop("generation", None)
assert http == cli, "HTTP and CLI catalogs differ"
assert sys.argv[3] in json.dumps(http), "session missing from catalog"
print("sessions ok: HTTP document matches `mobile sessions --json`")
PY
local_get /api/v0/status | python3 -c 'import json,sys; d=json.load(sys.stdin); assert [l["name"] for l in d["listeners"]]==["local","browser"], d; print("status ok")'
sc api status > /dev/null || fail "sidecar api status failed"

step "Browser listener guards"
code=$(curl -s -o /dev/null -w '%{http_code}' -H 'Host: evil.example' "$base/api/v0/hello")
[ "$code" = 421 ] || fail "foreign Host answered $code"
code=$(curl -s -o /dev/null -w '%{http_code}' -H 'Origin: http://evil.example' "$base/api/v0/hello")
[ "$code" = 403 ] || fail "foreign Origin answered $code"
code=$(curl -s -o /dev/null -w '%{http_code}' "$base/api/v0/hello")
[ "$code" = 401 ] || fail "unpaired request answered $code"
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$base/api/v0/pairing/codes")
[ "$code" = 403 ] || fail "pairing route on TCP answered $code"
echo "guards ok: 421 host, 403 origin, 401 unpaired, 403 local-only"

step "pair this browser (sidecar api open --print), doing what the pairing page does"
link=$(sc api open --print)
case "$link" in "$base/pair#code="*) ;; *) fail "pairing link does not carry the code in the fragment: $link" ;; esac
pair_code=$(python3 -c 'import sys, urllib.parse as u; print(u.parse_qs(u.urlparse(sys.argv[1]).fragment)["code"][0])' "$link")
pair_next=$(python3 -c 'import sys, urllib.parse as u; print(u.parse_qs(u.urlparse(sys.argv[1]).fragment)["next"][0])' "$link")
# What the browser requests: /pair with no fragment. It must set nothing.
headers=$(curl -s -o "$root/pair.html" -D - "$base/pair")
printf '%s' "$headers" | grep -qi '^HTTP/1.1 200' || fail "pair page: $headers"
if printf '%s' "$headers" | grep -qi '^set-cookie'; then fail "pair page set a cookie"; fi
printf '%s' "$headers" | grep -i '^content-security-policy' | grep -q "default-src 'none'" || fail "pair page CSP missing"
grep -q '/api/v0/pairing/exchange' "$root/pair.html" || fail "pair page does not exchange the code"
grep -q 'sidecar.session' "$root/pair.html" || fail "pair page does not store sidecar.session"
exchange() {
	curl -s -o "$root/exchange.json" -w '%{http_code}' -X POST -H "Origin: $1" -H 'Content-Type: application/json' -H 'X-Sidecar-Request: 1' \
		-d "{\"code\":\"$pair_code\",\"next\":\"$pair_next\"}" "$base/api/v0/pairing/exchange"
}
code=$(exchange http://proof-other.example)
[ "$code" = 403 ] || fail "exchange from a foreign origin answered $code"
code=$(exchange "$base")
[ "$code" = 200 ] || fail "exchange answered $code: $(cat "$root/exchange.json")"
browser_token=$(python3 -c 'import json,sys; d=json.load(open(sys.argv[1])); assert d["next"]=="/", d; print(d["token"])' "$root/exchange.json")
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $browser_token" "$base/api/v0/hello")
[ "$code" = 200 ] || fail "session token answered $code"
code=$(curl -s -o /dev/null -w '%{http_code}' -H "Authorization: Bearer $browser_token" -H 'Origin: http://localhost:3000' "$base/api/v0/hello")
[ "$code" = 403 ] || fail "session token from another origin answered $code"
code=$(exchange "$base")
[ "$code" = 401 ] || fail "reused pairing code answered $code"
session_ticket=$(curl -fsS -X POST -H "Origin: $base" -H "Authorization: Bearer $browser_token" -H 'Content-Type: application/json' -H 'X-Sidecar-Request: 1' -d '{}' "$base/api/v0/ws-tickets" |
	python3 -c 'import json,sys; print(json.load(sys.stdin)["ticket"])')
echo "browser pairing ok: fragment link, no cookie, single-use exchange, origin-bound session token"

step "pair an origin and take a WebSocket ticket"
app=http://proof.example
token=$(sc api pair --origin "$app" --json | python3 -c 'import json,sys; print(json.load(sys.stdin)["token"])')
origins="$root/state/sidecar/api/origins.json"
mode=$(stat -f '%Lp' "$origins" 2>/dev/null || stat -c '%a' "$origins")
[ "$mode" = 600 ] || fail "origins.json mode $mode"
if grep -q "$token" "$origins"; then fail "origins.json holds the plaintext token"; fi
ticket=$(curl -fsS -X POST -H "Origin: $app" -H "Authorization: Bearer $token" -H 'Content-Type: application/json' -H 'X-Sidecar-Request: 1' -d '{}' "$base/api/v0/ws-tickets" |
	python3 -c 'import json,sys; print(json.load(sys.stdin)["ticket"])')
echo "origin paired, ticket issued"

step "terminal round-trip over the WebSocket (Browser listener, ticket)"
"$root/uiapiproof" -url "ws://$tcp/api/v0/terminal?ticket=$ticket" -origin "$app" -target "$session" | tee "$root/terminal.json"
owner=$(env -u TMUX -u TMUX_PANE "$tmux_bin" -S "$socket" show-options -v -t "$session" @sidecar-owner 2>/dev/null || true)
[ -z "$owner" ] || fail "@sidecar-owner remained after release: $owner"
"$root/uiapiproof" -url "ws://$tcp/api/v0/terminal?ticket=$ticket" -origin "$app" -target "$session" > /dev/null 2>&1 && fail "a used ticket opened a second terminal"

step "terminal round-trip over the WebSocket (same-origin UI, session ticket)"
"$root/uiapiproof" -url "ws://$tcp/api/v0/terminal?ticket=$session_ticket" -origin "$base" -target "$session" -marker UIAPI_SESSION_PROOF > /dev/null

step "terminal round-trip over the Local socket"
"$root/uiapiproof" -socket "$api_sock" -url "ws://sidecar/api/v0/terminal" -target "$session" -marker UIAPI_LOCAL_PROOF > /dev/null
local_get /api/v0/status | python3 -c 'import json,sys; d=json.load(sys.stdin); assert d["terminals"]==[], d["terminals"]; print("status ok: no open attachments")'

step "stop"
kill -TERM "$server_pid"
wait "$server_pid" || fail "serve exited non-zero: $(cat "$root/serve.err")"
server_pid=""
[ ! -e "$endpoint" ] || fail "endpoint.json survived shutdown"
[ ! -e "$api_sock" ] || fail "api.sock survived shutdown"
echo "private_socket=$socket"
echo "ui-api-proof: PASS"
