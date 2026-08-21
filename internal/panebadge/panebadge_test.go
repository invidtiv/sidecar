package panebadge

import (
	"testing"

	"github.com/marcus/sidecar/internal/state"
)

func splitTree(leaves int) *state.PaneLayoutJSON {
	root := &state.PaneLayoutJSON{Kind: "terminal", Open: true}
	for i := 1; i < leaves; i++ {
		root = &state.PaneLayoutJSON{
			Open: true,
			Split: &state.PaneSplitJSON{
				Axis: "cols", Ratio: 50,
				A: root,
				B: &state.PaneLayoutJSON{Kind: "shell"},
			},
		}
	}
	return root
}

func TestGlyphSaysNothingUntilThereIsASplit(t *testing.T) {
	for panes, want := range map[int]string{0: "", 1: "", 2: SplitGlyph, 3: "⊞3", 4: "⊞4"} {
		if got := Glyph(panes); got != want {
			t.Fatalf("Glyph(%d) = %q, want %q", panes, got, want)
		}
	}
}

func TestGlyphForCountsThePersistedTree(t *testing.T) {
	for leaves, want := range map[int]string{1: "", 2: SplitGlyph, 3: "⊞3"} {
		if got := GlyphFor(splitTree(leaves)); got != want {
			t.Fatalf("GlyphFor(%d leaves) = %q, want %q", leaves, got, want)
		}
	}
}

// A layout that is remembered but hidden is not on screen, and the badge says
// what is on screen.
func TestHiddenLayoutEarnsNoBadge(t *testing.T) {
	layout := splitTree(2)
	layout.Open = false
	if got := GlyphFor(layout); got != "" {
		t.Fatalf("hidden layout badged %q, want nothing", got)
	}
	if got := GlyphFor(nil); got != "" {
		t.Fatalf("absent layout badged %q, want nothing", got)
	}
}

func TestForReadsTheSurfaceTheWorkspaceSavedUnder(t *testing.T) {
	surface := ShellSurface("sc-shell-a")
	layout := splitTree(2)
	layout.Surface = surface
	wtState := state.WorkspaceState{PaneLayouts: map[string]*state.PaneLayoutJSON{surface: layout}}
	if got := For(wtState, surface); got != SplitGlyph {
		t.Fatalf("For(%q) = %q, want %q", surface, got, SplitGlyph)
	}
	if got := For(wtState, ShellSurface("sc-shell-b")); got != "" {
		t.Fatalf("another shell badged %q, want nothing", got)
	}
	if got := For(wtState, ""); got != "" {
		t.Fatalf("empty surface badged %q, want nothing", got)
	}
}
