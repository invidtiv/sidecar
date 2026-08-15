package panemodal

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
)

const bgMarker = "PANECONTENT"

func newModal() *modal.Modal {
	return modal.New("Delete file").
		AddSection(modal.Text("Delete internal/main.go?")).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn("Delete", "confirm", modal.BtnDanger()),
			modal.Btn("Cancel", "cancel"),
		))
}

func background(box Box) string {
	line := strings.Repeat(bgMarker+" ", box.W/len(bgMarker)+1)
	lines := make([]string, box.H)
	for i := range lines {
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func TestRenderFillsExactlyTheBox(t *testing.T) {
	for _, box := range []Box{
		{X: 10, Y: 5, W: 80, H: 24},
		{X: 0, Y: 0, W: 40, H: 14},
		{X: 3, Y: 2, W: 34, H: 9},
	} {
		out := Render(newModal(), box, background(box), mouse.NewHandler())
		lines := strings.Split(out, "\n")
		if len(lines) != box.H {
			t.Errorf("box %+v: got %d lines, want %d", box, len(lines), box.H)
			continue
		}
		for i, line := range lines {
			if w := ansi.StringWidth(line); w != box.W {
				t.Errorf("box %+v line %d: width %d, want %d", box, i, w, box.W)
			}
		}
	}
}

func TestRoomyBoxCentresAndDimsPaneContent(t *testing.T) {
	box := Box{X: 12, Y: 4, W: 90, H: 26}
	out := Render(newModal(), box, background(box), mouse.NewHandler())
	plain := ansi.Strip(out)

	if !strings.Contains(plain, bgMarker) {
		t.Error("roomy box dropped the pane content instead of dimming it around the modal")
	}
	if !strings.Contains(plain, "Delete file") {
		t.Error("modal title missing from output")
	}
	// The modal is centred: its top border row keeps pane content on both sides.
	var found bool
	for _, line := range strings.Split(plain, "\n") {
		runes := []rune(line)
		start := -1
		end := -1
		for i, r := range runes {
			if r == '╭' {
				start = i
			}
			if r == '╮' {
				end = i
			}
		}
		if start < 0 || end < 0 {
			continue
		}
		found = true
		leftMargin := start
		rightMargin := len(runes) - 1 - end
		if leftMargin < dimMargin || rightMargin < dimMargin {
			t.Errorf("margins left=%d right=%d, want at least %d cells of pane content on each side",
				leftMargin, rightMargin, dimMargin)
		}
		if diff := leftMargin - rightMargin; diff > 1 || diff < -1 {
			t.Errorf("modal not centred: left margin %d, right margin %d", leftMargin, rightMargin)
		}
	}
	if !found {
		t.Fatal("no modal border row in output")
	}
}

func TestTightBoxFillsAndHidesPaneContent(t *testing.T) {
	box := Box{X: 2, Y: 1, W: 34, H: 10}
	out := Render(newModal(), box, background(box), mouse.NewHandler())
	plain := ansi.Strip(out)

	if strings.Contains(plain, bgMarker) {
		t.Error("tight box still shows pane content behind the modal")
	}
	if !strings.Contains(plain, "Delete file") {
		t.Error("modal title missing from output")
	}
}

func TestRegionsLandAtTheBoxOrigin(t *testing.T) {
	size := Box{W: 70, H: 22}
	origin := Box{X: 17, Y: 6, W: size.W, H: size.H}

	atOrigin := mouse.NewHandler()
	Render(newModal(), size, background(size), atOrigin)
	baseline := atOrigin.HitMap.Regions()
	if len(baseline) < 3 {
		t.Fatalf("expected the modal to register regions, got %d", len(baseline))
	}

	offset := mouse.NewHandler()
	Render(newModal(), origin, background(origin), offset)
	shifted := offset.HitMap.Regions()

	if len(shifted) != len(baseline) {
		t.Fatalf("region count changed: %d in a pane vs %d at the origin", len(shifted), len(baseline))
	}
	for i, r := range baseline {
		got := shifted[i]
		if got.ID != r.ID {
			t.Fatalf("region %d: id %q, want %q (order must be preserved)", i, got.ID, r.ID)
		}
		want := mouse.Rect{X: r.Rect.X + origin.X, Y: r.Rect.Y + origin.Y, W: r.Rect.W, H: r.Rect.H}
		if got.Rect != want {
			t.Errorf("region %q: rect %+v, want %+v", r.ID, got.Rect, want)
		}
	}

	// A click at the box origin plus (5,3) hits whatever the modal drew at its
	// own (5,3).
	want := atOrigin.HitMap.Test(5, 3)
	got := offset.HitMap.Test(origin.X+5, origin.Y+3)
	if want == nil || got == nil {
		t.Fatalf("hit test returned nil: origin=%v pane=%v", want, got)
	}
	if got.ID != want.ID {
		t.Errorf("click at (%d,%d) hit %q, want %q", origin.X+5, origin.Y+3, got.ID, want.ID)
	}

	// Same for a real focusable: the Cancel button.
	var button *mouse.Region
	for i := range baseline {
		if baseline[i].ID == "cancel" {
			button = &baseline[i]
		}
	}
	if button == nil {
		t.Fatal("cancel button did not register a hit region")
	}
	hit := offset.HitMap.Test(origin.X+button.Rect.X+1, origin.Y+button.Rect.Y)
	if hit == nil || hit.ID != "cancel" {
		t.Errorf("click on the cancel button in a pane hit %v, want cancel", hit)
	}
}

func TestRenderDegenerateBox(t *testing.T) {
	if out := Render(newModal(), Box{X: 3, Y: 3, W: 0, H: 10}, "", mouse.NewHandler()); out != "" {
		t.Errorf("zero-width box rendered %q, want empty", out)
	}
	if out := Render(nil, Box{W: 10, H: 2}, "", nil); strings.Count(out, "\n") != 1 {
		t.Errorf("nil modal rendered %q, want 2 blank lines", out)
	}
}
