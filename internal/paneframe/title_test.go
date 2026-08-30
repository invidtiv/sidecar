package paneframe

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/panelayout"
)

// titledContent is a content that draws its name wider than its Title() text,
// the way a focused terminal leaf draws a "▸ " marker before it.
type titledContent struct {
	fakeContent
	cols int
}

func (c *titledContent) TitleColumns() int { return c.cols }

func TestTitleHitBoxIsTheLeadingRunOfTheHeaderRow(t *testing.T) {
	inner := Box{X: 5, Y: 3, W: 20, H: 6}
	hit, ok := TitleHitBox(&fakeContent{kind: "shell"}, inner)
	if !ok {
		t.Fatal("a leaf with a title registered no target")
	}
	want := Box{X: 5, Y: 3, W: len("shell"), H: 1}
	if hit != want {
		t.Fatalf("title hit = %+v, want %+v", hit, want)
	}
}

// The marker before a focused leaf's name is part of the name a user aims at.
func TestTitleHitBoxUsesTheContentsOwnColumnsWhenItHasThem(t *testing.T) {
	hit, ok := TitleHitBox(&titledContent{fakeContent: fakeContent{kind: "term"}, cols: 9}, Box{W: 20, H: 4})
	if !ok || hit.W != 9 {
		t.Fatalf("title hit = %+v (ok=%v), want 9 columns", hit, ok)
	}
}

func TestTitleHitBoxIsClampedToTheLeafAndRefusesWhenThereIsNoTitle(t *testing.T) {
	hit, ok := TitleHitBox(&titledContent{cols: 40}, Box{W: 12, H: 4})
	if !ok || hit.W != 12 {
		t.Fatalf("narrow leaf title = %+v (ok=%v), want clamped to 12", hit, ok)
	}
	if _, ok := TitleHitBox(&fakeContent{}, Box{W: 12, H: 4}); ok {
		t.Fatal("a leaf with no title claimed a target")
	}
	if _, ok := TitleHitBox(nil, Box{W: 12, H: 4}); ok {
		t.Fatal("a leaf with no content claimed a target")
	}
}

// The title sits on the header row a click-to-focus leaf region also covers, so
// the order it is registered in is what makes the name the clickable thing —
// and the close button, registered after it, still wins its own cells.
func TestRegisterRegionsPutsTheTitleAfterTabsAndBeforeClose(t *testing.T) {
	layout := panelayout.Layout{
		Leaves: []panelayout.Placement{
			{Node: &panelayout.Node{ID: 1, Kind: panelayout.Shell}, Box: Box{W: 20, H: 8}},
		},
	}
	host := &fakeHost{
		contents: map[int]*fakeContent{1: {kind: "shell"}},
		chrome:   map[int]Chrome{},
	}
	sink := &recordingSink{}
	RegisterRegions(sink, host, layout)
	want := []string{"leaf:1", "tabs:1", "title:1", "layout:1", "close:1", "body:1"}
	if strings.Join(sink.calls, ",") != strings.Join(want, ",") {
		t.Fatalf("registration order = %v, want %v", sink.calls, want)
	}
}

// Focus is answered from geometry, so a press on the title still lands on the
// leaf it names. The rename target and the focus target are the same leaf.
func TestTitlePressAndHeaderPressResolveToTheSameLeaf(t *testing.T) {
	outer := Box{X: 2, Y: 1, W: 24, H: 8}
	layout := panelayout.Layout{
		Leaves: []panelayout.Placement{{Node: &panelayout.Node{ID: 7, Kind: panelayout.Shell}, Box: outer}},
	}
	host := &fakeHost{
		contents: map[int]*fakeContent{7: {kind: "shell"}},
		chrome:   map[int]Chrome{},
		layout:   layout,
		laid:     true,
	}
	inner := Inset(outer)
	hit, ok := TitleHitBox(host.Content(layout.Leaves[0].Node), inner)
	if !ok {
		t.Fatal("shell leaf registered no title target")
	}
	if !FocusLeafAt(host, hit.X, hit.Y) {
		t.Fatal("a press on the title focused nothing")
	}
	// A press further along the same header row is click-to-focus and nothing
	// else: it is outside the title's cells.
	beyond := hit.X + hit.W + 1
	if beyond >= inner.X+inner.W {
		t.Fatalf("header row too narrow for this test: %+v", inner)
	}
	if boxContains(hit, beyond, hit.Y) {
		t.Fatalf("title target %+v claims column %d, which is header space", hit, beyond)
	}
	if !FocusLeafAt(host, beyond, hit.Y) {
		t.Fatal("a press on the header focused nothing")
	}
	if len(host.setFocus) != 2 || host.setFocus[0] != 7 || host.setFocus[1] != 7 {
		t.Fatalf("focus moved to %v, want the same leaf twice", host.setFocus)
	}
}
