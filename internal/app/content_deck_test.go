package app

import (
	"encoding/json"
	"fmt"
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
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/livepanes"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
)

type deckHostTestPlugin struct {
	nativeTestPlugin
	id, focus     string
	frame         string
	width, height int
	innerActive   bool
	noFocusStops  bool
	wheelBoundary bool
	wheelX        int
	linkRect      mouse.Rect
	linkKinds     contentlink.KindSet
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
	if p.noFocusStops {
		return nil
	}
	return []plugin.PaneFocusStop{{ID: "tree"}, {ID: "preview"}}
}
func (p *deckHostTestPlugin) PaneFocus() string { return p.focus }
func (p *deckHostTestPlugin) SetPaneFocus(id string) tea.Cmd {
	p.focus = id
	return nil
}
func (p *deckHostTestPlugin) SetPaneFocusActive(active bool) { p.innerActive = active }
func (p *deckHostTestPlugin) ContentLinkSurfaces() []contentlink.Surface {
	rect := p.linkRect
	if rect == (mouse.Rect{}) {
		rect = mouse.Rect{W: len(p.frame), H: 1}
	}
	kinds := p.linkKinds
	if kinds == nil {
		kinds = contentlink.NewKindSet(contentlink.KindIssue, contentlink.KindFile, contentlink.KindDiff, contentlink.KindInternal)
	}
	return []contentlink.Surface{{
		ID: "preview", Rect: rect,
		WorkDir: "/tmp", ProjectRoot: "/tmp", ReadOnly: true,
		Kinds: kinds,
	}}
}

func TestAppContentDeckOnlyDecoratesAndRegistersSurfaceKinds(t *testing.T) {
	root := t.TempDir()
	const session = "sidecar-ws-alpha"

	disallowed := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: session}
	m := appDeckTestModel(t, root, disallowed)
	frame := m.renderContent(120, 30)
	h := m.currentContentDeck()
	if strings.Contains(frame, "\x1b[4m") || len(h.links) != 0 {
		t.Fatalf("omitted session kind looked active: frame=%q links=%+v", frame, h.links)
	}

	allowed := &deckHostTestPlugin{
		id: "file-browser", focus: "preview", frame: session,
		linkKinds: contentlink.NewKindSet(contentlink.KindSession),
	}
	m = appDeckTestModel(t, root, allowed)
	frame = m.renderContent(120, 30)
	h = m.currentContentDeck()
	if !strings.Contains(frame, "\x1b[4m") || len(h.links) != 1 || h.links[0].Ref != (contentlink.Ref{Kind: contentlink.KindSession, Value: session}) {
		t.Fatalf("allowed session kind was not active: frame=%q links=%+v", frame, h.links)
	}
}

func TestAppContentDeckConsumesInternalOSCAndActivatesNoteIntent(t *testing.T) {
	root := t.TempDir()
	open := "\x1b]8;;sidecar://note/nt-4jdj4e\x1b\\"
	close := "\x1b]8;;\x1b\\"
	want := contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: "nt-4jdj4e"}
	for _, tc := range []struct {
		name, frame string
	}{
		{name: "plain URI", frame: "sidecar://note/nt-4jdj4e"},
		{name: "Markdown OSC label", frame: open + "Renamed title" + close},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &deckHostTestPlugin{id: "notes", focus: "preview", frame: tc.frame}
			m := appDeckTestModel(t, root, p)
			frame := m.renderContent(120, 30)
			if strings.Contains(frame, "\x1b]8;") {
				t.Fatalf("rendered app frame leaked internal OSC: %q", frame)
			}
			h := m.currentContentDeck()
			if h == nil || len(h.links) != 1 || h.links[0].Ref != want {
				t.Fatalf("internal hits = %+v", h)
			}
			before := h.deck.Encode()
			cmd := m.openAppContent(root, p.id, h.links[0].Ref)
			if cmd == nil {
				t.Fatal("note intent returned no navigation command")
			}
			got, ok := cmd().(NavigateToNoteMsg)
			if !ok || got.ID != "nt-4jdj4e" || got.ProjectRoot != root {
				t.Fatalf("navigation message = %#v", cmd())
			}
			if after := h.deck.Encode(); !reflect.DeepEqual(after, before) {
				t.Fatalf("note intent mutated passive pane deck: before=%+v after=%+v", before, after)
			}
		})
	}
}

func TestAppContentDeckStripsUnknownInternalOSCWithoutActivation(t *testing.T) {
	root := t.TempDir()
	label := "sidecar://note/nt-4jdj4e"
	frame := "\x1b]8;;sidecar://unknown/nt-4jdj4e\x1b\\" + label + "\x1b]8;;\x1b\\"
	p := &deckHostTestPlugin{id: "notes", focus: "preview", frame: frame}
	m := appDeckTestModel(t, root, p)
	rendered := m.renderContent(120, 30)
	h := m.currentContentDeck()
	if strings.Contains(rendered, "\x1b]8;") || !strings.Contains(rendered, label) {
		t.Fatalf("unknown OSC sanitization = %q", rendered)
	}
	if h == nil || len(h.links) != 0 {
		t.Fatalf("unknown namespace activated: %+v", h.links)
	}
}

func (p *deckHostTestPlugin) WheelAtBoundary(msg tea.MouseWheelMsg) bool {
	p.wheelX = msg.X
	return p.wheelBoundary
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

func TestAppContentDeckResolvedAbsoluteDocumentKeepsCanonicalPath(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	absPath := filepath.Join(home, ".config", "sidecar", "config.json")
	if err := os.MkdirAll(filepath.Dir(absPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("{\"outside\":true}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	raw := "~/.config/sidecar/config.json"
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: raw}
	m := appDeckTestModel(t, root, p)
	m.renderContent(160, 40)
	h := m.currentContentDeck()
	if h == nil {
		t.Fatal("app content deck was not created")
	}
	candidate := contentlink.Pending{Kind: contentlink.KindFile, Raw: raw}
	resolved := resolveAppContentLink(h.key, contentlink.Surface{
		ID: "preview", WorkDir: root, ProjectRoot: root,
	}, candidate)().(appContentResolvedMsg)
	want, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.Found || resolved.Ref.Value != filepath.ToSlash(want) {
		t.Fatalf("absolute resolution = %#v, want %q", resolved, want)
	}
	cmd := m.openAppContent(root, p.id, resolved.Ref)
	if cmd == nil {
		t.Fatal("resolved absolute document returned no load")
	}
	result := cmd().(contentpanes.Result)
	loaded := result.Payload.(docview.LoadedMsg)
	if loaded.Result.Error != nil || loaded.Result.Content != "{\"outside\":true}\n" {
		t.Fatalf("absolute app load = error %v body %q", loaded.Result.Error, loaded.Result.Content)
	}
	updated, _ := m.Update(result)
	got := updated.(Model)
	m = &got
	h = m.currentContentDeck()
	view := h.deck.Viewer(h.deck.Leaf(panelayout.Document)).(*docview.Model)
	if view.Root() != "" || view.Title() != filepath.ToSlash(want) {
		t.Fatalf("absolute app viewer root/title = %q / %q", view.Root(), view.Title())
	}
	// App live-watch reconciliation calls SetRoot with the plugin workdir. The
	// shared viewer must retain the absolute target across that later phase.
	view.SetRoot(root)
	if target := view.WatchTarget(); view.Root() != "" || target.Path != want {
		t.Fatalf("absolute app live target = root %q path %q", view.Root(), target.Path)
	}
}

func TestAppContentDeckSizesPrimaryAndComposesOneFocusRing(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"README.md", "guide.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("# "+name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := &deckHostTestPlugin{id: "files", focus: "tree", frame: "plain preview"}
	p.cursor = tea.NewCursor(3, 2)
	m := appDeckTestModel(t, root, p)
	rendered := m.renderContent(200, 40)
	h := m.currentContentDeck()
	if h == nil || p.width != 200 || p.height != 40 {
		t.Fatalf("initial primary size = %dx%d deck=%p, want borderless 200x40", p.width, p.height, h)
	}
	if h.primaryInner != (paneframe.Box{W: 200, H: 40}) {
		t.Fatalf("primary origin = %+v, want the whole borderless placement", h.primaryInner)
	}
	if !strings.HasPrefix(rendered, "plain preview") {
		t.Fatalf("primary gained an enclosing frame: %q", rendered)
	}
	if cmd := m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}); cmd == nil {
		t.Fatal("first document returned no load command")
	}
	m.renderContent(200, 40)
	var primaryPlacement, docPlacement paneframe.Box
	var docNode *panelayout.Node
	for _, placement := range h.layout.Leaves {
		switch placement.Node.Kind {
		case panelayout.Primary:
			primaryPlacement = placement.Box
		case panelayout.Document:
			docPlacement = placement.Box
			docNode = placement.Node
		}
	}
	if h.primaryInner != primaryPlacement {
		t.Fatalf("split primary origin = %+v, want whole placement %+v", h.primaryInner, primaryPlacement)
	}
	if docPlacement == (paneframe.Box{}) || paneframe.GeometryForChrome(docPlacement, appDeckHost{h}.Chrome(docNode)).Inner == docPlacement {
		t.Fatalf("passive document did not retain framed geometry: %+v", docPlacement)
	}
	if p.width >= 200 {
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
	m.handleAppContentKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if h.deck.FocusedLeaf() != h.deck.Leaf(panelayout.Primary) || p.focus != "preview" || !p.innerActive {
		t.Fatalf("Shift+Tab did not restore flat primary preview focus: leaf=%d focus=%q active=%v", h.deck.FocusedLeaf(), p.focus, p.innerActive)
	}
	if cursor := m.pluginCursor(); cursor == nil || cursor.X != primaryPlacement.X+3 || cursor.Y != headerHeight+primaryPlacement.Y+2 {
		t.Fatalf("primary cursor = %#v, want borderless origin %+v plus local (3,2)", cursor, primaryPlacement)
	}
	seen := len(p.seen)
	m.appContentMouse(tea.MouseClickMsg(tea.Mouse{X: primaryPlacement.X + 5, Y: primaryPlacement.Y + 4, Button: tea.MouseLeft}))
	if len(p.seen) != seen+1 {
		t.Fatalf("primary click was not forwarded: seen %d -> %d", seen, len(p.seen))
	}
	click, ok := p.seen[len(p.seen)-1].(tea.MouseClickMsg)
	if !ok || click.X != 5 || click.Y != 4 {
		t.Fatalf("primary mouse origin = %#v, want plugin-local (5,4)", p.seen[len(p.seen)-1])
	}
}

func TestAppContentDeckPassiveFocusChangesOnlyOnIntentionalClick(t *testing.T) {
	for _, id := range []string{"file-browser", "git-status"} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("one\ntwo\nthree\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			p := &deckHostTestPlugin{id: id, focus: "preview", frame: "primary"}
			m := appDeckTestModel(t, root, p)
			m.renderContent(200, 40)
			m.openAppContent(root, id, contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"})
			m.renderContent(200, 40)
			h := m.currentContentDeck()
			docID := h.deck.Leaf(panelayout.Document)
			var primary, doc paneframe.Box
			for _, placement := range h.layout.Leaves {
				switch placement.Node.Kind {
				case panelayout.Primary:
					primary = placement.Box
				case panelayout.Document:
					doc = paneframe.GeometryForChrome(placement.Box, appDeckHost{h}.Chrome(placement.Node)).Inner
				}
			}
			m.appContentMouse(tea.MouseClickMsg(tea.Mouse{X: doc.X + 1, Y: doc.Y + 1, Button: tea.MouseLeft}))
			if h.deck.FocusedLeaf() != docID || p.innerActive {
				t.Fatalf("passive click focus leaf=%d active=%v, want doc %d", h.deck.FocusedLeaf(), p.innerActive, docID)
			}
			m.renderContent(200, 40)
			m.appContentMouse(tea.MouseMotionMsg(tea.Mouse{X: primary.X + 2, Y: primary.Y + 2}))
			m.appContentMouse(tea.MouseWheelMsg(tea.Mouse{X: primary.X + 2, Y: primary.Y + 2, Button: tea.MouseWheelDown}))
			m.appContentMouse(tea.MouseReleaseMsg(tea.Mouse{X: primary.X + 2, Y: primary.Y + 2, Button: tea.MouseLeft}))
			if h.deck.FocusedLeaf() != docID || p.innerActive {
				t.Fatalf("hover/wheel/release retargeted focus leaf=%d active=%v", h.deck.FocusedLeaf(), p.innerActive)
			}
			m.appContentMouse(tea.MouseClickMsg(tea.Mouse{X: primary.X + 2, Y: primary.Y + 2, Button: tea.MouseLeft}))
			if h.deck.FocusedLeaf() != h.deck.Leaf(panelayout.Primary) || !p.innerActive {
				t.Fatalf("primary click focus leaf=%d active=%v", h.deck.FocusedLeaf(), p.innerActive)
			}
		})
	}
}

func TestAppContentDeckClickRetainsDocumentKeyboardContextThroughUpdate(t *testing.T) {
	for _, id := range []string{"file-browser", "git-status"} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("alpha beta\nalpha\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			p := &deckHostTestPlugin{id: id, focus: "preview", frame: "primary"}
			m := appDeckTestModel(t, root, p)
			adopt := func(updated tea.Model) {
				switch next := updated.(type) {
				case Model:
					m = &next
				case *Model:
					m = next
				default:
					t.Fatalf("updated model type %T", updated)
				}
			}
			m.renderContent(200, 40)
			m.openAppContent(root, id, contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"})
			m.renderContent(200, 40)
			h := m.currentContentDeck()
			docID := h.deck.Leaf(panelayout.Document)
			var primary, doc paneframe.Box
			for _, placement := range h.layout.Leaves {
				switch placement.Node.Kind {
				case panelayout.Primary:
					primary = placement.Box
				case panelayout.Document:
					doc = paneframe.GeometryForChrome(placement.Box, appDeckHost{h}.Chrome(placement.Node)).Inner
				}
			}
			updated, _ := m.Update(tea.MouseClickMsg{X: doc.X + 1, Y: headerHeight + doc.Y + 1, Button: tea.MouseLeft})
			adopt(updated)
			if h.deck.FocusedLeaf() != docID || m.activeContext != "workspace-doc" {
				t.Fatalf("click leaf=%d context=%q, want doc/%q", h.deck.FocusedLeaf(), m.activeContext, "workspace-doc")
			}
			m.renderContent(200, 40)
			updated, _ = m.Update(tea.MouseMotionMsg{X: primary.X + 1, Y: headerHeight + primary.Y + 1})
			adopt(updated)
			if h.deck.FocusedLeaf() != docID || m.activeContext != "workspace-doc" {
				t.Fatalf("hover stole retained focus: leaf=%d context=%q", h.deck.FocusedLeaf(), m.activeContext)
			}
			updated, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
			adopt(updated)
			view := h.deck.Viewer(docID).(*docview.Model)
			if !view.SearchActive() || m.activeContext != "workspace-doc-find" {
				t.Fatalf("search active=%v context=%q", view.SearchActive(), m.activeContext)
			}
			updated, _ = m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
			adopt(updated)
			if view.SearchQuery() != "q" || h.deck.FocusedLeaf() != docID {
				t.Fatalf("search query=%q leaf=%d", view.SearchQuery(), h.deck.FocusedLeaf())
			}
			updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
			adopt(updated)
			if view.SearchActive() || m.activeContext != "workspace-doc" {
				t.Fatalf("escape search active=%v context=%q", view.SearchActive(), m.activeContext)
			}
			updated, _ = m.Update(tea.MouseClickMsg{X: primary.X + 1, Y: headerHeight + primary.Y + 1, Button: tea.MouseLeft})
			adopt(updated)
			if h.deck.FocusedLeaf() != h.deck.Leaf(panelayout.Primary) || m.activeContext == "workspace-doc" {
				t.Fatalf("click-away leaf=%d context=%q", h.deck.FocusedLeaf(), m.activeContext)
			}
		})
	}
}

func TestAppContentDeckAdvertisesAndRunsSupportedViewerCommands(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# title\nbody\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "primary"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	h.SetResourceResolver(func(int, uint64, uint64, resource.Reference, bool) tea.Cmd { return nil })
	for _, ref := range []contentlink.Ref{
		{Kind: contentlink.KindFile, Value: "README.md"},
		{Kind: contentlink.KindIssue, Value: "td-a91834"},
		{Kind: contentlink.KindDiff, Value: "wt"},
		{Kind: contentlink.KindResource, Provider: "test", Matcher: "item", Value: "one"},
	} {
		m.openAppContent(root, p.id, ref)
		m.renderContent(200, 40)
	}

	assertCommands := func(kind panelayout.Kind, want ...string) map[string]plugin.Command {
		t.Helper()
		h.deck.FocusLeaf(h.deck.Leaf(kind))
		h.syncInnerFocus()
		commands := make(map[string]plugin.Command)
		for _, command := range m.appContentCommands() {
			if command.Handler == nil {
				t.Fatalf("%v command %q has no handler", kind, command.ID)
			}
			commands[command.ID] = command
		}
		for _, id := range want {
			if _, ok := commands[id]; !ok {
				t.Fatalf("%v commands missing %q: %#v", kind, id, commands)
			}
		}
		return commands
	}

	docCommands := assertCommands(panelayout.Document, "search-content", "edit", "render", "toggle-wrap", "reveal")
	// Finder, project search, file info, sidebar and resize are Workspace host
	// surfaces, not docview interactions. The app deck does not advertise them
	// until it has those host adapters.
	for _, excluded := range []string{"find-file", "search-project", "info", "toggle-sidebar", "resize-pane-grow", "resize-pane-shrink"} {
		if _, ok := docCommands[excluded]; ok {
			t.Fatalf("unsupported app host command %q was advertised", excluded)
		}
	}
	if _, handled := m.handleAppContentKey(tea.KeyPressMsg{Code: 'I', Text: "I"}); !handled {
		t.Fatal("unsupported Workspace-only info key escaped the focused passive document")
	}
	doc := h.deck.Viewer(h.deck.Leaf(panelayout.Document)).(*docview.Model)
	beforeRendered, beforeWrap := doc.Rendered(), doc.Wrap()
	docCommands["render"].Handler()
	docCommands["toggle-wrap"].Handler()
	if doc.Rendered() == beforeRendered || doc.Wrap() == beforeWrap {
		t.Fatalf("document command handlers did not mutate viewer: rendered=%v wrap=%v", doc.Rendered(), doc.Wrap())
	}
	docCommands["search-content"].Handler()
	if !doc.SearchActive() {
		t.Fatal("InFile command did not enter shared docview search")
	}
	doc.CloseSearch()

	assertCommands(panelayout.Issue, "open-item", "open-in-td", "yank-issue", "yank-issue-key")
	issue := h.deck.Viewer(h.deck.Leaf(panelayout.Issue)).(*issueview.Model)
	openNested := issue.OpenHandler("td-child")
	if openNested == nil {
		t.Fatal("Issue viewer has no app activation handler")
	}
	activation, ok := openNested().(ActivateTargetMsg)
	if !ok || activation.Target.Kind != uirequest.TargetKindIssue || activation.Target.Value != "td-child" {
		t.Fatalf("nested Issue activation = %#v", openNested())
	}
	diffCommands := assertCommands(panelayout.Diff, "diff-open", "diff-down", "diff-up", "toggle-diff-view", "toggle-diff-scope")
	diff := h.deck.Viewer(h.deck.Leaf(panelayout.Diff)).(*workspacediff.View)
	beforeMode := diff.ViewMode
	diffCommands["toggle-diff-view"].Handler()
	if diff.ViewMode == beforeMode {
		t.Fatal("Diff palette handler did not change the shared viewer mode")
	}
	h.deck.FocusLeaf(h.deck.Leaf(panelayout.Diff))
	h.deck.CloseActive()
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindResource, Provider: "test", Matcher: "item", Value: "one"})
	m.renderContent(200, 40)
	assertCommands(panelayout.Resource, resourceview.CommandRefresh, resourceview.CommandOpenSource)
}

func TestAppContentDeckBorderlessPrimaryRuleIsCapabilityDriven(t *testing.T) {
	for _, id := range []string{"file-browser", "git-status", "notes", "td-monitor", "tasks"} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			p := &deckHostTestPlugin{id: id, focus: "preview", frame: id}
			m := appDeckTestModel(t, root, p)
			rendered := m.renderContent(120, 30)
			h := m.currentContentDeck()
			host := appDeckHost{h}
			if h == nil || h.primaryInner != (paneframe.Box{W: 120, H: 30}) || host.Chrome(h.deck.Tree()) != paneframe.ChromeNone {
				t.Fatalf("primary host = %+v inner=%+v chrome=%v", h, h.primaryInner, host.Chrome(h.deck.Tree()))
			}
			if !strings.HasPrefix(rendered, id) {
				t.Fatalf("capability host %q gained an enclosing frame: %q", id, rendered)
			}
		})
	}
}

func TestAppContentDeckTranslatesLinksFromBorderlessPrimaryOrigin(t *testing.T) {
	root := t.TempDir()
	p := &deckHostTestPlugin{
		id: "file-browser", focus: "preview",
		frame:    "ignored\nignored\n   td-22f35f",
		linkRect: mouse.Rect{X: 3, Y: 2, W: 9, H: 1},
	}
	m := appDeckTestModel(t, root, p)
	m.renderContent(120, 30)
	h := m.currentContentDeck()
	if h == nil || len(h.links) != 1 {
		t.Fatalf("borderless primary links = %+v", h)
	}
	want := mouse.Rect{X: 3, Y: 2, W: 9, H: 1}
	if h.links[0].Rect != want {
		t.Fatalf("link rect = %+v, want plugin-local rect translated without legacy frame inset %+v", h.links[0].Rect, want)
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

func TestAppContentDeckDividerDoesNotStealBorderlessPrimaryEdge(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("body"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "plain"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(200, 40)
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"})
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	if len(h.layout.Dividers) != 1 {
		t.Fatalf("layout dividers = %+v", h.layout.Dividers)
	}
	divider := h.layout.Dividers[0]

	seen := len(p.seen)
	primaryEdge := tea.MouseClickMsg(tea.Mouse{X: divider.Box.X - 1, Y: divider.Box.Y + divider.Box.H/2, Button: tea.MouseLeft})
	m.appContentMouse(primaryEdge)
	if h.dragSplit != 0 || len(p.seen) != seen+1 {
		t.Fatalf("primary edge started drag=%d or was not forwarded (%d -> %d)", h.dragSplit, seen, len(p.seen))
	}
	click, ok := p.seen[len(p.seen)-1].(tea.MouseClickMsg)
	if !ok || click.X != primaryEdge.X-h.primaryInner.X || click.Y != primaryEdge.Y-h.primaryInner.Y {
		t.Fatalf("primary edge mouse offset = %#v, origin=%+v", p.seen[len(p.seen)-1], h.primaryInner)
	}

	passiveBorder := tea.MouseClickMsg(tea.Mouse{X: divider.Box.X + divider.Box.W, Y: divider.Box.Y + divider.Box.H/2, Button: tea.MouseLeft})
	m.appContentMouse(passiveBorder)
	if h.dragSplit != divider.SplitID {
		t.Fatalf("passive framed border did not retain widened drag target: drag=%d want=%d", h.dragSplit, divider.SplitID)
	}
	m.appContentMouse(tea.MouseReleaseMsg(tea.Mouse(passiveBorder)))
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
	for _, tc := range []struct {
		width int
		fit   bool
	}{{90, false}, {114, false}, {115, true}} {
		t.Run(fmt.Sprint(tc.width), func(t *testing.T) {
			root := t.TempDir()
			p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "plain"}
			m := appDeckTestModel(t, root, p)
			m.renderContent(tc.width, 30)
			h := m.currentContentDeck()
			before := h.deck.Encode()
			out := m.openAppContentOutcome(h, contentlink.Ref{Kind: contentlink.KindIssue, Value: "td-22f35f"}, "")
			if tc.fit {
				if out.Status == contentpanes.StatusRefused || h.deck.Leaf(panelayout.Issue) == 0 {
					t.Fatalf("%dx30 should fit exact borderless-primary floor: %+v", tc.width, out)
				}
				return
			}
			if out.Status != contentpanes.StatusRefused || out.Refusal != contentpanes.RefusalFit {
				t.Fatalf("%dx30 outcome = status %v refusal %q", tc.width, out.Status, out.Refusal)
			}
			if after := h.deck.Encode(); !reflect.DeepEqual(after, before) {
				t.Fatalf("%dx30 refusal mutated deck\nbefore=%#v\nafter=%#v", tc.width, before, after)
			}
			if p.width != tc.width || p.height != 30 {
				t.Fatalf("narrow primary size = %dx%d, want borderless %dx30", p.width, p.height, tc.width)
			}
		})
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

func TestAppContentDeckDiffWheelAndPointerBoundaryOwnership(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "long.txt")
	if err := os.WriteFile(path, []byte(strings.Repeat("line\n", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "plain", wheelBoundary: true}
	m := appDeckTestModel(t, root, p)
	m.renderContent(200, 40)
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "long.txt"})
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	docLeaf := h.deck.Leaf(panelayout.Document)
	doc, ok := h.deck.Viewer(docLeaf).(*docview.Model)
	if !ok {
		t.Fatalf("document viewer = %T", h.deck.Viewer(docLeaf))
	}
	loaded, ok := doc.Load(9, root, "long.txt", 0, 77)().(docview.LoadedMsg)
	if !ok || !doc.SetResult(loaded) {
		t.Fatal("could not load long document fixture")
	}
	if doc.Rendered() {
		doc.ToggleRenderMode()
	}
	m.renderContent(200, 40)
	doc.Scroll(5)
	if doc.ScrollOffset() == 0 {
		t.Fatal("document fixture did not scroll")
	}
	m.renderContent(200, 40)
	docBox := appDeckLeafBox(t, h, docLeaf)
	wheelUp := tea.MouseWheelMsg{X: docBox.X + 2, Y: headerHeight + docBox.Y + 2, Button: tea.MouseWheelUp}
	if got := FilterInput(*m, wheelUp); got == nil {
		t.Fatal("Files' primary boundary swallowed movable Document wheel")
	}
	if p.wheelX != 0 {
		t.Fatalf("hidden Files boundary was consulted, x=%d", p.wheelX)
	}

	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindDiff, Value: "wt"})
	m.renderContent(200, 40)
	diffLeaf := h.deck.Leaf(panelayout.Diff)
	diff, ok := h.deck.Viewer(diffLeaf).(*workspacediff.View)
	if !ok {
		t.Fatalf("diff viewer = %T", h.deck.Viewer(diffLeaf))
	}
	diff.Content = strings.Repeat("changed line\n", 100)
	m.renderContent(200, 40)
	diffBox := appDeckLeafBox(t, h, diffLeaf)
	wheelDown := tea.MouseWheelMsg{X: diffBox.X + 2, Y: diffBox.Y + 2, Button: tea.MouseWheelDown}
	m.appContentMouse(wheelDown)
	if diff.DiffScroll == 0 {
		t.Fatal("passive Diff wheel did not move content")
	}
}

func TestAppContentDeckPersistsKeyboardAndPaletteNavigation(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.md", "two.md"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "plain"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(200, 40)
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "one.md"})
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "two.md"})
	m.renderContent(200, 40)
	m.handleAppContentKey(tea.KeyPressMsg{Code: '{', Text: "{"})
	if got := persistedAppDeckState(t, root, p.id); paneActive(got.Root, "document") != 0 {
		t.Fatalf("keyboard previous tab persisted active=%d", paneActive(got.Root, "document"))
	}
	m.handleAppContentKey(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := persistedAppDeckState(t, root, p.id); got.FocusKind != "primary" {
		t.Fatalf("Tab persisted focus kind %q, want primary", got.FocusKind)
	}
	m.runAppContentCommand("prev-pane")
	if got := persistedAppDeckState(t, root, p.id); got.FocusKind != "document" {
		t.Fatalf("palette previous pane persisted focus kind %q, want document", got.FocusKind)
	}
	m.runAppContentCommand("prev-tab")
	if got := persistedAppDeckState(t, root, p.id); paneActive(got.Root, "document") != 1 {
		t.Fatalf("palette previous tab persisted active=%d, want 1", paneActive(got.Root, "document"))
	}
}

func TestAppContentDeckLeavesTabWithPrimarySubmodeThatHasNoFocusStops(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.md"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &deckHostTestPlugin{id: "git-status", focus: "preview", frame: "plain"}
	m := appDeckTestModel(t, root, p)
	m.renderContent(200, 40)
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "one.md"})
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	h.deck.FocusLeaf(h.deck.Leaf(panelayout.Primary))
	p.noFocusStops = true

	if cmd, handled := m.handleAppContentKey(tea.KeyPressMsg{Code: tea.KeyTab}); handled || cmd != nil {
		t.Fatalf("primary submode Tab was claimed: handled=%v cmd=%v", handled, cmd != nil)
	}
	if h.deck.FocusedLeaf() != h.deck.Leaf(panelayout.Primary) {
		t.Fatal("declining submode Tab changed outer focus")
	}
}

func TestProjectWorkspaceRemainsItsExistingUnwrappedPaneHost(t *testing.T) {
	root := t.TempDir()
	p := &deckHostTestPlugin{id: workspacePluginID, focus: "preview", frame: "workspace"}
	m := appDeckTestModel(t, root, p)
	if h := m.activeContentDeck(); h != nil {
		t.Fatalf("project Workspace was wrapped in a second app deck: %+v", h)
	}
	if got := m.renderContent(120, 30); !strings.Contains(got, "workspace") {
		t.Fatalf("existing Workspace host did not render directly: %q", got)
	}
	if len(m.contentDecks) != 0 {
		t.Fatalf("Workspace created app deck state: %+v", m.contentDecks)
	}
}

func TestGlobalTasksUsesOneDeckAcrossProjectSwitches(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootA, "a.md"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "b.md"), []byte("b"), 0o600); err != nil {
		t.Fatal(err)
	}
	project := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "project"}
	tasks := &deckHostTestPlugin{id: "tasks", focus: "tree", frame: "tasks"}
	m := appDeckTestModel(t, rootA, project)
	m.globalTasks = &globalTasksHost{plugin: tasks}
	m.scope = ScopeGlobal
	m.globalTab = GlobalTasks

	m.renderGlobalContent(180, 36)
	h := m.currentContentDeck()
	if h == nil || !h.global || h.stateRoot != globalTasksDeckRoot || h.plugin != tasks {
		t.Fatalf("global Tasks deck = %+v", h)
	}
	if tasks.width != h.primaryInner.W || tasks.height != h.primaryInner.H {
		t.Fatalf("Tasks size=%dx%d inner=%dx%d", tasks.width, tasks.height, h.primaryInner.W, h.primaryInner.H)
	}
	ackDir := t.TempDir()
	config.SetTestStateDir(ackDir)
	t.Cleanup(config.ResetTestStateDir)
	req := uirequest.Request{
		ID: "global-tasks-open", Action: uirequest.ActionOpen, CreatedAt: time.Now().UTC(),
		Origin: uirequest.Origin{ProjectKey: "proof", WorkDir: rootA},
		Target: uirequest.Target{Kind: uirequest.TargetKindDiff, Value: "wt"},
	}
	if cmd, handled := m.handleAppContentUIRequest(req); !handled || cmd == nil {
		t.Fatalf("global Tasks request handled=%v cmd=%v", handled, cmd)
	}
	acks, err := uirequest.ReadAcks(ackDir, req.ID, req.Action)
	if err != nil || len(acks) != 1 || acks[0].Status != uirequest.StatusOpened || acks[0].Surface != "plugin:tasks" {
		t.Fatalf("global Tasks acks=%+v err=%v", acks, err)
	}
	if cmd := m.openAppContent(rootA, tasks.ID(), contentlink.Ref{Kind: contentlink.KindFile, Value: "a.md"}); cmd == nil {
		t.Fatal("global Tasks document returned no load command")
	}
	m.renderGlobalContent(180, 36)
	if raw := state.GetContentDeck(globalTasksDeckRoot, tasks.ID()); len(raw) == 0 {
		t.Fatal("global Tasks deck was not persisted under its stable root")
	}
	h.deck.FocusLeaf(h.deck.Leaf(panelayout.Primary))
	tasks.focus = "tree"
	seenBefore := len(tasks.seen)
	if _, handled := m.handleAppContentKey(tea.KeyPressMsg{Code: tea.KeyTab}); !handled || tasks.focus != "preview" {
		t.Fatalf("first global Tasks Tab handled=%v focus=%q", handled, tasks.focus)
	}
	if len(tasks.seen) != seenBefore {
		t.Fatal("global outer ring replayed Tab into Tasks")
	}
	if _, handled := m.handleAppContentKey(tea.KeyPressMsg{Code: tea.KeyTab}); !handled || h.deck.FocusedLeaf() == h.deck.Leaf(panelayout.Primary) {
		t.Fatalf("second global Tasks Tab handled=%v leaf=%d", handled, h.deck.FocusedLeaf())
	}
	if cmd := m.openAppContent(rootA, tasks.ID(), contentlink.Ref{Kind: contentlink.KindDiff, Value: "wt"}); cmd == nil {
		t.Fatal("global Tasks diff returned no load command")
	}
	m.renderGlobalContent(180, 36)
	h.deck.FocusLeaf(h.deck.Leaf(panelayout.Diff))
	h.syncInnerFocus()
	m.updateContext()
	labels := footerLabels(*m)
	for _, want := range []string{"Close", "Tab×", "Tab←", "Tab→", "Focus", "Back"} {
		if !containsHint(labels, want) {
			t.Fatalf("global passive footer labels=%v, missing %q", labels, want)
		}
	}
	h.deck.FocusLeaf(h.deck.Leaf(panelayout.Primary))
	m.globalMouse(tea.MouseClickMsg(tea.Mouse{X: h.primaryInner.X, Y: h.primaryInner.Y, Button: tea.MouseLeft}))
	if got := m.registry.Plugins()[0]; got != project {
		t.Fatalf("global Tasks mouse replaced project plugin with %T", got)
	}
	if m.globalTasks.plugin != tasks {
		t.Fatal("global Tasks mouse did not retain the hosted surface")
	}
	if click, ok := tasks.seen[len(tasks.seen)-1].(tea.MouseClickMsg); !ok || click.X != 0 || click.Y != 0 {
		t.Fatalf("global Tasks mouse offset = %#v", tasks.seen[len(tasks.seen)-1])
	}

	// Tasks is app-global: changing the selected project changes resolution
	// context, but neither replaces its deck nor creates project-keyed state.
	m.ui.WorkDir, m.ui.ProjectRoot = rootB, rootB
	m.renderGlobalContent(180, 36)
	if got := m.currentContentDeck(); got != h {
		t.Fatalf("project switch replaced global deck: got=%p want=%p", got, h)
	} else if got.workdir != rootB {
		t.Fatalf("global deck workdir=%q, want %q", got.workdir, rootB)
	}
	if cmd := m.openAppContent(rootB, tasks.ID(), contentlink.Ref{Kind: contentlink.KindFile, Value: "b.md"}); cmd == nil {
		t.Fatal("switched global Tasks document returned no load command")
	}
	if got := m.contentDecks[appDeckKey(rootB, tasks.ID())]; got != nil {
		t.Fatal("global Tasks created project-keyed deck state")
	}

	m.exitOverview()
	if h.laidOut || h.links != nil {
		t.Fatalf("leaving global Tasks retained active deck: laidOut=%v links=%v", h.laidOut, h.links)
	}
}

func TestEnteringGlobalScopeDeactivatesAppDeckLiveSurface(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "live.md"), []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "plain"}
	m := appDeckTestModel(t, root, p)
	m.globalTasks = &globalTasksHost{plugin: &deckHostTestPlugin{id: "tasks", focus: "tree"}}
	m.renderContent(200, 40)
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "live.md"})
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	if !h.laidOut || h.visibleDocument() == nil {
		t.Fatal("document was not a visible live target before global entry")
	}
	m.enterOverview()
	if h.laidOut || h.visibleDocument() != nil {
		t.Fatalf("global entry retained hidden deck visibility: laidOut=%v doc=%p", h.laidOut, h.visibleDocument())
	}
}

func TestWatcherStartedAfterGlobalEntryIsStoppedAndDoesNotWedgeDeck(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "live.md"), []byte("live"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "plain"}
	m := appDeckTestModel(t, root, p)
	m.globalTasks = &globalTasksHost{plugin: &deckHostTestPlugin{id: "tasks", focus: "tree"}}
	m.renderContent(200, 40)
	m.openAppContent(root, p.id, contentlink.Ref{Kind: contentlink.KindFile, Value: "live.md"})
	m.renderContent(200, 40)
	h := m.currentContentDeck()
	t.Cleanup(h.live.Stop)

	var started livepanes.WatchStartedMsg
	for _, cmd := range h.queued {
		if cmd == nil {
			continue
		}
		if msg, ok := cmd().(livepanes.WatchStartedMsg); ok {
			started = msg
			break
		}
	}
	if started.Watcher == nil {
		t.Fatal("visible document did not queue an in-flight watcher start")
	}
	h.queued = nil
	m.enterOverview()
	if h.laidOut {
		t.Fatal("global entry left the project deck laid out")
	}

	updated, _ := m.Update(started)
	model := updated.(Model)
	if got := h.live.Watcher("docs"); got != nil {
		t.Fatal("late watcher was adopted for a hidden deck")
	}
	model.scope = ScopeProject
	model.renderContent(200, 40)
	var restarted bool
	for _, cmd := range h.queued {
		if cmd != nil {
			restarted = true
			break
		}
	}
	if !restarted {
		t.Fatal("late watcher result left the document binding wedged")
	}
}

func appDeckLeafBox(t *testing.T, h *appContentDeck, leafID int) panelayout.Box {
	t.Helper()
	for _, placement := range h.layout.Leaves {
		if placement.Node != nil && placement.Node.ID == leafID {
			return placement.Box
		}
	}
	t.Fatalf("leaf %d has no placement", leafID)
	return panelayout.Box{}
}

func persistedAppDeckState(t *testing.T, root, pluginID string) contentpanes.State {
	t.Helper()
	var saved contentpanes.State
	if err := json.Unmarshal(state.GetContentDeck(root, pluginID), &saved); err != nil {
		t.Fatal(err)
	}
	return saved
}

func paneActive(node *contentpanes.NodeState, kind string) int {
	if node == nil {
		return -1
	}
	if node.Kind == kind && node.Pane != nil {
		return node.Pane.Active
	}
	if active := paneActive(node.A, kind); active >= 0 {
		return active
	}
	return paneActive(node.B, kind)
}
