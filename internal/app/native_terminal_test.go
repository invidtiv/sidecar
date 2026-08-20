package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
)

type nativeTestPlugin struct {
	focused bool
	cursor  *tea.Cursor
	mouse   tea.MouseMode
	seen    []tea.Msg
}

func (p *nativeTestPlugin) ID() string                        { return "native-test" }
func (p *nativeTestPlugin) Name() string                      { return "Native Test" }
func (p *nativeTestPlugin) Icon() string                      { return "" }
func (p *nativeTestPlugin) Init(*plugin.Context) error        { return nil }
func (p *nativeTestPlugin) Start() tea.Cmd                    { return nil }
func (p *nativeTestPlugin) Stop()                             {}
func (p *nativeTestPlugin) View(int, int) string              { return "terminal" }
func (p *nativeTestPlugin) IsFocused() bool                   { return p.focused }
func (p *nativeTestPlugin) SetFocused(focused bool)           { p.focused = focused }
func (p *nativeTestPlugin) Commands() []plugin.Command        { return nil }
func (p *nativeTestPlugin) FocusContext() string              { return "native-test" }
func (p *nativeTestPlugin) Cursor() *tea.Cursor               { return p.cursor }
func (p *nativeTestPlugin) PreferredMouseMode() tea.MouseMode { return p.mouse }
func (p *nativeTestPlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	p.seen = append(p.seen, msg)
	return p, nil
}

func TestViewDeclaresNativeTerminalCapabilities(t *testing.T) {
	cursor := tea.NewCursor(4, 5)
	cursor.Blink = false
	p := &nativeTestPlugin{
		focused: true,
		cursor:  cursor,
		mouse:   tea.MouseModeCellMotion,
	}
	m := nativeTestModel(t, p)

	view := m.View()
	if !view.ReportFocus {
		t.Fatal("View.ReportFocus = false")
	}
	if view.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("MouseMode = %v, want cell motion", view.MouseMode)
	}
	if view.Cursor == nil || view.Cursor.X != 4 || view.Cursor.Y != 5+headerHeight {
		t.Fatalf("Cursor = %#v, want plugin cursor offset by header", view.Cursor)
	}
	if view.KeyboardEnhancements.ReportEventTypes ||
		view.KeyboardEnhancements.ReportAlternateKeys ||
		view.KeyboardEnhancements.ReportAllKeysAsEscapeCodes ||
		view.KeyboardEnhancements.ReportAssociatedText {
		t.Fatalf("unexpected keyboard enhancement request: %#v", view.KeyboardEnhancements)
	}
	// The app must copy provider cursors rather than mutate plugin state.
	if cursor.Y != 5 {
		t.Fatalf("provider cursor Y mutated to %d", cursor.Y)
	}
}

func TestAppRoutesCompleteMouseGesturePastHeaderInPluginCoordinates(t *testing.T) {
	p := &nativeTestPlugin{focused: true, mouse: tea.MouseModeCellMotion}
	m := nativeTestModel(t, p)

	events := []tea.MouseMsg{
		tea.MouseClickMsg(tea.Mouse{X: 17, Y: headerHeight + 4, Button: tea.MouseLeft}),
		tea.MouseMotionMsg(tea.Mouse{X: 22, Y: headerHeight + 5, Button: tea.MouseLeft}),
		tea.MouseReleaseMsg(tea.Mouse{X: 22, Y: headerHeight + 5, Button: tea.MouseLeft}),
		tea.MouseWheelMsg(tea.Mouse{X: 22, Y: headerHeight + 5, Button: tea.MouseWheelDown}),
	}
	for _, event := range events {
		updated, cmd := m.Update(event)
		m = updated.(Model)
		if cmd != nil {
			t.Fatalf("%T unexpectedly returned a command", event)
		}
	}

	if len(p.seen) != len(events) {
		t.Fatalf("plugin saw %d events, want %d", len(p.seen), len(events))
	}
	for i, want := range []struct {
		typeName string
		x, y     int
		button   tea.MouseButton
	}{
		{"tea.MouseClickMsg", 17, 4, tea.MouseLeft},
		{"tea.MouseMotionMsg", 22, 5, tea.MouseLeft},
		{"tea.MouseReleaseMsg", 22, 5, tea.MouseLeft},
		{"tea.MouseWheelMsg", 22, 5, tea.MouseWheelDown},
	} {
		got, ok := p.seen[i].(tea.MouseMsg)
		if !ok {
			t.Fatalf("event %d reached plugin as %T, want mouse message", i, p.seen[i])
		}
		point := got.Mouse()
		if point.X != want.x || point.Y != want.y || point.Button != want.button {
			t.Fatalf("event %d (%s) = (%d,%d,%v), want (%d,%d,%v)", i, want.typeName,
				point.X, point.Y, point.Button, want.x, want.y, want.button)
		}
	}
}

func TestViewSuppressesPluginCursorAndCellMotionWhenCoveredOrBlurred(t *testing.T) {
	p := &nativeTestPlugin{
		focused: true,
		cursor:  tea.NewCursor(2, 3),
		mouse:   tea.MouseModeCellMotion,
	}
	m := nativeTestModel(t, p)

	m.applicationFocused = false
	if got := m.View().Cursor; got != nil {
		t.Fatalf("blurred cursor = %#v, want nil", got)
	}

	m.applicationFocused = true
	m.showHelp = true
	view := m.View()
	if view.Cursor != nil {
		t.Fatalf("modal-covered cursor = %#v, want nil", view.Cursor)
	}
	if view.MouseMode != tea.MouseModeAllMotion {
		t.Fatalf("modal MouseMode = %v, want all motion for hover UI", view.MouseMode)
	}
}

func TestViewRejectsOutOfBoundsPluginCursor(t *testing.T) {
	p := &nativeTestPlugin{
		focused: true,
		cursor:  tea.NewCursor(100, 0),
		mouse:   tea.MouseModeCellMotion,
	}
	m := nativeTestModel(t, p)
	if got := m.View().Cursor; got != nil {
		t.Fatalf("out-of-bounds cursor = %#v, want nil", got)
	}
}

func TestViewSuppressesPluginCursorForTooSmallWarning(t *testing.T) {
	p := &nativeTestPlugin{
		focused: true,
		cursor:  tea.NewCursor(2, 3),
		mouse:   tea.MouseModeCellMotion,
	}
	m := nativeTestModel(t, p)
	m.width = minWidth - 1
	if got := m.View().Cursor; got != nil {
		t.Fatalf("too-small warning cursor = %#v, want nil", got)
	}
}

func TestFocusAndBlurUpdateStateAndReachAllPlugins(t *testing.T) {
	first := &nativeTestPlugin{focused: true}
	second := &nativeTestPlugin{}
	reg := plugin.NewRegistry(nil)
	if err := reg.Register(first); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(second); err != nil {
		t.Fatal(err)
	}
	m := Model{
		registry:           reg,
		keymap:             keymap.NewRegistry(),
		applicationFocused: true,
	}

	blurredModel, _ := m.Update(tea.BlurMsg{})
	blurred := blurredModel.(Model)
	if blurred.applicationFocused {
		t.Fatal("application remained focused after BlurMsg")
	}
	for _, p := range []*nativeTestPlugin{first, second} {
		if len(p.seen) != 1 {
			t.Fatalf("%s saw %d focus messages, want 1", p.ID(), len(p.seen))
		}
		if _, ok := p.seen[0].(tea.BlurMsg); !ok {
			t.Fatalf("first focus message = %T, want tea.BlurMsg", p.seen[0])
		}
	}

	focusedModel, _ := blurred.Update(tea.FocusMsg{})
	focused := focusedModel.(Model)
	if !focused.applicationFocused {
		t.Fatal("application remained blurred after FocusMsg")
	}
	for _, p := range []*nativeTestPlugin{first, second} {
		if len(p.seen) != 2 {
			t.Fatalf("%s saw %d focus messages, want 2", p.ID(), len(p.seen))
		}
		if _, ok := p.seen[1].(tea.FocusMsg); !ok {
			t.Fatalf("second focus message = %T, want tea.FocusMsg", p.seen[1])
		}
	}
}

func nativeTestModel(t *testing.T, p *nativeTestPlugin) Model {
	t.Helper()
	reg := plugin.NewRegistry(nil)
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	return Model{
		registry:           reg,
		keymap:             keymap.NewRegistry(),
		activePlugin:       0,
		ui:                 &UIState{},
		ready:              true,
		applicationFocused: true,
		width:              100,
		height:             30,
		intro:              IntroModel{Done: true},
	}
}
