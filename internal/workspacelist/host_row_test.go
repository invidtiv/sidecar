package workspacelist

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The host prefix says which machine exactly once.
//
// Under the sorts that show a project label, the label is where the machine's
// name already lives ("mini · api"); the glyph and the per-host colour are what
// make it read as a machine rather than as a project that happens to be named
// that. Under the Project sort the heading carries the label and the row does
// not, so only the glyph is left — which is still what a reader needs at a
// glance.

func remoteItem() Item {
	return Item{
		ID: "mini\x1fapi:shell:s1", Name: "Claude pane", Host: "mini",
		Project: "mini · api", ProjectKey: "mini\x1f/home/me/api", Kind: KindShell,
		Marker: RowMarker{Icon: "◆", Lane: "blocked"},
	}
}

func TestRemoteRowMarksItsHostWithoutRepeatingTheName(t *testing.T) {
	prefix := rowNamePrefix(remoteItem(), true)
	if !strings.HasPrefix(prefix.Text, HostGlyph+" ") {
		t.Errorf("prefix = %q, want the host glyph first", prefix.Text)
	}
	if got := strings.Count(prefix.Text, "mini"); got != 1 {
		t.Errorf("prefix names the host %d times, want 1: %q", got, prefix.Text)
	}
	if !strings.Contains(ansi.Strip(prefix.Rendered), HostGlyph) {
		t.Errorf("the styled prefix lost the glyph: %q", ansi.Strip(prefix.Rendered))
	}
}

// Under the Project sort the heading says the machine, so the row must not.
func TestRemoteRowKeepsTheGlyphWhenTheHeadingCarriesTheLabel(t *testing.T) {
	prefix := rowNamePrefix(remoteItem(), false)
	if prefix.Text != HostGlyph+" " {
		t.Errorf("prefix = %q, want the glyph alone", prefix.Text)
	}
}

func TestLocalRowHasNoHostPrefix(t *testing.T) {
	local := Item{Name: "modal", Project: "sidecar", ProjectKey: "sidecar", Kind: KindWorktree}
	if prefix := rowNamePrefix(local, true); prefix.Text != "sidecar " {
		t.Errorf("prefix = %q, want the project label alone", prefix.Text)
	}
	if prefix := rowNamePrefix(local, false); prefix.Text != "" {
		t.Errorf("prefix = %q, want nothing", prefix.Text)
	}
}

// A remote row is drawn in its host's colour rather than its project's, so
// every row from one machine reads as one machine.
func TestRemoteRowUsesTheHostHue(t *testing.T) {
	item := remoteItem()
	rendered := rowNamePrefix(item, true).Rendered
	other := item
	other.Host = "other-box"
	if rendered == rowNamePrefix(other, true).Rendered {
		t.Error("two hosts rendered the same prefix; the colour is not per-host")
	}
}

// TestRemoteRowIsFindableByHostAlone. The global browser also writes the host
// into the project label, but a caller that carries it in Host alone must
// still be searchable by machine.
func TestRemoteRowIsFindableByHostAlone(t *testing.T) {
	item := remoteItem()
	item.Project = "api"
	if !Match(item, "mini") {
		t.Error("a remote row is not findable by its host name")
	}
	if Match(item, "some-other-host") {
		t.Error("a remote row matched an unrelated host name")
	}
}
