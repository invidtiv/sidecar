package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
)

type availableCLISound struct{ calls int }

func (s *availableCLISound) Probe(context.Context) notifydelivery.Capability {
	return notifydelivery.Capability{Available: true, Provider: "fake-sound"}
}
func (s *availableCLISound) Play(context.Context, notifydelivery.Cue) (notifydelivery.ProviderReceipt, error) {
	s.calls++
	return notifydelivery.ProviderReceipt{Provider: "fake-sound", Delivered: true}, nil
}

func notificationConfigEnv(t *testing.T) (Env, *bytes.Buffer, *bytes.Buffer, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"futureRoot":{"keep":true},"notifications":{"sources":{"future-source":{"expiry":"17s"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	config.SetTestConfigPath(path)
	config.SetTestStateDir(filepath.Join(dir, "state"))
	t.Cleanup(config.ResetTestConfigPath)
	t.Cleanup(config.ResetTestStateDir)
	t.Cleanup(func() { notify.ApplyConfig(config.DefaultNotificationsConfig()) })
	var out, errOut bytes.Buffer
	return Env{Stdout: &out, Stderr: &errOut, StateDir: config.StateDir()}, &out, &errOut, path
}

func TestNotifyConfigSetPreservesUnknownsAndMatchesResolvedConfig(t *testing.T) {
	env, out, errOut, path := notificationConfigEnv(t)
	if code := runNotifyConfigSet(env, []string{"--native", "background", "--sound=always", "--json"}); code != 0 {
		t.Fatalf("set=%d stderr=%q", code, errOut.String())
	}
	var written config.NotificationsConfig
	if err := json.Unmarshal(out.Bytes(), &written); err != nil {
		t.Fatalf("json=%v output=%q", err, out.String())
	}
	if written.Native.Mode != config.DeliveryBackground || written.Sound.Mode != config.DeliveryAlways {
		t.Fatalf("result=%+v", written)
	}
	if waiting, ok := written.Sources[string(notify.SourceWaiting)]; !ok || waiting.Native == nil || !*waiting.Native || waiting.Sound != config.SoundAttention {
		t.Fatalf("resolved source defaults missing from config output: %+v", written.Sources)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Notifications.Native.Mode != written.Native.Mode || notify.CurrentConfig().NativeMode != written.Native.Mode {
		t.Fatalf("file/live/result diverged: file=%q live=%q result=%q", loaded.Notifications.Native.Mode, notify.CurrentConfig().NativeMode, written.Native.Mode)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"futureRoot"`)) || !bytes.Contains(raw, []byte(`"future-source"`)) {
		t.Fatalf("targeted save lost unknown data: %s", raw)
	}
}

func TestNotifyConfigSetRefusesInvalidModeWithoutChangingFile(t *testing.T) {
	env, _, errOut, path := notificationConfigEnv(t)
	before, _ := os.ReadFile(path)
	if code := runNotifyConfigSet(env, []string{"--native", "sometimes"}); code != 2 {
		t.Fatalf("invalid mode exit=%d stderr=%q", code, errOut.String())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatalf("invalid mode changed config:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestNotifyConfigNormalizesMalformedPersistedModeInHumanAndJSONWithoutMutation(t *testing.T) {
	env, out, errOut, path := notificationConfigEnv(t)
	raw := []byte(`{"notifications":{"native":{"mode":"sometimes","provider":"auto"},"sound":{"mode":"off"}}}`)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runNotifyConfig(env, nil); code != 0 {
		t.Fatalf("human exit=%d stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "System notifications: off") {
		t.Fatalf("human output=%q", out.String())
	}
	out.Reset()
	if code := runNotifyConfig(env, []string{"--json"}); code != 0 {
		t.Fatalf("json exit=%d stderr=%q", code, errOut.String())
	}
	var view notificationConfigResult
	if err := json.Unmarshal(out.Bytes(), &view); err != nil || view.Native.Mode != config.DeliveryOff {
		t.Fatalf("json view=%+v err=%v output=%q", view, err, out.String())
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, raw) {
		t.Fatalf("read-only config normalized the file: %s err=%v", after, err)
	}
}

func TestNotifyStatusHumanAndJSONAreReadOnly(t *testing.T) {
	env, out, errOut, path := notificationConfigEnv(t)
	if err := os.WriteFile(path, []byte(`{"notifications":{"native":{"mode":"background","provider":"auto"},"sound":{"mode":"off"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	delivery := &fakeCLIDelivery{status: notifydelivery.Status{
		Native: notifydelivery.Capability{Available: true, Provider: "terminal-notifier"},
		Sound:  notifydelivery.Capability{Reason: "afplay unavailable"},
	}}
	env.NotificationDelivery = delivery
	before, _ := os.ReadFile(path)
	if code := runNotifyStatus(env, nil); code != 0 {
		t.Fatalf("status=%d stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Native: ready (terminal-notifier)") || !strings.Contains(out.String(), "Sound: unavailable") {
		t.Fatalf("human status=%q", out.String())
	}
	if delivery.observed.NativeMode != config.DeliveryBackground {
		t.Fatalf("status probed before persisted config was applied: %+v", delivery.observed)
	}
	out.Reset()
	if code := runNotifyStatus(env, []string{"--json"}); code != 0 {
		t.Fatalf("status json=%d", code)
	}
	var status notifydelivery.Status
	if err := json.Unmarshal(out.Bytes(), &status); err != nil || status.Native.Provider != "terminal-notifier" {
		t.Fatalf("status json=%+v err=%v", status, err)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("status changed configuration")
	}
}

func TestNotifyTestUsesSharedExplicitBoundaryAndCreatesNoRecord(t *testing.T) {
	env, out, errOut, _ := notificationConfigEnv(t)
	delivery := &fakeCLIDelivery{result: notifydelivery.Result{
		Native: notifydelivery.ChannelResult{Attempted: true, Provider: "terminal-notifier", Delivered: true},
		Sound:  notifydelivery.ChannelResult{Reason: notify.ReasonNotRequested},
	}}
	env.NotificationDelivery = delivery
	if code := runNotifyTest(env, []string{"--channel", "native", "--event", "failure", "--json"}); code != 0 {
		t.Fatalf("test=%d stderr=%q output=%q", code, errOut.String(), out.String())
	}
	if len(delivery.requests) != 1 || !delivery.requests[0].ExplicitTest || delivery.requests[0].Channel != notifydelivery.ChannelNative || delivery.requests[0].Notification.Severity != notify.SeverityError {
		t.Fatalf("request=%+v", delivery.requests)
	}
	if all, err := notify.ReadAll(notify.Path(env.StateDir)); err != nil || len(all) != 0 {
		t.Fatalf("explicit test created centre records: %+v err=%v", all, err)
	}
	var result struct {
		Native notifydelivery.ChannelResult `json:"native"`
		Sound  notifydelivery.ChannelResult `json:"sound"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || !result.Native.Attempted || result.Sound.Attempted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, field := range []string{`"attempted"`, `"provider"`, `"delivered"`, `"error"`} {
		if !bytes.Contains(out.Bytes(), []byte(field)) {
			t.Fatalf("structured test result missing %s: %s", field, out.String())
		}
	}
}

func TestNotifyTestDisabledAndUsageExitCodes(t *testing.T) {
	env, _, _, _ := notificationConfigEnv(t)
	env.NotificationDelivery = &fakeCLIDelivery{result: notifydelivery.Result{
		Native: notifydelivery.ChannelResult{Reason: notify.ReasonChannelOff},
	}}
	if code := runNotifyTest(env, []string{"--channel", "native"}); code != 3 {
		t.Fatalf("disabled channel exit=%d", code)
	}
	if code := runNotifyTest(env, []string{"--event", "waiting"}); code != 2 {
		t.Fatalf("missing channel exit=%d", code)
	}
	if code := runNotifyTest(env, []string{"--channel", "desktop"}); code != 2 {
		t.Fatalf("invalid channel exit=%d", code)
	}
}

func TestNotifyTestLoadsPersistedSourceRuleBeforeRealServiceDelivery(t *testing.T) {
	env, out, errOut, path := notificationConfigEnv(t)
	if err := os.WriteFile(path, []byte(`{"notifications":{"native":{"mode":"off","provider":"auto"},"sound":{"mode":"background"},"sources":{"waiting":{"sound":"none"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sound := &availableCLISound{}
	env.NotificationDelivery = notifydelivery.NewService(notifydelivery.ServiceOptions{
		Sound: sound, Ledger: func() (notifydelivery.Ledger, error) { return notifydelivery.NewMemoryLedger(), nil },
	})
	if code := runNotifyTest(env, []string{"--channel", "sound", "--event", "waiting", "--json"}); code != 3 {
		t.Fatalf("test exit=%d stderr=%q output=%q", code, errOut.String(), out.String())
	}
	var result struct {
		Sound notifydelivery.ChannelResult `json:"sound"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Sound.Reason != notify.ReasonSourceOff {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if sound.calls != 0 {
		t.Fatalf("source-off explicit test played sound %d times", sound.calls)
	}
}

func TestNotifyTestCommandLoadsPersistedSourceRuleInFreshDefaultEnvironment(t *testing.T) {
	env, _, _, path := notificationConfigEnv(t)
	if err := os.WriteFile(path, []byte(`{"notifications":{"native":{"mode":"off","provider":"auto"},"sound":{"mode":"background"},"sources":{"waiting":{"sound":"none"}}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"-config", path, "notify", "test", "--channel", "sound", "--event", "waiting", "--json"}, &out, &errOut)
	if !handled || code != 3 {
		t.Fatalf("handled=%v exit=%d stderr=%q output=%q state=%s", handled, code, errOut.String(), out.String(), env.StateDir)
	}
	var result struct {
		Sound notifydelivery.ChannelResult `json:"sound"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Sound.Reason != notify.ReasonSourceOff {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestNotifyTestCoordinationFailureIsRuntimeExit(t *testing.T) {
	env, out, errOut, path := notificationConfigEnv(t)
	if err := os.WriteFile(path, []byte(`{"notifications":{"native":{"mode":"off","provider":"auto"},"sound":{"mode":"always"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	env.NotificationDelivery = notifydelivery.NewService(notifydelivery.ServiceOptions{
		Sound:  &availableCLISound{},
		Ledger: func() (notifydelivery.Ledger, error) { return nil, errors.New("fixture failed") },
	})
	if code := runNotifyTest(env, []string{"--channel", "sound", "--json"}); code != 1 {
		t.Fatalf("runtime failure exit=%d stderr=%q output=%q", code, errOut.String(), out.String())
	}
	var result struct {
		Sound notifydelivery.ChannelResult `json:"sound"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Sound.Reason != notify.ReasonCoordination || result.Sound.Error == "" {
		t.Fatalf("runtime result=%+v err=%v", result, err)
	}
}

func TestNotifyTestHumanResultPreservesDeliveryWithCoordinationFailure(t *testing.T) {
	env, out, errOut, _ := notificationConfigEnv(t)
	env.NotificationDelivery = &fakeCLIDelivery{result: notifydelivery.Result{
		Native: notifydelivery.ChannelResult{Reason: notify.ReasonNotRequested},
		Sound: notifydelivery.ChannelResult{
			Attempted: true, Provider: "fake-sound", Delivered: true,
			Reason: notify.ReasonCoordination, Error: "complete delivery receipt: fixture failed",
		},
	}}
	if code := runNotifyTest(env, []string{"--channel", "sound"}); code != 1 {
		t.Fatalf("mixed result exit=%d stderr=%q output=%q", code, errOut.String(), out.String())
	}
	want := "Sound: delivered (fake-sound); coordination failed: complete delivery receipt: fixture failed"
	if !strings.Contains(out.String(), want) {
		t.Fatalf("mixed human output=%q want line containing %q", out.String(), want)
	}
}

func TestNotificationDeliveryCommandsAreDiscoverable(t *testing.T) {
	agents := RenderAgents(RootCommand())
	for _, want := range []string{"sidecar notify config --json", "sidecar notify config set", "sidecar notify status --json", "sidecar notify test --channel"} {
		if !strings.Contains(agents, want) {
			t.Fatalf("sidecar agents missing %q:\n%s", want, agents)
		}
	}
	configHelp := RenderHelp(RootCommand().FindSubcommand("notify").FindSubcommand("config"))
	if !strings.Contains(configHelp, "set") || !strings.Contains(configHelp, "--json") {
		t.Fatalf("notify config help is incomplete:\n%s", configHelp)
	}
	setHelp := RenderHelp(RootCommand().FindSubcommand("notify").FindSubcommand("config").FindSubcommand("set"))
	if !strings.Contains(setHelp, "--native MODE") || !strings.Contains(setHelp, "--sound MODE") {
		t.Fatalf("notify config set help is incomplete:\n%s", setHelp)
	}
}
