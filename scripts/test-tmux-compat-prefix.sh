#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=tmux-compat-lib.sh
source "$SCRIPT_DIR/tmux-compat-lib.sh"

root=$(mktemp -d "${TMPDIR:-/tmp}/sidecar-tmux-prefix-test.XXXXXX")
cleanup() {
    case "$root" in
        "${TMPDIR:-/tmp}"/sidecar-tmux-prefix-test.*) rm -rf -- "$root" ;;
    esac
}
trap cleanup EXIT INT TERM

expect_rejected_before_download() {
    local name=$1
    local prefix=$2
    local marker="$root/$name.curl-called"
    local fake_bin="$root/$name-bin"
    mkdir -p "$fake_bin"
    printf '#!/bin/sh\nprintf called >%q\nexit 99\n' "$marker" >"$fake_bin/curl"
    chmod +x "$fake_bin/curl"
    if PATH="$fake_bin:$PATH" "$SCRIPT_DIR/build-tmux-compat.sh" minimum "$prefix" >/dev/null 2>&1; then
        printf 'expected unsafe prefix to fail: %s\n' "$name" >&2
        exit 1
    fi
    if [[ -e "$marker" ]]; then
        printf 'unsafe prefix reached download step: %s\n' "$name" >&2
        exit 1
    fi
}

safe="$root/safe/prefix"
canonical_safe=$(tmux_compat_canonical_temp_prefix "$safe")
[[ "$canonical_safe" == "$safe" ]]

mkdir -p "$root/physical"
ln -s "$root/physical" "$root/inside-link"
canonical_inside=$(tmux_compat_canonical_temp_prefix "$root/inside-link/prefix")
[[ "$canonical_inside" == "$root/physical/prefix" ]]

expect_rejected_before_download traversal "/tmp/../usr/local/sidecar-tmux-prefix-test"
ln -s /usr "$root/outside-link"
expect_rejected_before_download symlink-escape "$root/outside-link/local/sidecar-tmux-prefix-test"

printf 'tmux compatibility prefix validation passed\n'
