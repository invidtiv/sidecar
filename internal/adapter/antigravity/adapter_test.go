package antigravity

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAdapterIDAndName(t *testing.T) {
	a := New()
	if a.ID() != "antigravity" {
		t.Errorf("ID() = %q, want 'antigravity'", a.ID())
	}
	if a.Name() != "Antigravity" {
		t.Errorf("Name() = %q, want 'Antigravity'", a.Name())
	}
	if a.Icon() != "★" {
		t.Errorf("Icon() = %q, want '★'", a.Icon())
	}
}

func TestDetectAndSessions(t *testing.T) {
	tempDir := t.TempDir()
	brainDir := filepath.Join(tempDir, "brain")
	sessionID := "test-session-12345678"
	logDir := filepath.Join(brainDir, sessionID, ".system_generated", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	transcript := `{"step_index":0,"source":"USER_EXPLICIT","type":"USER_INPUT","created_at":"2026-08-06T12:00:00Z","content":"<USER_REQUEST>\nFix bug in parser\n</USER_REQUEST>"}
{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","created_at":"2026-08-06T12:01:00Z","content":"I fixed the bug.","tool_calls":[{"name":"replace_file_content"}]}
`
	logPath := filepath.Join(logDir, "transcript.jsonl")
	if err := os.WriteFile(logPath, []byte(transcript), 0644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	adapter := NewWithBrainDir(brainDir)

	detected, err := adapter.Detect("/dummy/project")
	if err != nil {
		t.Fatalf("Detect error: %v", err)
	}
	if !detected {
		t.Errorf("Detect() = false, want true")
	}

	sessions, err := adapter.Sessions("/dummy/project")
	if err != nil {
		t.Fatalf("Sessions error: %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("Sessions count = %d, want 1", len(sessions))
	}

	s := sessions[0]
	if s.ID != sessionID {
		t.Errorf("Session ID = %q, want %q", s.ID, sessionID)
	}
	if s.Name != "Fix bug in parser" {
		t.Errorf("Session Name = %q, want 'Fix bug in parser'", s.Name)
	}
	if s.AdapterID != "antigravity" {
		t.Errorf("AdapterID = %q, want 'antigravity'", s.AdapterID)
	}

	messages, err := adapter.Messages(sessionID)
	if err != nil {
		t.Fatalf("Messages error: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("Messages count = %d, want 2", len(messages))
	}
	if messages[0].Role != "user" || messages[0].Content != "Fix bug in parser" {
		t.Errorf("Message 0 = %+v", messages[0])
	}
	if messages[1].Role != "assistant" || messages[1].Content != "I fixed the bug." {
		t.Errorf("Message 1 = %+v", messages[1])
	}
	if len(messages[1].ContentBlocks) != 1 || messages[1].ContentBlocks[0].ToolName != "replace_file_content" {
		t.Errorf("Tool block = %+v", messages[1].ContentBlocks)
	}
}
