package markdown

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/marcus/sidecar/internal/styles"
)

// unmistakable test palettes: every role gets a distinct colour so a mapping
// mistake shows up as the wrong hex rather than a near-miss.
func testPaletteA() styles.ColorPalette {
	return styles.ColorPalette{
		Primary:       "#ff0001",
		Secondary:     "#ff0002",
		Accent:        "#ff0003",
		Link:          "#ff0004",
		TextPrimary:   "#ff0005",
		TextSecondary: "#ff0006",
		TextMuted:     "#ff0007",
		TextSubtle:    "#ff0008",
		BgPrimary:     "#000000",
		BgSecondary:   "#ff0009",
		BorderNormal:  "#ff000a",
		BorderMuted:   "#ff000b",
		SyntaxTheme:   "monokai",
		MarkdownTheme: "dark",
	}
}

func testPaletteB() styles.ColorPalette {
	p := testPaletteA()
	p.Primary = "#00ff01"
	p.Secondary = "#00ff02"
	p.Accent = "#00ff03"
	p.Link = "#00ff04"
	p.TextPrimary = "#00ff05"
	p.TextSecondary = "#00ff06"
	p.TextMuted = "#00ff07"
	p.BgSecondary = "#00ff09"
	p.BorderMuted = "#00ff0b"
	p.SyntaxTheme = "dracula"
	return p
}

func deref(t *testing.T, role string, v *string) string {
	t.Helper()
	if v == nil {
		t.Fatalf("%s colour is nil", role)
	}
	return *v
}

func TestBuildStyleUsesPaletteRoles(t *testing.T) {
	pal := testPaletteA()
	cfg, key := BuildStyle(ThemeSnapshot{Palette: pal})

	checks := map[string]struct {
		got  *string
		want string
	}{
		"document":   {cfg.Document.Color, pal.TextPrimary},
		"paragraph":  {cfg.Paragraph.Color, pal.TextPrimary},
		"text":       {cfg.Text.Color, pal.TextPrimary},
		"h1":         {cfg.H1.Color, pal.Primary},
		"h2":         {cfg.H2.Color, pal.Accent},
		"h3":         {cfg.H3.Color, pal.TextSecondary},
		"h5":         {cfg.H5.Color, pal.TextMuted},
		"item":       {cfg.Item.Color, pal.Primary},
		"task":       {cfg.Task.Color, pal.Primary},
		"link":       {cfg.Link.Color, pal.Link},
		"link_text":  {cfg.LinkText.Color, pal.Link},
		"blockquote": {cfg.BlockQuote.Color, pal.Secondary},
		"hr":         {cfg.HorizontalRule.Color, pal.BorderMuted},
		"code":       {cfg.Code.Color, pal.Accent},
		"code_bg":    {cfg.Code.BackgroundColor, pal.BgSecondary},
		"image_text": {cfg.ImageText.Color, pal.TextMuted},
	}
	for role, c := range checks {
		if got := deref(t, role, c.got); got != c.want {
			t.Errorf("%s colour = %q, want %q", role, got, c.want)
		}
	}

	if cfg.Document.BackgroundColor != nil {
		t.Errorf("document background = %q, want none", *cfg.Document.BackgroundColor)
	}
	if cfg.CodeBlock.Chroma != nil {
		t.Error("CodeBlock.Chroma must be cleared so CodeBlock.Theme wins")
	}
	if cfg.CodeBlock.Theme != pal.SyntaxTheme {
		t.Errorf("CodeBlock.Theme = %q, want %q", cfg.CodeBlock.Theme, pal.SyntaxTheme)
	}
	if key == "" || !strings.HasPrefix(key, "palette|dark|") {
		t.Errorf("unexpected style key %q", key)
	}
}

func TestBuildStyleChromaFallback(t *testing.T) {
	tests := []struct {
		name   string
		syntax string
		bg     string
		mdMode string
		want   string
	}{
		{"known theme", "monokai", "#000000", "dark", "monokai"},
		{"sidecar modern custom", styles.SidecarModernSyntaxThemeName, "#000000", "dark", styles.SidecarModernSyntaxThemeName},
		{"invalid dark", "not-a-chroma-style", "#000000", "dark", fallbackChromaDark},
		{"invalid light", "not-a-chroma-style", "#ffffff", "light", fallbackChromaLight},
		{"empty light", "", "#ffffff", "light", fallbackChromaLight},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pal := testPaletteA()
			pal.SyntaxTheme = tt.syntax
			pal.BgPrimary = tt.bg
			pal.MarkdownTheme = tt.mdMode
			cfg, _ := BuildStyle(ThemeSnapshot{Palette: pal})
			if cfg.CodeBlock.Theme != tt.want {
				t.Errorf("CodeBlock.Theme = %q, want %q", cfg.CodeBlock.Theme, tt.want)
			}
		})
	}
}

func TestStyleKeyDistinguishesOverrides(t *testing.T) {
	a := ThemeSnapshot{Palette: testPaletteA()}
	// Same theme name, one colour overridden.
	overridden := testPaletteA()
	overridden.Accent = "#123456"
	b := ThemeSnapshot{Palette: overridden}

	if a.StyleKey() == b.StyleKey() {
		t.Fatal("same-name palettes with different overrides share a style key")
	}
	again := ThemeSnapshot{Palette: testPaletteA()}
	if a.StyleKey() != again.StyleKey() {
		t.Fatal("style key is not stable for identical palettes")
	}

	r, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	const doc = "# Title\n\ntext with `code` and [link](http://x)\n"
	outA := strings.Join(r.renderContent(doc, 60, a), "\n")
	outB := strings.Join(r.renderContent(doc, 60, b), "\n")
	if outA == outB {
		t.Fatal("override change produced identical ANSI")
	}
}

func TestExplicitMarkdownThemeOverride(t *testing.T) {
	pal := testPaletteA()
	pal.MarkdownTheme = "dracula"
	cfg, key := BuildStyle(ThemeSnapshot{Palette: pal})
	if !strings.HasPrefix(key, "override|dracula|") {
		t.Fatalf("style key = %q, want dracula override", key)
	}
	if cfg.Document.Color != nil && *cfg.Document.Color == pal.TextPrimary {
		t.Error("explicit override was recoloured from the Sidecar palette")
	}

	// A style file on disk is an override keyed by its contents.
	dir := t.TempDir()
	path := filepath.Join(dir, "style.json")
	if err := os.WriteFile(path, []byte(`{"document":{"color":"#abcdef"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	pal.MarkdownTheme = path
	cfg, key = BuildStyle(ThemeSnapshot{Palette: pal})
	if !strings.Contains(key, "file:") {
		t.Fatalf("style key = %q, want file hash", key)
	}
	if deref(t, "document", cfg.Document.Color) != "#abcdef" {
		t.Error("style file contents were not used")
	}

	if err := os.WriteFile(path, []byte(`{"document":{"color":"#fedcba"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, key2 := BuildStyle(ThemeSnapshot{Palette: pal})
	if key2 == key {
		t.Error("style key did not change when the style file changed")
	}

	// An unusable override falls back to the palette-derived mode.
	pal.MarkdownTheme = filepath.Join(dir, "missing.json")
	_, key3 := BuildStyle(ThemeSnapshot{Palette: pal})
	if !strings.HasPrefix(key3, "palette|") {
		t.Errorf("unusable override key = %q, want palette fallback", key3)
	}
}

func TestRendererRerendersOnThemeChange(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	const doc = "# Heading\n\nBody with `code`.\n"
	a := ThemeSnapshot{Palette: testPaletteA()}
	b := ThemeSnapshot{Palette: testPaletteB()}

	outA := strings.Join(r.renderContent(doc, 70, a), "\n")
	outB := strings.Join(r.renderContent(doc, 70, b), "\n")
	if outA == outB {
		t.Fatal("theme change did not change rendered output at the same width")
	}
	if a.StyleKey() == b.StyleKey() {
		t.Fatal("theme change did not change the style key")
	}
	// Switching back must not serve theme B's cached ANSI.
	outA2 := strings.Join(r.renderContent(doc, 70, a), "\n")
	if outA2 != outA {
		t.Fatal("switching back to theme A produced different output")
	}
	if outA2 == outB {
		t.Fatal("stale cache returned theme B's ANSI for theme A")
	}
}

func TestRendererConcurrentThemeSnapshots(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	const doc = "# Heading\n\nBody text and `code`.\n"
	snaps := []ThemeSnapshot{{Palette: testPaletteA()}, {Palette: testPaletteB()}}
	want := make([]string, len(snaps))
	for i, s := range snaps {
		want[i] = strings.Join(r.renderContent(doc, 70, s), "\n")
	}

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			idx := i % len(snaps)
			got := strings.Join(r.renderContent(doc, 70, snaps[idx]), "\n")
			if got != want[idx] {
				t.Errorf("concurrent render %d mixed palettes", i)
			}
		}(i)
	}
	wg.Wait()
}

func TestCurrentThemeSnapshotFollowsActiveTheme(t *testing.T) {
	original := styles.GetCurrentTheme()
	t.Cleanup(func() { styles.ApplyThemeColors(original) })

	styles.ApplyThemeColors(styles.Theme{Name: "test-a", Colors: testPaletteA()})
	keyA := CurrentThemeSnapshot().StyleKey()
	styles.ApplyThemeColors(styles.Theme{Name: "test-b", Colors: testPaletteB()})
	keyB := CurrentThemeSnapshot().StyleKey()
	if keyA == keyB {
		t.Fatal("style key did not follow the active theme")
	}

	r, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	if r.StyleKey() != keyB {
		t.Errorf("Renderer.StyleKey() = %q, want %q", r.StyleKey(), keyB)
	}
}
