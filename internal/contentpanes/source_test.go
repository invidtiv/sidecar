package contentpanes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestLocalSourceRefusesRelativeEscape(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	root := filepath.Join(parent, "proj")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, "secret.txt"), []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LocalSource{}.Resolve(context.Background(), SourceContext{Root: root}, contentlink.Pending{Kind: contentlink.KindFile, Raw: "../secret.txt"})
	if err == nil {
		t.Fatal("relative traversal resolved")
	}
}

func TestLocalSourceLoadDocumentMatchesFilepreview(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const rel = "note.md"
	body := "# hello from disk\nsecond line\n"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := LocalSource{}.LoadDocument(context.Background(), SourceContext{Root: dir}, DocumentReadRequest{
		Ref: contentlink.Ref{Kind: contentlink.KindFile, Value: rel},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.NotModified {
		t.Fatal("local adapter returned NotModified")
	}
	msg, ok := filepreview.LoadPreview(dir, rel, 0)().(filepreview.PreviewLoadedMsg)
	if !ok {
		t.Fatal("filepreview.LoadPreview did not return PreviewLoadedMsg")
	}
	if got.Value.Content != msg.Result.Content {
		t.Fatalf("local source content %q != filepreview %q", got.Value.Content, msg.Result.Content)
	}
	if got.Value.IsTruncated != msg.Result.IsTruncated || got.Value.IsBinary != msg.Result.IsBinary {
		t.Fatalf("local source flags truncated=%v binary=%v, filepreview truncated=%v binary=%v",
			got.Value.IsTruncated, got.Value.IsBinary, msg.Result.IsTruncated, msg.Result.IsBinary)
	}
	if got.Revision == "" {
		t.Fatal("local source omitted revision")
	}

	again, err := LocalSource{}.LoadDocument(context.Background(), SourceContext{Root: dir}, DocumentReadRequest{
		Ref: contentlink.Ref{Kind: contentlink.KindFile, Value: rel}, IfRevision: got.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !again.NotModified || again.Revision != got.Revision {
		t.Fatalf("conditional local load = %+v", again)
	}
}

type fakeDocumentSource struct {
	body        string
	revision    string
	notModified bool
	loads       int
	lastIfRev   string
}

func (f *fakeDocumentSource) Resolve(_ context.Context, _ SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	return contentlink.Ref{Kind: contentlink.KindFile, Value: pending.Raw}, nil
}

func (f *fakeDocumentSource) LoadDocument(_ context.Context, _ SourceContext, req DocumentReadRequest) (DocumentReadResult, error) {
	f.loads++
	f.lastIfRev = req.IfRevision
	rev := f.revision
	if rev == "" {
		rev = "1"
	}
	if f.notModified {
		return DocumentReadResult{NotModified: true, Revision: rev}, nil
	}
	return DocumentReadResult{
		Value:    filepreview.PreviewResult{Content: f.body, Lines: strings.Split(f.body, "\n")},
		Revision: rev,
	}, nil
}

func (f *fakeDocumentSource) LoadIssue(context.Context, SourceContext, IssueReadRequest) (IssueReadResult, error) {
	return IssueReadResult{}, fmt.Errorf("fake document source does not load issues")
}

func (f *fakeDocumentSource) LoadNote(context.Context, SourceContext, NoteReadRequest) (NoteReadResult, error) {
	return NoteReadResult{}, fmt.Errorf("fake document source does not load notes")
}

func TestDocumentSourceSuppliesContentWithoutTouchingPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fake := &fakeDocumentSource{body: "FAKE-BODY\n"}
	ctx := testContext(dir)
	d := New(ctx, Config{Source: fake})
	out := d.Open(ctx, fileRef("missing.md"), testPlacement())
	if out.Command == nil {
		t.Fatal("open did not start a load")
	}
	result, ok := out.Command().(Result)
	if !ok {
		t.Fatalf("load returned %T", out.Command())
	}
	if _, applied := d.Apply(result); !applied {
		t.Fatal("apply failed")
	}
	view := d.Viewer(out.LeafID).(*docview.Model)
	view.SetSize(40, 8)
	if !strings.Contains(view.View(), "FAKE-BODY") {
		t.Fatalf("view = %q, want fake body", view.View())
	}
	if fake.loads != 1 {
		t.Fatalf("loads = %d, want 1", fake.loads)
	}
	if _, err := os.Stat(filepath.Join(dir, "missing.md")); !os.IsNotExist(err) {
		t.Fatalf("fake source touched %s: %v", filepath.Join(dir, "missing.md"), err)
	}
}

func TestDocumentNotModifiedRefreshDoesNotReplaceContent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fake := &fakeDocumentSource{body: "FAKE-BODY\n"}
	ctx := testContext(dir)
	d := New(ctx, Config{Source: fake})
	out := d.Open(ctx, fileRef("doc.md"), testPlacement())
	if _, applied := d.Apply(out.Command().(Result)); !applied {
		t.Fatal("initial apply failed")
	}
	view := d.Viewer(out.LeafID).(*docview.Model)
	view.SetSize(40, 8)

	fake.notModified = true
	view.Observe()
	cmd := view.Refresh(false)
	if cmd == nil {
		t.Fatal("Refresh() = nil after Observe")
	}
	msg, ok := cmd().(docview.LoadedMsg)
	if !ok {
		t.Fatalf("refresh returned %T", cmd())
	}
	if !msg.NotModified || !msg.Refresh {
		t.Fatalf("refresh msg = %#v", msg)
	}
	if view.SetResult(msg) {
		t.Fatal("NotModified refresh replaced content")
	}
	if !strings.Contains(view.View(), "FAKE-BODY") {
		t.Fatalf("content was dropped: %q", view.View())
	}
}

func TestDocumentRefreshSendsLastAdoptedRevision(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	fake := &fakeDocumentSource{body: "FAKE-BODY\n", revision: "rev-1"}
	ctx := testContext(dir)
	d := New(ctx, Config{Source: fake})
	out := d.Open(ctx, fileRef("doc.md"), testPlacement())
	if _, applied := d.Apply(out.Command().(Result)); !applied {
		t.Fatal("initial apply failed")
	}
	if fake.lastIfRev != "" {
		t.Fatalf("first load IfRevision = %q, want empty", fake.lastIfRev)
	}
	view := d.Viewer(out.LeafID).(*docview.Model)
	view.Observe()
	cmd := view.Refresh(false)
	if cmd == nil {
		t.Fatal("Refresh() = nil after Observe")
	}
	_ = cmd()
	if fake.lastIfRev != "rev-1" {
		t.Fatalf("refresh IfRevision = %q, want rev-1", fake.lastIfRev)
	}
}

func TestSameContextTreatsHostIdentityAsIdentity(t *testing.T) {
	t.Parallel()
	a := testContext("/tmp/proj")
	a.Source = SourceContext{WorkspaceID: "shell:a", WorkspaceKind: workspaceinventory.KindShell}
	if !sameContext(a, a) {
		t.Fatal("identical contexts differ")
	}
	mutations := []struct {
		name string
		fn   func(*SurfaceContext)
	}{
		{"HostID", func(c *SurfaceContext) { c.Source.HostID = "mini" }},
		{"WorkspaceID", func(c *SurfaceContext) { c.Source.WorkspaceID = "shell:b" }},
		{"HostIncarnation", func(c *SurfaceContext) { c.Source.HostIncarnation = 7 }},
		{"Epoch", func(c *SurfaceContext) { c.Epoch++ }},
		{"Surface", func(c *SurfaceContext) { c.Surface = "other" }},
	}
	for _, tc := range mutations {
		t.Run(tc.name, func(t *testing.T) {
			b := a
			tc.fn(&b)
			if sameContext(a, b) {
				t.Fatalf("sameContext ignored %s: %#v", tc.name, b)
			}
		})
	}
}

func TestEncodeOmitsHostIncarnation(t *testing.T) {
	t.Parallel()
	ctx := testContext(t.TempDir())
	ctx.Source = SourceContext{
		HostID: "mini", HostIncarnation: 424242, WorkspaceID: "api:shell:s1",
		ProjectKey: "/home/me/api", Root: ctx.Root,
	}
	d := New(ctx, Config{})
	state := d.Encode()
	if state.Source == nil {
		t.Fatal("Encode dropped source identity")
	}
	if state.Source.HostID != "mini" || state.Source.WorkspaceID != "api:shell:s1" {
		t.Fatalf("encoded source = %#v", state.Source)
	}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Source map[string]any `json:"source"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Source == nil {
		t.Fatal("decoded source is nil")
	}
	if _, ok := decoded.Source["hostIncarnation"]; ok {
		t.Fatalf("encoded HostIncarnation: %s", raw)
	}
	if decoded.Source["hostId"] != "mini" {
		t.Fatalf("missing hostId: %s", raw)
	}
}
