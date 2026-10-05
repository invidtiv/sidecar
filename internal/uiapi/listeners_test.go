package uiapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/apiservice"
)

// This helper consumes actual fd 3..5 supplied via ExtraFiles, never a manager.
func TestActivatedListenerChild(t *testing.T) {
	if os.Getenv("SC_ACTIVATION_CHILD") != "1" {
		return
	}
	_ = os.Setenv("LISTEN_PID", strconv.Itoa(os.Getpid()))
	state := os.Getenv("SC_ACTIVATION_STATE")
	server, err := Start(Options{StateDir: state, Port: 0, Backend: newFakeBackend(), Tailnet: &TailnetOptions{Host: testTailnetHost, Logins: []string{testTailnetLogin}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		if os.Getenv(variable) != "" {
			t.Fatalf("activation leaked %s", variable)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(server.Endpoint()); err != nil {
		t.Fatal(err)
	}
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestActivatedListenersServeAndSurviveShutdown(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "sc-fds-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	state := filepath.Join(root, "state")
	if err := os.MkdirAll(Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	browser, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = browser.Close() }()
	listeners := []interface{ File() (*os.File, error) }{browser}
	paths := []string{filepath.Join(Dir(state), localSocketName), filepath.Join(Dir(state), tailnetSockName)}
	for _, path := range paths {
		listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		if err := os.Chmod(path, 0o600); err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
	}
	files := []*os.File{}
	for _, listener := range listeners {
		file, err := listener.File()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		files = append(files, file)
	}
	// Repeat with named and unnamed fd protocols, retaining the same manager sockets.
	for _, names := range []string{"browser:local:tailnet", "", "sidecar-api:sidecar-api:sidecar-api"} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestActivatedListenerChild$", "-test.timeout=10s")
		cmd.Env = append(os.Environ(), "SC_ACTIVATION_CHILD=1", "SC_ACTIVATION_STATE="+state, "LISTEN_PID=1", "LISTEN_FDS=3", "LISTEN_FDNAMES="+names, apiservice.ActivationRequired+"=1")
		cmd.ExtraFiles = files
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
		var endpoint Endpoint
		if err := json.NewDecoder(out).Decode(&endpoint); err != nil {
			rest, _ := io.ReadAll(out)
			_ = cmd.Wait()
			t.Fatalf("child: %v %s %s", err, rest, stderr.String())
		}
		if endpoint.TCP != browser.Addr().String() {
			t.Fatalf("rebound Browser: %+v", endpoint)
		}
		client := &http.Client{Timeout: 2 * time.Second}
		response, err := client.Get("http://" + endpoint.TCP + "/api/v0/hello")
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("Browser trust lost: %d", response.StatusCode)
		}
		local, err := NewLocalClient(state)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := local.Status(context.Background()); err != nil {
			t.Fatal(err)
		}
		transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", paths[1])
		}}
		tailnet := &http.Client{Transport: transport, Timeout: 2 * time.Second}
		request, _ := http.NewRequest("GET", "http://"+testTailnetHost+"/api/v0/hello", nil)
		request.Header.Set(tailscaleLoginHead, testTailnetLogin)
		response, err = tailnet.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		transport.CloseIdleConnections()
		if response.StatusCode != 200 {
			t.Fatalf("Tailnet trust: %d", response.StatusCode)
		}
		_, _ = input.Write([]byte("x"))
		_ = input.Close()
		_, _ = io.Copy(io.Discard, out)
		if err := cmd.Wait(); err != nil {
			t.Fatalf("child exit %v %s", err, stderr.String())
		}
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("manager socket unlinked: %v", err)
			}
		}
		if other, err := net.Listen("tcp", endpoint.TCP); err == nil {
			_ = other.Close()
			t.Fatal("manager port was freed")
		}
		if _, err := os.Stat(filepath.Join(Dir(state), endpointFileName)); !os.IsNotExist(err) {
			t.Fatalf("discovery survived: %v", err)
		}
	}
}

func TestActivatedListenerValidation(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "sc-fd-check-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	opts := Options{StateDir: root, Port: 0}
	browser, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = browser.Close() }()
	for _, name := range []string{"local", "tailnet", "unknown"} {
		if _, err := validateActivated(opts, []apiservice.ActivatedListener{{Name: name, Listener: browser}}); err == nil {
			t.Fatalf("accepted mismatched name %s", name)
		}
	}
	if _, err := validateActivated(opts, []apiservice.ActivatedListener{{Listener: browser}, {Listener: browser}}); err == nil {
		t.Fatal("accepted duplicate")
	}
	nonLoopback, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = nonLoopback.Close() }()
	if _, err := validateActivated(opts, []apiservice.ActivatedListener{{Listener: nonLoopback}}); err == nil {
		t.Fatal("accepted non-loopback")
	}
	opts.Port = 1
	if _, err := validateActivated(opts, []apiservice.ActivatedListener{{Listener: browser}}); err == nil {
		t.Fatal("accepted wrong port")
	}
	if err := os.MkdirAll(Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(Dir(root), localSocketName)
	unixListener, err := net.Listen("unix", localPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unixListener.Close() }()
	if err := os.Chmod(localPath, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := validateActivated(opts, []apiservice.ActivatedListener{{Listener: unixListener}}); err == nil {
		t.Fatal("accepted public Local socket")
	}
	if err := os.Chmod(localPath, 0o600); err != nil {
		t.Fatal(err)
	}
	opts.Tailnet = nil
	if _, err := validateActivated(Options{StateDir: root + "-other"}, []apiservice.ActivatedListener{{Listener: unixListener}}); err == nil {
		t.Fatal("accepted wrong Local path")
	}
}

func TestServiceActivationFailsClosed(t *testing.T) {
	t.Setenv(apiservice.ActivationRequired, "1")
	root, err := os.MkdirTemp("/tmp", "sc-fail-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	server, err := Start(Options{StateDir: root, Backend: newFakeBackend(), Inherited: []apiservice.ActivatedListener{}})
	if err == nil {
		_ = server.Shutdown(context.Background())
		t.Fatal("service fell back to self binding")
	}
	if !strings.Contains(err.Error(), "manager-held Browser") {
		t.Fatal(err)
	}
}

// launchd on recent macOS binds an activated Unix socket in its own private
// directory and moves it to SockPathName, so the descriptor reports the
// bind-time path. The launchd name is accepted only when the configured path
// demonstrably reaches the inherited listener.
func TestValidateActivatedAcceptsMovedLaunchdSocketOnlyWhenItServesThePath(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "sc-mv-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := os.MkdirAll(Dir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	localPath := filepath.Join(Dir(root), localSocketName)
	bindPath := filepath.Join(root, "local")
	moved, err := net.Listen("unix", bindPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = moved.Close() }()
	moved.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := os.Rename(bindPath, localPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(localPath, 0o600); err != nil {
		t.Fatal(err)
	}
	opts := Options{StateDir: root}
	if _, err := validateActivated(opts, []apiservice.ActivatedListener{{Listener: moved}}); err == nil {
		t.Fatal("accepted an unnamed socket whose address is not the Local path")
	}
	got, err := validateActivated(opts, []apiservice.ActivatedListener{{Name: "local", Listener: moved}})
	if err != nil || got[ListenerLocal] != moved {
		t.Fatalf("validateActivated(moved launchd socket) = %v, %v; want it as Local", got, err)
	}

	// A named descriptor whose path is served by some other socket is refused.
	if err := os.Remove(localPath); err != nil {
		t.Fatal(err)
	}
	other, err := net.Listen("unix", localPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if err := os.Chmod(localPath, 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := other.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	if _, err := validateActivated(opts, []apiservice.ActivatedListener{{Name: "local", Listener: moved}}); err == nil {
		t.Fatal("accepted a named socket that does not serve the Local path")
	}
}

// launchd queues clients that connect before the service starts. An idle one
// accepted ahead of the probe must not exhaust the probe's budget.
func TestActivationProbeSurvivesAnIdleClientQueuedFirst(t *testing.T) {
	root, err := os.MkdirTemp("/tmp", "sc-idle-*")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(root) }()
	bind, path := filepath.Join(root, "bind"), filepath.Join(root, "api.sock")
	listener, err := net.Listen("unix", bind)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := os.Rename(bind, path); err != nil {
		t.Fatal(err)
	}
	client, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if !unixPathReaches(listener, path, activationProbeTimeout) {
		t.Fatal("a queued idle client made the correct inherited listener fail validation")
	}
}
