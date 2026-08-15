package workspace

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
)

func wsWheel(x, y int, up bool) tea.MouseWheelMsg {
	btn := tea.MouseWheelDown
	if up {
		btn = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: btn}
}

// wsBodyPoint returns a point over modal-body that no control covers.
func wsBodyPoint(t *testing.T, h *mouse.Handler) (int, int) {
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

// wsModalPlugin builds a Workspaces plugin with one modal family open and the
// modal rendered, so the query sees real geometry. The sidebar underneath holds
// several rows with a mid-list selection, so it would report movable.
func wsModalPlugin(t *testing.T, height int, open func(p *Plugin) *modal.Modal) (*Plugin, *modal.Modal) {
	t.Helper()
	p := &Plugin{
		viewMode:     ViewModeList,
		mouseHandler: mouse.NewHandler(),
		width:        120,
		height:       height,
		sidebarWidth: 40,
		ctx:          &plugin.Context{WorkDir: t.TempDir(), Epoch: 1},
	}
	p.selection.Clear()
	for i := range 10 {
		p.worktrees = append(p.worktrees, &Worktree{
			Key:  string(rune('a' + i)),
			Name: string(rune('a' + i)),
			Path: "/tmp/" + string(rune('a'+i)),
		})
	}
	p.selectedIdx = 5
	m := open(p)
	if m == nil {
		t.Fatal("modal was not built")
	}
	m.Render(p.width, p.height, p.mouseHandler)
	return p, m
}

// workspaceModalFamilies covers every modal Workspaces routes mouse input to.
func workspaceModalFamilies() map[string]func(p *Plugin) *modal.Modal {
	wt := func() *Worktree { return &Worktree{Key: "w", Name: "feature", Path: "/tmp/feature", Branch: "feature"} }
	return map[string]func(p *Plugin) *modal.Modal{
		"create": func(p *Plugin) *modal.Modal {
			p.initCreateModalBase()
			p.ensureCreateModal()
			return p.createModal
		},
		"create operation": func(p *Plugin) *modal.Modal {
			p.initCreateModalBase()
			p.createPlan = &CreateOperationPlan{}
			p.ensureCreateOperationModal()
			return p.createOperationModal
		},
		"task link": func(p *Plugin) *modal.Modal {
			p.viewMode = ViewModeTaskLink
			p.linkingWorktree = wt()
			p.taskSearchInput = textinput.New()
			p.ensureTaskLinkModal()
			return p.taskLinkModal
		},
		"rename shell": func(p *Plugin) *modal.Modal {
			p.viewMode = ViewModeRenameShell
			p.renameShellSession = &ShellSession{Name: "Shell 1", TmuxName: "sh-1"}
			p.renameShellInput = textinput.New()
			p.ensureRenameShellModal()
			return p.renameShellModal
		},
		"rename worktree": func(p *Plugin) *modal.Modal {
			p.openRenameWorktree(wt())
			p.ensureRenameWorktreeModal()
			return p.renameWorktreeModal
		},
		"delete worktree": func(p *Plugin) *modal.Modal {
			p.viewMode = ViewModeConfirmDelete
			p.deleteConfirmWorktree = wt()
			p.ensureConfirmDeleteModal()
			return p.deleteConfirmModal
		},
		"delete shell": func(p *Plugin) *modal.Modal {
			p.viewMode = ViewModeConfirmDeleteShell
			p.deleteConfirmShell = &ShellSession{Name: "Shell 1", TmuxName: "sh-1"}
			p.ensureConfirmDeleteShellModal()
			return p.deleteShellModal
		},
		"type selector": func(p *Plugin) *modal.Modal {
			p.viewMode = ViewModeTypeSelector
			p.typeSelectorNameInput = textinput.New()
			p.ensureTypeSelectorModal()
			return p.typeSelectorModal
		},
		"agent choice": func(p *Plugin) *modal.Modal {
			p.viewMode = ViewModeAgentChoice
			p.agentChoiceWorktree = wt()
			p.ensureAgentChoiceModal()
			return p.agentChoiceModal
		},
		"agent config": func(p *Plugin) *modal.Modal {
			p.viewMode = ViewModeAgentConfig
			p.agentConfigWorktree = wt()
			p.agentConfigAgentInput = textinput.New()
			p.ensureAgentConfigModal()
			return p.agentConfigModal
		},
		"fetch PR": func(p *Plugin) *modal.Modal {
			p.viewMode = ViewModeFetchPR
			p.ensureFetchPRModal()
			return p.fetchPRModal
		},
		"merge": func(p *Plugin) *modal.Modal {
			p.viewMode = ViewModeMerge
			p.mergeState = &MergeWorkflowState{Worktree: wt()}
			p.ensureMergeModal()
			return p.mergeModal
		},
		"commit for merge": func(p *Plugin) *modal.Modal {
			p.viewMode = ViewModeCommitForMerge
			p.mergeCommitState = &MergeCommitState{Worktree: wt()}
			p.mergeCommitMessageInput = textinput.New()
			p.ensureCommitForMergeModal()
			return p.commitForMergeModal
		},
	}
}

func TestWorkspaceModalWheelIsAnsweredByTheModal(t *testing.T) {
	for name, open := range workspaceModalFamilies() {
		t.Run(name, func(t *testing.T) {
			p, m := wsModalPlugin(t, 40, open)
			x, y := wsBodyPoint(t, p.mouseHandler)
			for _, up := range []bool{true, false} {
				// A modal that fits its screen absorbs the whole stream.
				if !p.WheelAtBoundary(wsWheel(0, 0, up)) {
					t.Errorf("backdrop wheel (up=%v) was not absorbed", up)
				}
				bodyBounded := m.WheelAtBoundary(wsWheel(x, y, up), p.mouseHandler)
				if got := p.WheelAtBoundary(wsWheel(x, y, up)); got != bodyBounded {
					t.Errorf("body wheel (up=%v) = %v, want the modal's own answer %v", up, got, bodyBounded)
				}
			}
			// The sidebar underneath is mid-selection and would report movable,
			// but the modal owns the answer.
			if !p.WheelAtBoundary(wsWheel(5, 5, false)) {
				t.Error("an open modal must answer instead of the sidebar underneath")
			}
		})
	}
}

func TestWorkspaceDeleteConfirmationDropsItsWholeWheelStream(t *testing.T) {
	p, _ := wsModalPlugin(t, 40, workspaceModalFamilies()["delete shell"])
	x, y := wsBodyPoint(t, p.mouseHandler)
	for range 100 {
		if !p.WheelAtBoundary(wsWheel(x, y, false)) {
			t.Fatal("a short confirmation must drop its whole wheel stream")
		}
	}
}

func TestWorkspaceModalLongBodyTopMiddleBottom(t *testing.T) {
	// A short screen forces the merge modal's body to overflow.
	p, m := wsModalPlugin(t, 12, func(p *Plugin) *modal.Modal {
		p.viewMode = ViewModeMerge
		p.mergeState = &MergeWorkflowState{
			Worktree:    &Worktree{Key: "w", Name: "feature", Path: "/tmp/feature", Branch: "feature"},
			ErrorTitle:  "Direct Merge Failed",
			ErrorDetail: strings.Repeat("merge failed on a long path\n", 40),
		}
		p.ensureMergeModal()
		return p.mergeModal
	})
	x, y := wsBodyPoint(t, p.mouseHandler)
	if !p.WheelAtBoundary(wsWheel(x, y, true)) {
		t.Fatal("expected bounded at the top of a long body")
	}
	if p.WheelAtBoundary(wsWheel(x, y, false)) {
		t.Fatal("expected movable downward at the top of a long body")
	}

	m.ScrollBy(3)
	m.Render(p.width, p.height, p.mouseHandler)
	x, y = wsBodyPoint(t, p.mouseHandler)
	if p.WheelAtBoundary(wsWheel(x, y, true)) || p.WheelAtBoundary(wsWheel(x, y, false)) {
		t.Fatal("expected movable in both directions mid-body")
	}

	m.ScrollToBottom()
	m.Render(p.width, p.height, p.mouseHandler)
	x, y = wsBodyPoint(t, p.mouseHandler)
	if !p.WheelAtBoundary(wsWheel(x, y, false)) {
		t.Fatal("expected bounded at the bottom of a long body")
	}
	if p.WheelAtBoundary(wsWheel(x, y, true)) {
		t.Fatal("reverse event after the boundary must pass")
	}
}

func TestWorkspaceModalUnknownUntilRebuiltAfterAsyncContent(t *testing.T) {
	p, m := wsModalPlugin(t, 40, workspaceModalFamilies()["fetch PR"])
	x, y := wsBodyPoint(t, p.mouseHandler)
	if !p.WheelAtBoundary(wsWheel(x, y, false)) {
		t.Fatal("precondition: bounded before invalidation")
	}
	// The PR list arrives asynchronously and rewrites the modal's content.
	m.Invalidate()
	if p.WheelAtBoundary(wsWheel(x, y, false)) {
		t.Fatal("expected unknown (false) until the next render rebuilds the bounds")
	}
	m.Render(p.width, p.height, p.mouseHandler)
	if !p.WheelAtBoundary(wsWheel(x, y, false)) {
		t.Fatal("expected an exact answer again after re-rendering")
	}
}

func TestWorkspaceDocInfoModalAnswers(t *testing.T) {
	p := &Plugin{
		viewMode:     ViewModeList,
		mouseHandler: mouse.NewHandler(),
		width:        120,
		height:       40,
		sidebarWidth: 40,
	}
	p.selection.Clear()
	p.docInfo = &docview.Info{Root: "/tmp", Path: "/tmp/README.md"}
	p.docInfo.Render(p.width, p.height, p.mouseHandler)

	x, y := wsBodyPoint(t, p.mouseHandler)
	for _, up := range []bool{true, false} {
		if !p.WheelAtBoundary(wsWheel(x, y, up)) {
			t.Errorf("doc info body wheel (up=%v) was not bounded", up)
		}
		if !p.WheelAtBoundary(wsWheel(0, 0, up)) {
			t.Errorf("doc info backdrop wheel (up=%v) was not absorbed", up)
		}
	}
}

func TestWorkspaceBusyCreateStepAbsorbsWheel(t *testing.T) {
	p := &Plugin{
		viewMode:       ViewModeCreate,
		createBusyStep: "Creating worktree",
		mouseHandler:   mouse.NewHandler(),
		width:          120,
		height:         40,
	}
	p.selection.Clear()
	for _, up := range []bool{true, false} {
		if !p.WheelAtBoundary(wsWheel(30, 10, up)) {
			t.Errorf("busy create step wheel (up=%v) was not absorbed", up)
		}
	}
}

func TestWorkspaceFilePickerStaysUnknown(t *testing.T) {
	p := &Plugin{
		viewMode:     ViewModeFilePicker,
		mouseHandler: mouse.NewHandler(),
		width:        120,
		height:       40,
	}
	p.selection.Clear()
	if p.WheelAtBoundary(wsWheel(30, 10, false)) {
		t.Fatal("the file picker has no declared bounds and must stay unknown")
	}
}

func TestWorkspaceModalHorizontalWheelIsUnknown(t *testing.T) {
	p, _ := wsModalPlugin(t, 40, workspaceModalFamilies()["delete shell"])
	x, y := wsBodyPoint(t, p.mouseHandler)
	if p.WheelAtBoundary(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelLeft}) {
		t.Fatal("horizontal wheel must stay unknown")
	}
}
