package gitstatus

import (
	"testing"

	"github.com/marcus/sidecar/internal/keymap"
)

// The pane switcher's entry is bound per keymap context, so the contexts this
// plugin actually reports while browsing are the ones that have to carry it —
// which is not the same set as the contexts that merely have bindings in
// keymap. Git names both the focused pane and the cursor's row in its context,
// so reading a commit and reading a file diff are two contexts of their own,
// and `git-history` is never reported at all. A binding on a context nobody
// stands in is a key that does nothing, and the gap it leaves behind is a
// plugin the switcher cannot be opened from.
func TestBrowseContextsCarryThePaneSwitcherEntry(t *testing.T) {
	p := &Plugin{tree: &FileTree{Modified: []*FileEntry{{Path: "a.go"}}}}

	cases := []struct {
		name  string
		setup func()
		want  string
	}{
		{"cursor on a file row", func() { p.activePane = PaneSidebar; p.cursor = 0 }, "git-status"},
		{"cursor on a commit row", func() { p.activePane = PaneSidebar; p.cursor = 1 }, "git-status-commits"},
		{"file diff focused", func() { p.activePane = PaneDiff; p.cursor = 0 }, "git-status-diff"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			got := p.FocusContext()
			if got != tc.want {
				t.Fatalf("FocusContext() = %q, want %q", got, tc.want)
			}
			if key := paneSwitcherKeyForContext(got); key != "ctrl+n" {
				t.Fatalf("%s: open-pane is bound to %q, want ctrl+n — the switcher is unreachable from here", got, key)
			}
		})
	}
}

func paneSwitcherKeyForContext(context string) string {
	for _, b := range keymap.DefaultBindings() {
		if b.Context == context && b.Command == "open-pane" {
			return b.Key
		}
	}
	return ""
}
