package notify

import "testing"

func TestChromeColorOverridesTheSourceHueForErrors(t *testing.T) {
	if ChromeColor(SourceAgent, SeverityInfo) == ChromeColor(SourceAgent, SeverityError) {
		t.Fatal("an error must not take the source's ordinary hue")
	}
	if ChromeColor(SourceAgent, SeverityError) != ResolveHue(HueError) {
		t.Fatal("an error's chrome should be the error hue whatever posted it")
	}
	if SourceColor(SourceAgent) != ChromeColor(SourceAgent, SeverityInfo) {
		t.Fatal("SourceColor must be ChromeColor without a severity")
	}
}

func TestRenderGlyphCarriesTheSourceGlyph(t *testing.T) {
	for _, s := range Sources() {
		rendered := RenderGlyph(s.ID, SeverityInfo)
		if rendered == "" {
			t.Fatalf("%s rendered no glyph", s.ID)
		}
		if Glyph(s.ID) != s.Glyph {
			t.Fatalf("%s glyph = %q, want %q", s.ID, Glyph(s.ID), s.Glyph)
		}
	}
}
