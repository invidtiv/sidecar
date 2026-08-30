package app

import (
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
	"github.com/marcus/sidecar/internal/uirequest"
)

func TestConfigurationNotificationProbeAndTestRunOnlyInCommands(t *testing.T) {
	delivery := &fakeDeliveryCoordinator{
		status: notifydelivery.Status{
			Native: notifydelivery.Capability{Available: true, Provider: "fake-native"},
			Sound:  notifydelivery.Capability{Available: true, Provider: "fake-sound"},
		},
		result: notifydelivery.Result{
			Native: notifydelivery.ChannelResult{Attempted: true, Delivered: true, Provider: "fake-native"},
			Sound:  notifydelivery.ChannelResult{Attempted: true, Delivered: true, Provider: "fake-sound"},
		},
	}
	m, _ := scopeBaselineModel(t, "git")
	m.notificationDelivery = delivery
	m.config.Open(configui.PageNotifications)
	m.config.SetHostState(m.configHostState())
	before := len(m.notificationCache)

	probeCmd, handled := m.configSurfaceMsg(configui.ProbeNotificationDeliveryMsg{})
	if !handled || probeCmd == nil || delivery.probes != 0 {
		t.Fatalf("probe handled=%v cmd=%v synchronous calls=%d", handled, probeCmd != nil, delivery.probes)
	}
	probeMsg, ok := probeCmd().(configui.NotificationDeliveryStatusMsg)
	if !ok || delivery.probes != 1 || probeMsg.Status.Native.Provider != "fake-native" {
		t.Fatalf("probe message=%#v calls=%d", probeMsg, delivery.probes)
	}

	testCmd, handled := m.configSurfaceMsg(configui.TestNotificationDeliveryMsg{Event: notifydelivery.TestDone})
	if !handled || testCmd == nil || len(delivery.requests) != 0 {
		t.Fatalf("test handled=%v cmd=%v synchronous requests=%d", handled, testCmd != nil, len(delivery.requests))
	}
	resultMsg, ok := testCmd().(configui.NotificationTestResultMsg)
	if !ok || len(delivery.requests) != 1 || !delivery.requests[0].ExplicitTest || delivery.requests[0].Notification.Source != notify.SourceSession {
		t.Fatalf("result=%#v requests=%+v", resultMsg, delivery.requests)
	}
	if len(m.notificationCache) != before {
		t.Fatal("Configuration test created a notification-centre record")
	}
	flashCmd, handled := m.configSurfaceMsg(resultMsg)
	if !handled || flashCmd == nil {
		t.Fatal("test result did not produce outcome feedback")
	}
}

func TestNotificationTestFlashPreservesDeliveryWithCoordinationFailure(t *testing.T) {
	got := notificationTestFlash(notifydelivery.Result{
		Native: notifydelivery.ChannelResult{
			Attempted: true, Provider: "fake-native", Delivered: true,
			Reason: notify.ReasonCoordination, Error: "complete delivery receipt: fixture failed",
		},
		Sound: notifydelivery.ChannelResult{Reason: notify.ReasonChannelOff},
	})
	want := "Notification test delivered; coordination failed: complete delivery receipt: fixture failed"
	if got != want {
		t.Fatalf("mixed flash=%q want=%q", got, want)
	}
}

func TestConfigReloadRequestAppliesCLIChangeLive(t *testing.T) {
	dir := t.TempDir()
	config.SetTestConfigPath(filepath.Join(dir, "config.json"))
	config.SetTestStateDir(filepath.Join(dir, "state"))
	t.Cleanup(config.ResetTestConfigPath)
	t.Cleanup(config.ResetTestStateDir)
	t.Cleanup(func() { notify.ApplyConfig(config.DefaultNotificationsConfig()) })
	cfg := config.Default()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
	m, _ := scopeBaselineModel(t, "git")
	if err := config.SaveNotifications(func(n *config.NotificationsConfig) {
		n.Native.Mode = config.DeliveryAlways
		n.Sound.Mode = config.DeliveryBackground
	}); err != nil {
		t.Fatal(err)
	}
	cmd := m.handleConfigReloadRequest(uirequest.Request{ID: "reload-test", Action: uirequest.ActionConfigReload})
	if cmd == nil {
		t.Fatal("reload request returned no live-apply command")
	}
	if m.cfg.Notifications.Native.Mode != config.DeliveryAlways || m.cfg.Notifications.Sound.Mode != config.DeliveryBackground {
		t.Fatalf("app config did not reload: %+v", m.cfg.Notifications)
	}
	resolved := notify.CurrentConfig()
	if resolved.NativeMode != config.DeliveryAlways || resolved.SoundMode != config.DeliveryBackground {
		t.Fatalf("delivery policy did not apply live: native=%q sound=%q", resolved.NativeMode, resolved.SoundMode)
	}
}
