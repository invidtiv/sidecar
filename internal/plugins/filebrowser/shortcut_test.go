package filebrowser

import "testing"

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
