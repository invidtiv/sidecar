package filebrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/resource"
)

const remoteBody = "REMOTE-BYTES"

// fakeContentSource answers document reads without ssh, recording what it was
// asked so a test can prove the conditional read is really conditional.
type fakeContentSource struct {
	body        string
	revision    string
	notModified bool
	seen        []contentpanes.DocumentReadRequest
	ctxSeen     []contentpanes.SourceContext
}

func (f *fakeContentSource) Resolve(_ context.Context, _ contentpanes.SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	return contentlink.Ref{Kind: pending.Kind, Value: pending.Raw}, nil
}

func (f *fakeContentSource) LoadDocument(_ context.Context, src contentpanes.SourceContext, req contentpanes.DocumentReadRequest) (contentpanes.DocumentReadResult, error) {
	f.seen = append(f.seen, req)
	f.ctxSeen = append(f.ctxSeen, src)
	if f.notModified {
		return contentpanes.DocumentReadResult{NotModified: true, Revision: f.revision}, nil
	}
	return contentpanes.DocumentReadResult{
		Value:    filepreview.PreviewResult{Content: f.body, Lines: strings.Split(f.body, "\n")},
		Revision: f.revision,
	}, nil
}

func (f *fakeContentSource) LoadIssue(context.Context, contentpanes.SourceContext, contentpanes.IssueReadRequest) (contentpanes.IssueReadResult, error) {
	return contentpanes.IssueReadResult{}, fmt.Errorf("not used")
}
func (f *fakeContentSource) LoadNote(context.Context, contentpanes.SourceContext, contentpanes.NoteReadRequest) (contentpanes.NoteReadResult, error) {
	return contentpanes.NoteReadResult{}, fmt.Errorf("not used")
}
func (f *fakeContentSource) LoadDiff(context.Context, contentpanes.SourceContext, contentpanes.DiffReadRequest) (contentpanes.DiffReadResult, error) {
	return contentpanes.DiffReadResult{}, fmt.Errorf("not used")
}
func (f *fakeContentSource) Describe(context.Context, string) (contentservice.DescribeResult, error) {
	return contentservice.DescribeResult{}, nil
}
func (f *fakeContentSource) ResolveResource(context.Context, contentpanes.SourceContext, resource.Reference, bool) (resource.Document, error) {
	return resource.Document{}, fmt.Errorf("not used")
}

func boundFilesWithContent(t *testing.T) (*Plugin, *fakeContentSource) {
	t.Helper()
	p, _ := boundFilesPlugin(t, &recordingTreeSource{dirs: hostFixtureDirs()})
	src := &fakeContentSource{body: remoteBody, revision: "v1:abc"}
	p.contentSourceOverride = src
	applyBuild(t, p, p.Start())
	return p, src
}

func TestBoundPreviewReadsTheHostNotTheLocalTwin(t *testing.T) {
	p, src := boundFilesWithContent(t)

	cmd := p.loadPreview(remoteMarker)
	if cmd == nil {
		t.Fatal("a bound preview produced no read")
	}
	loaded, ok := cmd().(remotePreviewLoadedMsg)
	if !ok {
		t.Fatalf("read produced %T", cmd())
	}
	if loaded.Msg.Result.Content != remoteBody {
		t.Fatalf("preview content = %q, want the host's bytes", loaded.Msg.Result.Content)
	}
	if len(src.seen) != 1 || src.seen[0].Ref.Value != remoteMarker {
		t.Fatalf("host was asked for %+v", src.seen)
	}
	// The workspace id is what the host re-resolves on every request, so it
	// must be the durable identity rather than a path this viewer composed.
	if got := src.ctxSeen[0].WorkspaceID; got != "/home/me/sidecar:worktree:/home/me/sidecar" {
		t.Fatalf("source context WorkspaceID = %q", got)
	}
	if src.ctxSeen[0].HostID != "aerie" {
		t.Fatalf("source context host = %q", src.ctxSeen[0].HostID)
	}
}

// A host read is the same payload as a local one, so it must reach the same
// handler rather than a parallel one.
func TestRemotePreviewLandsThroughTheOrdinaryPreviewMessage(t *testing.T) {
	p, _ := boundFilesWithContent(t)
	p.previewFile = remoteMarker

	msg := p.loadPreview(remoteMarker)()
	if _, cmd := p.Update(msg); cmd != nil {
		_ = cmd()
	}
	if p.previewRevisions[remoteMarker] != "v1:abc" {
		t.Fatalf("revision not recorded: %v", p.previewRevisions)
	}
}

func TestASecondReadOfTheSameFileIsConditional(t *testing.T) {
	p, src := boundFilesWithContent(t)
	p.previewFile = remoteMarker

	if _, cmd := p.Update(p.loadPreview(remoteMarker)()); cmd != nil {
		_ = cmd()
	}
	src.notModified = true
	msg := p.loadPreview(remoteMarker)()

	if len(src.seen) != 2 || src.seen[1].IfRevision != "v1:abc" {
		t.Fatalf("second read = %+v, want the remembered revision", src.seen)
	}
	unchanged, ok := msg.(remotePreviewUnchangedMsg)
	if !ok {
		t.Fatalf("notModified produced %T", msg)
	}
	if _, cmd := p.Update(unchanged); cmd != nil {
		t.Fatal("an unchanged read scheduled work")
	}
}

func TestBoundPreviewRefusesWhenTheHostCannotAnswer(t *testing.T) {
	p, _ := boundFilesWithContent(t)
	p.ctx.HostShows = func() bool { return false }
	if cmd := p.loadPreview(remoteMarker); cmd != nil {
		t.Fatal("a disconnected host was still read")
	}
}

// The find-by-name index is the host's own catalog. Deriving it from the tree
// would list only the directories the user happened to expand.
func TestBoundFindByNameUsesTheHostCatalog(t *testing.T) {
	p, _ := boundFilesWithContent(t)
	var args []string
	p.ctx.RemoteRunner = func(_ context.Context, _ string, a []string, out any) error {
		args = a
		raw, err := json.Marshal(contentservice.CatalogResult{
			Kind:      contentservice.KindCatalog,
			Workspace: "/home/me/sidecar:worktree:/home/me/sidecar",
			Files:     []string{"internal/cli/content.go", remoteMarker},
		})
		if err != nil {
			return err
		}
		return json.Unmarshal(raw, out)
	}

	files, errText := p.scanRemoteCandidates("", false)
	if errText != "" {
		t.Fatalf("scan error: %s", errText)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "content catalog") || !strings.Contains(joined, "--kind file") {
		t.Fatalf("args = %v", args)
	}
	if len(files) != 2 || files[1] != remoteMarker {
		t.Fatalf("candidates = %v", files)
	}
}

func TestBoundDirectorySuggestionsRefuseRatherThanListThisMachine(t *testing.T) {
	p, _ := boundFilesWithContent(t)
	files, errText := p.scanRemoteCandidates("", true)
	if files != nil {
		t.Fatalf("directory suggestions answered with %v", files)
	}
	if !strings.Contains(errText, "aerie") {
		t.Fatalf("refusal = %q, want the host named", errText)
	}
}

// Binding the finder to a host must not survive a return to a local project.
func TestFinderScannerIsClearedWhenLocal(t *testing.T) {
	p, _ := boundFilesWithContent(t)
	if _, _ = p.finderRoot(); p.quickOpen.Scan == nil {
		t.Fatal("bound finder did not take the host scanner")
	}
	p.ctx.HostID = ""
	if _, _ = p.finderRoot(); p.quickOpen.Scan != nil {
		t.Fatal("the host scanner survived a return to a local project")
	}
}

var _ tea.Msg = remotePreviewLoadedMsg{}
