#!/bin/sh
set -eu

usage() {
  echo "usage: $0 prepare ROOT [TMUX_BIN] | run local|ssh ROOT [TMUX_BIN] | stop ROOT [TMUX_BIN]" >&2
  exit 2
}

action=${1:-}
case "$action" in
  prepare|stop) [ "$#" -ge 2 ] || usage ;;
  run) [ "$#" -ge 3 ] || usage ;;
  *) usage ;;
esac

if [ "$action" = run ]; then
  transport=$2
  root=$3
  tmux_bin=${4:-$(command -v tmux)}
	[ "$transport" = local ] || [ "$transport" = ssh ] || usage
else
  root=$2
  tmux_bin=${3:-$(command -v tmux)}
fi
case "$root" in
  /tmp/sidecar-mobile-*|/private/tmp/sidecar-mobile-*) ;;
  *) echo "proof ROOT must be an absolute task-specific path under /tmp/sidecar-mobile-*" >&2; exit 2 ;;
esac
case "$root" in
  *[!A-Za-z0-9_./-]*) echo "proof ROOT contains unsupported characters" >&2; exit 2 ;;
esac
uid=$(id -u)
socket="$root/tmux/tmux-$uid/default"
owned=0
keep=0

stop() {
	[ -f "$root/.sidecar-mobile-proof-owned" ] || { echo "refusing unowned proof root: $root" >&2; return 1; }
  if [ -S "$socket" ]; then
    env -u TMUX -u TMUX_PANE "$tmux_bin" -S "$socket" kill-server 2>/dev/null || true
  fi
}

prepare() {
  [ ! -e "$root" ] || { echo "refusing existing proof root: $root" >&2; exit 1; }
  mkdir -p "$root/state/sidecar/projects/demo" "$root/tmux/tmux-$uid" "$root/config"
	: > "$root/.sidecar-mobile-proof-owned"
	owned=1
  chmod 700 "$root/tmux" "$root/tmux/tmux-$uid"
  go build -o "$root/sidecar" ./cmd/sidecar
  env -u TMUX -u TMUX_PANE "$tmux_bin" -S "$socket" new-session -d -s sidecar-sh-mobile-m0c -x 80 -y 24 \
    "env BASH_SILENCE_DEPRECATION_WARNING=1 PS1='proof> ' /bin/bash --noprofile --norc"
  project=$(pwd -P)
  printf '{"path":"%s"}\n' "$project" > "$root/state/sidecar/projects/demo/meta.json"
  printf '{"version":1,"shells":[{"tmuxName":"sidecar-sh-mobile-m0c","displayName":"Mobile M0-C proof","namespace":"%s","createdAt":"2026-09-07T12:00:00Z","workDir":"%s"}]}\n' "$socket" "$project" > "$root/state/sidecar/projects/demo/shells.json"
  printf '{}\n' > "$root/config/config.json"
  cat > "$root/serve" <<EOF
#!/bin/sh
unset TMUX TMUX_PANE
export XDG_STATE_HOME='$root/state'
export TMUX_TMPDIR='$root/tmux'
export SIDECAR_ISOLATED_STATE=1
printf '%s\n' "\$\$" >> '$root/service-pids'
exec '$root/sidecar' -config '$root/config/config.json' mobile serve --stdio
EOF
  chmod 700 "$root/serve"
  cat > "$root/stop" <<EOF
#!/bin/sh
unset TMUX TMUX_PANE
exec '$tmux_bin' -S '$socket' kill-server
EOF
  chmod 700 "$root/stop"
  cat <<EOF
{"root":"$root","wrapper":"$root/serve","stop":"$root/stop","target":"sidecar-sh-mobile-m0c","socket":"$socket","state":"$root/state/sidecar","config":"$root/config/config.json","tmux":"$tmux_bin"}
EOF
	keep=1
}

if [ "$action" = stop ]; then
  stop
  exit 0
fi
trap 'if [ "$owned" = 1 ] && [ "$keep" = 0 ]; then stop; fi' EXIT INT TERM
prepare
if [ "$action" = prepare ]; then
  exit 0
fi
keep=0
python3 ./scripts/mobile-service-proof.py "$root" "$transport" > "$root/result.json"
owner=$(env -u TMUX -u TMUX_PANE "$tmux_bin" -S "$socket" show-options -v -t sidecar-sh-mobile-m0c @sidecar-owner 2>/dev/null || true)
geometry=$(env -u TMUX -u TMUX_PANE "$tmux_bin" -S "$socket" display-message -p -t sidecar-sh-mobile-m0c '#{pane_width}x#{pane_height}')
pids=$(sort -u "$root/service-pids" | wc -l | tr -d ' ')
[ -z "$owner" ] || { echo "mobile owner remained after release: $owner" >&2; exit 1; }
[ "$geometry" = "70x20" ] || { echo "accepted geometry was $geometry" >&2; exit 1; }
[ "$pids" = "2" ] || { echo "expected two service processes, got $pids" >&2; exit 1; }
cat "$root/result.json"
echo "private_socket=$socket"
echo "service_processes=$pids"
echo "owner_after_release=empty"
