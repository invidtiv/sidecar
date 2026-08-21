package workspacediff

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
)

func commitRootView() *View {
	v := &View{
		State:  LoadStateReady,
		Focus:  FocusCommitFiles,
		Target: Target{Kind: TargetCommit, A: "0fff4518"},
		CommitDetail: &CommitDetail{
			Hash: "0fff4518", ShortHash: "0fff451", Subject: "Session copy ring",
			Files: []CommitFile{{Path: "internal/clip/clip.go", Status: "A"}},
		},
	}
	v.SetSize(80, 20)
	return v
}

// The first row of a commit-root tab is a title, not the "←" control: the
// renderer draws "Commit <hash>" there and registers no back target. The hit
// map has to agree, or clicking the pane's own header is a click on a control
// that is not drawn.
func TestCommitRootTabRegistersNoBackTarget(t *testing.T) {
	v := commitRootView()
	box := mouse.Rect{X: 0, Y: 0, W: 40, H: 20}
	for _, hit := range v.commitFileHits(box) {
		if hit.ID == RegionCommitBack {
			t.Fatalf("commit-root tab registered a back target: %+v", hit)
		}
	}

	list := &View{
		State: LoadStateReady, Focus: FocusCommitFiles,
		Target:       Target{Kind: TargetWorkingTree},
		CommitDetail: v.CommitDetail,
	}
	list.SetSize(80, 20)
	var found bool
	for _, hit := range list.commitFileHits(box) {
		if hit.ID == RegionCommitBack {
			found = true
		}
	}
	if !found {
		t.Fatal("a commit reached from the list lost its back target")
	}
}

// Even if a stale region survives a frame, back must not empty a commit-root
// tab: there is no list to return to, so the reset would strand the pane on
// "Loading commit files…" with nothing loading.
func TestBackOnACommitRootTabKeepsItsFiles(t *testing.T) {
	v := commitRootView()
	if cmd := v.HandleClick(RegionCommitBack, nil); cmd != nil {
		t.Fatal("back on a commit-root tab started a load")
	}
	if v.CommitDetail == nil {
		t.Fatal("back discarded the commit-root tab's file list")
	}
	if got := v.renderCommitFileList(40, 20, 0, 0, RenderOpts{}); strings.Contains(got, "Loading commit files") {
		t.Fatalf("commit-root tab is stuck loading after back: %q", got)
	}
}
