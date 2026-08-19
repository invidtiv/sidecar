package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
)

// sizingPlugin records every content box the shell hands it. The panel's whole
// contract with plugins is that box, so these tests read it rather than any
// panel-specific hook — a plugin must not need to know the centre exists.
type sizingPlugin struct {
	nativeTestPlugin
	id     string
	widths []int
}

func (p *sizingPlugin) ID() string           { return p.id }
func (p *sizingPlugin) Name() string         { return p.id }
func (p *sizingPlugin) FocusContext() string { return p.id }
func (p *sizingPlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	if size, ok := msg.(tea.WindowSizeMsg); ok {
		p.widths = append(p.widths, size.Width)
	}
	return p, nil
}

func (p *sizingPlugin) lastWidth() int {
	if len(p.widths) == 0 {
		return -1
	}
	return p.widths[len(p.widths)-1]
}

func centreTestModel(t *testing.T, plugins ...*sizingPlugin) Model {
	t.Helper()
	reg := plugin.NewRegistry(nil)
	for _, p := range plugins {
		if err := reg.Register(p); err != nil {
			t.Fatal(err)
		}
	}
	km := keymap.NewRegistry()
	keymap.RegisterDefaults(km)
	m := Model{
		registry:                reg,
		keymap:                  km,
		ui:                      &UIState{},
		ready:                   true,
		applicationFocused:      true,
		width:                   120,
		height:                  40,
		intro:                   IntroModel{Done: true},
		cfg:                     config.Default(),
		notifications:           notify.NewMemStore(),
		notificationCentreMouse: mouse.NewHandler(),
	}
	m.updateContext()
	return m
}

func postCentreNotification(t *testing.T, m *Model, source notify.SourceID, title string) notify.Notification {
	t.Helper()
	stored, err := m.notifications.Post(notify.Notification{Source: source, Title: title})
	if err != nil {
		t.Fatal(err)
	}
	m.refreshNotifications()
	return stored
}

// The reservation is the feature: every plugin must be handed the narrowed
// width, and the narrowing must arrive as an ordinary resize.
func TestOpeningTheCentreNarrowsEveryPluginAndReEmitsSize(t *testing.T) {
	first := &sizingPlugin{id: "files"}
	second := &sizingPlugin{id: "git"}
	m := centreTestModel(t, first, second)

	m.toggleNotificationCentre()
	reserved := m.reservedRightWidth()
	if reserved <= 0 {
		t.Fatalf("reservedRightWidth = %d, want a reserved column", reserved)
	}
	want := m.width - reserved
	for _, p := range []*sizingPlugin{first, second} {
		if got := p.lastWidth(); got != want {
			t.Fatalf("%s width = %d, want %d", p.id, got, want)
		}
	}
	if got := m.contentWidth(); got != want {
		t.Fatalf("contentWidth = %d, want %d", got, want)
	}

	m.toggleNotificationCentre()
	for _, p := range []*sizingPlugin{first, second} {
		if got := p.lastWidth(); got != m.width {
			t.Fatalf("%s width after close = %d, want %d", p.id, got, m.width)
		}
	}
}

// A terminal resize while the panel is open must keep handing out the narrowed
// width rather than resetting to the full terminal.
func TestTerminalResizeKeepsTheReservation(t *testing.T) {
	p := &sizingPlugin{id: "files"}
	m := centreTestModel(t, p)
	m.toggleNotificationCentre()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 160, Height: 50})
	m = asAppModel(t, updated)
	if !m.notificationCentreOpen {
		t.Fatal("resize closed the centre")
	}
	want := 160 - m.reservedRightWidth()
	if got := p.lastWidth(); got != want {
		t.Fatalf("width after resize = %d, want %d", got, want)
	}
}

// Tab switches, modals, and clicks into content are navigation, not a close.
func TestCentreSurvivesTabSwitchesAndModals(t *testing.T) {
	first := &sizingPlugin{id: "files"}
	second := &sizingPlugin{id: "git"}
	m := centreTestModel(t, first, second)
	m.toggleNotificationCentre()
	reserved := m.reservedRightWidth()

	m.selectProjectTabByNumber(1)
	if !m.notificationCentreOpen || m.reservedRightWidth() != reserved {
		t.Fatal("switching tabs disturbed the centre")
	}
	if got := second.lastWidth(); got != m.width-reserved {
		t.Fatalf("plugin width after tab switch = %d, want %d", got, m.width-reserved)
	}

	// A modal takes the keyboard while it is up and gives it back afterwards,
	// without the panel ever closing.
	m.showHelp = true
	m.updateContext()
	if m.activeContext == notificationCentreContext {
		t.Fatal("an open modal left the centre holding the keyboard")
	}
	if !m.notificationCentreOpen {
		t.Fatal("opening a modal closed the centre")
	}
	m.showHelp = false
	m.updateContext()
	if m.activeContext != notificationCentreContext {
		t.Fatalf("context after modal = %q, want the centre to take the keyboard back", m.activeContext)
	}
	if !m.notificationCentreOpen {
		t.Fatal("closing a modal closed the centre")
	}
}

// The highest-risk path: a project switch rebuilds every plugin, so the
// reservation has to be restored before the next frame.
func TestCentreSurvivesProjectSwitchReinit(t *testing.T) {
	source := newOverviewGitRepo(t, "source")
	target := newOverviewGitRepo(t, "target")
	isolateAppState(t)

	cfg := config.Default()
	km := keymap.NewRegistry()
	ctx := &plugin.Context{WorkDir: source, ProjectRoot: source, Config: cfg, Keymap: km}
	reg := plugin.NewRegistry(ctx)
	p := &sizingPlugin{id: "files"}
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	m := New(reg, km, cfg, "", source, source, "files")
	m.width, m.height, m.ready = 120, 40, true
	m.toggleNotificationCentre()
	reserved := m.reservedRightWidth()
	if reserved <= 0 {
		t.Fatal("centre reserved nothing to begin with")
	}

	m.switchProjectWithInventory(target, nil)

	if !m.notificationCentreOpen {
		t.Fatal("a project switch closed the centre")
	}
	if got := m.reservedRightWidth(); got != reserved {
		t.Fatalf("reservation after switch = %d, want %d", got, reserved)
	}
	if got := p.lastWidth(); got != m.width-reserved {
		t.Fatalf("plugin width after Reinit = %d, want %d", got, m.width-reserved)
	}
}

// Clicking back into content returns focus; it does not close the panel.
func TestClickingContentBlursTheCentreWithoutClosingIt(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "files"})
	m.toggleNotificationCentre()
	if !m.notificationCentreOwnsKeys() {
		t.Fatal("opening the centre did not give it the keyboard")
	}
	m.View() // registers the panel's hit regions

	click := tea.MouseClickMsg{Button: tea.MouseLeft, X: 3, Y: 5}
	updated, _ := m.Update(click)
	m = asAppModel(t, updated)
	if !m.notificationCentreOpen {
		t.Fatal("a click in the content closed the centre")
	}
	if m.notificationCentreFocused {
		t.Fatal("a click in the content left focus on the centre")
	}
	if m.activeContext == notificationCentreContext {
		t.Fatalf("context after content click = %q", m.activeContext)
	}
}

// Esc with the panel focused is an explicit close, and the only kind there is.
func TestEscapeClosesTheFocusedCentre(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "files"})
	m.toggleNotificationCentre()
	m.handleKeyMsg(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.notificationCentreOpen {
		t.Fatal("esc did not close the focused centre")
	}
	if m.reservedRightWidth() != 0 {
		t.Fatal("a closed centre still reserves a column")
	}
}

func TestCentreListKeysMoveDismissAndGroupDismiss(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "files"})
	// Two sources so `D` has a group to clear and something to leave behind.
	postCentreNotification(t, &m, notify.SourceTasks, "task one")
	postCentreNotification(t, &m, notify.SourceTasks, "task two")
	postCentreNotification(t, &m, notify.SourceAgent, "agent one")
	m.toggleNotificationCentre()

	items := m.notificationCentreItems()
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	m.handleKeyMsg(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m.notificationCentreCursor != 1 {
		t.Fatalf("cursor after j = %d, want 1", m.notificationCentreCursor)
	}
	m.handleKeyMsg(tea.KeyPressMsg{Code: 'k', Text: "k"})
	if m.notificationCentreCursor != 0 {
		t.Fatalf("cursor after k = %d, want 0", m.notificationCentreCursor)
	}

	// `d` dismisses the selected notification outright, as it does on a toast,
	// and leaves the rest of its source alone.
	m.notificationCentreCursor = 1
	second := items[1]
	m.handleKeyMsg(tea.KeyPressMsg{Code: 'd', Text: "d"})
	remaining := m.notificationCentreItems()
	if len(remaining) != 2 {
		t.Fatalf("items after d = %d, want 2", len(remaining))
	}
	for _, n := range remaining {
		if n.ID == second.ID {
			t.Fatal("d left the selected notification in the list")
		}
	}

	// `D` clears the selected item's whole source and nothing else.
	m.notificationCentreCursor = 0
	source := notify.SourceOf(remaining[0].Source).ID
	m.handleKeyMsg(tea.KeyPressMsg{Code: 'D', Text: "D", Mod: tea.ModShift})
	left := m.notificationCentreItems()
	for _, n := range left {
		if notify.SourceOf(n.Source).ID == source {
			t.Fatalf("D left %q behind in source %s", n.Title, source)
		}
	}
	if len(left) == 0 {
		t.Fatal("D cleared sources it was not pointed at")
	}
}

// enter is deliberately inert in Phase 1, and consumed so it cannot mean
// something else by accident.
func TestCentreEnterIsANoOp(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "files"})
	postCentreNotification(t, &m, notify.SourceTasks, "task one")
	m.toggleNotificationCentre()
	before := len(m.notificationCentreItems())
	handled, cmd := m.notificationCentreKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || cmd != nil {
		t.Fatalf("enter handled=%v cmd=%v, want consumed and inert", handled, cmd != nil)
	}
	if got := len(m.notificationCentreItems()); got != before {
		t.Fatalf("enter changed the list: %d -> %d", before, got)
	}
}

// Navigation keys still work while the panel has focus: it is a panel, not a
// modal.
func TestCentreLeavesNavigationKeysAlone(t *testing.T) {
	first := &sizingPlugin{id: "files"}
	second := &sizingPlugin{id: "git"}
	m := centreTestModel(t, first, second)
	m.toggleNotificationCentre()
	m.handleKeyMsg(tea.KeyPressMsg{Code: '2', Text: "2"})
	if m.activePlugin != 1 {
		t.Fatalf("activePlugin = %d, want the tab number to still switch tabs", m.activePlugin)
	}
	if !m.notificationCentreOpen {
		t.Fatal("a tab switch closed the centre")
	}
}

// A terminal with nothing to spare keeps its width for content. The panel is
// still open, so widening the terminal brings it back with no user action.
func TestCentreYieldsOnATerminalWithNoRoom(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "files"})
	m.toggleNotificationCentre()
	m.width = 62
	if got := m.reservedRightWidth(); got != 0 {
		t.Fatalf("reserved = %d on a 62-column terminal, want 0", got)
	}
	if got := m.contentWidth(); got != 62 {
		t.Fatalf("contentWidth = %d, want the whole terminal", got)
	}
	if !m.notificationCentreOpen {
		t.Fatal("a narrow terminal closed the centre")
	}
	m.width = 120
	if m.reservedRightWidth() <= 0 {
		t.Fatal("widening the terminal did not bring the panel back")
	}
}

func TestCentreWidthClamps(t *testing.T) {
	if got := clampNotificationCentreWidth(notificationCentreMaxWidth+40, 200); got != notificationCentreMaxWidth {
		t.Fatalf("clamp(huge) = %d, want %d", got, notificationCentreMaxWidth)
	}
	if got := clampNotificationCentreWidth(2, 200); got != notificationCentreMinWidth {
		t.Fatalf("clamp(tiny) = %d, want %d", got, notificationCentreMinWidth)
	}
	if got := clampNotificationCentreWidth(40, 60); got != 0 {
		t.Fatalf("clamp on a cramped terminal = %d, want 0", got)
	}
}

// The panel paints its own title, close affordance, section grammar, and the
// retention note — and it paints them inside the reserved column, so nothing it
// draws lands on the content.
func TestCentreRendersTheSectionGrammar(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "files"})
	postCentreNotification(t, &m, notify.SourceTasks, "a due task")
	m.toggleNotificationCentre()

	panel := m.renderNotificationCentre(m.height - headerHeight - footerHeight)
	if panel == "" {
		t.Fatal("panel rendered nothing")
	}
	for _, want := range []string{"Notifications", "TASKS", "a due task", notificationCentreFootnote} {
		if !strings.Contains(panel, want) {
			t.Fatalf("panel is missing %q", want)
		}
	}
	for _, line := range strings.Split(panel, "\n") {
		if got := lipgloss.Width(line); got != m.reservedRightWidth() {
			t.Fatalf("panel row width = %d, want %d (%q)", got, m.reservedRightWidth(), line)
		}
	}
}

func TestCentreAgeMetaColumn(t *testing.T) {
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		age  time.Duration
		want string
	}{
		{10 * time.Second, "now"},
		{4 * time.Minute, "4m"},
		{3 * time.Hour, "3h"},
		{50 * time.Hour, "2d"},
	}
	for _, tc := range cases {
		if got := notificationAge(now.Add(-tc.age), now); got != tc.want {
			t.Fatalf("notificationAge(%s) = %q, want %q", tc.age, got, tc.want)
		}
	}
}
