package uiapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const (
	endpointFileName = "endpoint.json"
	lockFileName     = "serve.lock"
	localSocketName  = "api.sock"
	tailnetSockName  = "tailnet.sock"
	originsFileName  = "origins.json"
)

// Endpoint is $STATE/api/endpoint.json: how local CLI verbs and SDK users
// find the one running server.
type Endpoint struct {
	PID           int       `json:"pid"`
	Version       string    `json:"version"`
	APIVersion    int       `json:"api_version"`
	APIInstance   string    `json:"api_instance"`
	StartedAt     time.Time `json:"started_at"`
	UnixSocket    string    `json:"unix_socket"`
	TCP           string    `json:"tcp"`
	TailnetSocket string    `json:"tailnet_socket,omitempty"`
	TailnetTCP    string    `json:"tailnet_tcp,omitempty"`
}

// Dir is where the API keeps its sockets, endpoint and paired origins.
func Dir(stateDir string) string { return filepath.Join(stateDir, "api") }

// EndpointPath is the discovery file for a state tree.
func EndpointPath(stateDir string) string { return filepath.Join(Dir(stateDir), endpointFileName) }

// ErrNotRunning reports that no endpoint file names a live server.
var ErrNotRunning = errors.New("no Sidecar API server is running; start one with `sidecar api serve`")

// AlreadyRunningError is the single-instance refusal.
type AlreadyRunningError struct{ PID int }

func (e *AlreadyRunningError) Error() string {
	if e.PID > 0 {
		return fmt.Sprintf("a Sidecar API server is already running (pid %d); use `sidecar api status` or stop it first", e.PID)
	}
	return "a Sidecar API server is already running; use `sidecar api status` or stop it first"
}

// ReadEndpoint reads the discovery file and refuses one whose process is gone.
func ReadEndpoint(stateDir string) (Endpoint, error) {
	data, err := os.ReadFile(EndpointPath(stateDir))
	if errors.Is(err, os.ErrNotExist) {
		return Endpoint{}, ErrNotRunning
	}
	if err != nil {
		return Endpoint{}, err
	}
	var endpoint Endpoint
	if err := json.Unmarshal(data, &endpoint); err != nil {
		return Endpoint{}, fmt.Errorf("read %s: %w", EndpointPath(stateDir), err)
	}
	if !pidAlive(endpoint.PID) {
		return Endpoint{}, ErrNotRunning
	}
	return endpoint, nil
}

// acquireLock takes the per-state-tree single-instance lock. The kernel
// releases it when the process dies, so a crashed server never blocks the next
// one; the recorded PID only makes the refusal say who holds it.
func acquireLock(dir string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(dir, lockFileName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			pid := 0
			if data, readErr := os.ReadFile(filepath.Join(dir, endpointFileName)); readErr == nil {
				var endpoint Endpoint
				if json.Unmarshal(data, &endpoint) == nil {
					pid = endpoint.PID
				}
			}
			return nil, &AlreadyRunningError{PID: pid}
		}
		return nil, err
	}
	return file, nil
}

func releaseLock(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func writeEndpoint(dir string, endpoint Endpoint) error {
	data, err := json.MarshalIndent(endpoint, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(dir, endpointFileName), append(data, '\n'))
}

// removeEndpoint removes the discovery file only while it still names this
// process, so a slow shutdown cannot delete a successor's file.
func removeEndpoint(dir, instance string) {
	path := filepath.Join(dir, endpointFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var endpoint Endpoint
	if json.Unmarshal(data, &endpoint) == nil && endpoint.APIInstance == instance {
		_ = os.Remove(path)
	}
}

// writePrivateFile replaces path atomically with a 0600 file.
func writePrivateFile(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
