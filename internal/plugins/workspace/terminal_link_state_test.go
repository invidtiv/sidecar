package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
)

func TestTerminalActivationRejectsStaleEpochRootAndTarget(t *testing.T) {
	for _, test := range []struct {
		name   string
		rotate func(*testing.T, *Plugin)
	}{
		{name: "epoch", rotate: func(_ *testing.T, p *Plugin) { p.ctx.Epoch++ }},
		{name: "root", rotate: func(t *testing.T, p *Plugin) { p.worktrees[0].Path = t.TempDir() }},
		{name: "target", rotate: func(_ *testing.T, p *Plugin) { p.worktrees[0].Agent.TmuxPane = "%new-target" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p, _, _ := projectTerminalFixture(t)
			context := p.terminalLinkSurfaceContext(false)
			link := terminalLink{Kind: contentlink.KindFile, Value: "README.md", Raw: "README.md", Root: context.root}
			msg := terminalLinkRevalidatedMsg{
				Epoch: p.ctx.Epoch, Context: context, Link: link,
				Result: termpreview.FreshLinkResult{
					Request: termpreview.FreshLinkRequest{Root: context.root, RawRoot: context.rawRoot, Candidate: contentlink.Pending{Kind: contentlink.KindFile, Raw: "README.md"}},
					Ref:     contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, Found: true,
				},
			}
			test.rotate(t, p)
			if cmd := p.applyTerminalLinkRevalidated(msg); cmd != nil {
				t.Fatal("stale terminal activation returned a command")
			}
			if p.paneRoot != nil && p.paneRoot.Kind != PaneTerminal {
				t.Fatalf("stale terminal activation changed pane kind to %v", p.paneRoot.Kind)
			}
		})
	}
}

func TestTerminalActivationRejectsReplacedPreparedBuffer(t *testing.T) {
	for _, test := range []struct {
		name      string
		termPanel bool
	}{
		{name: "primary"},
		{name: "panel", termPanel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# ready\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			p := docPaneTestPlugin(t, root, true)
			buffer := p.shells[0].Agent.OutputBuf
			if test.termPanel {
				p.termPanelVisible = true
				p.termPanelSession, p.termPanelPaneID = "panel", "%panel"
				buffer = tty.NewOutputBuffer(4)
				p.termPanelOutput = buffer
			}
			buffer.Update("README.md")
			state, links := preparedTerminalLineForTest(t, p, test.termPanel, buffer, "README.md")
			if len(links) != 1 || links[0].Kind != contentlink.KindFile {
				t.Fatalf("prepared links = %#v", links)
			}
			if test.termPanel {
				p.panelLinkState = state
			} else {
				p.primaryLinkState = state
			}
			context := p.terminalLinkSurfaceContext(test.termPanel)
			cmd, ok := p.revalidateTerminalLink(links[0], context, test.termPanel)
			if !ok || cmd == nil {
				t.Fatal("prepared file did not start activation revalidation")
			}

			// Recreate the terminal model with the same surface and target IDs.
			// Only the LinkScope buffer identity distinguishes this state from the
			// one the click was prepared against.
			replacement := tty.NewOutputBuffer(4)
			replacement.Update("README.md")
			if test.termPanel {
				p.termPanelOutput = replacement
			} else {
				p.shells[0].Agent.OutputBuf = replacement
			}
			p.PrepareTerminalLinks()

			result := cmd()
			msg, ok := result.(terminalLinkRevalidatedMsg)
			if !ok {
				t.Fatalf("revalidation result = %T", result)
			}
			if msg.Scope.Buffer != buffer {
				t.Fatal("revalidation message did not preserve the full prepared scope")
			}
			if current := func() termpreview.LinkScope {
				if test.termPanel {
					return p.panelLinkState.Scope()
				}
				return p.primaryLinkState.Scope()
			}(); current.Buffer != replacement {
				t.Fatalf("current prepared buffer = %p, want replacement %p", current.Buffer, replacement)
			}
			if next := p.applyTerminalLinkRevalidated(msg); next != nil {
				t.Fatal("stale buffer result activated a file")
			}
			if len(p.docs) != 0 {
				t.Fatal("stale buffer result opened a document pane")
			}
		})
	}
}

func TestTerminalActivationAcceptsUnchangedPreparedScope(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := docPaneTestPlugin(t, root, true)
	buffer := p.shells[0].Agent.OutputBuf
	buffer.Update("README.md")
	state, links := preparedTerminalLineForTest(t, p, false, buffer, "README.md")
	p.primaryLinkState = state
	cmd, ok := p.revalidateTerminalLink(links[0], p.terminalLinkSurfaceContext(false), false)
	if !ok || cmd == nil {
		t.Fatal("prepared file did not start activation revalidation")
	}
	msg, ok := cmd().(terminalLinkRevalidatedMsg)
	if !ok {
		t.Fatalf("revalidation result has unexpected type")
	}
	if next := p.applyTerminalLinkRevalidated(msg); next == nil {
		t.Fatal("unchanged prepared scope did not activate")
	}
}

func TestTerminalCanonicalContextsStayBoundedAndResetOnInit(t *testing.T) {
	rootA, rootB := t.TempDir(), t.TempDir()
	p := docPaneTestPlugin(t, rootA, true)
	p.shells[0].WorkDir = rootA
	p.termPanelSession, p.termPanelPaneID = "panel", "%panel"

	primaryA := p.terminalLinkSurfaceContext(false)
	panelA := p.terminalLinkSurfaceContext(true)
	if !primaryA.ok || !panelA.ok {
		t.Fatalf("initial contexts: primary=%+v panel=%+v", primaryA, panelA)
	}
	p.shells[0].WorkDir = rootB
	primaryB := p.terminalLinkSurfaceContext(false)
	if !primaryB.ok || primaryB.rawRoot != filepath.Clean(rootB) || p.primaryLinkContext != primaryB {
		t.Fatalf("rotated primary context = %+v cached=%+v", primaryB, p.primaryLinkContext)
	}
	if p.panelLinkContext != panelA {
		t.Fatalf("primary rotation changed the independent panel slot: %+v", p.panelLinkContext)
	}
	if p.primaryLinkContext == primaryA {
		t.Fatal("old primary canonical context survived rotation")
	}

	p.primaryLinkState = termpreview.NewLinkCoordinator(nil).Prepare(termpreview.LinkPrepare{Scope: termpreview.LinkScope{Root: primaryB.root}})
	p.panelLinkState = termpreview.NewLinkCoordinator(nil).Prepare(termpreview.LinkPrepare{Scope: termpreview.LinkScope{Root: panelA.root}})
	if err := p.Init(&plugin.Context{Epoch: 18, WorkDir: rootB, ProjectRoot: rootB}); err != nil {
		t.Fatal(err)
	}
	if p.primaryLinkContext != (terminalLinkSurfaceContext{}) || p.panelLinkContext != (terminalLinkSurfaceContext{}) {
		t.Fatalf("reinit retained canonical contexts: primary=%+v panel=%+v", p.primaryLinkContext, p.panelLinkContext)
	}
	if p.primaryLinkState.Scope() != (termpreview.LinkScope{}) || p.panelLinkState.Scope() != (termpreview.LinkScope{}) {
		t.Fatal("reinit retained prepared terminal link state")
	}
}
