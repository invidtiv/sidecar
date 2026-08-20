package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/inlineedit"
	"github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
)

func appDeckEditFixture(t *testing.T) (*Model, *appContentDeck, int) {
	t.Helper()
	root := t.TempDir()
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "files"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(160, 30)
	if cmd := m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}); cmd == nil {
		t.Fatal("document open returned no load command")
	}
	m.renderContent(160, 30)
	h := m.currentContentDeck()
	leafID := h.deck.Leaf(panelayout.Document)
	h.deck.FocusLeaf(leafID)
	h.syncInnerFocus()
	t.Cleanup(h.releaseAppContentDocumentEdit)
	return m, h, leafID
}

func TestAppContentEditHostUsesExactRenderedDocumentBody(t *testing.T) {
	_, h, leafID := appDeckEditFixture(t)
	e := h.appContentDocumentEdit(true)
	e.leafID = leafID
	inner, ok := e.innerBox()
	if !ok {
		t.Fatal("focused document has no inner box")
	}
	width, height := e.editor().Viewport()
	if width != inner.W || height != inner.H-paneframe.HeaderRows {
		t.Fatalf("viewport = %dx%d, want rendered body %dx%d", width, height, inner.W, inner.H-paneframe.HeaderRows)
	}
	x, y, ok := e.editor().Host.EditorOrigin()
	if !ok || x != inner.X || y != inner.Y+paneframe.HeaderRows {
		t.Fatalf("origin = (%d,%d,%v), want (%d,%d,true)", x, y, ok, inner.X, inner.Y+paneframe.HeaderRows)
	}
	if !appContentEditPointInBody(e.editor(), x+width-1, y+height-1) || appContentEditPointInBody(e.editor(), x, y-1) {
		t.Fatal("editor body hit test disagrees with rendered geometry")
	}
}

func TestAppContentEditFeatureDisabledRefusesWithoutState(t *testing.T) {
	m, h, _ := appDeckEditFixture(t)
	cfg := config.Default()
	cfg.Features.Flags = map[string]bool{features.PluginContentPanes.Name: true, features.TmuxInlineEdit.Name: false}
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	cmd := m.enterAppContentDocumentEdit()
	if cmd == nil {
		t.Fatal("disabled edit returned no explanatory toast")
	}
	toast, ok := cmd().(msg.ToastMsg)
	if !ok || !strings.Contains(toast.Message, "disabled") {
		t.Fatalf("disabled edit result = %#v", toast)
	}
	if e := h.appContentDocumentEdit(false); e != nil && e.editing() {
		t.Fatal("disabled edit created a live session")
	}
}

func TestAppContentEditStartedAndExitedLifecycleWithoutTmux(t *testing.T) {
	m, h, leafID := appDeckEditFixture(t)
	cfg := config.Default()
	cfg.Features.Flags = map[string]bool{features.PluginContentPanes.Name: true, features.TmuxInlineEdit.Name: true}
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	start := m.enterAppContentDocumentEdit()
	if start == nil {
		t.Fatal("enabled edit returned no start command")
	}
	e := h.appContentDocumentEdit(true)
	session := e.editor()
	if e.leafID != leafID || session.Activation != 1 || session.Active {
		t.Fatalf("entry state leaf=%d activation=%d active=%v", e.leafID, session.Activation, session.Active)
	}
	activation := session.Activation
	epoch := h.deck.Context().Epoch

	cmd, handled := m.applyAppContentEditStarted(inlineedit.StartedMsg{
		Surface: h.appContentEditSurface(), LeafID: leafID, Path: "README.md",
		Activation: activation, Epoch: epoch,
	})
	if !handled || !session.Active || session.Path != "README.md" {
		t.Fatalf("started handled=%v active=%v path=%q", handled, session.Active, session.Path)
	}
	_ = cmd // Opening the tty is deliberately not executed by this model test.
	if !h.appContentDocumentEditing() || !m.appContentDocumentEditContext() {
		t.Fatal("started editor did not own the focused document context")
	}
	if frame, ok := h.renderAppContentDocumentEdit(leafID, paneframe.Size{Width: 50, Height: 10}); !ok || !strings.Contains(frame, "Editing: README.md") {
		t.Fatalf("editor render = %q ok=%v", frame, ok)
	}

	_, handled = m.applyAppContentEditExited(inlineedit.ExitedMsg{
		Surface: h.appContentEditSurface(), LeafID: leafID, Path: "README.md",
		Activation: activation, Epoch: epoch,
	})
	if !handled || session.Active || h.appContentDocumentEditing() {
		t.Fatalf("exit handled=%v active=%v focused-edit=%v", handled, session.Active, h.appContentDocumentEditing())
	}
}

func TestAppContentEditIgnoresStaleExitAndReleaseClearsHostState(t *testing.T) {
	m, h, leafID := appDeckEditFixture(t)
	e := h.appContentDocumentEdit(true)
	e.leafID = leafID
	session := e.editor()
	session.Active = true
	session.Path = "README.md"
	session.Activation = 2
	epoch := h.deck.Context().Epoch

	if _, handled := m.applyAppContentEditExited(inlineedit.ExitedMsg{
		Surface: h.appContentEditSurface(), LeafID: leafID, Path: "README.md",
		Activation: 1, Epoch: epoch,
	}); !handled || !session.Active {
		t.Fatalf("stale exit handled=%v active=%v", handled, session.Active)
	}

	h.releaseAppContentDocumentEdit()
	if session.Active || h.appContentDocumentEdit(false) != nil {
		t.Fatalf("release active=%v state=%#v", session.Active, h.appContentDocumentEdit(false))
	}
}

func TestAppContentEditConfirmationChangesRealSessionState(t *testing.T) {
	m, h, leafID := appDeckEditFixture(t)
	e := h.appContentDocumentEdit(true)
	e.leafID = leafID
	session := e.editor()
	session.Active = true
	session.Path = "README.md"
	session.ShowExitConfirm = true

	if _, handled := m.handleAppContentEditKey(tea.KeyPressMsg{Code: tea.KeyDown}); !handled || session.ConfirmSelection != 1 {
		t.Fatalf("confirmation key handled=%v selection=%d", handled, session.ConfirmSelection)
	}
	if _, handled := m.handleAppContentEditKey(tea.KeyPressMsg{Code: tea.KeyEscape}); !handled || session.ShowExitConfirm {
		t.Fatalf("cancel handled=%v still-open=%v", handled, session.ShowExitConfirm)
	}
}
