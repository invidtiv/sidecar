package tmuxserver

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/marcus/sidecar/internal/tmuxenv"
)

// State describes whether the tmux namespace can accept commands. ExitPending
// is distinct from Absent: a tmux server still owns the socket, but is refusing
// new commands while attached clients keep its shutdown pending.
type State string

const (
	StateUnknown     State = "unknown"
	StateAbsent      State = "absent"
	StateReady       State = "ready"
	StateExitPending State = "exit_pending"
)

func (s State) String() string { return string(s) }

// ControlClient is a process connected to the tmux socket whose argv is the
// exact control-mode attach form Sidecar starts.
type ControlClient struct {
	PID       int    `json:"pid"`
	ParentPID int    `json:"parentPid"`
	StartedAt string `json:"startedAt"`
	Command   string `json:"command"`
	Orphaned  bool   `json:"orphaned"`
}

// Status is one observation of the tmux namespace.
type Status struct {
	State   State           `json:"state"`
	Socket  string          `json:"socket"`
	Detail  string          `json:"detail,omitempty"`
	Clients []ControlClient `json:"clients,omitempty"`
}

// Inspect distinguishes an absent server, a responsive server, and tmux's
// exit-pending state. All subprocesses address the resolved socket explicitly.
func Inspect(ctx context.Context) (Status, error) {
	return inspect(ctx, tmuxenv.SocketPath())
}

func inspect(ctx context.Context, socket string) (Status, error) {
	status := Status{State: StateUnknown, Socket: socket}
	cmd := exec.CommandContext(ctx, "tmux", "-S", socket, "list-sessions", "-F", "#{session_name}")
	out, err := cmd.CombinedOutput()
	if err == nil {
		status.State = StateReady
		return status, nil
	}
	message := strings.TrimSpace(string(out))
	status.Detail = message
	switch {
	case isNoServerMessage(message):
		status.State = StateAbsent
		return status, nil
	case isExitPendingMessage(message):
		status.State = StateExitPending
		clients, inspectErr := socketControlClients(ctx, socket)
		status.Clients = clients
		if inspectErr != nil {
			return status, fmt.Errorf("inspect exit-pending tmux clients: %w", inspectErr)
		}
		return status, nil
	default:
		return status, fmt.Errorf("inspect tmux server: %w: %s", err, message)
	}
}

func isNoServerMessage(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "no server running") ||
		strings.Contains(message, "no sessions") ||
		(strings.Contains(message, "error connecting to") &&
			(strings.Contains(message, "no such file or directory") || strings.Contains(message, "connection refused")))
}

func isExitPendingMessage(message string) bool {
	return strings.Contains(strings.ToLower(message), "server exited unexpectedly")
}

// ErrLiveControlClients reports that recovery cannot finish without affecting
// a control client whose parent is still alive.
var ErrLiveControlClients = errors.New("live Sidecar control clients still hold the tmux server")

// RecoverExitPending terminates only revalidated orphaned Sidecar control
// clients attached to the observed socket, then waits for tmux to exit by
// itself. It never sends a signal to the tmux server.
func RecoverExitPending(ctx context.Context, observed Status) ([]int, error) {
	if observed.State != StateExitPending {
		return nil, fmt.Errorf("recover tmux server: state is not exit-pending")
	}
	// Callers commonly use a process-lifetime context. Recovery must still be
	// bounded when tmux reports exit-pending but process inspection cannot prove
	// any client safe to terminate.
	recoveryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	current, err := inspect(recoveryCtx, observed.Socket)
	if err != nil {
		return nil, err
	}
	if current.State != StateExitPending {
		return nil, nil
	}
	// Refuse the whole recovery before sending any signal when even one client
	// still has a live parent. A mixed set must not partially clean up siblings
	// and then report refusal.
	if liveErr := liveControlClientError(current.Clients); liveErr != nil {
		return nil, liveErr
	}
	var terminated []int
	for _, client := range current.Clients {
		if !client.Orphaned {
			continue
		}
		verified, verifyErr := inspectControlClient(recoveryCtx, observed.Socket, client.PID)
		if verifyErr != nil || !verified.Orphaned || verified.StartedAt != client.StartedAt {
			continue
		}
		if err := syscall.Kill(client.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return terminated, fmt.Errorf("terminate orphaned tmux control client %d: %w", client.PID, err)
		}
		terminated = append(terminated, client.PID)
	}

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, inspectErr := inspect(recoveryCtx, observed.Socket)
		if inspectErr != nil {
			return terminated, inspectErr
		}
		if status.State != StateExitPending {
			return terminated, nil
		}
		if liveErr := liveControlClientError(status.Clients); liveErr != nil {
			return terminated, liveErr
		}
		select {
		case <-recoveryCtx.Done():
			return terminated, fmt.Errorf("wait for exit-pending tmux server to clear after terminating %d safe client(s): %w", len(terminated), recoveryCtx.Err())
		case <-ticker.C:
		}
	}
}

func liveControlClientError(clients []ControlClient) error {
	for _, client := range clients {
		if !client.Orphaned {
			return fmt.Errorf("%w: pid %d has live parent %d", ErrLiveControlClients, client.PID, client.ParentPID)
		}
	}
	return nil
}

func socketControlClients(ctx context.Context, socket string) ([]ControlClient, error) {
	cmd := exec.CommandContext(ctx, "lsof", "-nP", "-U")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, err
	}
	pids := parseLsofSocketPIDs(string(out), socket)
	psOut, psErr := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,lstart=,command=").Output()
	if psErr != nil {
		return nil, psErr
	}
	for _, line := range strings.Split(string(psOut), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		pid, parseErr := strconv.Atoi(fields[0])
		if parseErr == nil && isSidecarControlAttach(strings.Join(fields[7:], " "), socket) && !containsPID(pids, pid) {
			pids = append(pids, pid)
		}
	}
	clients := make([]ControlClient, 0, len(pids))
	for _, pid := range pids {
		client, inspectErr := inspectControlClient(ctx, socket, pid)
		if inspectErr == nil && client.PID != 0 {
			clients = append(clients, client)
		}
	}
	return clients, nil
}

func inspectControlClient(ctx context.Context, socket string, pid int) (ControlClient, error) {
	out, err := exec.CommandContext(ctx, "ps", "-o", "ppid=", "-o", "lstart=", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ControlClient{}, err
	}
	ppid, startedAt, command, ok := parsePSLine(string(out))
	if !ok || !isSidecarControlAttach(command, socket) {
		return ControlClient{}, nil
	}
	connected, err := processTargetsSocket(ctx, pid, command, socket)
	if err != nil || !connected {
		return ControlClient{}, err
	}
	orphaned := ppid <= 1
	if !orphaned {
		if err := syscall.Kill(ppid, 0); errors.Is(err, syscall.ESRCH) {
			orphaned = true
		}
	}
	return ControlClient{PID: pid, ParentPID: ppid, StartedAt: startedAt, Command: command, Orphaned: orphaned}, nil
}

func containsPID(pids []int, want int) bool {
	for _, pid := range pids {
		if pid == want {
			return true
		}
	}
	return false
}

func processTargetsSocket(ctx context.Context, pid int, command, socket string) (bool, error) {
	fields := strings.Fields(command)
	for i := 1; i+1 < len(fields); i++ {
		if fields[i] == "-S" {
			return fields[i+1] == socket, nil
		}
	}
	// Sidecar's default attach has no -S argv, so pin it through the inherited
	// tmux namespace environment. This also works after the server half exits,
	// when lsof retains only an anonymous peer. Reading the process once avoids
	// a full-system lsof scan for every unrelated Sidecar control client.
	out, err := exec.CommandContext(ctx, "ps", "eww", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false, err
	}
	tmpDir := "/tmp"
	for _, field := range strings.Fields(string(out)) {
		if strings.HasPrefix(field, "TMUX=") && strings.TrimPrefix(field, "TMUX=") != "" {
			return false, nil
		}
		if strings.HasPrefix(field, "TMUX_TMPDIR=") {
			tmpDir = strings.TrimPrefix(field, "TMUX_TMPDIR=")
		}
	}
	want := filepath.Join(tmpDir, "tmux-"+strconv.Itoa(syscall.Getuid()), "default")
	return filepath.Clean(want) == filepath.Clean(socket), nil
}

// parseLsofSocketPIDs joins the two halves of a connected Unix socket. On
// macOS the listening/accepted server descriptor carries the socket pathname
// and a kernel node id, while a client descriptor carries only ->node-id.
// Filtering lsof by pathname therefore hides exactly the clients we need.
func parseLsofSocketPIDs(out, socket string) []int {
	type row struct {
		pid  int
		node string
		name string
	}
	var rows []row
	for i, line := range strings.Split(out, "\n") {
		if i == 0 || strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 8 {
			continue
		}
		pid, err := strconv.Atoi(fields[1])
		if err != nil || fields[4] != "unix" {
			continue
		}
		rows = append(rows, row{pid: pid, node: fields[5], name: strings.Join(fields[7:], " ")})
	}
	nodes := map[string]bool{}
	for _, row := range rows {
		if row.name == socket {
			nodes[row.node] = true
		}
	}
	seen := map[int]bool{}
	var pids []int
	for _, row := range rows {
		if strings.HasPrefix(row.name, "->") && nodes[strings.TrimPrefix(row.name, "->")] && !seen[row.pid] {
			seen[row.pid] = true
			pids = append(pids, row.pid)
		}
	}
	return pids
}

func parsePSLine(out string) (int, string, string, bool) {
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 7 {
		return 0, "", "", false
	}
	ppid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, "", "", false
	}
	return ppid, strings.Join(fields[1:6], " "), strings.Join(fields[6:], " "), true
}

func isSidecarControlAttach(command, socket string) bool {
	fields := strings.Fields(command)
	if len(fields) == 0 || filepath.Base(fields[0]) != "tmux" {
		return false
	}
	args := fields[1:]
	if len(args) >= 2 && args[0] == "-S" {
		if args[1] != socket {
			return false
		}
		args = args[2:]
	}
	return len(args) == 6 && args[0] == "-C" && args[1] == "attach-session" &&
		args[2] == "-f" && args[3] == "ignore-size" && args[4] == "-t" &&
		isSidecarSession(args[5])
}

func isSidecarSession(session string) bool {
	for _, prefix := range []string{"sidecar-sh-", "sidecar-ws-", "sidecar-tp-", "sidecar-edit-"} {
		if strings.HasPrefix(session, prefix) && len(session) > len(prefix) {
			return true
		}
	}
	return false
}
