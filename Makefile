.PHONY: build install install-dev install-local install-worktree worktree-init use-homebrew reap-test-tmux \
	install-status test-dev-install test test-v clean check-clean tag \
	release-snapshot check-release-state release release-dry-run release-tap \
	fmt fmt-check fmt-check-all lint lint-all lint-linux goreleaser-snapshot install-hooks sync-deps

# Default target
all: build

LINT_BASE ?= main

# RELEASE_VERSION must come from the environment so Make never interpolates an
# untrusted command-line value into shell source (see scripts/test-release-guards.sh).
# `release` is exempt: it hands off to scripts/release.sh, which reads
# RELEASE_VERSION as shell data (never Make-interpolated) and can derive a
# version on its own via BUMP=major|minor|patch, so no env var is required.
release_goals := $(filter check-release-state release-tap,$(MAKECMDGOALS))
ifneq ($(release_goals),)
ifneq ($(origin RELEASE_VERSION),environment)
$(error set RELEASE_VERSION in the environment, for example: RELEASE_VERSION=v0.92.0 make $(firstword $(release_goals)))
endif
endif

# Build metadata injected via ldflags. Assigned lazily with `=` so that parsing
# this Makefile for an unrelated target (`make test`) never spawns git or date.
#
# main.Version is deliberately NOT set here. These targets are unmanaged builds
# of possibly-unreleased code, and stamping a `git describe` tag onto them would
# make internal/version read the binary as an up-to-date release and stop
# offering updates. Leaving it empty keeps the devel+sha fallback in
# effectiveVersion. The managed path (scripts/dev-install.sh) sets Version itself.
BUILD_COMMIT = $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DIRTY = $(shell git diff --quiet 2>/dev/null && git diff --cached --quiet 2>/dev/null && echo false || echo true)
BUILD_DATE = $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILD_LDFLAGS = -X main.Commit=$(BUILD_COMMIT) -X main.Dirty=$(BUILD_DIRTY) -X main.BuildDate=$(BUILD_DATE)

# Build the binary
build:
	go build -ldflags "$(BUILD_LDFLAGS)" -o bin/sidecar ./cmd/sidecar

# Unmanaged Go install to GOBIN. This does not change Homebrew links or PATH.
install:
	go install -ldflags "$(BUILD_LDFLAGS)" ./cmd/sidecar

# Managed machine-wide development installs and Homebrew switching.
install-local:
	./scripts/dev-install.sh install-local

install-worktree:
	./scripts/dev-install.sh install-worktree

worktree-init:
	./scripts/worktree-init.sh

use-homebrew:
	./scripts/dev-install.sh use-homebrew

install-status:
	./scripts/dev-install.sh status

# Compatibility alias. The canonical-main guard still applies.
install-dev: install-local

test-dev-install:
	./scripts/test-dev-install.sh

# Run tests
test:
	go test ./...

# Run tests with verbose output
test-v:
	go test -v ./...

# List tmux servers left behind by a test run that died by panic, timeout, or
# SIGKILL, where TestMain's teardown could not run.
reap-test-tmux-list:
	./scripts/reap-test-tmux.sh

# Reap them. Never touches the developer's own server, and skips directories
# younger than an hour so a running suite cannot lose its TMUX_TMPDIR — but do
# not run this concurrently with `make test`. See scripts/reap-test-tmux.sh
# and td-4d99ae.
reap-test-tmux:
	./scripts/reap-test-tmux.sh --kill

# Clean build artifacts
clean:
	rm -rf bin/ dist/
	go clean

# Check for clean working tree
check-clean:
	@if [ -n "$$(git status --porcelain)" ]; then \
		echo "Error: Working tree is not clean"; \
		git status --short; \
		exit 1; \
	fi

# Legacy local tag helper. Prefer: RELEASE_VERSION=vX.Y.Z make release
# Usage: make tag VERSION=v0.1.0
tag: check-clean
ifndef VERSION
	$(error VERSION is required. Usage: make tag VERSION=v0.1.0)
endif
	@if ! echo "$(VERSION)" | grep -qE '^v[0-9]+\.[0-9]+\.[0-9]+$$'; then \
		echo "Error: VERSION must match vX.Y.Z format (got: $(VERSION))"; \
		exit 1; \
	fi
	@echo "Creating tag $(VERSION)"
	git tag -a $(VERSION) -m "Release $(VERSION)"
	@echo "Tag $(VERSION) created. Prefer RELEASE_VERSION=$(VERSION) make release"

# Show version that would be used
version:
	@git describe --tags --always --dirty 2>/dev/null || echo "dev"

# Format code
fmt:
	go fmt ./...

# Check formatting for changed Go files only (merge-base with LINT_BASE)
fmt-check:
	@files="$$(git diff --name-only --diff-filter=ACMRTUXB $(LINT_BASE)...HEAD -- '*.go')"; \
	if [ -z "$$files" ]; then \
		echo "No changed Go files to check."; \
		exit 0; \
	fi; \
	unformatted="$$(echo "$$files" | xargs gofmt -l)"; \
	if [ -n "$$unformatted" ]; then \
		echo "Unformatted changed Go files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

# Check formatting across all Go files
fmt-check-all:
	@unformatted="$$(find . -name '*.go' -not -path './vendor/*' -not -path './website/*' -print0 | xargs -0 gofmt -l)"; \
	if [ -n "$$unformatted" ]; then \
		echo "Unformatted Go files:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

# Must match golangci-lint-action version in .github/workflows/go-ci.yml.
GOLANGCI_LINT_VERSION ?= v2.13.1

# Same analysis GitHub runs: full codebase, linux, no go.work.
# --new-from-merge-base misses leftovers whose bodies were not edited
# (unused functions after their last caller is deleted).
# GOWORK=off belongs on the version query too, not just the run: under the
# dev go.work, `go list -m` answers for tasks and td as well and GOTOOLCHAIN
# collapses to "go1.26.0 1.26.0 1.25.8" — three words where one is expected.
lint lint-all lint-linux:
	@got=$$(golangci-lint version 2>/dev/null | sed -n 's/^golangci-lint has version \([0-9.]*\).*/\1/p' | head -1); \
	want=$(patsubst v%,%,$(GOLANGCI_LINT_VERSION)); \
	if [ -z "$$got" ]; then \
		echo "golangci-lint is not installed (need $(GOLANGCI_LINT_VERSION))"; \
		exit 1; \
	fi; \
	if [ "$$got" != "$$want" ]; then \
		echo "golangci-lint v$$got != GitHub $(GOLANGCI_LINT_VERSION) (.github/workflows/go-ci.yml)"; \
		exit 1; \
	fi
	GOOS=linux GOWORK=off GOTOOLCHAIN=go$(shell GOWORK=off go list -m -f '{{.GoVersion}}') golangci-lint run ./...

# Build for multiple platforms (local testing only — GoReleaser handles release builds)
build-all:
	GOOS=darwin GOARCH=amd64 go build -ldflags "$(BUILD_LDFLAGS)" -o bin/sidecar-darwin-amd64 ./cmd/sidecar
	GOOS=darwin GOARCH=arm64 go build -ldflags "$(BUILD_LDFLAGS)" -o bin/sidecar-darwin-arm64 ./cmd/sidecar
	GOOS=linux GOARCH=amd64 go build -ldflags "$(BUILD_LDFLAGS)" -o bin/sidecar-linux-amd64 ./cmd/sidecar
	GOOS=linux GOARCH=arm64 go build -ldflags "$(BUILD_LDFLAGS)" -o bin/sidecar-linux-arm64 ./cmd/sidecar

# Test GoReleaser locally (creates snapshot build without publishing)
release-snapshot goreleaser-snapshot:
	goreleaser release --snapshot --clean

# Pin sibling modules (td, tasks, …) to their newest published release.
# The release preflight refuses to tag when these drift, so this is the fix.
sync-deps:
	./scripts/sync-sibling-deps.sh

check-release-state:
	@test -n "$${RELEASE_VERSION:-}" || { echo 'RELEASE_VERSION=vX.Y.Z is required' >&2; exit 2; }
	./scripts/check-release-state.sh pre-tag

# Cut a release: derive/stamp the version, preflight, tag, wait for CI,
# verify/publish Homebrew formula. Write bullets under `## [Unreleased]` in
# CHANGELOG.md, then either let this derive the version (BUMP=major|minor|patch)
# or set RELEASE_VERSION=vX.Y.Z yourself.
release:
	./scripts/release.sh

# Print the release plan (derived version, changelog stamp, commit, push,
# publish) and stop before any mutation.
release-dry-run:
	./scripts/release.sh --dry-run

# Resume Homebrew tap publication after a successful tag/release.
release-tap:
	@test -n "$${RELEASE_VERSION:-}" || { echo 'RELEASE_VERSION=vX.Y.Z is required' >&2; exit 2; }
	./scripts/publish-homebrew-tap.sh

# Install pre-commit hooks
install-hooks:
	@chmod +x scripts/pre-commit.sh
	@hooks_dir="$$(git rev-parse --git-path hooks)"; \
	mkdir -p "$$hooks_dir"; \
	ln -sf "$$(pwd)/scripts/pre-commit.sh" "$$hooks_dir/pre-commit" || cp scripts/pre-commit.sh "$$hooks_dir/pre-commit"; \
	echo "✅ pre-commit hook installed to $$hooks_dir/pre-commit"
