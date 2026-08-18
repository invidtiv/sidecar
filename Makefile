.PHONY: build install install-dev install-local install-worktree use-homebrew reap-test-tmux \
	install-status test-dev-install test test-v clean check-clean tag \
	release-snapshot check-release-state release release-tap \
	fmt fmt-check fmt-check-all lint lint-all goreleaser-snapshot install-hooks

# Default target
all: build

LINT_BASE ?= main

# RELEASE_VERSION must come from the environment so Make never interpolates an
# untrusted command-line value into shell source (see scripts/test-release-guards.sh).
release_goals := $(filter check-release-state release release-tap,$(MAKECMDGOALS))
ifneq ($(release_goals),)
ifneq ($(origin RELEASE_VERSION),environment)
$(error set RELEASE_VERSION in the environment, for example: RELEASE_VERSION=v0.92.0 make $(firstword $(release_goals)))
endif
endif

# Build the binary
build:
	go build -o bin/sidecar ./cmd/sidecar

# Unmanaged Go install to GOBIN. This does not change Homebrew links or PATH.
install:
	go install ./cmd/sidecar

# Managed machine-wide development installs and Homebrew switching.
install-local:
	./scripts/dev-install.sh install-local

install-worktree:
	./scripts/dev-install.sh install-worktree

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

# Reap tmux servers left behind by a test run that died by panic, timeout, or
# SIGKILL, where TestMain's teardown could not run. Never touches the
# developer's own server; see scripts/reap-test-tmux.sh and td-4d99ae.
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

# Run linter
lint:
	golangci-lint run --new-from-merge-base=$(LINT_BASE) ./...

# Run linter across the full codebase (includes legacy debt)
lint-all:
	golangci-lint run ./...

# Build for multiple platforms (local testing only — GoReleaser handles release builds)
build-all:
	GOOS=darwin GOARCH=amd64 go build -o bin/sidecar-darwin-amd64 ./cmd/sidecar
	GOOS=darwin GOARCH=arm64 go build -o bin/sidecar-darwin-arm64 ./cmd/sidecar
	GOOS=linux GOARCH=amd64 go build -o bin/sidecar-linux-amd64 ./cmd/sidecar
	GOOS=linux GOARCH=arm64 go build -o bin/sidecar-linux-arm64 ./cmd/sidecar

# Test GoReleaser locally (creates snapshot build without publishing)
release-snapshot goreleaser-snapshot:
	goreleaser release --snapshot --clean

check-release-state:
	@test -n "$${RELEASE_VERSION:-}" || { echo 'RELEASE_VERSION=vX.Y.Z is required' >&2; exit 2; }
	./scripts/check-release-state.sh pre-tag

# Cut a release: preflight, tag, wait for CI, verify/publish Homebrew formula.
release:
	@test -n "$${RELEASE_VERSION:-}" || { echo 'RELEASE_VERSION=vX.Y.Z is required' >&2; exit 2; }
	./scripts/publish-release.sh

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
