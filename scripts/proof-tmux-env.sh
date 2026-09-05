#!/usr/bin/env bash
# Source, then call proof_tmux_env /absolute/private/run/tmux.
# This guards tmux only; Sidecar proofs also need isolated state and -config.
proof_tmux_env() {
  local requested="${1:-}" canonical system_tmp user_tmp uid
  case "$requested" in
    /*) ;;
    *) echo "proof-tmux-env: an absolute private directory is required" >&2; return 1 ;;
  esac
  case "/$requested/" in
    */../*|*/./*) echo "proof-tmux-env: dot components are refused" >&2; return 1 ;;
  esac
  system_tmp="$(cd /tmp && pwd -P)" || return 1
  user_tmp="$(cd "${TMPDIR:-/tmp}" && pwd -P)" || return 1
  uid="$(id -u)" || return 1
  # Require a dedicated subtree, never /tmp or its normal tmux directory.
  case "$requested" in
    /tmp/*/*|"$system_tmp"/*/*|"${TMPDIR:-/tmp}"/*/*|"$user_tmp"/*/*) ;;
    *) echo "proof-tmux-env: use a dedicated run directory under /tmp" >&2; return 1 ;;
  esac
  case "$requested" in
    /tmp/tmux-"$uid"/*|"$system_tmp"/tmux-"$uid"/*|"${TMPDIR:-/tmp}"/tmux-"$uid"/*|"$user_tmp"/tmux-"$uid"/*)
      echo "proof-tmux-env: the default tmux directory is refused" >&2; return 1 ;;
  esac
  mkdir -p "$requested" || return 1
  canonical="$(cd "$requested" && pwd -P)" || return 1
  case "$canonical" in
    "$system_tmp"/tmux-"$uid"|"$system_tmp"/tmux-"$uid"/*|"$user_tmp"/tmux-"$uid"|"$user_tmp"/tmux-"$uid"/*)
      echo "proof-tmux-env: the default tmux directory is refused" >&2; return 1 ;;
    "$system_tmp"/*/*|"$user_tmp"/*/*) ;;
    *) echo "proof-tmux-env: directory escaped the private temporary tree" >&2; return 1 ;;
  esac
  chmod 700 "$canonical" || return 1
  unset TMUX TMUX_PANE SIDECAR_TMUX_SERVER
  export TMUX_TMPDIR="$requested"
  mkdir -p "$TMUX_TMPDIR/tmux-$uid" || return 1
  if [[ "$(cd "$TMUX_TMPDIR/tmux-$uid" && pwd -P)" != "$canonical/tmux-$uid" ]]; then
    echo "proof-tmux-env: socket directory escaped the private tree" >&2
    return 1
  fi
  chmod 700 "$TMUX_TMPDIR/tmux-$uid" || return 1
  export PROOF_TMUX_SOCKET="$TMUX_TMPDIR/tmux-$uid/default"
}

# Explicit -S remains safe even if an attached pane later exports TMUX again.
proof_tmux() {
  if [[ -z "${PROOF_TMUX_SOCKET:-}" ]]; then
    echo "proof-tmux-env: call proof_tmux_env first" >&2
    return 1
  fi
  command tmux -S "$PROOF_TMUX_SOCKET" "$@"
}
