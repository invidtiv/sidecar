package version

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

func TestIsCacheValid(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name           string
		entry          *CacheEntry
		currentVersion string
		want           bool
	}{
		{
			name:           "nil entry",
			entry:          nil,
			currentVersion: "v1.0.0",
			want:           false,
		},
		{
			name: "valid cache - same version, recent",
			entry: &CacheEntry{
				LatestVersion:  "v1.1.0",
				CurrentVersion: "v1.0.0",
				CheckedAt:      now,
				HasUpdate:      true,
			},
			currentVersion: "v1.0.0",
			want:           true,
		},
		{
			name: "expired cache - same version, old timestamp",
			entry: &CacheEntry{
				LatestVersion:  "v1.1.0",
				CurrentVersion: "v1.0.0",
				CheckedAt:      now.Add(-4 * time.Hour), // older than 3h TTL
				HasUpdate:      true,
			},
			currentVersion: "v1.0.0",
			want:           false,
		},
		{
			name: "invalid cache - version mismatch (upgrade)",
			entry: &CacheEntry{
				LatestVersion:  "v1.1.0",
				CurrentVersion: "v1.0.0",
				CheckedAt:      now,
				HasUpdate:      true,
			},
			currentVersion: "v1.1.0",
			want:           false,
		},
		{
			name: "invalid cache - version mismatch (downgrade)",
			entry: &CacheEntry{
				LatestVersion:  "v1.1.0",
				CurrentVersion: "v1.0.0",
				CheckedAt:      now,
				HasUpdate:      true,
			},
			currentVersion: "v0.9.0",
			want:           false,
		},
		{
			name: "boundary - exactly at TTL",
			entry: &CacheEntry{
				LatestVersion:  "v1.1.0",
				CurrentVersion: "v1.0.0",
				CheckedAt:      now.Add(-3*time.Hour + time.Minute), // just under TTL
				HasUpdate:      true,
			},
			currentVersion: "v1.0.0",
			want:           true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsCacheValid(tt.entry, tt.currentVersion)
			if got != tt.want {
				t.Errorf("IsCacheValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func withIsolatedConfig(t *testing.T) {
	t.Helper()
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(config.ResetTestConfigPath)
}

func TestSaveAndLoadCacheFile_RoundTrip(t *testing.T) {
	withIsolatedConfig(t)

	entry := &CacheEntry{
		LatestVersion:  "v1.2.0",
		CurrentVersion: "v1.0.0",
		CheckedAt:      time.Now().Truncate(time.Second),
		HasUpdate:      true,
	}
	if err := SaveCacheFile(TasksDescriptor().CacheFile, entry); err != nil {
		t.Fatalf("SaveCacheFile: %v", err)
	}

	got, err := LoadCacheFile(TasksDescriptor().CacheFile)
	if err != nil {
		t.Fatalf("LoadCacheFile: %v", err)
	}
	if got.LatestVersion != entry.LatestVersion || got.CurrentVersion != entry.CurrentVersion ||
		got.HasUpdate != entry.HasUpdate || !got.CheckedAt.Equal(entry.CheckedAt) {
		t.Errorf("round trip mismatch: got %+v, want %+v", got, entry)
	}
}

func TestLoadCacheFile_Missing(t *testing.T) {
	withIsolatedConfig(t)
	if _, err := LoadCacheFile(TdDescriptor().CacheFile); err == nil {
		t.Error("expected an error for a cache file that does not exist")
	}
}

// Each product caches to its own file, so similarly numbered releases cannot
// collide across products.
func TestCacheFilesAreDistinctPerProduct(t *testing.T) {
	withIsolatedConfig(t)

	seen := map[string]ProductID{}
	for _, d := range []Descriptor{SidecarDescriptor(), TdDescriptor(), TasksDescriptor()} {
		if other, dup := seen[d.CacheFile]; dup {
			t.Fatalf("%s and %s share cache file %q", d.Product, other, d.CacheFile)
		}
		seen[d.CacheFile] = d.Product
		if err := SaveCacheFile(d.CacheFile, &CacheEntry{
			LatestVersion: "v9." + string(d.Product), CurrentVersion: "v1.0.0", CheckedAt: time.Now(),
		}); err != nil {
			t.Fatalf("SaveCacheFile(%s): %v", d.Product, err)
		}
	}

	for _, d := range []Descriptor{SidecarDescriptor(), TdDescriptor(), TasksDescriptor()} {
		got, err := LoadCacheFile(d.CacheFile)
		if err != nil {
			t.Fatalf("LoadCacheFile(%s): %v", d.Product, err)
		}
		if want := "v9." + string(d.Product); got.LatestVersion != want {
			t.Errorf("%s cache holds %q, want %q", d.Product, got.LatestVersion, want)
		}
	}
}
