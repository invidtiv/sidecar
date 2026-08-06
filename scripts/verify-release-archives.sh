#!/usr/bin/env bash
set -euo pipefail

dist=${1:-dist}
[[ -d $dist ]] || {
  echo "release directory does not exist: $dist" >&2
  exit 1
}
[[ -f $dist/checksums.txt ]] || {
  echo "missing $dist/checksums.txt" >&2
  exit 1
}

temporary=$(mktemp -d)
cleanup() {
  rm -rf "$temporary"
}
trap cleanup EXIT

host_os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$(uname -m)" in
  x86_64) host_arch=amd64 ;;
  arm64 | aarch64) host_arch=arm64 ;;
  *) host_arch=unsupported ;;
esac

find "$dist" -mindepth 1 -maxdepth 1 -type f -name '*.tar.gz' |
  LC_ALL=C sort >"$temporary/archives"
archive_count=$(wc -l <"$temporary/archives" | tr -d ' ')
[[ $archive_count -eq 4 ]] || {
  echo "expected 4 release archives, found $archive_count" >&2
  exit 1
}

archive_names=$(tr '[:upper:]' '[:lower:]' <"$temporary/archives")
for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64; do
  grep -Eq "_${target}\.tar\.gz$" <<<"$archive_names" || {
    echo "missing archive for $target" >&2
    exit 1
  }
done

index=0
while IFS= read -r archive; do
  index=$((index + 1))
  unpack="$temporary/unpack-$index"
  mkdir "$unpack"
  tar -xzf "$archive" -C "$unpack"

  # Sidecar archives are flat: binary + docs at the top level (no wrapper dir).
  find "$unpack" -mindepth 1 -maxdepth 1 >"$temporary/top-level-$index"
  if find "$unpack" -mindepth 1 -maxdepth 1 -type d | grep -q .; then
    echo "$archive must not wrap contents in a top-level directory" >&2
    exit 1
  fi

  binary="$unpack/sidecar"
  [[ -f $binary && -x $binary && ! -L $binary ]] || {
    echo "$archive is missing an executable sidecar binary" >&2
    exit 1
  }

  # Reject unexpected executables smuggled beside the binary.
  find "$unpack" -mindepth 1 -maxdepth 1 -type f -perm -111 \
    -exec basename {} \; | LC_ALL=C sort >"$temporary/executables-$index"
  printf 'sidecar\n' >"$temporary/expected-executables"
  if ! diff -u "$temporary/expected-executables" "$temporary/executables-$index"; then
    echo "$archive has unexpected executables at the top level" >&2
    exit 1
  fi

  for required in README.md CHANGELOG.md; do
    [[ -f $unpack/$required ]] || {
      echo "$archive is missing $required" >&2
      exit 1
    }
  done

  archive_lower=$(printf '%s' "$archive" | tr '[:upper:]' '[:lower:]')
  if [[ $archive_lower == *"_${host_os}_${host_arch}.tar.gz" ]]; then
    # GoReleaser strips the leading v from {{ .Version }}; archives look like
    # sidecar_0.91.0_darwin_arm64.tar.gz (or SNAPSHOT for local snapshots).
    version=$(basename "$archive" | sed -E 's/^sidecar_([^_]+)_.*/\1/')
    output=$("$binary" --version)
    [[ $output == "sidecar version $version" || $output == "sidecar version v$version" ]] || {
      echo "$archive has an unexpected version string: $output (expected $version)" >&2
      exit 1
    }
  fi
done <"$temporary/archives"

if command -v sha256sum >/dev/null 2>&1; then
  (cd "$dist" && sha256sum --check checksums.txt)
else
  (cd "$dist" && shasum -a 256 --check checksums.txt)
fi

echo "verified $archive_count archives"
