package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/uiapi"
)

func TestAPISpecCLIUsesGeneratedContractWithoutServer(t *testing.T) {
	apiStateTree(t, t.TempDir())
	want, err := uiapi.Spec()
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"api", "spec"}, {"api", "spec", "--json"}} {
		code, out, stderr := runAPICLI(t, args...)
		if code != 0 || out != string(want) {
			t.Fatalf("%v: %d %s", args, code, stderr)
		}
	}
	if code, _, _ := runAPICLI(t, "api", "spec", "--unknown"); code != 2 {
		t.Fatal(code)
	}
}

func TestAPIServeFixturesRefusesUnisolatedPaths(t *testing.T) {
	for _, mode := range []string{"no-assertion", "real-state", "real-config", "state-symlink", "origins-symlink", "sessions-symlink", "session-lock-symlink", "layouts-symlink"} {
		t.Run(mode, func(t *testing.T) {
			root, err := os.MkdirTemp("/tmp", "u1b-guard-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			state := apiStateTree(t, root)
			// A fake HOME makes the red proof safe even when the guard is absent.
			t.Setenv("HOME", root)
			switch mode {
			case "no-assertion":
				t.Setenv(config.IsolationEnv, "")
			case "real-state":
				state = config.RealUserStateDir()
			case "real-config":
				config.SetConfigPath(filepath.Join(config.RealUserConfigDir(), "config.json"))
			case "state-symlink", "origins-symlink", "sessions-symlink", "session-lock-symlink", "layouts-symlink":
				if err := os.MkdirAll(config.RealUserStateDir(), 0700); err != nil {
					t.Fatal(err)
				}
				link, target := filepath.Join(root, "alias"), config.RealUserStateDir()
				if mode != "state-symlink" {
					if err := os.MkdirAll(uiapi.Dir(state), 0700); err != nil {
						t.Fatal(err)
					}
					name := "origins.json"
					if mode == "sessions-symlink" {
						name = "sessions.json"
					}
					if mode == "session-lock-symlink" {
						name = "sessions.json.lock"
					}
					link, target = filepath.Join(uiapi.Dir(state), name), filepath.Join(target, name)
					if err := os.WriteFile(target, []byte(`{"origins":[]}`), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					state = link
				}
				if mode == "layouts-symlink" {
					link, target = filepath.Join(uiapi.Dir(state), "layouts"), config.RealUserStateDir()
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			var out, stderr bytes.Buffer
			code := runAPIServe(Env{Ctx: ctx, StateDir: state, Stdout: &out, Stderr: &stderr}, []string{"--port", "0", "--json", "--fixtures", filepath.Join("..", "..", "testdata", "ui-api", "v0")})
			if code != 1 || !strings.Contains(stderr.String(), "fixture isolation") || out.Len() != 0 {
				t.Fatalf("unsafe fixture start: code=%d stdout=%s stderr=%s", code, out.String(), stderr.String())
			}
			if _, err := os.Stat(uiapi.Dir(state)); mode == "real-state" && !os.IsNotExist(err) {
				t.Fatalf("created real API state: %v", err)
			}
		})
	}
}

func TestAPIServeFixturesCLIWithoutTmux(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "u1b-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	apiStateTree(t, root)
	// No tmux, git or external process can accidentally satisfy this proof.
	t.Setenv("PATH", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := make(chan []byte, 1)
	output := &fixtureEndpointWriter{ready: started}
	var stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runAPIServe(Env{Ctx: ctx, StateDir: root, Stdout: output, Stderr: &stderr}, []string{"--port", "0", "--json", "--fixtures", filepath.Join("..", "..", "testdata", "ui-api", "v0")})
	}()
	select {
	case data := <-started:
		var endpoint uiapi.Endpoint
		if err := json.Unmarshal(data, &endpoint); err != nil {
			t.Fatal(err)
		}
		status, err := uiapi.NewLocalClientForSocket(endpoint).Status(ctx)
		if err != nil || status.ServerVersion != "fixture" {
			t.Fatalf("status %+v %v", status, err)
		}
	case code := <-done:
		t.Fatalf("serve exited %d: %s", code, stderr.String())
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	cancel()
	if code := <-done; code != 0 {
		t.Fatalf("serve exited %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(uiapi.Dir(root), "endpoint.json")); !os.IsNotExist(err) {
		t.Fatalf("endpoint left: %v", err)
	}
}

type fixtureEndpointWriter struct{ ready chan []byte }

func (w *fixtureEndpointWriter) Write(data []byte) (int, error) {
	w.ready <- append([]byte(nil), data...)
	return len(data), nil
}
