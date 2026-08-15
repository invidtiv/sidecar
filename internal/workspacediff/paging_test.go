package workspacediff

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
)

func TestPageDownUsesSetSizeHeight(t *testing.T) {
	lines := make([]string, 80)
	for i := range lines {
		lines[i] = "line"
	}
	raw := strings.Join(lines, "\n")
	v := &View{
		State: LoadStateReady,
		Scope: ScopeWorkingTree,
		Focus: FocusDiff,
		Raw:   raw,
		Files: []File{{Path: "a.go", Raw: raw}},
	}
	v.SetSize(80, 20)
	if v.PageSize() != 10 {
		t.Fatalf("page size = %d, want 10 (height/2)", v.PageSize())
	}
	_, handled := v.HandleKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if !handled {
		t.Fatal("ctrl+d was not handled")
	}
	if v.DiffScroll != 10 {
		t.Fatalf("page-down moved %d, want 10 (SetSize height/2), not plugin-height-6", v.DiffScroll)
	}
	v.height = 40
	v.DiffScroll = 0
	_, _ = v.HandleKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if v.DiffScroll == 10 {
		t.Fatal("page step stayed 10 after height changed; SetSize height must drive paging")
	}
}

func TestApplySnapshotDoesNotMutateScrollDuringLaterRender(t *testing.T) {
	v := &View{Scope: ScopeWorkingTree, Cursor: 0, Scroll: 3}
	v.Snapshot = &Snapshot{
		State:       LoadStateReady,
		WorkingTree: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -0,0 +1 @@\n+hi\n",
	}
	v.SetSize(80, 20)
	v.ApplySnapshot()
	scroll := v.Scroll
	_ = v.Render(80, 20, RenderOpts{})
	if v.Scroll != scroll {
		t.Fatalf("Render mutated Scroll %d → %d", scroll, v.Scroll)
	}
}

func TestStaleSnapshotMsgIsDropped(t *testing.T) {
	v := &View{WorkspaceID: "wt-a", Epoch: 2, Target: WorkingTreeTarget()}
	v.State = LoadStateReady
	cmd := v.ApplySnapshotMsg(SnapshotMsg{
		Epoch: 1, WorkspaceID: "wt-a", Identity: IdentityWorkingTree,
		Snapshot: &Snapshot{State: LoadStateReady, WorkingTree: "stale"},
	}, "/tmp", "wt-a")
	if cmd != nil {
		t.Fatal("stale epoch applied")
	}
	if v.Snapshot != nil {
		t.Fatal("stale snapshot replaced the view")
	}
	cmd = v.ApplySnapshotMsg(SnapshotMsg{
		Epoch: 2, WorkspaceID: "other", Identity: IdentityWorkingTree,
		Snapshot: &Snapshot{State: LoadStateReady},
	}, "/tmp", "wt-a")
	if cmd != nil || v.Snapshot != nil {
		t.Fatal("wrong workspace id applied")
	}
}

func TestSetSizeDoesNotPersistClampedListWidth(t *testing.T) {
	v := &View{}
	v.SetListWidth(80)
	v.SetSize(90, 20)
	if v.ListWidth() != 80 {
		t.Fatalf("SetSize persisted clamped width %d, want stored 80", v.ListWidth())
	}
	if got := v.EffectiveListWidth(90); got != 60 {
		t.Fatalf("narrow display width = %d, want 60 (90-30)", got)
	}
	if got := v.EffectiveListWidth(160); got != 80 {
		t.Fatalf("grown display width = %d, want restored 80", got)
	}
}

func TestPaintFileChangesBodyAcrossViewModes(t *testing.T) {
	v := &View{
		State: LoadStateReady,
		Focus: FocusDiff,
		Files: []File{{Path: "a.go", Raw: "diff --git a/a.go b/a.go\n"}},
	}
	v.SetSize(80, 12)
	opts := RenderOpts{
		PaintFile: func(name, raw string, mode ViewMode, w, h, scroll, horiz int) string {
			return "PAINT-" + modeLabel(mode)
		},
	}
	strip := func(s string) string {
		lines := strings.Split(s, "\n")
		if len(lines) > 2 {
			return strings.Join(lines[2:], "\n")
		}
		return s
	}
	first := strip(v.Render(80, 12, opts))
	_ = v.CycleViewMode()
	second := strip(v.Render(80, 12, opts))
	if first == second {
		t.Fatalf("CycleViewMode left painted body identical after stripping header:\n%s", first)
	}
}

func modeLabel(mode ViewMode) string {
	switch mode {
	case ViewSideBySide:
		return "split"
	case ViewFullFile:
		return "full"
	default:
		return "unified"
	}
}

func TestCycleScopeClearsPaintedFile(t *testing.T) {
	cleared := 0
	v := &View{
		Scope: ScopeWorkingTree,
		Snapshot: &Snapshot{
			State:       LoadStateReady,
			WorkingTree: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -0,0 +1 @@\n+hi\n",
			Commits:     []CommitInfo{{Hash: "aaa1111", Subject: "first"}},
		},
		ClearPaintedFile: func() { cleared++ },
	}
	v.ApplySnapshot()
	if v.FileCount() == 0 {
		t.Fatal("premise: working-tree file so cursor can sit on file 0")
	}
	_ = v.CycleScope()
	if cleared == 0 {
		t.Fatal("CycleScope did not call ClearPaintedFile")
	}
}

func TestCommitFileKeysClearPaintedFile(t *testing.T) {
	v := &View{
		Focus: FocusCommitFiles,
		CommitDetail: &CommitDetail{
			Hash:  "aaa1111",
			Files: []CommitFile{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}},
		},
	}
	for _, key := range []struct {
		name string
		msg  tea.KeyPressMsg
		prep func(*View)
	}{
		{"j", tea.KeyPressMsg{Code: 'j', Text: "j"}, func(v *View) { v.CommitFileCursor = 0 }},
		{"k", tea.KeyPressMsg{Code: 'k', Text: "k"}, func(v *View) { v.CommitFileCursor = 1 }},
		{"g", tea.KeyPressMsg{Code: 'g', Text: "g"}, func(v *View) { v.CommitFileCursor = 2 }},
		{"G", tea.KeyPressMsg{Code: 'G', Text: "G"}, func(v *View) { v.CommitFileCursor = 0 }},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}, func(v *View) { v.CommitFileCursor = 0 }},
		{"h", tea.KeyPressMsg{Code: 'h', Text: "h"}, func(v *View) { v.CommitFileCursor = 0; v.Focus = FocusCommitFiles }},
	} {
		t.Run(key.name, func(t *testing.T) {
			cleared := 0
			v.ClearPaintedFile = func() { cleared++ }
			v.Focus = FocusCommitFiles
			v.CommitDetail = &CommitDetail{
				Hash:  "aaa1111",
				Files: []CommitFile{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}},
			}
			key.prep(v)
			_, handled := v.HandleKey(key.msg)
			if !handled {
				t.Fatalf("%s was not handled", key.name)
			}
			if cleared == 0 {
				t.Fatalf("%s on commit files did not call ClearPaintedFile", key.name)
			}
		})
	}
}

func TestDividerHitStaysInsideLeaf(t *testing.T) {
	v := &View{}
	v.SetSize(160, 24)
	leaf := mouse.Rect{X: 40, Y: 6, W: 160, H: 24}
	hit := v.DividerHit(leaf)
	if hit.W != 3 {
		t.Fatalf("hit width = %d, want 3", hit.W)
	}
	if hit.Y != leaf.Y || hit.H != leaf.H {
		t.Fatalf("hit %+v not in leaf height", hit)
	}
	if hit.X < leaf.X || hit.X+hit.W > leaf.X+leaf.W+2 {
		t.Fatalf("hit %+v not inside leaf %+v", hit, leaf)
	}
	v.SetListWidth(0)
	w := v.ApplyListWidthDelta(0, 160)
	if w < 20 || w > 160-30 {
		t.Fatalf("clamped width %d out of 20..130", w)
	}
}
