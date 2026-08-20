package styles

import (
	"regexp"
	"sync"

	"charm.land/lipgloss/v2"
)

// themeMu protects access to themeRegistry and currentTheme for thread safety
var themeMu sync.RWMutex

// hexColorRegex validates hex color codes (#RRGGBB or #RRGGBBAA with alpha)
var hexColorRegex = regexp.MustCompile(`^#[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?$`)

// ColorPalette holds all theme colors
type ColorPalette struct {
	// Brand colors
	Primary   string `json:"primary"`
	Secondary string `json:"secondary"`
	Accent    string `json:"accent"`

	// Status colors
	Success string `json:"success"`
	Warning string `json:"warning"`
	Error   string `json:"error"`
	Info    string `json:"info"`

	// Text colors
	TextPrimary   string `json:"textPrimary"`
	TextSecondary string `json:"textSecondary"`
	TextMuted     string `json:"textMuted"`
	TextSubtle    string `json:"textSubtle"`
	TextSelection string `json:"textSelection"` // Text on selected-row fills (BgTertiary)

	// Background colors
	BgPrimary   string `json:"bgPrimary"`
	BgSecondary string `json:"bgSecondary"`
	BgTertiary  string `json:"bgTertiary"`
	BgOverlay   string `json:"bgOverlay"`

	// SelectionBg is the highlight painted over selected text. Distinct from
	// BgTertiary, which is the selected-row fill and is often too close to the
	// canvas to read as a span highlight. Empty, inverted, or too-dim values
	// are lifted by NormalizePalette toward TargetSelectionSeparation against BgPrimary,
	// staying on the canvas ink pole so selected body text does not invert.
	SelectionBg string `json:"selectionBg"`

	// SurfaceRaised backs small chrome that sits on top of a bar — key hint
	// pills, palette keys, bar chips. Derived from BgPrimary by
	// NormalizePalette when empty; deliberately not BgTertiary, whose value
	// comes from a terminal scheme's selection colour and can be any hue.
	SurfaceRaised string `json:"surfaceRaised"`

	// KeyHintFg is the shortcut key text drawn on SurfaceRaised.
	KeyHintFg string `json:"keyHintFg"`

	// OnPrimary is foreground text drawn on a Primary-coloured fill
	// (focused rows, active chips, the current search match).
	OnPrimary string `json:"onPrimary"`

	// OnWarning is foreground text drawn on a Warning-coloured fill
	// (search match highlights).
	OnWarning string `json:"onWarning"`

	// Border colors
	BorderNormal string `json:"borderNormal"`
	BorderActive string `json:"borderActive"`
	BorderMuted  string `json:"borderMuted"`

	// Gradient border colors (for angled gradient borders on panels)
	GradientBorderActive []string `json:"gradientBorderActive"` // Colors for active panel gradient
	GradientBorderNormal []string `json:"gradientBorderNormal"` // Colors for inactive panel gradient
	GradientBorderAngle  float64  `json:"gradientBorderAngle"`  // Angle in degrees (default: 30)

	// Tab theme configuration
	TabStyle  string   `json:"tabStyle"`  // "gradient", "per-tab", "solid", "minimal", or preset name
	TabColors []string `json:"tabColors"` // Color stops for gradient OR per-tab colors

	// Diff colors
	DiffAddFg    string `json:"diffAddFg"`
	DiffAddBg    string `json:"diffAddBg"`
	DiffRemoveFg string `json:"diffRemoveFg"`
	DiffRemoveBg string `json:"diffRemoveBg"`

	// Additional UI colors
	TextHighlight    string `json:"textHighlight"`    // For subtitle, special text
	ButtonHover      string `json:"buttonHover"`      // Button hover state
	TabTextInactive  string `json:"tabTextInactive"`  // Inactive tab text
	Link             string `json:"link"`             // Hyperlink color
	ToastSuccessText string `json:"toastSuccessText"` // Toast success foreground
	ToastErrorText   string `json:"toastErrorText"`   // Toast error foreground

	// Danger button colors (for destructive action buttons)
	DangerLight  string `json:"dangerLight"`  // Light red for danger button text
	DangerDark   string `json:"dangerDark"`   // Dark red for danger button background
	DangerBright string `json:"dangerBright"` // Bright red for focused danger button bg
	DangerHover  string `json:"dangerHover"`  // Darker red for hover danger button bg
	TextInverse  string `json:"textInverse"`  // Inverse text (white on dark themes)

	// Scrollbar colors
	ScrollbarTrack string `json:"scrollbarTrack"` // Track color (default: TextSubtle)
	ScrollbarThumb string `json:"scrollbarThumb"` // Thumb color (default: TextMuted)

	// Blame age gradient colors (newest → oldest)
	BlameAge1 string `json:"blameAge1"` // < 1 week (light green)
	BlameAge2 string `json:"blameAge2"` // < 1 month (lime)
	BlameAge3 string `json:"blameAge3"` // < 3 months (amber)
	BlameAge4 string `json:"blameAge4"` // < 6 months (orange)
	BlameAge5 string `json:"blameAge5"` // < 1 year (gray)

	// Third-party theme names
	SyntaxTheme   string `json:"syntaxTheme"`   // Chroma theme name
	MarkdownTheme string `json:"markdownTheme"` // Glamour theme name

	// Overview board colours
	ProjectHues []string          `json:"projectHues"` // ordered ramp, cycled by project
	AgentColors map[string]string `json:"agentColors"` // provider name (lowercase) -> hex
	LaneWorking string            `json:"laneWorking"`
	LaneBlocked string            `json:"laneBlocked"`
	LaneDone    string            `json:"laneDone"`
	LaneIdle    string            `json:"laneIdle"`
	LanePaused  string            `json:"lanePaused"`
}

// Theme represents a complete theme configuration
type Theme struct {
	Name        string       `json:"name"`
	DisplayName string       `json:"displayName"`
	Colors      ColorPalette `json:"colors"`
}

// Built-in themes
var (
	// SidecarModernTheme is the launch theme, transcribed from the Agenda TUI
	// Refresh design (docs/guides/active/launch-visual-language.md).
	//
	// Its organising rule is that gold (#c0982f) is the only strong accent in
	// chrome — active tab, cursor, focused fill, key glyphs. Every other colour
	// is earned by semantics: teal for identifiers and open state, green for
	// live/success, red for failing and destructive, purple for reviewable,
	// blue for links. Structure is carried by a seven-step neutral ramp rather
	// than by saturation.
	SidecarModernTheme = Theme{
		Name:        "sidecar-modern",
		DisplayName: "Sidecar Modern",
		Colors: ColorPalette{
			// Brand colors. Primary and Accent are both gold on purpose: the
			// design gives chrome exactly one strong accent.
			Primary:   "#c0982f", // gold - cursor, active tab, focused fill
			Secondary: "#4a8f8f", // teal - identifiers, directories
			Accent:    "#c0982f", // gold - inline code, emphasis

			// Status colors
			Success: "#5b8f63", // green - done / live
			Warning: "#c0982f", // gold - attention, P2
			Error:   "#c06c64", // red - failing, P1, destructive
			Info:    "#4a8f8f", // teal - in progress / open

			// Text ramp
			TextPrimary:   "#cfd3d6",
			TextSecondary: "#8b9298",
			TextMuted:     "#858e95",
			TextSubtle:    "#697177",
			TextSelection: "#ffffff", // selected row title

			// Backgrounds
			BgPrimary:   "#0f1113", // canvas
			BgSecondary: "#131619", // header / footer bars
			BgTertiary:  "#171b1f", // selected row
			BgOverlay:   "#0b0d0ecc",
			// Text-selection highlight: a rich slate-blue tint providing clear
			// chromatic contrast off the canvas while preserving body text legibility.
			SelectionBg: "#264f78",

			// Raised chrome (key-hint pills, bar chips)
			SurfaceRaised: "#22272c",
			KeyHintFg:     "#c0982f", // footer key glyphs are gold
			OnPrimary:     "#0f1113", // canvas ink on a gold fill
			OnWarning:     "#0f1113",

			// Borders: hairline, rule, and the one gold active edge
			BorderNormal: "#3d444a",
			BorderActive: "#c0982f",
			BorderMuted:  "#272d32",

			// Gradient borders: gold to warm orange/red
			GradientBorderActive: []string{"#c0982f", "#b85d3b"},
			GradientBorderNormal: []string{"#3d444a", "#272d32"},
			GradientBorderAngle:  30.0,

			// Tab strip: minimal clean underline style
			TabStyle:  "minimal",
			TabColors: []string{"#c0982f"},

			// Diff: lighter green/red variants so the fill can stay near-black
			DiffAddFg:    "#7fae86",
			DiffAddBg:    "#14201a",
			DiffRemoveFg: "#c97a72",
			DiffRemoveBg: "#201415",

			// Additional UI colors
			TextHighlight:    "#e2e6e9",
			ButtonHover:      "#2f3438",
			TabTextInactive:  "#7b848c",
			Link:             "#4b8fd6",
			ToastSuccessText: "#0f1113",
			ToastErrorText:   "#0f1113",

			// Danger buttons
			DangerLight:  "#d99a94",
			DangerDark:   "#2a1614",
			DangerBright: "#b0574f",
			DangerHover:  "#96453e",
			TextInverse:  "#ffffff",

			// Scrollbar: track is the hairline, thumb the rule tone lifted
			// enough to be findable without becoming chrome.
			ScrollbarTrack: "#1c2126",
			ScrollbarThumb: "#5a6167",

			// Blame age ramp, newest → oldest, walking the semantic accents
			BlameAge1: "#7fae86",
			BlameAge2: "#5b8f63",
			BlameAge3: "#c0982f",
			BlameAge4: "#c06c64",
			BlameAge5: "#7b848c",

			// Third-party themes
			SyntaxTheme:   "sidecar-modern",
			MarkdownTheme: "dark",

			// Overview board
			ProjectHues: []string{"#c0982f", "#4a8f8f", "#5b8f63", "#a57fb9", "#4b8fd6", "#c97a72"},
			AgentColors: map[string]string{
				"claude":      "#c17c5b",
				"codex":       "#8b9298",
				"grok":        "#cfd3d6",
				"antigravity": "#4f9999",
				"gemini":      "#4d90d6",
				"cursor":      "#a57fb9",
			},
			LaneWorking: "#5b8f63",
			LaneBlocked: "#c0982f",
			LaneDone:    "#a57fb9",
			LaneIdle:    "#8b9298",
			LanePaused:  "#7b848c",
		},
	}

	// DefaultTheme aliases SidecarModernTheme for backwards compatibility.
	DefaultTheme = SidecarModernTheme

	// Named aliases for themes in CuratedThemes
	CatppuccinMochaTheme = CuratedThemes["catppuccin-mocha"]
	TokyoNightTheme      = CuratedThemes["tokyonight-storm"]
	DraculaTheme         = CuratedThemes["dracula"]
	NordTheme            = CuratedThemes["nord"]
	SolarizedDarkTheme   = CuratedThemes["solarized-dark"]
	MolokaiTheme         = CuratedThemes["monokai-pro"]
)

// canonicalThemeOrder defines the ordered list of all 21 themes.
var canonicalThemeOrder = []string{
	"sidecar-modern",
	"catppuccin-mocha",
	"tokyonight-storm",
	"gruvbox-dark",
	"dracula",
	"nord",
	"atom-one-dark",
	"kanagawa-wave",
	"rose-pine",
	"everforest-dark",
	"solarized-dark",
	"monokai-pro",
	"night-owl",
	"ayu-mirage",
	"github-dark",
	"synthwave",
	"cobalt2",
	"horizon",
	"shades-of-purple",
	"spacegray-eighties",
	"zenburn",
}

// themeRegistry holds all available themes and backwards compatibility aliases
var themeRegistry = func() map[string]Theme {
	m := map[string]Theme{
		"sidecar-modern": SidecarModernTheme,
		"default":        SidecarModernTheme,
	}
	for k, v := range CuratedThemes {
		m[k] = v
	}
	// Backwards compatibility aliases
	m["tokyo-night"] = CuratedThemes["tokyonight-storm"]
	m["molokai"] = CuratedThemes["monokai-pro"]
	m["catppuccin"] = CuratedThemes["catppuccin-mocha"]
	m["gruvbox"] = CuratedThemes["gruvbox-dark"]
	m["kanagawa"] = CuratedThemes["kanagawa-wave"]
	m["rosepine"] = CuratedThemes["rose-pine"]
	m["everforest"] = CuratedThemes["everforest-dark"]
	m["monokai"] = CuratedThemes["monokai-pro"]
	m["nightowl"] = CuratedThemes["night-owl"]
	m["ayu"] = CuratedThemes["ayu-mirage"]
	m["github"] = CuratedThemes["github-dark"]
	return m
}()

// FreshInstallTheme is the theme a Sidecar with no recorded theme choice lands on.
const FreshInstallTheme = "sidecar-modern"

// currentTheme tracks the active theme name
var currentTheme = "default"
var currentThemeValue = DefaultTheme

// IsValidHexColor checks if a string is a valid hex color code (#RRGGBB or #RRGGBBAA)
func IsValidHexColor(hex string) bool {
	return hexColorRegex.MatchString(hex)
}

// IsValidTheme checks if a theme name exists in the registry
func IsValidTheme(name string) bool {
	themeMu.RLock()
	defer themeMu.RUnlock()
	_, ok := themeRegistry[name]
	return ok
}

// GetTheme returns a theme by name, or the default theme if not found
func GetTheme(name string) Theme {
	themeMu.RLock()
	defer themeMu.RUnlock()
	if theme, ok := themeRegistry[name]; ok {
		return theme
	}
	return DefaultTheme
}

// GetCurrentTheme returns the currently active theme
func GetCurrentTheme() Theme {
	themeMu.RLock()
	theme := currentThemeValue
	themeMu.RUnlock()
	return theme
}

// GetCurrentThemeName returns the name of the currently active theme
func GetCurrentThemeName() string {
	themeMu.RLock()
	defer themeMu.RUnlock()
	return currentTheme
}

// ListThemes returns the names of all available themes in canonical order
func ListThemes() []string {
	themeMu.RLock()
	defer themeMu.RUnlock()
	res := make([]string, len(canonicalThemeOrder))
	copy(res, canonicalThemeOrder)
	return res
}

// RegisterTheme adds a custom theme to the registry
func RegisterTheme(theme Theme) {
	themeMu.Lock()
	defer themeMu.Unlock()
	themeRegistry[theme.Name] = theme
}

// ApplyTheme applies a theme by name, updating all style variables
func ApplyTheme(name string) {
	theme := GetTheme(name)
	ApplyThemeColors(theme)
	themeMu.Lock()
	currentTheme = name
	themeMu.Unlock()
}

// ApplyThemeWithOverrides applies a theme with color overrides from config
func ApplyThemeWithOverrides(name string, overrides map[string]string) {
	theme := GetTheme(name)

	// Apply overrides to the color palette
	if overrides != nil {
		applyOverrides(&theme.Colors, overrides)
	}

	ApplyThemeColors(theme)
	themeMu.Lock()
	currentTheme = name
	themeMu.Unlock()
}

// applyOverrides applies color overrides to a palette.
// Delegates to applySingleOverride which validates hex colors.
func applyOverrides(palette *ColorPalette, overrides map[string]string) {
	for key, value := range overrides {
		applySingleOverride(palette, key, value)
	}
}

// ApplyThemeWithGenericOverrides applies a theme with overrides that may include arrays.
// This supports gradient array overrides from YAML config.
func ApplyThemeWithGenericOverrides(name string, overrides map[string]interface{}) {
	theme := GetTheme(name)

	// Apply overrides to the color palette
	if overrides != nil {
		applyGenericOverrides(&theme.Colors, overrides)
	}

	ApplyThemeColors(theme)
	themeMu.Lock()
	currentTheme = name
	themeMu.Unlock()
}

// applyGenericOverrides applies overrides that may include arrays (for gradients).
func applyGenericOverrides(palette *ColorPalette, overrides map[string]interface{}) {
	for key, value := range overrides {
		switch v := value.(type) {
		case string:
			applySingleOverride(palette, key, v)
		case []interface{}:
			// Handle array values (for gradient colors)
			colors := make([]string, 0, len(v))
			for _, item := range v {
				if s, ok := item.(string); ok {
					colors = append(colors, s)
				}
			}
			applyArrayOverride(palette, key, colors)
		case []string:
			applyArrayOverride(palette, key, v)
		case map[string]interface{}:
			colors := make(map[string]string, len(v))
			for k, item := range v {
				if s, ok := item.(string); ok {
					colors[k] = s
				}
			}
			applyMapOverride(palette, key, colors)
		case map[string]string:
			applyMapOverride(palette, key, v)
		case float64:
			applyFloatOverride(palette, key, v)
		case int:
			applyFloatOverride(palette, key, float64(v))
		}
	}
}

// applySingleOverride applies a single string override.
// Color values must be valid hex colors (#RRGGBB). Invalid colors are silently ignored.
func applySingleOverride(palette *ColorPalette, key, value string) {
	// syntaxTheme, markdownTheme, and tabStyle are names, not colors
	isThemeName := key == "syntaxTheme" || key == "markdownTheme" || key == "tabStyle"
	if !isThemeName && !IsValidHexColor(value) {
		return // Skip invalid hex color
	}

	switch key {
	case "primary":
		palette.Primary = value
	case "secondary":
		palette.Secondary = value
	case "accent":
		palette.Accent = value
	case "success":
		palette.Success = value
	case "warning":
		palette.Warning = value
	case "error":
		palette.Error = value
	case "info":
		palette.Info = value
	case "textPrimary":
		palette.TextPrimary = value
	case "textSecondary":
		palette.TextSecondary = value
	case "textMuted":
		palette.TextMuted = value
	case "textSubtle":
		palette.TextSubtle = value
	case "textSelection":
		palette.TextSelection = value
	case "bgPrimary":
		palette.BgPrimary = value
	case "bgSecondary":
		palette.BgSecondary = value
	case "bgTertiary":
		palette.BgTertiary = value
	case "bgOverlay":
		palette.BgOverlay = value
	case "selectionBg":
		palette.SelectionBg = value
	case "surfaceRaised":
		palette.SurfaceRaised = value
	case "keyHintFg":
		palette.KeyHintFg = value
	case "onPrimary":
		palette.OnPrimary = value
	case "onWarning":
		palette.OnWarning = value
	case "borderNormal":
		palette.BorderNormal = value
	case "borderActive":
		palette.BorderActive = value
	case "borderMuted":
		palette.BorderMuted = value
	case "diffAddFg":
		palette.DiffAddFg = value
	case "diffAddBg":
		palette.DiffAddBg = value
	case "diffRemoveFg":
		palette.DiffRemoveFg = value
	case "diffRemoveBg":
		palette.DiffRemoveBg = value
	case "textHighlight":
		palette.TextHighlight = value
	case "buttonHover":
		palette.ButtonHover = value
	case "tabTextInactive":
		palette.TabTextInactive = value
	case "link":
		palette.Link = value
	case "toastSuccessText":
		palette.ToastSuccessText = value
	case "toastErrorText":
		palette.ToastErrorText = value
	case "syntaxTheme":
		palette.SyntaxTheme = value
	case "markdownTheme":
		palette.MarkdownTheme = value
	case "tabStyle":
		palette.TabStyle = value
	case "dangerLight":
		palette.DangerLight = value
	case "dangerDark":
		palette.DangerDark = value
	case "dangerBright":
		palette.DangerBright = value
	case "dangerHover":
		palette.DangerHover = value
	case "textInverse":
		palette.TextInverse = value
	case "blameAge1":
		palette.BlameAge1 = value
	case "blameAge2":
		palette.BlameAge2 = value
	case "blameAge3":
		palette.BlameAge3 = value
	case "blameAge4":
		palette.BlameAge4 = value
	case "blameAge5":
		palette.BlameAge5 = value
	case "scrollbarTrack":
		palette.ScrollbarTrack = value
	case "scrollbarThumb":
		palette.ScrollbarThumb = value
	case "laneWorking":
		palette.LaneWorking = value
	case "laneBlocked":
		palette.LaneBlocked = value
	case "laneDone":
		palette.LaneDone = value
	case "laneIdle":
		palette.LaneIdle = value
	case "lanePaused":
		palette.LanePaused = value
	}
}

// applyArrayOverride applies an array override (for gradient colors).
// All colors must be valid hex colors. The entire array is rejected if any color is invalid.
func applyArrayOverride(palette *ColorPalette, key string, colors []string) {
	// Validate all colors in the array
	for _, c := range colors {
		if !IsValidHexColor(c) {
			return // Reject entire array if any color is invalid
		}
	}

	switch key {
	case "gradientBorderActive":
		palette.GradientBorderActive = colors
	case "gradientBorderNormal":
		palette.GradientBorderNormal = colors
	case "tabColors":
		palette.TabColors = colors
	case "projectHues":
		palette.ProjectHues = colors
	}
}

// applyMapOverride applies a map override (for AgentColors, the first
// map-valued palette field). Invalid entries are dropped individually rather
// than rejecting the whole map, since each key is an independent provider.
func applyMapOverride(palette *ColorPalette, key string, colors map[string]string) {
	switch key {
	case "agentColors":
		cloned := make(map[string]string, len(palette.AgentColors)+len(colors))
		for k, v := range palette.AgentColors {
			cloned[k] = v
		}
		for k, v := range colors {
			if IsValidHexColor(v) {
				cloned[k] = v
			}
		}
		palette.AgentColors = cloned
	}
}

// applyFloatOverride applies a float override (for gradient angle).
func applyFloatOverride(palette *ColorPalette, key string, value float64) {
	switch key {
	case "gradientBorderAngle":
		palette.GradientBorderAngle = value
	}
}

// ApplyThemeColors updates all style package variables from a theme.
//
// IMPORTANT: This function is NOT thread-safe for concurrent reads.
// It must only be called during initialization, before the TUI starts.
// The TUI's single-threaded Bubble Tea model ensures safe access after init.
func ApplyThemeColors(theme Theme) {
	// Enforce contrast on the finished palette, whatever produced it: a
	// built-in theme, a converted community scheme, or user overrides.
	c := NormalizePalette(theme.Colors)
	theme.Colors = c

	// Update color variables
	Primary = lipgloss.Color(c.Primary)
	Secondary = lipgloss.Color(c.Secondary)
	Accent = lipgloss.Color(c.Accent)

	Success = lipgloss.Color(c.Success)
	Warning = lipgloss.Color(c.Warning)
	Error = lipgloss.Color(c.Error)
	Info = lipgloss.Color(c.Info)

	TextPrimary = lipgloss.Color(c.TextPrimary)
	TextSecondary = lipgloss.Color(c.TextSecondary)
	TextMuted = lipgloss.Color(c.TextMuted)
	TextSubtle = lipgloss.Color(c.TextSubtle)
	// TextSelectionColor with fallback to TextPrimary
	if c.TextSelection != "" {
		TextSelectionColor = lipgloss.Color(c.TextSelection)
	} else {
		TextSelectionColor = lipgloss.Color(c.TextPrimary)
	}

	BgPrimary = lipgloss.Color(c.BgPrimary)
	BgSecondary = lipgloss.Color(c.BgSecondary)
	BgTertiary = lipgloss.Color(c.BgTertiary)
	BgOverlay = lipgloss.Color(c.BgOverlay)
	CardSelectedBg = lipgloss.Color(cardSelectedBgHex(c.BgPrimary, c.BgSecondary))

	SurfaceRaised = lipgloss.Color(c.SurfaceRaised)
	KeyHintFgColor = lipgloss.Color(c.KeyHintFg)
	OnPrimaryColor = lipgloss.Color(c.OnPrimary)
	OnWarningColor = lipgloss.Color(c.OnWarning)

	BorderNormal = lipgloss.Color(c.BorderNormal)
	BorderActive = lipgloss.Color(c.BorderActive)
	BorderMuted = lipgloss.Color(c.BorderMuted)

	DiffAddFg = lipgloss.Color(c.DiffAddFg)
	DiffAddBg = lipgloss.Color(c.DiffAddBg)
	DiffRemoveFg = lipgloss.Color(c.DiffRemoveFg)
	DiffRemoveBg = lipgloss.Color(c.DiffRemoveBg)

	TextHighlight = lipgloss.Color(c.TextHighlight)
	ButtonHoverColor = lipgloss.Color(c.ButtonHover)
	TabTextInactiveColor = lipgloss.Color(c.TabTextInactive)
	LinkColor = lipgloss.Color(c.Link)
	ToastSuccessTextColor = lipgloss.Color(c.ToastSuccessText)
	ToastErrorTextColor = lipgloss.Color(c.ToastErrorText)

	// Danger button colors (with defaults)
	if c.DangerLight != "" {
		DangerLight = lipgloss.Color(c.DangerLight)
	}
	if c.DangerDark != "" {
		DangerDark = lipgloss.Color(c.DangerDark)
	}
	if c.DangerBright != "" {
		DangerBright = lipgloss.Color(c.DangerBright)
	}
	if c.DangerHover != "" {
		DangerHover = lipgloss.Color(c.DangerHover)
	}
	if c.TextInverse != "" {
		TextInverse = lipgloss.Color(c.TextInverse)
	}

	// Blame age gradient colors (with defaults)
	if c.BlameAge1 != "" {
		BlameAge1 = lipgloss.Color(c.BlameAge1)
	}
	if c.BlameAge2 != "" {
		BlameAge2 = lipgloss.Color(c.BlameAge2)
	}
	if c.BlameAge3 != "" {
		BlameAge3 = lipgloss.Color(c.BlameAge3)
	}
	if c.BlameAge4 != "" {
		BlameAge4 = lipgloss.Color(c.BlameAge4)
	}
	if c.BlameAge5 != "" {
		BlameAge5 = lipgloss.Color(c.BlameAge5)
	}

	// Scrollbar colors (with fallback to TextSubtle/TextMuted)
	if c.ScrollbarTrack != "" {
		ScrollbarTrackColor = lipgloss.Color(c.ScrollbarTrack)
	} else {
		ScrollbarTrackColor = TextSubtle
	}
	if c.ScrollbarThumb != "" {
		ScrollbarThumbColor = lipgloss.Color(c.ScrollbarThumb)
	} else {
		ScrollbarThumbColor = TextMuted
	}

	// Store the syntax theme name for external use. The Markdown theme is read
	// from the palette snapshot by internal/markdown, not from a global.
	CurrentSyntaxTheme = c.SyntaxTheme

	// Update tab theme state
	CurrentTabStyle = c.TabStyle
	CurrentTabColors = parseTabColors(c.TabColors)

	projectHues, agentColors, laneColors := overviewColorState(c)

	themeMu.Lock()
	currentThemeValue = theme
	currentProjectHues = projectHues
	currentAgentColors = agentColors
	currentLaneColors = laneColors
	themeMu.Unlock()

	// Rebuild all styles that depend on these colors
	rebuildStyles()
}

// rebuildStyles recreates all lipgloss styles with current colors
func rebuildStyles() {
	// Panel styles
	PanelActive = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(BorderActive).
		Padding(0, 1)

	PanelInactive = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(BorderNormal).
		Padding(0, 1)

	PanelHeader = lipgloss.NewStyle().
		Bold(true).
		Foreground(TextPrimary).
		MarginBottom(1)

	PanelNoBorder = lipgloss.NewStyle().
		Padding(0, 1)

	// Text styles
	Title = lipgloss.NewStyle().
		Bold(true).
		Foreground(TextPrimary)

	Subtitle = lipgloss.NewStyle().
		Foreground(TextHighlight)

	// WorktreeIndicator shows the current worktree branch in the header
	WorktreeIndicator = lipgloss.NewStyle().
		Foreground(Warning).
		Bold(true)

	Body = lipgloss.NewStyle().
		Foreground(TextPrimary)

	Muted = lipgloss.NewStyle().
		Foreground(TextMuted)

	Subtle = lipgloss.NewStyle().
		Foreground(TextSubtle)

	Code = lipgloss.NewStyle().
		Foreground(Accent)

	Link = lipgloss.NewStyle().
		Foreground(LinkColor).
		Underline(true)

	KeyHint = lipgloss.NewStyle().
		Foreground(KeyHintFgColor).
		Background(SurfaceRaised).
		Padding(0, 1)

	Logo = lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true)

	// Status indicator styles
	StatusStaged = lipgloss.NewStyle().
		Foreground(Success).
		Bold(true)

	StatusModified = lipgloss.NewStyle().
		Foreground(Warning).
		Bold(true)

	ToastSuccess = lipgloss.NewStyle().
		Background(Success).
		Foreground(ToastSuccessTextColor).
		Bold(true).
		Padding(0, 1)

	ToastError = lipgloss.NewStyle().
		Background(Error).
		Foreground(ToastErrorTextColor).
		Bold(true).
		Padding(0, 1)

	StatusUntracked = lipgloss.NewStyle().
		Foreground(TextMuted)

	StatusDeleted = lipgloss.NewStyle().
		Foreground(Error).
		Bold(true)

	StatusInProgress = lipgloss.NewStyle().
		Foreground(Info).
		Bold(true)

	StatusCompleted = lipgloss.NewStyle().
		Foreground(Success)

	StatusBlocked = lipgloss.NewStyle().
		Foreground(Error)

	StatusPending = lipgloss.NewStyle().
		Foreground(TextMuted)

	// List item styles
	ListItemNormal = lipgloss.NewStyle().
		Foreground(TextPrimary)

	ListItemSelected = lipgloss.NewStyle().
		Foreground(TextSelectionColor).
		Background(BgTertiary)

	// Multi-coloured kanban/overview cards: darker fill so coloured spans stay readable.
	CardSelected = lipgloss.NewStyle().
		Foreground(TextSelectionColor).
		Background(CardSelectedBg)

	ListItemFocused = lipgloss.NewStyle().
		Foreground(OnPrimaryColor).
		Background(Primary)

	ListCursor = lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true)

	// Bar element styles
	BarTitle = lipgloss.NewStyle().
		Foreground(TextPrimary).
		Bold(true)

	BrandLogo = lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true)

	HeaderDivider = lipgloss.NewStyle().
		Foreground(TextMuted)

	BarText = lipgloss.NewStyle().
		Foreground(TextMuted)

	BarChip = lipgloss.NewStyle().
		Foreground(KeyHintFgColor).
		Background(SurfaceRaised).
		Padding(0, 1)

	BarChipActive = lipgloss.NewStyle().
		Foreground(OnPrimaryColor).
		Background(Primary).
		Padding(0, 1).
		Bold(true)

	ProjectSelector = lipgloss.NewStyle().
		Foreground(Primary).
		Background(SurfaceRaised).
		Padding(0, 1).
		Bold(true)

	GlobalHeaderAction = lipgloss.NewStyle().
		Foreground(Primary).
		Padding(0, 1).
		Bold(true)

	ProjectRestore = lipgloss.NewStyle().
		Foreground(TextSecondary).
		Padding(0, 1)

	// Tab styles
	TabTextActive = lipgloss.NewStyle().
		Foreground(TextPrimary).
		Bold(true)

	TabTextInactive = lipgloss.NewStyle().
		Foreground(TabTextInactiveColor)

	// Diff line styles
	DiffAdd = lipgloss.NewStyle().
		Foreground(Success)

	DiffRemove = lipgloss.NewStyle().
		Foreground(Error)

	DiffContext = lipgloss.NewStyle().
		Foreground(TextMuted)

	DiffHeader = lipgloss.NewStyle().
		Foreground(Info).
		Bold(true)

	// File browser styles
	FileBrowserDir = lipgloss.NewStyle().
		Foreground(Secondary).
		Bold(true)

	FileBrowserFile = lipgloss.NewStyle().
		Foreground(TextPrimary)

	FileBrowserIgnored = lipgloss.NewStyle().
		Foreground(TextSubtle)

	FileBrowserLineNumber = lipgloss.NewStyle().
		Foreground(TextMuted).
		Width(5).
		AlignHorizontal(lipgloss.Right)

	FileBrowserIcon = lipgloss.NewStyle().
		Foreground(TextMuted)

	// Drop target while dragging a file onto a directory. Built from the
	// theme's Primary, so every theme gets a highlight that is distinct from
	// ListItemSelected's BgTertiary - both are visible at once during a drag.
	// The foreground is the theme's dark background, not TextPrimary: TextPrimary
	// on Primary is under 2:1 contrast in most built-in themes, which erased the
	// filename on the one row the gesture depends on being readable.
	FileBrowserDropTarget = lipgloss.NewStyle().
		Foreground(OnPrimaryColor).
		Background(Primary).
		Bold(true)

	// The row being dragged, dimmed so it reads as "in flight".
	FileBrowserDragSource = lipgloss.NewStyle().
		Foreground(TextSubtle).
		Italic(true)

	SearchMatch = lipgloss.NewStyle().
		Background(Warning).
		Foreground(OnWarningColor)

	SearchMatchCurrent = lipgloss.NewStyle().
		Background(Primary).
		Foreground(OnPrimaryColor)

	FuzzyMatchChar = lipgloss.NewStyle().
		Foreground(Primary).
		Bold(true)

	QuickOpenItem = lipgloss.NewStyle().
		Foreground(TextPrimary)

	QuickOpenItemSelected = lipgloss.NewStyle().
		Foreground(TextSelectionColor).
		Background(BgTertiary)

	PaletteEntry = lipgloss.NewStyle().
		Foreground(TextPrimary)

	PaletteEntrySelected = lipgloss.NewStyle().
		Foreground(TextSelectionColor).
		Background(BgTertiary)

	PaletteKey = lipgloss.NewStyle().
		Foreground(KeyHintFgColor).
		Background(SurfaceRaised).
		Padding(0, 1)

	TextSelection = lipgloss.NewStyle().
		Background(BgTertiary).
		Foreground(TextSelectionColor)

	// Footer and header
	Footer = lipgloss.NewStyle().
		Foreground(TextMuted).
		Background(BgSecondary)

	Header = lipgloss.NewStyle().
		Background(BgSecondary)

	HeaderGlobal = lipgloss.NewStyle().
		Background(BgTertiary)

	// Modal styles
	ModalOverlay = lipgloss.NewStyle().
		Background(BgOverlay)

	ModalBox = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Primary).
		Background(BgSecondary).
		Padding(1, 2)

	ModalTitle = lipgloss.NewStyle().
		Foreground(TextPrimary).
		Bold(true).
		MarginBottom(1)

	// Button styles
	Button = lipgloss.NewStyle().
		Foreground(TextSecondary).
		Background(BgTertiary).
		Padding(0, 2)

	ButtonFocused = lipgloss.NewStyle().
		Foreground(TextPrimary).
		Background(Primary).
		Padding(0, 2).
		Bold(true)

	ButtonHover = lipgloss.NewStyle().
		Foreground(TextPrimary).
		Background(ButtonHoverColor).
		Padding(0, 2)

	// Danger button styles
	ButtonDanger = lipgloss.NewStyle().
		Foreground(DangerLight).
		Background(DangerDark).
		Padding(0, 2)

	ButtonDangerFocused = lipgloss.NewStyle().
		Foreground(TextInverse).
		Background(DangerBright).
		Padding(0, 2).
		Bold(true)

	ButtonDangerHover = lipgloss.NewStyle().
		Foreground(TextInverse).
		Background(DangerHover).
		Padding(0, 2)
}

// GetSyntaxTheme returns the current syntax highlighting theme name
func GetSyntaxTheme() string {
	return CurrentSyntaxTheme
}
