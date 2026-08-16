package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderHandleVerticalOccupiesOneCell(t *testing.T) {
	got := RenderHandle(5, true, HandleIdle)
	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) != 5 {
		t.Fatalf("vertical lines = %d, want 5", len(lines))
	}
	for i, line := range lines {
		if line != handleBarV {
			t.Fatalf("line %d = %q, want %q", i, line, handleBarV)
		}
	}
}

func TestRenderHandleHorizontalOccupiesOneRow(t *testing.T) {
	got := ansi.Strip(RenderHandle(7, false, HandleIdle))
	if got != strings.Repeat(handleBarH, 7) {
		t.Fatalf("horizontal = %q, want seven %q", got, handleBarH)
	}
}

func TestRenderHandleStatesChangeColor(t *testing.T) {
	idle := RenderHandle(6, true, HandleIdle)
	hover := RenderHandle(6, true, HandleHover)
	drag := RenderHandle(6, true, HandleDrag)
	if idle == hover || hover == drag || idle == drag {
		t.Fatalf("states must render differently\nidle=%q\nhover=%q\ndrag=%q", idle, hover, drag)
	}
	for name, got := range map[string]string{"idle": idle, "hover": hover, "drag": drag} {
		if ansi.Strip(got) != strings.TrimSuffix(strings.Repeat(handleBarV+"\n", 6), "\n") {
			t.Fatalf("%s stripped glyph drifted: %q", name, ansi.Strip(got))
		}
	}
}

func TestRenderHandleEmptyLength(t *testing.T) {
	if got := RenderHandle(0, true, HandleIdle); got != "" {
		t.Fatalf("zero length = %q, want empty", got)
	}
	if got := RenderHandle(-1, false, HandleHover); got != "" {
		t.Fatalf("negative length = %q, want empty", got)
	}
}

func TestRenderDividerIsIdleVerticalHandle(t *testing.T) {
	if got, want := RenderDivider(4), RenderHandle(4, true, HandleIdle); got != want {
		t.Fatalf("RenderDivider = %q, want RenderHandle idle vertical %q", got, want)
	}
}

func TestHandleStateFromPrecedence(t *testing.T) {
	if got := HandleStateFrom(true, true); got != HandleDrag {
		t.Fatalf("hover+drag = %v, want drag", got)
	}
	if got := HandleStateFrom(true, false); got != HandleHover {
		t.Fatalf("hover = %v, want hover", got)
	}
	if got := HandleStateFrom(false, false); got != HandleIdle {
		t.Fatalf("neither = %v, want idle", got)
	}
}
