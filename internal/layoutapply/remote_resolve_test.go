package layoutapply

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentpanes"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/resource"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// recordingSource answers Resolve with whatever it was handed, so a test can
// assert the exact string a surface put on the wire.
type recordingSource struct {
	seen  []string
	kind  contentlink.Kind
	ns    string
	value func(raw string) string
}

func (s *recordingSource) Resolve(_ context.Context, _ contentpanes.SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	s.seen = append(s.seen, pending.Raw)
	kind := s.kind
	if kind == "" {
		kind = pending.Kind
	}
	value := pending.Raw
	if s.value != nil {
		value = s.value(pending.Raw)
	}
	return contentlink.Ref{Kind: kind, Namespace: s.ns, Value: value}, nil
}

func (s *recordingSource) LoadDocument(context.Context, contentpanes.SourceContext, contentpanes.DocumentReadRequest) (contentpanes.DocumentReadResult, error) {
	return contentpanes.DocumentReadResult{}, fmt.Errorf("not used")
}
func (s *recordingSource) LoadIssue(context.Context, contentpanes.SourceContext, contentpanes.IssueReadRequest) (contentpanes.IssueReadResult, error) {
	return contentpanes.IssueReadResult{}, fmt.Errorf("not used")
}
func (s *recordingSource) LoadNote(context.Context, contentpanes.SourceContext, contentpanes.NoteReadRequest) (contentpanes.NoteReadResult, error) {
	return contentpanes.NoteReadResult{}, fmt.Errorf("not used")
}
func (s *recordingSource) LoadDiff(context.Context, contentpanes.SourceContext, contentpanes.DiffReadRequest) (contentpanes.DiffReadResult, error) {
	return contentpanes.DiffReadResult{}, fmt.Errorf("not used")
}
func (s *recordingSource) Describe(context.Context, string) (contentservice.DescribeResult, error) {
	return contentservice.DescribeResult{}, nil
}
func (s *recordingSource) ResolveResource(context.Context, contentpanes.SourceContext, resource.Reference, bool) (resource.Document, error) {
	return resource.Document{}, fmt.Errorf("not used")
}

func remoteCtx() contentpanes.SourceContext {
	return contentpanes.SourceContext{HostID: "mac-mini", ProjectKey: "/home/me/sidecar", WorkspaceID: "/home/me/sidecar:shell:s1"}
}

func TestResolveRemoteTargetsSplitsDocumentLine(t *testing.T) {
	src := &recordingSource{kind: contentlink.KindFile}
	targets, refusal := ResolveRemoteTargets(panelayout.Document, uirequest.LayoutPane{Targets: []string{"docs/plan.md:42"}}, src, remoteCtx(), nil)
	if refusal != "" {
		t.Fatalf("refusal = %q", refusal)
	}
	if len(src.seen) != 1 || src.seen[0] != "docs/plan.md" {
		t.Fatalf("source saw %v, want the path without the line suffix", src.seen)
	}
	if len(targets) != 1 || targets[0].Value != "docs/plan.md" || targets[0].Line != 42 {
		t.Fatalf("target = %+v", targets)
	}
}

// A note link click carries the sidecar://note/<id> URI. Both remote surfaces
// have to strip it before the host resolves it: the bound project workspace
// once did not, and spelled the same pane two different ways.
func TestResolveRemoteTargetsNormalizesNoteURI(t *testing.T) {
	for _, raw := range []string{"sidecar://note/abc123", "abc123"} {
		src := &recordingSource{kind: contentlink.KindInternal, ns: "note"}
		targets, refusal := ResolveRemoteTargets(panelayout.Note, uirequest.LayoutPane{Targets: []string{raw}}, src, remoteCtx(), nil)
		if refusal != "" {
			t.Fatalf("%s: refusal = %q", raw, refusal)
		}
		if len(src.seen) != 1 || src.seen[0] != "abc123" {
			t.Fatalf("%s: source saw %v, want the bare note id", raw, src.seen)
		}
		if len(targets) != 1 || targets[0].Kind != uirequest.TargetKindNote || targets[0].Value != "abc123" {
			t.Fatalf("%s: target = %+v", raw, targets)
		}
	}
}

func TestResolveRemoteTargetsDiffDefaultsToWorkingTree(t *testing.T) {
	src := &recordingSource{kind: contentlink.KindDiff}
	targets, refusal := ResolveRemoteTargets(panelayout.Diff, uirequest.LayoutPane{}, src, remoteCtx(), nil)
	if refusal != "" {
		t.Fatalf("refusal = %q", refusal)
	}
	if len(src.seen) != 1 || src.seen[0] != workspacediff.IdentityWorkingTree {
		t.Fatalf("source saw %v", src.seen)
	}
	if len(targets) != 1 || targets[0].Kind != uirequest.TargetKindDiff {
		t.Fatalf("target = %+v", targets)
	}
}

func TestResolveRemoteTargetsRefusesKindMismatchByPaneName(t *testing.T) {
	src := &recordingSource{kind: contentlink.KindIssue}
	_, refusal := ResolveRemoteTargets(panelayout.Document, uirequest.LayoutPane{Targets: []string{"td-1"}}, src, remoteCtx(), nil)
	if !strings.Contains(refusal, panelayout.Issue.Name()) || !strings.Contains(refusal, panelayout.Document.Name()) {
		t.Fatalf("refusal = %q, want both pane names", refusal)
	}
	if strings.Contains(refusal, string(uirequest.TargetKindIssue)+" pane") && panelayout.Issue.Name() != string(uirequest.TargetKindIssue) {
		t.Fatalf("refusal leaked the wire kind: %q", refusal)
	}
}

func TestResolveRemoteTargetsNotFoundNamesTheHost(t *testing.T) {
	src := &recordingSource{kind: contentlink.KindFile, value: func(string) string { return "" }}
	_, refusal := ResolveRemoteTargets(panelayout.Document, uirequest.LayoutPane{Targets: []string{"missing.md"}}, src, remoteCtx(), nil)
	if !strings.Contains(refusal, "mac-mini") || !strings.Contains(refusal, "missing.md") {
		t.Fatalf("refusal = %q, want the host and the target named", refusal)
	}
}

func TestResolveRemoteTargetsEmptyDocumentTargetsRefuse(t *testing.T) {
	src := &recordingSource{kind: contentlink.KindFile}
	_, refusal := ResolveRemoteTargets(panelayout.Document, uirequest.LayoutPane{}, src, remoteCtx(), nil)
	if refusal == "" {
		t.Fatal("a document pane with no targets was accepted")
	}
	if len(src.seen) != 0 {
		t.Fatalf("source was consulted for an empty descriptor: %v", src.seen)
	}
}
