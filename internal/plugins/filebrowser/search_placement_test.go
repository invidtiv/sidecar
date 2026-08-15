package filebrowser

import (
	"testing"

	"github.com/marcus/sidecar/internal/filefind"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/projectsearch"
)

// Centring is arithmetic and the pane divider is wherever the user dragged it,
// so the two coincide sooner or later — at 200x50 the finder's left border
// landed exactly on the preview pane's border column, and the box read as
// welded to the frame with sixty empty columns beside it. The box moves; the
// frame does not.
func TestSearchBoxNeverLandsOnThePaneDivider(t *testing.T) {
	for width := 80; width <= 260; width++ {
		p := &Plugin{width: width, height: 40}
		p.calculatePaneWidths()

		for _, preferred := range []int{filefind.PreferredWidth, projectsearch.PreferredWidth} {
			w := p.boxWidthOffTheDivider(preferred)
			if w > modal.ContentBoxWidth(width) {
				t.Fatalf("width %d: box %d is wider than the surface allows", width, w)
			}
			left := (width - w) / 2
			right := left + w - 1
			for _, col := range []int{p.treeWidth, p.treeWidth + 1} {
				if left == col || right == col {
					t.Errorf("width %d: a %d-cell box puts a border on the frame column %d",
						width, w, col)
				}
			}
		}
	}
}
