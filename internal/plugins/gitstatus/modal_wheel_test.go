package gitstatus

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
)

func gsWheel(x, y int, up bool) tea.MouseWheelMsg {
	btn := tea.MouseWheelDown
	if up {
		btn = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: btn}
}

// gsBodyPoint returns a point over modal-body that no control covers.
func gsBodyPoint(t *testing.T, h *mouse.Handler) (int, int) {
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

// gsModalPlugin builds a Git plugin with files staged and a modal view mode
// open, then renders a frame so the query sees real geometry. The sidebar
// cursor sits mid-list, so the pane underneath would report movable.
func gsModalPlugin(t *testing.T, height int, open func(p *Plugin)) *Plugin {
	t.Helper()
	p := New()
	p.ctx = &plugin.Context{WorkDir: t.TempDir(), Epoch: 1}
	p.activateRepo(p.ctx.WorkDir)
	p.width, p.height, p.sidebarWidth = 120, height, 40
	for i := range 20 {
		p.tree.Modified = append(p.tree.Modified, &FileEntry{
			Path:   string(rune('a'+i%26)) + ".go",
			Status: StatusModified,
		})
	}
	p.cursor = 5
	open(p)
	p.View(p.width, p.height)
	return p
}

// gitModalFamilies covers every modal view mode Git routes mouse input to.
func gitModalFamilies() map[string]func(p *Plugin) {
	return map[string]func(p *Plugin){
		"commit": func(p *Plugin) {
			p.viewMode = ViewModeCommit
			p.initCommitTextarea()
		},
		"push menu": func(p *Plugin) { p.viewMode = ViewModePushMenu },
		"pull menu": func(p *Plugin) { p.viewMode = ViewModePullMenu },
		"pull conflict": func(p *Plugin) {
			p.viewMode = ViewModePullConflict
			p.pullConflictFiles = []string{"a.go", "b.go"}
		},
		"discard confirmation": func(p *Plugin) {
			p.viewMode = ViewModeConfirmDiscard
			p.discardFile = &FileEntry{Path: "a.go", Status: StatusModified}
		},
		"stash pop confirmation": func(p *Plugin) {
			p.viewMode = ViewModeConfirmStashPop
			p.stashPopItem = &Stash{Ref: "stash@{0}", Message: "wip"}
		},
		"error dialog": func(p *Plugin) {
			p.showErrorModal("Push Failed", errors.New("remote rejected"))
		},
	}
}

func TestGitModalWheelIsAbsorbedOverBodyBackdropAndControls(t *testing.T) {
	for name, open := range gitModalFamilies() {
		t.Run(name, func(t *testing.T) {
			p := gsModalPlugin(t, 40, open)
			x, y := gsBodyPoint(t, p.mouseHandler)
			for _, up := range []bool{true, false} {
				if !p.WheelAtBoundary(gsWheel(x, y, up)) {
					t.Errorf("body wheel (up=%v) was not bounded", up)
				}
				if !p.WheelAtBoundary(gsWheel(0, 0, up)) {
					t.Errorf("backdrop wheel (up=%v) was not absorbed", up)
				}
			}
			// The sidebar underneath is mid-cursor and would report movable.
			if p.cursor <= 0 || p.cursor >= p.totalSelectableItems()-1 {
				t.Fatalf("precondition: sidebar cursor %d should be mid-list", p.cursor)
			}
			if !p.WheelAtBoundary(gsWheel(5, 5, false)) {
				t.Error("an open modal must answer instead of the sidebar underneath")
			}
		})
	}
}

func TestGitShortConfirmationDropsItsWholeWheelStream(t *testing.T) {
	p := gsModalPlugin(t, 40, func(p *Plugin) {
		p.viewMode = ViewModeConfirmDiscard
		p.discardFile = &FileEntry{Path: "a.go", Status: StatusModified}
	})
	x, y := gsBodyPoint(t, p.mouseHandler)
	for range 100 {
		if !p.WheelAtBoundary(gsWheel(x, y, false)) {
			t.Fatal("a short confirmation must drop its whole wheel stream")
		}
	}
}

func TestGitErrorDialogLongBodyTopMiddleBottom(t *testing.T) {
	p := gsModalPlugin(t, 16, func(p *Plugin) {
		p.showErrorModal("Push Failed", errors.New(strings.Repeat("remote rejected the push\n", 60)))
	})
	x, y := gsBodyPoint(t, p.mouseHandler)
	if p.errorModal == nil {
		t.Fatal("expected a rendered error modal")
	}
	if !p.WheelAtBoundary(gsWheel(x, y, true)) {
		t.Fatal("expected bounded at the top of a long error body")
	}
	if p.WheelAtBoundary(gsWheel(x, y, false)) {
		t.Fatal("expected movable downward at the top of a long error body")
	}

	p.errorModal.ScrollBy(3)
	p.View(p.width, p.height)
	x, y = gsBodyPoint(t, p.mouseHandler)
	if p.WheelAtBoundary(gsWheel(x, y, true)) || p.WheelAtBoundary(gsWheel(x, y, false)) {
		t.Fatal("expected movable in both directions mid-body")
	}

	p.errorModal.ScrollToBottom()
	p.View(p.width, p.height)
	x, y = gsBodyPoint(t, p.mouseHandler)
	if !p.WheelAtBoundary(gsWheel(x, y, false)) {
		t.Fatal("expected bounded at the bottom of a long error body")
	}
	if p.WheelAtBoundary(gsWheel(x, y, true)) {
		t.Fatal("reverse event after the boundary must pass")
	}
}

func TestGitErrorDialogUnknownUntilRebuilt(t *testing.T) {
	p := gsModalPlugin(t, 40, func(p *Plugin) {
		p.showErrorModal("Push Failed", errors.New("remote rejected"))
	})
	x, y := gsBodyPoint(t, p.mouseHandler)
	if !p.WheelAtBoundary(gsWheel(x, y, false)) {
		t.Fatal("precondition: bounded before invalidation")
	}
	p.errorModal.Invalidate()
	if p.WheelAtBoundary(gsWheel(x, y, false)) {
		t.Fatal("expected unknown (false) until the next render rebuilds the bounds")
	}
	p.View(p.width, p.height)
	if !p.WheelAtBoundary(gsWheel(x, y, false)) {
		t.Fatal("expected an exact answer again after re-rendering")
	}
}

func TestGitBranchPickerCursorBounds(t *testing.T) {
	branches := make([]*Branch, 8)
	for i := range branches {
		branches[i] = &Branch{Name: string(rune('a' + i))}
	}
	tests := []struct {
		name    string
		cursor  int
		count   int
		up      bool
		want    bool
		wheelLR bool
	}{
		{name: "top up", cursor: 0, count: 8, up: true, want: true},
		{name: "top down", cursor: 0, count: 8},
		{name: "middle up", cursor: 4, count: 8, up: true},
		{name: "middle down", cursor: 4, count: 8},
		{name: "bottom down", cursor: 7, count: 8, want: true},
		{name: "bottom up (reverse)", cursor: 7, count: 8, up: true},
		{name: "no branches", cursor: 0, count: 0, want: true},
		{name: "horizontal wheel is unknown", cursor: 0, count: 8, up: true, wheelLR: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := gsModalPlugin(t, 40, func(p *Plugin) {
				p.viewMode = ViewModeBranchPicker
				p.branches = branches[:tt.count]
				p.branchCursor = tt.cursor
			})
			msg := gsWheel(60, 10, tt.up)
			if tt.wheelLR {
				msg = tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelLeft}
			}
			// The picker moves its cursor wherever the pointer is, so the
			// backdrop and the modal answer alike.
			if got := p.WheelAtBoundary(msg); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGitFilterOverlaysLeavePanesAnswering(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setup func(p *Plugin)
	}{
		{name: "history search", setup: func(p *Plugin) { p.historySearchMode = true }},
		{name: "path filter", setup: func(p *Plugin) { p.pathFilterMode = true }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := gsModalPlugin(t, 40, func(p *Plugin) {
				p.viewMode = ViewModeStatus
				tt.setup(p)
			})
			p.mouseHandler.HitMap.AddRect(regionSidebar, 0, 0, 42, 40, nil)
			p.cursor = 0
			if !p.WheelAtBoundary(gsWheel(5, 5, true)) {
				t.Fatal("the sidebar top must stay bounded behind a filter overlay")
			}
			if p.WheelAtBoundary(gsWheel(5, 5, false)) {
				t.Fatal("the sidebar must stay movable downward behind a filter overlay")
			}
		})
	}
}
