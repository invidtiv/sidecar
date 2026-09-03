package styles

import (
	"github.com/marcus/td/pkg/monitor"
)

// CreateTDPanelRenderer creates a PanelRenderer that uses sidecar's gradient borders.
// Maps td monitor PanelState values to appropriate gradients from the current theme.
func CreateTDPanelRenderer() monitor.PanelRenderer {
	return func(content string, width, height int, state monitor.PanelState) string {
		gradient := getTDPanelGradient(state)
		return RenderGradientBorder(content, width, height, gradient, 1)
	}
}

// CreateTDModalRenderer creates a ModalRenderer that uses sidecar's gradient borders
// and fills the entire modal surface (including padding and trailing lines) with the theme surface color.
// Maps td monitor ModalType and depth values to appropriate gradients from the current theme.
func CreateTDModalRenderer() monitor.ModalRenderer {
	return func(content string, width, height int, modalType monitor.ModalType, depth int) string {
		theme := GetCurrentTheme()
		gradient := getTDModalGradient(modalType, depth)
		return RenderGradientBorderWithBg(content, width, height, gradient, 1, theme.Colors.BgSecondary)
	}
}

// deriveSemanticGradientStops creates a 2-stop gradient from a base semantic color,
// blending with the contrast pole of the theme's background so the gradient works
// across dark, light, and custom themes without hardcoded color constants.
func deriveSemanticGradientStops(base, bg string) []string {
	if base == "" {
		base = "#888888"
	}
	pole := MaxContrastPole([]string{bg})
	stop2 := Blend(base, pole, 0.25)
	return []string{base, stop2}
}

// getTDPanelGradient returns the appropriate gradient for a panel state.
func getTDPanelGradient(state monitor.PanelState) Gradient {
	theme := GetCurrentTheme()
	angle := theme.Colors.GradientBorderAngle
	if angle == 0 {
		angle = DefaultGradientAngle
	}

	switch state {
	case monitor.PanelStateActive:
		// Active panel: use theme's active gradient
		colors := theme.Colors.GradientBorderActive
		if len(colors) < 2 {
			colors = []string{theme.Colors.BorderActive, theme.Colors.BorderActive}
		}
		return NewGradient(colors, angle)

	case monitor.PanelStateHover:
		// Hover: lightened version of normal gradient
		colors := theme.Colors.GradientBorderNormal
		if len(colors) < 2 {
			colors = []string{theme.Colors.BorderNormal, theme.Colors.BorderNormal}
		}
		pole := MaxContrastPole([]string{theme.Colors.BgPrimary})
		lightened := make([]string, len(colors))
		for i, c := range colors {
			lightened[i] = Blend(c, pole, 0.3)
		}
		return NewGradient(lightened, angle)

	case monitor.PanelStateDividerHover:
		// Divider hover: derived from theme's Info / Secondary
		base := theme.Colors.Info
		if base == "" {
			base = theme.Colors.Secondary
		}
		return NewGradient(deriveSemanticGradientStops(base, theme.Colors.BgPrimary), angle)

	case monitor.PanelStateDividerActive:
		// Divider active (dragging): derived from theme's Warning
		base := theme.Colors.Warning
		if base == "" {
			base = theme.Colors.Primary
		}
		return NewGradient(deriveSemanticGradientStops(base, theme.Colors.BgPrimary), angle)

	default:
		// Normal panel: use theme's normal gradient
		colors := theme.Colors.GradientBorderNormal
		if len(colors) < 2 {
			colors = []string{theme.Colors.BorderNormal, theme.Colors.BorderNormal}
		}
		return NewGradient(colors, angle)
	}
}

// getTDModalGradient returns the appropriate gradient for a modal type and depth.
func getTDModalGradient(modalType monitor.ModalType, depth int) Gradient {
	theme := GetCurrentTheme()
	angle := theme.Colors.GradientBorderAngle
	if angle == 0 {
		angle = DefaultGradientAngle
	}

	// Check for special modal types first
	switch modalType {
	case monitor.ModalTypeHandoffs:
		// Handoffs: derived from theme's Success
		return NewGradient(deriveSemanticGradientStops(theme.Colors.Success, theme.Colors.BgPrimary), angle)

	case monitor.ModalTypeConfirmation:
		// Confirmation: derived from theme's Error
		return NewGradient(deriveSemanticGradientStops(theme.Colors.Error, theme.Colors.BgPrimary), angle)
	}

	// For other types, use depth-based coloring
	switch depth {
	case 1:
		// Depth 1: active gradient
		colors := theme.Colors.GradientBorderActive
		if len(colors) < 2 {
			colors = []string{theme.Colors.BorderActive, theme.Colors.BorderActive}
		}
		return NewGradient(colors, angle)

	case 2:
		// Depth 2: derived from theme's Info / Secondary
		base := theme.Colors.Info
		if base == "" {
			base = theme.Colors.Secondary
		}
		return NewGradient(deriveSemanticGradientStops(base, theme.Colors.BgPrimary), angle)

	default:
		// Depth 3+: derived from theme's Warning / Accent
		base := theme.Colors.Warning
		if base == "" {
			base = theme.Colors.Accent
		}
		return NewGradient(deriveSemanticGradientStops(base, theme.Colors.BgPrimary), angle)
	}
}
