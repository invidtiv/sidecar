package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// applyThemeForTest switches the global palette and restores it afterwards.
// styles keeps the palette in package-level variables, so this is inherently
// global state; restoring the default keeps the rest of the package's tests
// (which assert against default-theme output) unaffected.
func applyThemeForTest(t *testing.T, name string) {
	t.Helper()
	styles.ApplyTheme(name)
	t.Cleanup(func() { styles.ApplyTheme("default") })
}

// TestDimStyleFollowsActiveTheme is the behavioural half of td-f2d94f's guard.
// DimStyle used to be a package-level var, so it captured the default theme's
// TextMuted at init and every modal backdrop stayed that grey no matter which
// theme the user picked. Asserting on rendered output rather than on source
// text is what makes this honest: it fails if the value is frozen for any
// reason, not just if someone rewrites it as a var.
func TestDimStyleFollowsActiveTheme(t *testing.T) {
	applyThemeForTest(t, "default")
	before := DimStyle().Render("x")

	applyThemeForTest(t, "solarized-dark")
	after := DimStyle().Render("x")

	if styles.GetTheme("default").Colors.TextMuted == styles.GetTheme("solarized-dark").Colors.TextMuted {
		t.Skip("the two themes share a TextMuted; this test cannot distinguish them")
	}
	if before == after {
		t.Fatalf("DimStyle did not change with the theme: %q both times (frozen at init?)", ansi.Strip(before))
	}
}

// TestOverlayModalBackdropFollowsActiveTheme exercises the real render path —
// the dimmed rows above and below the modal, and the dimmed left/right
// segments on the modal's own rows.
func TestOverlayModalBackdropFollowsActiveTheme(t *testing.T) {
	background := strings.Repeat("background text here\n", 6)
	modal := "+----+\n| hi |\n+----+"

	applyThemeForTest(t, "default")
	beforeSeq := backdropColorSequence(t, OverlayModal(background, modal, 40, 6))

	applyThemeForTest(t, "tokyo-night")
	afterSeq := backdropColorSequence(t, OverlayModal(background, modal, 40, 6))

	if beforeSeq == "" || afterSeq == "" {
		t.Fatalf("no colour sequence found in backdrop (before=%q after=%q)", beforeSeq, afterSeq)
	}
	if beforeSeq == afterSeq {
		t.Fatalf("modal backdrop dimmed with the same colour under both themes (%q); "+
			"the dim style is frozen to the theme active at init", beforeSeq)
	}
}

// backdropColorSequence returns the first SGR sequence in the rendered
// overlay, which is the escape the dim style opens the first backdrop cell
// with.
func backdropColorSequence(t *testing.T, rendered string) string {
	t.Helper()
	idx := strings.Index(rendered, "\x1b[")
	if idx < 0 {
		return ""
	}
	end := strings.IndexByte(rendered[idx:], 'm')
	if end < 0 {
		return ""
	}
	return rendered[idx : idx+end+1]
}
