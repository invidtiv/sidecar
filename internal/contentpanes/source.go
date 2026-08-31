package contentpanes

import (
	"context"
	"fmt"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/terminallink"
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

// LocalSource is today's filepreview / terminallink functions. A nil Config.Source
// also delegates to those functions so tests constructing Config{} stay valid.
type LocalSource struct{}

// Resolve maps a file token onto a regular file on this machine.
func (LocalSource) Resolve(_ context.Context, src SourceContext, pending contentlink.Pending) (contentlink.Ref, error) {
	if pending.Kind != contentlink.KindFile {
		return contentlink.Ref{}, fmt.Errorf("unsupported pending kind %q", pending.Kind)
	}
	display, abs, ok := terminallink.ResolveFile(src.Root, pending.Raw)
	if !ok {
		return contentlink.Ref{}, fmt.Errorf("file %q is not readable from %s", pending.Raw, src.Root)
	}
	file, err := terminallink.OpenRegular(abs)
	if err != nil {
		return contentlink.Ref{}, err
	}
	_ = file.Close()
	return contentlink.Ref{Kind: contentlink.KindFile, Value: display}, nil
}

// LoadDocument reads through filepreview.LoadPreview. It always returns a body;
// the viewer's fingerprint gate drops a no-op refresh.
func (LocalSource) LoadDocument(_ context.Context, src SourceContext, req DocumentReadRequest) (DocumentReadResult, error) {
	root := src.Root
	path := req.Ref.Value
	if filepath.IsAbs(filepath.FromSlash(path)) {
		root = ""
	}
	msg := filepreview.LoadPreview(root, path, 0)()
	loaded, ok := msg.(filepreview.PreviewLoadedMsg)
	if !ok {
		return DocumentReadResult{}, fmt.Errorf("unexpected preview load result")
	}
	return DocumentReadResult{Value: loaded.Result}, nil
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
		return filepreview.PreviewLoadedMsg{Epoch: epoch, Path: ref.Value, Result: result.Value}
	}
}
