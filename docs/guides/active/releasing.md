# Releasing sidecar

Sidecar releases are annotated Git tags on `main`. GitHub Actions verifies the
tag, builds archives for macOS and Linux (amd64 and arm64), creates the GitHub
release, and updates `marcus/homebrew-tap` from a rendered formula template.

## Prepare

1. Pin the sibling modules (`td`, `tasks`) to their newest published releases:
   `make sync-deps`, then commit `go.mod`/`go.sum`. `check-release-state.sh`
   refuses to tag when any `github.com/marcus/*` requirement is behind its
   latest tag — v1.1.0 shipped against `tasks` v1.9.0 while v1.11.0 was already
   out because nothing checked, and `go.work` hides the drift by resolving those
   imports to the local checkouts. Both the gate and `sync-deps` run with
   `GOWORK=off` for that reason. If a sibling jumped several minors, decide
   deliberately and smoke its tab in the app.
2. Write the changelog bullets under `## [Unreleased]` — leave the heading
   unstamped; `make release` stamps it for you (see Publish).
3. Make sure `main` is clean, reviewed, tested, pushed, and identical to
   `origin/main`. `check-release-state.sh` now checks Go CI's status for that
   commit itself (via `gh run list --workflow=go-ci.yml`) and fails closed if
   it is red — this used to be a manual checklist item, and main sat red for a
   day across two merges before a release caught it. Fix red CI yourself before
   retrying; don't bypass.

   It no longer fails on CI that is merely *pending*. A release head is usually
   a docs-only changelog commit, which `go-ci.yml`'s path filters skip, so the
   gate would find no run at all; it now dispatches one (the workflow carries a
   `workflow_dispatch` trigger for this) and waits for whichever run matches the
   commit. `RELEASE_CI_TIMEOUT` bounds the wait (default 1800s) and
   `RELEASE_CI_WAIT=0` restores the old fail-fast behavior.
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

The normal flow is: write the changelog, run one command.

```sh
BUMP=minor make release      # or major / patch
```

With bullets sitting under `## [Unreleased]`, the command derives the next
version from the latest tag, stamps the heading to `## [vX.Y.Z] - <today>`,
commits `release: prepare vX.Y.Z`, pushes `main`, and publishes. The version is
stated exactly once. Two equivalent spellings:

```sh
RELEASE_VERSION=v0.92.0 make release   # explicit version; stamps [Unreleased] if present
make release                           # heading already stamped `## [vX.Y.Z] - date` by hand
```

`make release-dry-run` (with the same variables) prints the full plan —
derived version, stamp, commit, push — and exits before any mutation.

The prep step refuses an empty `[Unreleased]` section, a working tree dirty
beyond `CHANGELOG.md`, a version whose tag already exists, and a
`RELEASE_VERSION` that contradicts an already-stamped heading. Publication then
fails closed unless the version is strict SemVer, the working tree is clean,
`HEAD` is the live `origin/main`, the changelog entry exists, there are no
`replace` directives in `go.mod`, the tag does not exist, and the operator can
complete Homebrew publication. It then:

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
shell resolution, which can differ on this machine. `make install-local` and
`make install-worktree` retarget PATH-winning copies (often `~/go/bin`) at
the activated artifact so the next `sidecar` is that build.

## Sidecar-specific notes

- **td embedding:** decide deliberately whether to take `@latest` or pin; a large
  jump bundled with an unrelated change makes field regressions hard to assign.
- **`go.work` / `replace`:** never ship with replace directives; always validate
  with `GOWORK=off`.
- **Lint:** Go CI runs tests *and* full-codebase `golangci-lint` (linux,
  `GOWORK=off`, v2.12.2). `make lint` is that same command. `go test` alone
  is not the gate.
- **Homebrew builds from source** (avoids Gatekeeper warnings). The formula is
  rendered from `packaging/homebrew/sidecar.rb.tmpl`, not sed-edited in place.

## Recovery

1. Prefer a new patch release over rewriting history.
2. Keep the git tag; only delete an unpublished GitHub release if needed
   (`gh release delete vX.Y.Z`).
3. Resume a failed tap update with `make release-tap`.
