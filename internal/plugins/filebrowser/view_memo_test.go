package filebrowser

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/terminalperf"
	"github.com/marcus/sidecar/internal/ui"
)

// memoIgnoredMsg is a message type the file browser has never heard of. It
// stands in for everything that reaches a plugin on an idle instance —
// terminal deliveries, agent watchers, ticks for other surfaces — and must
// leave the memoized frame alone.
type memoIgnoredMsg struct{ n int }

const (
	memoWidth  = 100
	memoHeight = 24
)

// memoPlugin builds a browser over a real temporary tree with a file already
// loaded into the preview, which is the state an idle Files tab is in.
func memoPlugin(t *testing.T) *Plugin {
	t.Helper()
	dir := t.TempDir()
	for _, rel := range []string{"alpha/one.txt", "beta.txt", "gamma.txt"} {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		body := strings.Repeat(rel+" line\n", 40)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	p := New()
	p.ctx = &plugin.Context{
		WorkDir:     dir,
		ProjectRoot: dir,
		Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
	}
	p.width, p.height = memoWidth, memoHeight
	p.stateRestored = true
	p.tree = NewFileTree(dir)
	if err := p.tree.Build(); err != nil {
		t.Fatalf("build tree: %v", err)
	}
	p.previewFile = "beta.txt"
	msg, ok := LoadPreview(dir, "beta.txt", 0)().(PreviewLoadedMsg)
	if !ok {
		t.Fatal("LoadPreview did not produce a PreviewLoadedMsg")
	}
	p.adoptPreviewFingerprint(msg.Result)
	p.applyPreviewResult(msg.Result)
	p.tabs = []FileTab{{Path: "beta.txt", Loaded: true}}
	p.activeTab = 0
	return p
}

// memoCounters installs a probe for the duration of the test.
func memoCounters(t *testing.T) *terminalperf.Counters {
	t.Helper()
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)
	return counters
}

func memoView(p *Plugin) string { return p.View(memoWidth, memoHeight) }

// TestIdleRendersReuseTheFrameAndRecordACacheHit is the whole point of the
// memo: a message the plugin does not recognize must not cost a rebuild.
func TestIdleRendersReuseTheFrameAndRecordACacheHit(t *testing.T) {
	counters := memoCounters(t)
	p := memoPlugin(t)

	first := memoView(p)
	if built := counters.Snapshot().FilesFramesBuilt; built != 1 {
		t.Fatalf("first render built %d frames, want 1", built)
	}

	p.Update(memoIgnoredMsg{n: 1})
	second := memoView(p)
	p.Update(memoIgnoredMsg{n: 2})
	third := memoView(p)

	if second != first || third != first {
		t.Fatal("idle renders returned different frames")
	}
	snapshot := counters.Snapshot()
	if snapshot.FilesFramesBuilt != 1 {
		t.Fatalf("idle renders built %d frames, want the first one only", snapshot.FilesFramesBuilt)
	}
	if snapshot.FilesFrameCacheHits != 2 {
		t.Fatalf("idle renders recorded %d cache hits, want 2", snapshot.FilesFrameCacheHits)
	}
}

// A frame memoized at one size says nothing about another size.
func TestMemoMissesOnDifferentDimensions(t *testing.T) {
	counters := memoCounters(t)
	p := memoPlugin(t)

	memoView(p)
	p.View(memoWidth-10, memoHeight)
	if built := counters.Snapshot().FilesFramesBuilt; built != 2 {
		t.Fatalf("narrower render built %d frames, want a rebuild", built)
	}
}

// TestWatchEventYieldsANewFrame drives the watcher signal the way the runtime
// does — event, resulting command, resulting message — and requires the frame
// to follow the filesystem.
func TestWatchEventYieldsANewFrame(t *testing.T) {
	p := memoPlugin(t)
	root := p.tree.RootDir

	before := memoView(p)

	if err := os.WriteFile(filepath.Join(root, "delta.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatalf("write delta.txt: %v", err)
	}
	p.Update(memoIgnoredMsg{})
	if cached := memoView(p); cached != before {
		t.Fatal("an ignored message repainted the tree before the watcher fired")
	}

	_, cmd := p.Update(WatchEventMsg{TreeChanged: true, Dirs: []string{root}})
	if cmd == nil {
		t.Fatal("watch event scheduled no work")
	}
	drainIntoPlugin(t, p, cmd)

	after := memoView(p)
	if after == before {
		t.Fatal("frame did not change after the tree rebuild landed")
	}
	if !strings.Contains(ansi.Strip(after), "delta.txt") {
		t.Fatal("rebuilt frame does not show the new file")
	}
}

// A preview refresh is the other half of the watcher: the file on screen
// changed, and the memo must not keep the old bytes.
func TestPreviewRefreshYieldsANewFrame(t *testing.T) {
	p := memoPlugin(t)
	before := memoView(p)

	path := filepath.Join(p.tree.RootDir, "beta.txt")
	if err := os.WriteFile(path, []byte("beta rewritten by the watcher\n"), 0o644); err != nil {
		t.Fatalf("rewrite beta.txt: %v", err)
	}
	cmd := p.refreshPreview()
	if cmd == nil {
		t.Fatal("refreshPreview scheduled no read")
	}
	drainIntoPlugin(t, p, cmd)

	after := memoView(p)
	if after == before {
		t.Fatal("frame did not change after the preview refresh landed")
	}
	if !strings.Contains(ansi.Strip(after), "rewritten by the watcher") {
		t.Fatal("refreshed frame does not show the new bytes")
	}
}

// TestInteractionsRebuildTheFrameOnTheVeryNextRender covers the gestures a
// user makes constantly. Each must be visible in the next frame, not the one
// after it.
func TestInteractionsRebuildTheFrameOnTheVeryNextRender(t *testing.T) {
	cases := []struct {
		name        string
		act         func(t *testing.T, p *Plugin)
		wantChanged bool
	}{
		{
			name:        "cursor move",
			act:         func(t *testing.T, p *Plugin) { p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"}) },
			wantChanged: true,
		},
		{
			name: "wheel scroll",
			act: func(t *testing.T, p *Plugin) {
				p.Update(tea.MouseWheelMsg{X: 2, Y: 4, Button: tea.MouseWheelDown})
			},
			wantChanged: true,
		},
		{
			name: "hover motion",
			act: func(t *testing.T, p *Plugin) {
				p.Update(tea.MouseMotionMsg{X: 4, Y: 5, Button: tea.MouseNone})
			},
		},
		{
			name: "selection drag",
			act: func(t *testing.T, p *Plugin) {
				x := memoWidth - 20
				p.Update(tea.MouseClickMsg{X: x, Y: 4, Button: tea.MouseLeft})
				p.Update(tea.MouseMotionMsg{X: x + 5, Y: 5, Button: tea.MouseLeft})
			},
		},
		{
			name:        "tree search",
			act:         func(t *testing.T, p *Plugin) { p.Update(tea.KeyPressMsg{Code: '/', Text: "/"}) },
			wantChanged: true,
		},
		{
			name: "content search",
			act: func(t *testing.T, p *Plugin) {
				p.activePane = PanePreview
				p.invalidateView()
				p.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
			},
			wantChanged: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counters := memoCounters(t)
			p := memoPlugin(t)
			before := memoView(p)
			built := counters.Snapshot().FilesFramesBuilt

			tc.act(t, p)
			after := memoView(p)

			if got := counters.Snapshot().FilesFramesBuilt; got == built {
				t.Fatal("the next render after the interaction was served from the memo")
			}
			if tc.wantChanged && after == before {
				t.Fatal("the interaction produced an identical frame")
			}
		})
	}
}

// TestClickAfterIdleRendersLandsOnTheRightRow is the hit-region half of the
// memo: a cached frame keeps the regions the render that produced it
// registered, so a click two idle frames later still resolves to the same row.
func TestClickAfterIdleRendersLandsOnTheRightRow(t *testing.T) {
	p := memoPlugin(t)
	// Start the cursor somewhere else so landing on the row is visible.
	p.treeCursor = p.tree.Len() - 1
	memoView(p)

	idx := p.tree.IndexOfPath("alpha")
	if idx < 0 {
		t.Fatal("test premise: no alpha directory row in the tree")
	}
	x, y, ok := treeRowScreenPos(p, idx)
	if !ok {
		t.Fatal("the first render registered no hit region for the alpha row")
	}
	if got := p.mouseHandler.HitMap.Test(x, y); got == nil || got.ID != regionTreeItem {
		t.Fatalf("region at the alpha row = %+v, want a tree item", got)
	}

	// Two idle renders, both served from the memo.
	p.Update(memoIgnoredMsg{n: 1})
	memoView(p)
	p.Update(memoIgnoredMsg{n: 2})
	memoView(p)

	if got := p.mouseHandler.HitMap.Test(x, y); got == nil || got.ID != regionTreeItem {
		t.Fatalf("region after two idle renders = %+v, want the same tree item", got)
	}

	node := p.tree.GetNode(idx)
	if node == nil || !node.IsDir {
		t.Fatalf("test premise: row %d is not the alpha directory", idx)
	}
	p.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if p.treeCursor != idx {
		t.Fatalf("click after idle renders moved the cursor to %d, want %d", p.treeCursor, idx)
	}
	if p.activePane != PaneTree {
		t.Fatal("click after idle renders did not focus the tree pane")
	}
	if p.dragSourcePath != node.Path {
		t.Fatalf("click after idle renders armed %q, want the row it landed on (%q)",
			p.dragSourcePath, node.Path)
	}
	p.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})

	// The preview scrollbar and the preview line regions are registered by the
	// same render, so they survive with it.
	if !hasRegion(p, regionPreviewLine) {
		t.Fatal("preview selection regions did not survive the cached frames")
	}
}

// Scrollbar regions belong to the frame that drew the bar; a cache hit must
// not drop them.
func TestScrollbarRegionsSurviveACacheHit(t *testing.T) {
	p := memoPlugin(t)
	p.previewLines = nil
	for i := 0; i < 500; i++ {
		p.previewLines = append(p.previewLines, "line")
	}
	p.previewHighlighted = append([]string(nil), p.previewLines...)
	memoView(p)
	if !hasRegion(p, ui.RegionScrollbarTrack) && !hasRegion(p, ui.RegionScrollbarThumb) {
		t.Skip("this build registers no scrollbar regions for the preview")
	}
	before := regionIDs(p)

	p.Update(memoIgnoredMsg{})
	memoView(p)

	if got := regionIDs(p); !equalStrings(got, before) {
		t.Fatalf("regions after a cache hit = %v, want %v", got, before)
	}
}

// TestInlineEditorBypassesTheMemo: tmux owns those pixels and changes them
// without any message this plugin sees.
func TestInlineEditorBypassesTheMemo(t *testing.T) {
	counters := memoCounters(t)
	p := memoPlugin(t)
	memoView(p)

	p.edit.Active = true
	memoView(p)
	built := counters.Snapshot().FilesFramesBuilt
	memoView(p)
	memoView(p)
	if got := counters.Snapshot().FilesFramesBuilt; got != built+2 {
		t.Fatalf("inline editor renders built %d frames, want one per render", got-built)
	}
	if counters.Snapshot().FilesFrameCacheHits != 0 {
		t.Fatal("the inline editor was served from the memo")
	}

	// Leaving the editor must not resurrect a frame captured while it was on
	// screen.
	p.edit.Active = false
	first := memoView(p)
	second := memoView(p)
	if second != first {
		t.Fatal("the first frame after the editor closed was not memoized")
	}
}

func TestImagePreviewBypassesTheMemo(t *testing.T) {
	counters := memoCounters(t)
	p := memoPlugin(t)
	p.previewFile = "logo.png"
	p.isImage = true

	memoView(p)
	built := counters.Snapshot().FilesFramesBuilt
	memoView(p)
	if got := counters.Snapshot().FilesFramesBuilt; got != built+1 {
		t.Fatal("an image preview was served from the memo")
	}
	if counters.Snapshot().FilesFrameCacheHits != 0 {
		t.Fatal("an image preview recorded a cache hit")
	}
}

// The app can change what is drawn without sending a message. Every such
// setter invalidates — and only when it really changes something, because the
// deck re-asserts pane focus on every pass and an unconditional invalidation
// there would cost a rebuild on every frame.
func TestAppSettersInvalidateTheFrameOnlyWhenSomethingChanges(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(p *Plugin)
		apply      func(p *Plugin)
		idempotent bool
	}{
		{
			name:       "SetFocused",
			apply:      func(p *Plugin) { p.SetFocused(true) },
			idempotent: true,
		},
		{
			name:       "SetPaneFocus",
			apply:      func(p *Plugin) { p.SetPaneFocus(filesPreviewFocusID) },
			idempotent: true,
		},
		{
			name:       "SetPaneFocusActive",
			apply:      func(p *Plugin) { p.SetPaneFocusActive(false) },
			idempotent: true,
		},
		{
			name:       "FocusCycleStart",
			setup:      func(p *Plugin) { p.activePane = PanePreview },
			apply:      func(p *Plugin) { p.FocusCycleStart(false) },
			idempotent: true,
		},
		{
			name:  "Stop",
			apply: func(p *Plugin) { p.Stop() },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			counters := memoCounters(t)
			p := memoPlugin(t)
			if tc.setup != nil {
				tc.setup(p)
			}
			memoView(p)
			built := counters.Snapshot().FilesFramesBuilt

			tc.apply(p)
			memoView(p)
			if got := counters.Snapshot().FilesFramesBuilt; got == built {
				t.Fatalf("%s left the memoized frame in place", tc.name)
			}
			if !tc.idempotent {
				return
			}
			built = counters.Snapshot().FilesFramesBuilt
			tc.apply(p)
			memoView(p)
			if got := counters.Snapshot().FilesFramesBuilt; got != built {
				t.Fatalf("%s repeated with the same value rebuilt the frame", tc.name)
			}
		})
	}
}

// A theme change repaints everything even though no state the user can see
// moved.
func TestThemeChangeInvalidatesTheFrame(t *testing.T) {
	counters := memoCounters(t)
	p := memoPlugin(t)
	memoView(p)
	built := counters.Snapshot().FilesFramesBuilt

	p.Update(appmsg.ThemeChangedMsg{})
	memoView(p)
	if got := counters.Snapshot().FilesFramesBuilt; got == built {
		t.Fatal("a theme change was served from the memo")
	}
}

// TestViewInvalidationCoversEveryHandledMessage compares the case types of
// update()'s type switches with the case types of viewInvalidatingMsg. Adding a
// message to one and not the other is the way this memo would go stale, so the
// two lists are compared in the source rather than trusted to review.
func TestViewInvalidationCoversEveryHandledMessage(t *testing.T) {
	handled := caseTypesOfFunc(t, "plugin.go", "update")
	invalidating := caseTypesOfFunc(t, "view_memo.go", "viewInvalidatingMsg")

	if len(handled) == 0 || len(invalidating) == 0 {
		t.Fatal("failed to read the case types out of the source")
	}
	for _, name := range handled {
		if !contains(invalidating, name) {
			t.Errorf("update() handles %s but viewInvalidatingMsg does not list it: "+
				"the memo would serve a stale frame after that message", name)
		}
	}
	for _, name := range invalidating {
		if !contains(handled, name) {
			t.Errorf("viewInvalidatingMsg lists %s but update() no longer handles it: "+
				"drop it so the memo survives that message", name)
		}
	}
}

// The ignore path is the type switch falling through. It must mutate nothing,
// which is what makes leaving the memo alone correct.
func TestIgnoredMessageMutatesNothingDrawn(t *testing.T) {
	p := memoPlugin(t)
	before := memoView(p)
	cursor, scroll, pane := p.treeCursor, p.previewScroll, p.activePane

	if _, cmd := p.Update(memoIgnoredMsg{}); cmd != nil {
		t.Fatal("an unrecognized message produced a command")
	}
	if p.treeCursor != cursor || p.previewScroll != scroll || p.activePane != pane {
		t.Fatal("an unrecognized message moved drawn state")
	}
	p.invalidateView()
	if after := memoView(p); after != before {
		t.Fatal("rebuilding after an unrecognized message produced a different frame")
	}
}

// --- helpers ---

func drainIntoPlugin(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	for depth := 0; cmd != nil && depth < 8; depth++ {
		msg := cmd()
		if msg == nil {
			return
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, sub := range batch {
				drainIntoPlugin(t, p, sub)
			}
			return
		}
		_, cmd = p.Update(msg)
	}
}

func treeRowScreenPos(p *Plugin, idx int) (x, y int, ok bool) {
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionTreeItem {
			continue
		}
		if data, isInt := region.Data.(int); !isInt || data != idx {
			continue
		}
		return region.Rect.X + 1, region.Rect.Y, true
	}
	return 0, 0, false
}

func hasRegion(p *Plugin, id string) bool {
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == id {
			return true
		}
	}
	return false
}

func regionIDs(p *Plugin) []string {
	var ids []string
	for _, region := range p.mouseHandler.HitMap.Regions() {
		ids = append(ids, region.ID)
	}
	sort.Strings(ids)
	return ids
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// caseTypesOfFunc returns the type names named by every type-switch case in the
// named function, in source form ("tea.MouseMsg", "TreeBuiltMsg").
func caseTypesOfFunc(t *testing.T, file, funcName string) []string {
	t.Helper()
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var target *ast.FuncDecl
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == funcName {
			target = fn
			break
		}
	}
	if target == nil {
		t.Fatalf("%s: no function %s", file, funcName)
	}

	seen := map[string]bool{}
	var names []string
	ast.Inspect(target, func(node ast.Node) bool {
		typeSwitch, ok := node.(*ast.TypeSwitchStmt)
		if !ok {
			return true
		}
		for _, stmt := range typeSwitch.Body.List {
			clause, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue
			}
			for _, expr := range clause.List {
				var buf bytes.Buffer
				if err := printer.Fprint(&buf, fset, expr); err != nil {
					t.Fatalf("print case type: %v", err)
				}
				name := buf.String()
				if name == "nil" || seen[name] {
					continue
				}
				seen[name] = true
				names = append(names, name)
			}
		}
		return true
	})
	sort.Strings(names)
	return names
}
