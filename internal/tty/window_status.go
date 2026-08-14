package tty

import (
	"fmt"

	"github.com/charmbracelet/x/ansi"
)

// LiveEdgeKey is the chord that puts a scrolled-back window back on the live
// edge while a pane is live. It is named in the status because a window off the
// live edge is showing history, and a user who cannot see how to get back reads
// that as a pane that stopped producing output.
const LiveEdgeKey = "⇧End"

// StatusNote is one fact about the drawn window, in the two lengths a header
// can afford it. A narrow right region states the fact in fewer columns rather
// than dropping it — the fact is what the reader needs; the wording is not.
type StatusNote struct {
	Text    string
	Compact string
}

// WindowStatusInput is what a header needs to know about the window drawn over a
// pane. Every field comes from the layout the surface actually rendered, so the
// status describes the rows on screen rather than a second derivation of them.
type WindowStatusInput struct {
	Layout       Viewport
	AbsoluteBase int
	// LoadingOlder reports a fetch of older history already in flight, which
	// replaces the offer of it.
	LoadingOlder bool
	// MouseReporting reports that the application in the pane has the mouse.
	// WindowStatus no longer states this; callers may still compute it.
	MouseReporting bool
	// PaneLive reports that this surface is typing into the pane.
	PaneLive   bool
	PaneWidth  int
	PaneHeight int

	// LiveEdgeKey is the chord this surface answers to return to the live edge in
	// the state it is in — every unshifted key belongs to a live pane, so it is
	// [LiveEdgeKey] there and the host's plain key while the pane is only being
	// watched. An empty value states the distance without naming a key, which is
	// what a surface that cannot be scrolled back to live owes the reader.
	LiveEdgeKey string
}

// WindowStatus is the facts a pane's header states about the window over it,
// most important first.
//
// The order is the priority order: a window off the live edge leads, because it
// is the only note that explains output the user cannot see. Both terminal
// surfaces read it, so a fact cannot be stated on one and missing on the other —
// which is exactly how the global browser came to show a scrollbar and no word
// about being off the live edge.
func WindowStatus(in WindowStatusInput) []StatusNote {
	notes := make([]StatusNote, 0, 4)
	if linesBack := in.Layout.MaxOffset - in.Layout.Start; linesBack > 0 {
		note := StatusNote{
			Text:    fmt.Sprintf("▲ %d lines back", linesBack),
			Compact: fmt.Sprintf("▲%d", linesBack),
		}
		if in.LiveEdgeKey != "" {
			note.Text += " • " + in.LiveEdgeKey + " live"
			note.Compact += " " + in.LiveEdgeKey
		}
		notes = append(notes, note)
	}
	switch {
	case in.LoadingOlder:
		notes = append(notes, StatusNote{Text: "loading older history…", Compact: "loading…"})
	case in.Layout.Start == 0 && in.AbsoluteBase > 0:
		notes = append(notes, StatusNote{
			Text:    fmt.Sprintf("▲ %d older lines available", in.AbsoluteBase),
			Compact: fmt.Sprintf("▲%d older", in.AbsoluteBase),
		})
	}
	if indicator := PaneSizeIndicator(in.PaneWidth, in.PaneHeight,
		in.Layout.DisplayWidth, in.Layout.DisplayHeight); in.Layout.PaneClipped && indicator != "" {
		notes = append(notes, StatusNote{
			Text:    indicator,
			Compact: fmt.Sprintf("%dx%d", in.PaneWidth, in.PaneHeight),
		})
	}
	return notes
}

// AppendStatus adds notes to a header's right region, in full where they fit,
// compactly where only that fits, and stops at the first that fits neither.
//
// Dropping is by width and takes the least important note last, because
// WindowStatus returns them most important first. A budget of zero or less keeps
// every note in full, for a host whose header clips from the right instead.
// style is the host's own dimming, applied here so the width measured is the
// width drawn.
func AppendStatus(hint string, notes []StatusNote, budget int, style func(string) string) string {
	for _, note := range notes {
		if note.Text == "" {
			continue
		}
		rendered, ok := "", false
		for _, candidate := range []string{note.Text, note.Compact} {
			if candidate == "" {
				continue
			}
			if style != nil {
				candidate = style(candidate)
			}
			joined := candidate
			if hint != "" {
				joined = hint + " " + candidate
			}
			if budget > 0 && ansi.StringWidth(joined) > budget {
				continue
			}
			rendered, ok = joined, true
			break
		}
		if !ok {
			break
		}
		hint = rendered
	}
	return hint
}
