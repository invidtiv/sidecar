package overview

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestPreviewActivationRejectsStaleGenerationRootAndTarget(t *testing.T) {
	for _, test := range []struct {
		name   string
		rotate func(*testing.T, *Model)
	}{
		{name: "generation", rotate: func(_ *testing.T, m *Model) { m.preview.generation++ }},
		{name: "root", rotate: func(t *testing.T, m *Model) {
			workspace := m.catalog[m.preview.workspaceID]
			workspace.Path = t.TempDir()
			m.catalog[m.preview.workspaceID] = workspace
			m.PrepareTerminalLinks()
		}},
		{name: "target", rotate: func(_ *testing.T, m *Model) {
			m.preview.terminalTarget.Pane = "%new-target"
			m.PrepareTerminalLinks()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := linkPreviewModel(t, workspaceinventory.KindWorktree)
			scope := m.preview.linkState.Scope()
			span := contentlink.Span{Kind: contentlink.KindFile, Value: "README.md", Extra: contentlink.Extra{Raw: "README.md"}}
			msg := previewLinkRevalidatedMsg{
				Generation: m.preview.generation, WorkspaceID: m.preview.workspaceID, Scope: scope, Span: span,
				Result: termpreview.FreshLinkResult{
					Request: termpreview.FreshLinkRequest{Root: scope.Root, RawRoot: m.previewResolveRoot(), Candidate: contentlink.Pending{Kind: contentlink.KindFile, Raw: "README.md"}},
					Ref:     contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, Found: true,
				},
			}
			test.rotate(t, m)
			if cmd := m.applyPreviewLinkRevalidated(msg); cmd != nil {
				t.Fatal("stale preview activation returned a command")
			}
			if m.preview.doc != nil {
				t.Fatal("stale preview activation opened a document")
			}
		})
	}
}

func TestPreviewPreparationUsesCanonicalRootOncePerAcceptedContext(t *testing.T) {
	realRoot := t.TempDir()
	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "repo")
	if _, err := filepath.EvalSymlinks(realRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realRoot, alias); err != nil {
		t.Fatal(err)
	}
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	workspace := m.catalog[m.preview.workspaceID]
	workspace.Path = alias
	m.catalog[m.preview.workspaceID] = workspace
	m.preview.buffer = tty.NewOutputBuffer(4)
	m.preview.buffer.Update("https://example.test")
	m.PrepareTerminalLinks()
	want, err := filepath.EvalSymlinks(alias)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.preview.linkState.Scope().Root; got != filepath.Clean(want) {
		t.Fatalf("scope root = %q, want %q", got, filepath.Clean(want))
	}
}

func TestPreviewCanonicalRootContextRotatesInPlace(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	m := &Model{}
	canonicalA := m.canonicalTerminalLinkRoot(rootA)
	if canonicalA == "" || m.terminalLinkRoot != (terminalLinkRootContext{raw: filepath.Clean(rootA), root: canonicalA}) {
		t.Fatalf("first root context = %+v", m.terminalLinkRoot)
	}
	canonicalB := m.canonicalTerminalLinkRoot(rootB)
	if canonicalB == "" || m.terminalLinkRoot != (terminalLinkRootContext{raw: filepath.Clean(rootB), root: canonicalB}) {
		t.Fatalf("rotated root context = %+v", m.terminalLinkRoot)
	}
	if canonicalA == canonicalB || m.terminalLinkRoot.raw == filepath.Clean(rootA) {
		t.Fatal("old overview canonical root survived rotation")
	}
}
