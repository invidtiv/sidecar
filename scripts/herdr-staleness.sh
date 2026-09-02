#!/usr/bin/env bash
#
# Staleness check for the vendored Herdr manifests.
#
# The weekly sync workflow is supposed to notice an upstream manifest bump and
# open a pull request for it. When the sync itself is broken — a changed alias
# table the extractor no longer matches, a validator refusal, a network path
# that quietly stopped working — nothing else says so: the vendored tree keeps
# working, the badges keep rendering, and Sidecar just falls further behind.
# This script is what makes that visible.
#
#   scripts/herdr-staleness.sh FRESH_LOCK COMMITTED_LOCK [MAX_AGE_DAYS]
#
# FRESH_LOCK is the lock a throwaway `herdrsync` run just wrote from live
# upstream; COMMITTED_LOCK is the one in the tree. For every agent whose fresh
# version is newer than the committed one — or that upstream has and the tree
# does not — the fresh lock's `updated_at` says when upstream changed it. An
# agent that has been newer upstream for longer than MAX_AGE_DAYS (default 14)
# is printed, one per line.
#
# Printing is the whole result: stdout is empty when nothing is stale, and a
# non-zero exit means the check could not be run, not that anything is stale.
# The caller decides what a stale agent costs, because the plan's rule has a
# second half this script cannot see — an open sync pull request means the bump
# is already in review and is not stale at all.
#
# Versions are Herdr's dotted-numeric form (2026.08.29.1) and are compared
# segment by segment as numbers, not as strings, so 2026.8.9.1 does not sort
# above 2026.08.29.1.

set -euo pipefail

fresh=${1:-}
committed=${2:-}
days=${3:-14}

if [ -z "$fresh" ] || [ -z "$committed" ]; then
  echo "usage: $0 FRESH_LOCK COMMITTED_LOCK [MAX_AGE_DAYS]" >&2
  exit 2
fi
for lock in "$fresh" "$committed"; do
  if [ ! -f "$lock" ]; then
    echo "$0: no lock file at $lock" >&2
    exit 2
  fi
done
case "$days" in
  '' | *[!0-9]*)
    echo "$0: MAX_AGE_DAYS must be a whole number of days, got $days" >&2
    exit 2
    ;;
esac

# GNU date on a runner, BSD date on the maintainer's machine.
cutoff=$(date -u -d "$days days ago" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null ||
  date -u -v-"${days}"d +%Y-%m-%dT%H:%M:%SZ)

jq -r -n \
  --slurpfile fresh "$fresh" \
  --slurpfile committed "$committed" \
  --arg cutoff "$cutoff" \
  --arg days "$days" '
    def parse: (. // "") | split(".") | map(tonumber? // 0);

    (($committed[0].agents // []) | map({key: .id, value: .version}) | from_entries) as $have
    | ($fresh[0].agents // [])
    | map(select($have[.id] == null or ((.version | parse) > ($have[.id] | parse))))
    | map(select(((.updated_at // "") != "") and (.updated_at < $cutoff)))
    | sort_by(.id)
    | .[]
    | "\(.id): upstream \(.version) since \(.updated_at), lock has \($have[.id] // "no manifest") (more than \($days) days behind)"
  '
