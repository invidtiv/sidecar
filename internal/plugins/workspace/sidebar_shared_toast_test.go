package workspace

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// A refusal to split is only useful if the user can read it, and the window
// that triggers it is the narrow one: at 60x24 the sidebar is seventeen
// columns, where the full message truncated to "⚠ Document pan…".
func TestPaneFitToastStaysLegibleInANarrowSidebar(t *testing.T) {
	msg := paneFitMessage("Document", SplitCols)

	for _, width := range []int{15, 24, 40, 80} {
		got := fitToast(msg, width)
		if w := ansi.StringWidth(got); w > width {
			t.Errorf("width %d: %q is %d cells", width, got, w)
		}
		if strings.HasSuffix(got, "…") {
			t.Errorf("width %d: the message was cut mid-word: %q", width, got)
		}
		if !strings.Contains(strings.ToLower(got), "narrow") && !strings.Contains(strings.ToLower(got), "wider") {
			t.Errorf("width %d: %q does not say what is wrong", width, got)
		}
	}

	// The tall-window refusal degrades the same way.
	if got := fitToast(paneFitMessage("Issue", SplitRows), 15); got != "Too short" {
		t.Errorf("narrow sidebar shows %q, want a short form", got)
	}
}
