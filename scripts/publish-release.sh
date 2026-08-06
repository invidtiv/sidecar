#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_root"

# Fail early if this operator cannot complete the supported Homebrew path
# (either CI token will handle it, or local resume can). Auth for gh is still
# required so we can watch the release workflow and verify the public release.
"$repo_root/scripts/publish-homebrew-tap.sh" --check

# Confirm the tree builds the way users install it (no go.work, no replace).
echo "checking GOWORK=off build..."
GOWORK=off go build -o /dev/null ./cmd/sidecar

"$repo_root/scripts/publish-release-tag.sh"

# Wait for CI (verify + goreleaser + homebrew). If the CI homebrew job already
# published the formula, this is idempotent verification. If it failed, this
# path publishes the formula from the public tag archive.
"$repo_root/scripts/publish-homebrew-tap.sh"

echo "release $RELEASE_VERSION complete"
