package markdown

import (
	"testing"

	chromastyles "github.com/alecthomas/chroma/v2/styles"

	"github.com/marcus/sidecar/internal/community"
	"github.com/marcus/sidecar/internal/styles"
)

// semanticRole is one palette input the Markdown style builder reads.
type semanticRole struct {
	name  string
	value func(styles.ColorPalette) string
}

// markdownSemanticRoles are the colour-bearing palette inputs listed in the
// theme-aware Markdown plan. Every curated theme and every community
// conversion must supply all of them as valid hex colours.
var markdownSemanticRoles = []semanticRole{
	{"textPrimary", func(c styles.ColorPalette) string { return c.TextPrimary }},
	{"textSecondary", func(c styles.ColorPalette) string { return c.TextSecondary }},
	{"textMuted", func(c styles.ColorPalette) string { return c.TextMuted }},
	{"primary", func(c styles.ColorPalette) string { return c.Primary }},
	{"secondary", func(c styles.ColorPalette) string { return c.Secondary }},
	{"accent", func(c styles.ColorPalette) string { return c.Accent }},
	{"link", func(c styles.ColorPalette) string { return c.Link }},
	{"bgPrimary", func(c styles.ColorPalette) string { return c.BgPrimary }},
	{"bgSecondary", func(c styles.ColorPalette) string { return c.BgSecondary }},
	{"borderNormal", func(c styles.ColorPalette) string { return c.BorderNormal }},
	{"borderMuted", func(c styles.ColorPalette) string { return c.BorderMuted }},
}

// auditPalette asserts one palette can produce a palette-derived Markdown
// style with every colour-bearing role resolved from the palette itself.
func auditPalette(t *testing.T, label string, c styles.ColorPalette) {
	t.Helper()

	for _, role := range markdownSemanticRoles {
		v := role.value(c)
		if v == "" {
			t.Errorf("%s: %s is empty; Markdown rendering has no source for it", label, role.name)
			continue
		}
		if !styles.IsValidHexColor(v) {
			t.Errorf("%s: %s = %q is not a valid hex colour", label, role.name, v)
		}
	}

	if c.SyntaxTheme == "" {
		t.Errorf("%s: syntaxTheme is empty; fenced code has no Chroma style", label)
	} else if _, ok := chromastyles.Registry[c.SyntaxTheme]; !ok {
		t.Errorf("%s: syntaxTheme %q is not a registered Chroma style", label, c.SyntaxTheme)
	}

	switch c.MarkdownTheme {
	case "dark", "light":
	default:
		t.Errorf("%s: markdownTheme = %q; curated and converted palettes must use the "+
			"structural modes \"dark\" or \"light\" (other values are explicit full-style overrides)",
			label, c.MarkdownTheme)
	}

	cfg, key := BuildStyle(ThemeSnapshot{Palette: c})
	if key == "" {
		t.Errorf("%s: empty style key", label)
	}
	if cfg.CodeBlock.Chroma != nil {
		t.Errorf("%s: CodeBlock.Chroma must be cleared so syntaxTheme reaches fenced code", label)
	}
	if cfg.CodeBlock.Theme != c.SyntaxTheme {
		t.Errorf("%s: CodeBlock.Theme = %q, want %q", label, cfg.CodeBlock.Theme, c.SyntaxTheme)
	}
	for name, got := range map[string]*string{
		"Document.Color": cfg.Document.Color,
		"H1.Color":       cfg.H1.Color,
		"H2.Color":       cfg.H2.Color,
		"Link.Color":     cfg.Link.Color,
		"Code.Color":     cfg.Code.Color,
		"BlockQuote":     cfg.BlockQuote.Color,
		"Item.Color":     cfg.Item.Color,
		"HorizontalRule": cfg.HorizontalRule.Color,
		"Table.Color":    cfg.Table.Color,
	} {
		if got == nil || *got == "" {
			t.Errorf("%s: %s is unset; Markdown would fall back to the generic preset", label, name)
		}
	}
	if cfg.Document.BackgroundColor != nil {
		t.Errorf("%s: Markdown document must not paint a background", label)
	}
	if cfg.Code.BackgroundColor == nil || *cfg.Code.BackgroundColor != c.BgSecondary {
		t.Errorf("%s: inline code background should come from bgSecondary", label)
	}
}

// TestAllCuratedThemesSupplyMarkdownInputs is the all-theme audit: every
// curated theme must supply the semantic inputs Markdown rendering reads,
// both as authored and after normalization.
func TestAllCuratedThemesSupplyMarkdownInputs(t *testing.T) {
	if len(styles.CuratedThemes) == 0 {
		t.Fatal("no curated themes registered")
	}
	for name, theme := range styles.CuratedThemes {
		t.Run(name, func(t *testing.T) {
			auditPalette(t, "curated/"+name, theme.Colors)
			auditPalette(t, "curated/"+name+" (normalized)", styles.NormalizePalette(theme.Colors))
		})
	}
}

// TestRegisteredThemesSupplyMarkdownInputs covers everything reachable through
// the theme registry, including aliases and the default theme.
func TestRegisteredThemesSupplyMarkdownInputs(t *testing.T) {
	names := styles.ListThemes()
	if len(names) == 0 {
		t.Fatal("no themes listed")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			auditPalette(t, "theme/"+name, styles.NormalizePalette(styles.GetTheme(name).Colors))
		})
	}
}

// TestCommunityConversionsSupplyMarkdownInputs audits the community scheme
// conversion path, which is how light palettes most often reach Sidecar.
func TestCommunityConversionsSupplyMarkdownInputs(t *testing.T) {
	names := community.ListSchemes()
	if len(names) == 0 {
		t.Fatal("no community schemes embedded")
	}
	sawLight := false
	for _, name := range names {
		scheme := community.GetScheme(name)
		if scheme == nil {
			t.Errorf("community scheme %q missing from map", name)
			continue
		}
		palette := styles.NormalizePalette(community.Convert(scheme))
		if palette.MarkdownTheme == "light" {
			sawLight = true
		}
		t.Run(name, func(t *testing.T) {
			auditPalette(t, "community/"+name, palette)
		})
	}
	if !sawLight {
		t.Error("expected at least one light community conversion in the audit")
	}
}

// TestOverriddenPaletteAudit covers the project-scoped/override path: a theme
// keeping its name while colours change must still audit clean and must
// produce a different style key.
func TestOverriddenPaletteAudit(t *testing.T) {
	base := styles.NormalizePalette(styles.GetTheme(styles.FreshInstallTheme).Colors)
	auditPalette(t, "override/base", base)

	overridden := base
	overridden.Accent = "#ff00ff"
	overridden.Link = "#00ffff"
	overridden.SyntaxTheme = "github-dark"
	auditPalette(t, "override/modified", overridden)

	baseKey := ThemeSnapshot{Palette: base}.StyleKey()
	overKey := ThemeSnapshot{Palette: overridden}.StyleKey()
	if baseKey == overKey {
		t.Fatalf("same-name override produced identical style key %q", baseKey)
	}
}
