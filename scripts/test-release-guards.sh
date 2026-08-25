#!/usr/bin/env bash
set -euo pipefail

dist=${1:-dist}
repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
[[ -d $dist ]] || {
  echo "release directory does not exist: $dist" >&2
  exit 1
}

temporary=$(mktemp -d)
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT

# Make must accept RELEASE_VERSION only as environment data. It must never
# interpolate an untrusted make command-line value into shell source.
sentinel="$temporary/version-injection-ran"
for malicious_version in \
  "v1.0.0\"; touch $sentinel; : #" \
  "v1.0.0'; touch $sentinel; : #" \
  "v1.0.0\$(touch $sentinel)" \
  "v1.0.0\$(shell touch $sentinel)"; do
  if env RELEASE_VERSION="$malicious_version" \
    make -s -C "$repo_root" check-release-state >/dev/null 2>&1; then
    echo "malicious release version unexpectedly passed" >&2
    exit 1
  fi
done
if [[ -e $sentinel ]]; then
  echo "release version was executed as shell source" >&2
  exit 1
fi

# Exercise both modes against a local bare remote: the annotated tag is valid
# only while it resolves to the live main commit.
guard_repo="$temporary/guard-repo"
remote="$temporary/origin.git"
git init --bare --quiet "$remote"
git init --quiet --initial-branch=main "$guard_repo"
mkdir "$guard_repo/scripts"
cp "$repo_root/scripts/check-release-state.sh" "$guard_repo/scripts/"
# Minimal files the guard inspects.
cat >"$guard_repo/CHANGELOG.md" <<'EOF'
# Changelog

## [v1.0.0] - 2026-01-01

- test release
EOF
cat >"$guard_repo/go.mod" <<'EOF'
module github.com/marcus/sidecar

go 1.25
EOF
(
  cd "$guard_repo"
  git add CHANGELOG.md go.mod scripts/check-release-state.sh
  git -c user.name=release-test -c user.email=release-test@example.invalid \
    commit --quiet -m initial
  git remote add origin "$remote"
  git push --quiet -u origin main
  RELEASE_VERSION=v1.0.0 ./scripts/check-release-state.sh pre-tag >/dev/null
  git -c user.name=release-test -c user.email=release-test@example.invalid \
    tag -a v1.0.0 -m "Release v1.0.0"
  git push --quiet origin refs/tags/v1.0.0
  tag_commit=$(git rev-parse "refs/tags/v1.0.0^{commit}")
  git checkout --quiet --detach "$tag_commit"
  git tag -d v1.0.0 >/dev/null
  RELEASE_VERSION=v1.0.0 ./scripts/check-release-state.sh tagged >/dev/null
  git checkout --quiet main
  git -c user.name=release-test -c user.email=release-test@example.invalid \
    commit --quiet --allow-empty -m drift
  git push --quiet origin main
  git checkout --quiet --detach v1.0.0
  if RELEASE_VERSION=v1.0.0 \
    ./scripts/check-release-state.sh tagged >/dev/null 2>&1; then
    echo "tagged state unexpectedly accepted a tag behind live main" >&2
    exit 1
  fi
)

# scripts/release.sh: version derivation and its refusals. Every scenario
# below dies before release.sh reaches its Homebrew --check call, so none of
# this touches gh or the network — a stub is only needed for the one dry-run
# happy path that runs past that call.
release_repo="$temporary/release-repo"
cp -R "$guard_repo" "$release_repo"
cp "$repo_root/scripts/release.sh" "$release_repo/scripts/"
(
  cd "$release_repo"
  git checkout --quiet main
  git add scripts/release.sh
  git -c user.name=release-test -c user.email=release-test@example.invalid \
    commit --quiet -m 'add release.sh'

  # An already-existing tag is refused, derived straight from the committed
  # CHANGELOG.md heading (## [v1.0.0], and v1.0.0 was tagged earlier in this
  # script) with no RELEASE_VERSION or BUMP needed.
  if output=$(./scripts/release.sh --dry-run 2>&1); then
    echo "release.sh accepted a version that is already tagged" >&2
    exit 1
  fi
  [[ $output == *"v1.0.0"* && $output == *"already exists"* ]] || {
    echo "release.sh did not explain the existing-tag refusal: $output" >&2
    exit 1
  }

  # An empty [Unreleased] section is refused, whether or not it is the top
  # heading — releasing a blank entry is always a mistake.
  cat >CHANGELOG.md <<'EOF'
# Changelog

## [Unreleased]

## [v1.0.0] - 2026-01-01

- test release
EOF
  if output=$(./scripts/release.sh --dry-run 2>&1); then
    echo "release.sh accepted an empty [Unreleased] section" >&2
    exit 1
  fi
  [[ $output == *empty* ]] || {
    echo "release.sh did not explain the empty-section refusal: $output" >&2
    exit 1
  }
  git checkout --quiet -- CHANGELOG.md

  # A RELEASE_VERSION that contradicts an already-stamped heading is refused,
  # naming both — the disagreement td-0dda74 exists to close.
  cat >CHANGELOG.md <<'EOF'
# Changelog

## [v1.2.0] - 2026-02-01

- something worth shipping

## [v1.0.0] - 2026-01-01

- test release
EOF
  if output=$(RELEASE_VERSION=v1.3.0 ./scripts/release.sh --dry-run 2>&1); then
    echo "release.sh accepted a RELEASE_VERSION that contradicts the stamped heading" >&2
    exit 1
  fi
  [[ $output == *"v1.3.0"* && $output == *"v1.2.0"* ]] || {
    echo "release.sh refusal did not name both versions: $output" >&2
    exit 1
  }
  git checkout --quiet -- CHANGELOG.md

  # A tree dirty beyond CHANGELOG.md is refused. BUMP derives v1.0.1 (unused
  # so far), which only leaves the dirty-tree check standing between here and
  # the Homebrew --check call.
  cat >CHANGELOG.md <<'EOF'
# Changelog

## [Unreleased]

- something worth shipping

## [v1.0.0] - 2026-01-01

- test release
EOF
  touch extra.txt
  if output=$(BUMP=patch ./scripts/release.sh --dry-run 2>&1); then
    echo "release.sh accepted a tree dirty beyond CHANGELOG.md" >&2
    exit 1
  fi
  [[ $output == *"beyond CHANGELOG.md"* ]] || {
    echo "release.sh did not explain the dirty-tree refusal: $output" >&2
    exit 1
  }
  rm extra.txt
  git checkout --quiet -- CHANGELOG.md

  # Happy path: BUMP derives the version and stamps the plan without it ever
  # being stated by hand. publish-homebrew-tap.sh is stubbed so the dry run
  # exercises release.sh's own logic without touching gh or the network.
  cat >scripts/publish-homebrew-tap.sh <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ ${1:-} == --check ]] || { echo "stub expected --check" >&2; exit 1; }
[[ -n ${RELEASE_VERSION:-} ]] || { echo "stub expected RELEASE_VERSION" >&2; exit 1; }
echo "stub: release publication prerequisites verified"
EOF
  chmod +x scripts/publish-homebrew-tap.sh
  git add scripts/publish-homebrew-tap.sh
  git -c user.name=release-test -c user.email=release-test@example.invalid \
    commit --quiet -m 'stub publish-homebrew-tap.sh for the dry-run test'
  cat >CHANGELOG.md <<'EOF'
# Changelog

## [Unreleased]

- something worth shipping

## [v1.0.0] - 2026-01-01

- test release
EOF
  output=$(BUMP=minor ./scripts/release.sh --dry-run 2>&1) || {
    echo "release.sh rejected a clean BUMP-driven dry run: $output" >&2
    exit 1
  }
  [[ $output == *"release plan: v1.1.0"* ]] || {
    echo "release.sh did not derive v1.1.0 from BUMP=minor: $output" >&2
    exit 1
  }
  [[ $output == *"dry run: stopping before any mutation"* ]] || {
    echo "release.sh dry run did not stop before mutation: $output" >&2
    exit 1
  }
  grep -Fq '## [Unreleased]' CHANGELOG.md || {
    echo "release.sh dry run mutated CHANGELOG.md" >&2
    exit 1
  }
)

# An extra executable beside the binary must be rejected.
probe_dist="$temporary/dist"
cp -R "$dist" "$probe_dist"
archive=$(find "$probe_dist" -mindepth 1 -maxdepth 1 \
  -type f -name '*darwin_amd64.tar.gz' -print -quit)
[[ -n $archive ]] || {
  echo "no darwin_amd64 archive found for negative test" >&2
  exit 1
}
unpack="$temporary/unpack"
mkdir "$unpack"
tar -xzf "$archive" -C "$unpack"
touch "$unpack/rogue"
chmod +x "$unpack/rogue"
tar -czf "$archive" -C "$unpack" .
if "$repo_root/scripts/verify-release-archives.sh" \
  "$probe_dist" >/dev/null 2>&1; then
  echo "archive verifier accepted an extra top-level executable" >&2
  exit 1
fi

# replace directives must fail closed.
replace_repo="$temporary/replace-repo"
cp -R "$guard_repo" "$replace_repo"
(
  cd "$replace_repo"
  git checkout --quiet main
  printf '\nreplace github.com/marcus/td => ../td\n' >>go.mod
  git -c user.name=release-test -c user.email=release-test@example.invalid \
    add go.mod
  git -c user.name=release-test -c user.email=release-test@example.invalid \
    commit --quiet -m 'add replace'
  git push --quiet origin main
  if RELEASE_VERSION=v1.0.1 \
    ./scripts/check-release-state.sh pre-tag >/dev/null 2>&1; then
    echo "pre-tag state unexpectedly accepted a replace directive" >&2
    exit 1
  fi
)

echo "release guard negative tests passed"
