package filebrowser

import (
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
)

func TestBracesCycleFileTabs(t *testing.T) {
	p := &Plugin{
		tabs: []FileTab{
			{Path: "one.go", Loaded: true},
			{Path: "two.go", Loaded: true},
		},
		activeTab:   0,
		previewFile: "one.go",
	}

	_, _ = p.handleTreeKey("}")
	if p.activeTab != 1 {
		t.Fatalf("activeTab after } = %d, want 1", p.activeTab)
	}
	_, _ = p.handlePreviewKey("{")
	if p.activeTab != 0 {
		t.Fatalf("activeTab after { = %d, want 0", p.activeTab)
	}
}

func TestPreviewPathActionsCallViewer(t *testing.T) {
	dir := t.TempDir()
	p := &Plugin{
		ctx:         &plugin.Context{WorkDir: dir},
		previewFile: "readme.md",
		activePane:  PanePreview,
	}
	_, reveal := p.handlePreviewKey("ctrl+r")
	if reveal == nil {
		t.Fatal("ctrl+r should reveal via the shared viewer")
	}
	_, yank := p.handlePreviewKey("Y")
	if yank == nil {
		t.Fatal("Y should yank path via the shared viewer")
	}
	_, info := p.handlePreviewKey("I")
	if !p.infoMode || info == nil {
		t.Fatalf("I should open info via the shared viewer: mode=%v cmd=%v", p.infoMode, info != nil)
	}
}

func TestFileInfoModalBlocksGlobalKeys(t *testing.T) {
	if !(&Plugin{infoMode: true}).BlocksGlobalKeys() {
		t.Fatal("file info modal does not block global keys")
	}
}

func TestBlameOverlayBlocksGlobalKeys(t *testing.T) {
	if !(&Plugin{blameMode: true}).BlocksGlobalKeys() {
		t.Fatal("blame overlay does not block global keys")
	}
}
