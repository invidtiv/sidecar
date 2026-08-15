package workspacelist

import (
	"fmt"
	"image/color"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/styles"
)

// PulseInterval is the frame period every surface that breathes a working or
// blocked marker advances on. Sharing it keeps the project sidebar and the
// global Workspaces list visually in step rather than beating against each
// other at different rates.
const PulseInterval = 175 * time.Millisecond

// Working breathes evenly. Blocked uses two brighter beats followed by a
// longer rest so it attracts attention without making the whole row blink.
var (
	WorkingPulse = []float64{1.00, 0.96, 0.87, 0.75, 0.62, 0.50, 0.42, 0.38, 0.42, 0.50, 0.62, 0.75, 0.87, 0.96}
	BlockedPulse = []float64{1.00, 0.78, 0.52, 0.88, 1.00, 0.68, 0.43, 0.38, 0.38, 0.38, 0.38, 0.38, 0.43, 0.52, 0.68, 0.84}
)

// PulseLane reports whether a lane is one of the two live states that breathe.
func PulseLane(lane string) bool { return lane == "working" || lane == "blocked" }

// PulseMarker is the animated form of a working or blocked marker at a given
// frame. ok is false for every other lane, so callers can fall back to the
// static marker they already resolved.
func PulseMarker(lane string, frame int) (icon string, style lipgloss.Style, ok bool) {
	switch lane {
	case "working":
		level := PulseLevel(WorkingPulse, frame)
		return PulseCircle(level), PulseStyle(styles.Success, level, false), true
	case "blocked":
		level := PulseLevel(BlockedPulse, frame)
		return PulseDiamond(level), PulseStyle(styles.Warning, level, true), true
	default:
		return "", lipgloss.NewStyle(), false
	}
}

func PulseLevel(levels []float64, frame int) float64 {
	if len(levels) == 0 {
		return 1
	}
	if frame < 0 {
		frame = -frame
	}
	return levels[frame%len(levels)]
}

func PulseCircle(level float64) string {
	switch {
	case level >= 0.84:
		return "●"
	case level >= 0.56:
		return "•"
	default:
		return "∙"
	}
}

func PulseDiamond(level float64) string {
	if level >= 0.72 {
		return "◆"
	}
	return "◇"
}

func PulseStyle(base color.Color, level float64, bold bool) lipgloss.Style {
	// Blend toward the theme's muted text rather than a hard-coded background;
	// this keeps every frame legible in both light and dark themes.
	fg := BlendColor(styles.TextMuted, base, level)
	return lipgloss.NewStyle().Foreground(fg).Bold(bold)
}

func BlendColor(low, high color.Color, amount float64) color.Color {
	if amount < 0 {
		amount = 0
	} else if amount > 1 {
		amount = 1
	}
	lr, lg, lb, _ := low.RGBA()
	hr, hg, hb, _ := high.RGBA()
	blend := func(a, b uint32) uint8 {
		return uint8((float64(a>>8)*(1-amount) + float64(b>>8)*amount) + 0.5)
	}
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", blend(lr, hr), blend(lg, hg), blend(lb, hb)))
}
