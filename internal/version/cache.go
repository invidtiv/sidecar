package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

const (
	cacheFile      = "version_cache.json"
	tdCacheFile    = "td_version_cache.json"
	tasksCacheFile = "tasks_version_cache.json"
	cacheTTL       = 3 * time.Hour
)

// CacheEntry stores cached version check result.
type CacheEntry struct {
	LatestVersion  string    `json:"latestVersion"`
	CurrentVersion string    `json:"currentVersion"`
	CheckedAt      time.Time `json:"checkedAt"`
	HasUpdate      bool      `json:"hasUpdate"`
	// Notes is the offered release's body at the time of the check, so the
	// cache-hit path can still show release notes without a network call.
	Notes string `json:"notes,omitempty"`
}

func isolatedConfigFile(name string) string {
	dir := filepath.Dir(config.ConfigPath())
	if dir == "" || dir == "." {
		return ""
	}
	path := filepath.Join(dir, name)
	if err := config.AssertIsolatedPath(path); err != nil {
		return ""
	}
	return path
}

// LoadCacheFile reads a cached version check result for a named cache file.
func LoadCacheFile(name string) (*CacheEntry, error) {
	path := isolatedConfigFile(name)
	if path == "" {
		return nil, os.ErrNotExist
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var entry CacheEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		return nil, err
	}
	return &entry, nil
}

// SaveCacheFile writes a version check result to a named cache file.
func SaveCacheFile(name string, entry *CacheEntry) error {
	path := isolatedConfigFile(name)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// IsCacheValid checks if cache exists and is not expired.
// Also invalidates if user version changed (upgrade or downgrade).
func IsCacheValid(entry *CacheEntry, currentVersion string) bool {
	if entry == nil {
		return false
	}
	// Invalidate if current version changed (handles upgrade or downgrade)
	if entry.CurrentVersion != currentVersion {
		return false
	}
	if time.Since(entry.CheckedAt) >= cacheTTL {
		return false
	}
	return true
}
