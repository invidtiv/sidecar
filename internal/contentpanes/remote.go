package contentpanes

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/hostproto"
)

// RemoteRunner is the hosts.RunSidecar seam. Production binds
// hosts.Registry.RunSidecar; tests bind a recorder.
type RemoteRunner func(ctx context.Context, hostID string, args []string, out any) error

// RemoteSource is a Document source that reads through `sidecar content`
// verbs on a registered host. Slice 3 wires this into Sessions; this slice
// only constructs it.
type RemoteSource struct {
	HostID string
	Verbs  hostproto.VerbCapabilities
	Run    RemoteRunner
}

var _ Source = RemoteSource{}

// NewRemoteSource builds a remote Document adapter. Capability is checked on
// each call so a host that advertised ContentReadV1 at hello and then rolled
// back is still refused.
func NewRemoteSource(hostID string, verbs hostproto.VerbCapabilities, run RemoteRunner) RemoteSource {
	return RemoteSource{HostID: hostID, Verbs: verbs, Run: run}
}

func (s RemoteSource) Resolve(ctx context.Context, src SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	if err := s.ready(); err != nil {
		return contentlink.Ref{}, err
	}
	if pending.Kind != contentlink.KindFile {
		return contentlink.Ref{}, fmt.Errorf("unsupported pending kind %q", pending.Kind)
	}
	var result contentservice.ResolveResult
	args := []string{"content", "resolve", "--workspace", src.WorkspaceID, "--kind", contentservice.KindFile, "--target", pending.Raw, "--json"}
	if err := s.Run(ctx, s.HostID, args, &result); err != nil {
		return contentlink.Ref{}, err
	}
	return contentlink.Ref{Kind: contentlink.KindFile, Value: result.Display}, nil
}

func (s RemoteSource) LoadDocument(ctx context.Context, src SourceContext, req DocumentReadRequest) (DocumentReadResult, error) {
	if err := s.ready(); err != nil {
		return DocumentReadResult{}, err
	}
	args := []string{
		"content", "read",
		"--workspace", src.WorkspaceID,
		"--kind", contentservice.KindFile,
		"--operation", contentservice.OpDocument,
		"--target", req.Ref.Value,
		"--json",
	}
	if req.IfRevision != "" {
		args = append(args, "--if-revision", req.IfRevision)
	}
	var result contentservice.ReadResult
	if err := s.Run(ctx, s.HostID, args, &result); err != nil {
		return DocumentReadResult{}, err
	}
	if result.NotModified {
		return DocumentReadResult{NotModified: true, Revision: result.Revision}, nil
	}
	return DocumentReadResult{
		Value:    previewFromDocument(documentFromRead(result)),
		Revision: result.Revision,
	}, nil
}

func (s RemoteSource) ready() error {
	if !s.Verbs.ContentReadV1 {
		return &contentservice.MissingCapabilityError{HostID: s.HostID}
	}
	if s.Run == nil {
		return fmt.Errorf("content remote source: no runner is configured")
	}
	if s.HostID == "" {
		return fmt.Errorf("content remote source: host id is required")
	}
	return nil
}

func documentFromRead(result contentservice.ReadResult) contentservice.Document {
	doc := contentservice.Document{
		Display:     result.Display,
		Absolute:    result.Path,
		Content:     result.Content,
		Binary:      result.Binary,
		Image:       result.Image,
		Truncated:   result.Truncated,
		TotalSize:   result.TotalSize,
		Revision:    result.Revision,
		NotModified: result.NotModified,
	}
	if result.ModTime != "" {
		if ts, err := time.Parse(time.RFC3339Nano, result.ModTime); err == nil {
			doc.ModTime = ts
		}
	}
	if result.Mode != "" {
		if parsed, err := strconv.ParseUint(result.Mode, 0, 32); err == nil {
			doc.Mode = os.FileMode(parsed)
		}
	}
	return doc
}
