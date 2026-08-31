package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
)

func terminalRekeyTree(primaryID, shellID int, shellFirst bool) *PaneNode {
	primary := &PaneNode{ID: primaryID, Kind: PaneTerminal}
	shell := &PaneNode{ID: shellID, Kind: PaneShell}
	if shellFirst {
		return &PaneNode{ID: max(primaryID, shellID) + 1, Split: &PaneSplit{Axis: SplitCols, Ratio: 50, A: shell, B: primary}}
	}
	return &PaneNode{ID: max(primaryID, shellID) + 1, Split: &PaneSplit{Axis: SplitCols, Ratio: 50, A: primary, B: shell}}
}

func TestReviewReinitDoesNotReuseFormerShellStateAsPrimary(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := New()
	p.paneRoot = terminalRekeyTree(2, 1, true)
	p.terminalPanes = nil
	p.primaryTermPane().Terminal = &tty.Model{}
	p.requestShellLeaf()
	oldShell := p.requireShellTermPane()
	if oldShell.RowAnalyzer == nil {
		t.Fatal("staged Shell leaf has no row analyzer")
	}
	oldShell.Session = "sidecar-tp-old-shell"
	oldShell.Buffer = tty.NewOutputBuffer(8)

	if err := p.Init(&plugin.Context{WorkDir: root, ProjectRoot: root}); err != nil {
		t.Fatal(err)
	}
	primary := p.primaryTermPane()
	if primary == oldShell {
		t.Fatal("reinit reused the former Shell leaf as the new primary")
	}
	if primary.Session != "" || primary.Requested {
		t.Fatalf("new primary inherited Shell state: session=%q requested=%v", primary.Session, primary.Requested)
	}
	if primary.RowAnalyzer == nil || primary.RowAnalyzer == oldShell.RowAnalyzer {
		t.Fatal("reinit did not give the replacement primary its own durable analyzer")
	}
}

func TestReviewInteractiveLeafIdentityFollowsLayoutRekey(t *testing.T) {
	for _, tc := range []struct {
		name      string
		activeOld int
		wantNew   int
		wantPanel bool
	}{
		{name: "primary", activeOld: 1, wantNew: 2},
		{name: "shell", activeOld: 2, wantNew: 1, wantPanel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &Plugin{}
			oldRoot := terminalRekeyTree(1, 2, false)
			newRoot := terminalRekeyTree(2, 1, true)
			p.paneRoot = oldRoot
			primaryModel, shellModel := &tty.Model{}, &tty.Model{}
			p.primaryTermPane().Terminal = primaryModel
			p.requestShellLeaf()
			p.requireShellTermPane().Terminal = shellModel
			primaryAnalyzer := p.primaryTermPane().RowAnalyzer
			shellAnalyzer := p.requireShellTermPane().RowAnalyzer
			if primaryAnalyzer == nil || shellAnalyzer == nil || primaryAnalyzer == shellAnalyzer {
				t.Fatal("live leaves do not own distinct row analyzers")
			}
			p.interactiveState = &InteractiveState{Active: true, LeafID: tc.activeOld}

			p.rebindTerminalPaneTree(oldRoot, newRoot)
			p.paneRoot = newRoot

			if p.interactiveState.LeafID != tc.wantNew {
				t.Fatalf("interactive leaf = %d, want rekeyed ID %d", p.interactiveState.LeafID, tc.wantNew)
			}
			if got := p.terminalPaneIsPanel(p.interactiveState.LeafID); got != tc.wantPanel {
				t.Fatalf("rekeyed leaf classified panel=%v, want %v", got, tc.wantPanel)
			}
			wantModel := primaryModel
			if tc.wantPanel {
				wantModel = shellModel
			}
			if got := p.activeInteractiveTerminal(); got != wantModel {
				t.Fatalf("active terminal = %p, want %p", got, wantModel)
			}
			if p.primaryTermPane().RowAnalyzer != primaryAnalyzer || p.requireShellTermPane().RowAnalyzer != shellAnalyzer {
				t.Fatal("layout rekey replaced a live leaf row analyzer")
			}
		})
	}
}
