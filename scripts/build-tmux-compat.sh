#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=tmux-compat-lib.sh
source "$SCRIPT_DIR/tmux-compat-lib.sh"

usage() {
    cat <<'EOF'
Usage: ./scripts/build-tmux-compat.sh minimum|latest ABSOLUTE_PREFIX

Builds the selected official tmux release into the caller-provided prefix.
The helper never installs globally and never contacts a tmux server.
EOF
}

if [[ $# -ne 2 ]]; then
    usage >&2
    exit 2
fi

role=$1
prefix=$(tmux_compat_canonical_temp_prefix "$2") || exit 2

IFS=$'\t' read -r _ release checksum < <(tmux_compat_row "$role")
archive_url=$(tmux_compat_archive_url "$release")
build_root=$(mktemp -d "${TMPDIR:-/tmp}/sidecar-tmux-$release.XXXXXX")
archive="$build_root/tmux-$release.tar.gz"
source_dir="$build_root/tmux-$release"

cleanup() {
    case "$build_root" in
        "${TMPDIR:-/tmp}"/sidecar-tmux-*) rm -rf -- "$build_root" ;;
    esac
}
trap cleanup EXIT INT TERM

for command_name in curl tar make; do
    if ! command -v "$command_name" >/dev/null 2>&1; then
        tmux_compat_error "$command_name is required to build tmux"
        exit 1
    fi
done

mkdir -p "$prefix"
printf 'Downloading tmux %s from %s\n' "$release" "$archive_url"
curl -fL --retry 3 --output "$archive" "$archive_url"

if command -v sha256sum >/dev/null 2>&1; then
    actual_checksum=$(sha256sum "$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
    actual_checksum=$(shasum -a 256 "$archive" | awk '{print $1}')
else
    tmux_compat_error "sha256sum or shasum is required to verify tmux"
    exit 1
fi
if [[ "$actual_checksum" != "$checksum" ]]; then
    tmux_compat_error "tmux $release archive checksum is $actual_checksum, expected $checksum"
    exit 1
fi

tar -xzf "$archive" -C "$build_root"
jobs=$(getconf _NPROCESSORS_ONLN 2>/dev/null || printf '2')
configure_env=()
if [[ $(uname -s) == Darwin ]] && command -v brew >/dev/null 2>&1; then
    # Homebrew does not make keg-only ncurses discoverable, and a developer
    # should not need to install pkg-config globally just for this proof. Feed
    # configure the same library prefixes directly on macOS. Linux CI uses
    # pkg-config and the distro development packages in the ordinary way.
    libevent_prefix=$(brew --prefix libevent)
    ncurses_prefix=$(brew --prefix ncurses)
    utf8proc_prefix=$(brew --prefix utf8proc)
    configure_env=(
        "CPPFLAGS=-I$libevent_prefix/include -I$ncurses_prefix/include ${CPPFLAGS:-}"
        "LDFLAGS=-L$libevent_prefix/lib -L$ncurses_prefix/lib ${LDFLAGS:-}"
        "LIBUTF8PROC_CFLAGS=-I$utf8proc_prefix/include"
        "LIBUTF8PROC_LIBS=-L$utf8proc_prefix/lib -lutf8proc"
    )
fi
(
    cd "$source_dir"
    # tmux 3.4 requires an explicit utf8proc decision. Sidecar renders Unicode
    # terminal content, so exercise the utf8proc-enabled build on both ends of
    # the matrix instead of accepting the reduced built-in table.
    env "${configure_env[@]}" ./configure --prefix="$prefix" --enable-utf8proc --disable-jemalloc
    make -j"$jobs"
    make install
)

actual_version=$($prefix/bin/tmux -V)
expected_version="tmux $release"
if [[ "$actual_version" != "$expected_version" ]]; then
    tmux_compat_error "$prefix/bin/tmux reports $actual_version, expected $expected_version"
    exit 1
fi
printf 'Built %s at %s/bin/tmux\n' "$actual_version" "$prefix"
