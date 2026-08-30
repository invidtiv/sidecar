package configui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
)

const (
	regionNotificationNative = "config-notifications-native"
	regionNotificationSound  = "config-notifications-sound"
	regionNotificationTest   = "config-notifications-test"
	notificationModeWidth    = 20
)

type notificationsState struct {
	checking bool
	checked  bool
	status   notifydelivery.Status
	testing  bool
	tested   bool
	result   notifydelivery.Result
}

func (m *Model) notifications() *notificationsState {
	if m.notificationsState == nil {
		m.notificationsState = &notificationsState{}
	}
	return m.notificationsState
}

func (m *Model) queueNotificationProbe() {
	if m.Page() != PageNotifications {
		return
	}
	state := m.notifications()
	if state.checking {
		return
	}
	state.checking, state.checked = true, false
	m.pending = append(m.pending, func() tea.Msg { return ProbeNotificationDeliveryMsg{} })
}

func notificationModeOptions() []dropdownOption {
	return []dropdownOption{
		{id: string(config.DeliveryOff), label: "Off"},
		{id: string(config.DeliveryBackground), label: "Background only"},
		{id: string(config.DeliveryAlways), label: "Always"},
	}
}

func saveNativeMode(_ *Model, option dropdownOption) tea.Cmd {
	mode := config.DeliveryMode(option.id)
	return SaveCmd("System notifications: "+option.label, func() error {
		return config.SaveNotifications(func(cfg *config.NotificationsConfig) { cfg.Native.Mode = mode })
	})
}

func saveSoundMode(_ *Model, option dropdownOption) tea.Cmd {
	mode := config.DeliveryMode(option.id)
	return SaveCmd("Sounds: "+option.label, func() error {
		return config.SaveNotifications(func(cfg *config.NotificationsConfig) { cfg.Sound.Mode = mode })
	})
}

func (m *Model) testNotifications() tea.Cmd {
	state := m.notifications()
	state.testing, state.tested = true, false
	return func() tea.Msg { return TestNotificationDeliveryMsg{Event: notifydelivery.TestWaiting} }
}

func (m *Model) buildNotifications(b *paneBuilder) {
	cfg := m.Config().Notifications
	state := m.notifications()

	b.text(PaneTitle(PageTitle(PageNotifications)), "")
	b.lead("Choose how Sidecar gets your attention when work changes state.")

	b.text(SectionHeader("Delivery"))
	b.selectRow(regionNotificationNative, "System notifications", notificationModeWidth,
		notificationModeOptions(), string(cfg.Native.Mode), saveNativeMode)
	b.selectRow(regionNotificationSound, "Sounds", notificationModeWidth,
		notificationModeOptions(), string(cfg.Sound.Mode), saveSoundMode)
	if b.inner >= 55 {
		b.help("Background only stays quiet for the workspace already in front of you.")
	}

	b.text(SectionHeader("Rules"))
	b.note(notificationRulesSummary(cfg))

	b.text(SectionHeader("Status & test"))
	b.text(FormRow("Delivery status", StaticField(notificationStatusSummary(state), b.controlWidth(48), State{Disabled: true}), State{}))
	b.keyChips(chipSpec{id: regionNotificationTest, key: "t", keys: "T", label: "Test enabled channels", run: func(m *Model) tea.Cmd {
		return m.testNotifications()
	}})
	if state.testing {
		b.note("Testing enabled providers…")
	} else if state.tested {
		b.note(notificationTestSummary(state.result))
	}
}

func notificationRulesSummary(cfg config.NotificationsConfig) string {
	quiet := "off"
	if cfg.QuietHours.Enabled {
		quiet = cfg.QuietHours.Start + "–" + cfg.QuietHours.End
	}
	return "Quiet hours " + quiet + ". Source rules keep their configured defaults; editing arrives in a focused route."
}

func notificationStatusSummary(state *notificationsState) string {
	if state == nil || state.checking {
		return "Checking providers…"
	}
	if !state.checked {
		return "Not checked"
	}
	channel := func(name string, capability notifydelivery.Capability) string {
		if !capability.Available {
			return name + " unavailable"
		}
		provider := capability.Provider
		if provider == "" {
			provider = "ready"
		}
		return name + " " + provider
	}
	return channel("Native", state.status.Native) + " · " + channel("Sound", state.status.Sound)
}

func notificationTestSummary(result notifydelivery.Result) string {
	return "Native " + channelTestSummary(result.Native) + " · Sound " + channelTestSummary(result.Sound)
}

func channelTestSummary(result notifydelivery.ChannelResult) string {
	switch {
	case result.Delivered && result.Error != "":
		return "delivered; coordination failed: " + result.Error
	case result.Delivered:
		return "delivered"
	case result.Error != "":
		return "failed: " + result.Error
	case result.Reason != "":
		return strings.ReplaceAll(string(result.Reason), "_", " ")
	case result.Attempted:
		return "not delivered"
	default:
		return string(notify.ReasonUnavailable)
	}
}
