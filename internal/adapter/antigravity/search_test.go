package antigravity

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/adapter"
)

func TestSearch(t *testing.T) {
	tempDir := t.TempDir()
	brainDir := filepath.Join(tempDir, "brain")
	sessionID := "test-search-session"
	logDir := filepath.Join(brainDir, sessionID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	transcript := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-08-06T12:00:00Z","content":"Search unique term foobar123"}
`
	if err := os.WriteFile(filepath.Join(logDir, "transcript.jsonl"), []byte(transcript), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	a := NewWithBrainDir(brainDir)
	_ = context.Background()
	results, err := a.SearchMessages(sessionID, "foobar123", adapter.SearchOptions{})
	if err != nil {
		t.Fatalf("SearchMessages error: %v", err)
	}
	if len(results) == 0 {
		t.Errorf("Expected search results for 'foobar123'")
	}
}
