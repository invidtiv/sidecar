package version

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

// The release-check cache persists the offered release's notes, so a cache
// hit still carries them — no network call needed to show release notes.
func TestCheckProduct_CacheHitCarriesNotes(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write([]byte(`{"tag_name":"v1.6.0","body":"fresh body","html_url":"https://example.invalid/r"}`))
	}))
	t.Setenv(apiBaseEnv, srv.URL)
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(func() {
		srv.Close()
		config.ResetTestConfigPath()
	})

	fake := newFakeEnv()
	for _, bin := range TasksDescriptor().SuiteBinaries {
		path := "/opt/homebrew/Cellar/tasks/1.5.0/bin/" + bin.Name
		fake.paths[bin.Name] = path
		fake.outputs[path+" "+strings.Join(bin.VersionArgs, " ")] = bin.Name + " 1.5.0"
	}
	fake.outputs["brew --cellar marcus/tap/tasks"] = "/opt/homebrew/Cellar/tasks\n"

	d := TasksDescriptor()

	first := checkProduct(context.Background(), fake.env(), d, "", false)
	if first.Target.Notes != "fresh body" {
		t.Fatalf("fresh check should carry the release body, got %q", first.Target.Notes)
	}

	second := checkProduct(context.Background(), fake.env(), d, "", false)
	if hits != 1 {
		t.Fatalf("the second check must come from cache, API hits = %d", hits)
	}
	if second.Target.Notes != "fresh body" {
		t.Errorf("cache-hit path dropped the persisted notes: %q", second.Target.Notes)
	}
}

func TestCacheEntry_NotesRoundTrip(t *testing.T) {
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(config.ResetTestConfigPath)

	entry := &CacheEntry{
		LatestVersion:  "v2.0.0",
		CurrentVersion: "1.9.0",
		CheckedAt:      time.Now(),
		HasUpdate:      true,
		Notes:          "## What's New\n\n- something",
	}
	if err := SaveCacheFile(tdCacheFile, entry); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := LoadCacheFile(tdCacheFile)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Notes != entry.Notes {
		t.Errorf("notes round-trip mismatch: %q != %q", got.Notes, entry.Notes)
	}
}

// The tag is a git ref, so it goes into the raw path exactly as the release
// published it — releases here are tagged "v1.2.3", and asking for "1.2.3"
// is a ref that does not exist.
func TestChangelogURL_IsTagPinnedAndOverridable(t *testing.T) {
	t.Setenv(changelogRawBaseEnv, "")
	if got := ChangelogURL("marcus", "sidecar", "v9.9.9"); got != defaultRawBase+"/marcus/sidecar/v9.9.9/CHANGELOG.md" {
		t.Errorf("default URL = %q", got)
	}
	t.Setenv(changelogRawBaseEnv, "http://127.0.0.1:8765/raw")
	if got := ChangelogURL("marcus", "td", "v1.1.0"); got != "http://127.0.0.1:8765/raw/marcus/td/v1.1.0/CHANGELOG.md" {
		t.Errorf("override URL = %q", got)
	}
	// An unprefixed tag is equally verbatim.
	if got := ChangelogURL("marcus", "td", "1.1.0"); got != "http://127.0.0.1:8765/raw/marcus/td/1.1.0/CHANGELOG.md" {
		t.Errorf("unprefixed tag URL = %q", got)
	}
}

func TestFetchChangelogCmd_CachesAndPinsTag(t *testing.T) {
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		switch {
		case strings.Contains(r.URL.Path, "/v9.9.9/"):
			_, _ = w.Write([]byte("# Changelog v9.9.9\n\n- released entries only"))
		case strings.Contains(r.URL.Path, "/main/"):
			_, _ = w.Write([]byte("# main is ahead"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Setenv(changelogRawBaseEnv, srv.URL)
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(func() {
		srv.Close()
		config.ResetTestConfigPath()
	})

	msg := FetchChangelogCmd("marcus", "sidecar", "v9.9.9")().(ChangelogMsg)
	if msg.Err != nil {
		t.Fatalf("fetch failed: %v", msg.Err)
	}
	if !strings.Contains(msg.Body, "released entries only") {
		t.Errorf("body = %q", msg.Body)
	}
	for _, p := range seen {
		if strings.Contains(p, "/main/") {
			t.Errorf("fetched the default branch instead of the offered tag: %v", seen)
		}
	}

	// Second fetch answers from disk; the network sees nothing new.
	before := len(seen)
	cached := FetchChangelogCmd("marcus", "sidecar", "v9.9.9")().(ChangelogMsg)
	if cached.Err != nil || cached.Body == "" {
		t.Fatalf("cached fetch failed: %+v", cached)
	}
	if len(seen) != before {
		t.Errorf("a tag's changelog is immutable: the cache should have answered")
	}
}

func TestFetchChangelogCmd_ErrorSurfacesForRetry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Setenv(changelogRawBaseEnv, srv.URL)
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(func() {
		srv.Close()
		config.ResetTestConfigPath()
	})

	msg := FetchChangelogCmd("marcus", "tasks", "v0.9.0")().(ChangelogMsg)
	if msg.Err == nil {
		t.Fatal("expected an error a retry button can act on")
	}
	if _, ok := LoadChangelogCache("tasks", "v0.9.0"); ok {
		t.Error("a failed fetch must not poison the changelog cache")
	}
	_ = os.Unsetenv(changelogRawBaseEnv)
}
