package tty

import (
	"strings"
	"testing"
)

// The facts a header states about the drawn window are one derivation, so a
// surface cannot silently omit one. The window being off the live edge leads,
// because it is the only note that explains output the user cannot see.
func TestWindowStatusStatesTheWindowFactsInPriorityOrder(t *testing.T) {
	notes := WindowStatus(WindowStatusInput{
		Layout: Viewport{
			Start: 10, MaxOffset: 40, DisplayWidth: 80, DisplayHeight: 24, PaneClipped: true,
		},
		AbsoluteBase:   120,
		MouseReporting: true,
		PaneWidth:      120,
		PaneHeight:     40,
		LiveEdgeKey:    LiveEdgeKey,
	})
	if len(notes) == 0 || !strings.HasPrefix(notes[0].Text, "▲ 30 lines back") {
		t.Fatalf("status = %q, want the distance off the live edge first", notes)
	}
	if !strings.Contains(notes[0].Text, LiveEdgeKey) || !strings.Contains(notes[0].Compact, LiveEdgeKey) {
		t.Fatalf("status %+v never names the key that returns to live", notes[0])
	}
	joined := ""
	for _, note := range notes {
		joined += note.Text + " | " + note.Compact + " | "
	}
	for _, want := range []string{"120x40, showing 80x24", "app mouse"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("status %q is missing %q", joined, want)
		}
	}
}

// A window at the live edge with older lines above it offers them, and says
// nothing about a distance it is not at.
func TestWindowStatusOffersOlderLinesOnlyAtTheTop(t *testing.T) {
	notes := WindowStatus(WindowStatusInput{
		Layout:       Viewport{Start: 0, MaxOffset: 0, DisplayWidth: 80, DisplayHeight: 24},
		AbsoluteBase: 90,
	})
	if len(notes) != 1 || !strings.Contains(notes[0].Text, "90 older lines available") {
		t.Fatalf("status = %q, want only the older-lines offer", notes)
	}
	notes = WindowStatus(WindowStatusInput{
		Layout:       Viewport{Start: 0, MaxOffset: 0, DisplayWidth: 80, DisplayHeight: 24},
		AbsoluteBase: 90,
		LoadingOlder: true,
	})
	if len(notes) != 1 || !strings.Contains(notes[0].Text, "loading older history") {
		t.Fatalf("status = %q, want the fetch already in flight", notes)
	}
}

// A narrow header drops notes by width, least important first, rather than by
// one surface never having implemented them.
func TestAppendStatusDropsTheLeastImportantNoteFirst(t *testing.T) {
	notes := []StatusNote{
		{Text: "▲ 3 lines back • ⇧End live", Compact: "▲3 ⇧End"},
		{Text: "app mouse • ⇧drag select", Compact: "⇧drag"},
	}
	got := AppendStatus("typing", notes, 40, nil)
	if !strings.Contains(got, "3 lines back") {
		t.Fatalf("hint %q dropped the fact that the window is off the live edge", got)
	}
	if strings.Contains(got, "app mouse • ") {
		t.Fatalf("hint %q kept a note in full that does not fit", got)
	}
	if !strings.Contains(got, "⇧drag") {
		t.Fatalf("hint %q dropped a fact that fits compactly", got)
	}
	// Too narrow for even the compact form of the leading note, so nothing is
	// stated rather than a clipped half-fact.
	if got := AppendStatus("typing", notes, 8, nil); got != "typing" {
		t.Fatalf("hint %q overran a header with no room", got)
	}
	if got := AppendStatus("typing", notes, 0, nil); !strings.Contains(got, "app mouse") {
		t.Fatalf("an unbudgeted header dropped a note: %q", got)
	}
}
