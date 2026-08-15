package filebrowser

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
)

func fbWheel(x, y int, up bool) tea.MouseWheelMsg {
	btn := tea.MouseWheelDown
	if up {
		btn = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: btn}
}

// fbBodyPoint returns a point over modal-body that no control covers.
func fbBodyPoint(t *testing.T, h *mouse.Handler) (int, int) {
	t.Helper()
	for _, r := range h.HitMap.Regions() {
		if r.ID != "modal-body" {
			continue
		}
		x := r.Rect.X + r.Rect.W/2
		for y := r.Rect.Y; y < r.Rect.Y+r.Rect.H; y++ {
			if hit := h.HitMap.Test(x, y); hit != nil && hit.ID == "modal-body" {
				return x, y
			}
		}
	}
	t.Fatal("no free modal-body point found")
	return 0, 0
}

// fbModalPlugin renders the plugin with the given overlay open, so the query
// sees the same hit map the real frame produced. The tree cursor sits mid-list
// so the panes underneath would report movable.
func fbModalPlugin(t *testing.T, open func(p *Plugin), height int) *Plugin {
	t.Helper()
	p := newScrollBurstPlugin(t, 240)
	p.height = height
	p.treeCursor = 40
	open(p)
	p.renderView()
	return p
}

func TestFilesInfoModalWheelIsAnsweredByTheModal(t *testing.T) {
	p := fbModalPlugin(t, func(p *Plugin) { p.infoMode = true }, 40)
	x, y := fbBodyPoint(t, p.mouseHandler)

	// The file info modal is short: every wheel event over it is a no-op.
	for _, up := range []bool{true, false} {
		if !p.WheelAtBoundary(fbWheel(x, y, up)) {
			t.Errorf("info modal body wheel (up=%v) was not bounded", up)
		}
		if !p.WheelAtBoundary(fbWheel(0, 0, up)) {
			t.Errorf("info modal backdrop wheel (up=%v) was not absorbed", up)
		}
	}

	// The tree underneath is mid-cursor and would report movable; the modal
	// still owns the answer.
	if !(sharedBoundsMovable(p)) {
		t.Fatal("precondition: the tree pane should be movable")
	}
	if !p.WheelAtBoundary(fbWheel(2, 5, false)) {
		t.Fatal("an open modal must answer instead of the tree underneath")
	}
}

// sharedBoundsMovable reports whether the tree pane could still move down.
func sharedBoundsMovable(p *Plugin) bool {
	return p.treeCursor < p.tree.Len()-1 && p.treeCursor > 0
}

func TestFilesBlameModalWheelIsAbsorbed(t *testing.T) {
	// Blame renders a fixed window of lines and moves through them with the
	// keyboard, so its modal body never scrolls: the whole wheel stream over an
	// open blame view is a known no-op.
	p := fbModalPlugin(t, func(p *Plugin) {
		p.blameMode = true
		p.blameState = &BlameState{FilePath: "a.go"}
		for i := range 400 {
			p.blameState.Lines = append(p.blameState.Lines, BlameLine{
				CommitHash: "abcdef1",
				Author:     "someone",
				LineNo:     i + 1,
				Content:    strings.Repeat("x", 40),
			})
		}
	}, 40)
	x, y := fbBodyPoint(t, p.mouseHandler)
	for _, pt := range [][2]int{{x, y}, {0, 0}} {
		for _, up := range []bool{true, false} {
			if !p.WheelAtBoundary(fbWheel(pt[0], pt[1], up)) {
				t.Errorf("blame wheel at (%d,%d) up=%v was not bounded", pt[0], pt[1], up)
			}
		}
	}
	if p.blameState.ScrollOffset != 0 || p.blameState.Cursor != 0 {
		t.Fatal("the boundary query must not move blame state")
	}
}

// A modal body that overflows a short screen is movable through the shared
// modal bounds, top to bottom, with the first reverse event passing.
func TestFilesModalBodyTopMiddleBottom(t *testing.T) {
	p := fbModalPlugin(t, func(p *Plugin) {
		p.blameMode = true
		p.blameState = &BlameState{FilePath: "a.go"}
		for i := range 60 {
			p.blameState.Lines = append(p.blameState.Lines, BlameLine{LineNo: i + 1, Content: "line"})
		}
	}, 12)
	x, y := fbBodyPoint(t, p.mouseHandler)
	if !p.WheelAtBoundary(fbWheel(x, y, true)) {
		t.Fatal("expected bounded at the top of an overflowing body")
	}
	if p.WheelAtBoundary(fbWheel(x, y, false)) {
		t.Fatal("expected movable downward at the top of an overflowing body")
	}

	p.blameModal.ScrollToBottom()
	p.renderView()
	x, y = fbBodyPoint(t, p.mouseHandler)
	if !p.WheelAtBoundary(fbWheel(x, y, false)) {
		t.Fatal("expected bounded at the bottom of an overflowing body")
	}
	if p.WheelAtBoundary(fbWheel(x, y, true)) {
		t.Fatal("reverse event after the boundary must pass")
	}
}

func TestFilesBlameModalUnknownUntilRebuiltAfterAsyncLoad(t *testing.T) {
	p := fbModalPlugin(t, func(p *Plugin) {
		p.blameMode = true
		p.blameState = &BlameState{FilePath: "a.go", IsLoading: true}
	}, 40)
	x, y := fbBodyPoint(t, p.mouseHandler)
	if !p.WheelAtBoundary(fbWheel(x, y, false)) {
		t.Fatal("precondition: the loading modal is bounded")
	}

	// The async result lands: the host must invalidate the cached geometry.
	p.Update(BlameLoadedMsg{Epoch: p.ctx.Epoch, Lines: []BlameLine{{LineNo: 1, Content: "x"}}})
	if p.WheelAtBoundary(fbWheel(x, y, false)) {
		t.Fatal("expected unknown (false) between the async load and the next render")
	}
	p.renderView()
	if !p.WheelAtBoundary(fbWheel(x, y, false)) {
		t.Fatal("expected an exact answer again after re-rendering")
	}
}

func TestFilesQuickOpenCursorBounds(t *testing.T) {
	tests := []struct {
		name     string
		matches  int
		cursor   int
		scanning bool
		up       bool
		want     bool
	}{
		{name: "top up", matches: 20, cursor: 0, up: true, want: true},
		{name: "top down", matches: 20, cursor: 0},
		{name: "middle up", matches: 20, cursor: 9, up: true},
		{name: "middle down", matches: 20, cursor: 9},
		{name: "bottom down", matches: 20, cursor: 19, want: true},
		{name: "bottom up (reverse)", matches: 20, cursor: 19, up: true},
		{name: "no matches", matches: 0, cursor: 0, want: true},
		{name: "scan in flight can still add matches", matches: 20, cursor: 19, scanning: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := fbModalPlugin(t, func(p *Plugin) {
				p.quickOpenMode = true
				p.quickOpenMatches = make([]QuickOpenMatch, tt.matches)
				p.quickOpenCursor = tt.cursor
				p.quickOpenScanning = tt.scanning
			}, 40)
			// The wheel moves the cursor wherever the pointer sits, so both a
			// point over the list and one over the backdrop answer the same.
			for _, x := range []int{2, 60} {
				if got := p.WheelAtBoundary(fbWheel(x, 5, tt.up)); got != tt.want {
					t.Errorf("x=%d: got %v, want %v", x, got, tt.want)
				}
			}
		})
	}
}

func TestFilesProjectSearchCursorBounds(t *testing.T) {
	results := []SearchFileResult{{
		Path:    "a.go",
		Matches: []SearchMatch{{LineNo: 1}, {LineNo: 2}, {LineNo: 3}},
	}}
	newState := func(cursor int, searching bool) *ProjectSearchState {
		return &ProjectSearchState{Results: results, Cursor: cursor, IsSearching: searching}
	}
	last := (&ProjectSearchState{Results: results}).FlatLen() - 1

	tests := []struct {
		name  string
		state *ProjectSearchState
		up    bool
		want  bool
	}{
		{name: "top up", state: newState(0, false), up: true, want: true},
		{name: "top down", state: newState(0, false)},
		{name: "bottom down", state: newState(last, false), want: true},
		{name: "bottom up (reverse)", state: newState(last, false), up: true},
		{name: "still searching", state: newState(last, true)},
		{name: "no state", state: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := fbModalPlugin(t, func(p *Plugin) {
				p.projectSearchMode = true
				p.projectSearchState = tt.state
			}, 40)
			if got := p.WheelAtBoundary(fbWheel(30, 10, tt.up)); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilesExitConfirmationAbsorbsWheelAndEditorStaysUnknown(t *testing.T) {
	p := fbModalPlugin(t, func(p *Plugin) { p.showExitConfirmation = true }, 40)
	for _, up := range []bool{true, false} {
		if !p.WheelAtBoundary(fbWheel(2, 5, up)) {
			t.Errorf("exit confirmation wheel (up=%v) was not absorbed", up)
		}
	}

	p = fbModalPlugin(t, func(p *Plugin) { p.inlineEditMode = true }, 40)
	if p.WheelAtBoundary(fbWheel(60, 5, false)) {
		t.Error("the inline editor owns its wheel and must stay unknown")
	}
}

func TestFilesFileOperationBarLeavesPanesAnswering(t *testing.T) {
	p := fbModalPlugin(t, func(p *Plugin) {
		p.fileOpMode = FileOpRename
		p.treeCursor = 0
	}, 40)
	if !p.WheelAtBoundary(fbWheel(2, 5, true)) {
		t.Fatal("the tree top must stay bounded while the file-op bar is open")
	}
	if p.WheelAtBoundary(fbWheel(2, 5, false)) {
		t.Fatal("the tree must stay movable downward while the file-op bar is open")
	}
}

func TestFilesModalHorizontalWheelIsUnknown(t *testing.T) {
	p := fbModalPlugin(t, func(p *Plugin) {
		p.quickOpenMode = true
		p.quickOpenMatches = make([]QuickOpenMatch, 3)
	}, 40)
	if p.WheelAtBoundary(tea.MouseWheelMsg{X: 30, Y: 5, Button: tea.MouseWheelLeft}) {
		t.Fatal("horizontal wheel must stay unknown")
	}
}
