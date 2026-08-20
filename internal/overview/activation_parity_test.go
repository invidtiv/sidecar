package overview

import (
	"testing"

	"github.com/marcus/sidecar/internal/targetactivation"
)

// TestPreviewDispatchesEveryPlanKind is one half of the surface parity pair;
// its twin is TestTerminalDispatchesEveryPlanKind in the workspace plugin. A
// kind that activates on one surface must activate on the other, and the shared
// list of plan kinds a scanned span can produce is what both are measured
// against.
func TestPreviewDispatchesEveryPlanKind(t *testing.T) {
	t.Parallel()
	for _, kind := range targetactivation.PlanKindsFromSpans() {
		if !previewHandlesPlanKind(kind) {
			t.Fatalf("the global preview surface does not activate %s", kind)
		}
	}
	if previewHandlesPlanKind(targetactivation.PlanKind("invented")) {
		t.Fatal("an unknown plan kind must not report as handled")
	}
}
