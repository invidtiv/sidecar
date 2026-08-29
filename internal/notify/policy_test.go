package notify

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

func policyConfig(native, sound config.DeliveryMode) ResolvedConfig {
	cfg := config.DefaultNotificationsConfig()
	cfg.Native.Mode, cfg.Sound.Mode = native, sound
	return resolveConfig(cfg)
}

func TestResolveDeliveryModeSourceAndForegroundMatrix(t *testing.T) {
	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.Local)
	n := Notification{Source: SourceWaiting, Severity: SeverityWarning, CreatedAt: now}
	capable := CapabilitySet{Native: true, Sound: true}
	tests := []struct {
		name       string
		cfg        ResolvedConfig
		foreground bool
		native     Reason
		sound      Reason
		deliver    bool
	}{
		{"off", policyConfig(config.DeliveryOff, config.DeliveryOff), false, ReasonChannelOff, ReasonChannelOff, false},
		{"background visible", policyConfig(config.DeliveryBackground, config.DeliveryBackground), true, ReasonForeground, ReasonForeground, false},
		{"background hidden", policyConfig(config.DeliveryBackground, config.DeliveryBackground), false, "", "", true},
		{"always visible", policyConfig(config.DeliveryAlways, config.DeliveryAlways), true, "", "", true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveDelivery(n, test.cfg, RuntimeContext{Now: now, Foreground: test.foreground, Capabilities: capable})
			if got.Native.Reason != test.native || got.Sound.Reason != test.sound || got.Native.Deliver != test.deliver || got.Sound.Deliver != test.deliver {
				t.Fatalf("decision = %+v", got)
			}
		})
	}

	disabled := config.DefaultNotificationsConfig()
	disabled.Native.Mode, disabled.Sound.Mode = config.DeliveryAlways, config.DeliveryAlways
	disabled.Sources = map[string]config.NotificationSourceConfig{
		"waiting": {Native: boolPtrNotify(false), Sound: config.SoundNone},
	}
	got := ResolveDelivery(n, resolveConfig(disabled), RuntimeContext{Now: now, Capabilities: capable})
	if got.Native.Reason != ReasonSourceOff || got.Sound.Reason != ReasonSourceOff {
		t.Fatalf("source-off decision = %+v", got)
	}
}

func boolPtrNotify(v bool) *bool { return &v }

func TestResolveDeliveryQuietHoursStalenessAvailabilityAndExplicitTest(t *testing.T) {
	loc := time.FixedZone("local", -7*60*60)
	now := time.Date(2026, 8, 29, 23, 0, 0, 0, loc)
	cfg := config.DefaultNotificationsConfig()
	cfg.Native.Mode, cfg.Sound.Mode = config.DeliveryBackground, config.DeliveryBackground
	cfg.QuietHours = config.QuietHoursConfig{Enabled: true, Start: "22:00", End: "08:00"}
	n := Notification{Source: SourceWaiting, CreatedAt: now.Add(-time.Minute)}
	runtime := RuntimeContext{Now: now, Discovered: true, Foreground: true, Capabilities: CapabilitySet{Native: true, Sound: true}}
	got := ResolveDelivery(n, resolveConfig(cfg), runtime)
	if got.Native.Reason != ReasonQuietHours || got.Sound.Reason != ReasonQuietHours {
		t.Fatalf("quiet decision = %+v", got)
	}
	runtime.ExplicitTest = true
	got = ResolveDelivery(n, resolveConfig(cfg), runtime)
	if !got.Native.Deliver || !got.Sound.Deliver {
		t.Fatalf("explicit test should bypass quiet, stale, and foreground: %+v", got)
	}
	runtime.ExplicitTest = false
	cfg.QuietHours.Enabled = false
	runtime.Foreground = false
	got = ResolveDelivery(n, resolveConfig(cfg), runtime)
	if got.Native.Reason != ReasonStale || got.Sound.Reason != ReasonStale {
		t.Fatalf("stale decision = %+v", got)
	}
	runtime.Capabilities = CapabilitySet{}
	got = ResolveDelivery(Notification{Source: SourceWaiting, CreatedAt: now}, resolveConfig(cfg), runtime)
	if got.Native.Reason != ReasonUnavailable || got.Sound.Reason != ReasonUnavailable {
		t.Fatalf("availability decision = %+v", got)
	}
	cfg.QuietHours = config.QuietHoursConfig{Enabled: true, Start: "22:00", End: "08:00"}
	runtime.Capabilities = CapabilitySet{Native: true, Sound: true}
	runtime.Now = time.Date(2026, 8, 29, 12, 0, 0, 0, loc)
	got = ResolveDelivery(Notification{Source: SourceWaiting, CreatedAt: runtime.Now}, resolveConfig(cfg), runtime)
	if !got.Native.Deliver || !got.Sound.Deliver {
		t.Fatalf("outside quiet hours was suppressed: %+v", got)
	}
	cfg.QuietHours = config.QuietHoursConfig{Enabled: true, Start: "10:00", End: "10:00"}
	got = ResolveDelivery(Notification{Source: SourceWaiting, CreatedAt: now}, resolveConfig(cfg), runtime)
	if got.Native.Reason != ReasonQuietHours {
		t.Fatalf("equal quiet hours must mean all day: %+v", got)
	}
}

func TestResolveDeliveryEventCues(t *testing.T) {
	cfg := policyConfig(config.DeliveryAlways, config.DeliveryAlways)
	runtime := RuntimeContext{Now: time.Now(), Capabilities: CapabilitySet{Native: true, Sound: true}}
	for _, test := range []struct {
		n   Notification
		cue Cue
	}{
		{Notification{Source: SourceWaiting, Severity: SeverityWarning}, CueAttention},
		{Notification{Source: SourceSession, Severity: SeverityInfo}, CueDone},
		{Notification{Source: SourceSession, Severity: SeverityError}, CueFailure},
		{Notification{Source: SourceAgent, Severity: SeverityInfo}, CueNone},
	} {
		test.n.CreatedAt = runtime.Now
		if got := ResolveDelivery(test.n, cfg, runtime).Cue; got != test.cue {
			t.Fatalf("%s/%s cue = %q, want %q", test.n.Source, test.n.Severity, got, test.cue)
		}
	}
}

func TestResolveDeliveryZeroTimeIsDeterministicAndSkipsClockRefusals(t *testing.T) {
	cfg := resolveConfig(config.NotificationsConfig{
		Native: config.NativeNotificationsConfig{Mode: config.DeliveryAlways},
		QuietHours: config.QuietHoursConfig{
			Enabled: true,
			Start:   "00:00",
			End:     "00:00",
		},
	})
	n := Notification{Source: SourceWaiting, CreatedAt: time.Unix(1, 0)}
	runtime := RuntimeContext{Discovered: true, Capabilities: CapabilitySet{Native: true}}

	first := ResolveDelivery(n, cfg, runtime)
	second := ResolveDelivery(n, cfg, runtime)
	if first != second {
		t.Fatalf("identical zero-time inputs differ: first=%+v second=%+v", first, second)
	}
	if !first.Native.Deliver || first.Native.Reason != "" {
		t.Fatalf("zero-time native decision = %+v, want delivery without clock-dependent refusal", first.Native)
	}
}

func TestResolveDeliveryNeverDeliversDismissedRecord(t *testing.T) {
	now := time.Now().UTC()
	n := Notification{Source: SourceWaiting, CreatedAt: now, DismissedAt: &now}
	decision := ResolveDelivery(n, resolveConfig(config.DefaultNotificationsConfig()), RuntimeContext{
		Now: now, ExplicitTest: true, Capabilities: CapabilitySet{Native: true, Sound: true},
	})
	if decision.Native.Deliver || decision.Native.Reason != ReasonCancelled || decision.Sound.Deliver || decision.Sound.Reason != ReasonCancelled {
		t.Fatalf("dismissed decision = %+v", decision)
	}
}
