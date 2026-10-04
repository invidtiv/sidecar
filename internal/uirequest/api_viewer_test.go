package uirequest

import (
	"os"
	"testing"
	"time"
)

func TestAPIViewerPinsOnlyLiveCapableScreen(t *testing.T) {
	dir := t.TempDir()
	now := time.Now()
	record := APIViewer{Instance: "api-viewer-example", PID: os.Getpid(), Focused: true, Capabilities: []string{APIViewerRelay}, ExpiresAt: now.Add(time.Minute)}
	if err := WriteAPIViewer(dir, record); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadAPIViewer(dir, now)
	if !ok || got.Instance != record.Instance {
		t.Fatalf("live screen: %+v %v", got, ok)
	}
	request := Request{ID: NewRequestID(), Action: ActionOpen, Target: Target{Kind: TargetKindFile, Value: "doc.md"}}
	path, err := WriteRequest(dir, request)
	if err != nil {
		t.Fatal(err)
	}
	posted, err := ReadRequest(path)
	if err != nil || posted.Viewer != record.Instance {
		t.Fatalf("request not pinned: %+v %v", posted, err)
	}
	record.ExpiresAt = now.Add(-time.Second)
	if err := WriteAPIViewer(dir, record); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadAPIViewer(dir, now); ok {
		t.Fatal("expired viewer is live")
	}
	request.ID = NewRequestID()
	path, err = WriteRequest(dir, request)
	if err != nil {
		t.Fatal(err)
	}
	posted, err = ReadRequest(path)
	if err != nil || posted.Viewer != "" {
		t.Fatalf("expired screen stole request: %+v %v", posted, err)
	}
	record.ExpiresAt = now.Add(time.Minute)
	record.Capabilities = nil
	if err := WriteAPIViewer(dir, record); err != nil {
		t.Fatal(err)
	}
	request.ID = NewRequestID()
	path, err = WriteRequest(dir, request)
	if err != nil {
		t.Fatal(err)
	}
	posted, err = ReadRequest(path)
	if err != nil || posted.Viewer != "" {
		t.Fatalf("incapable screen stole request: %+v %v", posted, err)
	}
}
