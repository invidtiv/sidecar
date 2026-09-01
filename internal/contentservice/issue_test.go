package contentservice

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/shellstate"
)

func TestServiceIssueNoteIdentityResolveDoesNotLookup(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepathEval(root); err == nil {
		root = resolved
	}
	initGitRepo(t, root)
	id := canonical(root) + ":worktree:" + canonical(root)
	var issueLookups, noteLookups int
	svc := testService(t, root, nil, &gitRecorder{})
	svc.LookupIssue = func(context.Context, string, string, []issueview.ProjectRef) (*issueview.Data, *issueview.Owner, error) {
		issueLookups++
		return nil, nil, fmt.Errorf("should not lookup")
	}
	svc.LookupNote = func(context.Context, string, string) (*noteview.Data, error) {
		noteLookups++
		return nil, fmt.Errorf("should not lookup")
	}

	resolved, err := svc.Resolve(context.Background(), id, KindIssue, "td-a4dd72")
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.ValidRemoteResult() || resolved.Target != "td-a4dd72" || resolved.Kind != KindIssue {
		t.Fatalf("issue resolve = %+v", resolved)
	}
	if issueLookups != 0 {
		t.Fatal("issue resolve consulted td")
	}

	resolved, err = svc.Resolve(context.Background(), id, KindNote, "nt-host01")
	if err != nil {
		t.Fatal(err)
	}
	if !resolved.ValidRemoteResult() || resolved.Target != "nt-host01" || resolved.Kind != KindNote {
		t.Fatalf("note resolve = %+v", resolved)
	}
	if noteLookups != 0 {
		t.Fatal("note resolve consulted td")
	}
}

func TestServiceIssueReadRoundTripAndNotModified(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepathEval(root); err == nil {
		root = resolved
	}
	initGitRepo(t, root)
	id := canonical(root) + ":worktree:" + canonical(root)
	data := &issueview.Data{
		ID: "td-a4dd72", Title: "Host issue", Status: "open",
		Parent:   &issueview.Ref{ID: "td-parent", Title: "Epic"},
		Children: []issueview.Ref{{ID: "td-child", Title: "Child"}},
		Logs:     []issueview.Log{{Message: "hello"}},
	}
	owner := &issueview.Owner{Name: "other", Root: "/other"}
	var gotFallbacks []issueview.ProjectRef
	svc := testService(t, root, []shellstate.Definition{{TmuxName: "s1"}}, &gitRecorder{})
	svc.LookupIssue = func(_ context.Context, workDir, issueID string, fallbacks []issueview.ProjectRef) (*issueview.Data, *issueview.Owner, error) {
		gotFallbacks = fallbacks
		if issueID != "td-a4dd72" {
			t.Fatalf("id = %q", issueID)
		}
		return data, owner, nil
	}

	read, err := svc.Read(context.Background(), id, KindIssue, OpCard, "td-a4dd72", "")
	if err != nil {
		t.Fatal(err)
	}
	if !read.ValidRemoteResult() || read.Issue == nil || read.Issue.Title != "Host issue" {
		t.Fatalf("read = %+v", read)
	}
	if read.Issue.Parent == nil || read.Issue.Parent.ID != "td-parent" {
		t.Fatalf("parent dropped: %+v", read.Issue.Parent)
	}
	if len(read.Issue.Children) != 1 || read.Issue.Owner == nil || read.Issue.Owner.Name != "other" {
		t.Fatalf("graph/owner dropped: %+v", read.Issue)
	}
	if len(gotFallbacks) != 1 || gotFallbacks[0].Name != "demo" {
		t.Fatalf("host fallbacks = %+v, want the configured project", gotFallbacks)
	}

	cached, err := svc.Read(context.Background(), id, KindIssue, OpCard, "td-a4dd72", read.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !cached.NotModified || cached.Issue != nil || cached.Revision != read.Revision {
		t.Fatalf("notModified = %+v", cached)
	}
	if !cached.ValidRemoteResult() {
		t.Fatal("notModified failed ValidRemoteResult")
	}

	raw, err := EncodeReadResult(read)
	if err != nil {
		t.Fatal(err)
	}
	var viaJSON ReadResult
	if err := json.Unmarshal(raw, &viaJSON); err != nil {
		t.Fatal(err)
	}
	if !viaJSON.ValidRemoteResult() || viaJSON.Issue.Title != read.Issue.Title {
		t.Fatalf("JSON drifted: %+v", viaJSON)
	}
	back, backOwner := IssueFromDTO(viaJSON.Issue)
	if back == nil || back.Title != data.Title || back.Parent == nil || backOwner == nil || backOwner.Name != "other" {
		t.Fatalf("IssueFromDTO = %#v owner=%#v", back, backOwner)
	}
}

func TestServiceNoteReadRoundTripAndNotModified(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if resolved, err := filepathEval(root); err == nil {
		root = resolved
	}
	initGitRepo(t, root)
	id := canonical(root) + ":worktree:" + canonical(root)
	data := &noteview.Data{ID: "nt-host01", Title: "Host note", Content: "body"}
	svc := testService(t, root, nil, &gitRecorder{})
	svc.LookupNote = func(_ context.Context, _, noteID string) (*noteview.Data, error) {
		if noteID != "nt-host01" {
			t.Fatalf("id = %q", noteID)
		}
		return data, nil
	}

	read, err := svc.Read(context.Background(), id, KindNote, OpNote, "nt-host01", "")
	if err != nil {
		t.Fatal(err)
	}
	if !read.ValidRemoteResult() || read.Note == nil || read.Note.Title != "Host note" {
		t.Fatalf("read = %+v", read)
	}
	cached, err := svc.Read(context.Background(), id, KindNote, OpNote, "nt-host01", read.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if !cached.NotModified || cached.Note != nil {
		t.Fatalf("notModified = %+v", cached)
	}

	raw, err := EncodeReadResult(read)
	if err != nil {
		t.Fatal(err)
	}
	var viaJSON ReadResult
	if err := json.Unmarshal(raw, &viaJSON); err != nil {
		t.Fatal(err)
	}
	if !viaJSON.ValidRemoteResult() || viaJSON.Note.Content != "body" {
		t.Fatalf("JSON drifted: %+v", viaJSON)
	}
}

func TestValidRemoteResultIssueNoteRefuseALogLine(t *testing.T) {
	logLine := []byte(`{"level":"info","msg":"loading nvm","name":"nvm","path":"/usr/local/nvm"}`)
	var read ReadResult
	if err := json.Unmarshal(logLine, &read); err != nil {
		t.Fatal(err)
	}
	if read.ValidRemoteResult() {
		t.Fatalf("log line passed for issue/note read: %+v", read)
	}
	for _, body := range []string{
		`{"kind":"issue"}`,
		`{"kind":"issue","revision":"v1"}`,
		`{"kind":"note","operation":"note","revision":"v1"}`,
		`{"kind":"issue","notModified":true}`,
	} {
		var empty ReadResult
		if err := json.Unmarshal([]byte(body), &empty); err != nil {
			t.Fatal(err)
		}
		if empty.ValidRemoteResult() {
			t.Errorf("%s passed for read", body)
		}
	}

	okIssue := ReadResult{Kind: KindIssue, Operation: OpCard, Workspace: "p:shell:s1", Revision: "v1:abc", Issue: &IssueDTO{ID: "td-a4dd72", Title: "T"}}
	if !okIssue.ValidRemoteResult() {
		t.Fatalf("real issue refused: %+v", okIssue)
	}
	okNote := ReadResult{Kind: KindNote, Operation: OpNote, Workspace: "p:shell:s1", Revision: "v1:abc", Note: &NoteDTO{ID: "nt-host01"}}
	if !okNote.ValidRemoteResult() {
		t.Fatalf("real note refused: %+v", okNote)
	}
	okNM := ReadResult{Kind: KindIssue, NotModified: true, Revision: "v1:abc"}
	if !okNM.ValidRemoteResult() {
		t.Fatal("issue notModified refused")
	}
	okResolve := ResolveResult{Kind: KindIssue, Workspace: "p:shell:s1", Target: "td-a4dd72"}
	if !okResolve.ValidRemoteResult() {
		t.Fatalf("issue resolve refused: %+v", okResolve)
	}
}

func filepathEval(root string) (string, error) {
	return canonical(root), nil
}
