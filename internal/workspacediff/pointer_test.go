package workspacediff

import "testing"

func TestIsBodyRegionNamesTheInnerHits(t *testing.T) {
	for _, id := range []string{
		RegionFile, RegionCommit, RegionDiffPane, RegionMinimap,
		RegionCommitBack, RegionCommitFile, RegionCommitDiff,
		RegionPreviewFile, RegionFileListPane, RegionDivider,
	} {
		if !IsBodyRegion(id) {
			t.Errorf("IsBodyRegion(%q) = false, want true", id)
		}
	}
	if IsBodyRegion("global-preview-diff") {
		t.Fatal("catch-all leaf region is the host's, not an inner Diff hit")
	}
}

func TestHandleClickSelectsTheFileRow(t *testing.T) {
	v := &View{
		State: LoadStateReady,
		Files: []File{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}},
		Focus: FocusDiff,
	}
	if cmd := v.HandleClick(RegionFile, 2); cmd != nil {
		t.Fatal("selecting a working-tree file should not load")
	}
	if v.Cursor != 2 {
		t.Fatalf("cursor = %d, want 2", v.Cursor)
	}
	if v.Focus != FocusFileList {
		t.Fatalf("focus = %v, want file list", v.Focus)
	}
}

func TestHandleClickOnFileListPaneTakesListFocus(t *testing.T) {
	v := &View{Focus: FocusDiff, Files: []File{{Path: "a.go"}}}
	_ = v.HandleClick(RegionFileListPane, nil)
	if v.Focus != FocusFileList {
		t.Fatalf("focus = %v, want file list", v.Focus)
	}
}

func TestHandleDoubleClickOpensTheFileDiff(t *testing.T) {
	v := &View{
		State: LoadStateReady,
		Files: []File{{Path: "a.go"}, {Path: "b.go"}},
		Focus: FocusFileList,
	}
	_ = v.HandleDoubleClick(RegionFile, 1)
	if v.Cursor != 1 || v.Focus != FocusDiff {
		t.Fatalf("cursor=%d focus=%v, want cursor 1 in hunks", v.Cursor, v.Focus)
	}
}

func TestHandleWheelMovesTheFileListCursor(t *testing.T) {
	v := &View{
		State: LoadStateReady,
		Files: []File{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}},
	}
	_ = v.HandleWheel(RegionFile, 1)
	if v.Cursor != 1 {
		t.Fatalf("cursor = %d, want 1", v.Cursor)
	}
	_ = v.HandleWheel(RegionFile, -1)
	if v.Cursor != 0 {
		t.Fatalf("cursor = %d, want 0", v.Cursor)
	}
	if !v.WheelAtBoundary(RegionFile, -1) {
		t.Fatal("wheel-up at the first file was not bounded")
	}
}
