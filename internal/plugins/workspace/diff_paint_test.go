package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/plugins/gitstatus"
	"github.com/marcus/sidecar/internal/workspacediff"
)

const paintSampleDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1,3 +1,4 @@
 line one
-old line
+new line
 line three
+added
`

func TestCycleViewModeChangesPaintedBody(t *testing.T) {
	p := testDiffPlugin(t)
	p.attachDiffPaint()
	p.diff.State = workspacediff.LoadStateReady
	p.diff.Focus = DiffTabFocusDiff
	p.diff.ViewMode = DiffViewUnified
	p.diff.Files = []workspacediff.File{{Path: "foo.go", Raw: paintSampleDiff}}
	p.diff.SetSize(160, 20)

	opts := workspacediff.RenderOpts{PaintFile: p.paintDiffFile}
	strip := func(s string) string {
		plain := ansi.Strip(s)
		lines := strings.Split(plain, "\n")
		if len(lines) > 2 {
			return strings.Join(lines[2:], "\n")
		}
		return plain
	}

	unified := strip(p.diff.Render(160, 20, opts))
	if !strings.Contains(unified, "+new line") && !strings.Contains(unified, "new line") {
		t.Fatalf("unified paint missing added line:\n%s", unified)
	}

	cmd := p.diff.CycleViewMode()
	if p.diff.ViewMode != DiffViewSideBySide {
		t.Fatalf("mode after first cycle = %v, want side-by-side", p.diff.ViewMode)
	}
	if cmd != nil {
		t.Fatal("side-by-side cycle returned a load cmd")
	}
	split := strip(p.diff.Render(160, 20, opts))
	if unified == split {
		t.Fatalf("CycleViewMode left painted body identical after stripping [mode] header:\n%s", unified)
	}

	cmd = p.diff.CycleViewMode()
	if p.diff.ViewMode != DiffViewFullFile {
		t.Fatalf("mode after second cycle = %v, want full-file", p.diff.ViewMode)
	}
	if cmd == nil {
		t.Fatal("full-file cycle did not return the load cmd")
	}
	full := strip(p.diff.Render(160, 20, opts))
	if !strings.Contains(full, "Loading full file") {
		t.Fatalf("full-file paint before load = %q, want loading placeholder", full)
	}
}

func TestCycleScopeClearsHostFullFileDiff(t *testing.T) {
	p := testDiffPlugin(t)
	p.attachDiffPaint()
	p.diff.State = workspacediff.LoadStateReady
	p.diff.Scope = DiffScopeWorkingTree
	p.diff.ViewMode = DiffViewFullFile
	p.diff.Snapshot = &workspacediff.Snapshot{
		State:       workspacediff.LoadStateReady,
		WorkingTree: paintSampleDiff,
		Commits:     []workspacediff.CommitInfo{{Hash: "aaa1111", Subject: "first"}},
	}
	p.diff.ApplySnapshot()
	p.fullFileDiff = &gitstatus.FullFileDiff{}
	_ = p.diff.CycleScope()
	if p.fullFileDiff != nil {
		t.Fatal("CycleScope left fullFileDiff set; file 0 would still paint the previous file")
	}
}

func TestCommitFileMoveClearsHostFullFileDiff(t *testing.T) {
	p := testDiffPlugin(t)
	p.attachDiffPaint()
	p.diff.Focus = DiffTabFocusCommitFiles
	p.diff.ViewMode = DiffViewFullFile
	p.diff.CommitDetail = &workspacediff.CommitDetail{
		Hash:  "aaa1111",
		Files: []workspacediff.CommitFile{{Path: "a.go"}, {Path: "b.go"}},
	}
	p.fullFileDiff = &gitstatus.FullFileDiff{}
	_, handled := p.diff.HandleKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if !handled {
		t.Fatal("j on commit files was not handled")
	}
	if p.fullFileDiff != nil {
		t.Fatal("commit-file j left fullFileDiff set; right pane would still paint the previous file")
	}
}
