---
name: release-sidecar
description: Release new versions of sidecar. Covers version tagging with semver, td dependency updates, go.mod validation, CHANGELOG updates, GoReleaser automation, Homebrew tap updates, and verification steps. Use when preparing or executing a release.
disable-model-invocation: true
---

# Releasing a New Version

Operator contract: **`docs/guides/active/releasing.md`**. Enforcement lives in `scripts/` and
`RELEASE_VERSION=vX.Y.Z make release`. Prefer the one-shot command over replaying
this checklist by hand.

## Prerequisites

- Go matching `go.mod`
- Clean working tree; `main` identical to live `origin/main`
- Tests and **Go CI** green on the commit you will tag (tests *and* lint)
- GitHub CLI authenticated with push access to `marcus/homebrew-tap`
- No `replace` directives in `go.mod`
- `HOMEBREW_TAP_TOKEN` secret present in the GitHub repo (CI tap job)

**Beware of go.work**: always use `GOWORK=off` when updating dependencies and
when validating install paths.

Local lint must match CI's golangci-lint **v2.12.2**, or trust CI:

```bash
gh run list --workflow=go-ci.yml --limit=1
```

## Prepare (sidecar-specific)

### 1. Version

```bash
git tag -l 'v*' | sort -V | tail -1
```

SemVer: major / minor / patch as usual.

### 2. td dependency

```bash
GOWORK=off go get github.com/marcus/td@latest
GOWORK=off go mod tidy
```

If td jumped several minors, decide deliberately (pin for a focused release vs
take latest and note it under Dependencies). Launch the app and open the td tab
when td moved.

### 3. CHANGELOG

```markdown
## [vX.Y.Z] - YYYY-MM-DD

### Features
- …

### Bug Fixes
- …

### Dependencies
- …
```

Commit the changelog (and any dependency bump) on `main`, then push so
`HEAD == origin/main`.

## Publish

```bash
# Dry-run (optional but recommended for tooling changes)
make release-snapshot
./scripts/verify-release-archives.sh dist
./scripts/test-release-guards.sh dist
./scripts/test-release-publication.sh

# Cut the release (fail-closed preflight → tag → CI → formula verify/publish)
RELEASE_VERSION=vX.Y.Z make release
```

What `make release` enforces and does is documented in `docs/guides/active/releasing.md`.

Resume only the tap step if the tag/release already exists:

```bash
RELEASE_VERSION=vX.Y.Z make release-tap
```

### CI jobs (on tag push)

1. **`verify`** — tag points at live `main`, tests, snapshot archives, release guards
2. **`release`** — GoReleaser publishes GitHub release + binaries
3. **`update-homebrew-tap`** — renders `packaging/homebrew/sidecar.rb.tmpl` and
   pushes `Formula/sidecar.rb` with downgrade/idempotency/race guards

td/nightshift formulas are **not** auto-bumped; edit them by hand when co-releasing.

## Verify

```bash
gh run list --workflow=release.yml --limit=1
gh release view vX.Y.Z --json assets -q '.assets[].name'

GOBIN=$(mktemp -d) GOWORK=off go install github.com/marcus/sidecar/cmd/sidecar@vX.Y.Z
"$GOBIN/sidecar" --version
```

`go install @vX.Y.Z` can 500 from the checksum DB for a minute or two after the
tag — wait and retry. Prefer a throwaway `GOBIN` so verification does not
clobber a dev machine's `sidecar`.

## Dev machine after release

```bash
make install-dev
# or: brew unlink sidecar
```

## Recovery

Prefer a new patch release. Keep tags. Resume tap with `make release-tap`.
See `docs/guides/active/releasing.md`.

## Checklist

- [ ] Go CI green (tests + lint) on the commit to tag
- [ ] Working tree clean; `main` == `origin/main`
- [ ] td bump considered; td tab smoke if td moved
- [ ] No `replace` in go.mod; `GOWORK=off` build works
- [ ] CHANGELOG entry `## [vX.Y.Z] - …`
- [ ] `RELEASE_VERSION=vX.Y.Z make release` succeeded
- [ ] Release assets present; formula URL/sha match (automatic)
- [ ] `go install` verified into throwaway `GOBIN`
- [ ] Dev machine still on the binary you want
