package configui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
)

func notificationFixture(t *testing.T) *Model {
	t.Helper()
	m, _ := configFixture(t, config.Default())
	return m
}

func TestNotificationsFollowsAgentsAndIsSearchable(t *testing.T) {
	pages := AllPages()
	for i, page := range pages {
		if page.ID == PageAgents {
			if i+1 >= len(pages) || pages[i+1].ID != PageNotifications {
				t.Fatalf("Notifications does not follow Agents: %#v", pages)
			}
		}
	}
	for _, query := range []string{"sound", "audio", "native", "desktop", "system notification", "waiting", "finished", "quiet hours", "terminal-notifier", "afplay"} {
		found := false
		for _, match := range Search(query) {
			found = found || match.Page == PageNotifications
		}
		if !found {
			t.Errorf("search %q did not find Notifications", query)
		}
	}
}

func TestNotificationsRootFitsSupportedSizes(t *testing.T) {
	m := notificationFixture(t)
	m.Open(PageNotifications)
	for _, size := range [][2]int{{60, 24}, {100, 30}, {160, 45}} {
		view := m.View(size[0], size[1])
		lines := strings.Split(view, "\n")
		if len(lines) != size[1] {
			t.Fatalf("size=%v height=%d", size, len(lines))
		}
		for i, line := range lines {
			if width := ansi.StringWidth(line); width > size[0] {
				t.Fatalf("size=%v line=%d width=%d", size, i, width)
			}
		}
		plain := ansi.Strip(view)
		for _, want := range []string{"Notifications", "System notifications", "Sounds", "Delivery status", "Test enabled channels"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("size=%v missing %q:\n%s", size, want, plain)
			}
		}
	}
}

func TestNotificationsModesSaveThroughSharedTargetedConfig(t *testing.T) {
	m := notificationFixture(t)
	m.Open(PageNotifications)
	choose(t, m, regionNotificationNative, string(config.DeliveryBackground))
	choose(t, m, regionNotificationSound, string(config.DeliveryAlways))
	got := loadSaved(t).Notifications
	if got.Native.Mode != config.DeliveryBackground || got.Sound.Mode != config.DeliveryAlways {
		t.Fatalf("saved modes = native %q sound %q", got.Native.Mode, got.Sound.Mode)
	}
}

func TestNotificationsProbeIsLazyAndTestActionIsPageLocal(t *testing.T) {
	m := notificationFixture(t)
	m.Open(PageAgents)
	if cmd := m.TakePending(); cmd != nil {
		t.Fatal("opening Agents queued a notification provider probe")
	}
	m.View(160, 45)
	for _, command := range m.Commands() {
		if command.ID == "test-notifications" {
			t.Fatal("Test leaked onto Agents")
		}
	}

	m.Navigate(PageNotifications)
	if cmd := m.TakePending(); !batchContains[ProbeNotificationDeliveryMsg](cmd) {
		t.Fatal("entering Notifications did not queue its provider probe")
	}
	m.View(160, 45)
	found := false
	for _, command := range m.Commands() {
		found = found || command.ID == "test-notifications" && command.Context == ContextConfig
	}
	if !found {
		t.Fatal("Notifications did not advertise its Test action")
	}
	handled, cmd := m.Key(tea.KeyPressMsg{Code: 't', Text: "t"})
	if !handled || !batchContains[TestNotificationDeliveryMsg](cmd) {
		t.Fatal("t did not run the page-local explicit test")
	}

	activate(t, m, regionNotificationNative)
	if m.dropdown == nil {
		t.Fatal("native selector did not open")
	}
	handled, cmd = m.Key(tea.KeyPressMsg{Code: 't', Text: "t"})
	if !handled || cmd != nil || m.dropdown == nil {
		t.Fatal("t escaped an open dropdown")
	}
	m.closeDropdown()
	m.Handle(NotificationTestResultMsg{})
	m.focusSearch()
	handled, _ = m.Key(tea.KeyPressMsg{Code: 't', Text: "t"})
	if !handled || m.Query() != "t" || m.notifications().testing {
		t.Fatalf("t escaped Search: handled=%v query=%q testing=%v", handled, m.Query(), m.notifications().testing)
	}
	m.ClearSearch()
	m.confirm = &confirmState{title: "Confirm"}
	m.View(160, 45)
	for _, command := range m.Commands() {
		if command.ID == "test-notifications" {
			t.Fatal("Test leaked into config-confirm")
		}
	}
	handled, cmd = m.Key(tea.KeyPressMsg{Code: 't', Text: "t"})
	if !handled || cmd != nil || m.notifications().testing {
		t.Fatal("t escaped config-confirm")
	}
	m.confirm = nil
	m.Navigate(PageAgents)
	for _, command := range m.Commands() {
		if command.ID == "test-notifications" {
			t.Fatal("Test remained advertised after leaving Notifications")
		}
	}
}

func TestNotificationsTestMouseAndResultStatus(t *testing.T) {
	m := notificationFixture(t)
	m.Open(PageNotifications)
	m.View(160, 45)
	var region *mouse.Region
	for _, candidate := range m.mouse.HitMap.Regions() {
		if candidate.ID == regionNotificationTest {
			copy := candidate
			region = &copy
			break
		}
	}
	if region == nil {
		t.Fatal("Test action has no mouse region")
	}
	cmd := m.Mouse(tea.MouseClickMsg{X: region.Rect.X + 1, Y: region.Rect.Y, Button: tea.MouseLeft})
	if !batchContains[TestNotificationDeliveryMsg](cmd) {
		t.Fatal("clicking Test did not request an explicit test")
	}
	m.Handle(NotificationDeliveryStatusMsg{Status: notifydelivery.Status{
		Native: notifydelivery.Capability{Available: true, Provider: "terminal-notifier"},
		Sound:  notifydelivery.Capability{Available: true, Provider: "afplay"},
	}})
	m.Handle(NotificationTestResultMsg{Result: notifydelivery.Result{
		Native: notifydelivery.ChannelResult{Attempted: true, Delivered: true, Provider: "terminal-notifier"},
		Sound:  notifydelivery.ChannelResult{Reason: "channel_off"},
	}})
	plain := ansi.Strip(m.View(160, 45))
	if !strings.Contains(plain, "Native terminal-notifier · Sound afplay") || !strings.Contains(plain, "Native delivered · Sound channel off") {
		t.Fatalf("status/result not rendered:\n%s", plain)
	}
}

func TestNotificationChannelSummaryPreservesDeliveryWithCoordinationFailure(t *testing.T) {
	got := channelTestSummary(notifydelivery.ChannelResult{
		Attempted: true, Provider: "fake-native", Delivered: true,
		Reason: notify.ReasonCoordination, Error: "complete delivery receipt: fixture failed",
	})
	want := "delivered; coordination failed: complete delivery receipt: fixture failed"
	if got != want {
		t.Fatalf("mixed channel summary=%q want=%q", got, want)
	}
}

func batchContains[T any](cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	if _, ok := msg.(T); ok {
		return true
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, nested := range batch {
			if batchContains[T](nested) {
				return true
			}
		}
	}
	return false
}
