package notes

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestInternalNoteNavigationVerifiesThenSelectsStableID(t *testing.T) {
	store := openTestStore(t)
	first, err := store.Create("first title", "first body")
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.Create("old title", "[Checklist](sidecar://note/nt-4jdj4e)")
	if err != nil {
		t.Fatal(err)
	}
	renamed := *target
	renamed.Title = "renamed title"
	if err := store.Update(&renamed); err != nil {
		t.Fatal(err)
	}

	p := navigationTestPlugin(store, "/project")
	p.notes = []Note{*first}
	p.editorNote = &p.notes[0]
	p.editorTextarea.SetValue(first.Content)
	p.previewLines = []string{first.Content}
	drainNotesCmd(t, p, mustNoteNavigationCmd(t, p, app.NavigateToNoteMsg{ID: target.ID, ProjectRoot: "/project"}))

	if p.editorNote == nil || p.editorNote.ID != target.ID || p.editorNote.Title != "renamed title" {
		t.Fatalf("navigated note = %+v", p.editorNote)
	}
	if p.activePane != PaneEditor || !p.previewMode {
		t.Fatalf("navigation pane=%v preview=%v", p.activePane, p.previewMode)
	}
}

func TestInternalNoteNavigationFailuresNeverMoveTheUser(t *testing.T) {
	store := openTestStore(t)
	current, err := store.Create("current", "body")
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := store.Create("deleted", "gone")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Delete(deleted.ID); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		req  app.NavigateToNoteMsg
	}{
		{name: "missing", req: app.NavigateToNoteMsg{ID: "nt-ffffff", ProjectRoot: "/project"}},
		{name: "deleted", req: app.NavigateToNoteMsg{ID: deleted.ID, ProjectRoot: "/project"}},
		{name: "foreign", req: app.NavigateToNoteMsg{ID: current.ID, ProjectRoot: "/other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := navigationTestPlugin(store, "/project")
			p.notes = []Note{*current}
			p.editorNote = &p.notes[0]
			p.activePane = PaneList
			before := p.editorNote.ID
			_, cmd := p.Update(tc.req)
			if tc.name == "foreign" {
				if cmd != nil {
					t.Fatal("foreign request scheduled work")
				}
			} else {
				drainNotesCmd(t, p, cmd)
			}
			if p.editorNote == nil || p.editorNote.ID != before || p.activePane != PaneList {
				t.Fatalf("failure moved Notes: note=%+v pane=%v", p.editorNote, p.activePane)
			}
		})
	}
}

func TestInternalNoteNavigationPreservesAnOptimisticCreate(t *testing.T) {
	store := openTestStore(t)
	target, err := store.Create("target", "body")
	if err != nil {
		t.Fatal(err)
	}
	controlled := newControlledMutationStore(store)
	p := navigationTestPlugin(controlled, "/project")
	p.notes = []Note{*target}
	p.editorNote = &p.notes[0]

	create := p.beginOptimisticCreate("new note", "new body")
	createdResult := runCommandAsync(create)
	<-controlled.createStarted
	tempID := p.mutation.tempID

	drainNotesCmd(t, p, mustNoteNavigationCmd(t, p, app.NavigateToNoteMsg{ID: target.ID, ProjectRoot: "/project"}))
	if p.editorNote == nil || p.editorNote.ID != target.ID || p.getSelectedNote() == nil || p.getSelectedNote().ID != target.ID {
		t.Fatalf("navigation selection = editor %+v selected %+v", p.editorNote, p.getSelectedNote())
	}
	if p.noteByID(tempID) == nil {
		t.Fatalf("navigation dropped pending create %q from %+v", tempID, p.notes)
	}

	close(controlled.createRelease)
	created := (<-createdResult).(NoteSavedMsg)
	_, followup := p.Update(created)
	if p.noteByID(created.Note.ID) == nil || p.editorNote == nil || p.editorNote.ID != target.ID {
		t.Fatalf("create completion lost canonical row or target selection: notes=%+v editor=%+v", p.notes, p.editorNote)
	}
	applyCommandResults(t, p, followup)
}

func TestNotesCapabilitiesExposeExactRenderedPreviewOnly(t *testing.T) {
	store := openTestStore(t)
	p := navigationTestPlugin(store, "/project")
	note := Note{ID: "nt-4jdj4e", Title: "title", Content: "[Release checklist](sidecar://note/nt-4jdj4e)"}
	p.notes = []Note{note}
	p.editorNote = &p.notes[0]
	p.previewLines = strings.Split(note.Content, "\n")
	p.previewMode = true
	p.markdownView = true
	frame := p.View(100, 12)

	stops := p.PaneFocusStops()
	if len(stops) != 2 || stops[0].ID != "list" || stops[1].ID != "note" {
		t.Fatalf("focus stops = %+v", stops)
	}
	p.SetPaneFocus("note")
	if p.PaneFocus() != "note" || p.activePane != PaneEditor {
		t.Fatalf("note focus = %q pane=%v", p.PaneFocus(), p.activePane)
	}
	p.SetPaneFocusActive(false)
	if p.innerPaneFocusActive() {
		t.Fatal("outer focus did not mute Notes inner chrome")
	}

	surfaces := p.ContentLinkSurfaces()
	if len(surfaces) != 1 || !surfaces[0].ReadOnly || surfaces[0].ID != "note" {
		t.Fatalf("surfaces = %+v", surfaces)
	}
	// The note preview is internal/markdown output, the same trust domain as
	// Files' rendered preview and a docview document pane.
	if !surfaces[0].RendererOwned {
		t.Error("note preview surface is not marked renderer-owned")
	}
	layout := p.editorLayout()
	want := mouse.Rect{X: p.listWidth + dividerWidth + 2 + layout.leftMargin, Y: 1 + layout.contentRow, W: layout.wrapColumn, H: len(p.viewSurface.Lines)}
	if surfaces[0].Rect != want {
		t.Fatalf("preview rect = %+v, want %+v", surfaces[0].Rect, want)
	}
	lines := strings.Split(frame, "\n")
	var body []string
	for row := range surfaces[0].Rect.H {
		y := surfaces[0].Rect.Y + row
		body = append(body, ansi.Cut(lines[y], surfaces[0].Rect.X, surfaces[0].Rect.X+surfaces[0].Rect.W))
	}
	scanned := contentlink.ScanFrame(strings.Join(body, "\n"), contentlink.FrameOptions{
		InternalNamespaces: map[string]contentlink.URIOptions{"note": {ValidateID: func(id string) bool { return id == note.ID }}},
	})
	if len(scanned.Spans) == 0 || scanned.Spans[0].Ref() != (contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: note.ID}) {
		t.Fatalf("rendered Markdown body = %q, internal spans = %+v", strings.Join(body, "\n"), scanned.Spans)
	}
}

func TestNotesContentLinksExcludeInteractiveAndOverlayStates(t *testing.T) {
	tests := []struct {
		name  string
		apply func(*Plugin)
	}{
		{name: "built-in edit", apply: func(p *Plugin) { p.previewMode = false }},
		{name: "search", apply: func(p *Plugin) { p.searchMode = true }},
		{name: "in-note search", apply: func(p *Plugin) { p.noteSearchMode = true }},
		{name: "task modal", apply: func(p *Plugin) { p.showTaskModal = true }},
		{name: "delete modal", apply: func(p *Plugin) { p.showDeleteModal = true }},
		{name: "info modal", apply: func(p *Plugin) { p.showInfoModal = true }},
		{name: "setup modal", apply: func(p *Plugin) { p.showSetupModal = true }},
		{name: "inline tmux", apply: func(p *Plugin) { p.edit.Active = true }},
		{name: "inline exit confirm", apply: func(p *Plugin) { p.edit.ShowExitConfirm = true }},
		{name: "loading", apply: func(p *Plugin) { p.loading = true }},
		{name: "error", apply: func(p *Plugin) { p.loadErr = io.ErrUnexpectedEOF }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := openTestStore(t)
			p := navigationTestPlugin(store, "/project")
			note := Note{ID: "nt-4jdj4e", Content: "sidecar://note/nt-4jdj4e"}
			p.notes, p.editorNote, p.previewLines = []Note{note}, &note, []string{note.Content}
			p.width, p.height, p.listWidth = 100, 12, 29
			tc.apply(p)
			if got := p.ContentLinkSurfaces(); got != nil {
				t.Fatalf("unsafe state exposed links: %+v", got)
			}
		})
	}
}

func TestPreviewClickOnContentLinkDoesNotEnterEdit(t *testing.T) {
	click := func(t *testing.T, content, token string) (*Plugin, bool, bool) {
		t.Helper()
		store := openTestStore(t)
		p := navigationTestPlugin(store, t.TempDir())
		note := Note{ID: "nt-4jdj4e", Title: "links", Content: content}
		p.notes = []Note{note}
		p.editorNote = &p.notes[0]
		p.previewLines = strings.Split(note.Content, "\n")
		p.previewMode = true
		p.markdownView = false
		_ = p.View(100, 12)
		p.registerMouseRegions()

		layout := p.editorLayout()
		y := 1 + layout.contentRow
		contentX := p.listWidth + dividerWidth + 2 + layout.leftMargin
		plain := ansi.Strip(p.viewSurface.Lines[0])
		at := strings.Index(plain, token)
		if at < 0 {
			t.Fatalf("%q not in preview %q", token, plain)
		}
		x := contentX + at + 1
		hit := p.previewContentLinkAt(x, y)
		_, _ = p.handleMouseClick(mouse.MouseAction{
			X: x, Y: y,
			Region: &mouse.Region{ID: regionEditorLine, Data: 0},
		})
		return p, hit, p.previewMode
	}

	t.Run("issue", func(t *testing.T) {
		p, hit, preview := click(t, "See td-7be1ec and README.md in the tree.", "td-7be1ec")
		if !hit || !preview {
			t.Fatalf("td link hit=%v preview=%v, want skip-edit", hit, preview)
		}
		_ = p
	})
	t.Run("unresolved-filename-enters-edit", func(t *testing.T) {
		p, hit, preview := click(t, "See td-7be1ec and README.md in the tree.", "README.md")
		if hit || preview {
			t.Fatalf("missing README.md hit=%v preview=%v, want enter-edit", hit, preview)
		}
		p.previewMode = true
		layout := p.editorLayout()
		y := 1 + layout.contentRow
		contentX := p.listWidth + dividerWidth + 2 + layout.leftMargin
		plain := ansi.Strip(p.viewSurface.Lines[0])
		at := strings.Index(plain, "README.md")
		_, _ = p.handleMouseClick(mouse.MouseAction{
			X: contentX + at + 1, Y: y, Alt: true,
			Region: &mouse.Region{ID: regionEditorLine, Data: 0},
		})
		if p.previewMode {
			t.Fatal("alt+click on missing README.md did not enter edit")
		}
	})
	t.Run("hex-lookalike-enters-edit", func(t *testing.T) {
		_, hit, preview := click(t, "commit abcdefa is not a link", "abcdefa")
		if hit || preview {
			t.Fatalf("hex lookalike hit=%v preview=%v, want enter-edit", hit, preview)
		}
	})
	t.Run("plain-text-enters-edit", func(t *testing.T) {
		_, hit, preview := click(t, "See td-7be1ec and README.md in the tree.", "See ")
		if hit || preview {
			t.Fatalf("plain text hit=%v preview=%v, want enter-edit", hit, preview)
		}
	})
	t.Run("internal-note-uri", func(t *testing.T) {
		_, hit, preview := click(t, "Open sidecar://note/nt-4jdj4e next", "sidecar://note/nt-4jdj4e")
		if !hit || !preview {
			t.Fatalf("internal note uri hit=%v preview=%v, want skip-edit", hit, preview)
		}
	})
	t.Run("http-url", func(t *testing.T) {
		_, hit, preview := click(t, "See https://example.com/notes later", "https://example.com/notes")
		if !hit || !preview {
			t.Fatalf("http url hit=%v preview=%v, want skip-edit", hit, preview)
		}
	})
}

func navigationTestPlugin(store noteStore, root string) *Plugin {
	p := New()
	p.ctx = &plugin.Context{WorkDir: root, ProjectRoot: root, Epoch: 7, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p.store = store
	p.width, p.height, p.listWidth = 100, 24, 30
	p.editorTextarea = textarea.New()
	p.previewMode = true
	return p
}

func mustNoteNavigationCmd(t *testing.T, p *Plugin, req app.NavigateToNoteMsg) tea.Cmd {
	t.Helper()
	_, cmd := p.Update(req)
	if cmd == nil {
		t.Fatal("note navigation returned no command")
	}
	return cmd
}
