package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/mobileproto"
)

// Opt-in because this needs a real local sshd, not the fake SSH fixture.
// Every key, process, control socket and Sidecar path belongs to this test.
func TestMobileOwnerSSHControlMasterTransportLoss(t *testing.T) {
	if os.Getenv("SIDECAR_OWNER_SSH_PROOF") != "1" {
		t.Skip("run scripts/test-mobile-owner-ssh.sh for real ControlMaster proof")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	root, err := os.MkdirTemp("/tmp", "sidecar-owner-ssh-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	run := func(name string, args ...string) []byte {
		t.Helper()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %v: %s", name, err, out)
		}
		return out
	}
	bin := os.Getenv("SIDECAR_OWNER_PROOF_BIN")
	if bin == "" {
		bin = filepath.Join(root, "sidecar")
		run("go", "build", "-o", bin, "../../cmd/sidecar")
	}
	for _, key := range []string{"host_key", "client_key"} {
		run("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", filepath.Join(root, key))
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	sshdPath, err := exec.LookPath("sshd")
	if err != nil {
		sshdPath = "/usr/sbin/sshd"
	}
	sshdConfig := fmt.Sprintf("ListenAddress 127.0.0.1\nPort %d\nHostKey %s/host_key\nPidFile %s/sshd.pid\nAuthorizedKeysFile %s/client_key.pub\nStrictModes no\nUsePAM no\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nLogLevel ERROR\n", port, root, root, root)
	configPath := filepath.Join(root, "sshd_config")
	if err := os.WriteFile(configPath, []byte(sshdConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	log, err := os.Create(filepath.Join(root, "sshd.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	daemon := exec.CommandContext(ctx, sshdPath, "-D", "-e", "-f", configPath)
	daemon.Stderr = log
	if err := daemon.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = daemon.Process.Kill(); _ = daemon.Wait() })
	sshArgs := []string{"-F", "/dev/null", "-p", strconv.Itoa(port), "-i", filepath.Join(root, "client_key"), "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=" + filepath.Join(root, "known_hosts"), "-o", "BatchMode=yes", "-o", "ControlMaster=auto", "-o", "ControlPath=" + filepath.Join(root, "master.sock"), "-o", "ControlPersist=60"}
	target := os.Getenv("USER") + "@127.0.0.1"
	ssh := func(extra ...string) *exec.Cmd {
		args := append(append([]string{}, sshArgs...), extra...)
		return exec.CommandContext(ctx, "ssh", args...)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := ssh(target, "true").CombinedOutput()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			detail, _ := os.ReadFile(filepath.Join(root, "sshd.log"))
			t.Fatalf("private SSH server unavailable: %v: %s; daemon: %s", err, out, detail)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Cleanup(func() { _ = ssh("-O", "exit", target).Run() })
	if out, err := ssh("-O", "check", target).CombinedOutput(); err != nil {
		t.Fatalf("ControlMaster not running: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "config.json"), []byte(`{"projects":{"list":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"state", "tmux"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	for _, drop := range []string{"stdin-eof", "slave-kill", "master-exit"} {
		t.Run(drop, func(t *testing.T) {
			var clients []*exec.Cmd
			var inputs []io.WriteCloser
			var pids []int
			for i := 0; i < 4; i++ {
				pidPath := filepath.Join(root, fmt.Sprintf("%s-%d.pid", drop, i))
				// sshd's non-login PATH may omit Homebrew's tmux. This private
				// server uses the test's tool paths, as the production registry
				// does through its configured login shell.
				remote := "echo $$ > " + quote(pidPath) + "; exec env -u TMUX -u TMUX_PANE PATH=" + quote(os.Getenv("PATH")) + " XDG_STATE_HOME=" + quote(filepath.Join(root, "state")) + " TMUX_TMPDIR=" + quote(filepath.Join(root, "tmux")) + " SIDECAR_ISOLATED_STATE=1 " + quote(bin) + " -config " + quote(filepath.Join(root, "config.json")) + " mobile serve --stdio --owner-only"
				cmd := ssh(target, remote)
				input, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				output, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				cmd.Stderr = log
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
				encoder, scanner := json.NewEncoder(input), bufio.NewScanner(output)
				scanner.Buffer(make([]byte, 64<<10), mobileproto.MaxLineBytes)
				for _, request := range []mobileproto.Request{{Version: mobileproto.Version, Type: mobileproto.RequestHello, RequestID: "hub-owner-hello"}, {Version: mobileproto.Version, Type: mobileproto.RequestSessions, RequestID: "catalog-1"}} {
					if err := encoder.Encode(request); err != nil {
						t.Fatal(err)
					}
					if !scanner.Scan() {
						t.Fatalf("owner failed before %s: %v", request.Type, scanner.Err())
					}
					var response mobileproto.Response
					if err := json.Unmarshal(scanner.Bytes(), &response); err != nil || response.RequestID != request.RequestID || response.Type != request.Type {
						t.Fatalf("unexpected owner response: %s (%v)", scanner.Bytes(), err)
					}
				}
				data, err := os.ReadFile(pidPath)
				if err != nil {
					t.Fatal(err)
				}
				pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
				if err != nil || pid <= 1 {
					t.Fatalf("invalid owned PID %q", data)
				}
				pids = append(pids, pid)
				t.Cleanup(func() {
					// A successful drop has already reaped this PID. Only signal
					// a surviving command that still names our private binary.
					args, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
					if err == nil && strings.Contains(string(args), bin) {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
				})
				clients, inputs = append(clients, cmd), append(inputs, input)
			}
			start := time.Now()
			switch drop {
			case "stdin-eof":
				for _, input := range inputs {
					_ = input.Close()
				}
			case "slave-kill":
				for _, client := range clients {
					_ = client.Process.Kill()
				}
			case "master-exit":
				if out, err := ssh("-O", "exit", target).CombinedOutput(); err != nil {
					t.Fatalf("drop master: %v: %s", err, out)
				}
			}
			for _, pid := range pids {
				for syscall.Kill(pid, 0) == nil {
					if time.Since(start) > 5*time.Second {
						t.Fatalf("owner %d survived %s beyond 5s", pid, drop)
					}
					time.Sleep(25 * time.Millisecond)
				}
			}
			t.Logf("4 owner streams, hello + catalog, %s: 0 survivors after %s", drop, time.Since(start).Round(time.Millisecond))
		})
	}
}
