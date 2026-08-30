package notifydelivery

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

type assetTestCache struct {
	path  string
	calls int
}

func (c *assetTestCache) Materialize(Cue) (string, error) {
	c.calls++
	return c.path, nil
}

func TestConfiguredAssetCacheResolvesRelativePathsAndRejectsNonRegularTargets(t *testing.T) {
	dir := t.TempDir()
	custom := filepath.Join(dir, "sounds", "done.wav")
	if err := os.MkdirAll(filepath.Dir(custom), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(custom, []byte("custom"), 0o644); err != nil {
		t.Fatal(err)
	}
	builtIn := &assetTestCache{path: "/cache/done.wav"}
	paths := SoundPaths{ConfigPath: filepath.Join(dir, "config.json"), Done: filepath.Join("sounds", "done.wav")}
	cache := NewConfiguredAssetCache(builtIn, func() (SoundPaths, error) { return paths, nil })
	path, err := cache.Materialize(CueDone)
	resolvedCustom, resolveErr := filepath.EvalSymlinks(custom)
	if err != nil || resolveErr != nil || path != resolvedCustom || builtIn.calls != 0 {
		t.Fatalf("custom path=%q err=%v builtInCalls=%d", path, err, builtIn.calls)
	}

	directoryLink := filepath.Join(dir, "not-a-file.wav")
	if err := os.Symlink(filepath.Join(dir, "sounds"), directoryLink); err != nil {
		t.Fatal(err)
	}
	paths.Done = directoryLink
	path, err = cache.Materialize(CueDone)
	if err != nil || path != builtIn.path || builtIn.calls != 1 {
		t.Fatalf("non-regular fallback path=%q err=%v builtInCalls=%d", path, err, builtIn.calls)
	}
}

func TestConfiguredAssetCacheReadsLivePathsAndFallsBackAfterRuntimeFailure(t *testing.T) {
	dir := t.TempDir()
	one := filepath.Join(dir, "one.mp3")
	two := filepath.Join(dir, "two.mp3")
	for _, path := range []string{one, two} {
		if err := os.WriteFile(path, []byte("custom"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	builtIn := &assetTestCache{path: "/cache/attention.wav"}
	current := one
	cache := NewConfiguredAssetCache(builtIn, func() (SoundPaths, error) {
		return SoundPaths{ConfigPath: filepath.Join(dir, "config.json"), Attention: current}, nil
	})
	path, err := cache.Materialize(CueAttention)
	resolvedOne, _ := filepath.EvalSymlinks(one)
	if err != nil || path != resolvedOne {
		t.Fatalf("first path=%q err=%v", path, err)
	}
	current = two
	path, err = cache.Materialize(CueAttention)
	resolvedTwo, _ := filepath.EvalSymlinks(two)
	if err != nil || path != resolvedTwo {
		t.Fatalf("live path=%q err=%v", path, err)
	}
	fallback, ok, err := cache.Fallback(CueAttention, resolvedTwo, errors.New("fake player failure"))
	if err != nil || !ok || fallback != builtIn.path || builtIn.calls != 1 {
		t.Fatalf("fallback path=%q ok=%v err=%v builtInCalls=%d", fallback, ok, err, builtIn.calls)
	}
}
