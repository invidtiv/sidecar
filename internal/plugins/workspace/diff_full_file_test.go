package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/plugins/gitstatus"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// A pane-hosted Diff view has its own cursor. Reconciling a landed full-file
// load against the legacy p.diff alone dropped it, and the pane sat on
// "Loading full file..." for the rest of the session.
func TestFullFileLoadReachesAPaneHostedView(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("show-diff opened nothing")
	}
	diff, _ := p.activeDiffPane()
	if diff == nil || diff.view() == nil {
		t.Fatal("no Diff leaf")
	}
	view := diff.view()
	view.State = workspacediff.LoadStateReady
	view.Files = []workspacediff.File{
		{Path: "first.go", Raw: "diff --git a/first.go b/first.go\n"},
		{Path: "second.go", Raw: "diff --git a/second.go b/second.go\n"},
	}
	// The pane's cursor is on the second file; the legacy view's is not.
	view.Cursor = 1
	p.diff.Files = view.Files
	p.diff.Cursor = 0

	msg := FullFileDiffLoadedMsg{
		Epoch:         p.ctx.Epoch,
		WorkspaceName: view.WorkspaceID,
		Identity:      view.Target.Identity(),
		FilePath:      "second.go",
		OldContent:    "one\n",
		NewContent:    "one\ntwo\n",
	}
	p.Update(msg)
	if p.fullFileDiff == nil {
		t.Fatal("full-file load for the pane's file was dropped")
	}
	if p.fullFileKey != ":second.go" {
		t.Fatalf("painted slot key = %q, want :second.go", p.fullFileKey)
	}
}

// A load for a file no live view is showing is still dropped.
func TestFullFileLoadForAnUnrelatedFileIsDropped(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("show-diff opened nothing")
	}
	view := p.activeDiffView()
	view.State = workspacediff.LoadStateReady
	view.Files = []workspacediff.File{{Path: "first.go"}}

	p.Update(FullFileDiffLoadedMsg{
		Epoch:         p.ctx.Epoch,
		WorkspaceName: view.WorkspaceID,
		Identity:      view.Target.Identity(),
		FilePath:      "nobody-is-showing-this.go",
	})
	if p.fullFileDiff != nil {
		t.Fatal("a load nothing asked for was installed")
	}
}

// The painted slot is shared, so a view whose file is not in it must issue its
// own load rather than paint the other view's file.
func TestFullFileLoadIsIssuedWhenTheSlotHoldsAnotherFile(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	if cmd := p.showDiffCmd(); cmd == nil {
		t.Fatal("show-diff opened nothing")
	}
	view := p.activeDiffView()
	view.State = workspacediff.LoadStateReady
	view.Files = []workspacediff.File{{Path: "wanted.go"}}
	view.ViewMode = workspacediff.ViewFullFile
	p.attachDiffPaintTo(view)

	// Something else already owns the slot.
	p.fullFileDiff = &gitstatus.FullFileDiff{}
	p.fullFileKey = ":other.go"

	if cmd := p.loadFullFileForView(view); cmd == nil {
		t.Fatal("no load issued for a view whose file is not painted")
	}
	if p.paintedFileIsFor(view) {
		t.Fatal("the other view's file was accepted as this view's")
	}

	p.fullFileKey = ":wanted.go"
	if cmd := p.loadFullFileForView(view); cmd != nil {
		t.Fatal("a second load was issued for a file already painted")
	}
	if !p.paintedFileIsFor(view) {
		t.Fatal("this view's own painted file was rejected")
	}
}
