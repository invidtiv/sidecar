package configui

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
)

const (
	ChildNotificationAgentRules ChildID = "notification-agent-rules"
	ChildNotificationOtherRules ChildID = "notification-other-rules"
	ChildNotificationQuietHours ChildID = "notification-quiet-hours"
	ChildNotificationSoundPaths ChildID = "notification-sound-paths"
	ChildNotificationStatus     ChildID = "notification-status"
	ChildNotificationSourceRule ChildID = "notification-source-rule"

	regionNotificationNative      = "config-notifications-native"
	regionNotificationSound       = "config-notifications-sound"
	regionNotificationQuiet       = "config-notifications-quiet"
	regionNotificationAgentRules  = "config-notifications-agent-rules"
	regionNotificationOtherRules  = "config-notifications-other-rules"
	regionNotificationSoundPaths  = "config-notifications-sound-paths"
	regionNotificationStatus      = "config-notifications-status"
	regionNotificationTest        = "config-notifications-test"
	regionNotificationQuietEnable = "config-notifications-quiet-enable"
	regionNotificationQuietStart  = "config-notifications-quiet-start"
	regionNotificationQuietEnd    = "config-notifications-quiet-end"
	regionNotificationRuleToast   = "config-notifications-rule-toast"
	regionNotificationRuleNative  = "config-notifications-rule-native"
	regionNotificationRuleSound   = "config-notifications-rule-sound"
	regionNotificationRuleExpiry  = "config-notifications-rule-expiry"
	regionNotificationRecheck     = "config-notifications-recheck"
	regionNotificationPathPrefix  = "config-notifications-path-"
	regionNotificationSource      = "config-notifications-source-"

	notificationModeWidth  = 20
	notificationFieldWidth = 48
)

type notificationsState struct {
	checking bool
	checked  bool
	status   notifydelivery.Status
	testing  bool
	tested   bool
	result   notifydelivery.Result

	configChecking  bool
	configChecked   bool
	configError     string
	probeGeneration uint64
	inputs          map[string]*textinput.Model
	source          notify.SourceID
	event           notifydelivery.TestEvent
	sourceTitle     string
}

func (m *Model) notifications() *notificationsState {
	if m.notificationsState == nil {
		m.notificationsState = &notificationsState{inputs: map[string]*textinput.Model{}}
	}
	return m.notificationsState
}

func (m *Model) queueNotificationProbe() {
	if m.Page() != PageNotifications {
		return
	}
	state := m.notifications()
	if state.checking || state.configChecking {
		return
	}
	state.checking, state.checked = true, false
	state.configChecking, state.configChecked, state.configError = true, false, ""
	state.probeGeneration++
	generation := state.probeGeneration
	cfg := m.Config().Notifications
	path := config.ConfigPath()
	m.pending = append(m.pending,
		func() tea.Msg { return ProbeNotificationDeliveryMsg{Generation: generation} },
		func() tea.Msg {
			err := config.ValidateNotifications(cfg, path)
			if err != nil {
				return NotificationConfigValidationMsg{Generation: generation, Err: err.Error()}
			}
			return NotificationConfigValidationMsg{Generation: generation}
		},
	)
}

func notificationModeOptions() []dropdownOption {
	return []dropdownOption{
		{id: string(config.DeliveryOff), label: "Off"},
		{id: string(config.DeliveryBackground), label: "Background only"},
		{id: string(config.DeliveryAlways), label: "Always"},
	}
}

func notificationCueOptions() []dropdownOption {
	return []dropdownOption{
		{id: string(config.SoundNone), label: "None"},
		{id: string(config.SoundEvent), label: "Match event"},
		{id: string(config.SoundAttention), label: "Attention"},
		{id: string(config.SoundDone), label: "Done"},
		{id: string(config.SoundFailure), label: "Failure"},
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

func (m *Model) testNotifications(event notifydelivery.TestEvent, source notify.SourceID) tea.Cmd {
	state := m.notifications()
	state.testing, state.tested = true, false
	return func() tea.Msg { return TestNotificationDeliveryMsg{Event: event, Source: source} }
}

func (m *Model) buildNotifications(b *paneBuilder) {
	cfg := m.Config().Notifications
	state := m.notifications()

	b.text(PaneTitle(PageTitle(PageNotifications)), "")
	b.lead("Privacy: Sidecar never uploads alerts. Native title/body go only to the OS, which may retain or show them on a lock screen.")

	b.text(SectionHeader("Delivery"))
	b.selectRow(regionNotificationNative, "System notifications", notificationModeWidth,
		notificationModeOptions(), string(cfg.Native.Mode), saveNativeMode)
	b.selectRow(regionNotificationSound, "Sounds", notificationModeWidth,
		notificationModeOptions(), string(cfg.Sound.Mode), saveSoundMode)

	b.text(SectionHeader("Rules"))
	b.notificationRouteRow(regionNotificationQuiet, "Quiet hours", quietHoursSummary(cfg.QuietHours), func(m *Model) {
		m.PushChild(ChildNotificationQuietHours, "Quiet hours")
	})
	b.notificationRouteRow(regionNotificationAgentRules, "Agent activity", "3 events", func(m *Model) {
		m.PushChild(ChildNotificationAgentRules, "Agent activity")
	})
	b.notificationRouteRow(regionNotificationOtherRules, "Other sources", "Agent, TD, Tasks", func(m *Model) {
		m.PushChild(ChildNotificationOtherRules, "Other sources")
	})
	b.notificationRouteRow(regionNotificationSoundPaths, "Sound choices", customSoundsSummary(cfg.Sound), func(m *Model) {
		m.PushChild(ChildNotificationSoundPaths, "Sound choices")
	})

	b.text(SectionHeader("Status & test"))
	b.notificationRouteRow(regionNotificationStatus, "Delivery status", notificationStatusSummary(state), func(m *Model) {
		m.PushChild(ChildNotificationStatus, "Delivery status")
		m.queueNotificationProbe()
	})
	b.keyChips(chipSpec{id: regionNotificationTest, key: "t", keys: "T", label: "Test enabled channels", run: func(m *Model) tea.Cmd {
		return m.testNotifications(notifydelivery.TestWaiting, "")
	}})
	if state.testing {
		b.note("Testing enabled providers…")
	} else if state.tested && b.inner >= 55 {
		b.note(notificationTestSummary(state.result))
	}
}

func (b *paneBuilder) notificationRouteRow(id, label, summary string, open func(*Model)) {
	b.row(id, "", func(m *Model) tea.Cmd {
		open(m)
		return nil
	}, func(state State) string {
		return FormRow(label, StaticField(summary+"  ›", b.controlWidth(notificationFieldWidth), state), state)
	})
}

func quietHoursSummary(q config.QuietHoursConfig) string {
	if !q.Enabled {
		return "Off"
	}
	if q.Start == q.End {
		return q.Start + "–" + q.End + " (all day)"
	}
	return q.Start + "–" + q.End
}

func customSoundsSummary(sound config.SoundNotificationsConfig) string {
	count := 0
	for _, path := range []string{sound.AttentionPath, sound.DonePath, sound.FailurePath} {
		if strings.TrimSpace(path) != "" {
			count++
		}
	}
	if count == 0 {
		return "Built-in"
	}
	return fmt.Sprintf("%d custom", count)
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

func (m *Model) buildNotificationAgentRules(b *paneBuilder) {
	b.lead("Choose the Sidecar cues for each agent transition.")
	m.notificationSourceRouteRow(b, "needs-input", "Needs input", notify.SourceWaiting, notifydelivery.TestWaiting)
	m.notificationSourceRouteRow(b, "finished", "Finished", notify.SourceSession, notifydelivery.TestDone)
	m.notificationSourceRouteRow(b, "session-ended", "Session ended", notify.SourceSession, notifydelivery.TestFailure)
	if b.inner >= 55 {
		b.note("Finished and Session ended share the Session source rule; errors still use the failure cue while Match event is selected.")
	}
}

func (m *Model) buildNotificationOtherRules(b *paneBuilder) {
	b.lead("Other registered sources stay in Sidecar even when external delivery is off.")
	for _, item := range []struct {
		id    notify.SourceID
		label string
	}{
		{notify.SourceAgent, "Agent posts"},
		{notify.SourceTD, "TD"},
		{notify.SourceTasks, "Tasks"},
		{notify.SourceSystem, "System"},
	} {
		m.notificationSourceRouteRow(b, string(item.id), item.label, item.id, eventForSource(item.id))
	}
}

func eventForSource(id notify.SourceID) notifydelivery.TestEvent {
	if id == notify.SourceSession {
		return notifydelivery.TestDone
	}
	return notifydelivery.TestWaiting
}

func (m *Model) notificationSourceRouteRow(b *paneBuilder, suffix, label string, source notify.SourceID, event notifydelivery.TestEvent) {
	rule := notify.ResolveConfig(m.Config().Notifications).SourceRule(source)
	summary := fmt.Sprintf("Toast %s · Native %s · %s", onOffSummary(rule.Toast), onOffSummary(rule.Native), cueLabel(rule.Sound))
	id := regionNotificationSource + suffix
	b.notificationRouteRow(id, label, summary, func(m *Model) {
		state := m.notifications()
		state.source, state.event, state.sourceTitle = source, event, label
		m.PushChild(ChildNotificationSourceRule, label)
	})
}

func onOffSummary(value bool) string {
	if value {
		return "on"
	}
	return "off"
}

func cueLabel(cue config.SoundCue) string {
	for _, option := range notificationCueOptions() {
		if option.id == string(cue) {
			return option.label
		}
	}
	return "None"
}

func (m *Model) buildNotificationSourceRule(b *paneBuilder) {
	state := m.notifications()
	if !notify.ValidSource(state.source) {
		b.lead("Choose a notification source from the parent route.")
		return
	}
	rule := notify.ResolveConfig(m.Config().Notifications).SourceRule(state.source)
	b.lead("Set how " + state.sourceTitle + " appears in Sidecar and on the local desktop.")
	b.toggleRow(regionNotificationRuleToast, "In-app toast", rule.Toast, func(m *Model) tea.Cmd {
		next := !notify.ResolveConfig(m.Config().Notifications).SourceRule(state.source).Toast
		return saveNotificationSource(state.source, "In-app toast "+onOffSummary(next), func(rule *config.NotificationSourceConfig) { rule.Toast = boolPointer(next) })
	})
	b.toggleRow(regionNotificationRuleNative, "System notification", rule.Native, func(m *Model) tea.Cmd {
		next := !notify.ResolveConfig(m.Config().Notifications).SourceRule(state.source).Native
		return saveNotificationSource(state.source, "System notification "+onOffSummary(next), func(rule *config.NotificationSourceConfig) { rule.Native = boolPointer(next) })
	})
	b.selectRow(regionNotificationRuleSound, "Sound cue", notificationModeWidth, notificationCueOptions(), string(rule.Sound), func(_ *Model, option dropdownOption) tea.Cmd {
		return saveNotificationSource(state.source, "Sound cue: "+option.label, func(rule *config.NotificationSourceConfig) { rule.Sound = config.SoundCue(option.id) })
	})
	m.notificationEditorRow(b, regionNotificationRuleExpiry, "Toast duration", formatNotificationExpiry(rule.Expiry), "10s or sticky", func(value string) tea.Cmd {
		return saveNotificationSource(state.source, "Toast duration: "+value, func(rule *config.NotificationSourceConfig) { rule.Expiry = value })
	})
	b.keyChips(chipSpec{id: regionNotificationTest, key: "t", keys: "T", label: "Test this event", run: func(m *Model) tea.Cmd {
		return m.testNotifications(state.event, state.source)
	}})
	if state.source == notify.SourceSession {
		b.note("With Match event, a finished turn uses Done and an error automatically uses Failure.")
	}
	if state.testing {
		b.note("Testing enabled providers…")
	} else if state.tested && b.inner >= 55 {
		b.note(notificationTestSummary(state.result))
	}
}

func saveNotificationSource(source notify.SourceID, notice string, mutate func(*config.NotificationSourceConfig)) tea.Cmd {
	return SaveCmd(notice, func() error {
		return config.SaveNotifications(func(cfg *config.NotificationsConfig) {
			if cfg.Sources == nil {
				cfg.Sources = map[string]config.NotificationSourceConfig{}
			}
			rule := cfg.Sources[string(source)]
			mutate(&rule)
			cfg.Sources[string(source)] = rule
		})
	})
}

func boolPointer(value bool) *bool { return &value }

func formatNotificationExpiry(expiry time.Duration) string {
	if expiry == 0 {
		return "sticky"
	}
	return expiry.String()
}

func (m *Model) buildNotificationQuietHours(b *paneBuilder) {
	quiet := m.Config().Notifications.QuietHours
	b.lead("Quiet hours suppress sounds and system notifications only. In-app notifications remain.")
	b.toggleRow(regionNotificationQuietEnable, "Enabled", quiet.Enabled, func(m *Model) tea.Cmd {
		next := !m.Config().Notifications.QuietHours.Enabled
		return SaveCmd("Quiet hours "+onOffSummary(next), func() error {
			return config.SaveNotifications(func(cfg *config.NotificationsConfig) { cfg.QuietHours.Enabled = next })
		})
	})
	m.notificationEditorRow(b, regionNotificationQuietStart, "Start", quiet.Start, "22:00", func(value string) tea.Cmd {
		return SaveCmd("Quiet hours start: "+value, func() error {
			return config.SaveNotifications(func(cfg *config.NotificationsConfig) { cfg.QuietHours.Start = value })
		})
	})
	m.notificationEditorRow(b, regionNotificationQuietEnd, "End", quiet.End, "08:00", func(value string) tea.Cmd {
		return SaveCmd("Quiet hours end: "+value, func() error {
			return config.SaveNotifications(func(cfg *config.NotificationsConfig) { cfg.QuietHours.End = value })
		})
	})
	if quiet.Enabled && quiet.Start == quiet.End {
		b.note("Equal start and end means quiet all day.")
	} else {
		b.note("Use local 24-hour HH:MM times. Overnight ranges are supported.")
	}
}

func (m *Model) buildNotificationSoundPaths(b *paneBuilder) {
	sound := m.Config().Notifications.Sound
	b.lead("Leave a path empty to use Sidecar's built-in WAV. Relative paths resolve beside config.json.")
	for _, item := range []struct {
		cue   string
		label string
		value string
	}{
		{"attention", "Attention", sound.AttentionPath},
		{"done", "Done", sound.DonePath},
		{"failure", "Failure", sound.FailurePath},
	} {
		item := item
		m.notificationEditorRow(b, regionNotificationPathPrefix+item.cue, item.label, item.value, "Built-in", func(value string) tea.Cmd {
			return SaveCmd(item.label+" sound saved", func() error {
				return config.SaveNotifications(func(cfg *config.NotificationsConfig) {
					switch item.cue {
					case "attention":
						cfg.Sound.AttentionPath = value
					case "done":
						cfg.Sound.DonePath = value
					case "failure":
						cfg.Sound.FailurePath = value
					}
				})
			})
		})
	}
	if m.notifications().configChecked && m.notifications().configError == "" {
		b.note("Configured paths are readable regular files.")
	}
}

func (m *Model) notificationEditorRow(b *paneBuilder, id, label, value, placeholder string, save func(string) tea.Cmd) {
	input := m.notificationInput(id)
	editing := m.editingID() == id
	state := b.declare(id, "", true, func(m *Model) tea.Cmd {
		input.SetValue(value)
		input.Placeholder = placeholder
		m.openEditor(&editorState{
			id: id, input: input,
			submit: func(_ *Model) (tea.Cmd, bool) { return save(strings.TrimSpace(input.Value())), false },
			cancel: func(_ *Model) { input.SetValue(value) },
		})
		m.focusControlByID(id)
		return nil
	})
	fieldWidth := b.controlWidth(notificationFieldWidth)
	field := StaticField(value, fieldWidth, state)
	if value == "" {
		field = StaticField(placeholder, fieldWidth, state)
	}
	if editing {
		state.Focused = true
		field = Field(input, fieldWidth, state)
	}
	y := len(b.lines)
	b.lines = append(b.lines, FormRow(label, field, state))
	b.m.mouse.HitMap.AddRect(id, b.originX, 1+y, b.inner, 1, nil)
}

func (m *Model) notificationInput(id string) *textinput.Model {
	state := m.notifications()
	if input := state.inputs[id]; input != nil {
		return input
	}
	input := textinput.New()
	input.Prompt = ""
	input.CharLimit = 1024
	state.inputs[id] = &input
	return &input
}

func (m *Model) buildNotificationStatus(b *paneBuilder) {
	state := m.notifications()
	b.lead("Provider checks run only on this route or after a notification setting changes.")
	b.text(SectionHeader("Providers"))
	b.text(FormRow("System notifications", StaticField(capabilitySummary(state.status.Native, state.checked), b.controlWidth(notificationFieldWidth), State{Disabled: true}), State{}))
	b.text(FormRow("Sounds", StaticField(capabilitySummary(state.status.Sound, state.checked), b.controlWidth(notificationFieldWidth), State{Disabled: true}), State{}))
	remote := "Local host"
	if state.checked && state.status.Remote {
		remote = "Remote SSH context"
	}
	b.text(FormRow("Delivery context", StaticField(remote, b.controlWidth(notificationFieldWidth), State{Disabled: true}), State{}))
	custom := "Checking custom paths…"
	if state.configChecked {
		custom = "Valid"
		if state.configError != "" {
			custom = "Invalid: " + state.configError
		}
	}
	b.text(FormRow("Custom sounds", StaticField(custom, b.controlWidth(notificationFieldWidth), State{Disabled: true}), State{}))
	b.keyChips(chipSpec{id: regionNotificationRecheck, key: "r", keys: "R", label: "Recheck", run: func(m *Model) tea.Cmd {
		m.queueNotificationProbe()
		return m.TakePending()
	}})
	if guidance := notificationRepairGuidance(state); guidance != "" {
		b.text(SectionHeader("Repair guidance"))
		if command := notificationRepairCommand(state); command != "" {
			b.text(IndentedRaw(CodeChip(command)))
			b.keyChips(chipSpec{id: "config-notifications-copy-repair", key: "c", keys: "C", label: "Copy install command", run: func(*Model) tea.Cmd {
				return copyCmd(command, "Copied install command")
			}})
		}
		if b.inner >= 55 || notificationRepairCommand(state) == "" {
			b.note(guidance)
		}
	}
}

func capabilitySummary(capability notifydelivery.Capability, checked bool) string {
	if !checked {
		return "Checking…"
	}
	if capability.Available {
		ready := "Ready"
		if capability.Provider != "" {
			ready += " · " + capability.Provider
		}
		if capability.Reason != "" {
			ready += " · Warning: " + capability.Reason
		}
		return ready
	}
	if capability.Reason != "" {
		return "Unavailable · " + capability.Reason
	}
	return "Unavailable"
}

func notificationRepairGuidance(state *notificationsState) string {
	if state == nil || !state.checked {
		return ""
	}
	if state.status.Remote {
		return "This process appears to be remote over SSH. Sidecar reports local desktop providers honestly and does not send terminal escape notifications."
	}
	if runtime.GOOS == "darwin" {
		if state.status.Native.Available && state.status.Native.Provider == "osascript" {
			return "The osascript fallback may be attributed to Script Editor and cannot focus the hosting terminal. For activation support, install terminal-notifier with: brew install terminal-notifier"
		}
		if !state.status.Native.Available {
			return "No native provider is ready. Install terminal-notifier with: brew install terminal-notifier. Sidecar never installs packages automatically."
		}
	}
	if runtime.GOOS == "linux" {
		if !state.status.Native.Available {
			return "Install the distribution package that provides notify-send and ensure DISPLAY or WAYLAND_DISPLAY reaches Sidecar."
		}
		if !state.status.Sound.Available {
			return "Install a desktop audio player supported by your distribution, such as paplay, pw-play, aplay, ffplay, or mpv."
		}
	}
	return ""
}

func notificationRepairCommand(state *notificationsState) string {
	if runtime.GOOS != "darwin" || state == nil || !state.checked || state.status.Remote {
		return ""
	}
	if !state.status.Native.Available || state.status.Native.Provider == "osascript" {
		return "brew install terminal-notifier"
	}
	return ""
}
