#!/bin/bash
# bundled-update-fixture.sh - build an isolated fake package-manager world for
# proving the bundled update journey (td-393e81) against the real app.
#
# Nothing here touches the machine's real brew, Go bin, tmux server, PATH, or
# installed products: every binary the app resolves lives under a scratch root
# that is put ahead of PATH only for the driven Sidecar process.
#
#   ./scripts/bundled-update-fixture.sh build ROOT SCENARIO
#
# Scenarios:
#   all-outdated     Sidecar, td, Tasks all outdated, all Homebrew-managed
#   tasks-current    Tasks already at the released version
#   tasks-absent     standalone Tasks not installed at all
#   mixed            td resolves outside the cellar (unmanaged) - manual only
#   tasks-fails      `brew upgrade marcus/tap/tasks` fails
#   standalone-only  Sidecar already current; only td and Tasks are outdated
#
# The fixture writes ROOT/wrapper.sh; point SIDECAR_BIN at it.

set -euo pipefail

cmd="${1:-}"
ROOT="${2:-}"
SCENARIO="${3:-all-outdated}"

if [ "$cmd" != "build" ] || [ -z "$ROOT" ]; then
    sed -n '2,20p' "$0"
    exit 1
fi
case "$ROOT" in
    /tmp/*|/private/tmp/*) ;;
    *) echo "refusing ROOT='$ROOT': must live under /tmp" >&2; exit 1 ;;
esac

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"

# Releases are served from a local fixture API, not GitHub: the proof is then
# hermetic, deterministic, and immune to rate limits.
SIDECAR_LATEST="v9.9.0"
TD_LATEST="v9.9.1"
TASKS_LATEST="v9.9.2"
API_PORT="${BUNDLED_UPDATE_API_PORT:-8731}"

# A version deliberately older than every real release.
OLD="0.0.1"

SIDECAR_START="$OLD"
[ "$SCENARIO" = "standalone-only" ] && SIDECAR_START="${SIDECAR_LATEST#v}"

rm -rf "$ROOT"
mkdir -p "$ROOT/bin" "$ROOT/cellar" "$ROOT/log" "$ROOT/elsewhere"

# --- fake product binaries -------------------------------------------------
# Each keg holds a script that prints the version its directory is named for,
# in the same shape as the real command — and, importantly, only for the exact
# arguments that real command accepts. A fake that answered any argv would let
# a wrong version invocation pass the proof unnoticed.
version_args_for() {
    case "$1" in
        tasks) echo "version" ;;
        td)    echo "version --short" ;;
        *)     echo "--version" ;;
    esac
}

version_output_for() {
    case "$1" in
        tasks|tasks-tui|tasks-api) echo "$1 %s" ;;
        *)                         echo "%s" ;;
    esac
}

write_fake_binary() {
    local path="$1" bin="$2" ver="$3"
    local want fmt
    want="$(version_args_for "$bin")"
    fmt="$(version_output_for "$bin")"
    cat > "$path" <<EOF
#!/bin/bash
# Fixture stand-in for $bin. Accepts only the real command's version arguments.
if [ "\$*" != "$want" ]; then
    echo "$bin: unexpected arguments: \$*" >&2
    exit 1
fi
printf '$fmt\n' '$ver'
EOF
    chmod +x "$path"
}

make_keg() {
    local name="$1" ver="$2"
    local dir="$ROOT/cellar/$name/$ver/bin"
    mkdir -p "$dir"
    for bin in ${3:-$name}; do
        write_fake_binary "$dir/$bin" "$bin" "$ver"
    done
    echo "$dir"
}

# link_bin NAME KEGDIR - expose a keg binary on the fixture PATH via symlink,
# exactly how Homebrew exposes a formula.
link_bin() {
    ln -sfn "$2/$1" "$ROOT/bin/$1"
}

install_sidecar() {
    local ver="$1"
    local dir
    dir="$(make_keg sidecar "$ver")"
    link_bin sidecar "$dir"
}

install_td() {
    local ver="$1" where="${2:-cellar}"
    if [ "$where" = "cellar" ]; then
        local dir
        dir="$(make_keg td "$ver")"
        link_bin td "$dir"
    else
        # An install Sidecar does not manage: outside any cellar or Go bin.
        write_fake_binary "$ROOT/elsewhere/td" td "$ver"
        ln -sfn "$ROOT/elsewhere/td" "$ROOT/bin/td"
    fi
}

install_tasks() {
    local ver="$1"
    local dir
    dir="$(make_keg tasks "$ver" 'tasks tasks-tui tasks-api')"
    link_bin tasks "$dir"
    link_bin tasks-tui "$dir"
    link_bin tasks-api "$dir"
}

install_sidecar "$SIDECAR_START"
case "$SCENARIO" in
    tasks-absent)
        install_td "$OLD"
        ;;
    tasks-current)
        install_td "$OLD"
        install_tasks "${TASKS_LATEST#v}"
        ;;
    mixed)
        install_td "$OLD" elsewhere
        install_tasks "$OLD"
        ;;
    *)
        install_td "$OLD"
        install_tasks "$OLD"
        ;;
esac

# --- fake brew -------------------------------------------------------------
cat > "$ROOT/bin/brew" <<EOF
#!/bin/bash
# Fake Homebrew. Records every invocation and upgrades only inside the fixture.
set -uo pipefail
ROOT="$ROOT"
SCENARIO="$SCENARIO"
SLOW="${BUNDLED_UPDATE_SLOW:-0}"
SIDECAR_LATEST="${SIDECAR_LATEST#v}"
TD_LATEST="${TD_LATEST#v}"
TASKS_LATEST="${TASKS_LATEST#v}"
echo "brew \$*" >> "\$ROOT/log/commands.log"

formula_name() { echo "\${1##*/}"; }

case "\${1:-}" in
    update)
        exit 0
        ;;
    --cellar|--prefix)
        name="\$(formula_name "\${2:-}")"
        # Only formulae this fixture actually installed are owned by brew.
        if [ -d "\$ROOT/cellar/\$name" ]; then
            echo "\$ROOT/cellar/\$name"
            exit 0
        fi
        echo "Error: No available formula" >&2
        exit 1
        ;;
    upgrade)
        name="\$(formula_name "\${2:-}")"
        case "\$name" in
            sidecar) latest="\$SIDECAR_LATEST" ;;
            td)      latest="\$TD_LATEST" ;;
            tasks)   latest="\$TASKS_LATEST" ;;
            *) echo "Error: unknown formula \$name" >&2; exit 1 ;;
        esac
        if [ "\$name" = "tasks" ] && [ "\$SCENARIO" = "tasks-fails" ]; then
            echo "Error: The 'brew link' step did not complete successfully" >&2
            exit 1
        fi
        case "\$name" in
            tasks) bins="tasks tasks-tui tasks-api" ;;
            *)     bins="\$name" ;;
        esac
        # An optional delay makes the per-product progress display observable
        # in a driven capture; real package managers are never this fast.
        [ "\$SLOW" = "1" ] && sleep 3
        dir="\$ROOT/cellar/\$name/\$latest/bin"
        mkdir -p "\$dir"
        for b in \$bins; do
            case "\$b" in
                tasks) want="version" ;;
                td)    want="version --short" ;;
                *)     want="--version" ;;
            esac
            case "\$b" in
                tasks|tasks-tui|tasks-api) fmt="\$b %s" ;;
                *)                         fmt="%s" ;;
            esac
            cat > "\$dir/\$b" <<INNER
#!/bin/bash
if [ "\\\$*" != "\$want" ]; then
    echo "\$b: unexpected arguments: \\\$*" >&2
    exit 1
fi
printf '\$fmt\\n' '\$latest'
INNER
            chmod +x "\$dir/\$b"
            ln -sfn "\$dir/\$b" "\$ROOT/bin/\$b"
        done
        echo "==> Upgrading \$name to \$latest"
        exit 0
        ;;
esac
exit 0
EOF
chmod +x "$ROOT/bin/brew"

# A `go` that refuses to run: nothing in these journeys is a Go install, so any
# invocation is a defect worth failing loudly on.
cat > "$ROOT/bin/go" <<EOF
#!/bin/bash
echo "go \$*" >> "$ROOT/log/commands.log"
echo "fixture: go install must not be used for a Homebrew-managed product" >&2
exit 1
EOF
chmod +x "$ROOT/bin/go"

# --- local release API -----------------------------------------------------
serve_release() {
    local repo="$1" tag="$2" notes="$3"
    local dir="$ROOT/api/repos/marcus/$repo/releases"
    mkdir -p "$dir"
    cat > "$dir/latest" <<EOF
{"tag_name": "$tag",
 "name": "$repo $tag",
 "body": "$notes",
 "html_url": "https://example.invalid/$repo/releases/tag/$tag"}
EOF
}
serve_release sidecar "$SIDECAR_LATEST" "- Bundled update journey now names every product\n- Per-product install provenance"
serve_release td "$TD_LATEST" "- td fixture release"
serve_release tasks "$TASKS_LATEST" "- tasks fixture release"

# --- wrapper ---------------------------------------------------------------
# Sidecar runs from inside the fake cellar so its own provenance resolves to
# the fake formula, with the fixture bin ahead of the real PATH.
REAL_BIN="$ROOT/cellar/sidecar/$SIDECAR_START/bin/sidecar"
go build -o "$ROOT/sidecar.real" -ldflags "-X main.Version=$SIDECAR_START" "$REPO_DIR/cmd/sidecar" >/dev/null
cp "$ROOT/sidecar.real" "$REAL_BIN"

# PATH: the fixture bin comes first, and every other directory is mirrored
# through a scratch dir with the fixture's own products removed. Without this a
# real `tasks` further down PATH would masquerade as the fixture's, and the
# "standalone Tasks not installed" journey could not be proved at all.
MIRROR="$ROOT/pathmirror"
mkdir -p "$MIRROR"
IFS=':' read -r -a path_dirs <<<"$PATH"
for d in "${path_dirs[@]}"; do
    [ -d "$d" ] || continue
    for f in "$d"/*; do
        [ -x "$f" ] && [ ! -d "$f" ] || continue
        base="$(basename "$f")"
        case "$base" in
            sidecar|td|tasks|tasks-tui|tasks-api|brew|go) continue ;;
        esac
        [ -e "$MIRROR/$base" ] || ln -sfn "$f" "$MIRROR/$base" 2>/dev/null || true
    done
done

cat > "$ROOT/wrapper.sh" <<EOF
#!/bin/bash
export PATH="$ROOT/bin:$MIRROR"
export SIDECAR_RELEASE_API_BASE="http://127.0.0.1:$API_PORT"
exec "$REAL_BIN" "\$@"
EOF
chmod +x "$ROOT/wrapper.sh"

echo "scenario:        $SCENARIO"
echo "root:            $ROOT"
echo "sidecar version: $SIDECAR_START (latest $SIDECAR_LATEST)"
echo "wrapper:         $ROOT/wrapper.sh"
echo "command log:     $ROOT/log/commands.log"
echo "release api:     http://127.0.0.1:$API_PORT (serve with: python3 -m http.server $API_PORT --directory $ROOT/api)"
