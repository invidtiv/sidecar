#!/usr/bin/env bash
# Prove gates do not wait for golangci-lint's global lock, and still analyze
# the whole tree. Uses the real linter against a tiny isolated repository.
set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
temporary=$(mktemp -d)
holder_pid=
cleanup() {
  if [[ -n $holder_pid ]]; then
    kill "$holder_pid" 2>/dev/null || true
    wait "$holder_pid" 2>/dev/null || true
  fi
  rm -rf "$temporary"
}
trap cleanup EXIT

mkdir -p "$temporary/repo/scripts" "$temporary/tmp"
cp "$repo_root/Makefile" "$temporary/repo/"
cp "$repo_root/scripts/pre-commit.sh" "$temporary/repo/scripts/"
cp "$repo_root/.golangci.yml" "$temporary/repo/"
go_version=$(GOWORK=off go list -m -f '{{.GoVersion}}')
printf 'module lintparallelproof\n\ngo %s\n' "$go_version" >"$temporary/repo/go.mod"
cat >"$temporary/repo/main.go" <<'EOF'
package main

func main() {}
EOF

# Synchronize on an acquired lock rather than assuming the holder is ready
# after a sleep. TMPDIR is private so this never blocks another real lane.
cat >"$temporary/hold.go" <<'EOF'
package main

import (
  "fmt"
  "os"
  "syscall"
)

func main() {
  f, err := os.Create(os.Args[1])
  if err != nil { panic(err) }
  defer f.Close()
  if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { panic(err) }
  fmt.Println("locked")
  var release [1]byte
  if _, err := os.Stdin.Read(release[:]); err != nil { panic(err) }
}
EOF
GOWORK=off go build -o "$temporary/hold" "$temporary/hold.go"
mkfifo "$temporary/ready" "$temporary/release"
exec 3<>"$temporary/release"
"$temporary/hold" "$temporary/tmp/golangci-lint.lock" <&3 >"$temporary/ready" &
holder_pid=$!
read -r ready <"$temporary/ready"
[[ $ready == locked ]]

cd "$temporary/repo"
git init --quiet
export TMPDIR="$temporary/tmp" GOWORK=off

for target in lint lint-all lint-linux; do
  if ! output=$(timeout 60s make "$target" 2>&1); then
    printf '%s failed while another runner held the global lock:\n%s\n' "$target" "$output" >&2
    exit 1
  fi
done
if ! output=$(timeout 60s bash scripts/pre-commit.sh 2>&1); then
  printf 'pre-commit failed while another runner held the global lock:\n%s\n' "$output" >&2
  exit 1
fi

# An untouched, unstaged file must still be checked. Parallelism must not
# change coverage to only new or staged lines, or turn a real failure green.
cat >unused.go <<'EOF'
package main

func unusedFunction() {}
EOF
for target in lint lint-all lint-linux; do
  if output=$(timeout 60s make "$target" 2>&1); then
    printf '%s missed an unused function\n' "$target" >&2
    exit 1
  fi
  [[ $output == *"unusedFunction"* && $output == *"unused"* ]] || {
    printf '%s failed without reporting the lint issue:\n%s\n' "$target" "$output" >&2
    exit 1
  }
done
if output=$(timeout 60s bash scripts/pre-commit.sh 2>&1); then
  echo 'pre-commit missed an unused function' >&2
  exit 1
fi
[[ $output == *"unusedFunction"* && $output == *"unused"* ]] || {
  printf 'pre-commit failed without reporting the lint issue:\n%s\n' "$output" >&2
  exit 1
}

printf 'lint parallel-runner and full-coverage proof passed\n'
