#!/usr/bin/env bash
# Pin every sibling module (github.com/marcus/*) to its newest published tag.
# Release preflight (scripts/check-release-state.sh) refuses to tag when these
# drift behind, so this is the one command that fixes it.
set -euo pipefail

cd "$(dirname "$0")/.."

# sibling_latest <module> — newest published version of a sibling module, or
# nothing if it cannot be resolved.
#
# The VCS is asked before the module proxy. proxy.golang.org indexes a freshly
# pushed tag a minute or two late, so during a co-release asking it for
# <sibling>@latest answers with the *previous* tag — a stale answer that is
# indistinguishable from a true one, and would let this check pass on exactly
# the sibling version the release is trying to move off. The proxy stays as a
# fallback so a run without VCS access still resolves; both failing prints
# nothing, so the caller keeps its existing skip behavior.
#
# Inlined rather than sourced: test-release-guards.sh copies check-release-state.sh alone
# into a synthetic repo; this copy keeps the two in step.
sibling_latest() {
  local mod=$1 latest=""
  latest=$(GOWORK=off GOPROXY=direct go list -m -f '{{.Version}}' "$mod@latest" 2>/dev/null || true)
  if [[ -n $latest ]]; then
    printf '%s\n' "$latest"
    return 0
  fi
  latest=$(GOWORK=off go list -m -f '{{.Version}}' "$mod@latest" 2>/dev/null || true)
  [[ -n $latest ]] && printf '%s\n' "$latest"
  return 0
}

mods=$(GOWORK=off go list -m -f '{{if not .Main}}{{.Path}} {{.Version}}{{end}}' all |
  awk '$1 ~ /^github\.com\/marcus\// {print $1, $2}')

changed=0
while read -r mod cur; do
  [[ -z $mod ]] && continue
  latest=$(sibling_latest "$mod")
  if [[ -z $latest ]]; then
    echo "  $mod $cur (could not resolve latest; left alone)" >&2
    continue
  fi
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
