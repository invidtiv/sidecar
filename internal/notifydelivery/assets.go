package notifydelivery

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const assetVersion = "v1"

//go:embed assets/*.wav
var soundAssets embed.FS

type AssetCache interface {
	Materialize(Cue) (string, error)
}

// EmbeddedAssetCache lazily materializes immutable embedded WAVs. Versioned
// filenames make an upgrade additive; atomic rename keeps concurrent Sidecar
// processes from observing a partial player input.
type EmbeddedAssetCache struct {
	Root string
	mu   sync.Mutex
}

func NewEmbeddedAssetCache(root string) *EmbeddedAssetCache {
	return &EmbeddedAssetCache{Root: root}
}

func (c *EmbeddedAssetCache) Materialize(cue Cue) (string, error) {
	name := string(cue) + ".wav"
	switch cue {
	case CueAttention, CueDone, CueFailure:
	default:
		return "", fmt.Errorf("notifydelivery: unknown cue %q", cue)
	}
	data, err := soundAssets.ReadFile("assets/" + name)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	root := c.Root
	if root == "" {
		root, err = defaultCacheRoot(os.Getenv, os.UserCacheDir)
		if err != nil {
			return "", err
		}
	}
	dir := filepath.Join(root, "sidecar", "notification-sounds", assetVersion)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	target := filepath.Join(dir, name)
	if info, statErr := os.Stat(target); statErr == nil && info.Mode().IsRegular() && info.Size() == int64(len(data)) {
		return target, nil
	}
	tmp, err := os.CreateTemp(dir, "."+name+".*.tmp")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if err := tmp.Chmod(0o644); err != nil {
		cleanup()
		return "", err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return "", err
	}
	if err := os.Rename(tmpName, target); err != nil {
		_ = os.Remove(tmpName)
		if info, statErr := os.Stat(target); statErr == nil && info.Mode().IsRegular() && info.Size() == int64(len(data)) {
			return target, nil
		}
		return "", err
	}
	return target, nil
}

func defaultCacheRoot(getenv func(string) string, userCacheDir func() (string, error)) (string, error) {
	if getenv != nil {
		if xdg := getenv("XDG_CACHE_HOME"); xdg != "" {
			return xdg, nil
		}
	}
	return userCacheDir()
}
