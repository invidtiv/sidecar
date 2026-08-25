package ui

import (
	"fmt"

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
		part := fmt.Sprintf("%s %s", chipKeyStyle(c.ID, focusID, hoverID).Render(c.Keys),
			chipLabelStyle(c.ID, focusID, hoverID).Render(c.Label))
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

// chipKeyStyle keeps the shared KeyHint chip verbatim and swaps only its
// background for the block buttons' hover/focus colours — same padding, so a
// highlight never moves the neighbouring chips.
func chipKeyStyle(id, focusID, hoverID string) lipgloss.Style {
	switch id {
	case focusID:
		return styles.KeyHint.Background(styles.Primary).Bold(true)
	case hoverID:
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
