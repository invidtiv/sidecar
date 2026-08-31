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
	"github.com/marcus/sidecar/internal/styles"
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

// Source is the Document seam later slices extend with issue, note, diff, and
// resource methods. Resolve returns identity only; it does not ship a body.
type Source interface {
	Resolve(context.Context, SourceContext, contentlink.Pending) (contentlink.Ref, error)
	LoadDocument(context.Context, SourceContext, DocumentReadRequest) (DocumentReadResult, error)
}

// LocalSource is the in-process Document adapter. It uses the same
// contentservice file contract as `sidecar content`, then highlights with
// this process's theme. A nil Config.Source also delegates here so tests
// constructing Config{} stay valid.
type LocalSource struct{}

// Resolve maps a file token onto a regular file on this machine.
func (LocalSource) Resolve(ctx context.Context, src SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	if pending.Kind != contentlink.KindFile {
		return contentlink.Ref{}, fmt.Errorf("unsupported pending kind %q", pending.Kind)
	}
	if err := ctx.Err(); err != nil {
		return contentlink.Ref{}, err
	}
	resolved, err := contentservice.ResolveFile(src.Root, pending.Raw)
	if err != nil {
		return contentlink.Ref{}, err
	}
	return contentlink.Ref{Kind: contentlink.KindFile, Value: resolved.Display}, nil
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
