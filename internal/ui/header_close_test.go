package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

func TestRenderCloseButtonUsesSharedButtonStyles(t *testing.T) {
	rest := RenderCloseButton(false)
	hover := RenderCloseButton(true)
	if !strings.Contains(ansi.Strip(rest), CloseButtonLabel) || !strings.Contains(ansi.Strip(hover), CloseButtonLabel) {
		t.Fatalf("close button dropped its label: rest=%q hover=%q", ansi.Strip(rest), ansi.Strip(hover))
	}
	if lipgloss.Width(rest) != lipgloss.Width(hover) {
		t.Fatalf("hover reflowed the close button: rest=%d hover=%d", lipgloss.Width(rest), lipgloss.Width(hover))
	}
	if lipgloss.Width(rest) != CloseButtonWidth() {
		t.Fatalf("CloseButtonWidth = %d, want %d", CloseButtonWidth(), lipgloss.Width(rest))
	}
	if ResolveButtonStyle(-1, -1, 0).GetBackground() != styles.Button.GetBackground() {
		t.Fatal("rest close style is not the shared Button")
	}
	if ResolveButtonStyle(-1, 0, 0).GetBackground() != styles.ButtonHover.GetBackground() {
		t.Fatal("hovered close style is not the shared ButtonHover")
	}
}

func TestReserveHeaderCloseKeepsTheButtonWhole(t *testing.T) {
	btnW := CloseButtonWidth()
	if got := ReserveHeaderClose(0); got.CloseW != 0 || got.TabsWidth != 0 {
		t.Fatalf("zero width reserved a button: %+v", got)
	}
	if got := ReserveHeaderClose(btnW - 1); got.CloseW != 0 || got.TabsWidth != btnW-1 {
		t.Fatalf("narrow row still reserved a button: %+v", got)
	}
	got := ReserveHeaderClose(40)
	if got.TabsWidth != 40-btnW || got.CloseCol != 40-btnW || got.CloseW != btnW {
		t.Fatalf("wide reserve = %+v, want tabs=%d col=%d w=%d", got, 40-btnW, 40-btnW, btnW)
	}
}

func TestComposeHeaderClosePinsTheButtonRight(t *testing.T) {
	const width = 24
	reserve := ReserveHeaderClose(width)
	tabs := strings.Repeat("t", reserve.TabsWidth)
	row := ComposeHeaderClose(tabs, width, false)
	if lipgloss.Width(row) != width {
		t.Fatalf("composed width = %d, want %d", lipgloss.Width(row), width)
	}
	plain := ansi.Strip(row)
	if !strings.HasSuffix(strings.TrimRight(plain, " "), CloseButtonLabel) && !strings.Contains(plain, CloseButtonLabel) {
		t.Fatalf("composed header has no close glyph: %q", plain)
	}
	hovered := ComposeHeaderClose(tabs, width, true)
	if lipgloss.Width(hovered) != width {
		t.Fatalf("hovered header reflowed: %d", lipgloss.Width(hovered))
	}
}
