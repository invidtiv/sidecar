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
