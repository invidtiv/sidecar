package ui

import (
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// KeyChip is one inline action affordance in the app's footer hint style: a
// styled key chip followed by a plain label. ID doubles as the action name
// the surface routes clicks and Enter to.
type KeyChip struct {
	Keys  string
	Label string
	ID    string
}

// KeyChipRegion reports where one rendered chip sits on its line, for hit
// regions and focus geometry.
type KeyChipRegion struct {
	ID      string
	OffsetX int
	Width   int
}

// RenderKeyChips renders chips as ONE inline line in exactly the footer/palette
// hint style — styles.KeyHint around the keys, one space to the label, two
// spaces between pairs — stopping before maxWidth when maxWidth is positive.
//
// The chip whose ID matches hoverID or focusID lights up with the same
// hover/focus colours the modal block buttons use (KeyHint geometry kept, only
// the background swapped, so styling never reflows the line). State-free over
// plain inputs: a headless caller could adopt it unchanged.
func RenderKeyChips(chips []KeyChip, maxWidth int, focusID, hoverID string) (string, []KeyChipRegion) {
	var line string
	x := 0
	regions := make([]KeyChipRegion, 0, len(chips))
	for _, c := range chips {
		if c.Keys == "" || c.Label == "" || c.ID == "" {
			continue
		}
		// The space between key and label belongs to the label's style run, so
		// a highlighted chip fills as one continuous pill instead of two
		// blocks with a hole punched between them. At rest the label style has
		// no fill and this renders identically to a plain separator.
		part := chipKeyStyle(c.ID, focusID, hoverID).Render(c.Keys) +
			chipLabelStyle(c.ID, focusID, hoverID).Render(" "+c.Label)
		w := ansi.StringWidth(part)
		visible := w
		if maxWidth > 0 && visible > maxWidth-x {
			// Downstream clamping truncates the glyphs to the column edge; the
			// hit rect must stop where the pixels do, so a click or hover past
			// the visible text never lands on an invisible target.
			visible = max(0, maxWidth-x)
		}
		if maxWidth > 0 && line != "" {
			// The leading chip always renders — renderHintLineTruncated's
			// contract — later ones stop before overflowing.
			if ansi.StringWidth(line+"  "+part) > maxWidth {
				break
			}
		}
		if line != "" {
			line += "  "
			x += 2
		}
		line += part
		if visible > 0 {
			regions = append(regions, KeyChipRegion{ID: c.ID, OffsetX: x, Width: visible})
		}
		x += w
	}
	if line == "" {
		return "", nil
	}
	return line, regions
}

// KeyChipsWidth is the rendered width of a chip line at unlimited width,
// without rendering: callers pre-checking fit.
func KeyChipsWidth(chips []KeyChip) int {
	line, _ := RenderKeyChips(chips, 0, "", "")
	return ansi.StringWidth(line)
}

// chipKeyStyle keeps the shared KeyHint chip's geometry verbatim — same
// padding, so a highlight never moves the neighbouring chips — and swaps its
// fill for the block buttons' hover/focus colours.
//
// The focused chip must also restate its foreground. KeyHint's own colour is
// chosen against SurfaceRaised, and in themes where the key glyphs are the
// accent (Sidecar Modern draws both from the same gold) inheriting it onto a
// Primary fill paints gold on gold — a chip that reads as a blank block. On a
// Primary fill the text colour is OnPrimary, exactly as styles.ButtonFocused
// does it.
func chipKeyStyle(id, focusID, hoverID string) lipgloss.Style {
	switch id {
	case focusID:
		return styles.KeyHint.Foreground(styles.OnPrimaryColor).Background(styles.Primary).Bold(true)
	case hoverID:
		// ButtonHover is a subtle raised fill in every theme, so the key keeps
		// its own colour here and stays recognisably a key.
		return styles.KeyHint.Background(styles.ButtonHoverColor)
	default:
		return styles.KeyHint
	}
}

func chipLabelStyle(id, focusID, hoverID string) lipgloss.Style {
	switch id {
	case focusID:
		return lipgloss.NewStyle().Foreground(styles.OnPrimaryColor).Background(styles.Primary).Bold(true)
	case hoverID:
		return lipgloss.NewStyle().Foreground(styles.TextPrimary).Background(styles.ButtonHoverColor)
	default:
		return lipgloss.NewStyle()
	}
}
