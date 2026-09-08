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
  /tmp/sidecar-mobile-history-*|/private/tmp/sidecar-mobile-history-*) ;;
  *) echo "proof ROOT must be an absolute task-specific path under /tmp/sidecar-mobile-history-*" >&2; exit 2 ;;
esac
case "$root" in
  *[!A-Za-z0-9_./-]*) echo "proof ROOT contains unsupported characters" >&2; exit 2 ;;
esac
[ ! -e "$root" ] || { echo "refusing existing proof root: $root" >&2; exit 1; }

owned=0
cleanup() {
  if [ "$owned" = 1 ]; then
    ./scripts/mobile-service-proof.sh stop "$root" "$tmux_bin" >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

./scripts/mobile-service-proof.sh prepare "$root" "$tmux_bin" > "$root-prepare.json"
owned=1
python3 ./scripts/mobile-history-proof.py "$root" "$tmux_bin" > "$root/result.json"
cat "$root/result.json"

