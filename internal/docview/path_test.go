package docview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
