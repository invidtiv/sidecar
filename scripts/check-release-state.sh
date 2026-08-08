#!/usr/bin/env bash
set -euo pipefail

mode=${1:-}
case "$mode" in
  pre-tag | tagged) ;;
  *)
    echo "usage: RELEASE_VERSION=vX.Y.Z $0 pre-tag|tagged" >&2
    exit 2
    ;;
esac

release_version=${RELEASE_VERSION:-}
if [[ ! $release_version =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "Error: RELEASE_VERSION must be strict SemVer vX.Y.Z" >&2
  exit 1
fi

if [[ -n $(git status --porcelain) ]]; then
  echo "Error: working tree is not clean" >&2
  exit 1
fi
if ! git remote get-url origin >/dev/null 2>&1; then
  echo "Error: origin remote is not configured" >&2
  exit 1
fi

remote_head=$(git ls-remote origin refs/heads/main | awk '{print $1}')
if [[ -z $remote_head ]]; then
  echo "Error: origin/main does not exist" >&2
  exit 1
fi

# Sidecar changelog entries keep the v-prefix: ## [vX.Y.Z] - YYYY-MM-DD
if ! grep -Fq "## [$release_version] - " CHANGELOG.md; then
  echo "Error: CHANGELOG.md has no $release_version release entry" >&2
  exit 1
fi

# replace directives break go install from the module proxy.
if grep -E '^\s*replace\s' go.mod >/dev/null 2>&1; then
  echo "Error: go.mod contains replace directives; remove them before releasing" >&2
  exit 1
fi

# Go CI (tests + golangci-lint) must be green on the commit being released.
# This used to be a manual checklist item ("confirm Go CI is green") and main
# sat red for a full day across two merges before a release caught it.
# Only enforced when origin resolves to a real GitHub repo `gh` can query
# (skipped for the synthetic local-bare-remote repos test-release-guards.sh
# exercises this script against).
if command -v gh >/dev/null 2>&1 && gh repo view >/dev/null 2>&1; then
  ci_runs=$(gh run list --workflow=go-ci.yml --branch main --limit 20 \
    --json headSha,status,conclusion -q \
    "[.[] | select(.headSha == \"$remote_head\")]" 2>/dev/null || echo '[]')
  ci_count=$(jq 'length' <<<"$ci_runs")
  if [[ $ci_count == 0 ]]; then
    echo "Error: no Go CI run found for $remote_head yet; wait for it to start" >&2
    exit 1
  fi
  ci_status=$(jq -r '.[0].status' <<<"$ci_runs")
  ci_conclusion=$(jq -r '.[0].conclusion' <<<"$ci_runs")
  if [[ $ci_status != completed ]]; then
    echo "Error: Go CI is still $ci_status on $remote_head; wait for it to finish" >&2
    exit 1
  fi
  if [[ $ci_conclusion != success ]]; then
    echo "Error: Go CI is $ci_conclusion on $remote_head; fix it before releasing" >&2
    exit 1
  fi
else
  echo "Warning: gh unavailable or origin is not a resolvable GitHub repo; skipping automated Go CI status check" >&2
fi

case "$mode" in
  pre-tag)
    if [[ $(git branch --show-current) != main ]]; then
      echo "Error: releases must be cut from main" >&2
      exit 1
    fi
    if [[ $(git rev-parse HEAD) != "$remote_head" ]]; then
      echo "Error: HEAD does not match live origin/main (push or pull first)" >&2
      exit 1
    fi
    if git rev-parse --verify --quiet "refs/tags/$release_version" >/dev/null; then
      echo "Error: local tag $release_version already exists" >&2
      exit 1
    fi
    if [[ -n $(git ls-remote --tags origin \
      "refs/tags/$release_version" "refs/tags/$release_version^{}") ]]; then
      echo "Error: remote tag $release_version already exists" >&2
      exit 1
    fi
    ;;
  tagged)
    # actions/checkout resolves a tag event to its commit but does not
    # necessarily leave the annotated tag object under refs/tags. Fetch the
    # already validated exact ref before checking its object type and target.
    if ! git fetch --force --no-tags origin \
      "refs/tags/$release_version:refs/tags/$release_version"; then
      echo "Error: could not fetch remote tag $release_version" >&2
      exit 1
    fi
    if [[ $(git cat-file -t "refs/tags/$release_version" 2>/dev/null || true) != tag ]]; then
      echo "Error: $release_version must be an annotated tag" >&2
      exit 1
    fi
    tag_commit=$(git rev-parse "refs/tags/$release_version^{commit}")
    if [[ $(git rev-parse HEAD) != "$tag_commit" ]]; then
      echo "Error: checked-out HEAD does not match $release_version" >&2
      exit 1
    fi
    if [[ $tag_commit != "$remote_head" ]]; then
      echo "Error: $release_version does not point at live origin/main" >&2
      exit 1
    fi
    remote_tag_commit=$(git ls-remote origin \
      "refs/tags/$release_version^{}" | awk '{print $1}')
    if [[ $remote_tag_commit != "$tag_commit" ]]; then
      echo "Error: remote $release_version does not resolve to the checked-out commit" >&2
      exit 1
    fi
    ;;
esac

echo "release state verified for $release_version ($mode)"
