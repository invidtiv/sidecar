// Package activitystore persists agent activity across Sidecar restarts.
//
// Without it every launch rebuilds its trackers from nothing, so the first
// observation of an already-idle session is treated as initialization: ages
// all read "just now" and a turn that finished while Sidecar was closed can
// never surface as done. Persisting the idle transition keeps both facts true
// across restarts.
package activitystore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// FileName is the store's basename inside the Sidecar state directory.
const FileName = "agent-activity.json"

// RetainFor bounds how long an untouched entry survives. Workspaces come and
// go; without pruning the file would accumulate every shell ever observed.
const RetainFor = 7 * 24 * time.Hour

type entry struct {
	agentactivity.Snapshot
	UpdatedAt time.Time `json:"updatedAt"`
}

type file struct {
	Version int              `json:"version"`
	Entries map[string]entry `json:"entries"`
}

// Load reads persisted trackers. A missing or unreadable store is not an
// error: activity state is a convenience, and losing it costs one poll cycle.
//
// Only idle state is restored. A persisted working/blocked state would be
// re-detected within a poll anyway, and restoring it risks manufacturing a
// false completion when the pane's process was replaced while Sidecar was down.
func Load(path string, now time.Time) map[string]agentactivity.Tracker {
	trackers := make(map[string]agentactivity.Tracker)
	data, err := os.ReadFile(path)
	if err != nil {
		return trackers
	}
	var stored file
	if err := json.Unmarshal(data, &stored); err != nil {
		return trackers
	}
	for key, e := range stored.Entries {
		if e.State != string(agentactivity.StateIdle) {
			continue
		}
		if !e.UpdatedAt.IsZero() && now.Sub(e.UpdatedAt) > RetainFor {
			continue
		}
		trackers[key] = agentactivity.Restore(e.Snapshot)
	}
	return trackers
}

// Save writes the given trackers, replacing the file atomically so a crash
// mid-write cannot leave a half-parsed store behind.
func Save(path string, trackers map[string]agentactivity.Tracker, now time.Time) error {
	out := file{Version: 1, Entries: make(map[string]entry, len(trackers))}
	for key, tracker := range trackers {
		if tracker.State != agentactivity.StateIdle {
			continue
		}
		out.Entries[key] = entry{Snapshot: tracker.Snapshot(), UpdatedAt: now}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agent-activity-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	return os.Rename(name, path)
}
