package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/uirequest"
)

const notificationProcessHelperEnv = "SIDECAR_NOTIFICATION_PROCESS_HELPER"

type processAttemptNative struct{ path string }

func (p processAttemptNative) Probe(context.Context) notifydelivery.Capability {
	return notifydelivery.Capability{Available: true, Provider: "process-native"}
}
func (p processAttemptNative) Deliver(_ context.Context, _ notifydelivery.Message) (notifydelivery.ProviderReceipt, error) {
	if err := appendProcessAttempt(p.path, "native"); err != nil {
		return notifydelivery.ProviderReceipt{Provider: "process-native"}, err
	}
	return notifydelivery.ProviderReceipt{Provider: "process-native", Delivered: true, At: time.Now().UTC()}, nil
}
func (processAttemptNative) Remove(context.Context, string) error { return nil }

type processAttemptSound struct{ path string }

func (p processAttemptSound) Probe(context.Context) notifydelivery.Capability {
	return notifydelivery.Capability{Available: true, Provider: "process-sound"}
}
func (p processAttemptSound) Play(_ context.Context, _ notifydelivery.Cue) (notifydelivery.ProviderReceipt, error) {
	if err := appendProcessAttempt(p.path, "sound"); err != nil {
		return notifydelivery.ProviderReceipt{Provider: "process-sound"}, err
	}
	return notifydelivery.ProviderReceipt{Provider: "process-sound", Delivered: true, At: time.Now().UTC()}, nil
}

type processStateAttention struct{ stateDir string }

func (a processStateAttention) Foreground(origin notify.Origin) (bool, error) {
	records, err := uirequest.ListAttention(a.stateDir)
	if err != nil {
		return false, err
	}
	return uirequest.OriginForeground(uirequest.Origin{
		TmuxSession: origin.TmuxSession,
		ProjectKey:  origin.ProjectKey,
		WorkDir:     origin.WorkDir,
	}, records), nil
}

func appendProcessAttempt(path, channel string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := fmt.Fprintf(f, "%d %s\n", os.Getpid(), channel)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func TestNotificationDeliveryProcessHelper(t *testing.T) {
	if os.Getenv(notificationProcessHelperEnv) != "1" {
		return
	}
	role := os.Getenv("SIDECAR_NOTIFICATION_PROCESS_ROLE")
	stateDir := os.Getenv("SIDECAR_NOTIFICATION_PROCESS_STATE")
	configPath := os.Getenv("SIDECAR_NOTIFICATION_PROCESS_CONFIG")
	barrierDir := os.Getenv("SIDECAR_NOTIFICATION_PROCESS_BARRIER")
	focused := os.Getenv("SIDECAR_NOTIFICATION_PROCESS_FOCUSED") == "1"
	if role == "" || stateDir == "" || configPath == "" || barrierDir == "" {
		t.Fatal("notification helper environment is incomplete")
	}

	config.SetConfigPath(configPath)
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	notify.ApplyConfig(cfg.Notifications)
	store, err := notify.Open(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("close notification store: %v", err)
		}
	})
	service := notifydelivery.NewService(notifydelivery.ServiceOptions{
		Native:    processAttemptNative{path: filepath.Join(barrierDir, "native-attempts")},
		Sound:     processAttemptSound{path: filepath.Join(barrierDir, "sound-attempts")},
		Ledger:    func() (notifydelivery.Ledger, error) { return notifydelivery.Open(stateDir) },
		Attention: processStateAttention{stateDir: stateDir},
		Owner:     fmt.Sprintf("%s-%d", role, os.Getpid()),
	})
	m := notifyModel()
	m.registry = plugin.NewRegistry(nil)
	m.notifications = store
	m.notificationDelivery = service
	m.refreshNotifications()

	origin := notify.Origin{TmuxSession: "isolated-notification-origin"}
	if err := uirequest.PublishAttention(stateDir, uirequest.Attention{
		PID: os.Getpid(), Focused: focused,
		VisibleOrigin: uirequest.Origin{TmuxSession: origin.TmuxSession},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := uirequest.WithdrawAttention(stateDir, os.Getpid()); err != nil {
			t.Errorf("withdraw attention: %v", err)
		}
	})
	if err := os.WriteFile(filepath.Join(barrierDir, "ready-"+role), []byte("ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitForProcessFile(t, filepath.Join(barrierDir, "release"), 10*time.Second)

	n := notify.Notification{
		ID: "ntf-process-steel-thread", Source: notify.SourceWaiting,
		Severity: notify.SeverityWarning, Title: "Agent needs input",
		CreatedAt: time.Now().UTC(), Origin: origin,
	}
	switch role {
	case "post":
		// Exercise the app-owned post and PostedMsg delivery wiring rather than
		// invoking Service directly from the proof.
		postCmd := m.postNotification(n)
		if postCmd == nil {
			t.Fatal("app post did not produce PostedMsg")
		}
		posted, ok := postCmd().(notify.PostedMsg)
		if !ok || !posted.Created {
			t.Fatalf("app post result = %#v", posted)
		}
		_, deliveryCmd := m.update(posted)
		runTeaCmd(deliveryCmd)
	case "reconcile":
		deadline := time.Now().Add(10 * time.Second)
		for {
			runTeaCmd(m.reconcileNotifications(time.Now().UTC()))
			for _, notification := range m.notificationCache {
				if notification.ID == n.ID {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatal("reconciliation did not discover the shared notification")
			}
			time.Sleep(5 * time.Millisecond)
		}
	default:
		t.Fatalf("unknown notification helper role %q", role)
	}
}

func waitForProcessFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestNotificationDeliveryAcrossProcessesRetainsCentreAndHonorsFocus(t *testing.T) {
	for _, scenario := range []struct {
		name       string
		focused    bool
		wantNative int
		wantSound  int
	}{
		{name: "background", wantNative: 1, wantSound: 1},
		{name: "focused-origin", focused: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			dir := t.TempDir()
			stateDir := filepath.Join(dir, "state", "sidecar")
			barrierDir := filepath.Join(dir, "barrier")
			configPath := filepath.Join(dir, "config", "config.json")
			if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(barrierDir, 0o755); err != nil {
				t.Fatal(err)
			}
			cfg := struct {
				Notifications config.NotificationsConfig `json:"notifications"`
			}{Notifications: config.DefaultNotificationsConfig()}
			cfg.Notifications.Native.Mode = config.DeliveryBackground
			cfg.Notifications.Sound.Mode = config.DeliveryBackground
			data, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, data, 0o644); err != nil {
				t.Fatal(err)
			}

			type processChild struct {
				role   string
				cmd    *exec.Cmd
				output strings.Builder
				waited bool
			}
			children := []*processChild{{role: "post"}, {role: "reconcile"}}
			// Register cleanup before the first Start. If a later child cannot
			// start, every earlier process is still killed and reaped by t.Fatal's
			// cleanup path.
			t.Cleanup(func() {
				for _, child := range children {
					if child.cmd != nil && child.cmd.Process != nil && !child.waited {
						_ = child.cmd.Process.Kill()
						_ = child.cmd.Wait()
					}
				}
			})
			for _, child := range children {
				child.cmd = exec.Command(os.Args[0], "-test.run=^TestNotificationDeliveryProcessHelper$")
				child.cmd.Stdout, child.cmd.Stderr = &child.output, &child.output
				child.cmd.Env = append(os.Environ(),
					notificationProcessHelperEnv+"=1",
					"SIDECAR_ISOLATED_STATE=1",
					"SIDECAR_NOTIFICATION_PROCESS_ROLE="+child.role,
					"SIDECAR_NOTIFICATION_PROCESS_STATE="+stateDir,
					"SIDECAR_NOTIFICATION_PROCESS_CONFIG="+configPath,
					"SIDECAR_NOTIFICATION_PROCESS_BARRIER="+barrierDir,
					fmt.Sprintf("SIDECAR_NOTIFICATION_PROCESS_FOCUSED=%d", map[bool]int{false: 0, true: 1}[scenario.focused]),
				)
				if err := child.cmd.Start(); err != nil {
					t.Fatal(err)
				}
			}
			for _, child := range children {
				waitForProcessFile(t, filepath.Join(barrierDir, "ready-"+child.role), 10*time.Second)
			}
			if err := os.WriteFile(filepath.Join(barrierDir, "release"), []byte("go\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, child := range children {
				done := make(chan error, 1)
				go func(child *processChild) { done <- child.cmd.Wait() }(child)
				select {
				case err := <-done:
					child.waited = true
					if err != nil {
						t.Fatalf("%s child failed: %v\n%s", child.role, err, child.output.String())
					}
				case <-time.After(15 * time.Second):
					_ = child.cmd.Process.Kill()
					<-done
					child.waited = true
					t.Fatalf("%s child timed out\n%s", child.role, child.output.String())
				}
			}

			if got := processAttemptCount(t, filepath.Join(barrierDir, "native-attempts")); got != scenario.wantNative {
				t.Fatalf("native attempts=%d want=%d", got, scenario.wantNative)
			}
			if got := processAttemptCount(t, filepath.Join(barrierDir, "sound-attempts")); got != scenario.wantSound {
				t.Fatalf("sound attempts=%d want=%d", got, scenario.wantSound)
			}
			records, err := notify.ReadAll(notify.Path(stateDir))
			if err != nil || len(records) != 1 || records[0].ID != "ntf-process-steel-thread" {
				t.Fatalf("centre records=%+v err=%v", records, err)
			}
		})
	}
}

func processAttemptCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(strings.TrimSpace(string(data)))) / 2
}
