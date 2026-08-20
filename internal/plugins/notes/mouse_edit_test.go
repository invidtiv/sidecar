package notes

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
)

func editorMousePoint(p *Plugin, visualRow, visualCol int) (int, int) {
	return p.listWidth + dividerWidth + 2 + p.editorLayout().leftMargin + visualCol,
		p.editorContentStartY() + visualRow
}

func clickAndRelease(p *Plugin, x, y int) {
	_, _ = p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	_, _ = p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
}

func TestBuiltInDoubleClickSelectsWordAndWordDragExtends(t *testing.T) {
	p := newEditPlugin(t, "alpha beta gamma")
	_ = p.View(p.width, p.height)
	x, y := editorMousePoint(p, 0, len("alpha b"))

	clickAndRelease(p, x, y)
	_, _ = p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	if got := strings.Join(p.getSelectedText(), "\n"); got != "beta" {
		t.Fatalf("double-click selection = %q, want beta", got)
	}

	dragX, _ := editorMousePoint(p, 0, len("alpha beta ga"))
	_, _ = p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: dragX, Y: y, Button: tea.MouseLeft}))
	if got := strings.Join(p.getSelectedText(), "\n"); got != "beta gamma" {
		t.Fatalf("word drag selection = %q, want %q", got, "beta gamma")
	}
	_, _ = p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: dragX, Y: y, Button: tea.MouseLeft}))
	if p.selection.Active {
		t.Fatal("word-drag release left selection active")
	}
}

func TestBuiltInTripleClickSelectsLogicalLineAndLineDragExtends(t *testing.T) {
	p := newEditPlugin(t, "first logical line\nsecond logical line")
	_ = p.View(p.width, p.height)
	x, y := editorMousePoint(p, 0, 3)

	clickAndRelease(p, x, y)
	clickAndRelease(p, x, y)
	_, _ = p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	if got := strings.Join(p.getSelectedText(), "\n"); got != "first logical line" {
		t.Fatalf("triple-click selection = %q, want first logical line", got)
	}

	_, dragY := editorMousePoint(p, 1, 3)
	_, _ = p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: x, Y: dragY, Button: tea.MouseLeft}))
	if got := strings.Join(p.getSelectedText(), "\n"); got != "first logical line\nsecond logical line" {
		t.Fatalf("line drag selection = %q", got)
	}
	_, _ = p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: x, Y: dragY, Button: tea.MouseLeft}))
	if p.selection.Active {
		t.Fatal("line-drag release left selection active")
	}
}

func TestClickAfterWrappedUnicodeEOLThenTypeAppendsAtLogicalEnd(t *testing.T) {
	content := strings.Repeat("界🙂", 18) + " tail"
	p := newEditPlugin(t, content)
	p.width, p.listWidth = 58, 18
	p.updateTextareaDimensions()
	_ = p.View(p.width, p.height)

	raw := markdown.MapWrappedSource(content, p.editorLayout().wrapColumn)
	if len(raw.Lines) < 2 {
		t.Fatalf("test content did not wrap at %d columns", p.editorLayout().wrapColumn)
	}
	last := len(raw.Lines) - 1
	lineWidth := ansi.StringWidth(raw.Lines[last])
	x, y := editorMousePoint(p, last, lineWidth+4)
	clickAndRelease(p, x, y)
	if got, want := p.editorTextarea.Column(), utf8.RuneCountInString(content); got != want {
		t.Fatalf("after-EOL caret = %d, want logical end %d", got, want)
	}

	typeKey(p, tea.KeyPressMsg{Code: 'Z', Text: "Z"})
	if got := p.editorTextarea.Value(); got != content+"Z" {
		t.Fatalf("typing after wrapped Unicode EOL = %q, want appended Z", got)
	}
}

func TestInlineEditorMouseRequiresReportingAndWheelUsesTerminalRoute(t *testing.T) {
	logPath := installNotesFakeTmux(t)
	p := New()
	p.store = &Store{}
	p.width, p.height, p.listWidth = 100, 24, 30
	p.activePane = PaneEditor
	p.focused = true
	p.edit.Active = true
	p.edit.Name = "mouse-editor"
	p.edit.Model.Enter("mouse-editor", "")
	p.edit.Model.Width = p.calculateInlineEditorWidth()
	p.edit.Model.Height = p.calculateInlineEditorHeight()
	applyCapture := func(reporting bool) {
		_, _ = p.Update(tty.CaptureResultMsg{
			Scope:          p.edit.Model.Scope(),
			PollGeneration: p.edit.Model.State.PollGeneration,
			Target:         "mouse-editor",
			Output:         "mouse-aware editor",
			CursorVisible:  true,
			PaneWidth:      p.edit.Model.Width,
			PaneHeight:     p.edit.Model.Height,
			MouseReporting: reporting,
		})
	}
	applyCapture(false)
	_ = p.View(p.width, p.height)
	x, y := p.listWidth+dividerWidth+2, 2

	if _, cmd := p.Update(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})); cmd != nil {
		t.Fatal("non-reporting editor received a click command")
	}
	if _, cmd := p.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}); cmd != nil {
		t.Fatal("non-reporting editor received a wheel command")
	}

	applyCapture(true)
	if !p.edit.Model.PaneMouseReporting() {
		t.Fatal("accepted live capture did not enable pane mouse ownership")
	}
	_, press := p.Update(tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft}))
	if press == nil {
		t.Fatal("reporting editor received no click command")
	}
	_ = press()
	_, drag := p.Update(tea.MouseMotionMsg(tea.Mouse{X: x + 5, Y: y, Button: tea.MouseLeft}))
	if drag == nil {
		t.Fatal("reporting editor received no drag command")
	}
	_ = drag()
	_, release := p.Update(tea.MouseReleaseMsg(tea.Mouse{X: x + 5, Y: y, Button: tea.MouseLeft}))
	if release == nil {
		t.Fatal("reporting editor received no release command")
	}
	_ = release()
	if p.edit.Dragging {
		t.Fatal("release left inline drag active")
	}
	gestureLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, encoded := range map[string]string{
		"press":   "1b 5b 3c 30 3b 31 3b 31 4d",
		"drag":    "1b 5b 3c 33 32 3b 36 3b 31 4d",
		"release": "1b 5b 3c 30 3b 36 3b 31 6d",
	} {
		if !strings.Contains(string(gestureLog), encoded) {
			t.Fatalf("%s did not emit expected pane-local SGR %q; log=%q", name, encoded, gestureLog)
		}
	}

	_, wheel := p.Update(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown})
	if wheel == nil {
		t.Fatal("reporting editor received no wheel command")
	}
	wheelResult := wheel()
	batch, ok := wheelResult.(tea.BatchMsg)
	if !ok || len(batch) < 1 {
		t.Fatalf("reporting wheel returned %T, want tty send batch", wheelResult)
	}
	// The first command is the ordered pane send; the second is only its poll.
	_ = batch[0]()
	tty.WaitForPendingSends()
	wheelLog, err := os.ReadFile(logPath)
	if err != nil || strings.Count(string(wheelLog), "send-keys -t mouse-editor -H") < 2 {
		t.Fatalf("reporting wheel did not emit SGR: err=%v log=%q", err, wheelLog)
	}
	// Shared tty coverage pins exact notch conversion, burst coalescing and the
	// ten-notch flush cap; this production host assertion proves Notes uses it.
	if route, notches := tty.RouteWheel(tty.WheelInput{Delta: mouse.WheelScrollLines, MouseReporting: true, InPane: true, WritesEnabled: true}); route != tty.WheelPane || notches != 1 {
		t.Fatalf("terminal wheel route = (%v,%d), want pane/1", route, notches)
	}
}
