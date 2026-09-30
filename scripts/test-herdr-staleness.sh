#!/usr/bin/env bash
# Exercise the real workflow decision with local locks and a stubbed GitHub API.
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
test_dir=$(mktemp -d "${TMPDIR:-/tmp}/sidecar-herdr-staleness.XXXXXX")
trap 'rm -rf "$test_dir"' EXIT
mkdir -p "$test_dir/bin" "$test_dir/scripts" "$test_dir/internal/agentactivity/manifests"
cp "$repo/scripts/herdr-staleness.sh" "$test_dir/scripts/"

# Run the actual Actions shell block so a regression to "any open PR is enough"
# fails this test. No copy of the workflow's decision logic lives in the test.
awk '
  /- name: Compare against the committed lock/ { found = 1; next }
  found && /^        run: \|$/ { body = 1; next }
  body && /^          / { print substr($0, 11); next }
  body && /^$/ { print; next }
  body { exit }
' "$repo/.github/workflows/herdr-sync.yml" > "$test_dir/compare.sh"
[ -s "$test_dir/compare.sh" ] || { echo "missing workflow comparison step" >&2; exit 1; }

cat > "$test_dir/bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
if [ "$1 $2" = 'pr list' ]; then
  [[ " $* " == *' --base main '* ]] || exit 91
  if [ "$TEST_REVIEW" != absent ]; then
    printf '{"number":42,"headRefOid":"abc123"}\n'
  fi
elif [ "$1" = api ]; then
  [[ "$2" == *'?ref=abc123' ]] || exit 92
  base64 < "$TEST_PROPOSAL"
else
  echo "unexpected gh request: $*" >&2
  exit 93
fi
GH
chmod +x "$test_dir/bin/gh"

cat > "$test_dir/fresh.json" <<'JSON'
{"agents":[
  {"id":"codex","version":"2026.09.23.1","updated_at":"2000-01-01T00:00:00Z"},
  {"id":"letta","version":"2026.09.15.1","updated_at":"2000-01-01T00:00:00Z"}
]}
JSON
cat > "$test_dir/old.json" <<'JSON'
{"agents":[{"id":"codex","version":"2026.8.9.1","updated_at":"2000-01-01T00:00:00Z"}]}
JSON
cat > "$test_dir/partial.json" <<'JSON'
{"agents":[{"id":"codex","version":"2026.09.23.1","updated_at":"2000-01-01T00:00:00Z"}]}
JSON

check_case() {
  local name=$1 committed=$2 review=$3 proposal=$4 expected=$5
  cp "$test_dir/$committed.json" "$test_dir/internal/agentactivity/manifests/upstream.lock.json"
  local status=0
  (
    cd "$test_dir"
    export PATH="$test_dir/bin:$PATH"
    export HERDR_FRESH_LOCK="$test_dir/fresh.json"
    export GITHUB_STEP_SUMMARY="$test_dir/summary" RUNNER_TEMP="$test_dir"
    export GITHUB_REPOSITORY=example/sidecar SYNC_BRANCH=bot/herdr-sync BASE_BRANCH=main MAX_AGE_DAYS=14
    export TEST_REVIEW="$review" TEST_PROPOSAL="$test_dir/$proposal.json"
    bash "$test_dir/compare.sh"
  ) > "$test_dir/output" 2>&1 || status=$?
  if [ "$status" -ne "$expected" ]; then
    cat "$test_dir/output" >&2
    echo "$name: got exit $status, expected $expected" >&2
    exit 1
  fi
  echo "PASS $name"
}

check_case 'current committed lock needs no review' fresh absent old 0
check_case 'stale committed lock with no review fails' old absent old 1
check_case 'open stale review does not hide drift' old present old 1
check_case 'review missing a new agent still fails' old present partial 1
check_case 'review containing the actual bumps passes' old present fresh 0

# A recent bump is not overdue; invalid inputs are execution failures, not a
# clean bill of health. These exercise the helper independently of GitHub.
jq '.agents |= map(.updated_at = "2999-01-01T00:00:00Z")' "$test_dir/fresh.json" > "$test_dir/recent.json"
[ -z "$("$repo/scripts/herdr-staleness.sh" "$test_dir/recent.json" "$test_dir/old.json" 14)" ]
if "$repo/scripts/herdr-staleness.sh" "$test_dir/fresh.json" "$test_dir/old.json" invalid >/dev/null 2>&1; then
  echo 'invalid age was accepted' >&2
  exit 1
fi
echo 'PASS recent bump and invalid age'
