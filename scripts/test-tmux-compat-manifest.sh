#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=tmux-compat-lib.sh
source "$SCRIPT_DIR/tmux-compat-lib.sh"

root=$(mktemp -d "${TMPDIR:-/tmp}/sidecar-tmux-manifest-test.XXXXXX")
cleanup() {
    case "$root" in
        "${TMPDIR:-/tmp}"/sidecar-tmux-manifest-test.*) rm -rf -- "$root" ;;
    esac
}
trap cleanup EXIT INT TERM

expect_invalid() {
    local name=$1
    local contents=$2
    local fixture="$root/$name.tsv"
    printf '%s\n' "$contents" >"$fixture"
    if tmux_compat_validate_manifest "$fixture" >/dev/null 2>&1; then
        printf 'expected invalid manifest to fail: %s\n' "$name" >&2
        exit 1
    fi
}

tmux_compat_validate_manifest
valid_minimum=$(tmux_compat_row minimum)
valid_latest=$(tmux_compat_row latest)
IFS=$'\t' read -r minimum_role minimum_release minimum_checksum <<<"$valid_minimum"
IFS=$'\t' read -r latest_role latest_release latest_checksum <<<"$valid_latest"
[[ "$minimum_role" == minimum && "$latest_role" == latest ]]
[[ "$minimum_release" != "$latest_release" ]]

expect_invalid missing-role "$valid_minimum"
expect_invalid duplicate-role "$valid_minimum"$'\n'"$valid_minimum"$'\n'"$valid_latest"
expect_invalid duplicate-release "$valid_minimum"$'\n'"latest"$'\t'"$minimum_release"$'\t'"$latest_checksum"
expect_invalid malformed-checksum "$valid_minimum"$'\n'"latest"$'\t'"$latest_release"$'\tdeadbeef'
expect_invalid unsupported-role "$valid_minimum"$'\n'"$valid_latest"$'\n'"future"$'\t999.0\t'"$minimum_checksum"

if tmux_compat_row future >/dev/null 2>&1; then
    printf 'unsupported role future unexpectedly resolved\n' >&2
    exit 1
fi
printf 'tmux compatibility manifest validation passed\n'
