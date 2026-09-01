package styles

import (
	"hash/fnv"
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// defaultProjectHues is the package-level ramp NormalizePalette falls back to
// when a theme supplies neither ProjectHues nor TabColors.
var defaultProjectHues = []string{"#A78BFA", "#22D3EE", "#FB923C", "#F472B6", "#60A5FA", "#A3E635"}

// minProjectHues is the shortest ramp that can still separate one project from
// another. Molokai, Nord and Solarized Dark each ship a single tab colour,
// which the TabColors fallback would otherwise borrow verbatim and hash every
// project onto one spine. A ramp that short is treated as no ramp at all.
const minProjectHues = 2

// defaultAgentColors is the package-level map NormalizePalette overlays a
// theme's AgentColors onto, per key.
var defaultAgentColors = map[string]string{
	"claude":      "#D97757",
	"codex":       "#7DD3FC",
	"grok":        "#E2E8F0",
	"antigravity": "#5EEAD4",
	"gemini":      "#60A5FA",
	"cursor":      "#C4B5FD",
	"muse":        "#A78BFA",
}

// Populated by ApplyThemeColors, guarded by themeMu like the rest of the
// package's theme state. Seeded from the default theme at init so the
// accessors are correct before any ApplyTheme call, matching how every other
// colour in this package carries its Default Dark value from the start.
var (
	currentProjectHues []color.Color
	currentAgentColors map[string]color.Color
	currentLaneColors  map[string]color.Color
)

func init() {
	currentProjectHues, currentAgentColors, currentLaneColors =
		overviewColorState(NormalizePalette(DefaultTheme.Colors))
}

// overviewColorState converts the board's palette fields into the resolved
// colours the accessors serve.
func overviewColorState(c ColorPalette) ([]color.Color, map[string]color.Color, map[string]color.Color) {
	hues := make([]color.Color, len(c.ProjectHues))
	for i, hex := range c.ProjectHues {
		hues[i] = lipgloss.Color(hex)
	}
	agents := make(map[string]color.Color, len(c.AgentColors))
	for provider, hex := range c.AgentColors {
		agents[strings.ToLower(provider)] = lipgloss.Color(hex)
	}
	lanes := map[string]color.Color{
		"working": lipgloss.Color(c.LaneWorking),
		"blocked": lipgloss.Color(c.LaneBlocked),
		"done":    lipgloss.Color(c.LaneDone),
		"idle":    lipgloss.Color(c.LaneIdle),
		"paused":  lipgloss.Color(c.LanePaused),
	}
	return hues, agents, lanes
}

// ProjectHue returns a stable hue for a project key. The same key always maps
// to the same ramp entry for a given ramp length, so a project keeps its
// colour across restarts.
func ProjectHue(projectKey string) color.Color {
	themeMu.RLock()
	ramp := currentProjectHues
	themeMu.RUnlock()
	if len(ramp) == 0 {
		return TextMuted
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(projectKey))
	return ramp[h.Sum32()%uint32(len(ramp))]
}

// AgentColor returns the chip foreground for a provider, case-insensitively.
// Unregistered providers get TextMuted rather than borrowing a hue.
func AgentColor(provider string) color.Color {
	themeMu.RLock()
	defer themeMu.RUnlock()
	if c, ok := currentAgentColors[strings.ToLower(provider)]; ok {
		return c
	}
	return TextMuted
}

// defaultAgentIcons maps workspace/overview provider names to the same glyphs
// conversations adapters show (Adapter.Icon). Keys are lowercase provider IDs
// as used by workspace.AgentType / workspaceinventory.Workspace.Provider.
// Keep in sync with each adapter's Icon() — this is the presentation seam for
// surfaces that only know the short provider name, not a loaded Adapter.
var defaultAgentIcons = map[string]string{
	"claude":      "◆",
	"codex":       "▶",
	"antigravity": "★",
	"gemini":      "★",
	"cursor":      "▌",
	"amp":         "\u26a1", // ⚡
	"opencode":    "◇",
	"pi":          "\U0001F43E", // 🐾
	"copilot":     "⋮⋮",
	"kiro":        "\u03ba", // κ
	"warp":        "»",
	"grok":        "✦",
	"muse":        "◈",
}

// AgentIcon returns the conversations-style glyph for a provider, case-
// insensitively. Unknown providers return "" so callers can fall back to the
// bare name.
func AgentIcon(provider string) string {
	if icon, ok := defaultAgentIcons[strings.ToLower(strings.TrimSpace(provider))]; ok {
		return icon
	}
	return ""
}

// AgentLabel is icon + space + provider name when an icon is known, otherwise
// just the provider name. Used for chips and compact agent labels.
// Provider names are lowercased so every surface shows the same token
// (matching conversations adapters and the Agent Overview board).
func AgentLabel(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return ""
	}
	if icon := AgentIcon(provider); icon != "" {
		return icon + " " + provider
	}
	return provider
}

// AgentChipFill is the background behind an agent chip.
func AgentChipFill() color.Color {
	return SurfaceRaised
}

// RenderAgentChip returns the themed agent chip: AgentLabel on AgentColor
// with AgentChipFill behind it. Empty providers return "".
// This is the reusable presentation for workspaces, overview, and any other
// surface that should match the Agent Overview board.
func RenderAgentChip(provider string) string {
	label := AgentLabel(provider)
	if label == "" {
		return ""
	}
	return lipgloss.NewStyle().
		Foreground(AgentColor(provider)).
		Background(AgentChipFill()).
		Render(label)
}

// RenderAgentLabel returns AgentLabel in AgentColor without the raised fill.
// Use on selected rows that already paint a full-width selection background,
// where a second fill would fight the selection.
func RenderAgentLabel(provider string) string {
	label := AgentLabel(provider)
	if label == "" {
		return ""
	}
	return lipgloss.NewStyle().
		Foreground(AgentColor(provider)).
		Render(label)
}

// LaneColor maps an agentstatus lane id ("working", "blocked", "done",
// "idle", "paused") to its colour. Unknown ids get TextMuted.
func LaneColor(lane string) color.Color {
	themeMu.RLock()
	defer themeMu.RUnlock()
	if c, ok := currentLaneColors[lane]; ok {
		return c
	}
	return TextMuted
}
