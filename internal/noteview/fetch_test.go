package noteview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadNoteParsesShowJSON(t *testing.T) {
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		`printf '{"id":"nt-abc123","title":"Release","content":"Ship it","pinned":true,"archived":false,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z"}\n'` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	data, err := loadNote(t.TempDir(), "nt-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if data.ID != "nt-abc123" || data.Title != "Release" || data.Content != "Ship it" || !data.Pinned {
		t.Fatalf("note = %#v", data)
	}
}

func TestLoadNoteDisablesSyncAndAnalytics(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "td-env.log")
	script := "#!/bin/sh\n" +
		`printf '%s|%s|%s\n' "$1" "$TD_SYNC_AUTO_START" "$TD_ANALYTICS" >> "$NOTEVIEW_ENV_LOG"` + "\n" +
		`printf '{"id":"nt-abc123","title":"T","content":"c"}\n'` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "td"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TD_SYNC_AUTO_START", "1")
	t.Setenv("TD_ANALYTICS", "true")
	t.Setenv("NOTEVIEW_ENV_LOG", logPath)

	if _, err := loadNote(t.TempDir(), "nt-abc123"); err != nil {
		t.Fatal(err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(logged))
	if !strings.Contains(line, "|0|false") {
		t.Fatalf("td env = %q, want TD_SYNC_AUTO_START=0 TD_ANALYTICS=false", line)
	}
}

func TestNormalizeID(t *testing.T) {
	if got := NormalizeID(" nt-abc123 "); got != "nt-abc123" {
		t.Fatalf("NormalizeID = %q", got)
	}
	if got := NormalizeID("td-abc123"); got != "" {
		t.Fatalf("issue id accepted: %q", got)
	}
}
