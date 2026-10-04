#!/bin/sh
# td-07f7b1: eight distinct concurrent CLI writers must all create durable shells.
# Original U2-b repro, now an asserted proof including cold registry allocation.
set -eu
if [ "${SIDECAR_CREATE_PROOF_BOUNDED:-}" != 1 ]; then
 exec timeout 120 env SIDECAR_CREATE_PROOF_BOUNDED=1 sh "$0"
fi
unset TMUX TMUX_PANE
unset SIDECAR_SHELL SIDECAR_SHELL_NAME SIDECAR_MANAGED_SHELL SIDECAR_TMUX_SERVER SIDECAR_HOST
root=$(mktemp -d /tmp/u2b-create-race.XXXXXX)
socket="$root/tmux/tmux-$(id -u)/default"
cleanup() {
 result=$?
 if [ -S "$socket" ]; then env -u TMUX -u TMUX_PANE tmux -S "$socket" kill-server >/dev/null 2>&1 || true; fi
 rm -rf "$root"
 exit "$result"
}
trap cleanup EXIT INT TERM
mkdir -p "$root/tmux/tmux-$(id -u)" "$root/config" "$root/project"
chmod 700 "$root/tmux" "$root/tmux/tmux-$(id -u)"
export XDG_STATE_HOME="$root/state" TMUX_TMPDIR="$root/tmux" SIDECAR_ISOLATED_STATE=1
config="$root/config/config.json"
printf '{"projects":{"list":[{"name":"proof","path":"%s"}]}}\n' "$root/project" > "$config"

go build -o "$root/sidecar" ./cmd/sidecar
# Start only our isolated socket before stressing naming/record persistence.
env -u TMUX -u TMUX_PANE tmux -S "$socket" new-session -d -s isolated-proof-anchor -c "$root/project"
i=1
while [ "$i" -le 8 ]; do
 ("$root/sidecar" -config "$config" create shell --project proof --tab --wait 0 --name "Parallel $i" --json > "$root/result-$i.json" 2> "$root/error-$i.txt" && echo 0 > "$root/exit-$i.txt" || echo $? > "$root/exit-$i.txt") &
 i=$((i+1))
done
wait
"$root/sidecar" -config "$config" shell list --project proof --json > "$root/records.json"
python3 - "$root" <<'PY'
import glob,json,pathlib,sys
root=pathlib.Path(sys.argv[1]);success=[];failed=[]
for f in sorted(root.glob('result-*.json')):
 n=f.stem.split('-')[-1];code=(root/f'exit-{n}.txt').read_text().strip()
 if code=='0':success.append(json.loads(f.read_text())['shell'])
 else:failed.append({'call':n,'exit':code,'stderr':(root/f'error-{n}.txt').read_text().strip()})
records=json.loads((root/'records.json').read_text())['shells']
print(json.dumps({'successful_results':success,'refusals':failed,'durable_records':records,'success_count':len(success),'unique_success_sessions':len({x['session'] for x in success})},indent=2))
assert not failed, failed
assert len(success)==8 and len({x['session'] for x in success})==8, success
assert len(records)==8 and {x['shell'] for x in records}=={x['session'] for x in success}, records
assert all(x['status']=='live' for x in records), records
assert {x['name'] for x in records}=={f'Parallel {n}' for n in range(1,9)}, records
print('concurrent-shell-create-proof: PASS (8 independent processes, 8 unique live durable identities)')
PY
