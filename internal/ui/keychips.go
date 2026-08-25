package ui

import (
	"fmt"

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
// State-free over plain inputs: a headless caller could adopt it unchanged.
func RenderKeyChips(chips []KeyChip, maxWidth int) (string, []KeyChipRegion) {
	var line string
	x := 0
	regions := make([]KeyChipRegion, 0, len(chips))
	for _, c := range chips {
		if c.Keys == "" || c.Label == "" || c.ID == "" {
			continue
		}
		part := fmt.Sprintf("%s %s", styles.KeyHint.Render(c.Keys), c.Label)
		w := ansi.StringWidth(part)
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
		regions = append(regions, KeyChipRegion{ID: c.ID, OffsetX: x, Width: w})
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
	line, _ := RenderKeyChips(chips, 0)
	return ansi.StringWidth(line)
}
