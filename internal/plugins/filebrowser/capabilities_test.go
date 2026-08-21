package filebrowser

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestPaneFocusProviderProjectsExistingVisiblePaneState(t *testing.T) {
	p := New()
	p.previewFile = "notes.txt"

	if got := p.PaneFocusStops(); len(got) != 2 || got[0].ID != "tree" || got[1].ID != "preview" {
		t.Fatalf("focus stops = %+v, want tree then preview", got)
	}
	if got := p.PaneFocus(); got != "tree" {
		t.Fatalf("initial pane focus = %q, want tree", got)
	}
	p.SetPaneFocus("preview")
	if p.activePane != PanePreview || p.PaneFocus() != "preview" {
		t.Fatalf("preview setter left pane=%v id=%q", p.activePane, p.PaneFocus())
	}

	p.treeVisible = false
	if got := p.PaneFocusStops(); len(got) != 1 || got[0].ID != "preview" {
		t.Fatalf("collapsed focus stops = %+v, want preview only", got)
	}
	p.SetPaneFocus("tree")
	if p.activePane != PanePreview {
		t.Fatal("setter focused a hidden tree pane")
	}
	p.SetPaneFocus("unknown")
	if p.activePane != PanePreview {
		t.Fatal("unknown focus ID changed existing focus")
	}
}

func TestPaneFocusActiveMutesOnlyAfterOuterHostOptsIn(t *testing.T) {
	p := New()
	if !p.innerPaneFocusActive() {
		t.Fatal("unmanaged Files focus should preserve historical active chrome")
	}
	p.SetPaneFocusActive(false)
	if p.innerPaneFocusActive() {
		t.Fatal("outer passive focus did not mute Files active chrome")
	}
	p.SetPaneFocusActive(true)
	if !p.innerPaneFocusActive() {
		t.Fatal("returning focus did not restore Files active chrome")
	}
}

func TestContentLinkSurfaceUsesRenderedWrappedPreviewGeometry(t *testing.T) {
	p := loadedContentLinkPreview("notes.txt", []string{
		strings.Repeat("x", 70),
		"td-22f35f",
	})
	p.previewWrapEnabled = true
	frame := p.View(100, 12)

	surfaces := p.ContentLinkSurfaces()
	if len(surfaces) != 1 {
		t.Fatalf("surfaces = %+v, want one preview surface", surfaces)
	}
	surface := surfaces[0]
	if surface.ID != "preview" || !surface.ReadOnly {
		t.Fatalf("surface identity/read-only = %+v", surface)
	}
	if surface.WorkDir != "/worktree" || surface.ProjectRoot != "/project" {
		t.Fatalf("surface roots = workdir %q project %q", surface.WorkDir, surface.ProjectRoot)
	}
	// width=100 gives tree=29, divider=1, preview=70. The source starts
	// after preview x=30, border=1, padding=1 and the five-cell gutter.
	// 1 column is reserved for the scrollbar.
	wantRect := (mouse.Rect{X: 37, Y: 3, W: 60, H: 3})
	if surface.Rect != wantRect {
		t.Fatalf("surface rect = %+v, want %+v", surface.Rect, wantRect)
	}
	frameLines := strings.Split(frame, "\n")
	var sourceRows []string
	for row := 0; row < surface.Rect.H; row++ {
		y := surface.Rect.Y + row
		if y >= len(frameLines) {
			t.Fatalf("surface row %d is outside %d-row frame", y, len(frameLines))
		}
		sourceRows = append(sourceRows, ansi.Strip(ansi.Cut(
			frameLines[y], surface.Rect.X, surface.Rect.X+surface.Rect.W,
		)))
	}
	if got := strings.TrimRight(sourceRows[0], " "); got != strings.Repeat("x", 60) {
		t.Fatalf("first wrapped source row = %q", got)
	}
	if got := strings.TrimRight(sourceRows[1], " "); got != strings.Repeat("x", 10) {
		t.Fatalf("second wrapped source row = %q", got)
	}
	if got := strings.TrimRight(sourceRows[2], " "); got != "td-22f35f" {
		t.Fatalf("third source row = %q", got)
	}
	for _, kind := range []contentlink.Kind{
		contentlink.KindFile, contentlink.KindIssue, contentlink.KindDiff,
		contentlink.KindResource, contentlink.KindURL, contentlink.KindInternal,
	} {
		if !surface.Kinds.Allows(kind) {
			t.Errorf("surface does not allow %q", kind)
		}
	}
}

func TestContentLinkSurfaceCollapsedTreeUsesFullPreviewGeometry(t *testing.T) {
	p := loadedContentLinkPreview("notes.txt", []string{"README.md"})
	p.treeVisible = false
	p.View(100, 12)

	surfaces := p.ContentLinkSurfaces()
	if len(surfaces) != 1 {
		t.Fatalf("surfaces = %+v, want one preview surface", surfaces)
	}
	if got, want := surfaces[0].Rect, (mouse.Rect{X: 7, Y: 3, W: 88, H: 1}); got != want {
		t.Fatalf("collapsed surface rect = %+v, want %+v", got, want)
	}
}

func TestContentLinkSurfaceExcludesVisibleTruncationNoticeRow(t *testing.T) {
	p := loadedContentLinkPreview("large.txt", []string{
		"one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
	})
	p.isTruncated = true
	p.View(100, 8) // Four source-area rows; the last is the truncation notice.

	surfaces := p.ContentLinkSurfaces()
	if len(surfaces) != 1 {
		t.Fatalf("surfaces = %+v, want one preview surface", surfaces)
	}
	if got, want := surfaces[0].Rect.H, 3; got != want {
		t.Fatalf("source surface height = %d, want %d above truncation notice", got, want)
	}
}

func TestContentLinkSurfaceOptsOutOfUnsafePreviewStates(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Plugin)
	}{
		{name: "loading", mutate: func(p *Plugin) { p.tabs[p.activeTab].Loaded = false }},
		{name: "empty", mutate: func(p *Plugin) { p.previewLines = nil; p.previewHighlighted = nil }},
		{name: "error", mutate: func(p *Plugin) { p.previewError = errors.New("no preview") }},
		{name: "binary", mutate: func(p *Plugin) { p.isBinary = true }},
		{name: "image", mutate: func(p *Plugin) { p.isImage = true }},
		{name: "inline edit", mutate: func(p *Plugin) { p.edit.Active = true }},
		{name: "tree search", mutate: func(p *Plugin) { p.searchMode = true }},
		{name: "content search", mutate: func(p *Plugin) { p.contentSearchMode = true }},
		{name: "quick open", mutate: func(p *Plugin) { p.quickOpenMode = true }},
		{name: "project search", mutate: func(p *Plugin) { p.projectSearchMode = true }},
		{name: "info modal", mutate: func(p *Plugin) { p.infoMode = true }},
		{name: "blame modal", mutate: func(p *Plugin) { p.blameMode = true }},
		{name: "file operation", mutate: func(p *Plugin) { p.fileOpMode = FileOpMove }},
		{name: "line jump", mutate: func(p *Plugin) { p.lineJumpMode = true }},
		{name: "rendered markdown", mutate: func(p *Plugin) {
			p.previewFile = "notes.md"
			p.tabs[p.activeTab].Path = "notes.md"
			p.markdownRenderMode = true
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := loadedContentLinkPreview("notes.txt", []string{"td-22f35f"})
			tt.mutate(p)
			// Unsafe modes own rendering dependencies (image renderer, modal tree,
			// editor session) this narrow capability fixture deliberately omits.
			// Geometry is already known; prove they opt out before any scan.
			p.width, p.height, p.previewWidth = 100, 12, 70
			if got := p.ContentLinkSurfaces(); got != nil {
				t.Fatalf("unsafe state exposed surfaces: %+v", got)
			}
		})
	}
}

func loadedContentLinkPreview(path string, lines []string) *Plugin {
	p := New()
	p.ctx = &plugin.Context{WorkDir: "/worktree", ProjectRoot: "/project"}
	p.previewFile = path
	p.previewLines = append([]string(nil), lines...)
	p.previewHighlighted = append([]string(nil), lines...)
	p.tabs = []FileTab{{Path: path, Loaded: true}}
	p.activeTab = 0
	return p
}
