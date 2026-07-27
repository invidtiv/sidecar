package filebrowser

import (
	"fmt"
	"testing"
)

func TestPendingNavigateLineIsAppliedAfterPreviewLoads(t *testing.T) {
	p := New()
	p.height = 20
	p.previewFile = "internal/foo.go"
	p.tabs = []FileTab{{Path: p.previewFile}}
	p.pendingNavigatePath = p.previewFile
	p.pendingNavigateLine = 50
	p.pendingNavigateGen = 7
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}

	updated, _ := p.Update(PreviewLoadedMsg{
		Path:               p.previewFile,
		NavigateGeneration: 7,
		Result: PreviewResult{
			Lines:            lines,
			HighlightedLines: lines,
		},
	})
	got := updated.(*Plugin)
	if got.previewScroll != 49 {
		t.Fatalf("preview scroll = %d, want 49 for requested line 50", got.previewScroll)
	}
	if got.pendingNavigateLine != 0 {
		t.Fatalf("pending navigate line was not cleared: %d", got.pendingNavigateLine)
	}
	if got.tabs[0].Scroll != 49 {
		t.Fatalf("active tab scroll = %d, want 49", got.tabs[0].Scroll)
	}
}

func TestPendingNavigateLineIgnoresDifferentPreviewGeneration(t *testing.T) {
	p := New()
	p.height = 20
	p.previewFile = "other.go"
	p.pendingNavigatePath = "target.go"
	p.pendingNavigateLine = 50
	p.pendingNavigateGen = 8
	lines := make([]string, 100)

	updated, _ := p.Update(PreviewLoadedMsg{
		Path:               p.previewFile,
		NavigateGeneration: 7,
		Result:             PreviewResult{Lines: lines, HighlightedLines: lines},
	})
	got := updated.(*Plugin)
	if got.previewScroll != 0 {
		t.Fatalf("unrelated preview applied pending line: %d", got.previewScroll)
	}
	if got.pendingNavigatePath != "target.go" || got.pendingNavigateLine != 50 {
		t.Fatalf("unrelated preview cleared pending target: %q:%d",
			got.pendingNavigatePath, got.pendingNavigateLine)
	}
}
