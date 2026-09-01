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
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/resource"
)

// RemoteRunner is the hosts.RunSidecar seam. Production binds
// hosts.Registry.RunSidecar; tests bind a recorder.
type RemoteRunner func(ctx context.Context, hostID string, args []string, out any) error

// RemoteSource is a Document source that reads through `sidecar content`
// verbs on a registered host.
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
	kind, refKind, namespace, err := remoteResolveKind(pending)
	if err != nil {
		return contentlink.Ref{}, err
	}
	var result contentservice.ResolveResult
	args := []string{"content", "resolve", "--workspace", src.WorkspaceID, "--kind", kind, "--target", pending.Raw, "--json"}
	if err := s.Run(ctx, s.HostID, args, &result); err != nil {
		return contentlink.Ref{}, mapRemoteContentErr(s.HostID, kind, err)
	}
	value := result.Target
	if value == "" {
		value = result.Display
	}
	return contentlink.Ref{Kind: refKind, Namespace: namespace, Value: value}, nil
}

func remoteResolveKind(pending contentlink.Pending) (kind string, refKind contentlink.Kind, namespace string, err error) {
	switch pending.Kind {
	case contentlink.KindFile:
		return contentservice.KindFile, contentlink.KindFile, "", nil
	case contentlink.KindIssue:
		return contentservice.KindIssue, contentlink.KindIssue, "", nil
	case contentlink.KindInternal:
		return contentservice.KindNote, contentlink.KindInternal, "note", nil
	case contentlink.KindDiff:
		return contentservice.KindDiff, contentlink.KindDiff, "", nil
	default:
		return "", "", "", fmt.Errorf("unsupported pending kind %q", pending.Kind)
	}
}

func (s RemoteSource) Describe(ctx context.Context, ifRevision string) (contentservice.DescribeResult, error) {
	if err := s.ready(); err != nil {
		return contentservice.DescribeResult{}, err
	}
	args := []string{"content", "describe", "--json"}
	if ifRevision != "" {
		args = append(args, "--if-revision", ifRevision)
	}
	var result contentservice.DescribeResult
	if err := s.Run(ctx, s.HostID, args, &result); err != nil {
		return contentservice.DescribeResult{}, mapRemoteContentErr(s.HostID, contentservice.KindResource, err)
	}
	if result.NotModified {
		return result, nil
	}
	if got := contentservice.FingerprintDescriptors(result.Descriptors); got != result.Fingerprint {
		return contentservice.DescribeResult{}, fmt.Errorf("content describe: fingerprint did not match descriptors")
	}
	return result, nil
}

func (s RemoteSource) ResolveResource(ctx context.Context, src SourceContext, ref resource.Reference, refresh bool) (resource.Document, error) {
	if err := s.ready(); err != nil {
		return resource.Document{}, err
	}
	if !ref.Valid() {
		return resource.Document{}, fmt.Errorf("resource reference is empty or exceeds its bounds")
	}
	args := []string{
		"content", "read",
		"--workspace", src.WorkspaceID,
		"--kind", contentservice.KindResource,
		"--operation", contentservice.OpResource,
		"--target", ref.Locator,
		"--provider", ref.Instance,
		"--matcher", ref.Matcher,
		"--json",
	}
	if refresh {
		args = append(args, "--refresh")
	}
	var result contentservice.ReadResult
	if err := s.Run(ctx, s.HostID, args, &result); err != nil {
		return resource.Document{}, mapRemoteContentErr(s.HostID, contentservice.KindResource, err)
	}
	if result.ResourceError != nil {
		return resource.Document{}, contentservice.ResourceErrorFromWire(result.ResourceError)
	}
	return contentservice.SanitizeWireDocument(result.Resource)
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

func (s RemoteSource) LoadIssue(ctx context.Context, src SourceContext, req IssueReadRequest) (IssueReadResult, error) {
	if err := s.ready(); err != nil {
		return IssueReadResult{}, err
	}
	args := []string{
		"content", "read",
		"--workspace", src.WorkspaceID,
		"--kind", contentservice.KindIssue,
		"--operation", contentservice.OpCard,
		"--target", req.Ref.Value,
		"--json",
	}
	if req.IfRevision != "" {
		args = append(args, "--if-revision", req.IfRevision)
	}
	var result contentservice.ReadResult
	if err := s.Run(ctx, s.HostID, args, &result); err != nil {
		return IssueReadResult{}, mapRemoteContentErr(s.HostID, contentservice.KindIssue, err)
	}
	if result.NotModified {
		return IssueReadResult{NotModified: true, Revision: result.Revision}, nil
	}
	data, owner := contentservice.IssueFromDTO(result.Issue)
	return IssueReadResult{
		Value:    IssuePayload{Data: data, Owner: owner},
		Revision: result.Revision,
	}, nil
}

func (s RemoteSource) LoadNote(ctx context.Context, src SourceContext, req NoteReadRequest) (NoteReadResult, error) {
	if err := s.ready(); err != nil {
		return NoteReadResult{}, err
	}
	args := []string{
		"content", "read",
		"--workspace", src.WorkspaceID,
		"--kind", contentservice.KindNote,
		"--operation", contentservice.OpNote,
		"--target", req.Ref.Value,
		"--json",
	}
	if req.IfRevision != "" {
		args = append(args, "--if-revision", req.IfRevision)
	}
	var result contentservice.ReadResult
	if err := s.Run(ctx, s.HostID, args, &result); err != nil {
		return NoteReadResult{}, mapRemoteContentErr(s.HostID, contentservice.KindNote, err)
	}
	if result.NotModified {
		return NoteReadResult{NotModified: true, Revision: result.Revision}, nil
	}
	return NoteReadResult{
		Value:    contentservice.NoteFromDTO(result.Note),
		Revision: result.Revision,
	}, nil
}

func (s RemoteSource) LoadDiff(ctx context.Context, src SourceContext, req DiffReadRequest) (DiffReadResult, error) {
	if err := s.ready(); err != nil {
		return DiffReadResult{}, err
	}
	op := req.Operation
	if op == "" {
		op = diffOperationFor(req.Ref.Value)
	}
	target := req.Ref.Value
	if target == "" {
		target = workspacediffIdentityWorkingTree()
	}
	args := []string{
		"content", "read",
		"--workspace", src.WorkspaceID,
		"--kind", contentservice.KindDiff,
		"--operation", op,
		"--target", target,
		"--json",
	}
	if req.IfRevision != "" {
		args = append(args, "--if-revision", req.IfRevision)
	}
	if req.Path != "" {
		args = append(args, "--path", req.Path)
	}
	if req.ParentHash != "" {
		args = append(args, "--parent", req.ParentHash)
	}
	if req.Offset > 0 {
		args = append(args, "--offset", strconv.Itoa(req.Offset))
	}
	if req.Limit > 0 {
		args = append(args, "--limit", strconv.Itoa(req.Limit))
	}
	var result contentservice.ReadResult
	if err := s.Run(ctx, s.HostID, args, &result); err != nil {
		return DiffReadResult{}, mapRemoteContentErr(s.HostID, contentservice.KindDiff, err)
	}
	if result.NotModified {
		return DiffReadResult{NotModified: true, Revision: result.Revision}, nil
	}
	return DiffReadResult{
		Value:    diffPayloadFromRead(result),
		Revision: result.Revision,
	}, nil
}

func workspacediffIdentityWorkingTree() string {
	return "wt"
}

func diffPayloadFromRead(result contentservice.ReadResult) DiffPayload {
	payload := DiffPayload{}
	if result.Diff == nil {
		return payload
	}
	if result.Diff.Snapshot != nil {
		payload.Snapshot = contentservice.SnapshotFromDTO(result.Diff.Snapshot)
	}
	if result.Diff.Commit != nil {
		payload.Commit = contentservice.CommitFromDTO(result.Diff.Commit)
	}
	if result.Diff.Range != nil {
		payload.RangeRaw = result.Diff.Range.Raw
	}
	if result.Diff.File != nil {
		payload.FileRaw = result.Diff.File.Raw
		payload.FilePath = result.Diff.File.Path
	}
	return payload
}

func mapRemoteContentErr(hostID, kind string, err error) error {
	if err == nil {
		return nil
	}
	if kind == contentservice.KindIssue || kind == contentservice.KindNote || kind == contentservice.KindDiff || kind == contentservice.KindResource {
		if hosts.RunFailure(err) == hosts.FailUnsupported {
			return &contentservice.MissingCapabilityError{HostID: hostID}
		}
	}
	return err
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
