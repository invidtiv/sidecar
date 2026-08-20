package overview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The global Workspaces browser must go blurred when an app-level surface — the
// notification centre — takes the keyboard, or the screen shows two focused
// panes at once. It inherits that from the shared border rule rather than
// knowing the centre exists, so what this checks is that every panel on this
// surface is painted through that rule: borders change, content bytes do not.
func TestGlobalSurfaceBlursWhileFocusIsHeldOutsideThePanes(t *testing.T) {
	t.Cleanup(func() { styles.SetFocusHeldOutsidePanes(false) })
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	m.WorkspacesView(previewWide, previewTall)

	styles.SetFocusHeldOutsidePanes(false)
	lit := m.WorkspacesView(previewWide, previewTall)
	styles.SetFocusHeldOutsidePanes(true)
	blurred := m.WorkspacesView(previewWide, previewTall)

	if blurred == lit {
		t.Fatal("the Workspaces browser drew identically with the centre focused: its focused pane stayed lit")
	}
	if strings.Count(ansi.Strip(blurred), "\n") != strings.Count(ansi.Strip(lit), "\n") ||
		ansi.Strip(blurred) != ansi.Strip(lit) {
		t.Fatal("blurring changed content, not just border chrome")
	}

	styles.SetFocusHeldOutsidePanes(false)
	if got := m.WorkspacesView(previewWide, previewTall); got != lit {
		t.Fatal("the focused pane did not re-light when focus came back to the surface")
	}
}
