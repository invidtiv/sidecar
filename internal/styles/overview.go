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

// AgentChipFill is the background behind an agent chip.
func AgentChipFill() color.Color {
	return SurfaceRaised
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
