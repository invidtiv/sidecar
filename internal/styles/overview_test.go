package styles

import "testing"

func TestProjectHueIsStableForFixedKeyAndRamp(t *testing.T) {
	ApplyThemeColors(Theme{Colors: ColorPalette{
		BgPrimary:   "#111111",
		BgSecondary: "#222222",
		ProjectHues: []string{"#A78BFA", "#22D3EE", "#FB923C"},
	}})
	defer ApplyTheme("default")

	first := ProjectHue("sidecar")
	for i := 0; i < 10; i++ {
		if got := ProjectHue("sidecar"); got != first {
			t.Fatalf("ProjectHue(%q) changed across calls: %v != %v", "sidecar", got, first)
		}
	}
	if got := ProjectHue("td"); got == first {
		// Not a hard requirement (hashes could collide), but sidecar/td happen
		// not to, and a bug that ignores the key entirely would show up here.
		t.Logf("ProjectHue(%q) == ProjectHue(%q): %v", "td", "sidecar", got)
	}
}

func TestAgentColorFallsBackToTextMutedForUnknownProvider(t *testing.T) {
	ApplyTheme("default")
	defer ApplyTheme("default")

	if got, want := AgentColor("some-unregistered-provider"), TextMuted; got != want {
		t.Errorf("AgentColor(unregistered) = %v, want TextMuted %v", got, want)
	}
	if AgentColor("claude") == TextMuted {
		t.Errorf("AgentColor(claude) fell back to TextMuted, want its own colour")
	}
	if AgentColor("CLAUDE") != AgentColor("claude") {
		t.Errorf("AgentColor is not case-insensitive")
	}
}

func TestLaneColorFallsBackToTextMutedForUnknownLane(t *testing.T) {
	ApplyTheme("default")
	defer ApplyTheme("default")

	if got, want := LaneColor("nonexistent"), TextMuted; got != want {
		t.Errorf("LaneColor(unknown) = %v, want TextMuted %v", got, want)
	}
	for _, lane := range []string{"working", "blocked", "done", "idle", "paused"} {
		if LaneColor(lane) == TextMuted {
			t.Errorf("LaneColor(%q) fell back to TextMuted, want its own colour", lane)
		}
	}
}

func TestNormalizePaletteOverviewDerivationsFireOnlyWhenEmpty(t *testing.T) {
	base := ColorPalette{
		BgPrimary:   "#111111",
		BgSecondary: "#222222",
		Success:     "#00ff00",
		Warning:     "#ffff00",
		Info:        "#0000ff",
	}
	// Give the roles NormalizePalette would otherwise fill in on its own pass,
	// so the assertions below are about the overview derivation, not the text
	// contrast pass upstream of it.
	base.TextSecondary = "#aaaaaa"
	base.TextMuted = "#888888"

	t.Run("ProjectHues from TabColors when both ramp fields are set", func(t *testing.T) {
		p := base
		p.TabColors = []string{"#123456", "#654321"}
		got := NormalizePalette(p)
		if len(got.ProjectHues) != len(p.TabColors) {
			t.Fatalf("ProjectHues = %v, want derived from TabColors %v", got.ProjectHues, p.TabColors)
		}
	})

	t.Run("ProjectHues from package default when both are empty", func(t *testing.T) {
		got := NormalizePalette(base)
		if len(got.ProjectHues) != len(defaultProjectHues) {
			t.Fatalf("ProjectHues = %v, want package default ramp (len %d)", got.ProjectHues, len(defaultProjectHues))
		}
	})

	t.Run("ProjectHues left alone when supplied", func(t *testing.T) {
		p := base
		p.ProjectHues = []string{"#FEDCBA"}
		got := NormalizePalette(p)
		if len(got.ProjectHues) != 1 {
			t.Fatalf("ProjectHues = %v, want the single supplied hue preserved", got.ProjectHues)
		}
	})

	t.Run("lane colours derive from status roles only when empty", func(t *testing.T) {
		got := NormalizePalette(base)
		if got.LaneWorking == "" || got.LaneBlocked == "" || got.LaneDone == "" || got.LaneIdle == "" || got.LanePaused == "" {
			t.Fatalf("expected every lane colour to be derived, got %+v", got)
		}

		p := base
		p.LaneWorking = "#ABCDEF"
		got = NormalizePalette(p)
		if got.LaneWorking != EnsureContrastOn("#ABCDEF", []string{base.BgPrimary, base.BgSecondary}, targetBodyText) {
			t.Errorf("LaneWorking overridden by derivation: got %s", got.LaneWorking)
		}
	})
}

func TestNormalizePaletteAgentColorsOverlayDefaultPerKey(t *testing.T) {
	base := ColorPalette{
		BgPrimary:     "#111111",
		BgSecondary:   "#222222",
		SurfaceRaised: "#333333",
		AgentColors: map[string]string{
			"claude": "#FFFFFF",
		},
	}
	got := NormalizePalette(base)

	if len(got.AgentColors) != len(defaultAgentColors) {
		t.Fatalf("AgentColors = %v, want overlay onto the full default map (len %d)", got.AgentColors, len(defaultAgentColors))
	}
	wantClaude := EnsureContrastOn("#FFFFFF", []string{got.SurfaceRaised}, targetBodyText)
	if got.AgentColors["claude"] != wantClaude {
		t.Errorf("AgentColors[claude] = %s, want the supplied override %s (contrast-adjusted)", got.AgentColors["claude"], wantClaude)
	}
	for provider, hex := range defaultAgentColors {
		if provider == "claude" {
			continue
		}
		want := EnsureContrastOn(hex, []string{got.SurfaceRaised}, targetBodyText)
		if got.AgentColors[provider] != want {
			t.Errorf("AgentColors[%s] = %s, want the untouched default %s (contrast-adjusted)", provider, got.AgentColors[provider], want)
		}
	}
}

func TestApplyGenericOverridesRejectsInvalidHex(t *testing.T) {
	p := DefaultTheme.Colors
	applyGenericOverrides(&p, map[string]interface{}{
		"laneWorking": "not-a-color",
		"agentColors": map[string]interface{}{
			"claude": "not-a-color",
			"codex":  "#111111",
		},
		"projectHues": []interface{}{"#111111", "not-a-color"},
	})

	if p.LaneWorking != DefaultTheme.Colors.LaneWorking {
		t.Errorf("invalid laneWorking override was applied: %s", p.LaneWorking)
	}
	if p.AgentColors["claude"] != DefaultTheme.Colors.AgentColors["claude"] {
		t.Errorf("invalid agentColors entry was applied: %v", p.AgentColors)
	}
	if p.AgentColors["codex"] != "#111111" {
		t.Errorf("valid agentColors entry was not applied: %v", p.AgentColors)
	}
	if len(p.ProjectHues) != len(DefaultTheme.Colors.ProjectHues) {
		t.Errorf("array override with an invalid entry was applied instead of rejected: %v", p.ProjectHues)
	}
}

// Every built-in theme must yield a ramp that can actually separate projects.
// Nord ships a single tab colour, which the TabColors fallback would otherwise
// borrow verbatim and hash every project to one spine.
func TestEveryThemeYieldsAUsableProjectRamp(t *testing.T) {
	for _, name := range ListThemes() {
		hues := NormalizePalette(GetTheme(name).Colors).ProjectHues
		distinct := map[string]bool{}
		for _, hue := range hues {
			distinct[hue] = true
		}
		if len(distinct) < minProjectHues {
			t.Errorf("theme %s: %d distinct project hues in %v, want at least %d", name, len(distinct), hues, minProjectHues)
		}
	}
}
