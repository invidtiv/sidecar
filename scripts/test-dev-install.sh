#!/bin/sh
set -eu

repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd -P)
temporary=$(mktemp -d)
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT HUP INT TERM

fail() {
  printf 'dev-install test: %s\n' "$*" >&2
  exit 1
}

assert_contains() {
  haystack=$1
  needle=$2
  case "$haystack" in
    *"$needle"*) ;;
    *) fail "expected output to contain: $needle" ;;
  esac
}

assert_kind() {
  expected=$1
  output=$(run status)
  assert_contains "$output" "link state: $expected"
}

test_repo=$temporary/repo
fake_bin=$temporary/fake-bin
brew_prefix=$temporary/homebrew
brew_state=$temporary/brew-state
dev_state=$temporary/dev-state
mkdir -p "$test_repo/cmd/sidecar" "$fake_bin" \
  "$brew_prefix/bin" "$brew_prefix/Cellar/sidecar/1.0.0/bin" "$brew_state"
git init --quiet --initial-branch=main "$test_repo"
printf 'module example.invalid/sidecar\n\ngo 1.25\n' >"$test_repo/go.mod"
printf 'package main\nfunc main() {}\n' >"$test_repo/cmd/sidecar/main.go"
git -C "$test_repo" add .
git -C "$test_repo" -c user.name=test -c user.email=test@example.invalid \
  commit --quiet -m initial

cat >"$fake_bin/go" <<'EOF'
#!/bin/sh
set -eu
[ "${GOWORK:-}" = off ] || exit 3
[ ! -e "$FAKE_BREW_STATE/go-fail" ] || exit 1
output=
ldflags=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output=$2; shift 2 ;;
    -ldflags) ldflags=$2; shift 2 ;;
    *) shift ;;
  esac
done
[ -n "$output" ] || exit 2
version=${ldflags##*main.Version=}
cat >"$output" <<SCRIPT
#!/bin/sh
printf 'sidecar version %s\\n' '$version'
SCRIPT
chmod +x "$output"
EOF
chmod +x "$fake_bin/go"

cat >"$fake_bin/brew" <<'EOF'
#!/bin/sh
set -eu
printf '%s\n' "$*" >>"$FAKE_BREW_STATE/calls"
case "${1:-}" in
  --prefix)
    if [ "${2:-}" = sidecar ]; then
      printf '%s\n' "$FAKE_BREW_PREFIX/Cellar/sidecar/1.0.0"
    else
      printf '%s\n' "$FAKE_BREW_PREFIX"
    fi
    ;;
  list)
    [ ! -e "$FAKE_BREW_STATE/formula-missing" ]
    printf 'sidecar 1.0.0\n'
    ;;
  unlink)
    [ ! -e "$FAKE_BREW_STATE/unlink-fail" ] || exit 1
    rm -f "$FAKE_BREW_PREFIX/bin/sidecar"
    if [ -e "$FAKE_BREW_STATE/interrupt-after-unlink" ]; then
      kill -TERM "$PPID"
    fi
    ;;
  link)
    [ ! -e "$FAKE_BREW_STATE/link-fail" ] || exit 1
    if [ -e "$FAKE_BREW_STATE/interrupt-during-link" ]; then
      kill -TERM "$PPID"
      exit 1
    fi
    if [ -e "$FAKE_BREW_STATE/link-invalid" ]; then
      ln -s "$FAKE_BREW_PREFIX/missing-sidecar" "$FAKE_BREW_PREFIX/bin/sidecar"
    else
      ln -s ../Cellar/sidecar/1.0.0/bin/sidecar "$FAKE_BREW_PREFIX/bin/sidecar"
    fi
    ;;
  *) exit 2 ;;
esac
EOF
chmod +x "$fake_bin/brew"

cat >"$fake_bin/sidecar" <<'EOF'
#!/bin/sh
printf 'sidecar version current-shell\n'
EOF
chmod +x "$fake_bin/sidecar"

cat >"$fake_bin/zsh" <<EOF
#!/bin/sh
set -eu
case "\${1:-}" in
  -lic)
    path=$brew_prefix/bin/sidecar
    [ -z "\${FAKE_LOGIN_SIDECAR:-}" ] || path=\$FAKE_LOGIN_SIDECAR
    ;;
  -lc)
    path=$brew_prefix/bin/sidecar
    [ -z "\${FAKE_NLOGIN_SIDECAR:-}" ] || path=\$FAKE_NLOGIN_SIDECAR
    ;;
  *) exit 2 ;;
esac
printf 'SIDECAR_DEV_INSTALL_PATH=%s\\n' "\$path"
if [ -x "\$path" ]; then
  "\$path" --version
fi
EOF
chmod +x "$fake_bin/zsh"

cat >"$brew_prefix/Cellar/sidecar/1.0.0/bin/sidecar" <<'EOF'
#!/bin/sh
printf 'sidecar version v1.0.0\n'
EOF
chmod +x "$brew_prefix/Cellar/sidecar/1.0.0/bin/sidecar"

run() {
  active_repo=${SIDECAR_TEST_REPO:-$test_repo}
  path="$brew_prefix/bin:$fake_bin:$PATH"
  if [ -n "${SIDECAR_TEST_PATH_PREFIX:-}" ]; then
    path="$SIDECAR_TEST_PATH_PREFIX:$path"
  fi
  env PATH="$path" \
    FAKE_BREW_PREFIX="$brew_prefix" \
    FAKE_BREW_STATE="$brew_state" \
    FAKE_LOGIN_SIDECAR="${FAKE_LOGIN_SIDECAR:-}" \
    FAKE_NLOGIN_SIDECAR="${FAKE_NLOGIN_SIDECAR:-}" \
    SIDECAR_REPO_ROOT="$active_repo" \
    SIDECAR_DEV_STATE="$dev_state" \
    SIDECAR_BREW_PREFIX="$brew_prefix" \
    SIDECAR_GO="$fake_bin/go" \
    SIDECAR_ZSH="$fake_bin/zsh" \
    "$repo_root/scripts/dev-install.sh" "$@"
}

# Missing -> canonical main install, with inspectable clean metadata.
output=$(run install-local)
assert_contains "$output" 'activated local Sidecar build'
assert_contains "$output" 'verified: sidecar on PATH is this build'
assert_contains "$output" 'branch=main'
assert_contains "$output" 'dirty=false'
assert_kind local
first_target=$(readlink "$brew_prefix/bin/sidecar")
[ -x "$first_target" ] || fail 'managed target is not executable'

# Reinstall creates a distinct completed artifact and changes the active link.
run install-local >/dev/null
second_target=$(readlink "$brew_prefix/bin/sidecar")
[ "$first_target" != "$second_target" ] || fail 'repeat install reused an artifact'
[ -x "$first_target" ] || fail 'repeat install deleted the previous artifact'

# Local replacement is one rename: mv must replace the existing symlink without
# an explicit removal, so the active command remains present to consumers.
[ -L "$brew_prefix/bin/sidecar" ] || fail 'repeat install lost the active link'

# Dirty state is reflected in metadata and the development version.
touch "$test_repo/untracked"
output=$(run install-local)
assert_contains "$output" 'dirty=true'
assert_contains "$output" '+dirty'
rm "$test_repo/untracked"

# Build failure leaves the current managed link untouched.
before=$(readlink "$brew_prefix/bin/sidecar")
touch "$brew_state/go-fail"
if run install-local >/dev/null 2>&1; then
  fail 'build failure unexpectedly succeeded'
fi
rm "$brew_state/go-fail"
[ "$(readlink "$brew_prefix/bin/sidecar")" = "$before" ] ||
  fail 'build failure changed the active link'

# A non-main branch and linked worktree require explicit worktree activation.
git -C "$test_repo" checkout --quiet -b feature
if run install-local >/dev/null 2>&1; then
  fail 'install-local accepted a feature branch'
fi
output=$(run install-worktree)
assert_contains "$output" 'branch=feature'
git -C "$test_repo" checkout --quiet main
linked=$temporary/linked
git -C "$test_repo" worktree add --quiet -b linked-branch "$linked"
if SIDECAR_TEST_REPO="$linked" run install-local >/dev/null 2>&1; then
  fail 'install-local accepted a linked worktree'
fi
output=$(SIDECAR_TEST_REPO="$linked" run install-worktree)
linked_physical=$(CDPATH= cd -- "$linked" && pwd -P)
assert_contains "$output" "source=$linked_physical"
git -C "$linked" checkout --quiet --detach
output=$(SIDECAR_TEST_REPO="$linked" run install-worktree)
assert_contains "$output" 'branch=detached'

# Foreign files, foreign links, and broken links are refusal cases.
rm -f "$brew_prefix/bin/sidecar"
printf 'foreign\n' >"$brew_prefix/bin/sidecar"
if run install-worktree >/dev/null 2>&1; then
  fail 'foreign regular file was replaced'
fi
rm "$brew_prefix/bin/sidecar"
ln -s /bin/sh "$brew_prefix/bin/sidecar"
if run install-worktree >/dev/null 2>&1; then
  fail 'foreign symlink was replaced'
fi
rm "$brew_prefix/bin/sidecar"
ln -s "$temporary/missing" "$brew_prefix/bin/sidecar"
if run install-worktree >/dev/null 2>&1; then
  fail 'broken symlink was replaced'
fi

# Homebrew -> local calls unlink, and unlink failure leaves Homebrew active.
rm -f "$brew_prefix/bin/sidecar"
ln -s ../Cellar/sidecar/1.0.0/bin/sidecar "$brew_prefix/bin/sidecar"
: >"$brew_state/calls"
run install-worktree >/dev/null
grep -q '^unlink sidecar$' "$brew_state/calls" || fail 'Homebrew was not unlinked'
run use-homebrew >/dev/null
assert_kind homebrew

# Interruption after Homebrew unlink restores the formula link.
touch "$brew_state/interrupt-after-unlink"
if run install-worktree >/dev/null 2>&1; then
  fail 'interrupted Homebrew-to-local switch unexpectedly succeeded'
fi
rm "$brew_state/interrupt-after-unlink"
assert_kind homebrew
touch "$brew_state/unlink-fail"
if run install-worktree >/dev/null 2>&1; then
  fail 'unlink failure unexpectedly succeeded'
fi
rm "$brew_state/unlink-fail"
assert_kind homebrew

# Missing formula and foreign targets fail closed.
touch "$brew_state/formula-missing"
if run use-homebrew >/dev/null 2>&1; then
  fail 'use-homebrew accepted a missing formula'
fi
rm "$brew_state/formula-missing"
rm -f "$brew_prefix/bin/sidecar"
ln -s /bin/sh "$brew_prefix/bin/sidecar"
if run use-homebrew >/dev/null 2>&1; then
  fail 'use-homebrew replaced a foreign link'
fi

# Failed Homebrew relinking restores the previously active managed build.
rm -f "$brew_prefix/bin/sidecar"
SIDECAR_TEST_REPO="$linked" run install-worktree >/dev/null
before=$(readlink "$brew_prefix/bin/sidecar")
touch "$brew_state/link-fail"
if run use-homebrew >/dev/null 2>&1; then
  fail 'failed Homebrew relink unexpectedly succeeded'
fi
rm "$brew_state/link-fail"
[ "$(readlink "$brew_prefix/bin/sidecar")" = "$before" ] ||
  fail 'failed Homebrew relink did not restore the managed target'

# A nominally successful brew link must still resolve to the formula executable.
touch "$brew_state/link-invalid"
if run use-homebrew >/dev/null 2>&1; then
  fail 'invalid post-link target unexpectedly succeeded'
fi
rm "$brew_state/link-invalid"
[ "$(readlink "$brew_prefix/bin/sidecar")" = "$before" ] ||
  fail 'invalid post-link target did not restore the managed target'

# Interruption while restoring Homebrew rolls back to the managed build.
touch "$brew_state/interrupt-during-link"
if run use-homebrew >/dev/null 2>&1; then
  fail 'interrupted local-to-Homebrew switch unexpectedly succeeded'
fi
rm "$brew_state/interrupt-during-link"
[ "$(readlink "$brew_prefix/bin/sidecar")" = "$before" ] ||
  fail 'interrupted Homebrew relink did not restore the managed target'

output=$(run status)
assert_contains "$output" "managed command directory: $brew_prefix/bin"
assert_contains "$output" 'link state: local'
assert_contains "$output" "raw target: $before"
assert_contains "$output" 'activation version: sidecar version devel+'
assert_contains "$output" "current shell resolves:
  $brew_prefix/bin/sidecar"
assert_contains "$output" "interactive login shell resolves:
  $brew_prefix/bin/sidecar"
assert_contains "$output" "non-interactive login shell resolves:
  $brew_prefix/bin/sidecar"

# Status remains read-only and still reports PATH disagreement.
mkdir -p "$temporary/interactive" "$temporary/nlogin"
printf '#!/bin/sh\nprintf "sidecar version interactive-login\\n"\n' >"$temporary/interactive/sidecar"
printf '#!/bin/sh\nprintf "sidecar version non-interactive-login\\n"\n' >"$temporary/nlogin/sidecar"
chmod +x "$temporary/interactive/sidecar" "$temporary/nlogin/sidecar"
SIDECAR_TEST_PATH_PREFIX=$fake_bin
FAKE_LOGIN_SIDECAR=$temporary/interactive/sidecar
FAKE_NLOGIN_SIDECAR=$temporary/nlogin/sidecar
export SIDECAR_TEST_PATH_PREFIX FAKE_LOGIN_SIDECAR FAKE_NLOGIN_SIDECAR
output=$(run status)
unset SIDECAR_TEST_PATH_PREFIX FAKE_LOGIN_SIDECAR FAKE_NLOGIN_SIDECAR
assert_contains "$output" "current shell resolves:
  $fake_bin/sidecar
  sidecar version current-shell"
assert_contains "$output" "interactive login shell resolves:
  $temporary/interactive/sidecar
  sidecar version interactive-login"
assert_contains "$output" "non-interactive login shell resolves:
  $temporary/nlogin/sidecar
  sidecar version non-interactive-login"

# A sidecar earlier on PATH is pointed at the activated artifact so
# `install-worktree && sidecar` runs this build.
shadow=$temporary/shadow
mkdir -p "$shadow"
printf '#!/bin/sh\nprintf "sidecar version shadowed\\n"\n' >"$shadow/sidecar"
chmod +x "$shadow/sidecar"
SIDECAR_TEST_PATH_PREFIX=$shadow
FAKE_LOGIN_SIDECAR=$shadow/sidecar
FAKE_NLOGIN_SIDECAR=$shadow/sidecar
export SIDECAR_TEST_PATH_PREFIX FAKE_LOGIN_SIDECAR FAKE_NLOGIN_SIDECAR
output=$(run install-worktree)
unset SIDECAR_TEST_PATH_PREFIX FAKE_LOGIN_SIDECAR FAKE_NLOGIN_SIDECAR
assert_contains "$output" "pointed $shadow/sidecar at the activated build"
assert_contains "$output" 'verified: sidecar on PATH is this build'
[ -L "$shadow/sidecar" ] || fail 'PATH winner was not replaced with a symlink'
shadow_resolved=$(realpath -q "$shadow/sidecar")
brew_resolved=$(realpath -q "$brew_prefix/bin/sidecar")
[ "$shadow_resolved" = "$brew_resolved" ] ||
  fail "PATH winner resolves to $shadow_resolved, want $brew_resolved"
assert_kind local

# Login-shell PATH winners are retargeted even when the current shell already matches.
login_shadow=$temporary/login-shadow
mkdir -p "$login_shadow"
printf '#!/bin/sh\nprintf "sidecar version login-shadowed\\n"\n' >"$login_shadow/sidecar"
chmod +x "$login_shadow/sidecar"
FAKE_LOGIN_SIDECAR=$login_shadow/sidecar
export FAKE_LOGIN_SIDECAR
output=$(run install-worktree)
unset FAKE_LOGIN_SIDECAR
assert_contains "$output" "pointed $login_shadow/sidecar at the activated build"
assert_contains "$output" 'verified: sidecar on PATH is this build'
[ -L "$login_shadow/sidecar" ] || fail 'login PATH winner was not replaced with a symlink'

printf 'dev-install tests passed\n'
