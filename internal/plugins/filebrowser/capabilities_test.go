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
	// Raw source rows are the file's own bytes; an OSC-8 sequence in them keeps
	// the terminal rule.
	if surface.RendererOwned {
		t.Error("raw source surface claimed to be renderer-owned")
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
		// Render mode itself is safe; render mode with nothing rendered yet is
		// not, because the rows on screen are still the raw fallback.
		{name: "render mode before rendering", mutate: func(p *Plugin) {
			p.previewFile = "notes.md"
			p.tabs[p.activeTab].Path = "notes.md"
			p.markdownRenderMode = true
			p.markdownRendered = nil
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

func TestContentLinkSurfaceScansRenderedMarkdown(t *testing.T) {
	p := renderedMarkdownContentLinkPreview("notes.md",
		[]string{"# Notes", "", "See td-22f35f."},
		[]string{"", "  \x1b[1mNotes\x1b[0m", "", "  See td-22f35f.", ""},
	)
	frame := p.View(100, 12)

	surfaces := p.ContentLinkSurfaces()
	if len(surfaces) != 1 {
		t.Fatalf("rendered markdown exported no surface: %+v", surfaces)
	}
	surface := surfaces[0]
	// The gutter is empty in render mode, so the scan rect is the panel's whole
	// content column: x = preview origin 30 + border 1 + padding 1, and width is
	// previewContentWidth (70 − 2 borders − 2 padding − 1 scrollbar). Glamour's
	// two-column document margin is visual indent drawn inside this box.
	if got, want := surface.Rect, (mouse.Rect{X: 32, Y: 3, W: 65, H: 5}); got != want {
		t.Fatalf("rendered surface rect = %+v, want %+v", got, want)
	}
	if surface.ID != "preview" || !surface.ReadOnly {
		t.Fatalf("rendered surface identity/read-only = %+v", surface)
	}
	// Only these rows were drawn by internal/markdown, so only these may let a
	// claiming provider reclassify an explicit hyperlink.
	if !surface.RendererOwned {
		t.Error("rendered Markdown surface is not marked renderer-owned")
	}
	for _, kind := range []contentlink.Kind{
		contentlink.KindFile, contentlink.KindIssue, contentlink.KindDiff,
		contentlink.KindResource, contentlink.KindURL, contentlink.KindInternal,
	} {
		if !surface.Kinds.Allows(kind) {
			t.Errorf("rendered surface does not allow %q", kind)
		}
	}

	// The exported rows must be the rows that were drawn, indent included.
	lines := strings.Split(frame, "\n")
	row := surface.Rect.Y + 3
	if row >= len(lines) {
		t.Fatalf("surface row %d is outside %d-row frame", row, len(lines))
	}
	got := strings.TrimRight(ansi.Strip(ansi.Cut(lines[row], surface.Rect.X, surface.Rect.X+surface.Rect.W)), " ")
	if got != "  See td-22f35f." {
		t.Fatalf("rendered row inside the scan rect = %q, want the drawn Glamour row", got)
	}
}

func TestContentLinkSurfaceHeightFollowsRenderedRowsNotSourceLines(t *testing.T) {
	longer := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		longer = append(longer, "  rendered row")
	}
	p := renderedMarkdownContentLinkPreview("notes.md", []string{"# One", "", "two"}, longer)
	p.View(100, 12)
	surfaces := p.ContentLinkSurfaces()
	if len(surfaces) != 1 {
		t.Fatalf("rendered markdown exported no surface: %+v", surfaces)
	}
	// Twelve rendered rows into an eight-row source area: the export is what the
	// frame could draw, never the three source lines.
	if got, want := surfaces[0].Rect.H, 8; got != want {
		t.Fatalf("surface height = %d, want %d rendered rows on screen", got, want)
	}

	shorter := []string{"  one rendered row", "  two"}
	q := renderedMarkdownContentLinkPreview("notes.md",
		[]string{"# One", "", "two", "three", "four", "five"}, shorter)
	q.View(100, 12)
	surfaces = q.ContentLinkSurfaces()
	if len(surfaces) != 1 {
		t.Fatalf("short rendered markdown exported no surface: %+v", surfaces)
	}
	if got, want := surfaces[0].Rect.H, len(shorter); got != want {
		t.Fatalf("short surface height = %d, want %d rendered rows", got, want)
	}
}

func renderedMarkdownContentLinkPreview(path string, source, rendered []string) *Plugin {
	p := loadedContentLinkPreview(path, source)
	p.markdownRenderMode = true
	p.markdownRendered = append([]string(nil), rendered...)
	return p
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
