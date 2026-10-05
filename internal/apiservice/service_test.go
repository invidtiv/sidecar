package apiservice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls   []string
	loaded  bool
	os      string
	failure string
}

func (f *fakeRunner) run(_ context.Context, command string, args ...string) ([]byte, error) {
	call := command + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if strings.Contains(call, f.failure) && f.failure != "" {
		return []byte("fake failure"), errors.New("exit 1")
	}
	if len(args) > 0 && args[0] == "print" {
		if !f.loaded {
			return []byte("Could not find service com.haplab.sidecar.api in domain for user gui: 501"), errors.New("exit 113")
		}
		return []byte(launchctlPrintRunning), nil
	}
	if strings.Contains(call, " show ") {
		if strings.Contains(call, SocketUnit) && f.loaded {
			return []byte("LoadState=loaded\nActiveState=active\nSubState=running\n"), nil
		}
		if !f.loaded {
			return []byte("LoadState=not-found\nActiveState=inactive\nMainPID=0\nExecMainCode=0\nExecMainStatus=0\n"), errors.New("exit 1")
		}
		return []byte("LoadState=loaded\nActiveState=active\nMainPID=42\nExecMainCode=1\nExecMainStatus=7\n"), nil
	}
	if strings.Contains(call, "bootstrap") || strings.Contains(call, "enable --now") {
		f.loaded = true
	}
	if strings.Contains(call, "bootout") || strings.Contains(call, "disable --now") {
		f.loaded = false
	}
	return nil, nil
}
func testManager(t *testing.T, platform string) (*Native, *fakeRunner) {
	t.Helper()
	root, err := os.MkdirTemp("/tmp", "sc-svc-test-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	f := &fakeRunner{os: platform}
	manager, err := New(Options{OS: platform, Home: root, ConfigHome: filepath.Join(root, "xdg"), StateDir: filepath.Join(root, "state", "sidecar"), ConfigPath: filepath.Join(root, "config & quotes", "config.json"), Executable: filepath.Join(root, "stable link", "sidecar"), UID: 501, Path: "/opt/homebrew/bin:/usr/bin:/bin", Run: f.run})
	if err != nil {
		t.Fatal(err)
	}
	return manager, f
}
func TestServiceManagerLifecycle(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			m, f := testManager(t, platform)
			ctx := context.Background()
			status, err := m.Status(ctx)
			if err != nil || status.Installed || status.Loaded || status.Running || status.LastExit != nil {
				t.Fatalf("before = %+v %v", status, err)
			}
			if err := m.Install(ctx); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(m.file)
			if err != nil {
				t.Fatal(err)
			}
			for _, part := range []string{"api", "serve", "sidecar"} {
				if !strings.Contains(string(data), part) {
					t.Fatalf("missing %s: %s", part, data)
				}
			}
			for _, unsafe := range []string{"TMUX", "SIDECAR_SHELL", "kill-server"} {
				if strings.Contains(string(data), unsafe) {
					t.Fatalf("unsafe definition: %s", data)
				}
			}
			info, err := os.Stat(m.file)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("file mode: %v %v", info, err)
			}
			status, err = m.Status(ctx)
			if err != nil || !status.Installed || !status.Loaded || !status.Running || status.PID != 42 || status.LastExit == nil || status.LastExit.Code != 7 || !status.Socket.Installed || !status.Socket.Listening {
				t.Fatalf("running = %+v %v", status, err)
			}
			f.calls = nil
			if err := m.Install(ctx); err != nil {
				t.Fatal(err)
			}
			unload := "launchctl bootout gui/501/" + Label
			load := "launchctl bootstrap gui/501 " + m.file
			if platform == "linux" {
				unload = "systemctl --user disable --now " + SocketUnit + " " + Unit
				load = "systemctl --user enable --now " + SocketUnit + " " + Unit
			}
			if strings.Index(strings.Join(f.calls, "\n"), unload) >= strings.Index(strings.Join(f.calls, "\n"), load) {
				t.Fatalf("reinstall must unload first: %v", f.calls)
			}
			if err := m.Uninstall(ctx); err != nil {
				t.Fatal(err)
			}
			if err := m.Uninstall(ctx); err != nil {
				t.Fatal(err)
			}
			status, err = m.Status(ctx)
			if err != nil || status.Installed || status.Loaded || status.Running {
				t.Fatalf("after = %+v %v", status, err)
			}
		})
	}
}
func TestManagerFailureIsActionableAndDoesNotDeleteDefinition(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			m, f := testManager(t, platform)
			ctx := context.Background()
			if err := m.Install(ctx); err != nil {
				t.Fatal(err)
			}
			if platform == "darwin" {
				f.failure = "bootout"
			} else {
				f.failure = "disable --now"
			}
			err := m.Uninstall(ctx)
			if err == nil || !strings.Contains(err.Error(), "inspect") || !strings.Contains(err.Error(), "fake failure") {
				t.Fatalf("error = %v", err)
			}
			if _, err := os.Stat(m.file); err != nil {
				t.Fatal("lost definition after unload failure", err)
			}
			f.failure = ""
			if platform == "darwin" {
				f.failure = "print"
			} else {
				f.failure = "show"
			}
			if _, err := m.Status(ctx); err == nil {
				t.Fatal("manager failure reported as not installed")
			}
		})
	}
}
func TestServiceDefinitionEscapingAndRestartPolicy(t *testing.T) {
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			m, _ := testManager(t, platform)
			m.options.Executable = "/path with spaces/%/$dollar/\"sidecar"
			m.options.Path = "/bin/$dollar/%/bin"
			data := string(m.definition())
			if platform == "darwin" {
				for _, part := range []string{"<key>KeepAlive</key><true/>", "&amp;", "&#34;"} {
					if !strings.Contains(data, part) {
						t.Fatalf("missing %q: %s", part, data)
					}
				}
			} else {
				for _, part := range []string{"Restart=always", "RestartSec=5", `"/path with spaces/%%/$$dollar/\"sidecar"`, `Environment="PATH=/bin/$dollar/%%/bin"`} {
					if !strings.Contains(data, part) {
						t.Fatalf("missing %q: %s", part, data)
					}
				}
			}
		})
	}
}
func TestStableExecutablePreservesLinksButNeverSelectsAnotherBuild(t *testing.T) {
	root := t.TempDir()
	own := filepath.Join(root, "own")
	other := filepath.Join(root, "other")
	link := filepath.Join(root, "sidecar")
	for _, path := range []string{own, other} {
		if err := os.WriteFile(path, []byte("binary"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(own, link); err != nil {
		t.Fatal(err)
	}
	if got := StableExecutable(own, "sidecar", root); got != link {
		t.Fatalf("lost link: %s", got)
	}
	if got := StableExecutable(other, "sidecar", root); got != other {
		t.Fatalf("selected another build: %s", got)
	}
}

// launchctlPrintRunning is the shape `launchctl print` produces on macOS 27:
// tab-indented, quoted socket names, and nested dictionaries that repeat
// `state` and `path`.
const launchctlPrintRunning = "gui/501/com.haplab.sidecar.api = {\n" +
	"\tactive count = 1\n\tstate = running\n\n\tprogram = /bin/sidecar\n\truns = 16\n\tpid = 42\n\tlast exit code = 7\n\n" +
	"\tsockets = {\n\t\t\"local\" = {\n\t\t\ttype = stream\n\t\t\tpath = /s/api.sock\n\t\t\tsockets = {\n\t\t\t\tstate = active\n\t\t\t}\n\t\t}\n" +
	"\t\t\"browser\" = {\n\t\t\ttype = stream\n\t\t\tsockets = {\n\t\t\t\tstate = active\n\t\t\t}\n\t\t}\n\t}\n}\n"

func TestParseManagerStatus(t *testing.T) {
	running := Status{}
	parseLaunchd(launchctlPrintRunning, &running)
	if !running.Running || running.PID != 42 || !reflect.DeepEqual(running.LastExit, &Exit{Code: 7}) {
		t.Fatalf("nested socket state overrode the job: %+v", running)
	}
	status := Status{}
	parseLaunchd("gui/501/x = {\n\tstate = waiting\n\tlast terminating signal = SIGTERM\n}\n", &status)
	if status.Running || !reflect.DeepEqual(status.LastExit, &Exit{Signal: "SIGTERM"}) {
		t.Fatalf("%+v", status)
	}
}

// Reinstalling a loaded job: launchd finishes a bootout after the command
// returns, and bootstrapping the label before then fails with EIO (5).
func TestReinstallWaitsForLaunchdToReleaseTheOldJob(t *testing.T) {
	manager, _ := testManager(t, "darwin")
	bootedOut, prints, bootstraps := false, 0, 0
	manager.options.Run = func(_ context.Context, command string, args ...string) ([]byte, error) {
		switch args[0] {
		case "print":
			if !bootedOut {
				return []byte(launchctlPrintRunning), nil
			}
			prints++
			if prints < 3 { // still tearing down
				return []byte("\tstate = running\n\tpid = 42\n"), nil
			}
			return []byte("Could not find service com.haplab.sidecar.api in domain for user gui: 501"), errors.New("exit 113")
		case "bootout":
			bootedOut = true
		case "bootstrap":
			bootstraps++
			if bootstraps == 1 {
				return []byte("Bootstrap failed: 5: Input/output error"), errors.New("exit status 5")
			}
		}
		return nil, nil
	}
	if err := manager.Install(t.Context()); err != nil {
		t.Fatalf("Install over a loaded job = %v; want it to wait out the bootout", err)
	}
	if prints < 3 || bootstraps != 2 {
		t.Fatalf("prints=%d bootstraps=%d; want a wait for the old job and one EIO retry", prints, bootstraps)
	}
}
