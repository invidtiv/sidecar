package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

func TestRenderHandleVerticalInsetsVisibleBar(t *testing.T) {
	got := RenderHandle(5, true, HandleIdle)
	lines := strings.Split(ansi.Strip(got), "\n")
	if len(lines) != 5 {
		t.Fatalf("vertical lines = %d, want 5", len(lines))
	}
	for i, line := range lines {
		want := handleBarV
		if i == 0 || i == len(lines)-1 {
			want = " "
		}
		if line != want {
			t.Fatalf("line %d = %q, want %q", i, line, want)
		}
	}
}

func TestRenderHandleHorizontalInsetsVisibleBar(t *testing.T) {
	got := ansi.Strip(RenderHandle(7, false, HandleIdle))
	if want := " " + strings.Repeat(handleBarH, 5) + " "; got != want {
		t.Fatalf("horizontal = %q, want %q", got, want)
	}
}

func TestRenderHandleIdleBlendsTowardUnfocusedBorders(t *testing.T) {
	theme := styles.GetCurrentTheme()
	want := lipgloss.Color(styles.Blend(theme.Colors.BorderMuted, theme.Colors.BorderNormal, handleIdleBorderMix))
	if got := handleStyle(HandleIdle).GetForeground(); got != want {
		t.Fatalf("idle foreground = %v, want blended theme color %v", got, want)
	}
}

func TestRenderHandleStatesChangeColor(t *testing.T) {
	idle := RenderHandle(6, true, HandleIdle)
	hover := RenderHandle(6, true, HandleHover)
	drag := RenderHandle(6, true, HandleDrag)
	if idle == hover || hover == drag || idle == drag {
		t.Fatalf("states must render differently\nidle=%q\nhover=%q\ndrag=%q", idle, hover, drag)
	}
	want := " \n" + strings.TrimSuffix(strings.Repeat(handleBarV+"\n", 4), "\n") + "\n "
	for name, got := range map[string]string{"idle": idle, "hover": hover, "drag": drag} {
		if ansi.Strip(got) != want {
			t.Fatalf("%s stripped glyph drifted: %q", name, ansi.Strip(got))
		}
	}
}

func TestRenderHandleShortVerticalLengthsRemainAllocated(t *testing.T) {
	for _, length := range []int{1, 2} {
		got := strings.Split(ansi.Strip(RenderHandle(length, true, HandleIdle)), "\n")
		if len(got) != length {
			t.Fatalf("length %d rendered %d rows", length, len(got))
		}
		for i, line := range got {
			if line != " " {
				t.Fatalf("length %d line %d = %q, want blank allocated cell", length, i, line)
			}
		}
	}
}

func TestRenderHandleShortHorizontalLengthsRemainAllocated(t *testing.T) {
	for _, length := range []int{1, 2} {
		got := ansi.Strip(RenderHandle(length, false, HandleIdle))
		if got != strings.Repeat(" ", length) {
			t.Fatalf("length %d = %q, want blank allocated cells", length, got)
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
