package community

import (
	"fmt"
	"math"
	"strings"

	"github.com/marcus/sidecar/internal/styles"
)

// Convert maps a CommunityScheme to a full Sidecar ColorPalette following the Sidecar Modern design principles.
func Convert(scheme *CommunityScheme) styles.ColorPalette {
	bg := scheme.Background
	fg := scheme.Foreground
	isDark := styles.IsDarkBackground(bg)

	// 1. Structured Neutral Ramp
	var (
		bgSecondary   string // header & footer framing bars
		bgTertiary    string // selected row background
		surfaceRaised string // key-hint pills, chips
		borderNormal  string // pane dividers, rules
		borderMuted   string // subtle hairlines
	)

	if isDark {
		bgSecondary = Darken(bg, 0.18)
		bgTertiary = Lighten(bg, 0.08)
		surfaceRaised = Lighten(bg, 0.12)
		borderNormal = Lighten(bg, 0.22)
		borderMuted = Lighten(bg, 0.10)
	} else {
		bgSecondary = Lighten(bg, 0.18)
		bgTertiary = Darken(bg, 0.08)
		surfaceRaised = Darken(bg, 0.12)
		borderNormal = Darken(bg, 0.22)
		borderMuted = Darken(bg, 0.10)
	}

	// All background fills content or accents can land on
	fills := []string{bg, bgSecondary, bgTertiary}
	textSurfaces := []string{bg, bgSecondary, bgTertiary, surfaceRaised}

	// 2. Signature Chrome Primary Accent
	// Intelligently select brand color based on theme identity
	signaturePrimary, signatureSecondary := pickSignatureAccents(scheme)

	primary := EnsureContrastOn(signaturePrimary, fills, 4.5)
	secondary := EnsureContrastOn(signatureSecondary, fills, 4.5)
	accent := primary

	// 3. Status & Semantic Colors (Earned by meaning)
	success := EnsureContrastOn(firstNonEmpty(scheme.Green, scheme.BrightGreen, "#a6e3a1"), fills, 4.5)
	warning := EnsureContrastOn(firstNonEmpty(scheme.Yellow, scheme.BrightYellow, "#f9e2af"), fills, 4.5)
	errorCol := EnsureContrastOn(firstNonEmpty(scheme.Red, scheme.BrightRed, "#f38ba8"), fills, 4.5)
	info := EnsureContrastOn(firstNonEmpty(scheme.Cyan, scheme.BrightCyan, secondary), fills, 4.5)

	// 4. Typography Ramp
	textPrimary := EnsureContrastOn(fg, textSurfaces, 4.5)
	textSecondary := EnsureContrastOn(Blend(fg, bg, 0.25), textSurfaces, 4.5)
	textMuted := EnsureContrastOn(Blend(fg, bg, 0.40), textSurfaces, 4.5)
	textSubtle := EnsureContrastOn(Blend(fg, bg, 0.55), []string{bg, bgSecondary, surfaceRaised}, 3.0)
	tabTextInactive := EnsureContrastOn(Blend(fg, bg, 0.50), []string{bgSecondary, surfaceRaised}, 3.0)

	maxContrast := styles.MaxContrastPole(fills)
	textHighlight := EnsureContrastOn(maxContrast, fills, 4.5)
	textSelection := textHighlight

	keyHintFg := EnsureContrastOn(primary, []string{surfaceRaised}, 4.5)

	// Inking on fills
	onPrimary := inkOnColor(primary)
	onWarning := inkOnColor(warning)

	// 5. Danger Actions
	dangerDark := Blend(bg, errorCol, 0.20)
	dangerBright := errorCol
	var dangerLight string
	if isDark {
		dangerLight = EnsureContrastOn(Lighten(errorCol, 0.30), []string{dangerDark}, 4.5)
	} else {
		dangerLight = EnsureContrastOn(Darken(errorCol, 0.30), []string{dangerDark}, 4.5)
	}
	var dangerHover string
	if isDark {
		dangerHover = Darken(errorCol, 0.15)
	} else {
		dangerHover = Lighten(errorCol, 0.15)
	}
	textInverse := inkOnColor(dangerBright)

	// 6. Diff Backgrounds & Foreground
	diffAddBg := Blend(bg, success, 0.15)
	diffAddFg := EnsureContrastOn(success, []string{diffAddBg}, 4.5)
	diffRemoveBg := Blend(bg, errorCol, 0.15)
	diffRemoveFg := EnsureContrastOn(errorCol, []string{diffRemoveBg}, 4.5)

	// 7. Toasts
	toastSuccessText := inkOnColor(success)
	toastErrorText := inkOnColor(errorCol)

	// 8. Overview & Lane Colors
	purple := EnsureContrastOn(firstNonEmpty(scheme.Purple, scheme.BrightPurple, secondary), fills, 4.5)
	projectHues := []string{primary, secondary, success, warning, purple, errorCol}

	return styles.ColorPalette{
		Primary:   primary,
		Secondary: secondary,
		Accent:    accent,

		Success: success,
		Warning: warning,
		Error:   errorCol,
		Info:    info,

		TextPrimary:   textPrimary,
		TextSecondary: textSecondary,
		TextMuted:     textMuted,
		TextSubtle:    textSubtle,
		TextSelection: textSelection,
		TextHighlight: textHighlight,

		BgPrimary:   bg,
		BgSecondary: bgSecondary,
		BgTertiary:  bgTertiary,
		BgOverlay:   WithAlpha(bg, "cc"),

		SurfaceRaised: surfaceRaised,
		KeyHintFg:     keyHintFg,
		OnPrimary:     onPrimary,
		OnWarning:     onWarning,

		BorderNormal: borderNormal,
		BorderActive: primary,
		BorderMuted:  borderMuted,

		GradientBorderActive: []string{primary, secondary},
		GradientBorderNormal: []string{borderNormal, borderMuted},
		GradientBorderAngle:  30.0,

		TabStyle:  "minimal",
		TabColors: []string{primary},

		DiffAddFg:    diffAddFg,
		DiffAddBg:    diffAddBg,
		DiffRemoveFg: diffRemoveFg,
		DiffRemoveBg: diffRemoveBg,

		ButtonHover:      borderMuted,
		TabTextInactive:  tabTextInactive,
		Link:             primary,
		ToastSuccessText: toastSuccessText,
		ToastErrorText:   toastErrorText,

		DangerLight:  dangerLight,
		DangerDark:   dangerDark,
		DangerBright: dangerBright,
		DangerHover:  dangerHover,
		TextInverse:  textInverse,

		ScrollbarTrack: bgTertiary,
		ScrollbarThumb: borderNormal,

		BlameAge1: success,
		BlameAge2: secondary,
		BlameAge3: warning,
		BlameAge4: errorCol,
		BlameAge5: textMuted,

		SyntaxTheme:   matchSyntaxTheme(bg),
		MarkdownTheme: markdownTheme(isDark),

		ProjectHues: projectHues,
		AgentColors: map[string]string{
			"claude":      warning,
			"codex":       textSecondary,
			"grok":        textPrimary,
			"antigravity": secondary,
			"gemini":      primary,
			"cursor":      purple,
		},
		LaneWorking: success,
		LaneBlocked: warning,
		LaneDone:    purple,
		LaneIdle:    textSecondary,
		LanePaused:  textMuted,
	}
}

// pickSignatureAccents chooses the primary and secondary signature accents based on theme metadata.
func pickSignatureAccents(s *CommunityScheme) (string, string) {
	nameLower := strings.ToLower(s.Name)

	switch {
	case strings.Contains(nameLower, "gruvbox"):
		return firstNonEmpty(s.BrightYellow, s.Yellow, "#fabd2f"), firstNonEmpty(s.Cyan, s.BrightCyan, "#8ec07c")
	case strings.Contains(nameLower, "solarized"):
		return firstNonEmpty(s.Yellow, s.BrightYellow, "#b58900"), firstNonEmpty(s.Cyan, s.BrightCyan, "#2aa198")
	case strings.Contains(nameLower, "monokai"):
		return firstNonEmpty(s.Yellow, s.BrightYellow, "#ffd866"), firstNonEmpty(s.Green, s.BrightGreen, "#a6e22e")
	case strings.Contains(nameLower, "synthwave") || strings.Contains(nameLower, "cyberpunk") || strings.Contains(nameLower, "shades of purple"):
		return firstNonEmpty(s.BrightPurple, s.Purple, "#ff77ff"), firstNonEmpty(s.BrightCyan, s.Cyan, "#00f0ff")
	case strings.Contains(nameLower, "dracula"):
		return firstNonEmpty(s.Purple, s.BrightPurple, "#bd93f9"), firstNonEmpty(s.Cyan, s.BrightCyan, "#8be9fd")
	case strings.Contains(nameLower, "everforest") || strings.Contains(nameLower, "zenburn"):
		return firstNonEmpty(s.Green, s.BrightGreen, "#a7c080"), firstNonEmpty(s.Yellow, s.BrightYellow, "#dbbc7f")
	case strings.Contains(nameLower, "rose pine") || strings.Contains(nameLower, "rose-pine"):
		return firstNonEmpty(s.BrightRed, s.Red, "#eb6f92"), firstNonEmpty(s.BrightCyan, s.Cyan, "#9ccfd8")
	case strings.Contains(nameLower, "horizon"):
		return firstNonEmpty(s.BrightRed, s.Red, "#e95678"), firstNonEmpty(s.Cyan, s.BrightCyan, "#26bbd9")
	case strings.Contains(nameLower, "cobalt"):
		return firstNonEmpty(s.Yellow, s.BrightYellow, "#ffe50a"), firstNonEmpty(s.Blue, s.BrightBlue, "#0088ff")
	default:
		// Default to Blue / Cyan or first high-contrast bright color
		primary := firstNonEmpty(s.Blue, s.BrightBlue, "#89b4fa")
		secondary := firstNonEmpty(s.Cyan, s.BrightCyan, s.Purple, "#94e2d5")
		return primary, secondary
	}
}

// inkOnColor returns a high-contrast foreground ink for drawing on a colored fill.
func inkOnColor(bgHex string) string {
	if Luminance(bgHex) > 0.25 {
		return "#11111b"
	}
	return "#ffffff"
}

func firstNonEmpty(candidates ...string) string {
	for _, c := range candidates {
		if c != "" && styles.IsValidHexColor(c) {
			return c
		}
	}
	return "#ffffff"
}

// PaletteToOverrides serializes a ColorPalette to the override map format for config.json.
func PaletteToOverrides(p styles.ColorPalette) map[string]interface{} {
	m := map[string]interface{}{
		"primary":          p.Primary,
		"secondary":        p.Secondary,
		"accent":           p.Accent,
		"success":          p.Success,
		"warning":          p.Warning,
		"error":            p.Error,
		"info":             p.Info,
		"textPrimary":      p.TextPrimary,
		"textSecondary":    p.TextSecondary,
		"textMuted":        p.TextMuted,
		"textSubtle":       p.TextSubtle,
		"textSelection":    p.TextSelection,
		"textHighlight":    p.TextHighlight,
		"bgPrimary":        p.BgPrimary,
		"bgSecondary":      p.BgSecondary,
		"bgTertiary":       p.BgTertiary,
		"bgOverlay":        p.BgOverlay,
		"surfaceRaised":    p.SurfaceRaised,
		"keyHintFg":        p.KeyHintFg,
		"onPrimary":        p.OnPrimary,
		"onWarning":        p.OnWarning,
		"borderNormal":     p.BorderNormal,
		"borderActive":     p.BorderActive,
		"borderMuted":      p.BorderMuted,
		"diffAddFg":        p.DiffAddFg,
		"diffAddBg":        p.DiffAddBg,
		"diffRemoveFg":     p.DiffRemoveFg,
		"diffRemoveBg":     p.DiffRemoveBg,
		"buttonHover":      p.ButtonHover,
		"tabTextInactive":  p.TabTextInactive,
		"link":             p.Link,
		"toastSuccessText": p.ToastSuccessText,
		"toastErrorText":   toastErrorText(p),
		"dangerLight":      p.DangerLight,
		"dangerDark":       p.DangerDark,
		"dangerBright":     p.DangerBright,
		"dangerHover":      p.DangerHover,
		"textInverse":      p.TextInverse,
		"scrollbarTrack":   p.ScrollbarTrack,
		"scrollbarThumb":   p.ScrollbarThumb,
		"blameAge1":        p.BlameAge1,
		"blameAge2":        p.BlameAge2,
		"blameAge3":        p.BlameAge3,
		"blameAge4":        p.BlameAge4,
		"blameAge5":        p.BlameAge5,
		"laneWorking":      p.LaneWorking,
		"laneBlocked":      p.LaneBlocked,
		"laneDone":         p.LaneDone,
		"laneIdle":         p.LaneIdle,
		"lanePaused":       p.LanePaused,
		"syntaxTheme":      p.SyntaxTheme,
		"markdownTheme":    p.MarkdownTheme,
		"tabStyle":         p.TabStyle,
	}

	if len(p.GradientBorderActive) > 0 {
		arr := make([]interface{}, len(p.GradientBorderActive))
		for i, c := range p.GradientBorderActive {
			arr[i] = c
		}
		m["gradientBorderActive"] = arr
	}
	if len(p.GradientBorderNormal) > 0 {
		arr := make([]interface{}, len(p.GradientBorderNormal))
		for i, c := range p.GradientBorderNormal {
			arr[i] = c
		}
		m["gradientBorderNormal"] = arr
	}
	m["gradientBorderAngle"] = p.GradientBorderAngle
	if len(p.TabColors) > 0 {
		arr := make([]interface{}, len(p.TabColors))
		for i, c := range p.TabColors {
			arr[i] = c
		}
		m["tabColors"] = arr
	}
	if len(p.ProjectHues) > 0 {
		arr := make([]interface{}, len(p.ProjectHues))
		for i, c := range p.ProjectHues {
			arr[i] = c
		}
		m["projectHues"] = arr
	}
	if len(p.AgentColors) > 0 {
		agents := make(map[string]interface{}, len(p.AgentColors))
		for k, v := range p.AgentColors {
			agents[k] = v
		}
		m["agentColors"] = agents
	}

	return m
}

func toastErrorText(p styles.ColorPalette) string {
	if p.ToastErrorText != "" {
		return p.ToastErrorText
	}
	return inkOnColor(p.Error)
}

func markdownTheme(isDark bool) string {
	if isDark {
		return "dark"
	}
	return "light"
}

// Known Chroma theme backgrounds for matching.
var chromaThemes = map[string]string{
	"monokai":          "#272822",
	"dracula":          "#282a36",
	"nord":             "#2e3440",
	"solarized-dark":   "#002b36",
	"github":           "#ffffff",
	"github-dark":      "#24292e",
	"onedark":          "#282c34",
	"gruvbox":          "#282828",
	"catppuccin-mocha": "#1e1e2e",
	"vs":               "#ffffff",
	"solarized-light":  "#fdf6e3",
}

// matchSyntaxTheme finds the closest Chroma theme by background color.
func matchSyntaxTheme(bg string) string {
	isDark := styles.IsDarkBackground(bg)
	best := "monokai"
	if !isDark {
		best = "github"
	}
	bestDist := math.MaxFloat64

	for name, themeBg := range chromaThemes {
		themeIsDark := styles.IsDarkBackground(themeBg)
		if themeIsDark != isDark {
			continue
		}
		dist := ColorDistance(bg, themeBg)
		if dist < bestDist {
			bestDist = dist
			best = name
		}
	}

	if bestDist > 100 {
		if isDark {
			return "monokai"
		}
		return "github"
	}
	return best
}

// FormatSchemeInfo returns a brief description for display.
func FormatSchemeInfo(scheme *CommunityScheme) string {
	mode := "dark"
	if !styles.IsDarkBackground(scheme.Background) {
		mode = "light"
	}
	return fmt.Sprintf("%s (%s)", scheme.Name, mode)
}
