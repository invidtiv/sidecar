package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/parityscan"
	"github.com/marcus/sidecar/internal/targetactivation"
)

// TestTerminalDispatchesEveryPlanKind is one half of the surface parity pair;
// its twin is TestPreviewDispatchesEveryPlanKind in internal/overview. A kind
// that activates on one surface must activate on the other, and the shared list
// of plan kinds a scanned span can produce is what both are measured against.
func TestTerminalDispatchesEveryPlanKind(t *testing.T) {
	t.Parallel()
	for _, kind := range targetactivation.PlanKindsFromSpans() {
		if !terminalHandlesPlanKind(kind) {
			t.Fatalf("the workspace terminal surface does not activate %s", kind)
		}
	}
	if terminalHandlesPlanKind(targetactivation.PlanKind("invented")) {
		t.Fatal("an unknown plan kind must not report as handled")
	}
}

// TestTerminalHandledKindsAreTheDispatchedKinds closes the gap the declaration
// above leaves open: terminalHandlesPlanKind is hand-written, so a kind added to
// it but never given a branch in activateResolvedTerminalLink would satisfy the
// parity pair while clicking it did nothing. This reads the real switch out of
// the source and requires the two to name exactly the same kinds.
func TestTerminalHandledKindsAreTheDispatchedKinds(t *testing.T) {
	t.Parallel()
	declared := parityscan.HandledKinds(t, "terminal_links.go", "terminalHandlesPlanKind")
	dispatched := parityscan.HandledKinds(t, "terminal_links.go", "activateResolvedTerminalLink")
	parityscan.RequireSameKinds(t, "the workspace terminal surface", declared, dispatched)
}
