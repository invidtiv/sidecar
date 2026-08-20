package gitstatus

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
)

func safeDiffCapabilityPlugin(t *testing.T) (*Plugin, *FileEntry, *FileEntry) {
	t.Helper()
	root := t.TempDir()
	staged := &FileEntry{Path: "same.go", Status: StatusModified, Staged: true}
	unstaged := &FileEntry{Path: "same.go", Status: StatusModified, Unstaged: true}
	parsed, err := ParseUnifiedDiff("--- a/same.go\n+++ b/same.go\n@@ -1 +1,2 @@\n-old\n+internal/app/model.go:388\n+td-459d3a\n")
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.ctx = &plugin.Context{WorkDir: root + "/nested", ProjectRoot: root}
	p.activateRepo(root)
	p.tree.Staged = []*FileEntry{staged}
	p.tree.Modified = []*FileEntry{unstaged}
	p.cursor = 1
	p.selectedDiffFile = unstaged.Path
	p.diffPaneParsedDiff = parsed
	p.diffPaneViewMode = DiffViewUnified
	p.viewMode = ViewModeStatus
	return p, staged, unstaged
}

func TestGitContentLinkSurfaceMatchesRenderedDiffWithoutMutatingDuplicateSelection(t *testing.T) {
	p, staged, unstaged := safeDiffCapabilityPlugin(t)
	frame := p.View(160, 30)
	surfaces := p.ContentLinkSurfaces()
	if len(surfaces) != 1 {
		t.Fatalf("surfaces = %+v, want one passive diff surface", surfaces)
	}
	surface := surfaces[0]
	if surface.WorkDir != p.repoRoot || surface.WorkDir == p.ctx.WorkDir {
		t.Fatalf("surface workdir = %q, repo root = %q, initial cwd = %q", surface.WorkDir, p.repoRoot, p.ctx.WorkDir)
	}
	if !surface.ReadOnly || !surface.Kinds.Allows(contentlink.KindFile) || !surface.Kinds.Allows(contentlink.KindDiff) {
		t.Fatalf("surface contract = %+v", surface)
	}
	cut := cutGitSurface(frame, surface.Rect)
	for _, want := range []string{"internal/app/model.go:388", "td-459d3a"} {
		if !strings.Contains(cut, want) {
			t.Fatalf("declared diff surface omitted %q:\n%s", want, cut)
		}
	}
	if strings.Contains(cut, "Staged") || strings.Contains(cut, "Unstaged") {
		t.Fatalf("declared diff surface included interactive sidebar rows:\n%s", cut)
	}

	beforeOperation := p.activeOperation
	p.SetPaneFocus(gitDiffFocusID)
	p.handleMouseClick(mouse.MouseAction{Region: &mouse.Region{ID: regionDiffPane}, X: surface.Rect.X, Y: surface.Rect.Y})
	if p.cursor != 1 || p.tree.Staged[0] != staged || p.tree.Modified[0] != unstaged || !staged.Staged || !unstaged.Unstaged {
		t.Fatalf("passive focus/click changed duplicate-path selection: cursor=%d staged=%+v unstaged=%+v", p.cursor, staged, unstaged)
	}
	if p.selectedDiffFile != "same.go" || p.activeOperation != beforeOperation {
		t.Fatalf("passive focus/click changed Git operation state: file=%q operation=%v", p.selectedDiffFile, p.activeOperation)
	}
}

func TestGitContentLinkSurfaceExcludesMinimapAndCommitRows(t *testing.T) {
	t.Run("minimap", func(t *testing.T) {
		p, _, _ := safeDiffCapabilityPlugin(t)
		p.diffPaneViewMode = DiffViewFullFile
		p.diffPaneFullFileDiff = &FullFileDiff{OldFile: "same.go", NewFile: "same.go"}
		for i := 1; i <= 100; i++ {
			p.diffPaneFullFileDiff.Lines = append(p.diffPaneFullFileDiff.Lines, FullFileLine{
				OldLineNo: i, NewLineNo: i, OldText: "old", NewText: "new", Type: LineContext,
			})
		}
		p.View(160, 30)
		surface := p.ContentLinkSurfaces()[0]
		var minimap *mouse.Region
		for _, region := range p.mouseHandler.HitMap.Regions() {
			if region.ID == regionMinimap {
				copy := region
				minimap = &copy
				break
			}
		}
		if minimap == nil {
			t.Fatal("full-file fixture did not render a minimap")
		}
		if rectsIntersect(surface.Rect, minimap.Rect) {
			t.Fatalf("content surface %+v overlaps minimap %+v", surface.Rect, minimap.Rect)
		}
	})

	t.Run("commit file rows", func(t *testing.T) {
		p, _, _ := safeDiffCapabilityPlugin(t)
		commit := &Commit{
			Hash: "0123456789012345678901234567890123456789", ShortHash: "0123456",
			Author: "A", Date: time.Now(), Subject: "See td-459d3a",
			Body:  "Follow internal/app/model.go:388\nSecond line\nThird line\nFourth line",
			Files: []CommitFile{{Path: "must-not-scan/row.go:77", Status: StatusModified}},
		}
		p.recentCommits = []*Commit{commit}
		p.cursor = len(p.tree.AllEntries())
		p.previewCommit = commit
		frame := p.View(160, 30)
		surfaces := p.ContentLinkSurfaces()
		if len(surfaces) != 1 || surfaces[0].ID != "git-commit-description" {
			t.Fatalf("commit surfaces = %+v", surfaces)
		}
		cut := cutGitSurface(frame, surfaces[0].Rect)
		if !strings.Contains(cut, "td-459d3a") || !strings.Contains(cut, "internal/app/model.go:388") {
			t.Fatalf("commit description surface missed subject/body:\n%s", cut)
		}
		if strings.Contains(cut, "must-not-scan") || strings.Contains(cut, "Files (1)") || strings.Contains(cut, "full message") {
			t.Fatalf("commit description surface included interactive file rows:\n%s", cut)
		}
	})
}

func TestGitContentLinkSurfaceOptsOutOfUnsafeStates(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*Plugin)
	}{
		{"commit form", func(p *Plugin) { p.viewMode = ViewModeCommit }},
		{"push menu", func(p *Plugin) { p.viewMode = ViewModePushMenu }},
		{"pull menu", func(p *Plugin) { p.viewMode = ViewModePullMenu }},
		{"pull conflict", func(p *Plugin) { p.viewMode = ViewModePullConflict }},
		{"discard confirm", func(p *Plugin) { p.viewMode = ViewModeConfirmDiscard }},
		{"branch picker", func(p *Plugin) { p.viewMode = ViewModeBranchPicker }},
		{"stash confirm", func(p *Plugin) { p.viewMode = ViewModeConfirmStashPop }},
		{"error", func(p *Plugin) { p.viewMode = ViewModeError }},
		{"history search", func(p *Plugin) { p.historySearchMode = true }},
		{"path filter", func(p *Plugin) { p.pathFilterMode = true }},
		{"history filter", func(p *Plugin) { p.historyFilterActive = true }},
		{"loading diff", func(p *Plugin) { p.diffPaneParsedDiff = nil }},
		{"binary diff", func(p *Plugin) { p.diffPaneParsedDiff = &ParsedDiff{Binary: true} }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, _, _ := safeDiffCapabilityPlugin(t)
			p.View(160, 30)
			tc.apply(p)
			if got := p.ContentLinkSurfaces(); len(got) != 0 {
				t.Fatalf("unsafe state exposed surfaces: %+v", got)
			}
		})
	}
}

func TestGitPaneFocusProviderProjectsExistingFocus(t *testing.T) {
	p, staged, unstaged := safeDiffCapabilityPlugin(t)
	p.View(160, 30)
	stops := p.PaneFocusStops()
	if len(stops) != 2 || stops[0].ID != gitSidebarFocusID || stops[1].ID != gitDiffFocusID {
		t.Fatalf("focus stops = %+v", stops)
	}
	p.SetPaneFocusActive(false)
	if p.innerPaneFocusActive() {
		t.Fatal("inactive outer focus did not mute Git pane chrome")
	}
	p.SetPaneFocus(gitDiffFocusID)
	if p.activePane != PaneDiff || p.PaneFocus() != gitDiffFocusID {
		t.Fatalf("diff focus projection = pane %v id %q", p.activePane, p.PaneFocus())
	}
	p.SetPaneFocus(gitSidebarFocusID)
	if p.activePane != PaneSidebar || p.cursor != 1 || p.tree.Staged[0] != staged || p.tree.Modified[0] != unstaged {
		t.Fatal("focus projection changed neighboring or duplicate-path selection")
	}
	p.sidebarVisible = false
	if stops := p.PaneFocusStops(); len(stops) != 1 || stops[0].ID != gitDiffFocusID {
		t.Fatalf("collapsed sidebar stops = %+v", stops)
	}
	p.viewMode = ViewModeDiff
	if stops := p.PaneFocusStops(); len(stops) != 0 {
		t.Fatalf("full-screen diff exposed app-owned focus stops: %+v", stops)
	}
}

func TestDuplicatePathPreviewSwitchesBetweenStagedAndUnstagedIdentity(t *testing.T) {
	p, _, _ := safeDiffCapabilityPlugin(t)
	p.cursor = 0
	if cmd := p.autoLoadDiff(); cmd == nil || !p.selectedDiffStaged {
		t.Fatalf("staged duplicate did not start its own preview: cmd=%v staged=%v", cmd != nil, p.selectedDiffStaged)
	}
	p.cursor = 1
	if cmd := p.autoLoadDiff(); cmd == nil || p.selectedDiffStaged {
		t.Fatalf("unstaged duplicate did not start its own preview: cmd=%v staged=%v", cmd != nil, p.selectedDiffStaged)
	}
}

func TestHeldFullFileResultCannotCrossDuplicatePathStagingSide(t *testing.T) {
	p, _, _ := safeDiffCapabilityPlugin(t)
	p.diffPaneViewMode = DiffViewFullFile
	p.selectedDiffStaged = true
	p.inlineFullFileRequestID = 7
	p.diffPaneFullFileDiff = &FullFileDiff{Lines: []FullFileLine{{NewText: "staged"}}}
	p.cursor = 1 // same.go on the unstaged side
	if cmd := p.autoLoadDiff(); cmd == nil {
		t.Fatal("switching staging side did not start a new inline preview")
	}
	if p.inlineFullFileRequestID != 0 || p.diffPaneFullFileDiff != nil {
		t.Fatalf("side switch retained full-file request/content: request=%d content=%+v", p.inlineFullFileRequestID, p.diffPaneFullFileDiff)
	}

	updated, _ := p.Update(FullFileDiffLoadedMsg{
		Epoch: 0, RequestID: 7, File: "same.go", Staged: true, ForInline: true,
		OldContent: "old", NewContent: "staged",
	})
	p = updated.(*Plugin)
	if p.diffPaneFullFileDiff != nil {
		t.Fatalf("held staged full-file result applied under unstaged selection: %+v", p.diffPaneFullFileDiff)
	}
}

func cutGitSurface(frame string, rect mouse.Rect) string {
	lines := strings.Split(frame, "\n")
	cut := make([]string, 0, rect.H)
	for y := rect.Y; y < rect.Y+rect.H && y < len(lines); y++ {
		cut = append(cut, ansi.Strip(ansi.Cut(lines[y], rect.X, rect.X+rect.W)))
	}
	return strings.Join(cut, "\n")
}

func rectsIntersect(a, b mouse.Rect) bool {
	return a.X < b.X+b.W && b.X < a.X+a.W && a.Y < b.Y+b.H && b.Y < a.Y+a.H
}
