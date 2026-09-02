package cli

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
)

// The serve stream must survive a login banner.
//
// It did not (td-055768): scripts/loopback-ssh.sh writes one line to stdout
// before every invocation on purpose, and the decoder rejected the first
// non-JSON line, so a viewer showed `not-protocol` forever — no hello, no
// snapshot, no Sessions rows, no `@` destinations, and an error naming the
// exact cause it refused to handle. The bug survived because the loopback
// checks only ever exercised the run-verb path, which has its own recovery,
// and the shell CI ran `up --no-drive` and never opened the stream.
//
// This runs the real `host serve --stdio` over the shared fake ssh, through
// hosts.Transport's own argv rendering, into hostproto's decoder — the whole
// path a viewer uses, minus sshd.
func TestServeStreamSurvivesTheLoopbackLoginBanner(t *testing.T) {
	h := newLoopbackHost(t)

	transport, err := hosts.NewTransport(hosts.Host{
		ID:           "loopback",
		Target:       "loopback",
		RemoteBinary: h.bin,
		RemoteConfig: h.hostConfig,
		Env:          h.hostEnv,
	}, filepath.Join(t.TempDir(), "ctl"))
	if err != nil {
		t.Fatalf("transport: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// --cycles 1 so the host exits on its own rather than streaming until the
	// pipe closes; everything before that point is the ordinary serve output.
	cmd := transport.Command(ctx, transport.SidecarCommand("host", "serve", "--stdio", "--cycles", "1"))
	cmd.Env = append(os.Environ(), "SIDECAR_LOOPBACK_SSH_EXIT=", "SIDECAR_LOOPBACK_SSH_DELAY=")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start serve: %v", err)
	}
	defer func() { _ = cmd.Wait() }()

	decoder := hostproto.NewDecoder(stdout)
	msg, err := decoder.Next()
	if err != nil {
		t.Fatalf("first message through the banner: %v (stderr %q)", err, stderr.String())
	}
	if msg.Kind != hostproto.KindHello {
		t.Fatalf("first message = %q, want hello", msg.Kind)
	}
	if msg.Hello == nil || !hostproto.Compatible(msg.Proto) {
		t.Fatalf("hello = %+v proto %d", msg.Hello, msg.Proto)
	}

	// The stream keeps working after the prelude, and the banner is not
	// re-applied to anything that follows.
	sawSnapshot := false
	for {
		next, err := decoder.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("after the hello: %v (stderr %q)", err, stderr.String())
		}
		if next.Kind == hostproto.KindSnapshot {
			sawSnapshot = true
		}
	}
	if !sawSnapshot {
		t.Fatal("no snapshot arrived, so the stream never really started")
	}

	// The banner the decoder skipped is really there. Without this the test
	// would keep passing if the fixture ever stopped printing one.
	if !bannerIsWrittenToStdout(t, h) {
		t.Fatal("the fixture no longer writes a stdout banner, so this proves nothing")
	}
}

// bannerIsWrittenToStdout confirms the fake ssh still contaminates stdout.
func bannerIsWrittenToStdout(t *testing.T, h *loopbackHost) bool {
	t.Helper()
	cmd := exec.Command("ssh", "loopback", "$SHELL -l -c 'true'")
	cmd.Env = append(os.Environ(), "SIDECAR_LOOPBACK_SSH_EXIT=", "SIDECAR_LOOPBACK_SSH_DELAY=")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("probe the fake ssh: %v", err)
	}
	_ = h
	return strings.Contains(string(out), "stdout banner")
}
