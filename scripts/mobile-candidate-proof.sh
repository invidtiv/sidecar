#!/bin/sh
set -eu

usage() {
  echo "usage: $0 ROOT [TMUX_BIN]" >&2
  exit 2
}

[ "$#" -ge 1 ] && [ "$#" -le 2 ] || usage
root=$1
tmux_bin=${2:-$(command -v tmux)}
case "$root" in
  /tmp/sidecar-mobile-candidates-*|/private/tmp/sidecar-mobile-candidates-*) ;;
  *) echo "proof ROOT must be an absolute task-specific path under /tmp/sidecar-mobile-candidates-*" >&2; exit 2 ;;
esac
case "$root" in
  *[!A-Za-z0-9_./-]*) echo "proof ROOT contains unsupported characters" >&2; exit 2 ;;
esac
[ ! -e "$root" ] || { echo "refusing existing proof root: $root" >&2; exit 1; }

uid=$(id -u)
socket="$root/tmux/tmux-$uid/default"
owned=0
cleanup() {
  if [ "$owned" = 1 ] && [ -f "$root/.sidecar-mobile-candidates-owned" ] && [ -S "$socket" ]; then
    env -u TMUX -u TMUX_PANE "$tmux_bin" -S "$socket" kill-server >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

mkdir -p "$root/tmux/tmux-$uid" "$root/state" "$root/config" "$root/repo"
: > "$root/.sidecar-mobile-candidates-owned"
owned=1
chmod 700 "$root/tmux" "$root/tmux/tmux-$uid"
git -C "$root/repo" init -q
git -C "$root/repo" -c user.name=Sidecar -c user.email=proof@invalid commit --allow-empty -qm initial
printf '{"projects":{"list":[{"name":"Candidate proof","path":"%s"}]}}\n' "$root/repo" > "$root/config/config.json"
go build -o "$root/sidecar" ./cmd/sidecar

cat > "$root/serve" <<EOF
#!/bin/sh
unset TMUX TMUX_PANE
export XDG_STATE_HOME='$root/state'
export TMUX_TMPDIR='$root/tmux'
export SIDECAR_ISOLATED_STATE=1
exec '$root/sidecar' -config '$root/config/config.json' mobile serve --stdio
EOF
chmod 700 "$root/serve"

python3 ./scripts/mobile-candidate-proof.py "$root" "$tmux_bin" > "$root/result.json"
cat "$root/result.json"
