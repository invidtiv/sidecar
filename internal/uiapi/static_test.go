package uiapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticUIReplacedDirectoryUnderRunningServer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "build")
	writeBuild := func(text string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		for _, file := range []string{"index.html", "app.js"} {
			if err := os.WriteFile(filepath.Join(dir, file), []byte(text+file), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	writeBuild("old-")
	h := newHarness(t, func(o *Options) { o.UIDir = dir })
	r, b := h.browserDo(req{path: "/"})
	if r.StatusCode != 200 || string(b) != "old-index.html" {
		t.Fatalf("initial: %d %s", r.StatusCode, b)
	}
	// Keep the old inode around as well: serving it must not silently succeed.
	if err := os.Rename(dir, dir+".previous"); err != nil {
		t.Fatal(err)
	}
	r, _ = h.browserDo(req{path: "/"})
	if r.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing build: %d", r.StatusCode)
	}
	writeBuild("new-")
	for path, want := range map[string]string{"/": "new-index.html", "/s/host/shell": "new-index.html", "/app.js": "new-app.js"} {
		r, b = h.browserDo(req{path: path})
		if r.StatusCode != 200 || string(b) != want {
			t.Fatalf("replaced %s: %d %s", path, r.StatusCode, b)
		}
	}
}

func TestStaticUIReplacementStillRefusesSymlinkEscape(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "build")
	secret := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(secret, []byte("PRIVATE_OUTSIDE_ROOT"), 0600); err != nil {
		t.Fatal(err)
	}
	build := func() {
		t.Helper()
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("safe"), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(secret, filepath.Join(dir, "leak.txt")); err != nil {
			t.Fatal(err)
		}
	}
	build()
	h := newHarness(t, func(o *Options) { o.UIDir = dir })
	for i := 0; i < 2; i++ {
		r, b := h.browserDo(req{path: "/leak.txt"})
		if strings.Contains(string(b), "PRIVATE_OUTSIDE_ROOT") {
			t.Fatalf("symlink leaked: %d %s", r.StatusCode, b)
		}
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
		build()
	}
}
