package docview

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRevealWaitsForTheHelperAndReportsItsFailure(t *testing.T) {
	wantErr := errors.New("exit status 7")
	called := false
	err := reveal("/tmp/missing.md", "darwin", func(name string, args ...string) ([]byte, error) {
		called = true
		if name != "open" || len(args) != 2 || args[0] != "-R" || args[1] != "/tmp/missing.md" {
			t.Fatalf("command = %q %#v", name, args)
		}
		return []byte("The file does not exist"), wantErr
	})
	if !called {
		t.Fatal("reveal helper was not run")
	}
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "The file does not exist") {
		t.Fatalf("reveal error = %v, want helper exit and output", err)
	}
}

func TestRevealReportsAnUnsupportedPlatformWithoutRunningAHelper(t *testing.T) {
	err := reveal("/tmp/file", "plan9", func(string, ...string) ([]byte, error) {
		t.Fatal("unsupported platform ran a helper")
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "plan9") {
		t.Fatalf("reveal error = %v, want unsupported platform", err)
	}
}

func TestRevealPreservesAnAbsolutePathOutsideTheWorkspace(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := revealPath(t.TempDir(), outside, "darwin", func(name string, args ...string) ([]byte, error) {
		if name != "open" || len(args) != 2 || args[1] != outside {
			t.Fatalf("command = %q %#v, want absolute path %q", name, args, outside)
		}
		return nil, nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInspectReadsFile(t *testing.T) {
	dir := t.TempDir()
	rel := "docs/note.md"
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := Inspect(dir, rel)
	if d.Err != nil || d.Name != "note.md" || d.Kind != "MD File" || d.Where != "docs" || d.IsDir {
		t.Fatalf("Inspect = %#v", d)
	}
	if !strings.Contains(RenderInfo(d, "Clean", "abc"), "MD File") {
		t.Fatalf("RenderInfo missing kind: %q", RenderInfo(d, "Clean", "abc"))
	}
}

func TestInspectEmptyPath(t *testing.T) {
	d := Inspect(t.TempDir(), "")
	if d.Path != "" || d.Err != nil {
		t.Fatalf("empty Inspect = %#v", d)
	}
	if got := RenderInfo(d, "", ""); !strings.Contains(got, "No file selected") {
		t.Fatalf("empty RenderInfo = %q", got)
	}
}

func TestInspectReadsAnAbsolutePathOutsideTheWorkspace(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	d := Inspect(t.TempDir(), outside)
	if d.Err != nil || d.Name != "outside.md" || d.Path != outside {
		t.Fatalf("Inspect absolute path = %#v", d)
	}
}

func TestYankPathEmptyIsNil(t *testing.T) {
	if YankPath("") != nil || YankContents(t.TempDir(), "") != nil {
		t.Fatal("empty yank should be a no-op")
	}
}

func TestFetchGitInfoEmptyPath(t *testing.T) {
	msg, ok := FetchGitInfo(t.TempDir(), "")().(GitInfoMsg)
	if !ok || msg.Status != "" || msg.LastCommit != "" {
		t.Fatalf("empty git info = %#v", msg)
	}
}

func TestFormatSize(t *testing.T) {
	if got := FormatSize(512); got != "512B" {
		t.Fatalf("FormatSize(512) = %q", got)
	}
	if got := FormatSize(1536); got != "1.5KB" {
		t.Fatalf("FormatSize(1536) = %q", got)
	}
}
