package markdown

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"charm.land/glamour/v2/ansi"
	glamourstyles "charm.land/glamour/v2/styles"
	chromastyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/cespare/xxhash/v2"

	"github.com/marcus/sidecar/internal/styles"
)

// Chroma styles used when a palette names a syntax theme Chroma does not know.
const (
	fallbackChromaLight = "github"
	fallbackChromaDark  = "monokai"
)

// ThemeSnapshot is an immutable capture of the theme inputs that affect
// Markdown rendering. Take one per render operation so a concurrent theme
// change cannot mix one palette's cache key with another palette's style.
type ThemeSnapshot struct {
	Palette styles.ColorPalette
}

// CurrentThemeSnapshot captures the active palette. ApplyThemeColors stores an
// already-normalized palette, so this must not re-normalize: NormalizePalette
// mutates shared slices and is not safe to call concurrently.
func CurrentThemeSnapshot() ThemeSnapshot {
	return ThemeSnapshot{Palette: styles.GetCurrentTheme().Colors}
}

// StyleKey is a stable identity for the Glamour style a snapshot produces.
// It covers every effective input: palette colors, syntax theme, markdown
// theme, and the resolved contents of an explicit full-style file.
func (s ThemeSnapshot) StyleKey() string {
	_, _, key, _ := s.resolve()
	return key
}

// BuildStyle returns the Glamour style config derived from the snapshot along
// with its style key.
func BuildStyle(snapshot ThemeSnapshot) (ansi.StyleConfig, string) {
	return snapshot.build()
}

func (s ThemeSnapshot) build() (ansi.StyleConfig, string) {
	mode, chromaTheme, key, override := s.resolve()
	if override != nil {
		return *override, key
	}
	return applyPalette(presetStyle(mode), s.Palette, chromaTheme), key
}

// resolve decides between palette-derived and explicit-override styling and
// computes the style key without building the palette-derived config.
func (s ThemeSnapshot) resolve() (mode, chromaTheme, key string, override *ansi.StyleConfig) {
	c := s.Palette
	mode = strings.ToLower(strings.TrimSpace(c.MarkdownTheme))

	if mode != "dark" && mode != "light" && mode != "" {
		if cfg, keyExtra, ok := explicitStyle(mode); ok {
			return mode, "", "override|" + mode + "|" + keyExtra, &cfg
		}
		// Unusable override: fall back to a palette-derived mode.
		mode = ""
	}
	if mode == "" {
		mode = "dark"
		if isLightPalette(c) {
			mode = "light"
		}
	}
	chromaTheme = resolveChromaTheme(c.SyntaxTheme, mode)
	return mode, chromaTheme, paletteKey(c, mode, chromaTheme), nil
}

// explicitStyle resolves a nonstandard markdownTheme value as a full-style
// override: a registered Glamour preset name, or a JSON style file on disk.
func explicitStyle(name string) (ansi.StyleConfig, string, bool) {
	if preset, ok := glamourstyles.DefaultStyles[name]; ok && preset != nil {
		return copyStyle(*preset), "preset", true
	}
	data, err := os.ReadFile(name)
	if err != nil {
		return ansi.StyleConfig{}, "", false
	}
	var cfg ansi.StyleConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return ansi.StyleConfig{}, "", false
	}
	return cfg, fmt.Sprintf("file:%016x", xxhash.Sum64(data)), true
}

// presetStyle returns a deep copy of a Glamour preset, used only for its
// structural choices (margins, prefixes, glyphs).
func presetStyle(mode string) ansi.StyleConfig {
	preset, ok := glamourstyles.DefaultStyles[mode]
	if !ok || preset == nil {
		preset = glamourstyles.DefaultStyles["dark"]
	}
	return copyStyle(*preset)
}

func copyStyle(in ansi.StyleConfig) ansi.StyleConfig {
	data, err := json.Marshal(in)
	if err != nil {
		return in
	}
	var out ansi.StyleConfig
	if err := json.Unmarshal(data, &out); err != nil {
		return in
	}
	return out
}

// resolveChromaTheme validates the palette's syntax theme against Chroma's
// registry, falling back deterministically per light/dark mode.
func resolveChromaTheme(name, mode string) string {
	if name != "" {
		if _, ok := chromastyles.Registry[name]; ok {
			return name
		}
	}
	if mode == "light" {
		return fallbackChromaLight
	}
	return fallbackChromaDark
}

// applyPalette overwrites every color-bearing Markdown role with the palette's
// semantic colors, leaving the preset's structure intact.
func applyPalette(cfg ansi.StyleConfig, c styles.ColorPalette, chromaTheme string) ansi.StyleConfig {
	text := pick(c.TextPrimary)
	secondary := pick(c.TextSecondary, c.TextPrimary)
	muted := pick(c.TextMuted, c.TextSecondary, c.TextPrimary)
	primary := pick(c.Primary, c.TextPrimary)
	accent := pick(c.Accent, c.Primary)
	link := pick(c.Link, c.Accent, c.Primary)
	quote := pick(c.Secondary, c.Accent)
	rule := pick(c.BorderMuted, c.BorderNormal, c.TextMuted)
	codeBg := pick(c.BgSecondary)

	// The document must not paint a background; pane chrome and selection own
	// the canvas.
	cfg.Document.Color = text
	cfg.Document.BackgroundColor = nil
	cfg.Paragraph.Color = text
	cfg.Text.Color = text
	cfg.Text.BackgroundColor = nil

	cfg.Heading.Color = accent
	cfg.H1.Color = primary
	cfg.H1.BackgroundColor = nil
	cfg.H2.Color = accent
	cfg.H3.Color = secondary
	cfg.H4.Color = secondary
	cfg.H5.Color = muted
	cfg.H6.Color = muted

	cfg.Emph.Color = text
	cfg.Strong.Color = primary
	cfg.Strikethrough.Color = muted
	cfg.HorizontalRule.Color = rule

	cfg.Item.Color = primary
	cfg.Enumeration.Color = primary
	cfg.Task.Color = primary

	cfg.Link.Color = link
	cfg.LinkText.Color = link
	cfg.Image.Color = secondary
	cfg.ImageText.Color = muted

	cfg.BlockQuote.Color = quote
	cfg.BlockQuote.BackgroundColor = nil

	cfg.Code.Color = accent
	cfg.Code.BackgroundColor = codeBg

	// Glamour gives an inline Chroma table precedence over Theme; clearing it
	// is what lets the active Sidecar syntax theme reach fenced code.
	cfg.CodeBlock.Chroma = nil
	cfg.CodeBlock.Theme = chromaTheme
	cfg.CodeBlock.Color = nil
	cfg.CodeBlock.BackgroundColor = nil

	cfg.Table.Color = text
	cfg.Table.BackgroundColor = nil
	cfg.DefinitionList.Color = text
	cfg.DefinitionTerm.Color = secondary
	cfg.DefinitionDescription.Color = muted
	cfg.HTMLBlock.Color = muted
	cfg.HTMLSpan.Color = muted
	return cfg
}

// pick returns a pointer to the first non-empty candidate, or nil.
func pick(candidates ...string) *string {
	for _, v := range candidates {
		if v != "" {
			out := v
			return &out
		}
	}
	return nil
}

// isLightPalette reports whether the palette's primary background is light.
func isLightPalette(c styles.ColorPalette) bool {
	r, g, b, ok := parseHex(c.BgPrimary)
	if !ok {
		return false
	}
	// Rec. 601 luma.
	luma := 0.299*float64(r) + 0.587*float64(g) + 0.114*float64(b)
	return luma > 128
}

func parseHex(hex string) (int, int, int, bool) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 && len(hex) != 8 {
		return 0, 0, 0, false
	}
	var r, g, b int
	if _, err := fmt.Sscanf(hex[:6], "%02x%02x%02x", &r, &g, &b); err != nil {
		return 0, 0, 0, false
	}
	return r, g, b, true
}

// paletteKey hashes every palette input that reaches the generated style, so
// two palettes with the same name but different overrides get different keys.
func paletteKey(c styles.ColorPalette, mode, chromaTheme string) string {
	h := xxhash.New()
	for _, v := range []string{
		mode, chromaTheme,
		c.TextPrimary, c.TextSecondary, c.TextMuted, c.TextSubtle,
		c.Primary, c.Secondary, c.Accent, c.Link,
		c.BgPrimary, c.BgSecondary, c.BorderNormal, c.BorderMuted,
		c.SyntaxTheme, c.MarkdownTheme,
	} {
		_, _ = h.WriteString(v)
		_, _ = h.WriteString("\x00")
	}
	return fmt.Sprintf("palette|%s|%016x", mode, h.Sum64())
}
