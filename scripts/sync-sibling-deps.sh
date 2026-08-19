#!/usr/bin/env bash
# Pin every sibling module (github.com/marcus/*) to its newest published tag.
# Release preflight (scripts/check-release-state.sh) refuses to tag when these
# drift behind, so this is the one command that fixes it.
set -euo pipefail

cd "$(dirname "$0")/.."

mods=$(GOWORK=off go list -m -f '{{if not .Main}}{{.Path}} {{.Version}}{{end}}' all |
  awk '$1 ~ /^github\.com\/marcus\// {print $1, $2}')

changed=0
while read -r mod cur; do
  [[ -z $mod ]] && continue
  latest=$(GOWORK=off go list -m -f '{{.Version}}' "$mod@latest")
  if [[ $cur == "$latest" ]]; then
    echo "  $mod $cur (current)"
    continue
  fi
  echo "  $mod $cur -> $latest"
  GOWORK=off go get "$mod@$latest"
  changed=1
done <<<"$mods"

if ((changed)); then
  GOWORK=off go mod tidy
  echo "Updated go.mod/go.sum; review and commit."
else
  echo "All sibling dependencies already pinned to their newest release."
fi
