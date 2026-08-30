package notifydelivery

import (
	"embed"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const assetVersion = "v1"

//go:embed assets/*.wav
var soundAssets embed.FS

type AssetCache interface {
	Materialize(Cue) (string, error)
}

// SoundPaths is the persisted custom-path portion of notification config. It
// stays transport-neutral so adapters do not need to know how config is stored.
type SoundPaths struct {
	ConfigPath string
	Attention  string
	Done       string
	Failure    string
}

type SoundPathSource func() (SoundPaths, error)

type assetSelection struct {
	Cue    Cue
	Path   string
	Format string
	Custom bool
	Err    error
}

// fallbackAssetCache is deliberately private: platform players may recover a
// failed custom invocation to the built-in WAV without widening AssetCache's
// simple contract for test fakes.
type fallbackAssetCache interface {
	Fallback(Cue, string, error) (string, bool, error)
	Selections() []assetSelection
}

// ConfiguredAssetCache selects a live custom file when configured and falls
// back to Sidecar's embedded cue if a hand-edited path becomes invalid. Saved
// paths are validated earlier by config; this second check protects runtime
// edits, symlink changes, and file removal between saves.
type ConfiguredAssetCache struct {
	fallback AssetCache
	source   SoundPathSource

	mu       sync.Mutex
	selected map[Cue]string
	reported map[string]struct{}
}

func NewConfiguredAssetCache(fallback AssetCache, source SoundPathSource) *ConfiguredAssetCache {
	return &ConfiguredAssetCache{
		fallback: fallback, source: source,
		selected: make(map[Cue]string), reported: make(map[string]struct{}),
	}
}

func (c *ConfiguredAssetCache) Materialize(cue Cue) (string, error) {
	selection := c.selection(cue)
	if selection.Custom && selection.Err == nil {
		c.mu.Lock()
		c.selected[cue] = selection.Path
		c.mu.Unlock()
		return selection.Path, nil
	}
	if selection.Err != nil {
		c.reportOnce(cue, "invalid", "custom sound is unavailable; using the built-in cue")
	}
	return c.builtIn(cue)
}

func (c *ConfiguredAssetCache) Fallback(cue Cue, attempted string, _ error) (string, bool, error) {
	c.mu.Lock()
	selected := c.selected[cue]
	delete(c.selected, cue)
	c.mu.Unlock()
	if selected == "" || selected != attempted {
		return "", false, nil
	}
	c.reportOnce(cue, "playback", "custom sound playback failed; using the built-in cue")
	path, err := c.builtIn(cue)
	return path, true, err
}

func (c *ConfiguredAssetCache) Selections() []assetSelection {
	if c == nil || c.source == nil {
		return []assetSelection{{Cue: CueAttention, Format: "wav"}, {Cue: CueDone, Format: "wav"}, {Cue: CueFailure, Format: "wav"}}
	}
	paths, err := c.source()
	if err != nil {
		return []assetSelection{{Cue: CueAttention, Format: "wav", Err: err}, {Cue: CueDone, Format: "wav", Err: err}, {Cue: CueFailure, Format: "wav", Err: err}}
	}
	return []assetSelection{selectionFromPaths(CueAttention, paths), selectionFromPaths(CueDone, paths), selectionFromPaths(CueFailure, paths)}
}

func (c *ConfiguredAssetCache) selection(cue Cue) assetSelection {
	selection := assetSelection{Cue: cue, Format: "wav"}
	if c == nil || c.source == nil {
		return selection
	}
	paths, err := c.source()
	if err != nil {
		selection.Err = err
		return selection
	}
	return selectionFromPaths(cue, paths)
}

func selectionFromPaths(cue Cue, paths SoundPaths) assetSelection {
	selection := assetSelection{Cue: cue, Format: "wav"}
	var raw string
	switch cue {
	case CueAttention:
		raw = paths.Attention
	case CueDone:
		raw = paths.Done
	case CueFailure:
		raw = paths.Failure
	default:
		selection.Err = fmt.Errorf("notifydelivery: unknown cue %q", cue)
		return selection
	}
	if strings.TrimSpace(raw) == "" {
		return selection
	}
	selection.Custom = true
	selection.Path, selection.Err = resolveCustomSoundPath(raw, paths.ConfigPath)
	if selection.Err == nil {
		selection.Format = strings.TrimPrefix(strings.ToLower(filepath.Ext(selection.Path)), ".")
	}
	return selection
}

func (c *ConfiguredAssetCache) builtIn(cue Cue) (string, error) {
	if c == nil || c.fallback == nil {
		return "", fmt.Errorf("notifydelivery: no built-in sound cache")
	}
	return c.fallback.Materialize(cue)
}

func (c *ConfiguredAssetCache) reportOnce(cue Cue, kind, message string) {
	key := string(cue) + ":" + kind
	c.mu.Lock()
	if _, exists := c.reported[key]; exists {
		c.mu.Unlock()
		return
	}
	c.reported[key] = struct{}{}
	c.mu.Unlock()
	slog.Warn("notifydelivery: "+message, "channel", ChannelSound, "cue", cue)
}

func resolveCustomSoundPath(raw, configPath string) (string, error) {
	path := strings.TrimSpace(raw)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		path = filepath.Join(home, strings.TrimPrefix(path, "~/"))
	}
	if !filepath.IsAbs(path) {
		if strings.TrimSpace(configPath) == "" {
			return "", fmt.Errorf("notifydelivery: relative custom sound has no config directory")
		}
		path = filepath.Join(filepath.Dir(configPath), path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", fmt.Errorf("notifydelivery: custom sound is not readable")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("notifydelivery: custom sound must resolve to a readable regular file")
	}
	f, err := os.Open(resolved)
	if err != nil {
		return "", fmt.Errorf("notifydelivery: custom sound is not readable")
	}
	openedInfo, statErr := f.Stat()
	if statErr != nil || !openedInfo.Mode().IsRegular() {
		_ = f.Close()
		return "", fmt.Errorf("notifydelivery: custom sound must resolve to a readable regular file")
	}
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("notifydelivery: custom sound is not readable")
	}
	return resolved, nil
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
