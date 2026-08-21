package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/panelayout"
)

func TestOpenNoteRefCreatesNoteLeafAndTab(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	surfaceRoot, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("no selected surface")
	}
	if cmd := p.openNotePaneForSurface(surfaceRoot, surface, "nt-abc123"); cmd == nil {
		t.Fatal("opening a note returned no command")
	}
	if p.contentDeck.Leaf(panelayout.Note) == 0 {
		t.Fatal("note leaf was not created")
	}
	items, active := p.contentDeck.Tabs(p.contentDeck.Leaf(panelayout.Note))
	if len(items) != 1 || active != 0 || items[0].Ref.Value != "nt-abc123" {
		t.Fatalf("note tabs = %#v active=%d", items, active)
	}
	if p.contentDeck.Leaf(panelayout.Note) != p.paneFocus {
		t.Fatalf("focus = %d, want note leaf", p.paneFocus)
	}
	note, leaf := p.activeNotePane()
	if note == nil || leaf == nil || note.tabs.Find("nt-abc123") < 0 {
		t.Fatalf("projected note pane missing: %#v %#v", note, leaf)
	}
}

func TestSecondNoteAddsTabOnSameLeaf(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	surfaceRoot, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("no selected surface")
	}
	_ = p.openNotePaneForSurface(surfaceRoot, surface, "nt-abc123")
	leaf := p.contentDeck.Leaf(panelayout.Note)
	if cmd := p.openNotePaneForSurface(surfaceRoot, surface, "nt-def456"); cmd == nil {
		t.Fatal("second note returned no command")
	}
	if p.contentDeck.Leaf(panelayout.Note) != leaf {
		t.Fatal("second note split a new leaf")
	}
	items, active := p.contentDeck.Tabs(leaf)
	if len(items) != 2 || active != 1 || items[1].Ref.Value != "nt-def456" {
		t.Fatalf("note tabs = %#v active=%d", items, active)
	}
}

func TestNotePanePersistRoundTrip(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	surfaceRoot, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("no selected surface")
	}
	_ = p.openNotePaneForSurface(surfaceRoot, surface, "nt-abc123")
	_ = p.openNotePaneForSurface(surfaceRoot, surface, "nt-def456")
	saved := p.encodePaneNode(p.paneRoot)
	if saved == nil {
		t.Fatal("encode produced no layout")
	}
	leaf := firstLayoutLeafOfKind(saved, contentKindNote)
	if leaf == nil || len(leaf.NoteTabs) != 2 || leaf.NoteTabs[0].Note != "nt-abc123" || leaf.NoteTabs[1].Note != "nt-def456" {
		t.Fatalf("encoded note leaf = %#v", leaf)
	}

	ctx := p.workspaceDeckContext(surfaceRoot, surface)
	restored := contentpanes.Decode(ctx, contentpanes.Config{}, contentpanes.State{Version: 1, Root: workspaceDeckNode(saved)})
	if restored.Leaf(panelayout.Note) == 0 {
		t.Fatal("decoded deck lost the note leaf")
	}
	items, active := restored.Tabs(restored.Leaf(panelayout.Note))
	if len(items) != 2 || active != 1 {
		t.Fatalf("restored tabs = %#v active=%d", items, active)
	}
	want := contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: "nt-abc123"}
	if items[0].Ref != want {
		t.Fatalf("first tab ref = %#v want %#v", items[0].Ref, want)
	}
}
