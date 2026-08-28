package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panemodal"
	"github.com/marcus/sidecar/internal/panesearch"
	"github.com/marcus/sidecar/internal/ui"
)

// appDeckSearch is the host-owned part of Find and Search for a document leaf.
// panesearch owns the actual finder/search behavior; the deck owns only which
// leaf opened it, where it is drawn, and how a selected file replaces or joins
// that leaf's tabs.
type appDeckSearch struct {
	leafID     int
	generation uint64
	mode       *panesearch.Mode
	caches     panesearch.Caches
	box        mouse.Rect
	regions    []mouse.Region
}

// appDeckSearchMsg keeps async finder/search traffic attached to the deck and
// leaf that issued it. The underlying messages are shared with Files and carry
// no host identity, so broadcasting them would let one pane consume another's
// scan or ripgrep result.
type appDeckSearchMsg struct {
	DeckKey    string
	LeafID     int
	Generation uint64
	Msg        tea.Msg
}

// appDeckInfoMsg gives asynchronous git metadata the same host identity as
// pane search traffic. A relative path alone is not unique across projects,
// and the unwrapped docview message is also consumed by Files' own info modal.
type appDeckInfoMsg struct {
	DeckKey string
	LeafID  int
	Msg     docview.GitInfoMsg
}

func appDeckInfoCmd(deckKey string, leafID int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := cmd().(docview.GitInfoMsg)
		if !ok {
			return nil
		}
		return appDeckInfoMsg{DeckKey: deckKey, LeafID: leafID, Msg: msg}
	}
}

func appDeckSearchCmd(deckKey string, leafID int, generation uint64, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if msg == nil {
			return nil
		}
		return appDeckSearchMsg{DeckKey: deckKey, LeafID: leafID, Generation: generation, Msg: msg}
	}
}

func (h *appContentDeck) appContentSearchActive() bool {
	return h != nil && h.search.mode != nil && h.deck != nil && h.search.leafID == h.deck.FocusedLeaf()
}

func (m Model) appContentSearchActive() bool {
	h := m.currentContentDeck()
	return h != nil && h.appContentSearchActive()
}

func (h *appContentDeck) openAppContentFinder() tea.Cmd {
	if h == nil || h.deck == nil {
		return nil
	}
	leaf := panelayout.Find(h.deck.Tree(), h.deck.FocusedLeaf())
	if leaf == nil || leaf.Kind != panelayout.Document {
		return nil
	}
	h.closeAppContentSearch()
	h.info, h.infoLeaf = nil, 0
	mode, scan := panesearch.NewFinder(&h.search.caches, h.workdir, h.deck.Context().Epoch)
	h.search.leafID, h.search.mode = leaf.ID, mode
	return appDeckSearchCmd(h.key, leaf.ID, h.search.generation, scan)
}

func (h *appContentDeck) openAppContentProjectSearch() tea.Cmd {
	if h == nil || h.deck == nil {
		return nil
	}
	leaf := panelayout.Find(h.deck.Tree(), h.deck.FocusedLeaf())
	if leaf == nil || leaf.Kind != panelayout.Document {
		return nil
	}
	h.closeAppContentSearch()
	h.info, h.infoLeaf = nil, 0
	h.search.leafID = leaf.ID
	h.search.mode = panesearch.NewProject(h.workdir, h.deck.Context().Epoch)
	if h.search.box.W > 0 && h.search.box.H > 0 {
		h.search.mode.SetSize(h.search.box.W, h.search.box.H)
	}
	return nil
}

func (h *appContentDeck) closeAppContentSearch() {
	if h == nil {
		return
	}
	if h.search.mode != nil {
		h.search.mode.Close()
	}
	// A mode's internal project-search run tokens begin at one. Advance a
	// host generation at every lifecycle boundary so a queued result from a
	// cancelled mode cannot collide with a reopened mode on the same leaf.
	h.search.generation++
	h.search.leafID = 0
	h.search.mode = nil
	h.search.box = mouse.Rect{}
	h.search.regions = nil
}

func (h *appContentDeck) openAppContentInfo(view *docview.Model, leafID int) tea.Cmd {
	if h == nil || view == nil {
		return nil
	}
	info, cmd := docview.OpenInfo(view.Root(), view.Title())
	h.info, h.infoLeaf = info, leafID
	return appDeckInfoCmd(h.key, leafID, cmd)
}

func (h *appContentDeck) applyAppContentInfoMsg(msg appDeckInfoMsg) {
	if h == nil || h.key != msg.DeckKey || h.info == nil || h.infoLeaf != msg.LeafID {
		return
	}
	h.info.ApplyGit(msg.Msg)
}

func (h *appContentDeck) releaseAppContentInputs() {
	if h == nil {
		return
	}
	h.releaseAppContentDocumentEdit()
	h.closeAppContentSearch()
	h.info, h.infoLeaf = nil, 0
}

func (h *appContentDeck) applyAppContentSearchMsg(msg appDeckSearchMsg) tea.Cmd {
	if !h.appContentSearchMsgCurrent(msg) {
		return nil
	}
	return appDeckSearchCmd(h.key, msg.LeafID, msg.Generation, h.search.mode.Update(msg.Msg))
}

func (h *appContentDeck) appContentSearchMsgCurrent(msg appDeckSearchMsg) bool {
	return h != nil && h.key == msg.DeckKey && h.search.mode != nil &&
		h.search.leafID == msg.LeafID && h.search.generation == msg.Generation
}

func (m *Model) handleAppContentSearchKey(key tea.KeyPressMsg) tea.Cmd {
	h := m.currentContentDeck()
	if h == nil || !h.appContentSearchActive() {
		return nil
	}
	out, cmd := h.search.mode.HandleKey(key)
	return m.applyAppContentSearchOutcome(h, out, cmd)
}

func (m *Model) applyAppContentSearchOutcome(h *appContentDeck, out panesearch.Outcome, cmd tea.Cmd) tea.Cmd {
	if h == nil || h.search.mode == nil {
		return nil
	}
	wrapper := appDeckSearchCmd(h.key, h.search.leafID, h.search.generation, cmd)
	switch {
	case out.Cancelled:
		h.closeAppContentSearch()
		return wrapper
	case out.Open && out.Path != "":
		h.closeAppContentSearch()
		ctx := contentpanes.SurfaceContext{Root: h.workdir, DiffRoot: h.workdir, Surface: h.pluginID, Epoch: h.deck.Context().Epoch}
		ref := contentlink.Ref{Kind: contentlink.KindFile, Value: out.Path, Line: out.Line}
		var opened contentpanes.Outcome
		if out.NewTab {
			opened = m.openAppContentOutcome(h, ref, "", nil)
		} else {
			opened = h.deck.ReplaceActive(ctx, ref)
			if opened.Accepted() {
				h.syncInnerFocus()
				m.persistAppContentDeck(h)
			}
		}
		return tea.Batch(wrapper, opened.Command)
	default:
		return wrapper
	}
}

func (m *Model) handleAppContentSearchMouse(msg tea.MouseMsg) (tea.Cmd, bool) {
	h := m.activeContentDeck()
	if h == nil || !h.appContentSearchActive() {
		return nil, false
	}
	pos := msg.Mouse()
	if !h.search.box.Contains(pos.X, pos.Y) {
		if click, ok := msg.(tea.MouseClickMsg); ok {
			h.closeAppContentSearch()
			// The search covers the body but deliberately leaves pane chrome
			// visible. Visible header controls must remain one-click controls,
			// not require a first click merely to dismiss the overlay.
			if click.Button == tea.MouseLeft {
				region := h.mouse.HitMap.Test(pos.X, pos.Y)
				if region != nil && (region.ID == appDeckCloseRegion || region.ID == appDeckTabRegion) {
					// Resolve the header first, exactly as the ordinary mouse
					// path does. Passing the search leaf here would close it
					// when the user clicked another pane's visible X.
					paneframe.FocusLeafAt(appDeckHost{h}, pos.X, pos.Y)
					h.syncInnerFocus()
					leaf := panelayout.Find(h.deck.Tree(), h.deck.FocusedLeaf())
					if leaf != nil {
						cmd := h.handlePassiveMouse(msg, leaf)
						m.persistAppContentDeck(h)
						return cmd, true
					}
				}
			}
		}
		return nil, true
	}
	out, cmd := h.search.mode.HandleMouse(msg, h.mouse)
	return m.applyAppContentSearchOutcome(h, out, cmd), true
}

// renderAppContentSearch composites the shared search surface over the
// document body while leaving its tab header visible.
func (h *appContentDeck) renderAppContentSearch(leafID int, background string, origin mouse.Rect, size paneframe.Size) string {
	if h == nil || h.search.mode == nil || h.search.leafID != leafID || size.Width <= 0 || size.Height <= 0 {
		return background
	}
	h.search.box = mouse.Rect{X: origin.X, Y: origin.Y, W: size.Width, H: size.Height}
	h.search.mode.SetSize(size.Width, size.Height)
	scratch := mouse.NewHandler()
	out := panemodal.RenderFunc(
		panemodal.Box{X: origin.X, Y: origin.Y, W: size.Width, H: size.Height},
		ui.FitBlock(background, size.Width, size.Height), scratch, h.search.mode.View,
	)
	h.search.regions = scratch.HitMap.Regions()
	return out
}

func (h *appContentDeck) registerAppContentSearchRegions() {
	if h == nil || h.search.mode == nil || h.mouse == nil {
		return
	}
	for _, region := range h.search.regions {
		h.mouse.HitMap.Add(region.ID, region.Rect, region.Data)
	}
}
