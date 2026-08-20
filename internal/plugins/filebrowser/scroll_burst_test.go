package filebrowser

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
)

func newScrollBurstPlugin(t *testing.T, files int) *Plugin {
	t.Helper()
	p := New()
	p.ctx = &plugin.Context{WorkDir: t.TempDir(), Epoch: 7}
	p.tree = NewFileTree(p.ctx.WorkDir)
	p.tree.FlatList = make([]*FileNode, files)
	for i := range files {
		path := fmt.Sprintf("file-%03d.go", i)
		p.tree.FlatList[i] = &FileNode{Name: path, Path: path}
	}
	p.width, p.height, p.treeWidth = 100, 30, 30
	return p
}

// A dense gesture moves the tree only on the shared burst's apply points. The
// held tail stays in the burst exactly as it does on terminal surfaces, so the
// applied cursor distance plus Pending is the complete input distance.
func TestTreeWheelBurstKeepsDistanceAndActivatesOnlyTheLandingPreview(t *testing.T) {
	p := newScrollBurstPlugin(t, 240)
	at := time.Unix(100, 0)
	p.wheelNow = func() time.Time { return at }
	action := mouse.MouseAction{Type: mouse.ActionScrollDown, Delta: mouse.WheelScrollLines, X: 2}

	type scheduled struct {
		gen  uint64
		path string
	}
	var timers []scheduled
	rawDistance := 0
	for i := range 40 {
		_, cmd := p.handleMouseScroll(action)
		rawDistance += action.Delta
		if cmd != nil {
			node := p.tree.GetNode(p.treeCursor)
			timers = append(timers, scheduled{gen: p.treePreviewGen, path: node.Path})
		}
		at = at.Add(time.Duration(4+i/8) * time.Millisecond)
	}

	pending := p.wheelBursts.For(regionTreePane).Pending()
	if got := p.treeCursor + pending; got != rawDistance {
		t.Fatalf("cursor %d + pending %d = %d, want all %d input rows", p.treeCursor, pending, got, rawDistance)
	}
	if len(timers) == 0 || len(timers) >= 40 {
		t.Fatalf("scheduled %d landing timers for 40 events, want immediate first movement and coalesced remainder", len(timers))
	}
	if len(p.tabs) != 0 || p.previewFile != "" {
		t.Fatalf("tree burst activated preview before quiet period: tabs=%v preview=%q", p.tabs, p.previewFile)
	}

	// Every earlier timer is stale. Only the current landing generation may
	// mutate the preview tab and start its load.
	for _, timer := range timers[:len(timers)-1] {
		_, cmd := p.handleTreePreviewQuiet(treePreviewQuietMsg{Gen: timer.gen, Epoch: p.ctx.Epoch, Path: timer.path})
		if cmd != nil {
			t.Fatalf("stale generation %d started a preview load", timer.gen)
		}
	}
	landing := timers[len(timers)-1]
	_, load := p.handleTreePreviewQuiet(treePreviewQuietMsg{Gen: landing.gen, Epoch: p.ctx.Epoch, Path: landing.path})
	if load == nil {
		t.Fatal("landing quiet timer did not start its preview load")
	}
	if len(p.tabs) != 1 || p.previewFile != landing.path || p.tabs[0].Path != landing.path {
		t.Fatalf("landing preview = tabs=%v preview=%q, want only %q", p.tabs, p.previewFile, landing.path)
	}
	_, duplicate := p.handleTreePreviewQuiet(treePreviewQuietMsg{Gen: landing.gen, Epoch: p.ctx.Epoch, Path: landing.path})
	if duplicate != nil {
		t.Fatal("the same landing timer started a second preview load")
	}
}

func TestHeldTreeKeysDebounceAndStaleTimerCannotChangeNewerContext(t *testing.T) {
	p := newScrollBurstPlugin(t, 12)

	_, first := p.handleTreeKey("j")
	firstGen := p.treePreviewGen
	firstPath := p.tree.GetNode(p.treeCursor).Path
	_, second := p.handleTreeKey("j")
	secondGen := p.treePreviewGen
	secondPath := p.tree.GetNode(p.treeCursor).Path
	if first == nil || second == nil {
		t.Fatal("tree movement did not schedule quiet preview activation")
	}
	if len(p.tabs) != 0 {
		t.Fatalf("held keys activated %d tabs before the quiet period", len(p.tabs))
	}

	if _, cmd := p.handleTreePreviewQuiet(treePreviewQuietMsg{Gen: firstGen, Epoch: p.ctx.Epoch, Path: firstPath}); cmd != nil {
		t.Fatal("older key-repeat timer started a preview load")
	}
	p.ctx.Epoch++
	if _, cmd := p.handleTreePreviewQuiet(treePreviewQuietMsg{Gen: secondGen, Epoch: p.ctx.Epoch - 1, Path: secondPath}); cmd != nil {
		t.Fatal("timer from the previous project epoch started a preview load")
	}
	if len(p.tabs) != 0 || p.previewFile != "" {
		t.Fatalf("stale timer changed preview state: tabs=%v preview=%q", p.tabs, p.previewFile)
	}
}

func TestHeldFilesWheelReusesOnlyOneSameSizeFrame(t *testing.T) {
	p := newScrollBurstPlugin(t, 12)
	p.viewCache = "already rendered"
	p.viewCacheW, p.viewCacheH, p.viewCacheOK = 100, 30, true
	p.reuseViewOnce = true

	if got := p.View(100, 30); got != "already rendered" {
		t.Fatalf("held wheel rebuilt view, got %q", got)
	}
	if p.reuseViewOnce {
		t.Fatal("held-wheel frame reuse was not consumed after one View")
	}
}

func TestFilesWheelFirstEventIsImmediateAndNextDenseEventRequestsReuse(t *testing.T) {
	p := newScrollBurstPlugin(t, 20)
	at := time.Unix(200, 0)
	p.wheelNow = func() time.Time { return at }
	action := mouse.MouseAction{Type: mouse.ActionScrollDown, Delta: mouse.WheelScrollLines, X: 2}

	p.handleMouseScroll(action)
	if p.treeCursor != mouse.WheelScrollLines || p.reuseViewOnce {
		t.Fatalf("first notch cursor=%d reuse=%v, want immediate cursor movement", p.treeCursor, p.reuseViewOnce)
	}
	at = at.Add(tty.WheelDebounceInterval / 2)
	p.handleMouseScroll(action)
	if p.treeCursor != mouse.WheelScrollLines || !p.reuseViewOnce {
		t.Fatalf("held notch cursor=%d reuse=%v, want unchanged cursor and cached-frame request", p.treeCursor, p.reuseViewOnce)
	}
}

func TestFilesBoundaryWheelDropsAndClearsHeldBurst(t *testing.T) {
	p := newScrollBurstPlugin(t, 20)
	p.treeCursor = p.tree.Len() - 1
	p.mouseHandler.HitMap.AddRect(regionTreePane, 0, 0, p.treeWidth, p.height, nil)
	at := time.Unix(300, 0)
	p.wheelBursts.For(regionTreePane).Add(mouse.WheelScrollLines, at)
	p.wheelBursts.For(regionTreePane).Add(mouse.WheelScrollLines, at.Add(time.Millisecond))
	if p.wheelBursts.For(regionTreePane).Pending() == 0 {
		t.Fatal("test premise: burst has no held delta")
	}

	down := tea.MouseWheelMsg{X: 2, Y: 4, Button: tea.MouseWheelDown}
	if !p.WheelAtBoundary(down) {
		t.Fatal("tree inertia at the last row was not identified as a boundary")
	}
	if pending := p.wheelBursts.For(regionTreePane).Pending(); pending != 0 {
		t.Fatalf("held delta survived boundary drop: %d", pending)
	}
	up := tea.MouseWheelMsg{X: 2, Y: 4, Button: tea.MouseWheelUp}
	if p.WheelAtBoundary(up) {
		t.Fatal("wheel back into the tree was dropped")
	}
}

func TestFilesPreviewBoundaryUsesRenderedContentHeight(t *testing.T) {
	p := newScrollBurstPlugin(t, 1)
	p.treeWidth = 30
	p.previewLines = []string{"one", "two", "three"}
	p.previewScroll = 0
	p.mouseHandler.HitMap.AddRect(regionPreviewPane, 31, 0, 69, p.height, nil)

	up := tea.MouseWheelMsg{X: 50, Y: 4, Button: tea.MouseWheelUp}
	if !p.WheelAtBoundary(up) {
		t.Fatal("short preview at top did not drop upward inertia")
	}
	p.edit.Active = true
	if p.WheelAtBoundary(up) {
		t.Fatal("inline editor wheel was classified using the ordinary preview")
	}
}
