package tty

import "testing"

func TestGeometryHolderHintUsesOnlyCurrentOwnerMetadata(t *testing.T) {
	session := "holder-hint-proof"
	t.Cleanup(func() { geometryHolders.Delete(session) })
	geometryHolders.Store(session, geometryHolder{owner: "phone-123", kind: "ios", label: "iPhone"})
	if got := GeometryHolderHint(session); got != "sized for iPhone" {
		t.Fatalf("hint = %q", got)
	}
	geometryHolders.Store(session, geometryHolder{owner: InstanceID(), kind: "tui", label: "This screen"})
	if got := GeometryHolderHint(session); got != "" {
		t.Fatalf("local holder hint = %q", got)
	}
	kind, label := holderFromMetadata("desktop-123:2:0", "old-phone-456", "ios", "Old iPhone")
	if kind != "tui" || label != "TUI on desktop" {
		t.Fatalf("stale metadata attributed to current owner: %q %q", kind, label)
	}
	kind, label = holderFromMetadata("", "old-phone-456", "ios", "Old iPhone")
	if kind != "" || label != "" {
		t.Fatal("unowned pane retained old holder")
	}
}
