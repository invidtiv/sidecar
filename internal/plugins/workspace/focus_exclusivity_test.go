package workspace

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// The twin of the global browser's test (internal/overview): the project
// workspace must blur the same way when the notification centre takes the
// keyboard, and for the same reason — both paint every panel through the shared
// border rule. A change that lands on one surface and not the other is a bug.
func TestWorkspaceBlursWhileFocusIsHeldOutsideThePanes(t *testing.T) {
	t.Cleanup(func() { styles.SetFocusHeldOutsidePanes(false) })
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)
	p.View(p.width, p.height)

	styles.SetFocusHeldOutsidePanes(false)
	lit := p.View(p.width, p.height)
	styles.SetFocusHeldOutsidePanes(true)
	blurred := p.View(p.width, p.height)

	if blurred == lit {
		t.Fatal("the workspace drew identically with the centre focused: its focused pane stayed lit")
	}
	if ansi.Strip(blurred) != ansi.Strip(lit) {
		t.Fatal("blurring changed content, not just border chrome")
	}

	styles.SetFocusHeldOutsidePanes(false)
	if got := p.View(p.width, p.height); got != lit {
		t.Fatal("the focused pane did not re-light when focus came back to the surface")
	}
}
