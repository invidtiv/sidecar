#!/bin/bash
# screen-compare-evidence.sh - one command that produces the slice-2 decision-gate
# evidence for the byte-fed tmux screen model spike (td-64c916).
#
# It runs, in order:
#   1. the deterministic byte corpus against the adapter (no tmux server needed;
#      replays the committed fixtures recorded from an isolated tmux);
#   2. the shadow-comparison unit suite, including the privacy assertions;
#   3. the real application matrix and the alternate-screen attach finding,
#      driven against a THROWAWAY tmux server the test creates in its own temp
#      directory, with TMUX scrubbed and HOME redirected;
#   4. the sustained-output soak (memory, latency, per-burst command counts);
#   5. the per-burst cost benchmarks for the capture path and the model path.
#
# tmux safety: nothing here touches the developer's default tmux server. Every
# invocation inside the Go tests carries an explicit -S inside the test's own
# t.TempDir(), asserted before use. This script starts no tmux itself.
#
# Usage:
#   ./scripts/screen-compare-evidence.sh [OUT_DIR]
#
# Everything lands in OUT_DIR (default /tmp/sidecar-screen-compare), and the
# consolidated markdown report is printed to stdout.

set -euo pipefail

REPO_DIR="$(cd "$(dirname "$0")/.." && pwd)"
OUT_DIR="${1:-/tmp/sidecar-screen-compare}"
mkdir -p "$OUT_DIR"

cd "$REPO_DIR"

section() { printf '\n\n%s\n\n' "## $1"; }

{
    printf '# Byte-fed screen model — slice 2 evidence run\n\n'
    printf -- '- date: %s\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    printf -- '- host: %s %s\n' "$(uname -s)" "$(uname -m)"
    printf -- '- tmux: %s\n' "$(tmux -V 2>/dev/null || echo 'not installed')"
    printf -- '- go: %s\n' "$(go version)"
} | tee "$OUT_DIR/report.md"

run() {
    local name="$1"; shift
    local log="$OUT_DIR/$name.log"
    echo "==> $name" >&2
    if "$@" >"$log" 2>&1; then
        echo "PASS $name" >&2
    else
        echo "FAIL $name (see $log)" >&2
        FAILED=1
    fi
    printf '\n\n### %s\n\n```text\n' "$name" >>"$OUT_DIR/report.md"
    cat "$log" >>"$OUT_DIR/report.md"
    printf '```\n' >>"$OUT_DIR/report.md"
}

FAILED=0

run deterministic-corpus \
    go test ./internal/tty/screenmodel -count=1 -v -run 'TestCorpus|TestSeed|TestFrameOutput|TestSplit|TestDeviceQueries|TestReseeding'
run shadow-unit \
    go test ./internal/tty -count=1 -v -run 'TestCaptureCommandsUnchangedWhenCompareOff|TestCompareOn|TestBothMetadataLayouts|TestCompare|TestCursor|TestClassifier|TestReportAndJSON|TestHistory|TestRISIs|TestDegenerateGeometry|TestCaptureDeliveryUnchangedWhenModelPathOff'
run real-application-matrix \
    go test ./internal/tty -count=1 -v -timeout 900s -screencompare \
    -screencompare-out "$OUT_DIR/matrix.md" -run TestScreenCompareRealApplicationMatrix
run alt-screen-attach-finding \
    go test ./internal/tty -count=1 -v -timeout 300s -screencompare -run TestAltScreenAttachCannotRestoreTheMainScreen
run sustained-output-soak \
    go test ./internal/tty -count=1 -v -timeout 600s -screencompare -run TestScreenCompareSustainedOutputSoak
run per-burst-benchmarks \
    go test ./internal/tty -count=1 -run XXX -bench 'BenchmarkCapturePathPerBurst|BenchmarkModelPath|BenchmarkShadowCompare' -benchtime 300x -timeout 600s

if [ -f "$OUT_DIR/matrix.md" ]; then
    {
        printf '\n\n## Application matrix report\n\n'
        cat "$OUT_DIR/matrix.md"
    } >>"$OUT_DIR/report.md"
fi

cat "$OUT_DIR/report.md"
echo >&2
echo "report: $OUT_DIR/report.md" >&2
exit "$FAILED"
