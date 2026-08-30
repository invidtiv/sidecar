package configui

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
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

func TestNotificationChildRoutesFitAndKeepEveryControlReachable(t *testing.T) {
	tests := []struct {
		name     string
		open     func(*Model)
		wantText []string
		regions  []string
	}{
		{"quiet", func(m *Model) { m.PushChild(ChildNotificationQuietHours, "Quiet hours") }, []string{"Enabled", "Start", "End"}, []string{regionNotificationQuietEnable, regionNotificationQuietStart, regionNotificationQuietEnd}},
		{"agent rules", func(m *Model) { m.PushChild(ChildNotificationAgentRules, "Agent activity") }, []string{"Needs input", "Finished", "Session ended"}, []string{regionNotificationSource + "needs-input", regionNotificationSource + "finished", regionNotificationSource + "session-ended"}},
		{"other rules", func(m *Model) { m.PushChild(ChildNotificationOtherRules, "Other sources") }, []string{"Agent posts", "TD", "Tasks", "System"}, []string{regionNotificationSource + "agent", regionNotificationSource + "td", regionNotificationSource + "tasks", regionNotificationSource + "system"}},
		{"sound choices", func(m *Model) { m.PushChild(ChildNotificationSoundPaths, "Sound choices") }, []string{"Attention", "Done", "Failure"}, []string{regionNotificationPathPrefix + "attention", regionNotificationPathPrefix + "done", regionNotificationPathPrefix + "failure"}},
		{"status", func(m *Model) { m.PushChild(ChildNotificationStatus, "Delivery status") }, []string{"System notifications", "Sounds", "Delivery context", "Custom sounds", "Recheck"}, []string{regionNotificationRecheck}},
		{"source rule", func(m *Model) {
			state := m.notifications()
			state.source, state.event, state.sourceTitle = notify.SourceWaiting, notifydelivery.TestWaiting, "Needs input"
			m.PushChild(ChildNotificationSourceRule, "Needs input")
		}, []string{"In-app toast", "System notification", "Sound cue", "Toast duration", "Test this event"}, []string{regionNotificationRuleToast, regionNotificationRuleNative, regionNotificationRuleSound, regionNotificationRuleExpiry, regionNotificationTest}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := notificationFixture(t)
			m.Open(PageNotifications)
			test.open(m)
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
				for _, want := range test.wantText {
					if !strings.Contains(plain, want) {
						t.Fatalf("size=%v missing %q:\n%s", size, want, plain)
					}
				}
				for _, id := range test.regions {
					found := false
					for _, region := range m.mouse.HitMap.Regions() {
						if region.ID == id && region.Rect.Y < size[1]-1 {
							found = true
						}
					}
					if !found {
						t.Fatalf("size=%v control %q is not reachable", size, id)
					}
				}
			}
		})
	}
}

func TestNotificationRulesAndCustomPathsSaveWithoutLosingUnknownConfig(t *testing.T) {
	m, path := configFixture(t, config.Default())
	raw := []byte(`{"futureRoot":{"keep":true},"notifications":{"sources":{"future-source":{"expiry":"17s"}}}}`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	m.SetHostState(HostState{Config: loaded})
	m.Open(PageNotifications)
	m.PushChild(ChildNotificationAgentRules, "Agent activity")
	activate(t, m, regionNotificationSource+"needs-input")
	choose(t, m, regionNotificationRuleSound, string(config.SoundFailure))
	activate(t, m, regionNotificationRuleToast)

	activate(t, m, regionNotificationRuleExpiry)
	m.notificationInput(regionNotificationRuleExpiry).SetValue("17s")
	_, cmd := m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
	applySave(t, m, cmd)

	m.Back()
	m.Back()
	m.PushChild(ChildNotificationQuietHours, "Quiet hours")
	activate(t, m, regionNotificationQuietEnable)
	for id, value := range map[string]string{regionNotificationQuietStart: "10:00", regionNotificationQuietEnd: "10:00"} {
		activate(t, m, id)
		m.notificationInput(id).SetValue(value)
		_, cmd = m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
		applySave(t, m, cmd)
	}

	m.Back()
	m.PushChild(ChildNotificationSoundPaths, "Sound choices")
	validPath := filepath.Join(filepath.Dir(path), "attention.wav")
	if err := os.WriteFile(validPath, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	activate(t, m, regionNotificationPathPrefix+"attention")
	m.notificationInput(regionNotificationPathPrefix + "attention").SetValue("attention.wav")
	_, cmd = m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
	applySave(t, m, cmd)

	before, _ := os.ReadFile(path)
	activate(t, m, regionNotificationPathPrefix+"done")
	m.notificationInput(regionNotificationPathPrefix + "done").SetValue("missing.wav")
	_, cmd = m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg, ok := cmd().(ConfigSavedMsg)
	if !ok || msg.Err == "" {
		t.Fatalf("invalid custom path was not refused: %#v", msg)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("invalid custom path changed the previous valid config")
	}
	if !bytes.Contains(after, []byte(`"futureRoot"`)) || !bytes.Contains(after, []byte(`"future-source"`)) {
		t.Fatalf("notification saves lost unknown config: %s", after)
	}
	got := loadSaved(t).Notifications
	if got.Sources["waiting"].Sound != config.SoundFailure || got.Sources["waiting"].Toast == nil || *got.Sources["waiting"].Toast || got.Sources["waiting"].Expiry != "17s" {
		t.Fatalf("waiting rule=%+v", got.Sources["waiting"])
	}
	if !got.QuietHours.Enabled || got.QuietHours.Start != "10:00" || got.QuietHours.End != "10:00" || got.Sound.AttentionPath != "attention.wav" {
		t.Fatalf("saved notifications=%+v", got)
	}
}

func TestNotificationChildKeyboardMouseHoverDropdownAndEditorPrecedence(t *testing.T) {
	m := notificationFixture(t)
	m.Open(PageNotifications)
	_ = m.TakePending()
	m.View(100, 30)
	var quiet *mouse.Region
	for _, region := range m.mouse.HitMap.Regions() {
		if region.ID == regionNotificationQuiet {
			copy := region
			quiet = &copy
			break
		}
	}
	if quiet == nil {
		t.Fatal("quiet-hours route has no mouse target")
	}
	m.Mouse(tea.MouseMotionMsg{X: quiet.Rect.X + 1, Y: quiet.Rect.Y, Button: tea.MouseNone})
	if m.hoverID != regionNotificationQuiet {
		t.Fatalf("hover=%q", m.hoverID)
	}
	m.Mouse(tea.MouseClickMsg{X: quiet.Rect.X + 1, Y: quiet.Rect.Y, Button: tea.MouseLeft})
	if m.Route().Child != ChildNotificationQuietHours {
		t.Fatalf("mouse did not open quiet-hours route: %#v", m.Route())
	}
	if !m.Back() || m.Route().IsChild() {
		t.Fatal("Back did not restore Notifications root")
	}

	m.PushChild(ChildNotificationOtherRules, "Other sources")
	activate(t, m, regionNotificationSource+"td")
	if m.Route().Child != ChildNotificationSourceRule || len(m.router.stack) != 3 {
		t.Fatalf("nested source route=%#v stack=%#v", m.Route(), m.router.stack)
	}
	choose(t, m, regionNotificationRuleSound, string(config.SoundAttention))
	activate(t, m, regionNotificationRuleSound)
	handled, cmd := m.Key(tea.KeyPressMsg{Code: 't', Text: "t"})
	if !handled || cmd != nil || m.dropdown == nil {
		t.Fatal("t escaped the source sound dropdown")
	}
	m.closeDropdown()

	activate(t, m, regionNotificationRuleExpiry)
	before := m.notificationInput(regionNotificationRuleExpiry).Value()
	handled, cmd = m.Key(tea.KeyPressMsg{Code: 't', Text: "t"})
	if !handled || batchContains[TestNotificationDeliveryMsg](cmd) || m.notificationInput(regionNotificationRuleExpiry).Value() == before {
		t.Fatal("typed t escaped the expiry editor")
	}
	if !m.Escape() || m.editing() {
		t.Fatal("Escape did not cancel the expiry edit")
	}

	m.View(100, 30)
	handled, cmd = m.Key(tea.KeyPressMsg{Code: 't', Text: "t"})
	if !handled || !batchContains[TestNotificationDeliveryMsg](cmd) {
		t.Fatal("source-rule Test shortcut did not run")
	}
	msg := cmd()
	var testMsg TestNotificationDeliveryMsg
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, nested := range batch {
			if got, ok := nested().(TestNotificationDeliveryMsg); ok {
				testMsg = got
			}
		}
	} else {
		testMsg, _ = msg.(TestNotificationDeliveryMsg)
	}
	if testMsg.Source != notify.SourceTD {
		t.Fatalf("TD test used source %q", testMsg.Source)
	}
	if !m.Back() || m.Route().Child != ChildNotificationOtherRules || !m.Back() || m.Route().IsChild() {
		t.Fatalf("nested back stack did not restore parent then root: %#v", m.router.stack)
	}
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
	for _, query := range []string{"sound", "audio", "native", "desktop", "system notification", "waiting", "finished", "quiet hours", "terminal-notifier", "afplay", "notify-send", "paplay", "pw-play", "aplay", "ffplay", "mpv"} {
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
		plain := strings.Join(strings.Fields(ansi.Strip(view)), " ")
		for _, want := range []string{"Notifications", "Privacy:", "never uploads", "title/body", "only", "to the OS", "lock", "screen.", "retain", "System notifications", "Sounds", "Delivery status", "Test enabled channels"} {
			if !strings.Contains(plain, want) {
				t.Fatalf("size=%v missing %q:\n%s", size, want, plain)
			}
		}
	}
}

func TestNotificationProbeRefreshIgnoresResultsFromBeforeSave(t *testing.T) {
	m := notificationFixture(t)
	m.Open(PageNotifications)
	first := m.notifications().probeGeneration
	_ = m.TakePending()
	m.RefreshNotificationProbe()
	second := m.notifications().probeGeneration
	if second <= first || m.TakePending() == nil {
		t.Fatalf("refresh generation=%d after initial=%d without a new probe", second, first)
	}

	m.Handle(NotificationDeliveryStatusMsg{Generation: first, Status: notifydelivery.Status{
		Native: notifydelivery.Capability{Available: true, Provider: "stale-native"},
	}})
	m.Handle(NotificationConfigValidationMsg{Generation: first, Err: "stale validation"})
	state := m.notifications()
	if !state.checking || !state.configChecking || state.checked || state.configChecked {
		t.Fatalf("stale probe settled current generation: %+v", state)
	}

	m.Handle(NotificationDeliveryStatusMsg{Generation: second, Status: notifydelivery.Status{
		Native: notifydelivery.Capability{Available: true, Provider: "fresh-native"},
	}})
	m.Handle(NotificationConfigValidationMsg{Generation: second})
	if !state.checked || !state.configChecked || state.status.Native.Provider != "fresh-native" || state.configError != "" {
		t.Fatalf("fresh probe did not settle current generation: %+v", state)
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

func TestNotificationCapabilitySummaryPreservesAvailableWarning(t *testing.T) {
	got := capabilitySummary(notifydelivery.Capability{
		Available: true, Provider: "afplay", Reason: "custom file unsupported; built-in fallback ready",
	}, true)
	for _, want := range []string{"Ready", "afplay", "Warning", "custom file unsupported", "built-in fallback ready"} {
		if !strings.Contains(got, want) {
			t.Fatalf("capability summary %q omitted %q", got, want)
		}
	}
}

func TestNotificationStatusOffersCopyableMacRepairWithoutInstalling(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS provider guidance")
	}
	m := notificationFixture(t)
	m.Open(PageNotifications)
	_ = m.TakePending()
	m.PushChild(ChildNotificationStatus, "Delivery status")
	m.Handle(NotificationDeliveryStatusMsg{Status: notifydelivery.Status{
		Native: notifydelivery.Capability{Available: true, Provider: "osascript"},
		Sound:  notifydelivery.Capability{Available: true, Provider: "afplay"},
	}})
	plain := ansi.Strip(m.View(100, 30))
	if !strings.Contains(plain, "brew install terminal-notifier") || !strings.Contains(plain, "Copy install command") {
		t.Fatalf("status omitted copyable repair guidance:\n%s", plain)
	}
	handled, cmd := m.Key(tea.KeyPressMsg{Code: 'c', Text: "c"})
	if !handled || cmd == nil {
		t.Fatal("copy repair shortcut did not run")
	}
	copy, ok := cmd().(CopyMsg)
	if !ok || copy.Text != "brew install terminal-notifier" {
		t.Fatalf("copy action=%#v", copy)
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
