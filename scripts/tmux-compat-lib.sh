#!/usr/bin/env bash

# Shared manifest access for the tmux compatibility build and test scripts.
# This file is sourced; callers choose whether an error should terminate their
# process. Keep it free of commands that contact or start a tmux server.

TMUX_COMPAT_SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TMUX_COMPAT_REPO_ROOT=$(cd "$TMUX_COMPAT_SCRIPT_DIR/.." && pwd)
TMUX_COMPAT_MANIFEST=${SIDECAR_TMUX_COMPAT_MANIFEST:-$TMUX_COMPAT_REPO_ROOT/compat/tmux-versions.tsv}

tmux_compat_error() {
    printf 'tmux compatibility: %s\n' "$*" >&2
}

tmux_compat_validate_manifest() {
    local manifest=${1:-$TMUX_COMPAT_MANIFEST}
    local role release checksum extra
    local roles_seen=' ' releases_seen=' '
    local minimum_count=0 latest_count=0 line_number=0

    if [[ ! -r "$manifest" ]]; then
        tmux_compat_error "manifest is not readable: $manifest"
        return 1
    fi

    while read -r role release checksum extra || [[ -n "${role:-}" ]]; do
        line_number=$((line_number + 1))
        case "${role:-}" in
            ''|'#'*) continue ;;
        esac
        if [[ -n "${extra:-}" || -z "${release:-}" || -z "${checksum:-}" ]]; then
            tmux_compat_error "$manifest:$line_number must contain exactly role, version, and SHA-256"
            return 1
        fi
        case "$role" in
            minimum) minimum_count=$((minimum_count + 1)) ;;
            latest) latest_count=$((latest_count + 1)) ;;
            *)
                tmux_compat_error "$manifest:$line_number has unsupported role $role"
                return 1
                ;;
        esac
        if [[ ! "$release" =~ ^[0-9]+\.[0-9]+[a-z]?$ ]]; then
            tmux_compat_error "$manifest:$line_number has malformed version $release"
            return 1
        fi
        if [[ ! "$checksum" =~ ^[0-9a-f]{64}$ ]]; then
            tmux_compat_error "$manifest:$line_number has malformed SHA-256 for $role"
            return 1
        fi
        if [[ "$roles_seen" == *" $role "* ]]; then
            tmux_compat_error "$manifest:$line_number duplicates role $role"
            return 1
        fi
        if [[ "$releases_seen" == *" $release "* ]]; then
            tmux_compat_error "$manifest:$line_number duplicates version $release"
            return 1
        fi
        roles_seen+="$role "
        releases_seen+="$release "
    done <"$manifest"

    if [[ $minimum_count -ne 1 || $latest_count -ne 1 ]]; then
        tmux_compat_error "$manifest must define minimum and latest exactly once"
        return 1
    fi
}

tmux_compat_row() {
    local wanted=${1:-}
    local manifest=${2:-$TMUX_COMPAT_MANIFEST}
    local role release checksum extra

    tmux_compat_validate_manifest "$manifest" || return 1
    case "$wanted" in
        minimum|latest) ;;
        *)
            tmux_compat_error "unsupported role ${wanted:-<empty>}; expected minimum or latest"
            return 1
            ;;
    esac
    while read -r role release checksum extra || [[ -n "${role:-}" ]]; do
        if [[ "$role" == "$wanted" ]]; then
            printf '%s\t%s\t%s\n' "$role" "$release" "$checksum"
            return 0
        fi
    done <"$manifest"
    tmux_compat_error "role $wanted is missing from $manifest"
    return 1
}

tmux_compat_archive_url() {
    local release=$1
    printf 'https://github.com/tmux/tmux/releases/download/%s/tmux-%s.tar.gz\n' "$release" "$release"
}

# tmux_compat_canonical_temp_prefix validates a caller-selected install prefix
# before the build helper creates any directory or downloads any source. It
# prints the canonical destination on success.
#
# The destination itself need not exist. Resolve its nearest existing ancestor
# physically, then append only plain missing path components. Rejecting dot
# components keeps the result unambiguous and prevents lexical /tmp/../ escapes.
tmux_compat_canonical_temp_prefix() {
    local requested=${1:-}
    local candidate parent component suffix=''
    local canonical_prefix temporary_root canonical_root
    local prefix_is_temporary=0

    case "$requested" in
        /*) ;;
        *) tmux_compat_error "prefix must be an absolute path"; return 1 ;;
    esac
    case "/$requested/" in
        */../*|*/./*)
            tmux_compat_error "prefix must not contain . or .. path components: $requested"
            return 1
            ;;
    esac

    candidate=$requested
    while [[ "$candidate" != / && "$candidate" == */ ]]; do
        candidate=${candidate%/}
    done
    while [[ ! -e "$candidate" ]]; do
        component=${candidate##*/}
        if [[ -z "$component" ]]; then
            tmux_compat_error "could not resolve prefix: $requested"
            return 1
        fi
        suffix="/$component$suffix"
        parent=${candidate%/*}
        [[ -n "$parent" ]] || parent=/
        candidate=$parent
    done
    if [[ ! -d "$candidate" ]]; then
        tmux_compat_error "prefix ancestor is not a directory: $candidate"
        return 1
    fi
    canonical_prefix=$(cd -P -- "$candidate" 2>/dev/null && pwd) || {
        tmux_compat_error "could not resolve prefix ancestor: $candidate"
        return 1
    }
    canonical_prefix+=$suffix

    for temporary_root in /tmp /private/tmp "${TMPDIR:-}" "${RUNNER_TEMP:-}"; do
        [[ -n "$temporary_root" && "$temporary_root" != / && -d "$temporary_root" ]] || continue
        canonical_root=$(cd -P -- "$temporary_root" 2>/dev/null && pwd) || continue
        case "$canonical_prefix" in
            "$canonical_root"/*)
                prefix_is_temporary=1
                break
                ;;
        esac
    done
    if [[ $prefix_is_temporary -ne 1 ]]; then
        tmux_compat_error "resolved prefix must remain below /tmp, /private/tmp, TMPDIR, or RUNNER_TEMP: $canonical_prefix"
        return 1
    fi
    printf '%s\n' "$canonical_prefix"
}
