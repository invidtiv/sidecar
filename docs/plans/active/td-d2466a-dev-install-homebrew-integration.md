# Plan: Managed dev installs and Homebrew switching (td-d2466a)

**Task:** [`td-d2466a`](td-d2466a) — Dev install script and Homebrew integration  
**Research snapshot:** 2026-08-08

---

## Decision

Add one repository-owned script that builds an immutable development artifact and safely
switches the Homebrew-prefix `sidecar` link between that artifact and the installed Homebrew
formula. Expose it through explicit Make targets for the canonical `main` checkout, any
deliberately selected worktree, Homebrew restoration, and diagnostics.

Homebrew is a requirement for these machine-wide switching commands. Do not silently fall back
to `GOPATH/bin` or `$HOME/.local/bin`: doing so would create another installation without making
it the active command and would preserve the ambiguity this work is intended to remove. The
existing `make install` remains the ordinary Go install path for callers that do not want the
managed Homebrew-prefix workflow.

## User journey

1. From the canonical checkout on `main`, `make install-local` builds and activates exactly the
   current checkout state. It does not fetch, pull, or otherwise update `main`.
2. From a linked worktree or non-`main` branch, the same command refuses with guidance to use
   `make install-worktree`; the explicit worktree command builds and activates that checkout.
3. `make install-status` identifies the Homebrew-prefix link and independently shows what the
   current shell, an interactive login zsh, and a non-interactive login zsh resolve and execute.
4. `make use-homebrew` removes only a link managed by this script and restores the already
   installed Homebrew formula. If it cannot switch safely, it leaves or restores the previous
   working installation.

This is presentation/developer tooling around Go and Homebrew, which own the underlying install
capabilities. It does not add a Sidecar CLI command or application-core API.

## Acceptance criteria

| # | Criterion | Proof |
|---|-----------|-------|
| 1 | `make install-local` works only from the canonical checkout on branch `main`, builds the current checkout (including a dirty marker), records its source, and activates it at `$(brew --prefix)/bin/sidecar`. | Isolated script test plus a real canonical-checkout run; inspect link, metadata, and `--version`. |
| 2 | `make install-worktree` works from any branch, linked worktree, or detached HEAD and records an unambiguous source path, revision, dirty state, and build identity. | Build from a temporary linked worktree and verify status/metadata refer to it. |
| 3 | Activating a dev build unlinks an installed Homebrew `sidecar` formula without editing Cellar contents or tap metadata. It refuses to replace a regular file, broken foreign link, or symlink outside the managed state root and Sidecar Cellar. | Automated fake-prefix/fake-brew cases assert success and refusal behavior. |
| 4 | `make use-homebrew` requires an installed formula, removes only the managed dev link, and runs `brew link sidecar`; it must not use `--overwrite` after preflight has established the destination is safe. Failed relinking restores the prior managed dev link and returns non-zero. | Automated success, already-active, missing-formula, foreign-target, and relink-failure/rollback cases; then real Homebrew restoration proof. |
| 5 | `make install-status` is read-only and reports the managed command directory; link class (`local`, `homebrew`, `other`, `missing`); raw and resolved link target; recorded source/revision/dirty state; direct binary version; and path/version for current, interactive-login, and non-interactive-login shell resolution. | Snapshot/assert output in all isolated link states and run it on the real machine. |
| 6 | Repeated installs are atomic from the command-link consumer's perspective, use a unique completed artifact, and do not delete older artifacts in this change. Interrupted builds or link failures do not leave a partial active binary. | Automated build/link failure cases and two successive successful installs. |
| 7 | `make install-dev` remains a compatibility alias for `install-local`; `make install` retains its existing Go-install semantics and is documented as unmanaged. | Make dry-run/target tests and documentation review. |
| 8 | Developer and release documentation consistently describes the new workflow. | Check `AGENTS.md`, `README.md`, `.claude/skills/release-sidecar/SKILL.md`, `docs/guides/active/releasing.md`, and all repository references found by `rg 'install-dev|make install'`. |

## Design and implementation

### 1. Add `scripts/dev-install.sh`

Use POSIX `sh` syntax and the repository's existing command-line dependencies. Actions:

- `install-local`: require `git rev-parse --git-common-dir` to identify the canonical checkout
  and require branch `main`, then call the shared install function.
- `install-worktree`: call the same install function without the canonical-main guard.
- `use-homebrew`: restore the installed formula transactionally.
- `status`: inspect state without mutation.

Configuration and paths:

- command: `sidecar`;
- state root: `${SIDECAR_DEV_STATE:-$HOME/.local/state/sidecar/dev-installs}`;
- Homebrew executable: resolved from `PATH`;
- activation directory: `${SIDECAR_BREW_PREFIX:-$(brew --prefix)}/bin`;
- immutable artifact directory: a filesystem-safe branch (or `detached`), checkout-path hash,
  short commit, dirty marker, UTC timestamp, and PID/build nonce;
- artifact metadata: a small inspectable file containing at least absolute source path, full
  revision, branch/detached state, dirty state, build time, and rendered version.

Build with `GOWORK=off` so a surrounding workspace cannot change the installed artifact. Set
`main.Version` to a development value derived from branch, short commit, and dirty state. Keep
the value compatible with `internal/version.isDevelopmentVersion` so a dev build does not offer
itself release updates. Build into a temporary directory, write metadata, then rename the
completed directory before changing any active link.

Before switching, classify the existing activation path by resolving relative symlinks:

- **local**: a symlink whose resolved target is below this exact managed state root;
- **homebrew**: a symlink whose resolved target is below the formula prefix returned by
  `brew --prefix sidecar` (not a broad `*Cellar/sidecar/*` string match);
- **other**: any regular file, broken link, or link to another location;
- **missing**: no directory entry.

Refuse to replace `other`. If the formula is installed, `brew unlink sidecar` before atomically
renaming a staged symlink over a managed/missing activation path. On any failure after the old
link is disturbed, restore the previous managed link or relink Homebrew and exit non-zero.

For `use-homebrew`, require `brew list --versions sidecar` and preflight the destination. Remove
only a managed link, run `brew link sidecar`, verify the resulting link is classified as
Homebrew and executable, and roll back to the saved managed target on failure. Do not pass
`--overwrite`; a foreign destination is a refusal condition, not permission to overwrite it.

`status` must never assume that the activation directory is the command the user actually runs.
Show `command -v sidecar` plus `sidecar --version` in the current process environment, then probe
both `/bin/zsh -lic` and `/bin/zsh -lc`. Quote the probe command safely and suppress startup
noise only where it would otherwise obscure the diagnostic; command failures remain visible in
the reported state.

### 2. Add isolated regression coverage

Add `scripts/test-dev-install.sh`. It must use a temporary state root, temporary Homebrew prefix,
temporary git checkout/worktree, and a fake `brew` executable with recorded calls. It must never
unlink, link, overwrite, or remove the machine's real `/opt/homebrew/bin/sidecar`.

Cover at least:

- canonical-main guard, branch worktree, linked worktree, and detached HEAD;
- clean/dirty metadata and development version output;
- missing, managed-local, Homebrew, foreign regular file, foreign symlink, and broken symlink;
- repeat install and staged-link/build failure behavior;
- Homebrew already active, formula absent, unlink failure, relink failure, rollback, and
  post-switch verification failure;
- status output and shell-resolution disagreement.

Allow the script to inject or override the Go builder and shell probe where necessary so these
failure paths are deterministic. Add the test to the repository's appropriate check target (or
create a focused Make target if the full test suite should not depend on Homebrew).

### 3. Update `Makefile`

Add phony targets:

```makefile
install-local:
	./scripts/dev-install.sh install-local

install-worktree:
	./scripts/dev-install.sh install-worktree

use-homebrew:
	./scripts/dev-install.sh use-homebrew

install-status:
	./scripts/dev-install.sh status

# Compatibility: deliberately guarded to canonical main.
install-dev: install-local
```

Keep `install` as `go install ./cmd/sidecar`; document that it is unmanaged and may not win PATH
precedence. Do not make it silently mutate Homebrew state.

### 4. Update documentation

- `AGENTS.md`: document the four managed commands, the canonical-main guard, and the isolated
  `make install` behavior.
- `README.md`: show the main/worktree/status/Homebrew workflow in Development.
- `.claude/skills/release-sidecar/SKILL.md`: after public-release verification, use
  `make install-local` when returning the dev machine to canonical `main`, or `make use-homebrew`
  when keeping the release active; always finish with `make install-status`.
- `docs/guides/active/releasing.md`: mirror that operator choice so the skill and canonical guide
  do not drift.
- Review every remaining `install-dev`/`make install` reference (including agent prompt docs),
  updating only those whose semantics are the managed developer installation.

## Verification sequence

1. Run `sh -n scripts/dev-install.sh scripts/test-dev-install.sh` and the isolated script tests.
2. Run `GOWORK=off go test ./...` and the repository's relevant formatting/lint checks.
3. From a temporary linked worktree, run `make install-worktree`, then verify the link, metadata,
   and all three shell-resolution reports. Do not launch Sidecar or touch the default tmux server.
4. Restore the machine deliberately with `make use-homebrew`; verify the activation link resolves
   below `brew --prefix sidecar`, run its `--version`, and run `make install-status` again.
5. From the canonical `main` checkout, run `make install-local` only if the desired final machine
   state is the current dirty/clean checkout; otherwise leave the verified Homebrew release active.
6. Inspect `git diff`, confirm unrelated files are untouched, and obtain independent review before
   approving/closing `td-d2466a`.

## Explicit non-goals

- Fetching or updating `main` as part of installation.
- Garbage-collecting historical dev artifacts; add retention only after real state growth warrants
  it.
- Managing arbitrary PATH entries or shell configuration.
- Replacing a foreign binary/link, using `sudo`, editing Cellar contents, or modifying tap metadata.
- Restarting Sidecar sessions or the machine's default tmux server.
