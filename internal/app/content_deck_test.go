package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
)

type deckHostTestPlugin struct {
	nativeTestPlugin
	id, focus     string
	frame         string
	width, height int
	innerActive   bool
}

type queuedAppDeckTestMsg struct{}

func (p *deckHostTestPlugin) ID() string           { return p.id }
func (p *deckHostTestPlugin) Name() string         { return p.id }
func (p *deckHostTestPlugin) FocusContext() string { return "files" }
func (p *deckHostTestPlugin) View(width, height int) string {
	p.width, p.height = width, height
	return p.frame
}
func (p *deckHostTestPlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		p.width, p.height = size.Width, size.Height
	}
	p.seen = append(p.seen, msg)
	return p, nil
}
func (p *deckHostTestPlugin) PaneFocusStops() []plugin.PaneFocusStop {
	return []plugin.PaneFocusStop{{ID: "tree"}, {ID: "preview"}}
}
func (p *deckHostTestPlugin) PaneFocus() string { return p.focus }
func (p *deckHostTestPlugin) SetPaneFocus(id string) tea.Cmd {
	p.focus = id
	return nil
}
func (p *deckHostTestPlugin) SetPaneFocusActive(active bool) { p.innerActive = active }
func (p *deckHostTestPlugin) ContentLinkSurfaces() []contentlink.Surface {
	return []contentlink.Surface{{
		ID: "preview", Rect: mouse.Rect{W: len(p.frame), H: 1},
		WorkDir: "/tmp", ProjectRoot: "/tmp", ReadOnly: true,
		Kinds: contentlink.NewKindSet(contentlink.KindIssue, contentlink.KindFile, contentlink.KindDiff),
	}}
}

func appDeckTestModel(t *testing.T, root string, plugins ...*deckHostTestPlugin) *Model {
	t.Helper()
	cfg := config.Default()
	cfg.Features.Flags = map[string]bool{features.PluginContentPanes.Name: true}
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = state.InitWithDir(t.TempDir()) })
	reg := plugin.NewRegistry(&plugin.Context{WorkDir: root, ProjectRoot: root, Epoch: 9, Config: cfg})
	for _, p := range plugins {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	km := keymap.NewRegistry()
	keymap.RegisterDefaults(km)
	m := &Model{
		registry: reg, keymap: km, activePlugin: 0, contentDecks: make(map[string]*appContentDeck),
		ui: &UIState{WorkDir: root, ProjectRoot: root}, ready: true, applicationFocused: true,
		width: 200, height: 50, cfg: cfg,
	}
	plugins[0].SetFocused(true)
	return m
}

func TestAppContentDeckSizesPrimaryAndComposesOneFocusRing(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"README.md", "guide.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("# "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := &deckHostTestPlugin{id: "files", focus: "tree", frame: "plain preview"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	if h == nil || p.width != 196 || p.height != 38 {
		t.Fatalf("initial primary inner size = %dx%d deck=%p, want 196x38", p.width, p.height, h)
	}
	if cmd := m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}); cmd == nil {
		t.Fatal("first document returned no load command")
	}
	m.renderContent(200, 40)
	if p.width >= 196 {
		t.Fatalf("split primary retained full width %d", p.width)
	}
	firstLeaf := h.deck.Leaf(panelayout.Document)
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "guide.md"})
	if got := h.deck.Leaf(panelayout.Document); got != firstLeaf {
		t.Fatalf("same-kind open created leaf %d, want existing %d", got, firstLeaf)
	}
	items, active := h.deck.Tabs(firstLeaf)
	if len(items) != 2 || active != 1 {
		t.Fatalf("document tabs=%d active=%d, want two with second active", len(items), active)
	}
	m.renderContent(200, 40)
	var firstTab *mouse.Region
	for _, region := range h.mouse.HitMap.Regions() {
		if hit, ok := region.Data.(appDeckTabHit); region.ID == appDeckTabRegion && ok && hit.leafID == firstLeaf && hit.index == 0 {
			copy := region
			firstTab = &copy
			break
		}
	}
	if firstTab == nil {
		t.Fatal("first document tab has no canonical hit region")
	}
	m.appContentMouse(tea.MouseClickMsg(tea.Mouse{X: firstTab.Rect.X, Y: firstTab.Rect.Y, Button: tea.MouseLeft}))
	_, active = h.deck.Tabs(firstLeaf)
	if active != 0 {
		t.Fatalf("tab click left active index %d, want 0", active)
	}
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindDiff, Value: "wt"})
	m.renderContent(200, 40)
	h.deck.FocusLeaf(h.deck.Leaf(panelayout.Primary))
	p.focus = "tree"
	m.handleAppContentKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if p.focus != "preview" || h.deck.FocusedLeaf() != h.deck.Leaf(panelayout.Primary) {
		t.Fatalf("first Tab focus=%q leaf=%d", p.focus, h.deck.FocusedLeaf())
	}
	m.handleAppContentKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if h.deck.FocusedLeaf() == h.deck.Leaf(panelayout.Primary) || p.innerActive {
		t.Fatalf("second Tab did not leave primary: leaf=%d innerActive=%v", h.deck.FocusedLeaf(), p.innerActive)
	}
}

func TestAppContentDeckRefusalSwitchIsolationAndLinkRelease(t *testing.T) {
	root := t.TempDir()
	p1 := &deckHostTestPlugin{id: "files", focus: "preview", frame: "td-22f35f"}
	p2 := &deckHostTestPlugin{id: "other", focus: "tree", frame: "plain"}
	m := appDeckTestModel(t, root, p1, p2)
	m.renderContent(200, 40)
	h1 := m.currentContentDeck()
	if len(h1.links) != 1 {
		t.Fatalf("rendered links = %#v, want issue hit", h1.links)
	}
	hit := h1.links[0]
	click := tea.MouseClickMsg(tea.Mouse{X: hit.Rect.X, Y: hit.Rect.Y, Button: tea.MouseLeft})
	release := tea.MouseReleaseMsg(tea.Mouse{X: hit.Rect.X, Y: hit.Rect.Y, Button: tea.MouseLeft})
	m.appContentMouse(click)
	m.appContentMouse(release)
	if h1.deck.Leaf(panelayout.Issue) == 0 {
		t.Fatal("left release on current-generation link opened no Issue leaf")
	}
	m.renderContent(200, 40)
	before := h1.deck.Encode()
	h1.canvas.W = 40
	cmd := m.openAppContent(root, p1.id, contentlink.Ref{Kind: contentlink.KindDiff, Value: "wt"})
	if cmd == nil {
		t.Fatal("fit refusal did not return its toast command")
	}
	if after := h1.deck.Encode(); !reflect.DeepEqual(after, before) {
		t.Fatalf("fit refusal mutated deck\nbefore=%#v\nafter=%#v", before, after)
	}
	m.activePlugin = 1
	p1.SetFocused(false)
	p2.SetFocused(true)
	m.renderContent(200, 40)
	h2 := m.currentContentDeck()
	if h2 == nil || h2 == h1 || h2.deck.Leaf(panelayout.Issue) != 0 {
		t.Fatalf("plugin switch reused first deck: first=%p second=%p second issue=%d", h1, h2, h2.deck.Leaf(panelayout.Issue))
	}
	m.activePlugin = 0
	m.renderContent(200, 40)
	if got := m.currentContentDeck(); got != h1 || got.deck.Leaf(panelayout.Issue) == 0 {
		t.Fatal("switching back did not restore the first plugin deck")
	}
	if raw := state.GetContentDeck(root, p1.id); len(raw) == 0 || !strings.Contains(string(raw), "td-22f35f") {
		t.Fatalf("reference-only persisted deck missing issue: %s", raw)
	}
}

func TestAppContentLinkDragDoesNotActivate(t *testing.T) {
	root := t.TempDir()
	p := &deckHostTestPlugin{id: "files", focus: "preview", frame: "td-22f35f"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	hit := h.links[0]
	m.appContentMouse(tea.MouseClickMsg(tea.Mouse{X: hit.Rect.X, Y: hit.Rect.Y, Button: tea.MouseLeft}))
	m.appContentMouse(tea.MouseMotionMsg(tea.Mouse{X: hit.Rect.X + 2, Y: hit.Rect.Y, Button: tea.MouseLeft}))
	m.appContentMouse(tea.MouseReleaseMsg(tea.Mouse{X: hit.Rect.X + 2, Y: hit.Rect.Y, Button: tea.MouseLeft}))
	if h.deck.Leaf(panelayout.Issue) != 0 {
		t.Fatal("dragging across link activated it")
	}
}

func TestActivateTargetKeepsPassiveTargetOnEligibleFilesSurface(t *testing.T) {
	root := t.TempDir()
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "plain"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(200, 40)
	cmd := m.activateTarget(ActivateTargetMsg{Target: uirequest.Target{
		Kind: uirequest.TargetKindIssue, Value: "td-22f35f",
	}})
	if cmd == nil {
		t.Fatal("eligible Files activation returned no issue load command")
	}
	h := m.currentContentDeck()
	if h == nil || h.deck.Leaf(panelayout.Issue) == 0 || m.activePlugin != 0 {
		t.Fatalf("activation left Files surface: deck=%p issue=%d active=%d", h, h.deck.Leaf(panelayout.Issue), m.activePlugin)
	}
}

func TestAppContentDeckClaimsProjectUIRequestAndTruthfullyRefusesFit(t *testing.T) {
	root := t.TempDir()
	ackDir := t.TempDir()
	config.SetTestStateDir(ackDir)
	t.Cleanup(config.ResetTestStateDir)
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "plain"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(200, 40)
	req := uirequest.Request{
		ID: "app-content-open", Action: uirequest.ActionOpen, CreatedAt: time.Now().UTC(),
		Origin: uirequest.Origin{ProjectKey: "sidecar", WorkDir: root},
		Target: uirequest.Target{Kind: uirequest.TargetKindIssue, Value: "td-22f35f"},
	}
	cmd, handled := m.handleAppContentUIRequest(req)
	if !handled || cmd == nil || m.currentContentDeck().deck.Leaf(panelayout.Issue) == 0 {
		t.Fatalf("project request handled=%v cmd=%v issue=%d", handled, cmd, m.currentContentDeck().deck.Leaf(panelayout.Issue))
	}
	acks, err := uirequest.ReadAcks(ackDir, req.ID, req.Action)
	if err != nil || len(acks) != 1 || acks[0].Status != uirequest.StatusOpened || acks[0].Surface != "plugin:file-browser" {
		t.Fatalf("opened acks=%+v err=%v", acks, err)
	}

	h := m.currentContentDeck()
	h.canvas.W = 40
	req.ID = "app-content-refused"
	req.Target = uirequest.Target{Kind: uirequest.TargetKindDiff, Value: "wt"}
	cmd, handled = m.handleAppContentUIRequest(req)
	if !handled || cmd != nil || h.deck.Leaf(panelayout.Diff) != 0 {
		t.Fatalf("fit request handled=%v cmd=%v diff=%d", handled, cmd, h.deck.Leaf(panelayout.Diff))
	}
	acks, err = uirequest.ReadAcks(ackDir, req.ID, req.Action)
	if err != nil || len(acks) != 1 || acks[0].Status != uirequest.StatusDeclined || !strings.Contains(acks[0].Reason, "too small") {
		t.Fatalf("refused acks=%+v err=%v", acks, err)
	}

	req.Origin.TmuxSession = "sidecar-sh-1"
	if _, handled := m.handleAppContentUIRequest(req); handled {
		t.Fatal("shell-scoped request was stolen from its terminal host")
	}
}

func TestAppContentDeckRefusesPassiveSplitAtFitBoundary(t *testing.T) {
	root := t.TempDir()
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "plain"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(90, 30)
	h := m.currentContentDeck()
	before := h.deck.Encode()
	out := m.openAppContentOutcome(h, contentlink.Ref{Kind: contentlink.KindIssue, Value: "td-22f35f"}, "")
	if out.Status != contentpanes.StatusRefused || out.Refusal != contentpanes.RefusalFit {
		t.Fatalf("90x30 outcome = status %v refusal %q", out.Status, out.Refusal)
	}
	if after := h.deck.Encode(); !reflect.DeepEqual(after, before) {
		t.Fatalf("90x30 refusal mutated deck\nbefore=%#v\nafter=%#v", before, after)
	}
	if p.width != 86 || p.height != 28 {
		t.Fatalf("narrow primary inner size = %dx%d, want 86x28", p.width, p.height)
	}
}

func TestAppContentDeckQueuedRenderCommandSurvivesEarlyUpdateReturn(t *testing.T) {
	root := t.TempDir()
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "plain"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(120, 40)
	h := m.currentContentDeck()
	h.queued = append(h.queued, func() tea.Msg { return queuedAppDeckTestMsg{} })
	_, cmd := m.Update(contentpanes.Result{})
	found := false
	for _, msg := range collect(cmd) {
		if _, ok := msg.(queuedAppDeckTestMsg); ok {
			found = true
		}
	}
	if !found {
		t.Fatal("render-queued command was dropped by content result early return")
	}
}
