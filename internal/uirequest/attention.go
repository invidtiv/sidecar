package uirequest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

var attentionPublishMu sync.Mutex

// Attention is one live Sidecar instance's host-visible focus context.
type Attention struct {
	PID           int       `json:"pid"`
	Host          string    `json:"host"`
	Focused       bool      `json:"focused"`
	VisibleOrigin Origin    `json:"visibleOrigin,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

func attentionDir(stateDir string) (string, error) {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return "", err
	}
	dir := filepath.Join(stateDir, "attention")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

func attentionPath(stateDir string, pid int) string {
	return filepath.Join(stateDir, "attention", fmt.Sprintf("%d.json", pid))
}

// PublishAttention atomically replaces this process's attention record.
func PublishAttention(stateDir string, attention Attention) error {
	if attention.PID <= 0 {
		attention.PID = os.Getpid()
	}
	if attention.Host == "" {
		attention.Host = HostName()
	}
	if attention.UpdatedAt.IsZero() {
		attention.UpdatedAt = time.Now().UTC()
	} else {
		attention.UpdatedAt = attention.UpdatedAt.UTC()
	}
	if _, err := attentionDir(stateDir); err != nil {
		return err
	}
	attentionPublishMu.Lock()
	defer attentionPublishMu.Unlock()
	target := attentionPath(stateDir, attention.PID)
	if currentData, err := os.ReadFile(target); err == nil {
		var current Attention
		if json.Unmarshal(currentData, &current) == nil && current.UpdatedAt.After(attention.UpdatedAt) {
			return nil
		}
	}
	data, err := json.MarshalIndent(attention, "", "  ")
	if err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", target, os.Getpid())
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func WithdrawAttention(stateDir string, pid int) error {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return err
	}
	if pid <= 0 {
		pid = os.Getpid()
	}
	err := os.Remove(attentionPath(stateDir, pid))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListAttention returns live same-host records and discards malformed, stale,
// or dead-process files using the same PID liveness rule as ListInstances.
func ListAttention(stateDir string) ([]Attention, error) {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return nil, err
	}
	dir := filepath.Join(stateDir, "attention")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	host := HostName()
	out := make([]Attention, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(dir, name)
		if entry.IsDir() || strings.Contains(name, ".tmp.") || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, readErr := os.ReadFile(path)
		var attention Attention
		if readErr != nil || json.Unmarshal(data, &attention) != nil || attention.PID <= 0 {
			_ = os.Remove(path)
			continue
		}
		if attention.Host != "" && attention.Host != host {
			continue
		}
		if !pidAlive(attention.PID) {
			_ = os.Remove(path)
			continue
		}
		out = append(out, attention)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, nil
}

// OriginForeground reports whether any focused live instance is visibly
// showing the notification origin. An unresolved origin is background.
func OriginForeground(origin Origin, attention []Attention) bool {
	if origin.TmuxSession == "" && origin.ProjectKey == "" && origin.WorkDir == "" {
		return false
	}
	for _, record := range attention {
		if record.Focused && originsMatch(origin, record.VisibleOrigin) {
			return true
		}
	}
	return false
}

func originsMatch(a, b Origin) bool {
	// Machine first. A remote workspace and a local one are different places
	// however much their session names, project keys, or paths agree, and
	// treating them as one would silence a remote agent's alert because
	// something local happened to be selected.
	if a.HostID != b.HostID {
		return false
	}
	if a.TmuxSession != "" || b.TmuxSession != "" {
		return a.TmuxSession != "" && a.TmuxSession == b.TmuxSession
	}
	if a.WorkDir != "" || b.WorkDir != "" {
		return a.WorkDir != "" && filepath.Clean(a.WorkDir) == filepath.Clean(b.WorkDir)
	}
	return a.ProjectKey != "" && a.ProjectKey == b.ProjectKey
}
