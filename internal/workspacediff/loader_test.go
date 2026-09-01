package workspacediff

import (
	"context"
	"testing"
)

type stubLoader struct {
	snapshots int
	lastIfRev string
	snap      *Snapshot
}

func (s *stubLoader) LoadSnapshot(_ context.Context, _, _, ifRevision string) (SnapshotResult, error) {
	s.snapshots++
	s.lastIfRev = ifRevision
	return SnapshotResult{Snapshot: s.snap, Revision: "v1:1"}, nil
}
func (s *stubLoader) LoadCommitDetail(context.Context, string, string, string) (CommitResult, error) {
	return CommitResult{}, nil
}
func (s *stubLoader) LoadRange(context.Context, string, Target, string) (RangeResult, error) {
	return RangeResult{}, nil
}
func (s *stubLoader) LoadCommitFile(context.Context, string, string, string, string, string) (FileResult, error) {
	return FileResult{}, nil
}
func (s *stubLoader) LoadWorkingTreeFile(context.Context, string, string, string) (FileResult, error) {
	return FileResult{}, nil
}

func TestViewLoadSnapshotCmdUsesInjectedLoader(t *testing.T) {
	raw := "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -0,0 +1,1 @@\n+hello\n"
	stub := &stubLoader{snap: &Snapshot{State: LoadStateReady, WorkingTree: raw}}
	v := &View{Target: WorkingTreeTarget(), Loader: stub}
	v.Bind("/not-a-repo", "ws", 1)
	cmd := v.LoadSnapshotCmd("", false)
	if cmd == nil {
		t.Fatal("LoadSnapshotCmd = nil")
	}
	msg, ok := cmd().(SnapshotMsg)
	if !ok {
		t.Fatalf("got %T", cmd())
	}
	if stub.snapshots != 1 {
		t.Fatalf("loader calls = %d", stub.snapshots)
	}
	if msg.Snapshot == nil || msg.Snapshot.WorkingTree != raw {
		t.Fatalf("snapshot = %+v", msg.Snapshot)
	}
	if _, err := LoadSnapshot(context.Background(), "/not-a-repo", ""); err == nil {
		t.Fatal("local git unexpectedly succeeded; the test would not prove the loader was used")
	}

	v.Revision = "v1:1"
	v.State = LoadStateReady
	v.Observe()
	cmd = v.Refresh("/not-a-repo", "", "ws", false)
	if cmd == nil {
		t.Fatal("Refresh = nil")
	}
	_ = cmd()
	if stub.lastIfRev != "v1:1" {
		t.Fatalf("refresh IfRevision = %q", stub.lastIfRev)
	}
}
