package tmuxserver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestExitPendingMessageIsDistinct(t *testing.T) {
	if !isExitPendingMessage("server exited unexpectedly") {
		t.Fatal("tmux exit-pending diagnostic was not recognized")
	}
	if isNoServerMessage("server exited unexpectedly") {
		t.Fatal("exit-pending server was classified absent")
	}
}

func TestParsePSLine(t *testing.T) {
	ppid, started, command, ok := parsePSLine("  1 Fri Sep  4 20:01:02 2026 /opt/homebrew/bin/tmux -C attach-session -f ignore-size -t sidecar-sh-x\n")
	if !ok || ppid != 1 || started != "Fri Sep 4 20:01:02 2026" || command != "/opt/homebrew/bin/tmux -C attach-session -f ignore-size -t sidecar-sh-x" {
		t.Fatalf("parsePSLine = %d, %q, %q, %v", ppid, started, command, ok)
	}
}

func TestSidecarControlAttachRequiresExactOwnedShape(t *testing.T) {
	socket := "/tmp/private-socket"
	tests := []struct {
		command string
		want    bool
	}{
		{"tmux -C attach-session -f ignore-size -t sidecar-sh-shell", true},
		{"/opt/homebrew/bin/tmux -S " + socket + " -C attach-session -f ignore-size -t sidecar-tp-shell", true},
		{"evil-tmux -C attach-session -f ignore-size -t sidecar-sh-shell", false},
		{"tmux -C attach-session -f ignore-size -t third-party", false},
		{"tmux -C attach-session -t sidecar-sh-shell", false},
		{"tmux -C attach-session -f ignore-size", false},
		{"tmux -S /tmp/other -C attach-session -f ignore-size -t sidecar-sh-shell", false},
		{"tmux attach-session -f ignore-size -t sidecar-sh-shell", false},
		{"other -C attach-session -f ignore-size -t sidecar-sh-shell", false},
	}
	for _, tt := range tests {
		if got := isSidecarControlAttach(tt.command, socket); got != tt.want {
			t.Errorf("isSidecarControlAttach(%q) = %v, want %v", tt.command, got, tt.want)
		}
	}
}

func TestLiveClientErrorIsRecognizable(t *testing.T) {
	err := fmtWrapLiveClient(42, 7)
	if !errors.Is(err, ErrLiveControlClients) {
		t.Fatalf("errors.Is(%v, ErrLiveControlClients) = false", err)
	}
}

func TestParseLsofSocketPIDsJoinsUnixPeers(t *testing.T) {
	const listing = `COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME
tmux 10 marcus 6u unix 0xaa 0t0 /tmp/private/tmux.sock
tmux 10 marcus 7u unix 0xbb 0t0 /tmp/private/tmux.sock
tmux 20 marcus 5u unix 0xcc 0t0 ->0xbb
tmux 21 marcus 5u unix 0xdd 0t0 ->0xother
`
	got := parseLsofSocketPIDs(listing, "/tmp/private/tmux.sock")
	if len(got) != 1 || got[0] != 20 {
		t.Fatalf("parseLsofSocketPIDs = %v, want [20]", got)
	}
}

func TestLiveControlClientErrorRefusesMixedSet(t *testing.T) {
	clients := []ControlClient{
		{PID: 10, ParentPID: 1, Orphaned: true},
		{PID: 20, ParentPID: 19, Orphaned: false},
	}
	err := liveControlClientError(clients)
	if !errors.Is(err, ErrLiveControlClients) || !strings.Contains(err.Error(), "pid 20") {
		t.Fatalf("mixed client preflight = %v", err)
	}
	if err := liveControlClientError(clients[:1]); err != nil {
		t.Fatalf("orphan-only preflight = %v", err)
	}
}

func TestSocketControlClientsFindsLiveClientOnExactSocket(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "sc-tmuxserver-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "tmux.sock")
	if out, err := exec.Command("tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", "sidecar-sh-proof", "sleep", "30").CombinedOutput(); err != nil {
		t.Fatalf("start isolated server: %v (%s)", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	input, inputWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = input.Close() }()
	defer func() { _ = inputWriter.Close() }()
	client := exec.Command("tmux", "-S", socket, "-C", "attach-session", "-f", "ignore-size", "-t", "sidecar-sh-proof")
	client.Stdin = input
	if err := client.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Process.Signal(syscall.SIGTERM)
		_ = client.Wait()
	})

	deadline := time.Now().Add(2 * time.Second)
	for {
		clients, inspectErr := socketControlClients(context.Background(), socket)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		if len(clients) == 1 {
			if clients[0].PID != client.Process.Pid || clients[0].Orphaned {
				t.Fatalf("clients = %+v, want live pid %d", clients, client.Process.Pid)
			}
			break
		}
		if time.Now().After(deadline) {
			lsofOut, _ := exec.Command("lsof", "-nP", "-U").Output()
			psOut, _ := exec.Command("ps", "-o", "ppid=", "-o", "lstart=", "-o", "command=", "-p", strconv.Itoa(client.Process.Pid)).Output()
			t.Fatalf("client %d was not found on %s: %+v\nps: %s\nlsof matches:\n%s", client.Process.Pid, socket, clients, psOut, lsofLinesForPIDs(string(lsofOut), client.Process.Pid))
		}
		time.Sleep(20 * time.Millisecond)
	}

	other := filepath.Join(dir, "other.sock")
	clients, err := socketControlClients(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}
	if len(clients) != 0 {
		t.Fatalf("client leaked across socket identity: %+v", clients)
	}
}

func lsofLinesForPIDs(out string, pid int) string {
	var matches []string
	needle := strconv.Itoa(pid)
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) || strings.Contains(line, "sc-tmuxserver-") {
			matches = append(matches, line)
		}
	}
	return strings.Join(matches, "\n")
}

func fmtWrapLiveClient(pid, ppid int) error {
	return errors.Join(ErrLiveControlClients, &clientError{pid: pid, ppid: ppid})
}

type clientError struct{ pid, ppid int }

func (e *clientError) Error() string { return "client" }
