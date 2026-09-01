package contentpanes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/resource"
)

func TestRemoteSourceRefusesMissingContentReadV1(t *testing.T) {
	t.Parallel()
	var ran bool
	src := NewRemoteSource("mac-mini", hostproto.VerbCapabilities{}, func(context.Context, string, []string, any) error {
		ran = true
		return nil
	})
	_, err := src.Resolve(context.Background(), SourceContext{WorkspaceID: "p:shell:s1"}, contentlink.Pending{Kind: contentlink.KindFile, Raw: "a.md"})
	var missing *contentservice.MissingCapabilityError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want MissingCapabilityError", err)
	}
	if !strings.Contains(err.Error(), "Update Sidecar on mac-mini") {
		t.Fatalf("message = %q", err.Error())
	}
	if ran {
		t.Fatal("a host without ContentReadV1 was invoked")
	}
}

func TestRemoteSourceBuildsContentArgv(t *testing.T) {
	t.Parallel()
	var calls [][]string
	src := NewRemoteSource("mac-mini", hostproto.VerbCapabilities{ContentReadV1: true}, func(_ context.Context, hostID string, args []string, out any) error {
		if hostID != "mac-mini" {
			t.Fatalf("host = %q", hostID)
		}
		calls = append(calls, append([]string(nil), args...))
		switch args[1] {
		case "resolve":
			*(out.(*contentservice.ResolveResult)) = contentservice.ResolveResult{
				Kind: "file", Workspace: "p:shell:s1", Display: "a.md", Path: "/p/a.md", Revision: "v1:abc",
			}
		case "read":
			*(out.(*contentservice.ReadResult)) = contentservice.ReadResult{
				Kind: "file", Operation: "document", Workspace: "p:shell:s1",
				Display: "a.md", Path: "/p/a.md", Revision: "v1:abc", Content: "hi\n",
			}
		}
		return nil
	})
	ctx := SourceContext{WorkspaceID: "p:shell:s1"}
	ref, err := src.Resolve(context.Background(), ctx, contentlink.Pending{Kind: contentlink.KindFile, Raw: "a.md"})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Value != "a.md" {
		t.Fatalf("ref = %+v", ref)
	}
	got, err := src.LoadDocument(context.Background(), ctx, DocumentReadRequest{
		Ref: ref, IfRevision: "v1:old",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision != "v1:abc" || got.Value.Content != "hi\n" {
		t.Fatalf("load = %+v", got)
	}
	if len(calls) != 2 {
		t.Fatalf("calls = %v", calls)
	}
	if strings.Join(calls[0], " ") != "content resolve --workspace p:shell:s1 --kind file --target a.md --json" {
		t.Fatalf("resolve argv = %v", calls[0])
	}
	wantRead := "content read --workspace p:shell:s1 --kind file --operation document --target a.md --json --if-revision v1:old"
	if strings.Join(calls[1], " ") != wantRead {
		t.Fatalf("read argv = %v", calls[1])
	}
}

func TestRemoteSourcePropagatesRunFailures(t *testing.T) {
	t.Parallel()
	cases := []hosts.Failure{hosts.FailCanceled, hosts.FailTimeout, hosts.FailRejected, hosts.FailUnsupported}
	for _, failure := range cases {
		t.Run(string(failure), func(t *testing.T) {
			src := NewRemoteSource("mac-mini", hostproto.VerbCapabilities{ContentReadV1: true}, func(context.Context, string, []string, any) error {
				return &hosts.RunError{Failure: failure, HostID: "mac-mini", Args: []string{"content", "read"}, ExitCode: -1, Detail: string(failure)}
			})
			_, err := src.LoadDocument(context.Background(), SourceContext{WorkspaceID: "p:shell:s1"}, DocumentReadRequest{
				Ref: contentlink.Ref{Kind: contentlink.KindFile, Value: "a.md"},
			})
			if hosts.RunFailure(err) != failure {
				t.Fatalf("failure = %q, want %q (err %v)", hosts.RunFailure(err), failure, err)
			}
		})
	}
}

func TestRemoteSourceIssueNoteArgvAndNotModified(t *testing.T) {
	t.Parallel()
	var calls [][]string
	src := NewRemoteSource("mac-mini", hostproto.VerbCapabilities{ContentReadV1: true}, func(_ context.Context, _ string, args []string, out any) error {
		calls = append(calls, append([]string(nil), args...))
		switch {
		case args[1] == "resolve" && args[5] == contentservice.KindIssue:
			*(out.(*contentservice.ResolveResult)) = contentservice.ResolveResult{
				Kind: contentservice.KindIssue, Workspace: "p:shell:s1", Target: "td-a4dd72", Display: "td-a4dd72",
			}
		case args[1] == "read" && args[5] == contentservice.KindIssue:
			*(out.(*contentservice.ReadResult)) = contentservice.ReadResult{
				Kind: contentservice.KindIssue, Operation: contentservice.OpCard, Workspace: "p:shell:s1",
				Revision: "v1:iss", Issue: &contentservice.IssueDTO{ID: "td-a4dd72", Title: "Host"},
			}
		case args[1] == "read" && args[5] == contentservice.KindNote:
			*(out.(*contentservice.ReadResult)) = contentservice.ReadResult{
				Kind: contentservice.KindNote, NotModified: true, Revision: "v1:note",
			}
		}
		return nil
	})
	ctx := SourceContext{WorkspaceID: "p:shell:s1"}
	ref, err := src.Resolve(context.Background(), ctx, contentlink.Pending{Kind: contentlink.KindIssue, Raw: "td-a4dd72"})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != contentlink.KindIssue || ref.Value != "td-a4dd72" {
		t.Fatalf("ref = %+v", ref)
	}
	got, err := src.LoadIssue(context.Background(), ctx, IssueReadRequest{Ref: ref, IfRevision: "v1:old"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Value.Data == nil || got.Value.Data.Title != "Host" || got.Revision != "v1:iss" {
		t.Fatalf("load issue = %+v", got)
	}
	note, err := src.LoadNote(context.Background(), ctx, NoteReadRequest{
		Ref:        contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: "nt-host01"},
		IfRevision: "v1:note",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !note.NotModified || note.Revision != "v1:note" {
		t.Fatalf("note notModified = %+v", note)
	}
	if strings.Join(calls[0], " ") != "content resolve --workspace p:shell:s1 --kind issue --target td-a4dd72 --json" {
		t.Fatalf("resolve argv = %v", calls[0])
	}
	wantIssue := "content read --workspace p:shell:s1 --kind issue --operation card --target td-a4dd72 --json --if-revision v1:old"
	if strings.Join(calls[1], " ") != wantIssue {
		t.Fatalf("issue read argv = %v", calls[1])
	}
	wantNote := "content read --workspace p:shell:s1 --kind note --operation note --target nt-host01 --json --if-revision v1:note"
	if strings.Join(calls[2], " ") != wantNote {
		t.Fatalf("note read argv = %v", calls[2])
	}
}

func TestRemoteSourceUnsupportedIssueKindIsMissingCapability(t *testing.T) {
	t.Parallel()
	src := NewRemoteSource("mac-mini", hostproto.VerbCapabilities{ContentReadV1: true}, func(context.Context, string, []string, any) error {
		return &hosts.RunError{Failure: hosts.FailUnsupported, HostID: "mac-mini", Args: []string{"content", "read"}, ExitCode: 2, Detail: "unknown content kind"}
	})
	_, err := src.LoadIssue(context.Background(), SourceContext{WorkspaceID: "p:shell:s1"}, IssueReadRequest{
		Ref: contentlink.Ref{Kind: contentlink.KindIssue, Value: "td-a4dd72"},
	})
	var missing *contentservice.MissingCapabilityError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want MissingCapabilityError", err)
	}
}

func TestRemoteSourceDiffArgvAndNotModified(t *testing.T) {
	t.Parallel()
	var calls [][]string
	src := NewRemoteSource("mac-mini", hostproto.VerbCapabilities{ContentReadV1: true}, func(_ context.Context, _ string, args []string, out any) error {
		calls = append(calls, append([]string(nil), args...))
		switch args[1] {
		case "resolve":
			*(out.(*contentservice.ResolveResult)) = contentservice.ResolveResult{
				Kind: contentservice.KindDiff, Workspace: "p:shell:s1", Target: "c:aabbcc1", Display: "aabbcc1",
			}
		case "read":
			for i, a := range args {
				if a == "--if-revision" && i+1 < len(args) && args[i+1] == "v1:diff" {
					*(out.(*contentservice.ReadResult)) = contentservice.ReadResult{
						Kind: contentservice.KindDiff, NotModified: true, Revision: "v1:diff",
					}
					return nil
				}
			}
			*(out.(*contentservice.ReadResult)) = contentservice.ReadResult{
				Kind: contentservice.KindDiff, Operation: contentservice.OpWorkingTree, Workspace: "p:shell:s1",
				Revision: "v1:diff", Diff: &contentservice.DiffDTO{Target: "wt", Snapshot: &contentservice.DiffSnapshotDTO{WorkingTree: "HOST-ONLY-DIFF\n"}},
			}
		}
		return nil
	})
	ctx := SourceContext{WorkspaceID: "p:shell:s1"}
	ref, err := src.Resolve(context.Background(), ctx, contentlink.Pending{Kind: contentlink.KindDiff, Raw: "aabbcc1"})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Kind != contentlink.KindDiff || ref.Value != "c:aabbcc1" {
		t.Fatalf("ref = %+v", ref)
	}
	got, err := src.LoadDiff(context.Background(), ctx, DiffReadRequest{
		Ref: contentlink.Ref{Kind: contentlink.KindDiff, Value: "wt"}, Operation: contentservice.OpWorkingTree, IfRevision: "v1:old",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Value.Snapshot == nil || !strings.Contains(got.Value.Snapshot.WorkingTree, "HOST-ONLY-DIFF") {
		t.Fatalf("load diff = %+v", got)
	}
	again, err := src.LoadDiff(context.Background(), ctx, DiffReadRequest{
		Ref: contentlink.Ref{Kind: contentlink.KindDiff, Value: "wt"}, Operation: contentservice.OpWorkingTree, IfRevision: "v1:diff",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !again.NotModified {
		t.Fatalf("notModified = %+v", again)
	}
	if strings.Join(calls[0], " ") != "content resolve --workspace p:shell:s1 --kind diff --target aabbcc1 --json" {
		t.Fatalf("resolve argv = %v", calls[0])
	}
	want := "content read --workspace p:shell:s1 --kind diff --operation working-tree --target wt --json --if-revision v1:old"
	if strings.Join(calls[1], " ") != want {
		t.Fatalf("read argv = %v", calls[1])
	}
}

func TestRemoteSourceNotModified(t *testing.T) {
	t.Parallel()
	src := NewRemoteSource("mac-mini", hostproto.VerbCapabilities{ContentReadV1: true}, func(_ context.Context, _ string, _ []string, out any) error {
		*(out.(*contentservice.ReadResult)) = contentservice.ReadResult{Kind: "file", NotModified: true, Revision: "v1:abc"}
		return nil
	})
	got, err := src.LoadDocument(context.Background(), SourceContext{WorkspaceID: "p:shell:s1"}, DocumentReadRequest{
		Ref: contentlink.Ref{Kind: contentlink.KindFile, Value: "a.md"}, IfRevision: "v1:abc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !got.NotModified || got.Revision != "v1:abc" || got.Value.Content != "" {
		t.Fatalf("notModified = %+v", got)
	}
}

func TestRemoteSourceDescribeArgvAndFingerprint(t *testing.T) {
	t.Parallel()
	descriptors := []contentservice.ProviderDescriptor{{
		Instance: "jira-work", Order: 0,
		Matchers: []contentservice.ResourceMatcherDTO{{ID: "issue-key", Pattern: `CASH-\d+`}},
	}}
	fp := contentservice.FingerprintDescriptors(descriptors)
	var calls [][]string
	src := NewRemoteSource("mac-mini", hostproto.VerbCapabilities{ContentReadV1: true}, func(_ context.Context, _ string, args []string, out any) error {
		calls = append(calls, append([]string(nil), args...))
		for i, a := range args {
			if a == "--if-revision" && i+1 < len(args) && args[i+1] == fp {
				*(out.(*contentservice.DescribeResult)) = contentservice.DescribeResult{Fingerprint: fp, NotModified: true}
				return nil
			}
		}
		*(out.(*contentservice.DescribeResult)) = contentservice.DescribeResult{Fingerprint: fp, Descriptors: descriptors}
		return nil
	})
	got, err := src.Describe(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Fingerprint != fp || len(got.Descriptors) != 1 {
		t.Fatalf("describe = %+v", got)
	}
	cached, err := src.Describe(context.Background(), fp)
	if err != nil {
		t.Fatal(err)
	}
	if !cached.NotModified {
		t.Fatalf("notModified = %+v", cached)
	}
	if strings.Join(calls[0], " ") != "content describe --json" {
		t.Fatalf("describe argv = %v", calls[0])
	}
	if strings.Join(calls[1], " ") != "content describe --json --if-revision "+fp {
		t.Fatalf("if-revision argv = %v", calls[1])
	}
}

func TestRemoteSourceResolveResourceSanitizesWireAndRefusesUnsafe(t *testing.T) {
	t.Parallel()
	var calls [][]string
	src := NewRemoteSource("mac-mini", hostproto.VerbCapabilities{ContentReadV1: true}, func(_ context.Context, _ string, args []string, out any) error {
		calls = append(calls, append([]string(nil), args...))
		if len(calls) == 1 {
			*(out.(*contentservice.ReadResult)) = contentservice.ReadResult{
				Kind: contentservice.KindResource, Operation: contentservice.OpResource,
				Workspace: "p:shell:s1", Revision: "v1:res",
				Resource: &resource.WireDocument{Identity: "CASH-1245", Title: "Host ticket", Body: &resource.WireBody{Text: "ok"}},
			}
			return nil
		}
		*(out.(*contentservice.ReadResult)) = contentservice.ReadResult{
			Kind: contentservice.KindResource, Operation: contentservice.OpResource,
			Workspace: "p:shell:s1", Revision: "v1:bad",
			Resource: &resource.WireDocument{Title: "no identity"},
		}
		return nil
	})
	ctx := SourceContext{WorkspaceID: "p:shell:s1"}
	ref := resource.Reference{Instance: "jira-work", Matcher: "issue-key", Locator: "CASH-1245"}
	got, err := src.ResolveResource(context.Background(), ctx, ref, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Host ticket" || got.Identity != "CASH-1245" {
		t.Fatalf("doc = %+v", got)
	}
	if strings.Join(calls[0], " ") != "content read --workspace p:shell:s1 --kind resource --operation resource --target CASH-1245 --provider jira-work --matcher issue-key --json --refresh" {
		t.Fatalf("read argv = %v", calls[0])
	}
	if _, err := src.ResolveResource(context.Background(), ctx, ref, false); err == nil {
		t.Fatal("unsafe wire body was adopted")
	}
}
