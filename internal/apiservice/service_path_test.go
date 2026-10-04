package apiservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The service runs tmux, git and agents through the PATH captured at install,
// for as long as it stays installed. An entry another local user can write to,
// or can create later, would let that user run code as the owner at the next
// service start, so install keeps only directories the owner already controls.
func TestServicePathDropsEntriesAnotherUserCouldPlant(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "good")
	shared := filepath.Join(root, "shared")
	missing := filepath.Join(root, "missing-shims")
	file := filepath.Join(root, "file")
	for _, dir := range []string{good, shared} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(shared, 0o777); err != nil { //nolint:gosec // the world-writable directory under test
		t.Fatal(err)
	}
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	raw := strings.Join([]string{"", "relative/bin", ".", shared, missing, file, good, good, "/usr/bin"}, string(os.PathListSeparator))
	for _, platform := range []string{"darwin", "linux"} {
		t.Run(platform, func(t *testing.T) {
			m, err := New(Options{OS: platform, Home: root, StateDir: filepath.Join(root, "state", "sidecar"), ConfigPath: filepath.Join(root, "config.json"),
				Executable: filepath.Join(root, "sidecar"), UID: os.Getuid(), Path: raw, Run: (&fakeRunner{}).run})
			if err != nil {
				t.Fatal(err)
			}
			want := good + string(os.PathListSeparator) + "/usr/bin"
			if m.options.Path != want {
				t.Fatalf("service PATH = %q, want %q", m.options.Path, want)
			}
			data := string(m.definition())
			for _, unsafe := range []string{shared, missing, "relative/bin"} {
				if strings.Contains(data, unsafe) {
					t.Fatalf("definition kept %q:\n%s", unsafe, data)
				}
			}
		})
	}
}

func TestServicePathFallsBackToSystemDirectories(t *testing.T) {
	if got := ServicePath("relative:"+filepath.Join(t.TempDir(), "gone"), os.Getuid()); got != defaultServicePath {
		t.Fatalf("empty sanitized PATH = %q, want %q", got, defaultServicePath)
	}
}
