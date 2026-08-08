package version

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

const (
	cacheFile   = "version_cache.json"
	tdCacheFile = "td_version_cache.json"
	cacheTTL    = 3 * time.Hour
)

// CacheEntry stores cached version check result.
type CacheEntry struct {
	LatestVersion  string    `json:"latestVersion"`
	CurrentVersion string    `json:"currentVersion"`
	CheckedAt      time.Time `json:"checkedAt"`
	HasUpdate      bool      `json:"hasUpdate"`
}

// cachePath returns the full path to the cache file.
//
// It follows config.ConfigPath() rather than $HOME so -config moves it with the
// rest of the config axis, and it returns "" when isolation is asserted and the
// path still lands in the real user tree. A version cache is optional, so a
// refusal simply means no caching for that run (td-8d18de).
func cachePath() string {
	return isolatedConfigFile(cacheFile)
}

// tdCachePath returns the full path to the td cache file.
func tdCachePath() string {
	return isolatedConfigFile(tdCacheFile)
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

// LoadCache reads cached version check result from disk.
func LoadCache() (*CacheEntry, error) {
	path := cachePath()
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

// SaveCache writes version check result to disk.
func SaveCache(entry *CacheEntry) error {
	path := cachePath()
	if path == "" {
		return nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
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

// LoadTdCache reads cached td version check result from disk.
func LoadTdCache() (*CacheEntry, error) {
	path := tdCachePath()
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

// SaveTdCache writes td version check result to disk.
func SaveTdCache(entry *CacheEntry) error {
	path := tdCachePath()
	if path == "" {
		return nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}
