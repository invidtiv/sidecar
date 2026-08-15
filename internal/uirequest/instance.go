package uirequest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

// Instance is a live Sidecar TUI process, announced under the state dir so
// `sidecar open` can address it from outside a project shell.
type Instance struct {
	PID        int       `json:"pid"`
	Host       string    `json:"host"`
	ProjectKey string    `json:"projectKey"`
	Project    string    `json:"project"`
	WorkDir    string    `json:"workDir"`
	StartedAt  time.Time `json:"startedAt"`
}

// InstancesDir returns and ensures the instance-presence directory.
func InstancesDir(stateDir string) (string, error) {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return "", err
	}
	dir := filepath.Join(stateDir, "instances")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

// InstancePath is the presence file for pid.
func InstancePath(stateDir string, pid int) string {
	return filepath.Join(stateDir, "instances", fmt.Sprintf("%d.json", pid))
}

// Announce writes (or replaces) this process's presence file.
func Announce(stateDir string, inst Instance) error {
	if inst.PID <= 0 {
		inst.PID = os.Getpid()
	}
	if inst.Host == "" {
		inst.Host = HostName()
	}
	if inst.StartedAt.IsZero() {
		inst.StartedAt = time.Now().UTC()
	}

	if _, err := InstancesDir(stateDir); err != nil {
		return err
	}

	data, err := json.MarshalIndent(inst, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal instance: %w", err)
	}

	targetPath := InstancePath(stateDir, inst.PID)
	tmpPath := targetPath + fmt.Sprintf(".tmp.%d", os.Getpid())

	f, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open tmp instance: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write tmp instance: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("sync tmp instance: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close tmp instance: %w", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename instance: %w", err)
	}
	return nil
}

// Withdraw removes this process's presence file. Liveness is still the
// authority; a crash that skips this is cleaned up by ListInstances.
func Withdraw(stateDir string, pid int) error {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return err
	}
	if pid <= 0 {
		pid = os.Getpid()
	}
	err := os.Remove(InstancePath(stateDir, pid))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// ListInstances returns live same-host presence records and sweeps dead files.
func ListInstances(stateDir string) ([]Instance, error) {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return nil, err
	}
	dir := filepath.Join(stateDir, "instances")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	host := HostName()
	var live []Instance
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(dir, name)
		if entry.IsDir() || strings.Contains(name, ".tmp.") || !strings.HasSuffix(name, ".json") {
			continue
		}
		inst, readErr := readInstance(path)
		if readErr != nil {
			_ = os.Remove(path)
			continue
		}
		if inst.Host != "" && inst.Host != host {
			continue
		}
		if !pidAlive(inst.PID) {
			_ = os.Remove(path)
			continue
		}
		live = append(live, inst)
	}
	sort.Slice(live, func(i, j int) bool {
		if live[i].ProjectKey != live[j].ProjectKey {
			return live[i].ProjectKey < live[j].ProjectKey
		}
		if live[i].Project != live[j].Project {
			return live[i].Project < live[j].Project
		}
		return live[i].PID < live[j].PID
	})
	return live, nil
}

func readInstance(path string) (Instance, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Instance{}, err
	}
	var inst Instance
	if err := json.Unmarshal(data, &inst); err != nil {
		return Instance{}, err
	}
	if inst.PID <= 0 {
		if pid, convErr := pidFromInstanceName(filepath.Base(path)); convErr == nil {
			inst.PID = pid
		}
	}
	if inst.PID <= 0 {
		return Instance{}, fmt.Errorf("instance file %s has no pid", path)
	}
	return inst, nil
}

func pidFromInstanceName(name string) (int, error) {
	return strconv.Atoi(strings.TrimSuffix(name, ".json"))
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
