#!/bin/sh
set -eu

action=${1:-status}
if [ -n "${SIDECAR_REPO_ROOT:-}" ]; then
  repo_root=$(CDPATH= cd -- "$SIDECAR_REPO_ROOT" && pwd -P)
else
  repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
fi
state_root=${SIDECAR_DEV_STATE:-"$HOME/.local/state/sidecar/dev-installs"}
go_command=${SIDECAR_GO:-go}
zsh_command=${SIDECAR_ZSH:-/bin/zsh}

die() {
  printf 'sidecar dev install: %s\n' "$*" >&2
  exit 1
}

require_brew() {
  command -v brew >/dev/null 2>&1 ||
    die "Homebrew is required for managed installs; use 'make install' for an unmanaged Go install"
}

brew_prefix() {
  require_brew
  if [ -n "${SIDECAR_BREW_PREFIX:-}" ]; then
    printf '%s\n' "$SIDECAR_BREW_PREFIX"
  else
    brew --prefix
  fi
}

active_bin_dir() {
  printf '%s/bin\n' "$(brew_prefix)"
}

resolved_path() {
  realpath -q "$1" 2>/dev/null
}

path_is_below() {
  candidate=$1
  root=$2
  [ "$candidate" = "$root" ] || case "$candidate" in
    "$root"/*) return 0 ;;
    *) return 1 ;;
  esac
}

formula_root() {
  brew --prefix sidecar 2>/dev/null | while IFS= read -r prefix; do
    resolved_path "$prefix" || printf '%s\n' "$prefix"
  done
}

link_kind() {
  path=$1
  if [ ! -e "$path" ] && [ ! -L "$path" ]; then
    printf 'missing\n'
    return
  fi
  if [ ! -L "$path" ]; then
    printf 'other\n'
    return
  fi
  target=$(resolved_path "$path" || true)
  [ -n "$target" ] || {
    printf 'other\n'
    return
  }
  resolved_state=$(resolved_path "$state_root" || true)
  if [ -n "$resolved_state" ] && path_is_below "$target" "$resolved_state"; then
    printf 'local\n'
    return
  fi
  resolved_formula=$(formula_root || true)
  if [ -n "$resolved_formula" ] && path_is_below "$target" "$resolved_formula"; then
    printf 'homebrew\n'
    return
  fi
  printf 'other\n'
}

describe_link() {
  link_path=$1
  kind=$(link_kind "$link_path")
  raw=$(readlink "$link_path" 2>/dev/null || printf 'regular file')
  resolved=$(resolved_path "$link_path" || printf 'unresolved')
  printf 'link state: %s\n' "$kind"
  printf 'activation path: %s\n' "$link_path"
  printf 'raw target: %s\n' "$raw"
  printf 'resolved target: %s\n' "$resolved"
  if [ "$kind" = local ]; then
    metadata=$(dirname "$resolved")/metadata
    if [ -r "$metadata" ]; then
      printf 'artifact metadata:\n'
      sed 's/^/  /' "$metadata"
    fi
  fi
  if [ -x "$link_path" ]; then
    printf 'activation version: '
    "$link_path" --version 2>&1 || true
  fi
}

# Login zshrc may print to stdout. Parsers look for this sentinel, not the first line.
# Version the resolved file, not `command sidecar`, so a hashed/shadowed binary
# is the one we inspect and so a SIGKILL'd GOBIN copy cannot hide behind PATH.
path_probe_script='printf "SIDECAR_DEV_INSTALL_PATH=%s\n" "$(command -v sidecar 2>/dev/null || true)"'
probe_script="$path_probe_script"'; p=$(command -v sidecar 2>/dev/null || true); [ -n "$p" ] && [ -x "$p" ] && "$p" --version'

probe_path_from() {
  printf '%s\n' "$1" | sed -n 's/^SIDECAR_DEV_INSTALL_PATH=//p' | tail -n 1
}

probe_version_from() {
  printf '%s\n' "$1" | awk '/^sidecar version / { v = $0 } END { print v }'
}

print_probe_report() {
  label=$1
  probe_output=$2
  found_path=$(probe_path_from "$probe_output")
  found_version=$(probe_version_from "$probe_output")
  printf '%s resolves:\n' "$label"
  if [ -n "$found_path" ]; then
    printf '  %s\n' "$found_path"
  else
    printf '  not found\n'
  fi
  if [ -n "$found_version" ]; then
    printf '  %s\n' "$found_version"
  fi
}

current_shell_probe() {
  sh -c "$probe_script" 2>/dev/null || true
}

login_shell_probe_output() {
  option=$1
  "$zsh_command" "$option" "$probe_script" 2>/dev/null || true
}

current_shell_sidecar_path() {
  probe_path_from "$(sh -c "$path_probe_script" 2>/dev/null || true)"
}

login_shell_sidecar_path() {
  option=$1
  probe_path_from "$("$zsh_command" "$option" "$path_probe_script" 2>/dev/null || true)"
}

status() {
  bin_dir=$(active_bin_dir)
  printf 'managed command directory: %s\n' "$bin_dir"
  describe_link "$bin_dir/sidecar"
  print_probe_report 'current shell' "$(current_shell_probe)"
  if [ -x "$zsh_command" ]; then
    print_probe_report 'interactive login shell' "$(login_shell_probe_output -lic)"
    print_probe_report 'non-interactive login shell' "$(login_shell_probe_output -lc)"
  else
    printf 'interactive login shell resolves:\n  unavailable: %s\n' "$zsh_command"
    printf 'non-interactive login shell resolves:\n  unavailable: %s\n' "$zsh_command"
  fi
}

# A PATH winner we can safely point at the activated artifact: a sidecar
# file or symlink whose parent is writable, not the Homebrew-prefix link
# and not a Cellar or system binary.
can_retarget_launcher() {
  launcher=$1
  brew_link=$(active_bin_dir)/sidecar
  [ -n "$launcher" ] || return 1
  [ "$launcher" != "$brew_link" ] || return 1
  case "$launcher" in
    */sidecar) ;;
    *) return 1 ;;
  esac
  case "$launcher" in
    /bin/* | /sbin/* | /usr/bin/* | /usr/sbin/* | /usr/libexec/*) return 1 ;;
  esac
  [ -f "$launcher" ] || [ -L "$launcher" ] || return 1
  dir=$(dirname "$launcher")
  [ -w "$dir" ] || return 1
  resolved_formula=$(formula_root || true)
  resolved_launcher=$(resolved_path "$launcher" || true)
  if [ -n "$resolved_formula" ] && [ -n "$resolved_launcher" ] &&
    path_is_below "$resolved_launcher" "$resolved_formula"; then
    return 1
  fi
  return 0
}

retarget_launcher() {
  launcher=$1
  artifact=$2
  staged=$(dirname "$launcher")/.sidecar-path-$$
  ln -s "$artifact" "$staged"
  if ! mv "$staged" "$launcher"; then
    rm -f "$staged"
    return 1
  fi
  printf 'pointed %s at the activated build\n' "$launcher"
}

# `make install-worktree && sidecar` must run this build. Homebrew is the
# managed link; copies that win PATH (usually ~/go/bin from `make install`)
# are retargeted to the same artifact.
sync_launch_paths() {
  activation=$1
  artifact=$(resolved_path "$activation" || true)
  [ -n "$artifact" ] || die "activated sidecar at $activation could not be resolved"
  launchers=
  add_launcher() {
    candidate=$1
    [ -n "$candidate" ] || return 0
    case "$launchers" in
      *"|$candidate|"*) return 0 ;;
    esac
    launchers=$launchers"|$candidate|"
  }
  add_launcher "$(current_shell_sidecar_path)"
  if [ -x "$zsh_command" ]; then
    add_launcher "$(login_shell_sidecar_path -lic)"
    add_launcher "$(login_shell_sidecar_path -lc)"
  fi

  leftover=
  old_ifs=$IFS
  IFS='|'
  # shellcheck disable=SC2086
  set -- $launchers
  IFS=$old_ifs
  for launcher in "$@"; do
    [ -n "$launcher" ] || continue
    got=$(resolved_path "$launcher" || true)
    if [ -n "$got" ] && [ "$got" = "$artifact" ]; then
      continue
    fi
    if can_retarget_launcher "$launcher" && retarget_launcher "$launcher" "$artifact"; then
      continue
    fi
    leftover=$leftover$(printf '\n  %s' "$launcher")
  done
  if [ -n "$leftover" ]; then
    die "could not point PATH's sidecar at the activated build:$leftover"
  fi
}

verify_activated_sidecar() {
  activation=$1
  want=$(resolved_path "$activation" || true)
  want_version=$("$activation" --version 2>/dev/null || true)
  failed=0
  report=
  append_mismatch() {
    label=$1
    probe_output=$2
    found_path=$(probe_path_from "$probe_output")
    got=$(resolved_path "$found_path" 2>/dev/null || true)
    found_version=$(probe_version_from "$probe_output")
    [ -n "$got" ] || got='not found'
    [ -n "$found_version" ] || found_version='no --version'
    if [ -n "$want" ] && [ "$got" = "$want" ]; then
      return 0
    fi
    failed=1
    report=$report$(printf '\n  %s:\n    %s\n    %s' "$label" "$got" "$found_version")
  }

  append_mismatch 'current shell' "$(current_shell_probe)"
  if [ -x "$zsh_command" ]; then
    append_mismatch 'interactive login shell' "$(login_shell_probe_output -lic)"
    append_mismatch 'non-interactive login shell' "$(login_shell_probe_output -lc)"
  fi

  if [ "$failed" -ne 0 ]; then
    printf 'sidecar dev install: `sidecar` does not run the build just activated\n' >&2
    printf '  activated:\n    %s\n    %s\n' "${want:-unresolved}" "${want_version:-no --version}" >&2
    printf '%s\n' "$report" >&2
    printf '  The Homebrew-prefix link is in place, but another sidecar still wins PATH.\n' >&2
    exit 1
  fi
  printf 'verified: sidecar on PATH is this build (%s)\n' "${want_version:-ok}"
}

restore_previous() {
  bin_dir=$1
  previous_kind=$2
  previous_target=$3
  path=$bin_dir/sidecar
  rm -f "$path"
  case "$previous_kind" in
    local)
      ln -s "$previous_target" "$path" || return 1
      ;;
    homebrew)
      brew link sidecar >/dev/null || return 1
      ;;
    missing) ;;
    *) return 1 ;;
  esac
}

rollback_on_signal() {
  trap - EXIT HUP INT TERM
  [ -z "${rollback_staged:-}" ] || rm -f "$rollback_staged"
  if ! restore_previous "$rollback_bin_dir" "$rollback_previous_kind" \
    "$rollback_previous_target"; then
    printf 'sidecar dev install: interrupted; previous installation could not be restored\n' >&2
  fi
  exit 1
}

install_local() {
  mode=$1
  git -C "$repo_root" rev-parse --git-dir >/dev/null 2>&1 ||
    die "$repo_root is not a git checkout"
  branch=$(git -C "$repo_root" branch --show-current)
  [ -n "$branch" ] || branch=detached
  common_dir=$(git -C "$repo_root" rev-parse --path-format=absolute --git-common-dir)
  canonical=false
  canonical_git=$(resolved_path "$repo_root/.git" || true)
  resolved_common=$(resolved_path "$common_dir" || true)
  [ -n "$canonical_git" ] && [ "$resolved_common" = "$canonical_git" ] && canonical=true
  if [ "$mode" = main ] && { [ "$canonical" != true ] || [ "$branch" != main ]; }; then
    die "install-local requires the canonical main checkout; use 'make install-worktree' to activate this checkout deliberately"
  fi

  require_brew
  commit=$(git -C "$repo_root" rev-parse HEAD)
  short_commit=$(git -C "$repo_root" rev-parse --short HEAD)
  dirty=false
  [ -z "$(git -C "$repo_root" status --porcelain --untracked-files=normal)" ] || dirty=true
  safe_branch=$(printf '%s' "$branch" | tr -cs 'A-Za-z0-9._-' '-')
  checkout_id=$(printf '%s' "$repo_root" | shasum -a 256 | cut -c1-12)
  dirty_suffix=
  [ "$dirty" = false ] || dirty_suffix=+dirty
  version=devel+$safe_branch.$short_commit$dirty_suffix
  build_id=$(date -u '+%Y%m%dT%H%M%SZ')-$$
  destination=$state_root/$safe_branch-$checkout_id-$short_commit-$build_id
  temporary=$state_root/.build-$checkout_id-$$

  mkdir -p "$state_root"
  rm -rf "$temporary"
  mkdir "$temporary"
  trap 'rm -rf "$temporary"' EXIT HUP INT TERM
  (
    cd "$repo_root"
    GOWORK=off "$go_command" build -ldflags "-s -w -X main.Version=$version" \
      -o "$temporary/sidecar" ./cmd/sidecar
  )
  {
    printf 'source=%s\n' "$repo_root"
    printf 'revision=%s\n' "$commit"
    printf 'branch=%s\n' "$branch"
    printf 'dirty=%s\n' "$dirty"
    printf 'built_at=%s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf 'version=%s\n' "$version"
  } >"$temporary/metadata"
  mv "$temporary" "$destination"
  trap - EXIT HUP INT TERM

  bin_dir=$(active_bin_dir)
  mkdir -p "$bin_dir"
  path=$bin_dir/sidecar
  previous_kind=$(link_kind "$path")
  [ "$previous_kind" != other ] ||
    die "$path is not managed by this repository or Homebrew; refusing to replace it"
  previous_target=$(readlink "$path" 2>/dev/null || true)
  staged=$bin_dir/.sidecar-dev-$$
  ln -s "$destination/sidecar" "$staged"
  rollback_bin_dir=$bin_dir
  rollback_previous_kind=$previous_kind
  rollback_previous_target=$previous_target
  rollback_staged=$staged
  trap rollback_on_signal HUP INT TERM
  trap 'rm -f "$rollback_staged"' EXIT

  if [ "$previous_kind" = homebrew ]; then
    if ! brew unlink sidecar >/dev/null; then
      rm -f "$staged"
      trap - EXIT HUP INT TERM
      die "Homebrew unlink failed; previous installation was left active"
    fi
  fi
  if ! mv "$staged" "$path" || [ ! -x "$path" ]; then
    rm -f "$staged"
    restore_previous "$bin_dir" "$previous_kind" "$previous_target" ||
      die "activation failed and the previous installation could not be restored"
    trap - EXIT HUP INT TERM
    die "activation failed; restored the previous installation"
  fi
  trap - EXIT HUP INT TERM
  printf 'activated local Sidecar build from %s\n' "$repo_root"
  sync_launch_paths "$bin_dir/sidecar"
  status
  verify_activated_sidecar "$bin_dir/sidecar"
}

use_homebrew() {
  require_brew
  brew list --versions sidecar >/dev/null 2>&1 ||
    die "the sidecar formula is not installed; run 'brew install marcus/tap/sidecar'"
  bin_dir=$(active_bin_dir)
  mkdir -p "$bin_dir"
  path=$bin_dir/sidecar
  previous_kind=$(link_kind "$path")
  case "$previous_kind" in
    homebrew)
      printf 'Homebrew Sidecar is already active\n'
      sync_launch_paths "$bin_dir/sidecar"
      status
      verify_activated_sidecar "$bin_dir/sidecar"
      return
      ;;
    local|missing) ;;
    other) die "$path is not managed by this repository or Homebrew; refusing to replace it" ;;
  esac
  previous_target=$(readlink "$path" 2>/dev/null || true)
  rollback_bin_dir=$bin_dir
  rollback_previous_kind=$previous_kind
  rollback_previous_target=$previous_target
  rollback_staged=
  trap rollback_on_signal HUP INT TERM
  [ "$previous_kind" != local ] || rm -f "$path"
  if ! brew link sidecar >/dev/null || [ "$(link_kind "$path")" != homebrew ] || [ ! -x "$path" ]; then
    trap - HUP INT TERM
    brew unlink sidecar >/dev/null 2>&1 || true
    restore_previous "$bin_dir" "$previous_kind" "$previous_target" ||
      die "Homebrew relinking failed and the previous installation could not be restored"
    die "Homebrew relinking failed; restored the previous installation"
  fi
  trap - HUP INT TERM
  printf 'activated Homebrew Sidecar\n'
  sync_launch_paths "$bin_dir/sidecar"
  status
  verify_activated_sidecar "$bin_dir/sidecar"
}

case "$action" in
  install-local) install_local main ;;
  install-worktree) install_local worktree ;;
  use-homebrew) use_homebrew ;;
  status) status ;;
  *) die "usage: $0 {install-local|install-worktree|use-homebrew|status}" ;;
esac
