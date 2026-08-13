package workspace

import (
	"github.com/marcus/sidecar/internal/plugins/gitstatus"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// asDiffView copies plugin Diff snapshot/cursor state into the shared model.
func (p *Plugin) asDiffView() *workspacediff.View {
	v := &workspacediff.View{
		Snapshot:    p.diffSnapshot,
		State:       p.diffState,
		Error:       p.diffError,
		Scope:       p.diffScope,
		Content:     p.diffContent,
		Raw:         p.diffRaw,
		Commits:     p.commitStatusList,
		Cursor:      p.diffTabCursor,
		Scroll:      p.diffTabScroll,
		DiffScroll:  p.diffTabDiffScroll,
		HorizScroll: p.diffTabHorizScroll,
		Focus:       p.diffTabFocus,
		ViewMode:    p.diffViewMode,
		ListWidth:   p.diffTabListWidth,
	}
	if v.Raw != "" && v.Scope != workspacediff.ScopeAggregate && v.Scope != workspacediff.ScopeCommits {
		v.Files = workspacediff.ParseFiles(v.Raw)
	}
	return v
}

func (p *Plugin) fromDiffView(v *workspacediff.View) {
	if v == nil {
		return
	}
	p.diffSnapshot = v.Snapshot
	p.diffState = v.State
	p.diffError = v.Error
	p.diffScope = v.Scope
	p.diffContent = v.Content
	p.diffRaw = v.Raw
	p.commitStatusList = v.Commits
	p.diffTabCursor = v.Cursor
	p.diffTabScroll = v.Scroll
	p.diffTabDiffScroll = v.DiffScroll
	p.diffTabHorizScroll = v.HorizScroll
	p.diffTabFocus = v.Focus
	p.diffViewMode = v.ViewMode
	p.diffTabListWidth = v.ListWidth
}

func (p *Plugin) applySharedDiffScope() {
	v := p.asDiffView()
	v.ApplySnapshot()
	p.fromDiffView(v)
	if p.diffRaw != "" && p.diffScope != DiffScopeAggregate && p.diffScope != DiffScopeCommits {
		p.multiFileDiff = gitstatus.ParseMultiFileDiff(p.diffRaw)
	} else {
		p.multiFileDiff = nil
	}
}
