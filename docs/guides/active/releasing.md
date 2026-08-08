# Releasing sidecar

Sidecar releases are annotated Git tags on `main`. GitHub Actions verifies the
tag, builds archives for macOS and Linux (amd64 and arm64), creates the GitHub
release, and updates `marcus/homebrew-tap` from a rendered formula template.

## Prepare

1. Update the td dependency when appropriate (`GOWORK=off go get …`, tidy, and
   smoke the td tab if td moved).
2. Add a dated entry to `CHANGELOG.md` for the **v-prefixed** version
   (`## [vX.Y.Z] - YYYY-MM-DD`).
3. Make sure `main` is clean, reviewed, tested, pushed, and identical to
   `origin/main`. Confirm Go CI is green on that commit.
4. Confirm GitHub CLI authentication can read `marcus/sidecar` and push
   `marcus/homebrew-tap` (needed for verification and for local tap resume).
5. Install `curl`, `gh`, `git`, `goreleaser`, `jq`, and optionally `ruby`.

Local dry-run before publishing:

```sh
make release-snapshot
./scripts/verify-release-archives.sh dist
./scripts/test-release-guards.sh dist
./scripts/test-release-publication.sh
```

## Publish

```sh
RELEASE_VERSION=v0.92.0 make release
```

The command fails closed unless the version is strict SemVer, the working tree
is clean, `HEAD` is the live `origin/main`, the changelog entry exists, there
are no `replace` directives in `go.mod`, the tag does not exist, and the
operator can complete Homebrew publication. It then:

1. builds once with `GOWORK=off`;
2. creates and pushes the annotated tag;
3. waits for the exact tag workflow and GitHub release;
4. downloads and verifies GitHub's source archive;
5. renders `Formula/sidecar.rb` from the in-repo template;
6. pushes the tap without force (race-safe rebase), or no-ops if CI already
   published the exact formula;
7. verifies the formula committed to the remote tap.

If the tag exists but tap publication was interrupted:

```sh
RELEASE_VERSION=v0.92.0 make release-tap
```

## Verify the public install

```sh
# Binaries on the GitHub release
gh release view v0.92.0 --json assets -q '.assets[].name'

# go install into a throwaway GOBIN (do not clobber a dev machine's binary)
GOBIN=$(mktemp -d) GOWORK=off go install github.com/marcus/sidecar/cmd/sidecar@v0.92.0
"$GOBIN/sidecar" --version

# Homebrew (prefer a non-dev machine, or brew unlink first)
brew update
brew install marcus/tap/sidecar
brew test marcus/tap/sidecar
```

After public verification, deliberately choose the development machine's active
binary. Use `make install-local` to return to the canonical `main` checkout, or
`make use-homebrew` to keep the released formula active. Finish with
`make install-status`; it reports both interactive and non-interactive login
shell resolution, which can differ on this machine.

## Sidecar-specific notes

- **td embedding:** decide deliberately whether to take `@latest` or pin; a large
  jump bundled with an unrelated change makes field regressions hard to assign.
- **`go.work` / `replace`:** never ship with replace directives; always validate
  with `GOWORK=off`.
- **Lint:** Go CI runs tests *and* golangci-lint. `go test` alone is not the gate.
- **Homebrew builds from source** (avoids Gatekeeper warnings). The formula is
  rendered from `packaging/homebrew/sidecar.rb.tmpl`, not sed-edited in place.

## Recovery

1. Prefer a new patch release over rewriting history.
2. Keep the git tag; only delete an unpublished GitHub release if needed
   (`gh release delete vX.Y.Z`).
3. Resume a failed tap update with `make release-tap`.
