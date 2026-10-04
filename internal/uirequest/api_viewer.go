package uirequest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

// APIViewer is an ephemeral screen claim. The API owns selection; callers pin
// its connection identity when posting, so reconnects never replay requests.
type APIViewer struct {
	Instance     string    `json:"instance"`
	PID          int       `json:"pid"`
	Focused      bool      `json:"focused"`
	Capabilities []string  `json:"capabilities"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

const APIViewerRelay = "uiRequestRelayV1"

func ReadAPIViewer(stateDir string, now time.Time) (APIViewer, bool) {
	if config.AssertIsolatedPath(stateDir) != nil {
		return APIViewer{}, false
	}
	data, err := os.ReadFile(filepath.Join(stateDir, "viewers", "api-screen.json"))
	var v APIViewer
	if err != nil || json.Unmarshal(data, &v) != nil || v.Instance == "" || v.PID <= 0 || !v.ExpiresAt.After(now) {
		return APIViewer{}, false
	}
	if err := syscall.Kill(v.PID, 0); err != nil && err != syscall.EPERM {
		return APIViewer{}, false
	}
	return v, true
}

func WriteAPIViewer(stateDir string, v APIViewer) error {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return err
	}
	dir := filepath.Join(stateDir, "viewers")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".api-screen-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(data)
	closeErr := tmp.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, "api-screen.json"))
}

func (v APIViewer) HasCapability(name string) bool {
	for _, capability := range v.Capabilities {
		if capability == name {
			return true
		}
	}
	return false
}
