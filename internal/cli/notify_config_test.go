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

func TestNotifyConfigSetQuietHoursAndCustomPathsPreservePriorConfig(t *testing.T) {
	env, out, errOut, path := notificationConfigEnv(t)
	soundPath := filepath.Join(filepath.Dir(path), "attention.wav")
	if err := os.WriteFile(soundPath, []byte("fixture"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := runNotifyConfigSet(env, []string{"--quiet-hours", "10:00-10:00", "--attention-path", "attention.wav", "--json"}); code != 0 {
		t.Fatalf("set=%d stderr=%q output=%q", code, errOut.String(), out.String())
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Notifications.QuietHours.Enabled || loaded.Notifications.QuietHours.Start != "10:00" || loaded.Notifications.QuietHours.End != "10:00" {
		t.Fatalf("quiet hours=%+v", loaded.Notifications.QuietHours)
	}
	if loaded.Notifications.Sound.AttentionPath != "attention.wav" {
		t.Fatalf("attention path=%q", loaded.Notifications.Sound.AttentionPath)
	}
	out.Reset()
	errOut.Reset()
	if code := runNotifyConfig(env, nil); code != 0 || !strings.Contains(out.String(), "Quiet hours: 10:00-10:00 (all day)") {
		t.Fatalf("all-day quiet hours were ambiguous: exit=%d output=%q stderr=%q", code, out.String(), errOut.String())
	}
	for _, want := range []string{"Sound choices:", "Attention: attention.wav", "Done: built-in", "Failure: built-in", "Source rules:", "waiting: toast on, native on, sound attention, expiry sticky", "future-source:"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("human config omitted %q:\n%s", want, out.String())
		}
	}
	before, _ := os.ReadFile(path)
	out.Reset()
	errOut.Reset()
	if code := runNotifyConfigSet(env, []string{"--done-path", "missing.wav"}); code != 2 {
		t.Fatalf("invalid path exit=%d stderr=%q", code, errOut.String())
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatalf("invalid path changed prior config:\nbefore=%s\nafter=%s", before, after)
	}
	if !bytes.Contains(after, []byte(`"futureRoot"`)) || !bytes.Contains(after, []byte(`"future-source"`)) {
		t.Fatalf("targeted save lost unknown config: %s", after)
	}
	if code := runNotifyConfigSet(env, []string{"--quiet-hours", "off", "--attention-path="}); code != 0 {
		t.Fatalf("reset=%d stderr=%q", code, errOut.String())
	}
	loaded, _ = config.Load()
	if loaded.Notifications.QuietHours.Enabled || loaded.Notifications.Sound.AttentionPath != "" {
		t.Fatalf("reset result=%+v", loaded.Notifications)
	}
}

func TestNotifyConfigSetAppliesSSHDeliveryAndReportsItEverywhere(t *testing.T) {
	env, out, errOut, path := notificationConfigEnv(t)
	if code := runNotifyConfigSet(env, []string{"--ssh-managed-hosts", "on", "--ssh-terminal=kitty", "--json"}); code != 0 {
		t.Fatalf("set=%d stderr=%q", code, errOut.String())
	}
	var written notificationConfigResult
	if err := json.Unmarshal(out.Bytes(), &written); err != nil {
		t.Fatalf("json=%v output=%q", err, out.String())
	}
	if !written.SSH.ManagedHosts || written.SSH.Terminal != config.TerminalNotifierKitty {
		t.Fatalf("ssh result=%+v", written.SSH)
	}
	loaded, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Notifications.SSH != written.SSH {
		t.Fatalf("file and result diverged: file=%+v result=%+v", loaded.Notifications.SSH, written.SSH)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"futureRoot"`)) || !bytes.Contains(raw, []byte(`"future-source"`)) {
		t.Fatalf("targeted SSH save lost unrelated data: %s", raw)
	}

	out.Reset()
	if code := runNotifyConfig(env, nil); code != 0 {
		t.Fatalf("config=%d stderr=%q", code, errOut.String())
	}
	for _, want := range []string{"SSH delivery:", "Managed hosts: on", "Terminal: kitty"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("human config omitted %q:\n%s", want, out.String())
		}
	}
}

func TestNotifyConfigSetRefusesInvalidSSHValuesWithoutChangingFile(t *testing.T) {
	env, _, errOut, path := notificationConfigEnv(t)
	before, _ := os.ReadFile(path)
	for _, args := range [][]string{
		{"--ssh-terminal", "bell"},
		{"--ssh-managed-hosts", "yes"},
	} {
		errOut.Reset()
		if code := runNotifyConfigSet(env, args); code != 2 {
			t.Fatalf("%v exit=%d stderr=%q", args, code, errOut.String())
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatalf("%v changed config:\nbefore=%s\nafter=%s", args, before, after)
		}
	}
}

func TestNotifyConfigSSHDefaultsAreOffAndReportedExplicitly(t *testing.T) {
	env, out, errOut, _ := notificationConfigEnv(t)
	if code := runNotifyConfig(env, []string{"--json"}); code != 0 {
		t.Fatalf("config=%d stderr=%q", code, errOut.String())
	}
	var view notificationConfigResult
	if err := json.Unmarshal(out.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.SSH.ManagedHosts {
		t.Fatal("managed-host delivery must be off until the user asks for it")
	}
	// An omitted terminal is reported as the explicit "off" it behaves as,
	// rather than an empty string a reader has to interpret.
	if view.SSH.Terminal != config.TerminalNotifierOff {
		t.Fatalf("terminal=%q, want an explicit off", view.SSH.Terminal)
	}
}

func TestNotifySourceSetUsesSharedValidationAndAppliesLive(t *testing.T) {
	env, out, errOut, path := notificationConfigEnv(t)
	if code := runNotifySourceSet(env, []string{"waiting", "--toast", "off", "--native=on", "--sound", "failure", "--expiry", "sticky", "--json"}); code != 0 {
		t.Fatalf("source set=%d stderr=%q output=%q", code, errOut.String(), out.String())
	}
	var result struct {
		Source string                 `json:"source"`
		Rule   notificationSourceView `json:"rule"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json=%v output=%q", err, out.String())
	}
	if result.Source != "waiting" || result.Rule.Toast || !result.Rule.Native || result.Rule.Sound != config.SoundFailure || result.Rule.Expiry != "sticky" {
		t.Fatalf("result=%+v", result)
	}
	rule := notify.CurrentConfig().SourceRule(notify.SourceWaiting)
	if rule.Toast || !rule.Native || rule.Sound != config.SoundFailure || rule.Expiry != 0 {
		t.Fatalf("live rule=%+v", rule)
	}
	raw, _ := os.ReadFile(path)
	if !bytes.Contains(raw, []byte(`"futureRoot"`)) || !bytes.Contains(raw, []byte(`"future-source"`)) {
		t.Fatalf("source save lost unknown config: %s", raw)
	}
}

func TestNotifySourceSetRefusesInvalidValuesWithoutWriting(t *testing.T) {
	env, _, errOut, path := notificationConfigEnv(t)
	for _, args := range [][]string{
		{"unknown", "--native", "on"},
		{"td", "--toast", "yes"},
		{"tasks", "--sound", "loud"},
		{"system", "--expiry", "tomorrow"},
	} {
		before, _ := os.ReadFile(path)
		errOut.Reset()
		if code := runNotifySourceSet(env, args); code != 2 {
			t.Fatalf("args=%v exit=%d stderr=%q", args, code, errOut.String())
		}
		after, _ := os.ReadFile(path)
		if !bytes.Equal(before, after) {
			t.Fatalf("args=%v changed config", args)
		}
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

func TestNotifyStatusHumanPreservesWarningOnAvailableFallback(t *testing.T) {
	env, out, errOut, _ := notificationConfigEnv(t)
	env.NotificationDelivery = &fakeCLIDelivery{status: notifydelivery.Status{
		Sound: notifydelivery.Capability{Available: true, Provider: "afplay", Reason: "custom file unsupported; built-in fallback ready"},
	}}
	if code := runNotifyStatus(env, nil); code != 0 {
		t.Fatalf("status=%d stderr=%q", code, errOut.String())
	}
	if !strings.Contains(out.String(), "Sound: ready (afplay); warning: custom file unsupported; built-in fallback ready") {
		t.Fatalf("available warning was hidden: %q", out.String())
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

func TestNotifyTestCanExerciseSelectedSourceRule(t *testing.T) {
	env, out, errOut, _ := notificationConfigEnv(t)
	delivery := &fakeCLIDelivery{result: notifydelivery.Result{Native: notifydelivery.ChannelResult{Reason: notify.ReasonSourceOff}}}
	env.NotificationDelivery = delivery
	if code := runNotifyTest(env, []string{"--channel", "native", "--source", "td", "--json"}); code != 3 {
		t.Fatalf("test=%d stderr=%q output=%q", code, errOut.String(), out.String())
	}
	if len(delivery.requests) != 1 || delivery.requests[0].Notification.Source != notify.SourceTD {
		t.Fatalf("requests=%+v", delivery.requests)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"source":"td"`)) {
		t.Fatalf("json omitted selected source: %s", out.String())
	}
	if code := runNotifyTest(env, []string{"--channel", "native", "--source", "unknown"}); code != 2 {
		t.Fatalf("invalid source exit=%d", code)
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
	for _, want := range []string{"sidecar notify config --json", "sidecar notify config set", "sidecar notify source set", "sidecar notify status --json", "sidecar notify test --channel"} {
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
	for _, want := range []string{"--quiet-hours RANGE", "--attention-path PATH", "--done-path PATH", "--failure-path PATH"} {
		if !strings.Contains(setHelp, want) {
			t.Fatalf("notify config set help missing %q:\n%s", want, setHelp)
		}
	}
	sourceHelp := RenderHelp(RootCommand().FindSubcommand("notify").FindSubcommand("source").FindSubcommand("set"))
	for _, want := range []string{"--toast on|off", "--native on|off", "--sound CUE", "--expiry DURATION"} {
		if !strings.Contains(sourceHelp, want) {
			t.Fatalf("notify source set help missing %q:\n%s", want, sourceHelp)
		}
	}
}
