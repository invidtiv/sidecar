package contentpanes

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/contentservice"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/noteview"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// SourceContext is the host-qualified identity of the workspace a content
// pane was opened from. Empty HostID means this machine. HostIncarnation is
// the viewer registry client identity and is never serialized or sent as
// authority.
type SourceContext struct {
	HostID          string
	HostIncarnation uint64
	ProjectKey      string
	ProjectRoot     string
	WorkspaceID     string
	WorkspaceKind   workspaceinventory.Kind
	WorkspaceKey    string
	Root            string
}

// Remote reports whether this source lives on another machine.
func (s SourceContext) Remote() bool { return s.HostID != "" }

// DocumentReadRequest is one Document resolve/load. IfRevision is the last
// adopted source revision and is empty on the first load.
type DocumentReadRequest struct {
	Ref        contentlink.Ref
	IfRevision string
}

// DocumentReadResult is a typed Document payload the shared viewer consumes.
// NotModified completes an in-flight refresh without replacing content.
type DocumentReadResult struct {
	Value       filepreview.PreviewResult
	Revision    string
	NotModified bool
}

// IssueReadRequest is one Issue resolve/load. Fallbacks are local-only;
// a remote source ignores them and uses the host's config.
type IssueReadRequest struct {
	Ref        contentlink.Ref
	IfRevision string
	Fallbacks  []issueview.ProjectRef
}

// IssuePayload is the typed issue card a shared viewer consumes.
type IssuePayload struct {
	Data  *issueview.Data
	Owner *issueview.Owner
}

// IssueReadResult is a typed Issue payload. NotModified completes an
// in-flight refresh without replacing content.
type IssueReadResult struct {
	Value       IssuePayload
	Revision    string
	NotModified bool
}

// NoteReadRequest is one Note resolve/load.
type NoteReadRequest struct {
	Ref        contentlink.Ref
	IfRevision string
}

// NoteReadResult is a typed Note payload. NotModified completes an
// in-flight refresh without replacing content.
type NoteReadResult struct {
	Value       *noteview.Data
	Revision    string
	NotModified bool
}

// DiffReadRequest is one Diff resolve/load. Operation is a contentservice
// diff op; Path/ParentHash/Offset/Limit are operation-specific locators.
type DiffReadRequest struct {
	Ref        contentlink.Ref
	Operation  string
	IfRevision string
	BaseRef    string
	Path       string
	ParentHash string
	Offset     int
	Limit      int
}

// DiffPayload is the typed diff body a shared viewer consumes.
type DiffPayload struct {
	Snapshot *workspacediff.Snapshot
	Commit   *workspacediff.CommitDetail
	RangeRaw string
	FileRaw  string
	FilePath string
}

// DiffReadResult is a typed Diff payload. NotModified completes an
// in-flight refresh without replacing content.
type DiffReadResult struct {
	Value       DiffPayload
	Revision    string
	NotModified bool
}

// Source is the Document/Issue/Note/Diff seam. Resolve returns identity only; it
// does not ship a body. Resource methods arrive in a later slice.
type Source interface {
	Resolve(context.Context, SourceContext, contentlink.Pending) (contentlink.Ref, error)
	LoadDocument(context.Context, SourceContext, DocumentReadRequest) (DocumentReadResult, error)
	LoadIssue(context.Context, SourceContext, IssueReadRequest) (IssueReadResult, error)
	LoadNote(context.Context, SourceContext, NoteReadRequest) (NoteReadResult, error)
	LoadDiff(context.Context, SourceContext, DiffReadRequest) (DiffReadResult, error)
}

// LocalSource is the in-process Document adapter. It uses the same
// contentservice file contract as `sidecar content`, then highlights with
// this process's theme. A nil Config.Source also delegates here so tests
// constructing Config{} stay valid.
type LocalSource struct{}

// Resolve maps a token onto identity on this machine. Issue and note ids
// are normalized without consulting td.
func (LocalSource) Resolve(ctx context.Context, src SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	if err := ctx.Err(); err != nil {
		return contentlink.Ref{}, err
	}
	switch pending.Kind {
	case contentlink.KindFile:
		resolved, err := contentservice.ResolveFile(src.Root, pending.Raw)
		if err != nil {
			return contentlink.Ref{}, err
		}
		return contentlink.Ref{Kind: contentlink.KindFile, Value: resolved.Display}, nil
	case contentlink.KindIssue:
		id := issueview.NormalizeID(pending.Raw)
		if id == "" {
			return contentlink.Ref{}, fmt.Errorf("invalid issue id %q", pending.Raw)
		}
		return contentlink.Ref{Kind: contentlink.KindIssue, Value: id}, nil
	case contentlink.KindInternal:
		id := noteview.NormalizeID(pending.Raw)
		if id == "" {
			return contentlink.Ref{}, fmt.Errorf("invalid note id %q", pending.Raw)
		}
		return contentlink.Ref{Kind: contentlink.KindInternal, Namespace: "note", Value: id}, nil
	case contentlink.KindDiff:
		resolved, err := contentservice.ResolveDiff(ctx, src.Root, pending.Raw)
		if err != nil {
			return contentlink.Ref{}, err
		}
		return contentlink.Ref{Kind: contentlink.KindDiff, Value: resolved.Identity()}, nil
	default:
		return contentlink.Ref{}, fmt.Errorf("unsupported pending kind %q", pending.Kind)
	}
}

// LoadDocument reads through contentservice.ReadFile so local/direct and
// remote/JSON share containment, revision, and notModified.
func (LocalSource) LoadDocument(ctx context.Context, src SourceContext, req DocumentReadRequest) (DocumentReadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root := src.Root
	path := req.Ref.Value
	if filepath.IsAbs(filepath.FromSlash(path)) {
		root = ""
	}
	doc, err := contentservice.ReadFile(ctx, root, path, req.IfRevision)
	if err != nil {
		return DocumentReadResult{}, err
	}
	return DocumentReadResult{
		Value:       previewFromDocument(doc),
		Revision:    doc.Revision,
		NotModified: doc.NotModified,
	}, nil
}

// LoadIssue reads through contentservice.ReadIssue so local/direct and
// remote/JSON share fallback search, revision, and notModified.
func (LocalSource) LoadIssue(ctx context.Context, src SourceContext, req IssueReadRequest) (IssueReadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root := src.Root
	id := req.Ref.Value
	doc, err := contentservice.ReadIssue(ctx, root, id, req.IfRevision, req.Fallbacks)
	if err != nil {
		return IssueReadResult{}, err
	}
	return IssueReadResult{
		Value:       IssuePayload{Data: doc.Data, Owner: doc.Owner},
		Revision:    doc.Revision,
		NotModified: doc.NotModified,
	}, nil
}

// LoadNote reads through contentservice.ReadNote so local/direct and
// remote/JSON share revision and notModified.
func (LocalSource) LoadNote(ctx context.Context, src SourceContext, req NoteReadRequest) (NoteReadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	doc, err := contentservice.ReadNote(ctx, src.Root, req.Ref.Value, req.IfRevision)
	if err != nil {
		return NoteReadResult{}, err
	}
	return NoteReadResult{
		Value:       doc.Data,
		Revision:    doc.Revision,
		NotModified: doc.NotModified,
	}, nil
}

// LoadDiff reads through contentservice so local/direct and remote/JSON
// share git execution, revision, and notModified.
func (LocalSource) LoadDiff(ctx context.Context, src SourceContext, req DiffReadRequest) (DiffReadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	target := req.Ref.Value
	if target == "" {
		target = workspacediff.IdentityWorkingTree
	}
	op := req.Operation
	if op == "" {
		op = diffOperationFor(target)
	}
	doc, err := contentservice.ReadDiff(ctx, src.Root, contentservice.ReadParams{
		Operation:  op,
		Target:     target,
		IfRevision: req.IfRevision,
		Path:       req.Path,
		Parent:     req.ParentHash,
		Offset:     req.Offset,
		Limit:      req.Limit,
	})
	if err != nil {
		return DiffReadResult{}, err
	}
	if doc.Snapshot == nil && doc.DTO != nil && doc.DTO.Snapshot != nil {
		doc.Snapshot = contentservice.SnapshotFromDTO(doc.DTO.Snapshot)
	}
	if doc.Commit == nil && doc.DTO != nil && doc.DTO.Commit != nil {
		doc.Commit = contentservice.CommitFromDTO(doc.DTO.Commit)
	}
	if doc.RangeRaw == "" && doc.DTO != nil && doc.DTO.Range != nil {
		doc.RangeRaw = doc.DTO.Range.Raw
	}
	if doc.FileRaw == "" && doc.DTO != nil && doc.DTO.File != nil {
		doc.FileRaw = doc.DTO.File.Raw
		doc.FilePath = doc.DTO.File.Path
	}
	return DiffReadResult{
		Value: DiffPayload{
			Snapshot: doc.Snapshot,
			Commit:   doc.Commit,
			RangeRaw: doc.RangeRaw,
			FileRaw:  doc.FileRaw,
			FilePath: doc.FilePath,
		},
		Revision:    doc.Revision,
		NotModified: doc.NotModified,
	}, nil
}

func diffOperationFor(target string) string {
	t, ok := workspacediff.ParseSpec(target)
	if !ok {
		return contentservice.OpWorkingTree
	}
	switch t.Kind {
	case workspacediff.TargetCommit:
		return contentservice.OpCommit
	case workspacediff.TargetRange:
		return contentservice.OpRange
	default:
		return contentservice.OpWorkingTree
	}
}

func previewFromDocument(doc contentservice.Document) filepreview.PreviewResult {
	result := filepreview.PreviewResult{
		Content:     doc.Content,
		IsBinary:    doc.Binary,
		IsImage:     doc.Image,
		IsTruncated: doc.Truncated,
		TotalSize:   doc.TotalSize,
		ModTime:     doc.ModTime,
		Mode:        doc.Mode,
	}
	if result.IsBinary || result.IsImage {
		return result
	}
	result.Lines = strings.Split(result.Content, "\n")
	highlighted, err := filepreview.Highlight(result.Content, filepath.Ext(doc.Display), styles.GetSyntaxTheme())
	if err == nil {
		result.HighlightedLines = strings.Split(highlighted, "\n")
	} else {
		result.HighlightedLines = result.Lines
	}
	if len(result.Lines) > filepreview.MaxPreviewLines {
		result.Lines = result.Lines[:filepreview.MaxPreviewLines]
		if len(result.HighlightedLines) > filepreview.MaxPreviewLines {
			result.HighlightedLines = result.HighlightedLines[:filepreview.MaxPreviewLines]
		}
		result.IsTruncated = true
	}
	return result
}

// ResolveDocument uses src, or LocalSource when src is nil.
func ResolveDocument(src Source, source SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	if src == nil {
		src = LocalSource{}
	}
	return src.Resolve(context.Background(), source, pending)
}

func (c Config) documentSource() Source {
	if c.Source == nil {
		return nil
	}
	return c.Source
}

// ContentSource is the deck's Document source, defaulting to LocalSource.
func (d *Deck) ContentSource() Source {
	if d == nil || d.cfg.Source == nil {
		return LocalSource{}
	}
	return d.cfg.Source
}

func documentLoadCmd(src Source, ctx SurfaceContext, ref contentlink.Ref, ifRevision string, epoch uint64) tea.Cmd {
	if src == nil {
		return nil
	}
	return func() tea.Msg {
		srcCtx := ctx.Source
		if srcCtx.Root == "" {
			srcCtx.Root = ctx.Root
		}
		result, err := src.LoadDocument(context.Background(), srcCtx, DocumentReadRequest{Ref: ref, IfRevision: ifRevision})
		if err != nil {
			return filepreview.PreviewLoadedMsg{
				Epoch:  epoch,
				Path:   ref.Value,
				Result: filepreview.PreviewResult{Error: err},
			}
		}
		if result.NotModified {
			return docview.NotModified{Path: ref.Value, Epoch: epoch, Revision: result.Revision}
		}
		return docview.LoadedMsg{
			Epoch: epoch, Path: ref.Value, Result: result.Value, Revision: result.Revision,
		}
	}
}

func issueLoadCmd(src Source, ctx SurfaceContext, ref contentlink.Ref, ifRevision string, epoch uint64, view *issueview.Model) tea.Cmd {
	if src == nil {
		return nil
	}
	return func() tea.Msg {
		srcCtx := ctx.Source
		if srcCtx.Root == "" {
			srcCtx.Root = ctx.Root
		}
		req := IssueReadRequest{Ref: ref, IfRevision: ifRevision}
		if !srcCtx.Remote() && view != nil && view.FallbackRefs != nil {
			req.Fallbacks = view.FallbackRefs()
		}
		result, err := src.LoadIssue(context.Background(), srcCtx, req)
		if err != nil {
			return issueview.LoadedMsg{Epoch: epoch, IssueID: ref.Value, Error: err}
		}
		if result.NotModified {
			return issueview.NotModified{IssueID: ref.Value, Epoch: epoch, Revision: result.Revision}
		}
		return issueview.LoadedMsg{
			Epoch: epoch, IssueID: ref.Value,
			Data: result.Value.Data, FoundIn: result.Value.Owner,
			Revision: result.Revision,
		}
	}
}

type sourceDiffLoader struct {
	src Source
	ctx SurfaceContext
}

func (l sourceDiffLoader) LoadSnapshot(ctx context.Context, workdir, baseRef, ifRevision string) (workspacediff.SnapshotResult, error) {
	srcCtx := l.sourceContext(workdir)
	result, err := l.src.LoadDiff(ctx, srcCtx, DiffReadRequest{
		Ref:        contentlink.Ref{Kind: contentlink.KindDiff, Value: workspacediff.IdentityWorkingTree},
		Operation:  contentservice.OpWorkingTree,
		IfRevision: ifRevision,
		BaseRef:    baseRef,
	})
	if err != nil {
		return workspacediff.SnapshotResult{}, err
	}
	return workspacediff.SnapshotResult{Snapshot: result.Value.Snapshot, Revision: result.Revision, NotModified: result.NotModified}, nil
}

func (l sourceDiffLoader) LoadCommitDetail(ctx context.Context, workdir, hash, ifRevision string) (workspacediff.CommitResult, error) {
	srcCtx := l.sourceContext(workdir)
	result, err := l.src.LoadDiff(ctx, srcCtx, DiffReadRequest{
		Ref:        contentlink.Ref{Kind: contentlink.KindDiff, Value: hash},
		Operation:  contentservice.OpCommit,
		IfRevision: ifRevision,
	})
	if err != nil {
		return workspacediff.CommitResult{}, err
	}
	return workspacediff.CommitResult{Commit: result.Value.Commit, Revision: result.Revision, NotModified: result.NotModified}, nil
}

func (l sourceDiffLoader) LoadRange(ctx context.Context, workdir string, t workspacediff.Target, ifRevision string) (workspacediff.RangeResult, error) {
	srcCtx := l.sourceContext(workdir)
	result, err := l.src.LoadDiff(ctx, srcCtx, DiffReadRequest{
		Ref:        contentlink.Ref{Kind: contentlink.KindDiff, Value: t.Identity()},
		Operation:  contentservice.OpRange,
		IfRevision: ifRevision,
	})
	if err != nil {
		return workspacediff.RangeResult{}, err
	}
	files := workspacediff.ParseFiles(result.Value.RangeRaw)
	return workspacediff.RangeResult{Raw: result.Value.RangeRaw, Files: files, Revision: result.Revision, NotModified: result.NotModified}, nil
}

func (l sourceDiffLoader) LoadCommitFile(ctx context.Context, workdir, hash, path, parentHash, ifRevision string) (workspacediff.FileResult, error) {
	srcCtx := l.sourceContext(workdir)
	result, err := l.src.LoadDiff(ctx, srcCtx, DiffReadRequest{
		Ref:        contentlink.Ref{Kind: contentlink.KindDiff, Value: "c:" + hash},
		Operation:  contentservice.OpCommitFile,
		IfRevision: ifRevision,
		Path:       path,
		ParentHash: parentHash,
	})
	if err != nil {
		return workspacediff.FileResult{}, err
	}
	return workspacediff.FileResult{Path: path, Raw: result.Value.FileRaw, Revision: result.Revision, NotModified: result.NotModified}, nil
}

func (l sourceDiffLoader) LoadWorkingTreeFile(ctx context.Context, workdir, path, ifRevision string) (workspacediff.FileResult, error) {
	srcCtx := l.sourceContext(workdir)
	result, err := l.src.LoadDiff(ctx, srcCtx, DiffReadRequest{
		Ref:        contentlink.Ref{Kind: contentlink.KindDiff, Value: workspacediff.IdentityWorkingTree},
		Operation:  contentservice.OpWorkingTreeFile,
		IfRevision: ifRevision,
		Path:       path,
	})
	if err != nil {
		return workspacediff.FileResult{}, err
	}
	return workspacediff.FileResult{Path: path, Raw: result.Value.FileRaw, Revision: result.Revision, NotModified: result.NotModified}, nil
}

func (l sourceDiffLoader) sourceContext(workdir string) SourceContext {
	srcCtx := l.ctx.Source
	if srcCtx.Root == "" {
		srcCtx.Root = workdir
	}
	if srcCtx.Root == "" {
		srcCtx.Root = l.ctx.Root
	}
	return srcCtx
}

func noteLoadCmd(src Source, ctx SurfaceContext, ref contentlink.Ref, ifRevision string, epoch uint64) tea.Cmd {
	if src == nil {
		return nil
	}
	return func() tea.Msg {
		srcCtx := ctx.Source
		if srcCtx.Root == "" {
			srcCtx.Root = ctx.Root
		}
		result, err := src.LoadNote(context.Background(), srcCtx, NoteReadRequest{Ref: ref, IfRevision: ifRevision})
		if err != nil {
			return noteview.LoadedMsg{Epoch: epoch, NoteID: ref.Value, Error: err}
		}
		if result.NotModified {
			return noteview.NotModified{NoteID: ref.Value, Epoch: epoch, Revision: result.Revision}
		}
		return noteview.LoadedMsg{
			Epoch: epoch, NoteID: ref.Value, Data: result.Value, Revision: result.Revision,
		}
	}
}
