package hostserve

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

const viewerCapabilityUIRequestRelayV1 = "uiRequestRelayV1"

// viewerPresence is the ephemeral file serve writes so the host CLI can tell
// a connected viewer from a stale lease. Isolation-gated; not shells.json.
type viewerPresence struct {
	Instance     string    `json:"instance"`
	PID          int       `json:"pid"`
	Capabilities []string  `json:"capabilities"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

func viewersDir(stateDir string) (string, error) {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return "", err
	}
	dir := filepath.Join(stateDir, "viewers")
	if err := config.AssertIsolatedPath(dir); err != nil {
		return "", err
	}
	return dir, nil
}

func viewerPresencePath(stateDir, instance string) (string, error) {
	if !safeViewerInstance(instance) {
		return "", fmt.Errorf("hostserve: invalid viewer instance %q", instance)
	}
	dir, err := viewersDir(stateDir)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, instance+".json")
	if err := config.AssertIsolatedPath(path); err != nil {
		return "", err
	}
	return path, nil
}

func safeViewerInstance(instance string) bool {
	if instance == "" || instance == "." || instance == ".." {
		return false
	}
	if strings.ContainsAny(instance, `/\`) {
		return false
	}
	return true
}

func viewerPID(instance string) int {
	i := strings.LastIndex(instance, "-")
	if i < 0 {
		return 0
	}
	pid, err := strconv.Atoi(instance[i+1:])
	if err != nil || pid <= 0 {
		return 0
	}
	return pid
}

func presenceTTL(opts Options) time.Duration {
	// Refresh is per cycle, which can be the idle cadence, so the TTL has to
	// cover that wait plus one live poll or a connected viewer looks gone.
	ttl := opts.IdlePoll + opts.LivePoll
	if ttl <= 0 {
		ttl = DefaultIdlePoll + DefaultLivePoll
	}
	return ttl
}

func refreshViewerPresence(stateDir, instance string, now time.Time, ttl time.Duration) error {
	path, err := viewerPresencePath(stateDir, instance)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	body, err := json.Marshal(viewerPresence{
		Instance:     instance,
		PID:          viewerPID(instance),
		Capabilities: []string{viewerCapabilityUIRequestRelayV1},
		ExpiresAt:    now.Add(ttl).UTC(),
	})
	if err != nil {
		return err
	}
	tmp := path + fmt.Sprintf(".tmp.%d", os.Getpid())
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
